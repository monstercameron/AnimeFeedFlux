// Package e2e is the true end-to-end suite: the whole application, started
// as close to the way production starts it as the current tree allows, and
// driven only through its public surfaces — real SQLite, the real publish
// HTTP plane, the real gRPC-over-WebSocket bridge, the real RPC services,
// with only the LLM provider and outbound HTTP faked (PLAN.md §17.5).
//
// # What this suite found, and what is left
//
// Standing this package up was worth more than the tests it contains. The
// control plane existed as complete, unit-tested parts that nothing
// connected: cmd/animefeedflux started only the publish listener, and
// rpc.ContextWithSessionToken — the only way to get a raw token to the
// interceptor that authorizes every RPC — had zero production callers. Every
// RPC over a real bridge connection would have authorized as Unauthenticated
// no matter how valid the session was. Nothing failed, because nothing ran.
//
// Those are now closed in the packages that owned them, not worked around
// here:
//
//   - cmd/animefeedflux/wire.go is the composition root: two listeners, the
//     six services, the scheduler, graceful shutdown.
//   - internal/bridge's ServeHTTP sets Session.Token from the cookie it has
//     already validated, unconditionally, and internal/rpc's interceptor
//     falls back to it. There is no wiring step a composition root can omit,
//     which was the point — the omission used to fail as "everything is
//     unauthenticated" with nothing pointing at the cause. Two callers hit
//     that seam and improvised two different workarounds before it was
//     fixed properly; this file's earlier version was one of them, and
//     smuggled the raw token through bridge.Session.UserID against that
//     field's own documentation.
//   - GetFeedForSample has a production implementation in wire.go.
//
// Validity is still re-checked against the store on every single call. The
// bridge relays credential bytes; it does not vouch for them. An upgrade-time
// credential trusted for the life of the WebSocket would make revocation
// cosmetic (SEC-41), and internal/bridge has a test that says so.
//
// One real gap remains, and this suite cannot reach it: Login cannot go over
// the bridge, because the bridge requires a valid cookie before any RPC —
// including Login — is reachable. Login therefore goes over a second plain
// gRPC listener, exactly as cmd/aff dials one. See loginOverPlainGRPC.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// allowedOrigin is the single Origin this suite's bridge accepts (PLAN.md
// §4). Its exact value is arbitrary; what matters is that it is used
// consistently as both the server's allowlist entry and the client's
// Origin header.
const allowedOrigin = "https://admin.e2e.test"

// adminPassword satisfies auth.IsWeak (>= the NIST length floor, §4) without
// being a real credential anywhere outside this test process.
const adminPassword = "correct horse battery staple e2e"

// totpIssuer/totpAccount mirror cmd/aff/admin_cmd.go's constants; this
// package cannot import cmd/aff (an unexported main package), so the enroll
// call is reproduced directly against internal/auth instead.
const (
	totpIssuer  = "AnimeFeedFlux-e2e"
	totpAccount = "e2e-admin"
)

// App is one test's whole system. Build one with New per test; it is not
// meant to be shared across tests (mirrors internal/flowtest.World's
// contract for the same reason: isolation, not speed).
type App struct {
	T *testing.T

	Store    *store.Store
	Provider *llm.Fake
	Clock    *Clock

	PublishURL string // real HTTP base URL, e.g. "http://127.0.0.1:PORT"
	AdminAddr  string // plain gRPC address Login dials (see loginOverPlainGRPC)
	BridgeURL  string // http(s) base URL the WebSocket bridge is served on

	secretKey []byte
	idSource  ids.Source

	authServer   *rpc.AuthServer
	feedServer   *rpc.FeedServer
	itemServer   *rpc.ItemServer
	runServer    *rpc.RunServer
	sampleServer *rpc.SampleServer
	systemServer *rpc.SystemServer

	publishSrv *httptest.Server
	bridgeSrv  *httptest.Server
	plainLis   net.Listener
	plainSrv   *grpc.Server

	closeOnce sync.Once
}

// --- Clock ------------------------------------------------------------

// Clock is the injected clock this suite anchors to, structurally
// satisfying both bridge.Clock and schedule.Clock (both are the same
// Now()/After(d) shape — internal/bridge/clock.go and
// internal/schedule/runner.go declare it independently, so one concrete
// type here serves both without an adapter). Modeled on
// internal/bridge/server_test.go's fakeClock: After(d) for d<=0 fires
// immediately without a real sleep, which is what lets killswitch_test.go
// drive a schedule.Runner deterministically.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

