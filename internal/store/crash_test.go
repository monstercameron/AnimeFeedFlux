// crash_test.go is the C4-14 crash-safety test: PLAN.md §9's "items and the
// closed run row commit in one transaction" rule exists precisely so that a
// crash in the gap between the model returning and that commit landing
// cannot leave live items beside a run the boot watchdog marks interrupted —
// the failure mode that makes a feed's history lie about what happened.
//
// Crash-simulation method: an injected in-transaction failure via
// commitRunPreCommitFailure (runs.go), fired after every item and the run's
// closing UPDATE are staged inside CommitRun's transaction but before
// tx.Commit() runs. That makes the transaction's deferred rollback discard
// everything, which is exactly how a real process death in that window would
// leave things — SQLite never durably commits a transaction the process died
// inside. A closed store handle (killing s.writer outright) was considered
// and rejected: this package's tests share one *Store per test via
// newTestStore, but closing the shared writer connection mid-test would also
// break StartRun/ListRuns calls made from the SAME goroutine afterward and
// risks poisoning connection-pool state other tests rely on — collateral
// damage the real crash does not have. The hook reproduces the one thing
// that matters (this transaction never commits) without any of that blast
// radius.
package store

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// TestCrashBeforeCommitLeavesRunInterruptedWithZeroItems is C4-14's primary
// assertion: kill the process after the model has returned (items are ready
// to write) but before CommitRun's transaction actually commits. §9 requires
// this to produce zero live items and, once the boot watchdog reclaims the
// stale run, a truthful 'interrupted' status — never a run that silently
// looks like it is still in progress forever, and never items that outlived
// the run record saying they don't exist.
func TestCrashBeforeCommitLeavesRunInterruptedWithZeroItems(t *testing.T) {
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

	// Arm the crash: fires after items + the run-closing UPDATE are staged
	// inside CommitRun's transaction, before tx.Commit(). Reset it
	// afterward so it can never leak into another test in this package.
	commitRunPreCommitFailure = func() error {
		return errors.New("simulated crash: process died before commit")
	}
	t.Cleanup(func() { commitRunPreCommitFailure = nil })

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	items := []model.Item{makeRunItem(feedID, "crash-key-1", published)}
	summary := RunSummary{TokensIn: 42, TokensOut: 17, CostUSD: 0.007}

	if err := s.CommitRun(ctx, runID, items, summary); err == nil {
		t.Fatal("expected CommitRun to fail when the pre-commit crash hook fires")
	}

	// Nothing landed: the transaction never committed, so the item the model
	// produced must not be visible anywhere.
	if _, err := s.GetItem(ctx, "crash-key-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("crash-key-1 err = %v, want ErrNotFound", err)
	}
	// The run row itself is untouched too: the closing UPDATE rolled back
	// along with everything else, so it still reads 'running' — a live
	// process would still be renewing its heartbeat; a crashed one won't,
	// which is what makes it discoverable as stale next.
	if status := runStatus(t, s, runID); status != "running" {
		t.Fatalf("status immediately after simulated crash = %q, want running", status)
	}

	// Now play the boot watchdog: the heartbeat has gone stale (the crashed
	// process is not renewing it), so ReclaimStaleRuns must find this run,
	// see zero items attributed to it (run_id references none, because the
	// insert never committed), and mark it 'interrupted' truthfully rather
	// than 'completed_unconfirmed'.
	setHeartbeat(t, s, runID, time.Now().Add(-10*time.Minute))

	reclaimed, unconfirmed, err := s.ReclaimStaleRuns(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim stale runs: %v", err)
	}
	if reclaimed != 1 || unconfirmed != 0 {
		t.Fatalf("reclaimed=%d unconfirmed=%d, want 1/0 — a crash before commit must never read as completed_unconfirmed", reclaimed, unconfirmed)
	}
	if status := runStatus(t, s, runID); status != "interrupted" {
		t.Fatalf("status after reclaim = %q, want interrupted", status)
	}

	runs, err := s.ListRuns(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].ItemsAdded != 0 {
		t.Errorf("items_added = %d, want 0 — the crash must not have durably added anything", runs[0].ItemsAdded)
	}
	// Confirm via the source of truth ReclaimStaleRuns itself reads, not just
	// the run's own bookkeeping column: no item anywhere references this run.
	var itemCount int
	if err := s.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE run_id = ?`, runID).Scan(&itemCount); err != nil {
		t.Fatalf("counting items for run %d: %v", runID, err)
	}
	if itemCount != 0 {
		t.Errorf("items referencing crashed run = %d, want 0", itemCount)
	}
}

// TestCrashAfterCommitLeavesItemsPresentAndRunTerminal is C4-14's reverse
// assertion: a crash that happens AFTER CommitRun's transaction has actually
// committed must not be undone by boot-time reclaim. §9's atomicity cuts
// both ways — it protects a successful, fully-committed run from being
// second-guessed just as firmly as it protects against a partial one.
func TestCrashAfterCommitLeavesItemsPresentAndRunTerminal(t *testing.T) {
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
	items := []model.Item{makeRunItem(feedID, "post-commit-key-1", published)}
	summary := RunSummary{TokensIn: 8, TokensOut: 4, CostUSD: 0.002}

	// No crash hook armed: this commit lands for real, the way it would if
	// the process kept running just long enough to finish tx.Commit() and
	// then died on the very next line (e.g. before cache invalidation,
	// which §9 deliberately places OUTSIDE this transaction for exactly this
	// reason — it's idempotent and recoverable, unlike the commit itself).
	if err := s.CommitRun(ctx, runID, items, summary); err != nil {
		t.Fatalf("commit run: %v", err)
	}
	if status := runStatus(t, s, runID); status != "success" {
		t.Fatalf("status right after commit = %q, want success", status)
	}

	// Simulate the boot-time watchdog running anyway, as it always does on
	// every restart regardless of whether the previous shutdown was clean.
	// Because this run already reached a terminal status, ReclaimStaleRuns'
	// query (`status = 'running'`) must not touch it at all.
	reclaimed, unconfirmed, err := s.ReclaimStaleRuns(ctx, 0)
	if err != nil {
		t.Fatalf("reclaim stale runs: %v", err)
	}
	if reclaimed != 0 || unconfirmed != 0 {
		t.Fatalf("reclaimed=%d unconfirmed=%d, want 0/0 — a terminal run must never be reclaimed", reclaimed, unconfirmed)
	}

	if status := runStatus(t, s, runID); status != "success" {
		t.Errorf("status after boot-time reclaim = %q, want success (unchanged)", status)
	}
	got, err := s.GetItem(ctx, "post-commit-key-1")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Title != "Title post-commit-key-1" {
		t.Errorf("title = %q", got.Title)
	}
}
