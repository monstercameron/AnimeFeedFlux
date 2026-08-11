package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"strconv"
)

// countingInvalidator records every InvalidateFeed/InvalidateAll call, the
// same fake shape internal/rpc's own tests use.
type countingInvalidator struct {
	mu    sync.Mutex
	feeds []string
	all   int
}

func (c *countingInvalidator) InvalidateFeed(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.feeds = append(c.feeds, slug)
}
func (c *countingInvalidator) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all++
}
func (c *countingInvalidator) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.feeds)
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// wireTestConfig builds a *config.Config against fixed addresses (so tests
// can dial them directly) and a fresh temp DB path, without touching the
// real environment.
func wireTestConfig(t *testing.T, publishAddr, adminAddr string, generationEnabled bool) *config.Config {
	t.Helper()
	env := map[string]string{
		"AFF_DB_PATH":               filepath.Join(t.TempDir(), "aff.db"),
		"AFF_PUBLISH_ADDR":          publishAddr,
		"AFF_ADMIN_ADDR":            adminAddr,
		"AFF_PUBLIC_BASE_URL":       "https://anime.example.com",
		"AFF_ALLOWED_ORIGINS":       "https://admin.example.com",
		"AFF_SECRET_KEY":            "test-secret-key-0123456789",
		"SCHEMAFLUX_API_KEY":        "test-provider-key",
		"AFF_GENERATION_ENABLED":    "true",
		"AFF_PROVIDER_MAX_INFLIGHT": "1",
	}
	if !generationEnabled {
		env["AFF_GENERATION_ENABLED"] = "false"
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// waitUntilUp polls url until it responds or timeout elapses.
func waitUntilUp(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to come up", url)
}

// --- Two listeners, admin surface unreachable from the publish port -------

func TestRunAllTwoListenersAdminPathNotOnPublish(t *testing.T) {
	publishAddr := "127.0.0.1:18471"
	adminAddr := "127.0.0.1:18472"
	cfg := wireTestConfig(t, publishAddr, adminAddr, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("runAll did not exit after cancel")
		}
	})

	waitUntilUp(t, "http://"+publishAddr+"/healthz", 5*time.Second)
	waitUntilUp(t, "http://"+adminAddr+"/", 5*time.Second)

	// The publish plane's index route (server.go's handleRoot) 404s any
	// path other than "/" or its other fixed routes — a control-plane-
	// shaped path (a gRPC method path, which only ever exists on the admin
	// listener) must not resolve to anything there.
	resp, err := http.Get("http://" + publishAddr + "/aff.v1.SystemService/GetSettings")
	if err != nil {
		t.Fatalf("GET publish control-plane-shaped path: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("control-plane path on publish listener = %d, want 404", resp.StatusCode)
	}

	// And the reverse property that makes the split meaningful: the admin
	// listener does not serve the publish plane's content.
	//
	// This asserted 401 when the bridge owned "/". It no longer does — the
	// bridge owns exactly bridgeEndpoint and the admin UI owns the rest, so
	// "/" is the SPA shell. That is a routing change, not a boundary change,
	// and the boundary is what this test is for: whatever the admin listener
	// answers at "/", it must not be the public feed index.
	resp2, err := http.Get("http://" + adminAddr + "/")
	if err != nil {
		t.Fatalf("GET admin listener /: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	body2, _ := io.ReadAll(resp2.Body)
	if bytes.Contains(body2, []byte("/feeds/")) {
		t.Fatalf("admin listener / served the publish plane's feed index; the two planes must not share content")
	}

	if publishAddr == adminAddr {
		t.Fatal("publish and admin must not share an address")
	}
}

// --- Admin UI bundle is mounted on the ADMIN listener, never the publish ---
// --- plane -------------------------------------------------------------

// TestAdminStaticBundleNotServedFromPublishListener asserts the constraint
// internal/publish/static.go's own doc comment calls out by name: the admin
// UI bundle must never be reachable from the public, unauthenticated
// publish plane. It extends TestRunAllTwoListenersAdminPathNotOnPublish's
// pattern (same two-listener runAll, same "GET the wrong listener, want
// 404" shape) rather than inventing a new one, but targets a static-asset
// path instead of a gRPC-method-shaped path.
func TestAdminStaticBundleNotServedFromPublishListener(t *testing.T) {
	publishAddr := "127.0.0.1:18475"
	adminAddr := "127.0.0.1:18476"
	cfg := wireTestConfig(t, publishAddr, adminAddr, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("runAll did not exit after cancel")
		}
	})

	waitUntilUp(t, "http://"+publishAddr+"/healthz", 5*time.Second)

	// wireTestConfig sets no AFF_ADMIN_STATIC_DIR, so this run has no admin
	// bundle at all (config.DefaultAdminStaticDir won't exist relative to
	// the test binary's working directory) — which only strengthens the
	// assertion: even the asset names the bundle WOULD serve if built
	// (app.wasm, wasm_exec.js, index.html) must 404 on the publish plane
	// rather than ever reaching a static handler there.
	for _, path := range []string{"/app.wasm", "/wasm_exec.js", "/index.html"} {
		resp, err := http.Get("http://" + publishAddr + path)
		if err != nil {
			t.Fatalf("GET publish %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("admin bundle path %s on publish listener = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestAdminMuxKeepsBridgeOnRootAndRoutesEverythingElseToStatic is a direct,
// server-less test of adminMux's routing rule: "/" (wsconn.DefaultEndpoint,
// web/wsconn/conn.go, out of scope for this change) must always reach the
// bridge, and any other path must reach static when one is configured.
// TestAdminMuxRoutesBridgeAndSPA pins the routing that makes the admin UI
// reachable at all. The earlier arrangement gave the bridge "/" and 404'd
// every client-side route; both failures were invisible to the test suite
// and immediately obvious the first time a browser loaded the app.
func TestAdminMuxRoutesBridgeAndSPA(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<!doctype html><title>shell</title>")
	writeFile(t, filepath.Join(dir, "app.wasm"), "\x00asm")
	sh, err := publish.NewStaticHandler(dir)
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}

	bridgeHit := false
	mux := &adminMux{
		bridge: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bridgeHit = true }),
		static: sh,
	}

	get := func(path string) *httptest.ResponseRecorder {
		bridgeHit = false
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	// The bridge owns exactly one path, and it must be the one the client
	// dials. A mismatch here fails as "the UI cannot connect", with no
	// useful error anywhere and no way to see it except in a browser.
	get(bridgeEndpoint)
	if !bridgeHit {
		t.Fatalf("GET %s: want bridge", bridgeEndpoint)
	}
	// web/wsconn is js-tagged, so this package cannot import it to compare
	// the constants directly. Read the source instead: crude, but it is a
	// real check, and the failure it prevents ("the UI cannot connect", no
	// error anywhere, visible only in a browser) is worth an ugly test.
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "wsconn", "conn.go"))
	if err != nil {
		t.Fatalf("reading wsconn source: %v", err)
	}
	want := "const DefaultEndpoint = " + strconv.Quote(bridgeEndpoint)
	if !strings.Contains(string(src), want) {
		t.Fatalf("web/wsconn.DefaultEndpoint does not match bridgeEndpoint %q — the client would dial a path the server does not answer",
			bridgeEndpoint)
	}

	// A real asset is served as itself.
	if rec := get("/app.wasm"); rec.Code != http.StatusOK || bridgeHit {
		t.Fatalf("GET /app.wasm: code=%d bridgeHit=%v, want 200 from static", rec.Code, bridgeHit)
	}

	// The SPA loads at its own base href, and every client-side route
	// serves the shell so the WASM router gets a chance to run.
	for _, path := range []string{"/", "/login", "/generate", "/history", "/settings", "/nope"} {
		rec := get(path)
		if bridgeHit {
			t.Fatalf("GET %s: reached the bridge; a client route must serve the shell", path)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: code=%d, want 200 serving index.html", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>shell</title>") {
			t.Fatalf("GET %s: did not serve the SPA shell", path)
		}
	}

	// Bundle missing at boot (before `make web`): everything falls through
	// to the bridge rather than panicking on a nil static handler.
	bridgeHit = false
	nilStatic := &adminMux{bridge: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bridgeHit = true })}
	nilStatic.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/app.wasm", nil))
	if !bridgeHit {
		t.Fatal("GET /app.wasm with static=nil: want fallback to bridge")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- Graceful shutdown returns within the timeout --------------------------

func TestRunAllShutdownReturnsWithinTimeout(t *testing.T) {
	cfg := wireTestConfig(t, "127.0.0.1:18473", "127.0.0.1:18474", true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()

	waitUntilUp(t, "http://127.0.0.1:18473/healthz", 5*time.Second)

	start := time.Now()
	cancel()
	select {
	case code := <-done:
		if elapsed := time.Since(start); elapsed > httpShutdownTimeout+5*time.Second {
			t.Fatalf("shutdown took %s, want well under %s", elapsed, httpShutdownTimeout)
		}
		if code != exitOK {
			t.Fatalf("runAll exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(httpShutdownTimeout + 10*time.Second):
		t.Fatal("runAll did not return after ctx cancellation")
	}
}

// genExecutorCompletesRunAndInvalidates confirms the wiring between
// genExecutor, generate.Run, and the store commits a real run row and fires
// the invalidator — the property "in-flight work finished" depends on, at
// the level this file is responsible for (schedule.Runner's own draining
// logic is internal/schedule's concern and tested there).
func TestGenExecutorCompletesRunAndInvalidates(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)

	specJSON := `{
		"Kind": "generative", "Cron": "0 12 * * *", "Timezone": "UTC", "ItemsPerRun": 1,
		"SystemPrompt": "sys", "UserPrompt": "user {{.Today}}",
		"Model": {"Model": "gpt-test"},
		"Novelty": {"ExcludeLast": 5, "Threshold": 0.9, "MaxRetries": 1},
		"Budgets": {"DailyTokens": 100000, "DailyRuns": 10}
	}`
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET spec_json = ? WHERE id = ?`, specJSON, feedID); err != nil {
		t.Fatalf("seeding spec_json: %v", err)
	}

	fake := llm.NewFake()
	fake.QueueResult(llm.Result{Items: []llm.GeneratedItem{{
		Title:       "Today's Anime Trivia Question",
		SummaryText: "A daily trivia question about anime.",
		BodyHTML:    "<p>What anime aired in 1998 and became a cult classic?</p>",
	}}})

	inv := &countingInvalidator{}
	exec := &genExecutor{
		st: st, provider: fake, novelty: nil, fetcher: noFetcher{},
		ids: ids.NewSource(), prices: budget.NewTable(), inv: inv,
		log: discardLogger(), holder: "test",
	}

	if err := exec.Execute(t.Context(), feedID, "cron"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := inv.count(); got == 0 {
		t.Fatal("expected the invalidator to fire after a completed run")
	}

	runs, err := st.ListRuns(t.Context(), feedID, 5)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Status != "success" {
		t.Fatalf("run status = %q, want success (error=%s)", runs[0].Status, runs[0].Error)
	}
	if runs[0].ItemsAdded != 1 {
		t.Fatalf("items added = %d, want 1", runs[0].ItemsAdded)
	}
}

// --- The novelty gate actually rejects a real repeat, through the full ------
// --- wiring: genExecutor -> generate.Run -> noveltyAdapter.CheckVector ->  --
// --- genStoreAdapter.CommitRun -> store.CommitRun -> item_embeddings.     --
//
// This is the test PLAN.md §9/§20 asked for and the one a hand-seeded corpus
// (internal/store/embeddings_test.go's TestNoveltyCheckFindsPriorEmbeddings...)
// cannot stand in for: that test proves Gate.Check discriminates once a
// corpus already has rows in it, but a hand-built ItemEmbedding never
// exercises whether anything in this codebase actually PUTS a row there
// during a real run. Before this change, nothing did — every candidate was
// compared against an empty table and always passed, silently, behind a
// green build. This test drives two real generations through genExecutor
// (the exact type the scheduler and RunNow use) against a real store and
// proves the second one is skipped for repeating the first.
func TestNoveltyGateRejectsARealRepeatThroughGenExecutor(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)

	specJSON := `{
		"Kind": "generative", "Cron": "0 12 * * *", "Timezone": "UTC", "ItemsPerRun": 1,
		"SystemPrompt": "sys", "UserPrompt": "user {{.Today}}",
		"Model": {"Model": "gpt-test"},
		"Novelty": {"ExcludeLast": 5, "Threshold": 0.9, "MaxRetries": 1},
		"Budgets": {"DailyTokens": 100000, "DailyRuns": 10}
	}`
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET spec_json = ? WHERE id = ?`, specJSON, feedID); err != nil {
		t.Fatalf("seeding spec_json: %v", err)
	}

	candidate := llm.GeneratedItem{
		Title:       "Today's Anime Trivia Question",
		SummaryText: "A daily trivia question about anime.",
		BodyHTML:    "<p>What anime aired in 1998 and became a cult classic?</p>",
	}

	fake := llm.NewFake()
	// The SAME noveltyAdapter type production wiring uses (runAll), not a
	// test double — the bug this change closes was in this adapter's own
	// loadCorpus and in how its Check result reached CommitRun, so a fake
	// Novelty would prove nothing about the fix.
	nov := noveltyAdapter{st: st, embedder: novelty.NewFakeEmbedder(8), threshold: 0.9, window: 500}
	exec := &genExecutor{
		st: st, provider: fake, novelty: nov, fetcher: noFetcher{},
		ids: ids.NewSource(), prices: budget.NewTable(), inv: nil,
		log: discardLogger(), holder: "test",
	}

	// --- Run 1: genuinely novel against an empty corpus. Publishes, and its
	// embedding must land in item_embeddings inside the same commit.
	fake.QueueResult(llm.Result{Items: []llm.GeneratedItem{candidate}})
	if err := exec.Execute(t.Context(), feedID, "cron"); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	runs, err := st.ListRuns(t.Context(), feedID, 5)
	if err != nil {
		t.Fatalf("ListRuns after first run: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "success" || runs[0].ItemsAdded != 1 {
		t.Fatalf("first run = %+v, want exactly one successful run with 1 item added", runs)
	}

	corpus, err := st.EmbeddingsForFeed(t.Context(), feedID, 500)
	if err != nil {
		t.Fatalf("EmbeddingsForFeed after first run: %v", err)
	}
	if len(corpus) != 1 {
		t.Fatalf("corpus after first run has %d rows, want 1 — CommitRun did not persist the embedding", len(corpus))
	}

	// --- Run 2: the model returns the EXACT same item on every retry
	// attempt (maxAttempts = 1 + MaxRetries = 2, per the spec seeded above)
	// — the realistic "the model keeps handing back a near-duplicate"
	// failure §20 names as the top product risk. If the corpus this gate
	// reads is empty or wrong (the bug), this publishes a second item
	// instead of skipping.
	fake.QueueResult(llm.Result{Items: []llm.GeneratedItem{candidate}})
	fake.QueueResult(llm.Result{Items: []llm.GeneratedItem{candidate}})
	if err := exec.Execute(t.Context(), feedID, "cron"); err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	runs, err = st.ListRuns(t.Context(), feedID, 5)
	if err != nil {
		t.Fatalf("ListRuns after second run: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs after two executions, want 2", len(runs))
	}
	// Pick by ID rather than trusting ListRuns' newest-first ordering here:
	// StartRun's started_at can tie at this test's timestamp resolution (two
	// StartRun calls a few microseconds apart on a coarse clock), and a tie
	// leaves ORDER BY started_at DESC free to return either row first. The
	// row IDs are strictly increasing regardless.
	second := runs[0]
	if runs[1].ID > second.ID {
		second = runs[1]
	}
	// store.SkipRun (runs.go, off-limits to edit) records its reason in the
	// `error` column, not `error_kind` — Run.ErrorKind stays blank for a
	// skip, matching genStoreAdapter.CommitRun's call (SkipRun's second
	// parameter is named `reason`, not `errorKind`).
	if second.Status != "skipped" || second.Error != "novelty_exhausted" {
		t.Fatalf("second run = %+v, want status=skipped error=novelty_exhausted — "+
			"the gate let a real repeat through", second)
	}
	if second.ItemsAdded != 0 {
		t.Fatalf("second run items_added = %d, want 0", second.ItemsAdded)
	}
	if second.RejectReasons["novelty_duplicate"] == 0 {
		t.Fatalf("second run reject reasons = %+v, want novelty_duplicate counted at least once", second.RejectReasons)
	}

	corpusAfter, err := st.EmbeddingsForFeed(t.Context(), feedID, 500)
	if err != nil {
		t.Fatalf("EmbeddingsForFeed after second run: %v", err)
	}
	if len(corpusAfter) != 1 {
		t.Fatalf("corpus after the skipped second run has %d rows, want still 1 "+
			"(a skipped run must not leave an embedding for an item that was never published)", len(corpusAfter))
	}
}

// --- Kill switch prevents dispatch but not serving --------------------------

func TestGenGateKillSwitchBlocksDispatchNotServing(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	seedItem(t, st, feedID, "01J000000000000000000AAA1", time.Now().UTC(), false)

	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", false) // AFF_GENERATION_ENABLED=false

	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger()}
	allowed, reason := gate.Allowed(feedID)
	if allowed {
		t.Fatal("gate.Allowed = true with the kill switch off, want false")
	}
	if reason != "generation_disabled" {
		t.Fatalf("reason = %q, want generation_disabled", reason)
	}

	// The publish plane is a completely separate handler/store-reader path
	// (buildPublishHandlerWithInvalidator) with no reference to genGate —
	// serving is unaffected by the switch by construction. Prove it still
	// serves the feed seeded above.
	handler, _, err := buildPublishHandlerWithInvalidator(st, cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if err != nil {
		t.Fatalf("buildPublishHandlerWithInvalidator: %v", err)
	}
	rec := doReq(handler, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed request with generation disabled = %d, want 200", rec.Code)
	}
}

// --- Provider semaphore is the same instance for the scheduler and Sample --

func TestSchedulerAndSampleShareOneProviderSemaphore(t *testing.T) {
	st := openTestStore(t)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true)
	// AFF_PROVIDER_MAX_INFLIGHT=1 (set in wireTestConfig) so a single held
	// slot is enough to prove sharing.

	provider := llm.NewFake()
	runner, err := buildScheduler(t.Context(), st, cfg, provider, nil, budget.NewTable(), nil, nil, discardLogger())
	if err != nil {
		t.Fatalf("buildScheduler: %v", err)
	}
	sem := runner.ProviderSemaphore()

	// Hold the scheduler's only slot directly, exactly as schedule.Runner's
	// own runOne would while a scheduled generation is in flight.
	holdCtx, releaseHold := context.WithCancel(context.Background())
	defer releaseHold()
	if err := sem.Acquire(holdCtx); err != nil {
		t.Fatalf("acquiring scheduler semaphore: %v", err)
	}

	// The Sample path's provider (buildControlPlane's sampledProvider) is
	// wrapped with the SAME *schedule.Semaphore returned by
	// runner.ProviderSemaphore() above. If it were a second, independently
	// sized instance, this call would proceed immediately instead of
	// blocking on the slot already held.
	sampled := semaphoreProvider{Provider: provider, sem: sem}

	shortCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, genErr := sampled.Generate(shortCtx, llm.Request{Prompt: "p"})
	if genErr == nil {
		t.Fatal("Generate succeeded while the scheduler's only semaphore slot was held elsewhere — " +
			"the two paths are not sharing one instance")
	}
	if shortCtx.Err() == nil {
		t.Fatalf("Generate failed for a reason other than the blocked semaphore: %v", genErr)
	}
}

// --- runAll actually wires obs.Setup and the ops scheduler --------------
//
// This is the gap that let two whole subsystems (obs.Setup's real
// TracerProvider/MeterProvider, internal/ops.Scheduler's nightly
// backup/prune/staleness job) ship fully tested in their own packages and
// never run in production: every existing test above proves the listeners
// bind and serve, which passes identically whether or not either subsystem
// is wired into runAll at all. These tests instead assert the composition
// root itself constructs and starts both, and that shutdown actually
// flushes/drains them — not merely that obs and ops work correctly when
// exercised directly against their own packages (schedule_test.go and
// otel_test.go already cover that).

// captureStdout redirects the process's real os.Stdout to a pipe for the
// duration of the test, returning a function that restores it and returns
// everything written. obs.Setup's "stdout" exporter (otel.go's
// setupTraces/setupMetrics) writes to the literal os.Stdout package
// variable, not to any injectable io.Writer, so this is the only way to
// observe what it exported without editing otel.go (off-limits).
func captureStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outCh <- buf.String()
	}()
	return func() string {
		os.Stdout = orig
		_ = w.Close()
		out := <-outCh
		_ = r.Close()
		return out
	}
}