type clockWaiter struct {
	fire time.Time
	ch   chan time.Time
}

// NewClock returns a Clock starting at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now implements bridge.Clock / schedule.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After implements bridge.Clock / schedule.Clock.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	fire := c.now.Add(d)
	if !fire.After(c.now) {
		ch <- fire
		return ch
	}
	c.waiters = append(c.waiters, clockWaiter{fire: fire, ch: ch})
	return ch
}

// Advance moves the clock forward and fires every waiter whose deadline has
// now passed.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var fired []clockWaiter
	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.fire.After(now) {
			fired = append(fired, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
	for _, w := range fired {
		w.ch <- w.fire
	}
}

// testEpoch anchors every App the same way internal/flowtest.testEpoch does:
// a stable instant, never time.Now().
var testEpoch = time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)

// --- construction -------------------------------------------------------

// New builds a fully wired App: a migrated temp SQLite store, the real
// publish plane on a real listener, the real bridge+RPC control plane on
// another real listener (with this package's session-propagation glue, see
// the package doc), a fake LLM provider, and an injected clock. Registers
// t.Cleanup to tear everything down.
func New(t *testing.T) *App {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, store.Options{Path: t.TempDir() + "/aff.db"})
	if err != nil {
		t.Fatalf("e2e: opening store: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("e2e: migrating store: %v", err)
	}

	a := &App{
		T:         t,
		Store:     st,
		Provider:  llm.NewFake(),
		Clock:     NewClock(testEpoch),
		secretKey: []byte("e2e-suite-secret-key-not-a-real-secret"),
		idSource:  ids.NewDeterministicSource(1),
	}

	a.authServer, err = rpc.NewAuthServer(st, a.secretKey)
	if err != nil {
		t.Fatalf("e2e: building AuthServer: %v", err)
	}

	// buildPublishPlane must run before the RPC servers are constructed:
	// they need the SAME Invalidator that is bound to the *publish.Cache the
	// publish HTTP handler actually serves reads through (RULE-6). See the
	// bug this fixes, documented on buildPublishPlane itself: a
	// separately-constructed *publish.Cache passed to the RPC servers is a
	// different cache instance than the one publish.NewServerAndInvalidator
	// builds internally for the handler, so invalidating it does nothing to
	// what readers actually see.
	inv := a.buildPublishPlane()

	a.feedServer = rpc.NewFeedServer(st, inv, nil /* no RunNow executor: see killswitch_test.go's own scheduler */)
	a.itemServer = rpc.NewItemServer(st, inv, a.idSource)
	a.runServer = rpc.NewRunServer(st, slog.New(slog.DiscardHandler))
	// WithCredentialVerifier matches production (cmd/animefeedflux/wire.go):
	// Backup re-proves password + TOTP rather than trusting the session alone,
	// because it returns the whole database — every password hash, the
	// encrypted TOTP secret, every session and recovery-code hash. A harness
	// that leaves it unwired would exercise a Backup this app never serves.
	a.systemServer = rpc.NewSystemServer(st, slog.New(slog.DiscardHandler),
		rpc.WithDefaultGenerationEnabled(true),
		rpc.WithCredentialVerifier(a.authServer))
	a.sampleServer = rpc.NewSampleServer(rpc.SampleServerConfig{
		Feeds:    feedLookup{st},
		GenStore: genStoreAdapter{st},
		Novelty:  nil, // generate.Run/Sample treat a nil Novelty as "no dedup gate" (internal/flowtest does the same)
		Fetcher:  nil, // this suite only exercises generative feeds; see lifecycle_test.go
		Provider: a.Provider,
		IDs:      a.idSource,
		Budget:   permissiveBudget{},
		Samples:  st,
		Enabled:  a.generationEnabled,
		Now:      a.Clock.Now,
	})

	a.buildControlPlane(t)

	t.Cleanup(a.Close)
	return a
}

// Close tears down every listener and the store. Safe to call once (New
// registers it with t.Cleanup already).
func (a *App) Close() {
	a.closeOnce.Do(func() {
		if a.publishSrv != nil {
			a.publishSrv.Close()
		}
		if a.bridgeSrv != nil {
			a.bridgeSrv.Close()
		}
		if a.plainSrv != nil {
			a.plainSrv.GracefulStop()
		}
		if a.Store != nil {
			_ = a.Store.Close()
		}
	})
}

