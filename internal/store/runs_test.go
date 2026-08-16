package store

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// makeRunItem builds an item suitable for CommitRun. Named distinctly from
// items_test.go's makeItem to avoid any ambiguity about which file owns it,
// even though there is no actual name collision (same package, so makeItem
// is already reachable — this just keeps run-specific fixtures separate).
func makeRunItem(feedID int64, key string, published time.Time) model.Item {
	return model.Item{
		FeedID:      feedID,
		ItemKey:     key,
		ContentHash: "hash-" + key,
		Title:       "Title " + key,
		SummaryText: "summary",
		BodyHTML:    "<p>body</p>",
		PublishedAt: published,
		Origin:      model.OriginGenerated,
	}
}

// setHeartbeat backdates a run's heartbeat directly through the writer, since
// there is no public API that manufactures a stale lock — only the passage
// of real time would, and tests should not wait on that.
func setHeartbeat(t *testing.T, s *Store, runID int64, at time.Time) {
	t.Helper()
	if _, err := s.writer.ExecContext(t.Context(),
		`UPDATE runs SET heartbeat_at = ? WHERE id = ?`, formatTime(at), runID); err != nil {
		t.Fatalf("backdating heartbeat for run %d: %v", runID, err)
	}
}

func runStatus(t *testing.T, s *Store, runID int64) string {
	t.Helper()
	var status string
	if err := s.writer.QueryRowContext(t.Context(),
		`SELECT status FROM runs WHERE id = ?`, runID).Scan(&status); err != nil {
		t.Fatalf("reading status for run %d: %v", runID, err)
	}
	return status
}

func TestStartRunSecondCallForSameFeedReturnsErrRunInProgress(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	if _, err := s.StartRun(ctx, feedID, "cron", "worker-1"); err != nil {
		t.Fatalf("first start run: %v", err)
	}

	_, err = s.StartRun(ctx, feedID, "cron", "worker-2")
	if !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("second start run err = %v, want ErrRunInProgress", err)
	}
}

func TestStartRunWithStaleHeartbeatDoesNotBlockNewRun(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	firstID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("first start run: %v", err)
	}
	setHeartbeat(t, s, firstID, time.Now().Add(-2*runLockStaleAfter))

	secondID, err := s.StartRun(ctx, feedID, "cron", "worker-2")
	if err != nil {
		t.Fatalf("start run with stale predecessor: %v", err)
	}
	if secondID == firstID {
		t.Fatal("expected a distinct run id")
	}
}

func TestCommitRunWritesItemsAndClosesRunAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	items := []model.Item{makeRunItem(feedID, "key-1", published)}
	summary := RunSummary{TokensIn: 10, TokensOut: 20, CostUSD: 0.01}

	if err := s.CommitRun(ctx, runID, items, summary); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	if status := runStatus(t, s, runID); status != "success" {
		t.Errorf("status = %q, want success", status)
	}
	got, err := s.GetItem(ctx, "key-1")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Title != "Title key-1" {
		t.Errorf("title = %q", got.Title)
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].ItemsAdded != 1 {
		t.Errorf("items_added = %d, want 1", runs[0].ItemsAdded)
	}
	if runs[0].TokensIn != 10 || runs[0].TokensOut != 20 {
		t.Errorf("tokens = %d/%d, want 10/20", runs[0].TokensIn, runs[0].TokensOut)
	}
}