// lockedBuffer is a concurrency-safe io.Writer, since runAll logs from
// multiple goroutines (listeners, both schedulers) concurrently with the
// test reading the buffer after shutdown.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunAllWiresObsSetupAndFlushesOnShutdown asserts two things a
// listeners-bind-and-serve test cannot: (1) the global TracerProvider and
// MeterProvider obs.Start/obs.NewMetrics read (obs/otel.go's package-level
// vars) are no longer the no-op defaults once runAll is running with
// AFF_OTEL_ENABLED=1 — proving runAll actually calls obs.Setup, not merely
// that obs.Setup works when called directly (otel_test.go already proves
// that); and (2) a metric recorded through that same provider while runAll
// is up is actually exported to the configured "stdout" exporter only once
// shutdown runs — proving the shutdown func obs.Setup returned is both
// wired into runAll's shutdown path AND actually invoked (flushed), not
// merely constructed and discarded.
func TestRunAllWiresObsSetupAndFlushesOnShutdown(t *testing.T) {
	publishAddr := "127.0.0.1:18480"
	adminAddr := "127.0.0.1:18481"
	env := map[string]string{
		"AFF_DB_PATH":               filepath.Join(t.TempDir(), "aff.db"),
		"AFF_PUBLISH_ADDR":          publishAddr,
		"AFF_ADMIN_ADDR":            adminAddr,
		"AFF_PUBLIC_BASE_URL":       "https://anime.example.com",
		"AFF_ALLOWED_ORIGINS":       "https://admin.example.com",
		"AFF_SECRET_KEY":            "test-secret-key-0123456789",
		"SCHEMAFLUX_API_KEY":        "test-provider-key",
		"AFF_GENERATION_ENABLED":    "true",
		"AFF_PROVIDER_MAX_INFLIGHT": "1",
		"AFF_OTEL_ENABLED":          "true",
		"AFF_OTEL_EXPORTER":         "stdout",
		"AFF_TRACE_SAMPLE_RATIO":    "1",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	restoreStdout := captureStdout(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()

	waitUntilUp(t, "http://"+publishAddr+"/healthz", 5*time.Second)

	// (1) The composition root, not just the obs package, installed real
	// providers.
	if tp := obs.GetTracerProvider(); fmt.Sprintf("%T", tp) == fmt.Sprintf("%T", tracenoop.NewTracerProvider()) {
		t.Fatal("TracerProvider is still the package no-op default while runAll is up with AFF_OTEL_ENABLED=1 " +
			"— runAll did not call obs.Setup")
	}
	if mp := obs.GetMeterProvider(); fmt.Sprintf("%T", mp) == fmt.Sprintf("%T", metricnoop.NewMeterProvider()) {
		t.Fatal("MeterProvider is still the package no-op default while runAll is up with AFF_OTEL_ENABLED=1 " +
			"— runAll did not call obs.Setup")
	}

	// (2) Record one data point on the SAME MeterProvider runAll's obs.Setup
	// installed (obs.GetMeterProvider(), not a second one), the same way
	// internal/ops.Scheduler's nightly staleness check would. The default
	// PeriodicReader export interval is far longer than this test's
	// lifetime, so this metric reaching stdout at all can only be
	// attributed to the explicit ForceFlush an OTel MeterProvider.Shutdown
	// performs — i.e. to runAll's deferred obsShutdown call actually
	// running.
	metrics, err := obs.NewMetrics(obs.GetMeterProvider())
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}
	const probeFeedSlug = "wire-test-flush-probe"
	if err := metrics.RecordFeedStaleness(context.Background(), probeFeedSlug, 42); err != nil {
		t.Fatalf("RecordFeedStaleness: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(httpShutdownTimeout + 10*time.Second):
		t.Fatal("runAll did not return after ctx cancellation")
	}

	out := restoreStdout()
	if !strings.Contains(out, "aff_feed_staleness_seconds") || !strings.Contains(out, probeFeedSlug) {
		t.Fatalf("shutdown did not flush the recorded metric to the stdout exporter; "+
			"captured output:\n%s", out)
	}
}

// TestRunAllStartsAndDrainsOpsScheduler asserts the composition root builds
// and starts internal/ops.Scheduler (PLAN.md §15.4) alongside the
// generation scheduler, and that shutdown genuinely drains it rather than
// leaking a goroutine: runAll's shutdown sequence blocks on `<-opsDone`
// after cancelRun(), so if the ops scheduler were never constructed or
// never handed runCtx (i.e. this wiring regressed to what it was before
// this change), that read would block forever and this test would time out
// instead of returning within httpShutdownTimeout. It also asserts the
// visibility requirement: an unset AFF_SLACK_WEBHOOK_URL must be a loud
// startup warning, not a silent gap.
func TestRunAllStartsAndDrainsOpsScheduler(t *testing.T) {
	publishAddr := "127.0.0.1:18482"
	adminAddr := "127.0.0.1:18483"
	env := map[string]string{
		"AFF_DB_PATH":               filepath.Join(t.TempDir(), "aff.db"),
		"AFF_PUBLISH_ADDR":          publishAddr,
		"AFF_ADMIN_ADDR":            adminAddr,
		"AFF_PUBLIC_BASE_URL":       "https://anime.example.com",
		"AFF_ALLOWED_ORIGINS":       "https://admin.example.com",
		"AFF_SECRET_KEY":            "test-secret-key-0123456789",
		"SCHEMAFLUX_API_KEY":        "test-provider-key",
		"AFF_GENERATION_ENABLED":    "true",
		"AFF_PROVIDER_MAX_INFLIGHT": "1",
		"AFF_BACKUP_DIR":            filepath.Join(t.TempDir(), "backups"),
		"AFF_OFFSITE_DIR":           filepath.Join(t.TempDir(), "offsite"),
		// AFF_SLACK_WEBHOOK_URL deliberately left unset.
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	logBuf := &lockedBuffer{}
	log := slog.New(slog.NewTextHandler(logBuf, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, log) }()

	waitUntilUp(t, "http://"+publishAddr+"/healthz", 5*time.Second)

	if !strings.Contains(logBuf.String(), "nightly backup/prune/staleness alerting is disabled") {
		t.Fatalf("no visible warning for an unset AFF_SLACK_WEBHOOK_URL; log so far:\n%s", logBuf.String())
	}

	start := time.Now()
	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("runAll exit code = %d, want %d", code, exitOK)
		}
		if elapsed := time.Since(start); elapsed > httpShutdownTimeout+5*time.Second {
			t.Fatalf("shutdown took %s, want well under %s — the ops scheduler may not be draining on runCtx",
				elapsed, httpShutdownTimeout)
		}
	case <-time.After(httpShutdownTimeout + 10*time.Second):
		t.Fatal("runAll did not return after ctx cancellation — the ops scheduler goroutine likely was never " +
			"started (or never wired to runCtx), so <-opsDone in runAll's shutdown path is blocking forever")
	}
}

// --- Admin listener serves plain gRPC AND everything else on one port ------

// TestAdminListenerServesPlainGRPC reproduces the defect cmd/aff hit against
// a running server: `grpc.NewClient(a.Server, ...)` (cmd/aff/client.go's
// realDial) had no *grpc.Server to talk to, because runAll's admin listener
// was ONLY adminMux — a plain http.Handler, never wrapped for h2c, with no
// gRPC surface at all. Every CLI command failed at the transport before this
// change; this proves a real, unauthenticated AuthService call now reaches
// AuthServer.Login (codes.Unauthenticated, the server's genuine credential
// verdict — NOT a transport error, which is exactly the distinction that
// matters: an Unavailable/transport error here would mean the request never
// arrived).
func TestAdminListenerServesPlainGRPC(t *testing.T) {
	publishAddr := "127.0.0.1:18484"
	adminAddr := "127.0.0.1:18485"
	cfg := wireTestConfig(t, publishAddr, adminAddr, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("runAll did not exit after cancel")
		}
	})

	waitUntilUp(t, "http://"+adminAddr+"/", 5*time.Second)

	conn, err := grpc.NewClient(adminAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := affv1.NewAuthServiceClient(conn)
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	_, err = client.Login(rctx, &affv1.AuthServiceLoginRequest{Password: "wrong", TotpCode: "000000"})
	if err == nil {
		t.Fatal("expected Login to fail (no admin has been provisioned in this test store), but it succeeded")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Login error code = %v (%v), want Unauthenticated — a different code (esp. Unavailable) "+
			"would mean the request never reached the gRPC server at all", status.Code(err), err)
	}

	// The same port must still answer plain HTTP alongside gRPC: a browser
	// request gets a real HTTP response rather than a gRPC-framing error or
	// a hang, proving adminRouter's fallthrough to adminMux still works.
	//
	// The status is deliberately not asserted to be 401 any more. It was,
	// back when the bridge owned "/" and refused without a cookie; that
	// refusal is exactly what made login impossible, since the socket was
	// the only route to the call that mints the cookie. What matters here is
	// that the content-type routing works at all.
	resp, err := http.Get("http://" + adminAddr + "/")
	if err != nil {
		t.Fatalf("GET admin listener /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		t.Fatalf("GET admin listener / = %d; a plain HTTP request must not hit a server error on the gRPC-sharing port", resp.StatusCode)
	}
}

