package main

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
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
	// listener does not serve the publish plane's index at all (it requires
	// a session cookie before anything, including "/", is answered).
	resp2, err := http.Get("http://" + adminAddr + "/")
	if err != nil {
		t.Fatalf("GET admin listener /: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin listener / = %d, want 401 (no session cookie)", resp2.StatusCode)
	}

	if publishAddr == adminAddr {
		t.Fatal("publish and admin must not share an address")
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
	handler, _, err := buildPublishHandlerWithInvalidator(st, cfg, "test")
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
	runner, err := buildScheduler(t.Context(), st, cfg, provider, nil, budget.NewTable(), nil, discardLogger())
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
