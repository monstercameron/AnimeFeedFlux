package main

// TestLifecycleCLIOnly is B3-11 (§18 B3): drives one feed's full lifecycle —
// create, sample, promote, run, item read-back, run history — using nothing
// but `aff`'s own command code (a.run) against a real gRPC server on a real
// TCP loopback listener, reached with the CLI's real production dial path
// (a.realDial). Every other CLI test in this package drives a fakeXClient,
// which proves the CLI's own logic but never that the CLI and internal/rpc
// agree on anything on the wire, or that a value one command prints is
// actually accepted by the next. This is the one place the whole lifecycle
// is proven end to end, over all six services at once instead of only
// AuthService — plaingrpc_e2e_test.go's shape, extended.
//
// The scheduler that will map a stored FeedSpec into a live generate.Spec
// (PLAN.md A7) does not exist yet: nothing in internal/rpc constructs a
// generate.Spec today (internal/flowtest's package doc records the same
// gap). So, exactly like internal/flowtest's World, this test's
// FeedRunExecutor and SampleService feed-lookup hand back a fixed, valid
// generate.Spec directly rather than deriving one from the feed's stored
// FeedSpec JSON — test-only glue standing in for A7, not a shortcut around
// anything this pass owns.
import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

const lcPassword = "correct horse battery staple long enough"

var lcSecretKey = []byte("lifecycle-e2e-secret-key-not-real")

// lcSpec is the fixed, valid generate.Spec every feed this test creates
// actually samples/runs with — see the file's doc comment for why.
func lcSpec() generate.Spec {
	return generate.Spec{
		SystemPrompt:         "You write short anime trivia questions.",
		UserPromptTemplate:   "Write {{.ItemsPerRun}} item(s) for {{.Today}}. Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		Model:                "lifecycle-e2e-model",
		ItemsPerRun:          1,
		PriceInputPerMToken:  1.00,
		PriceOutputPerMToken: 2.00,
	}
}

