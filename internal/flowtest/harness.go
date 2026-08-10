// Package flowtest is the harness for PLAN.md §22's ten user flows and the
// BF-* sanity suite TODOS.md derives from them (§17.5).
//
// §17.5's point, restated because it drives every design choice here: these
// tests drive a whole flow end to end and then assert on the RESULTING
// SYSTEM STATE, not on a mock's call log. A mock proves a function was
// called; only a real migrated store, a real render pass and a real HTTP
// fetch can prove the row that mattered actually landed (or didn't). So
// World stands up the real internal/store (migrated, on a temp file), the
// real internal/publish handler, and the real internal/generate pipeline —
// everything is a fake only at the network boundary (the LLM provider and
// the upstream fetcher), per PLAN.md §17's "the default test run never
// calls a paid API".
//
// Two pieces of glue live here that do not exist anywhere else in the repo
// yet, because the code that will own them (the scheduler's wiring and the
// B1 RPC control plane) has not been built:
//
//   - storeAdapter satisfies generate.Store against the real *store.Store.
//     Nothing today connects internal/generate's Run/Sample to a live
//     database; that is normally the scheduler's job (PLAN.md A7) and it has
//     not landed. Building the adapter here is what makes "run a real
//     generation against a real store" testable before it does.
//   - World.Sample persists the dry run's result via store.PutSample, and
//     honors KillSwitch before ever calling the provider. Both are
//     SampleService's job (TODOS.md B1-04) and it does not exist either.
//
// Everything else — CreateFeed, PromoteSample's retry-on-collision, the
// publish plane's render+cache path — already exists as real, tested code
// in internal/store and internal/publish; World only wires it together.
package flowtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// ErrKillSwitchOn is returned by World.Sample and World.RunGeneration when
// KillSwitch is set, before the provider is ever touched (§22 J3 failure
// branch "kill switch on"; BF-15).
var ErrKillSwitchOn = errors.New("flowtest: generation kill switch is on")

// --- clock ------------------------------------------------------------

// Clock is the injected clock every flow test anchors to (PLAN.md §17.1:
// "an injected clock" — no sleeping, no wall-clock flakiness). It is safe
// for concurrent use because generation and promotion can race in a test on
// purpose (PLAN.md §17's concurrency test pattern).
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock starting at start.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

// Now returns the current instant.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set pins the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// --- fake fetcher -------------------------------------------------------

// FakeFetcher is the scripted internal/sources.Fetcher stand-in a grounded
// feed's flow tests need, mirroring internal/llm.Fake's call-counting shape.
type FakeFetcher struct {
	mu         sync.Mutex
	candidates []sources.Candidate
	err        error
	calls      int
}

// NewFakeFetcher returns an empty FakeFetcher.
func NewFakeFetcher() *FakeFetcher { return &FakeFetcher{} }

// SetCandidates scripts the candidate set every subsequent call returns.
func (f *FakeFetcher) SetCandidates(c []sources.Candidate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candidates = c
}

// SetError scripts the next call (and every call after, until SetError(nil))
// to fail with err.
func (f *FakeFetcher) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Candidates implements generate.Fetcher / sources.Fetcher's shape.
func (f *FakeFetcher) Candidates(ctx context.Context, feedID int64) ([]sources.Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]sources.Candidate, len(f.candidates))
	copy(out, f.candidates)
	return out, nil
}

// CallCount reports how many times Candidates was invoked.
func (f *FakeFetcher) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// --- storeAdapter: generate.Store over the real *store.Store ------------

// storeAdapter satisfies generate.Store. See the package doc for why this
// lives here rather than in internal/store or internal/generate.
type storeAdapter struct {
	s *store.Store
}

// RecentTitles implements generate.Store. store.ListItems already returns
// newest-first (PLAN.md §5.5's ordering), so this is a direct field
// projection, not a second sort.
func (a storeAdapter) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	items, err := a.s.ListItems(ctx, feedID, n, false)
	if err != nil {
		return nil, fmt.Errorf("flowtest: recent titles for feed %d: %w", feedID, err)
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out, nil
}

// NewestPublished implements generate.Store.
func (a storeAdapter) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	items, err := a.s.ListItems(ctx, feedID, 1, false)
	if err != nil {
		return time.Time{}, fmt.Errorf("flowtest: newest published for feed %d: %w", feedID, err)
	}
	if len(items) == 0 {
		return time.Time{}, nil
	}
	return items[0].PublishedAt, nil
}