// TestPublishListenerHasNoGRPCSurface asserts the §2 boundary this change
// must not weaken: the ADMIN listener gained a gRPC surface, but the PUBLISH
// listener — public, read-only, HTTP/1.1 only — must not. A plain gRPC
// dial against the publish address must fail at the transport (the publish
// server never speaks h2c), never reach a service, and never hang.
func TestPublishListenerHasNoGRPCSurface(t *testing.T) {
	publishAddr := "127.0.0.1:18486"
	adminAddr := "127.0.0.1:18487"
	cfg := wireTestConfig(t, publishAddr, adminAddr, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runAll(ctx, cfg, discardLogger()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("runAll did not exit after cancel")
		}
	})

	waitUntilUp(t, "http://"+publishAddr+"/healthz", 5*time.Second)

	conn, err := grpc.NewClient(publishAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := affv1.NewAuthServiceClient(conn)
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	_, err = client.Login(rctx, &affv1.AuthServiceLoginRequest{Password: "wrong", TotpCode: "000000"})
	if err == nil {
		t.Fatal("expected a plain gRPC call against the PUBLISH listener to fail; it must have no gRPC surface")
	}
	if status.Code(err) == codes.Unauthenticated {
		t.Fatalf("Login on the publish listener returned a real credential verdict (Unauthenticated) — "+
			"the publish plane must never reach an RPC service at all: %v", err)
	}
}