// lcGeneratedItem clears every internal/generate/contract.go rule for a
// generative feed, the same fixture shape internal/flowtest/fixtures_test.go
// uses.
func lcGeneratedItem(title string) llm.GeneratedItem {
	return llm.GeneratedItem{
		Title:       title,
		SummaryText: "A short plain-text summary of the item, safely under the cap",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a></p>`,
		Tags:        []string{"anime"},
	}
}

// lcFeedByID resolves a feed by id: *store.Store only exposes
// GetFeedBySlug (slug is the stable public identity, PLAN.md §14.1), so this
// does the id->slug indirection with one small raw query, the same pattern
// internal/flowtest's feedByID uses.
func lcFeedByID(ctx context.Context, st *store.Store, id int64) (model.Feed, error) {
	var slug string
	if err := st.Writer().QueryRowContext(ctx, `SELECT slug FROM feeds WHERE id = ?`, id).Scan(&slug); err != nil {
		return model.Feed{}, err
	}
	return st.GetFeedBySlug(ctx, slug)
}

// lcGenStore is the read-only generate.Store slice both SampleService and
// this test's FeedRunExecutor need, backed by the real store.
type lcGenStore struct{ st *store.Store }

func (g lcGenStore) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
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

func (g lcGenStore) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	items, err := g.st.ListItems(ctx, feedID, 1, false)
	if err != nil {
		return time.Time{}, err
	}
	if len(items) == 0 {
		return time.Time{}, nil
	}
	return items[0].PublishedAt, nil
}

// lcFeedLookup satisfies SampleServerConfig.Feeds against the real store,
// handing back lcSpec() for every feed regardless of its stored FeedSpec —
// see the file's doc comment.
type lcFeedLookup struct{ st *store.Store }

func (l lcFeedLookup) GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error) {
	f, err := lcFeedByID(ctx, l.st, feedID)
	if err != nil {
		return model.Feed{}, generate.Spec{}, err
	}
	return f, lcSpec(), nil
}

// lcBudget always allows: budget enforcement is BF-13..18's job, not
// B3-11's.
type lcBudget struct{}

func (lcBudget) CheckSample(ctx context.Context, feedID int64, projectedTokens int) budget.Decision {
	return budget.Decision{Allow: true}
}

func (lcBudget) RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error) {
	return 100, nil
}

// lcRunStore adapts generate.Store's single CommitRun hook to the run row
// FeedService.RunNow already opened (runID), rather than opening a second
// one the way internal/flowtest's World has to (it has no pre-existing run
// id to reuse).
type lcRunStore struct {
	st    *store.Store
	runID int64
}

func (a lcRunStore) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	return lcGenStore{st: a.st}.RecentTitles(ctx, feedID, n)
}

func (a lcRunStore) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	return lcGenStore{st: a.st}.NewestPublished(ctx, feedID)
}

func (a lcRunStore) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	summary := store.RunSummary{
		TokensIn:      run.TokensIn,
		TokensOut:     run.TokensOut,
		CostUSD:       run.EstCostUSD,
		ItemsRejected: run.ItemsRejected,
		RejectReasons: run.RejectReasons,
	}
	switch run.Status {
	case generate.StatusCompleted:
		return a.st.CommitRun(ctx, a.runID, items, summary)
	case generate.StatusSkipped:
		return a.st.SkipRun(ctx, a.runID, run.Error)
	default:
		return a.st.FailRun(ctx, a.runID, run.ErrorKind, run.Error, summary)
	}
}

// lcRunExecutor implements rpc.FeedRunExecutor: it backgrounds the real
// internal/generate.Run pipeline against the run row RunNow already started,
// exactly as PLAN.md A7's scheduler will once it exists (internal/rpc/
// feed.go's FeedRunExecutor doc comment: "ExecuteRun must not block the RPC
// caller").
type lcRunExecutor struct {
	st       *store.Store
	provider llm.Provider
	ids      ids.Source
}

func (e lcRunExecutor) ExecuteRun(feedID, runID int64) {
	go func() {
		ctx := context.Background()
		feed, err := lcFeedByID(ctx, e.st, feedID)
		if err != nil {
			_ = e.st.FailRun(ctx, runID, "lookup_failed", err.Error(), store.RunSummary{})
			return
		}
		deps := generate.Deps{
			Store:    lcRunStore{st: e.st, runID: runID},
			Provider: e.provider,
			IDs:      e.ids,
			Now:      time.Now,
		}
		_, _ = generate.Run(ctx, deps, feed, lcSpec())
	}()
}

// lcStartServer stands up a real *grpc.Server exposing every RPC service
// `aff` talks to (Auth, Feed, Sample, Item, Run), all sharing the same real
// migrated *store.Store, with AuthServer's own interceptors chained in front
// of every call — the same shape plaingrpc_e2e_test.go's
// startPlainGRPCServer uses for AuthService alone.
func lcStartServer(t *testing.T) (addr, totpSecret string) {
	t.Helper()

	st, err := store.Open(t.Context(), store.Options{Path: t.TempDir() + "/aff.db"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	hash, err := auth.Hash(lcPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := st.InitAdmin(t.Context(), hash, "argon2id m=65536,t=3,p=1"); err != nil {
		t.Fatalf("init admin: %v", err)
	}
	secret, _, err := auth.Enroll("admin", "AnimeFeedFlux-lifecycle-e2e")
	if err != nil {
		t.Fatalf("enroll totp: %v", err)
	}
	enc, err := auth.EncryptSecret(secret, lcSecretKey)
	if err != nil {
		t.Fatalf("encrypt totp secret: %v", err)
	}
	if err := st.SetTOTPSecret(t.Context(), enc); err != nil {
		t.Fatalf("set totp secret: %v", err)
	}

	authSrv, err := rpc.NewAuthServer(st, lcSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}

	provider := llm.NewFake()
	idSource := ids.NewDeterministicSource(1)

	sampleSrv := rpc.NewSampleServer(rpc.SampleServerConfig{
		Feeds:    lcFeedLookup{st: st},
		GenStore: lcGenStore{st: st},
		Provider: provider,
		IDs:      idSource,
		Budget:   lcBudget{},
		Samples:  st,
		Enabled:  func(context.Context) (bool, string, error) { return true, "", nil },
	})
	feedSrv := rpc.NewFeedServer(st, nil, lcRunExecutor{st: st, provider: provider, ids: idSource})
	itemSrv := rpc.NewItemServer(st, nil, idSource)
	runSrv := rpc.NewRunServer(st, nil)

	gs := grpc.NewServer(
		grpc.ChainUnaryInterceptor(authSrv.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authSrv.StreamInterceptor()),
	)
	affv1.RegisterAuthServiceServer(gs, authSrv)
	affv1.RegisterFeedServiceServer(gs, feedSrv)
	affv1.RegisterSampleServiceServer(gs, sampleSrv)
	affv1.RegisterItemServiceServer(gs, itemSrv)
	affv1.RegisterRunServiceServer(gs, runSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	// The provider is scripted here, AFTER every server piece exists but
	// BEFORE the CLI drives anything, so both the sample dry run and the
	// real run each get exactly one queued result to consume, in the order
	// the flow below calls them.
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{lcGeneratedItem("Lifecycle sample candidate")}, Raw: "{}"})
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{lcGeneratedItem("Lifecycle run item")}, Raw: "{}"})

	return lis.Addr().String(), secret
}

// lcNewApp builds a real (non-fake) *app pointed at addr, using the CLI's
// production dial path — a.realDial — exactly like plaingrpc_e2e_test.go's
// newE2EApp.
func lcNewApp(t *testing.T, addr, sessionFile string) *app {
	t.Helper()
	a := &app{
		Stdout:      new(strings.Builder),
		Stderr:      new(strings.Builder),
		Stdin:       strings.NewReader(""),
		Server:      addr,
		SessionFile: sessionFile,
		JSON:        true,
	}
	a.dial = a.realDial
	return a
}

// lcRunCmd runs one `aff` command against a, failing the test with stdout
// and stderr on a non-exitOK result, and returns stdout's captured text.
func lcRunCmd(t *testing.T, a *app, args ...string) string {
	t.Helper()
	out := a.Stdout.(*strings.Builder)
	errOut := a.Stderr.(*strings.Builder)
	out.Reset()
	errOut.Reset()
	if code := a.run(args); code != exitOK {
		t.Fatalf("aff %s: exit code = %d, want %d (stderr: %s, stdout: %s)",
			strings.Join(args, " "), code, exitOK, errOut.String(), out.String())
	}
	return out.String()
}

// lcLastJSONMessage decodes stdout as a sequence of concatenated protojson
// values — `aff run`'s --json output prints one such value per Watch
// message, not one for the whole command (run_cmd.go's cmdRun calls
// a.printProtoJSON(ev) once per streamed message) — and returns the LAST
// one, which for a terminated Watch stream is the run's final state.
// Commands that print exactly one message decode to that same single value.
func lcLastJSONMessage(t *testing.T, jsonOut string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(jsonOut))
	var last map[string]any
	seen := 0
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if seen == 0 {
				t.Fatalf("parsing JSON output: %v (output: %s)", err, jsonOut)
			}
			break
		}
		last = m
		seen++
	}
	return last
}

// lcJSONString parses stdout's protojson output (see lcLastJSONMessage) and
// returns field from the last message as a string — protojson serializes
// every int64 as a JSON string, so this covers both string and
// numeric-looking id fields uniformly.
func lcJSONString(t *testing.T, jsonOut, field string) string {
	t.Helper()
	m := lcLastJSONMessage(t, jsonOut)
	v, ok := m[field]
	if !ok {
		t.Fatalf("JSON output has no field %q (output: %s)", field, jsonOut)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("field %q = %#v, want a string", field, v)
	}
	return s
}

// TestLifecycleCLIOnly is B3-11.
func TestLifecycleCLIOnly(t *testing.T) {
	addr, totpSecret := lcStartServer(t)
	sessionFile := t.TempDir() + "/session.json"
	login := lcNewApp(t, addr, sessionFile)
	t.Cleanup(func() { _ = login.Close() })

	// --- login (real Login RPC, real TOTP code, real session file) ---
	code, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	login.Stdin = strings.NewReader(lcPassword + "\n" + code + "\n")
	lcRunCmd(t, login, "login")

	// Every command after login uses a FRESH app instance, exactly like
	// plaingrpc_e2e_test.go's `a` (login) / `b` (authenticated calls) split:
	// app.client() dials lazily on first use and then caches the connection
	// (app.go's client()), so an app that dialed unauthenticated during
	// `login` (session.go has no token yet at that point) would keep using
	// that same, still-tokenless connection for every later call if reused.
	// A fresh app re-reads the just-written session file at its own first
	// dial instead.
	a := lcNewApp(t, addr, sessionFile)
	t.Cleanup(func() { _ = a.Close() })

	// --- feed create ---
	specJSON := `{
		"cron": "0 9 * * *",
		"timezone": "UTC",
		"itemsPerRun": 1,
		"feedWindow": 50,
		"model": "lifecycle-e2e-model",
		"systemPromptTemplate": "You write short anime trivia questions.",
		"userPromptTemplate": "Write {{.ItemsPerRun}} item(s) for {{.Today}}. Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		"dailyTokenBudget": 100000,
		"dailyRunBudget": 10
	}`
	feedOut := lcRunCmd(t, a, "feed", "create",
		"--slug", "lifecycle-e2e-feed",
		"--kind", "generative",
		"--title", "Lifecycle E2E Feed",
		"--spec-json", specJSON)
	feedID := lcJSONString(t, feedOut, "id")
	if feedID == "" || feedID == "0" {
		t.Fatalf("feed create returned no usable id (output: %s)", feedOut)
	}

	// `feed get` round-trips the same feed by slug — proves Create's write
	// and Get's read agree, not just that Create returned something.
	getOut := lcRunCmd(t, a, "feed", "get", "lifecycle-e2e-feed")
	if got := lcJSONString(t, getOut, "id"); got != feedID {
		t.Fatalf("feed get id = %q, want %q (the id feed create returned)", got, feedID)
	}

	// --- sample (real SampleService.Sample, real generate.Sample dry run,
	// consuming the FIRST queued provider result) ---
	sampleOut := lcRunCmd(t, a, "sample", "--size", "1", "lifecycle-e2e-feed")
	sampleID := lcJSONString(t, sampleOut, "sample_id")
	if sampleID == "" {
		t.Fatalf("sample returned no sample id (output: %s)", sampleOut)
	}
	sampleMsg := lcLastJSONMessage(t, sampleOut)
	candidates, _ := sampleMsg["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("sample returned %d candidates, want 1 (output: %s)", len(candidates), sampleOut)
	}
	candidate, _ := candidates[0].(map[string]any)
	candidateID, _ := candidate["candidate_id"].(string)
	if candidateID == "" {
		t.Fatalf("sample's one candidate has no candidate id (output: %s)", sampleOut)
	}

	// --- promote (real ItemService.PromoteSample) ---
	promoteOut := lcRunCmd(t, a, "promote", "--candidate", candidateID, sampleID)
	promotedItemID := lcJSONString(t, promoteOut, "id")
	if promotedItemID == "" || promotedItemID == "0" {
		t.Fatalf("promote returned no usable item id (output: %s)", promoteOut)
	}

	// --- run (real FeedService.RunNow + RunService.Watch, consuming the
	// SECOND queued provider result through lcRunExecutor's real
	// generate.Run) ---
	runOut := lcRunCmd(t, a, "run", "lifecycle-e2e-feed")
	lastMsg := lcLastJSONMessage(t, runOut)
	runObj, _ := lastMsg["run"].(map[string]any)
	if runObj == nil {
		t.Fatalf("run's last streamed message has no run object (output: %s)", runOut)
	}
	if got, _ := runObj["status"].(string); got != "RUN_STATUS_SUCCEEDED" {
		t.Fatalf("run status = %q, want RUN_STATUS_SUCCEEDED (output: %s)", got, runOut)
	}
	// items_added is int32 (proto/aff/v1/run.proto) — protojson only
	// stringifies 64-bit integer types, so this decodes as a JSON number.
	if got, _ := runObj["items_added"].(float64); got != 1 {
		t.Fatalf("run items_added = %v, want 1 (output: %s)", runObj["items_added"], runOut)
	}

	// --- item list / item get: both the promoted item and the generated
	// run item must be visible through the CLI's own read path, proving the
	// whole lifecycle actually left real, readable state behind — not just
	// that each command individually returned exitOK. ---
	listOut := lcRunCmd(t, a, "item", "list", "--feed", feedID)
	listMsg := lcLastJSONMessage(t, listOut)
	itemsRaw, _ := listMsg["items"].([]any)
	if len(itemsRaw) != 2 {
		t.Fatalf("item list has %d items, want 2 (one promoted, one generated) (output: %s)", len(itemsRaw), listOut)
	}
	var sawPromoted, sawGenerated bool
	for _, raw := range itemsRaw {
		it, _ := raw.(map[string]any)
		id, _ := it["id"].(string)
		origin, _ := it["origin"].(string)
		if id == promotedItemID {
			sawPromoted = true
			if origin != "ORIGIN_SAMPLED" {
				t.Fatalf("promoted item's origin = %q, want ORIGIN_SAMPLED", origin)
			}
		}
		if origin == "ORIGIN_GENERATED" {
			sawGenerated = true
		}
	}
	if !sawPromoted {
		t.Fatalf("item list never showed the promoted item %s (output: %s)", promotedItemID, listOut)
	}
	if !sawGenerated {
		t.Fatalf("item list never showed a generated item from the run (output: %s)", listOut)
	}

	itemGetOut := lcRunCmd(t, a, "item", "get", promotedItemID)
	if got := lcJSONString(t, itemGetOut, "id"); got != promotedItemID {
		t.Fatalf("item get id = %q, want %q", got, promotedItemID)
	}

	// --- runs (real RunService.History): the manual run just driven above
	// is in the feed's history. ---
	runsOut := lcRunCmd(t, a, "runs", "--feed", feedID)
	runsMsg := lcLastJSONMessage(t, runsOut)
	runsRaw, _ := runsMsg["runs"].([]any)
	if len(runsRaw) != 1 {
		t.Fatalf("runs history has %d entries, want 1 (output: %s)", len(runsRaw), runsOut)
	}
	histRun, _ := runsRaw[0].(map[string]any)
	if got, _ := histRun["status"].(string); got != "RUN_STATUS_SUCCEEDED" {
		t.Fatalf("run history status = %q, want RUN_STATUS_SUCCEEDED (output: %s)", got, runsOut)
	}
	if got, _ := histRun["trigger"].(string); got != "RUN_TRIGGER_MANUAL" {
		t.Fatalf("run history trigger = %q, want RUN_TRIGGER_MANUAL (CLI-triggered run)", got)
	}
}