// CommitRun implements generate.Store by composing the three separate calls
// *store.Store actually exposes (StartRun, then CommitRun/FailRun/SkipRun)
// into the single call generate.Run expects. §9's "items and the closed run
// row commit in one call" is preserved for the success path (store.CommitRun
// is itself one transaction); the StartRun call before it is a second,
// necessarily separate write only because generate.Run does not carry a
// run id of its own to open the row earlier — the real scheduler (A7) will
// call StartRun itself, once, before invoking Run.
func (a storeAdapter) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	runID, err := a.s.StartRun(ctx, run.FeedID, run.Trigger, "flowtest")
	if err != nil {
		return fmt.Errorf("flowtest: starting run for feed %d: %w", run.FeedID, err)
	}
	summary := store.RunSummary{
		TokensIn:      run.TokensIn,
		TokensOut:     run.TokensOut,
		CostUSD:       run.EstCostUSD,
		ItemsRejected: run.ItemsRejected,
		RejectReasons: run.RejectReasons,
	}
	switch run.Status {
	case generate.StatusCompleted:
		return a.s.CommitRun(ctx, runID, items, summary)
	case generate.StatusSkipped:
		return a.s.SkipRun(ctx, runID, run.Error)
	default:
		return a.s.FailRun(ctx, runID, run.ErrorKind, run.Error, summary)
	}
}

// --- World ----------------------------------------------------------------

// World is one flow test's whole system: a migrated store, fake network
// boundaries, a controllable clock, and the real publish plane serving over
// real HTTP. Build one with New (or NewWithDir for the harness's own
// teardown test) per test — World is not meant to be shared across tests.
type World struct {
	T *testing.T

	Store    *store.Store
	Provider *llm.Fake
	Fetcher  *FakeFetcher
	Clock    *Clock
	IDs      ids.Source

	// KillSwitch stands in for TODOS.md's B1-07 SystemService kill switch:
	// when true, Sample and RunGeneration must refuse to call the provider
	// at all (§22 J3, BF-15), matching config.GenerationEnabled=false.
	KillSwitch bool

	// BaseURL is the public base URL the publish plane and every Channel
	// this package builds use, matching internal/publish.Deps.BaseURL's
	// contract (absolute, no trailing slash).
	BaseURL string

	httpServer *httptest.Server
}

// New builds a World backed by a fresh temp directory that Go's testing
// package cleans up on its own (t.TempDir's contract) and registers
// t.Cleanup to close the store and the HTTP server. Use this for every flow
// test except the harness's own teardown test (harness_test.go), which
// needs to control cleanup itself to prove Close() actually released the
// database files.
func New(t *testing.T) *World {
	t.Helper()
	w, err := build(t, t.TempDir())
	if err != nil {
		t.Fatalf("flowtest: building world: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// NewWithDir is New, but the caller owns dir and its cleanup. It exists so
// harness_test.go can assert that closing a World releases every file
// handle — os.RemoveAll(dir) failing on Windows after Close is exactly the
// "leaves no files" regression this harness must never have.
func NewWithDir(t *testing.T, dir string) (*World, error) {
	t.Helper()
	return build(t, dir)
}

func build(t *testing.T, dir string) (*World, error) {
	t.Helper()
	ctx := context.Background()

	s, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "aff.db")})
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrating store: %w", err)
	}

	w := &World{
		T:        t,
		Store:    s,
		Provider: llm.NewFake(),
		Fetcher:  NewFakeFetcher(),
		Clock:    NewClock(testEpoch),
		IDs:      ids.NewDeterministicSource(1),
	}

	// The server's listener address must be known BEFORE building Deps —
	// Deps.BaseURL is a plain string baked into every rendered <link> and
	// self URL (PLAN.md §5.4), not a closure, so it has to be right at
	// construction time. NewUnstartedServer gives us the listener (and
	// therefore the real URL) without serving a single request yet, so
	// w.BaseURL is correct for the Deps the handler is built with.
	w.httpServer = httptest.NewUnstartedServer(nil)
	w.BaseURL = "http://" + w.httpServer.Listener.Addr().String()
	w.httpServer.Config.Handler = publish.NewServer(w.publishDeps())
	w.httpServer.Start()

	return w, nil
}

// testEpoch is the fixed instant every World starts at, matching
// testutil.FixedTime's spirit (a stable anchor, never time.Now) but kept
// local so this package has no test-only dependency on internal/testutil's
// fixtures, which are shaped for renderer goldens, not for flows that need
// to advance a clock.
var testEpoch = time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)

// Close shuts down the HTTP server and the store. Safe to call once; New
// already registers it with t.Cleanup.
func (w *World) Close() {
	if w.httpServer != nil {
		w.httpServer.Close()
	}
	if w.Store != nil {
		_ = w.Store.Close()
	}
}

// --- generation plumbing --------------------------------------------------

// genDeps builds the generate.Deps this World's generation calls use, always
// freshly (Clock.Now is read at call time, not captured once).
func (w *World) genDeps() generate.Deps {
	return generate.Deps{
		Store:    storeAdapter{s: w.Store},
		Fetcher:  w.Fetcher,
		Provider: w.Provider,
		IDs:      w.IDs,
		Now:      w.Clock.Now,
	}
}