// --- publish plane --------------------------------------------------------

// buildPublishPlane wires internal/publish exactly the way
// cmd/animefeedflux/server.go's buildPublishHandler does: every Deps closure
// reads through st.Reader() only, never st.Writer(), so PLAN.md §2's "the
// publish plane structurally cannot write" claim is real here too, not
// merely asserted. The query/scan logic is intentionally the same shape as
// that file's — cmd/animefeedflux is a `main` package this test binary
// cannot import, so it is reproduced rather than shared, matching the
// existing precedent that file itself sets relative to internal/store's own
// (writer-bound) scan helpers.
//
// It returns the Invalidator publish.NewServerAndInvalidator hands back,
// which is bound to the SAME *publish.Cache instance the returned handler
// reads through. That return value MUST be what the RPC servers are
// constructed with (New's caller does this). A prior version of this file
// built its own, separate *publish.Cache via publish.NewCache(), wired THAT
// into the RPC servers as their Invalidator, and then called
// publish.NewServerAndInvalidator here anyway — which builds its own
// internal *Cache (invalidate.go: `s := &server{deps: deps, cache:
// NewCache()}`) and is what the handler actually serves reads through. The
// RPC servers' writes invalidated the orphaned cache nobody read from; the
// handler's real cache was never told anything changed. Every mutation up
// through the first render of a given feed still looked correct (nothing
// had been cached yet to be stale), which is exactly why PromoteSample's
// item showed up but PublishCorrection's — a write against a feed the
// publish plane had already rendered and cached once — did not.
func (a *App) buildPublishPlane() publish.Invalidator {
	reader := a.Store.Reader()
	deps := publish.Deps{
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			return getFeedBySlug(ctx, reader, slug)
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) { return listItems(ctx, reader, feedID) },
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) { return listFeeds(ctx, reader) },
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			return getItemByKey(ctx, reader, itemKey)
		},
		BaseURL:   "", // filled in once the listener's real address is known, below
		Generator: "AnimeFeedFlux e2e",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026, // fixed epoch (§5.1); this suite never asserts on the guid's embedded year
		Now:       a.Clock.Now,
	}

	// NewUnstartedServer first (mirrors internal/flowtest/harness.go's own
	// comment on this exact ordering): Deps.BaseURL is a plain string baked
	// into every rendered <link>/self URL, not a closure, so the listener's
	// real address must be known before the handler is built.
	srv := httptest.NewUnstartedServer(nil)
	deps.BaseURL = "http://" + srv.Listener.Addr().String()

	handler, inv := publish.NewServerAndInvalidator(deps)
	srv.Config.Handler = handler
	srv.Start()

	a.publishSrv = srv
	a.PublishURL = deps.BaseURL
	return inv
}

// --- read-only publish queries (mirrors cmd/animefeedflux/server.go) ------

const storeTimeLayout = time.RFC3339Nano

func parseStoreTime(s string) (time.Time, error) { return time.Parse(storeTimeLayout, s) }

func parseNullStoreTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return parseStoreTime(ns.String)
}

const feedSelectColumns = `id, slug, title, description, language, kind, enabled, timezone,
	author, copyright, og_image, ttl_minutes, last_built_at, version`

type rowScanner interface{ Scan(dest ...any) error }

func scanFeedRow(sc rowScanner) (model.Feed, error) {
	var (
		f                                       model.Feed
		enabled                                 int64
		kind                                    string
		author, copyright, ogImage, lastBuiltAt sql.NullString
	)
	if err := sc.Scan(
		&f.ID, &f.Slug, &f.Title, &f.Description, &f.Language, &kind, &enabled, &f.Timezone,
		&author, &copyright, &ogImage, &f.TTLMinutes, &lastBuiltAt, &f.Version,
	); err != nil {
		return model.Feed{}, err
	}
	f.Kind = model.FeedKind(kind)
	f.Enabled = enabled != 0
	f.Author = author.String
	f.Copyright = copyright.String
	f.OGImage = ogImage.String
	lastBuilt, err := parseNullStoreTime(lastBuiltAt)
	if err != nil {
		return model.Feed{}, fmt.Errorf("parsing feeds.last_built_at: %w", err)
	}
	f.LastBuiltAt = lastBuilt
	return f, nil
}