func TestCommitRunMidCommitFailureLeavesNeitherItemsNorClosedRun(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	// Two items sharing published_at violate UNIQUE(feed_id, published_at)
	// (§5.5), which should abort the whole transaction.
	items := []model.Item{
		makeRunItem(feedID, "key-1", published),
		makeRunItem(feedID, "key-2", published),
	}
	summary := RunSummary{TokensIn: 5, TokensOut: 5, CostUSD: 0.001}

	err = s.CommitRun(ctx, runID, items, summary)
	if err == nil {
		t.Fatal("expected commit run to fail on duplicate published_at")
	}

	if status := runStatus(t, s, runID); status != "running" {
		t.Errorf("status after failed commit = %q, want running", status)
	}
	if _, err := s.GetItem(ctx, "key-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("key-1 err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetItem(ctx, "key-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("key-2 err = %v, want ErrNotFound", err)
	}
}

func TestReclaimStaleRunsMarksZeroItemRunInterrupted(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	setHeartbeat(t, s, runID, time.Now().Add(-10*time.Minute))

	reclaimed, unconfirmed, err := s.ReclaimStaleRuns(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim stale runs: %v", err)
	}
	if reclaimed != 1 || unconfirmed != 0 {
		t.Fatalf("reclaimed=%d unconfirmed=%d, want 1/0", reclaimed, unconfirmed)
	}
	if status := runStatus(t, s, runID); status != "interrupted" {
		t.Errorf("status = %q, want interrupted", status)
	}
}

func TestReclaimStaleRunsMarksRunWithItemsCompletedUnconfirmed(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Simulate a crash that happened AFTER the items landed but somehow left
	// the run row 'running' — the case §15 says the watchdog must not treat
	// as a demonstrable failure. CommitRun is not used here on purpose: it
	// would close the run, and this test wants a running row with items
	// already attributed to it.
	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	item := makeRunItem(feedID, "key-1", published)
	if _, err := s.writer.ExecContext(ctx, insertRunItemSQL,
		item.FeedID, item.ItemKey, item.ContentHash, item.Title, item.SummaryText, item.BodyHTML,
		nullString(item.AnswerHTML), nullString(item.Link), nullString(item.SourceName),
		"", formatTime(item.PublishedAt), string(item.Origin), runID,
		formatTime(time.Now()), formatTime(time.Now())); err != nil {
		t.Fatalf("seeding orphaned item: %v", err)
	}
	setHeartbeat(t, s, runID, time.Now().Add(-10*time.Minute))

	reclaimed, unconfirmed, err := s.ReclaimStaleRuns(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim stale runs: %v", err)
	}
	if reclaimed != 0 || unconfirmed != 1 {
		t.Fatalf("reclaimed=%d unconfirmed=%d, want 0/1", reclaimed, unconfirmed)
	}
	if status := runStatus(t, s, runID); status != "completed_unconfirmed" {
		t.Errorf("status = %q, want completed_unconfirmed", status)
	}
}

func TestFailRunStillRecordsTokensAndCost(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	summary := RunSummary{
		TokensIn: 100, TokensOut: 40, CostUSD: 0.05,
		ItemsRejected: 1,
		RejectReasons: map[string]int{"link_mismatch": 1},
	}
	if err := s.FailRun(ctx, runID, "provider_error", "boom", summary); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	r := runs[0]
	if r.Status != "failed" {
		t.Errorf("status = %q, want failed", r.Status)
	}
	if r.TokensIn != 100 || r.TokensOut != 40 || r.EstCostUSD != 0.05 {
		t.Errorf("spend = %d/%d/%v, want 100/40/0.05", r.TokensIn, r.TokensOut, r.EstCostUSD)
	}
	if r.ErrorKind != "provider_error" || r.Error != "boom" {
		t.Errorf("error fields = %q/%q", r.ErrorKind, r.Error)
	}
	if r.ItemsRejected != 1 || r.RejectReasons["link_mismatch"] != 1 {
		t.Errorf("reject accounting = %d/%v", r.ItemsRejected, r.RejectReasons)
	}
}

func TestSkipRunWithNoSummaryRecordsZeroSpend(t *testing.T) {
	// The pre-call budget-gate case (§13): SkipRun called with no RunSummary
	// must record honest zeros, not leave the previous run's stale tokens/
	// cost columns in place.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if err := s.SkipRun(ctx, runID, "budget exhausted"); err != nil {
		t.Fatalf("skip run: %v", err)
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	r := runs[0]
	if r.Status != "skipped" {
		t.Errorf("status = %q, want skipped", r.Status)
	}
	if r.TokensIn != 0 || r.TokensOut != 0 || r.EstCostUSD != 0 {
		t.Errorf("spend = %d/%d/%v, want 0/0/0", r.TokensIn, r.TokensOut, r.EstCostUSD)
	}
}

// TestSkipRunAfterSpendRecordsTokensAndCost is Gap 1: a run can be skipped
// AFTER money has been spent — the novelty gate rejecting every candidate on
// the third retry, for instance (§9 step 5) — and that spend must still show
// up on the run and in SpendSince, exactly like a failed run (§13, §22 J5).
func TestSkipRunAfterSpendRecordsTokensAndCost(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	summary := RunSummary{
		TokensIn: 300, TokensOut: 150, CostUSD: 0.09,
		ItemsRejected: 3,
		RejectReasons: map[string]int{"novelty_duplicate": 3},
	}
	if err := s.SkipRun(ctx, runID, "novelty_exhausted", summary); err != nil {
		t.Fatalf("skip run: %v", err)
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	r := runs[0]
	if r.Status != "skipped" {
		t.Errorf("status = %q, want skipped", r.Status)
	}
	if r.TokensIn != 300 || r.TokensOut != 150 || r.EstCostUSD != 0.09 {
		t.Errorf("spend = %d/%d/%v, want 300/150/0.09", r.TokensIn, r.TokensOut, r.EstCostUSD)
	}
	if r.ItemsRejected != 3 || r.RejectReasons["novelty_duplicate"] != 3 {
		t.Errorf("reject accounting = %d/%v", r.ItemsRejected, r.RejectReasons)
	}

	// And it must not vanish from spend accounting either — the whole point
	// of the fix.
	_, _, cost, err := s.SpendSince(ctx, feedID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("spend since: %v", err)
	}
	if cost != 0.09 {
		t.Errorf("SpendSince cost = %v, want 0.09 — a skipped run's spend must still count", cost)
	}
}

func TestSkipRunRejectsMoreThanOneSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.SkipRun(ctx, runID, "reason", RunSummary{}, RunSummary{}); err == nil {
		t.Fatal("expected an error passing two RunSummary values")
	}
}

func TestSpendSinceSumsAndRespectsWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedA, err := s.CreateFeed(ctx, makeFeed("feed-a"))
	if err != nil {
		t.Fatalf("create feed a: %v", err)
	}
	feedB, err := s.CreateFeed(ctx, makeFeed("feed-b"))
	if err != nil {
		t.Fatalf("create feed b: %v", err)
	}

	// In-window run on feed A.
	runA, err := s.StartRun(ctx, feedA, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run a: %v", err)
	}
	if err := s.CommitRun(ctx, runA, nil, RunSummary{TokensIn: 10, TokensOut: 5, CostUSD: 0.02}); err != nil {
		t.Fatalf("commit run a: %v", err)
	}

	// In-window run on feed B.
	runB, err := s.StartRun(ctx, feedB, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run b: %v", err)
	}
	if err := s.CommitRun(ctx, runB, nil, RunSummary{TokensIn: 3, TokensOut: 7, CostUSD: 0.03}); err != nil {
		t.Fatalf("commit run b: %v", err)
	}

	// Out-of-window run on feed A: backdate started_at before the window.
	oldRunID, err := s.StartRun(ctx, feedA, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start old run: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if _, err := s.writer.ExecContext(ctx,
		`UPDATE runs SET started_at = ? WHERE id = ?`, formatTime(old), oldRunID); err != nil {
		t.Fatalf("backdating old run: %v", err)
	}
	if err := s.CommitRun(ctx, oldRunID, nil, RunSummary{TokensIn: 1000, TokensOut: 1000, CostUSD: 99}); err != nil {
		t.Fatalf("commit old run: %v", err)
	}

	since := time.Now().Add(-24 * time.Hour)

	tokensIn, tokensOut, cost, err := s.SpendSince(ctx, feedA, since)
	if err != nil {
		t.Fatalf("spend since (feed A): %v", err)
	}
	if tokensIn != 10 || tokensOut != 5 || cost != 0.02 {
		t.Errorf("feed A spend = %d/%d/%v, want 10/5/0.02", tokensIn, tokensOut, cost)
	}

	gTokensIn, gTokensOut, gCost, err := s.SpendSince(ctx, 0, since)
	if err != nil {
		t.Fatalf("spend since (global): %v", err)
	}
	if gTokensIn != 13 || gTokensOut != 12 {
		t.Errorf("global tokens = %d/%d, want 13/12", gTokensIn, gTokensOut)
	}
	wantCost := 0.05
	if diff := gCost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("global cost = %v, want %v", gCost, wantCost)
	}
}

func TestListRunsIsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := s.StartRun(ctx, feedID, "cron", "worker-1")
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if err := s.SkipRun(ctx, id, "budget exhausted"); err != nil {
			t.Fatalf("skip run %d: %v", i, err)
		}
		ids = append(ids, id)
		// Force distinct started_at ordering rather than relying on
		// sub-millisecond timing being reliably monotonic across the loop.
		if _, err := s.writer.ExecContext(ctx,
			`UPDATE runs SET started_at = ? WHERE id = ?`,
			formatTime(time.Now().Add(time.Duration(i)*time.Minute)), id); err != nil {
			t.Fatalf("spacing run %d: %v", i, err)
		}
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(runs))
	}
	if runs[0].ID != ids[2] || runs[1].ID != ids[1] || runs[2].ID != ids[0] {
		t.Errorf("order = %v, want newest first %v", []int64{runs[0].ID, runs[1].ID, runs[2].ID}, []int64{ids[2], ids[1], ids[0]})
	}
	for _, r := range runs {
		if r.Status != "skipped" {
			t.Errorf("run %d status = %q, want skipped", r.ID, r.Status)
		}
	}
}