// RunGeneration drives the real internal/generate.Run pipeline against this
// World's store, exactly as the scheduler (A7) or a manual RunNow will once
// it exists. It honors KillSwitch first, the same gate Sample honors, so a
// flow test can share one setup for both J2's "no provider call" branch and
// J3/J5's generation branches.
func (w *World) RunGeneration(ctx context.Context, feed model.Feed, spec generate.Spec) (generate.RunResult, error) {
	if w.KillSwitch {
		return generate.RunResult{}, ErrKillSwitchOn
	}
	return generate.Run(ctx, w.genDeps(), feed, spec)
}

// SampleOutcome is what World.Sample returns: the dry-run result plus the
// samples row id it wrote — the receipt PLAN.md §11 says sampling leaves
// behind instead of an item.
type SampleOutcome struct {
	Result   generate.SampleResult
	SampleID int64
}

// Sample runs the real internal/generate.Sample dry run and then persists
// its receipt via store.PutSample — the two things TODOS.md's A8-04 and the
// not-yet-built SampleService are jointly responsible for. It is the one
// function in this package that must NEVER call CommitRun through any path,
// matching generate.Sample's own guarantee (runner.go's doc comment); the
// only store write here is PutSample.
//
// With KillSwitch set, this returns ErrKillSwitchOn without calling
// generate.Sample at all — so w.Provider.GenerateCallCount() stays exactly
// where it was before the call (§22 J3, BF-15).
func (w *World) Sample(ctx context.Context, feed model.Feed, spec generate.Spec) (SampleOutcome, error) {
	if w.KillSwitch {
		return SampleOutcome{}, ErrKillSwitchOn
	}

	res, err := generate.Sample(ctx, w.genDeps(), feed, spec)
	if err != nil {
		return SampleOutcome{Result: res}, err
	}

	payload, merr := json.Marshal(res.Items)
	if merr != nil {
		return SampleOutcome{}, fmt.Errorf("flowtest: marshaling sample payload: %w", merr)
	}
	const sampleTTL = 24 * time.Hour
	sampleID, perr := w.Store.PutSample(ctx, feed.ID, payload, res.TokensIn, res.TokensOut, res.EstCostUSD, sampleTTL)
	if perr != nil {
		return SampleOutcome{}, fmt.Errorf("flowtest: persisting sample: %w", perr)
	}
	return SampleOutcome{Result: res, SampleID: sampleID}, nil
}

// StampForPromotion mints the fresh identity a promote assigns an item
// (PLAN.md §22 J4, §9 step 7): an opaque ULID keyed off the current clock,
// origin=sampled, and a content hash for idempotency only — never identity.
// It does NOT set PublishedAt; store.PromoteSample computes that itself
// (always strictly after the feed's current newest item, with the +1s
// collision retry), so setting it here would just be overwritten and would
// invite a caller to rely on a value that was never authoritative.
func (w *World) StampForPromotion(it model.Item, feed model.Feed) model.Item {
	now := w.Clock.Now()
	it.ItemKey = w.IDs.NewItemKey(now)
	it.ContentHash = fmt.Sprintf("flowtest-%s-%s", feed.Slug, it.ItemKey)
	it.Origin = model.OriginSampled
	it.CreatedAt = now
	it.UpdatedAt = now
	return it
}

// PromoteSample is a thin, named wrapper over store.Store.PromoteSample —
// the real, already-tested promotion path (internal/store/samples.go) that
// this package does not need to reimplement. It exists only so flow tests
// read as flow steps ("world.PromoteSample(...)") rather than reaching past
// World into w.Store directly.
func (w *World) PromoteSample(ctx context.Context, sampleID int64, it model.Item) (int64, error) {
	return w.Store.PromoteSample(ctx, sampleID, it)
}

// --- store read-back helpers ----------------------------------------------