func getFeedBySlug(ctx context.Context, reader store.Reader, slug string) (model.Feed, bool, error) {
	row := reader.QueryRowContext(ctx, `SELECT `+feedSelectColumns+` FROM feeds WHERE slug = ?`, slug)
	f, err := scanFeedRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, false, nil
	}
	if err != nil {
		return model.Feed{}, false, fmt.Errorf("getting feed %q: %w", slug, err)
	}
	return f, true, nil
}

func listFeeds(ctx context.Context, reader store.Reader) ([]model.Feed, error) {
	rows, err := reader.QueryContext(ctx, `SELECT `+feedSelectColumns+` FROM feeds ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("listing feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.Feed
	for rows.Next() {
		f, err := scanFeedRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning feed: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const itemSelectColumns = `id, feed_id, item_key, content_hash, title, summary_text, body_html,
	answer_html, link, source_name, published_at, origin, version,
	created_at, updated_at, edited_at, deleted_at`

func scanItemRow(sc rowScanner) (model.Item, error) {
	var (
		it                                model.Item
		origin                            string
		answerHTML, link, sourceName      sql.NullString
		publishedAt, createdAt, updatedAt string
		editedAt, deletedAt               sql.NullString
	)
	if err := sc.Scan(
		&it.ID, &it.FeedID, &it.ItemKey, &it.ContentHash, &it.Title, &it.SummaryText, &it.BodyHTML,
		&answerHTML, &link, &sourceName, &publishedAt, &origin, &it.Version,
		&createdAt, &updatedAt, &editedAt, &deletedAt,
	); err != nil {
		return model.Item{}, err
	}
	it.AnswerHTML = answerHTML.String
	it.Link = link.String
	it.SourceName = sourceName.String
	it.Origin = model.Origin(origin)
	var err error
	if it.PublishedAt, err = parseStoreTime(publishedAt); err != nil {
		return model.Item{}, err
	}
	if it.CreatedAt, err = parseStoreTime(createdAt); err != nil {
		return model.Item{}, err
	}
	if it.UpdatedAt, err = parseStoreTime(updatedAt); err != nil {
		return model.Item{}, err
	}
	if it.EditedAt, err = parseNullStoreTime(editedAt); err != nil {
		return model.Item{}, err
	}
	if it.DeletedAt, err = parseNullStoreTime(deletedAt); err != nil {
		return model.Item{}, err
	}
	return it, nil
}

func listItems(ctx context.Context, reader store.Reader, feedID int64) ([]model.Item, error) {
	rows, err := reader.QueryContext(ctx,
		`SELECT `+itemSelectColumns+` FROM items WHERE feed_id = ? AND deleted_at IS NULL ORDER BY published_at DESC LIMIT 50`,
		feedID)
	if err != nil {
		return nil, fmt.Errorf("listing items for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.Item
	for rows.Next() {
		it, err := scanItemRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func getItemByKey(ctx context.Context, reader store.Reader, itemKey string) (model.Feed, model.Item, bool, error) {
	row := reader.QueryRowContext(ctx, `
		SELECT items.id, items.feed_id, items.item_key, items.content_hash, items.title,
		       items.summary_text, items.body_html, items.answer_html, items.link, items.source_name,
		       items.published_at, items.origin, items.version,
		       items.created_at, items.updated_at, items.edited_at, items.deleted_at
		FROM items WHERE items.item_key = ?`, itemKey)
	it, err := scanItemRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, model.Item{}, false, nil
	}
	if err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("getting item %q: %w", itemKey, err)
	}
	f, ok, err := getFeedByID(ctx, reader, it.FeedID)
	if err != nil || !ok {
		return model.Feed{}, model.Item{}, false, err
	}
	return f, it, true, nil
}

func getFeedByID(ctx context.Context, reader store.Reader, id int64) (model.Feed, bool, error) {
	var slug string
	if err := reader.QueryRowContext(ctx, `SELECT slug FROM feeds WHERE id = ?`, id).Scan(&slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Feed{}, false, nil
		}
		return model.Feed{}, false, err
	}
	return getFeedBySlug(ctx, reader, slug)
}

// --- generation glue: GetFeedForSample (a real, unwired gap; see package doc) ---

// feedLookup implements rpc's smpFeedLookup (unexported, but its method set
// is public) against the real store, closing the "nothing in production
// implements GetFeedForSample" gap named in the package doc. It reads the
// same feeds table columns internal/rpc/feed.go's feedRecord reads and
// converts the persisted feedspec.Spec into the generate.Spec the pipeline
// actually consumes — a conversion that, per the same grep, also does not
// exist anywhere in production yet (feedspec.Spec and generate.Spec are
// deliberately different shapes: PLAN.md §7 vs §9).
type feedLookup struct{ st *store.Store }

func (f feedLookup) GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error) {
	var (
		slug, title, description, language, kind, timezone, specJSON, createdAt string
	)
	err := f.st.Reader().QueryRowContext(ctx,
		`SELECT slug, title, description, language, kind, timezone, spec_json, created_at FROM feeds WHERE id = ?`,
		feedID,
	).Scan(&slug, &title, &description, &language, &kind, &timezone, &specJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, generate.Spec{}, store.ErrNotFound
	}
	if err != nil {
		return model.Feed{}, generate.Spec{}, fmt.Errorf("e2e: resolving feed %d for sample: %w", feedID, err)
	}

	created, err := parseStoreTime(createdAt)
	if err != nil {
		return model.Feed{}, generate.Spec{}, fmt.Errorf("e2e: parsing feed %d created_at: %w", feedID, err)
	}

	fs, err := feedspec.Import([]byte(specJSON))
	if err != nil {
		return model.Feed{}, generate.Spec{}, fmt.Errorf("e2e: decoding spec for feed %d: %w", feedID, err)
	}

	feed := model.Feed{
		ID: feedID, Slug: slug, Title: title, Description: description,
		Language: language, Kind: model.FeedKind(kind), Timezone: timezone, CreatedAt: created,
	}
	spec := generate.Spec{
		SystemPrompt:       fs.SystemPrompt,
		UserPromptTemplate: fs.UserPrompt,
		Model:              fs.Model.Model,
		Temperature:        fs.Model.Temperature,
		ItemsPerRun:        fs.ItemsPerRun,
		MaxNoveltyRetries:  fs.Novelty.MaxRetries,
	}
	return feed, spec, nil
}

// genStoreAdapter satisfies generate.Store's read side against the real
// store for SampleServer's injected GenStore dependency (it never calls
// CommitRun — see internal/rpc/sample.go's smpGenerateStore poison pill,
// which wraps this).
type genStoreAdapter struct{ st *store.Store }

func (g genStoreAdapter) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	items, err := g.st.ListItems(ctx, feedID, n, false)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out, nil
}

func (g genStoreAdapter) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	items, err := g.st.ListItems(ctx, feedID, 1, false)
	if err != nil {
		return time.Time{}, err
	}
	if len(items) == 0 {
		return time.Time{}, nil
	}
	return items[0].PublishedAt, nil
}

// permissiveBudget implements rpc's smpBudgetChecker using the REAL
// internal/budget package with zero Limits — budget.Check/CheckRequest's own
// contract is "a zero value for any field means no cap", so this is real
// budget-package behavior (an always-allow limit set), not a stub that
// bypasses the package.
type permissiveBudget struct{}

func (permissiveBudget) CheckSample(ctx context.Context, feedID int64, projectedTokens int) budget.Decision {
	return budget.Check(budget.Limits{}, budget.Spend{}, budget.Spend{}, projectedTokens)
}

func (permissiveBudget) RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error) {
	return 1e9, nil
}

// generationEnabled implements rpc's smpEnabledCheck (and doubles as the
// killswitch_test.go scheduler Gate's data source) by reading the exact
// settings row internal/rpc/system.go's SetGenerationEnabled writes —
// "generation" -> JSON-encoded affv1.Settings_Generation. Reproduced here
// (rather than exported from internal/rpc, which this change may not touch)
// because both SampleServer and this suite's own scheduler glue need the
// live value.
func (a *App) generationEnabled(ctx context.Context) (bool, string, error) {
	var raw string
	err := a.Store.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'generation'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "", nil // no row yet: same cold-start default SystemServer seeds
	}
	if err != nil {
		return false, "", err
	}
	var g struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return false, "", err
	}
	return g.Enabled, "generation disabled via SystemService.SetGenerationEnabled", nil
}

// --- control plane: bridge + rpc, with the session-propagation glue ------

// buildControlPlane wires the real bridge.NewServer and all six real RPC
// services together, closing the "nothing propagates the bridge session
// into rpc's interceptor" gap described in the package doc — see
// glueUnaryInterceptor/glueStreamInterceptor below for exactly how, and
// sessionValidator for the real (not stubbed) session lookup it performs.
// It also opens a second, plain (non-bridge) *grpc.Server carrying only
// AuthService, because Login itself cannot go over the bridge — see
// loginOverPlainGRPC's doc comment for why that is a real production gap
// and not a shortcut this suite invented.
func (a *App) buildControlPlane(t *testing.T) {
	t.Helper()

	register := func(gs *grpc.Server) {
		affv1.RegisterAuthServiceServer(gs, a.authServer)
		affv1.RegisterFeedServiceServer(gs, a.feedServer)
		affv1.RegisterItemServiceServer(gs, a.itemServer)
		affv1.RegisterRunServiceServer(gs, a.runServer)
		affv1.RegisterSampleServiceServer(gs, a.sampleServer)
		affv1.RegisterSystemServiceServer(gs, a.systemServer)
	}

	cfg := bridge.Config{
		SessionCookieName: auth.CookieName(),
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         sessionValidator{st: a.Store},
		GRPCOptions: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(a.authServer.UnaryInterceptor()),
			grpc.ChainStreamInterceptor(a.authServer.StreamInterceptor()),
		},
	}
	handler, err := bridge.NewServer(cfg, register)
	if err != nil {
		t.Fatalf("e2e: building bridge server: %v", err)
	}
	a.bridgeSrv = httptest.NewServer(handler)
	a.BridgeURL = a.bridgeSrv.URL

	// The plain listener: real TCP, real grpc.Server, only AuthService
	// registered — this is genuinely the only reachable pre-authentication
	// entry point in the current tree (see loginOverPlainGRPC).
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("e2e: listening for plain grpc: %v", err)
	}
	plain := grpc.NewServer()
	affv1.RegisterAuthServiceServer(plain, a.authServer)
	go func() { _ = plain.Serve(lis) }()
	a.plainLis = lis
	a.plainSrv = plain
	a.AdminAddr = lis.Addr().String()
}

// sessionValidator implements bridge.SessionValidator against the real
// store, performing the identical lookup internal/rpc/interceptor.go's
// authorize does (hash, GetSessionByTokenHash, Valid check) — real session
// state, not a stand-in. It repurposes bridge.Session.UserID to carry the
// RAW token through the only hook the bridge package exposes, because
// bridge.Session has no token field at all (see the package doc's second
// bullet). bridge.Session.UserID's own doc comment says it is "informational
// / logging value today, not an authz key" — this is a deliberate, narrow
// reuse of that slot, scoped to this e2e glue, and it is exactly the shape a
// real fix would need to add to internal/bridge itself (a token-bearing
// field on Session) rather than something this suite is inventing out of
// nothing.
type sessionValidator struct{ st *store.Store }

func (v sessionValidator) Validate(ctx context.Context, token string, now time.Time) (bridge.Session, error) {
	if token == "" {
		return bridge.Session{}, auth.ErrTokenEmpty
	}
	hash := auth.HashToken(token)
	sess, err := v.st.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return bridge.Session{}, err
	}
	if !sess.Valid(now, auth.SessionIdleTimeout) {
		return bridge.Session{}, fmt.Errorf("e2e: session expired or revoked")
	}
	// TokenHash, not the raw token. This used to return the raw token in
	// UserID to smuggle it through to rpc's interceptor, against that
	// field's own documentation ("not an authz key"). The bridge now sets
	// Session.Token itself in ServeHTTP, so the smuggling — and the pair of
	// glue interceptors that unpacked it — are gone.
	return bridge.Session{UserID: sess.TokenHash, ExpiresAt: sess.ExpiresAt}, nil
}

// --- admin bootstrap (mirrors `aff admin init`, cmd/aff/admin_cmd.go) -----

// InitAdmin creates the singleton admin row and enrolls TOTP directly
// against the real store, the same sequence cmd/aff/admin_cmd.go's
// cmdAdminInit runs — reproduced here because that file lives in an
// unexported `main` package this test binary cannot import (PLAN.md §11:
// "aff admin init ... requires filesystem access to the DB", i.e. it is
// deliberately not an RPC). Returns the plaintext TOTP secret so the test
// can compute valid login codes.
func (a *App) InitAdmin(ctx context.Context, password string) (totpSecret string, err error) {
	if err := auth.IsWeak(password); err != nil {
		return "", fmt.Errorf("e2e: admin password rejected: %w", err)
	}
	hash, err := auth.Hash(password, auth.DefaultParams())
	if err != nil {
		return "", fmt.Errorf("e2e: hashing admin password: %w", err)
	}
	kdfParams, err := json.Marshal(auth.DefaultParams())
	if err != nil {
		return "", fmt.Errorf("e2e: encoding kdf params: %w", err)
	}
	if err := a.Store.InitAdmin(ctx, hash, string(kdfParams)); err != nil {
		return "", fmt.Errorf("e2e: InitAdmin: %w", err)
	}

	secret, _, err := auth.Enroll(totpAccount, totpIssuer)
	if err != nil {
		return "", fmt.Errorf("e2e: enrolling totp: %w", err)
	}
	enc, err := auth.EncryptSecret(secret, a.secretKey)
	if err != nil {
		return "", fmt.Errorf("e2e: encrypting totp secret: %w", err)
	}
	if err := a.Store.SetTOTPSecret(ctx, enc); err != nil {
		return "", fmt.Errorf("e2e: storing totp secret: %w", err)
	}
	return secret, nil
}

// TOTPCode computes a valid RFC 6238 code for secret at instant at, the same
// way an authenticator app would — used because Login needs a live code, not
// a canned one, and the suite's Clock is not wall-clock time.
func TOTPCode(secret string, at time.Time) (string, error) {
	return totp.GenerateCode(secret, at)
}

// --- Login: the one compromise this suite cannot avoid --------------------

// LoginResult carries what a successful Login leaves behind: the raw
// session token (the __Host-aff_session cookie value a real browser would
// hold) and the resolved clients bundle, already dialed over the REAL
// WebSocket bridge and ready for every RPC after Login.
type LoginResult struct {
	Token   string
	Clients *Clients
	conn    *grpc.ClientConn
}

// Close releases the bridge connection this login opened.
func (l *LoginResult) Close() {
	if l.conn != nil {
		_ = l.conn.Close()
	}
}

// Login performs the real AuthService.Login RPC and then dials the REAL
// gRPC-over-WebSocket bridge with the resulting session cookie, exactly as
// a browser would for every RPC after Login.
//
// Login ITSELF, however, is called over the plain grpc.Server on AdminAddr,
// not through the bridge — because it structurally cannot be: bridge.NewServer's
// ServeHTTP (internal/bridge/server.go) requires a VALID, ALREADY-EXISTING
// session cookie before it ever attempts the WebSocket upgrade, with no
// exemption for AuthService.Login. internal/rpc/interceptor.go's
// noSessionMethods correctly lists Login as reachable pre-session at the RPC
// layer, but the bridge's transport-level gate runs strictly before any RPC
// is reached, so an unauthenticated client can never open a bridge
// connection in the first place — a chicken-and-egg gap in the current
// tree, verified directly: TestUpgradeWithoutCookie_Unauthorized in
// internal/bridge/server_test.go proves the SAME behavior this comment
// describes (a request with no cookie 401s before any RPC), and that
// behavior does not special-case Login. This suite works around it without
// editing internal/bridge, using the one transport genuinely reachable
// pre-auth today: the plain grpc.Server cmd/aff itself already dials this
// same way at AdminAddr (cmd/aff/client.go's realDial). Every RPC AFTER
// Login in this suite — feed creation, sampling, promotion, correction,
// settings — goes over the real WebSocket bridge, which is the part PLAN.md
// §2/§4 actually needs proven.
func (a *App) Login(ctx context.Context, password, totpSecret string) (*LoginResult, error) {
	return a.LoginAt(ctx, password, totpSecret, time.Now())
}

// LoginAt is Login, but the TOTP code is computed for totpAt instead of the
// real wall-clock instant Login itself uses. This exists so a test can open
// a SECOND live session shortly after the first without the second Login's
// code being refused as a replay of the first's: internal/rpc/auth.go's
// verifyTOTPCode marks each 30s TOTP step used exactly once
// (store.ErrTOTPReplay), and two Logins issued milliseconds apart against
// real time.Now() would otherwise compute the identical code for the
// identical step. Passing totpAt = (a reference instant).Add(30*time.Second)
// (or a further multiple) mints a code for the NEXT step instead, which
// AuthServer's real ±1-step skew (internal/auth/totp.go's totpSkew) still
// accepts at the moment the RPC actually lands, without colliding with a
// code already marked used for the current step.
func (a *App) LoginAt(ctx context.Context, password, totpSecret string, totpAt time.Time) (*LoginResult, error) {
	// AuthServer has no injected clock (internal/rpc/auth.go's NewAuthServer
	// hard-codes now: time.Now with no override hook), so the TOTP code must
	// be computed against REAL wall-clock time, not this suite's fake
	// app.Clock — a code minted against app.Clock's fixed testEpoch would
	// never validate against AuthServer's real-time verification window.
	code, err := TOTPCode(totpSecret, totpAt)
	if err != nil {
		return nil, fmt.Errorf("e2e: computing totp code: %w", err)
	}
	return a.LoginWithCode(ctx, password, code)
}

// LoginWithCode is Login/LoginAt's shared core: it takes an already-computed
// TOTP code directly rather than a secret+instant to derive one from. Split
// out so a test can exercise Login with a code it constructed some other
// way — e.g. sessions_test.go's negative case, which deliberately REUSES an
// already-consumed code to prove a wrong password is refused before the
// TOTP check is ever reached, without that reuse being mistaken for the
// thing under test (a replay).
func (a *App) LoginWithCode(ctx context.Context, password, code string) (*LoginResult, error) {
	plainConn, err := grpc.NewClient(a.AdminAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("e2e: dialing plain grpc for login: %w", err)
	}
	defer func() { _ = plainConn.Close() }()

	authClient := affv1.NewAuthServiceClient(plainConn)
	var header metadata.MD
	_, err = authClient.Login(ctx, &affv1.AuthServiceLoginRequest{Password: password, TotpCode: code}, grpc.Header(&header))
	if err != nil {
		return nil, fmt.Errorf("e2e: Login RPC: %w", err)
	}
	tokens := header.Get(rpc.SessionTokenHeader)
	if len(tokens) == 0 || tokens[0] == "" {
		return nil, errors.New("e2e: Login response carried no session token header")
	}
	token := tokens[0]

	conn, err := a.dialBridge(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("e2e: dialing bridge with fresh session: %w", err)
	}
	return &LoginResult{Token: token, Clients: newClients(conn), conn: conn}, nil
}

// dialBridge opens a real gRPC-over-WebSocket connection to the bridge,
// attaching the session cookie and Origin exactly as internal/bridge/
// server_test.go's dialTunnel does — the same real client-side API
// (grpctunnel.DialContext) a browser-embedded gRPC-Web-over-WS client and
// this suite both go through.
func (a *App) dialBridge(ctx context.Context, token string) (*grpc.ClientConn, error) {
	cookie := auth.CookieName() + "=" + token
	return grpctunnel.DialContext(ctx, a.BridgeURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithHeader("Cookie", cookie),
		grpctunnel.WithHeader("Origin", allowedOrigin),
	)
}

// DialBridgeUnauthenticated opens a bridge connection with no cookie at
// all, for isolation_test.go's "unauthenticated bridge upgrade is refused"
// assertion. It returns the raw HTTP response so the test can assert the
// 401 status directly (the WebSocket handshake itself never completes).
func (a *App) DialBridgeUnauthenticated() (*http.Response, error) {
	return http.Get(a.BridgeURL)
}

// Clients bundles every generated service client dialed over one bridge
// connection.
type Clients struct {
	Auth   affv1.AuthServiceClient
	Feed   affv1.FeedServiceClient
	Item   affv1.ItemServiceClient
	Run    affv1.RunServiceClient
	Sample affv1.SampleServiceClient
	System affv1.SystemServiceClient
}

func newClients(conn *grpc.ClientConn) *Clients {
	return &Clients{
		Auth:   affv1.NewAuthServiceClient(conn),
		Feed:   affv1.NewFeedServiceClient(conn),
		Item:   affv1.NewItemServiceClient(conn),
		Run:    affv1.NewRunServiceClient(conn),
		Sample: affv1.NewSampleServiceClient(conn),
		System: affv1.NewSystemServiceClient(conn),
	}
}

// --- misc small helpers used by more than one test file -------------------

// FetchFeed performs a real HTTP GET against the publish plane over real
// TCP loopback (not an in-process ResponseRecorder) — the same distinction
// internal/flowtest/harness.go's FetchFeed doc comment calls out as
// load-bearing for PLAN.md §17.5's J10 exactness claims.
func (a *App) FetchFeed(t *testing.T, slug, ext string) *http.Response {
	t.Helper()
	url := a.PublishURL + "/feeds/" + slug + "." + ext
	resp, err := a.publishSrv.Client().Get(url)
	if err != nil {
		t.Fatalf("e2e: fetching %s: %v", url, err)
	}
	return resp
}