// ItemCount returns the total row count in items for feedID, including soft
// deleted rows — the literal count BF-11 requires be unchanged by a sample.
func (w *World) ItemCount(ctx context.Context, feedID int64) (int, error) {
	var n int
	err := w.Store.Writer().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE feed_id = ?`, feedID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("flowtest: counting items for feed %d: %w", feedID, err)
	}
	return n, nil
}

// SampleRow reads a samples row back by id, distinguishing "does not exist"
// from a real error so callers can assert existence with a plain error
// check rather than string-matching.
func (w *World) SampleRow(ctx context.Context, id int64) (store.Sample, error) {
	return w.Store.GetSample(ctx, id)
}

// feedByID resolves a feed by its numeric id. *store.Store only exposes
// GetFeedBySlug (PLAN.md's slug is the stable public identity, §14.1), so
// this does the id->slug indirection with one small raw query rather than
// duplicating store's row-scanning here.
func (w *World) feedByID(ctx context.Context, id int64) (model.Feed, bool, error) {
	var slug string
	err := w.Store.Writer().QueryRowContext(ctx, `SELECT slug FROM feeds WHERE id = ?`, id).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, false, nil
	}
	if err != nil {
		return model.Feed{}, false, fmt.Errorf("flowtest: resolving feed %d: %w", id, err)
	}
	f, err := w.Store.GetFeedBySlug(ctx, slug)
	if errors.Is(err, store.ErrNotFound) {
		return model.Feed{}, false, nil
	}
	if err != nil {
		return model.Feed{}, false, err
	}
	return f, true, nil
}

// listFeedSlugs returns every feed's slug, for publish Deps.ListFeeds. A raw
// query rather than a new store method: the index route (§14.1) is not part
// of any BF-* flow this package implements yet, so this stays minimal.
func (w *World) listFeedSlugs(ctx context.Context) ([]string, error) {
	rows, err := w.Store.Writer().QueryContext(ctx, `SELECT slug FROM feeds WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("flowtest: listing feed slugs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("flowtest: scanning feed slug: %w", err)
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// --- feed creation ----------------------------------------------------

// CreateFeed inserts f via the real store.CreateFeed and returns it with ID
// populated, so tests read "create the feed" as one line instead of
// hand-rolling the insert-then-refetch dance.
func (w *World) CreateFeed(ctx context.Context, f model.Feed) (model.Feed, error) {
	id, err := w.Store.CreateFeed(ctx, f)
	if err != nil {
		return model.Feed{}, err
	}
	f.ID = id
	return f, nil
}

// --- publish plane / real HTTP --------------------------------------------

// publishDeps wires internal/publish.Deps to this World's real store reads,
// the same shape publish/server_test.go's fakeBackend uses, except backed by
// a real *store.Store instead of an in-memory map.
func (w *World) publishDeps() publish.Deps {
	return publish.Deps{
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			f, err := w.Store.GetFeedBySlug(ctx, slug)
			if errors.Is(err, store.ErrNotFound) {
				return model.Feed{}, false, nil
			}
			if err != nil {
				return model.Feed{}, false, err
			}
			return f, true, nil
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) {
			return w.Store.ListItems(ctx, feedID, 0, false)
		},
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) {
			slugs, err := w.listFeedSlugs(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]model.Feed, 0, len(slugs))
			for _, slug := range slugs {
				f, err := w.Store.GetFeedBySlug(ctx, slug)
				if err != nil {
					return nil, err
				}
				out = append(out, f)
			}
			return out, nil
		},
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			it, err := w.Store.GetItem(ctx, itemKey)
			if errors.Is(err, store.ErrNotFound) {
				return model.Feed{}, model.Item{}, false, nil
			}
			if err != nil {
				return model.Feed{}, model.Item{}, false, err
			}
			f, found, err := w.feedByID(ctx, it.FeedID)
			if err != nil {
				return model.Feed{}, model.Item{}, false, err
			}
			if !found {
				return model.Feed{}, model.Item{}, false, nil
			}
			return f, it, true, nil
		},
		BaseURL:   w.BaseURL,
		Generator: "AnimeFeedFlux flowtest",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026,
		Now:       w.Clock.Now,
	}
}

// Channel builds a model.Channel for feed+items using exactly the same
// metadata (BaseURL, Generator, DocsURL, TagYear, BuildTime) publishDeps
// hands the real publish plane, so a test that renders directly with
// internal/render and a test that fetches through the real HTTP handler are
// comparing like with like (PLAN.md §22 J3, BF-14: "byte-identical to what
// publishing would emit").
func (w *World) Channel(feed model.Feed, items []model.Item) model.Channel {
	return model.Channel{
		Feed:      feed,
		SelfURL:   w.BaseURL + "/feeds/" + feed.Slug + ".xml",
		HTMLURL:   w.BaseURL + "/",
		Host:      w.BaseURL,
		TagYear:   2026,
		Items:     items,
		BuildTime: w.Clock.Now(),
		Generator: "AnimeFeedFlux flowtest",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}

// FetchFeed performs a real HTTP GET against this World's publish plane
// (httptest, real TCP loopback — not a recorded ResponseWriter) for
// "/feeds/{slug}.{ext}", ext one of "xml", "atom", "json". This is the
// helper §17.5 calls out by name: J10's exactness claims cannot be proven
// against an in-process ResponseRecorder, only a real fetch.
func (w *World) FetchFeed(t *testing.T, slug, ext string) *http.Response {
	t.Helper()
	url := w.BaseURL + "/feeds/" + slug + "." + ext
	resp, err := w.httpServer.Client().Get(url)
	if err != nil {
		t.Fatalf("flowtest: fetching %s: %v", url, err)
	}
	return resp
}
