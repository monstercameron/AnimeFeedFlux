// J5 — diagnose a bad run (PLAN.md §22 "J5 — Diagnose a bad run", TODOS.md
// BF-22..BF-25).
package flowtest

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// runByID re-reads a single run row through ListRuns, since *store.Store
// exposes no GetRun-by-id — every J5 assertion is about one just-closed run,
// so scanning the (short) per-feed list back is simpler than adding a new
// store method this package does not own.
func runByID(t *testing.T, w *World, feedID, runID int64) store.Run {
	t.Helper()
	runs, err := w.Store.ListRuns(t.Context(), feedID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	for _, r := range runs {
		if r.ID == runID {
			return r
		}
	}
	t.Fatalf("run %d not found among feed %d's runs", runID, feedID)
	return store.Run{}
}

// TestJ5_EveryRunReachesTerminalStatus covers both halves of the sanity
// claim: a run that runs to completion through the real pipeline closes
// itself (never left "running" by generate.Run's own commit path), and a run
// that never gets a chance to close — the crash case, simulated the same way
// internal/store's own tests do: StartRun with no matching Commit/Fail/Skip,
// then the boot watchdog reclaims it.
func TestJ5_EveryRunReachesTerminalStatus(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j5-terminal"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	// A run driven all the way through generate.Run: its own commit path
	// (runner.go's single `commit` exit point) must leave it closed.
	w.Provider.QueueResult(validGenerateResult("A completed run's only item today"))
	spec := validSampleSpec()
	spec.Trigger = "manual" // runs.trigger has a CHECK constraint; RunGeneration always needs a real one
	res, err := w.RunGeneration(ctx, feed, spec)
	if err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}

	// A run that crashed mid-flight: StartRun opens the lock row, and
	// nothing ever calls CommitRun/FailRun/SkipRun for it — the same shape
	// internal/store/runs_test.go uses to exercise ReclaimStaleRuns, driven
	// here through the real store rather than a package-internal helper.
	crashedID, err := w.Store.StartRun(ctx, feed.ID, "cron", "flowtest-crash")
	if err != nil {
		t.Fatalf("StartRun (simulated crash): %v", err)
	}
	staleHeartbeat := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := w.Store.Writer().ExecContext(ctx,
		`UPDATE runs SET heartbeat_at = ? WHERE id = ?`, staleHeartbeat, crashedID); err != nil {
		t.Fatalf("backdating crashed run's heartbeat: %v", err)
	}
	if _, _, err := w.Store.ReclaimStaleRuns(ctx, 5*time.Minute); err != nil {
		t.Fatalf("ReclaimStaleRuns: %v", err)
	}

	// BF-22 (§22 J5): every run reaches a terminal status, none left
	// 'running' after the process exits — checked across BOTH runs above, not
	// just the happy path.
	runs, err := w.Store.ListRuns(ctx, feed.ID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("feed has %d runs, want 2 (completed + crashed)", len(runs))
	}
	for _, r := range runs {
		if r.Status == "running" {
			t.Fatalf("run %d is still 'running' after the process exited", r.ID)
		}
	}

	// storeAdapter.CommitRun (harness.go) maps generate.StatusCompleted onto
	// store.Store.CommitRun, which persists the literal status 'success' —
	// the store's own vocabulary, distinct from generate's RunRecord.Status
	// strings — so the lookup below searches for that, not for "completed".
	if res.Run.Status != generate.StatusCompleted {
		t.Fatalf("RunResult.Run.Status = %q, want %q", res.Run.Status, generate.StatusCompleted)
	}
	completed := runByID(t, w, feed.ID, findRunID(t, runs, "success"))
	if completed.Status != "success" {
		t.Fatalf("persisted completed run status = %q, want %q", completed.Status, "success")
	}
	crashed := runByID(t, w, feed.ID, crashedID)
	if crashed.Status != "interrupted" {
		t.Fatalf("crashed run status = %q, want %q (zero items committed, so it must read as interrupted, not a lying 'failed')", crashed.Status, "interrupted")
	}
}

// findRunID picks out the one run in runs whose status matches want, for the
// completed-run half of TestJ5_EveryRunReachesTerminalStatus — generate.Run
// itself never returns the numeric run id (storeAdapter.CommitRun mints it
// internally), so this is how the test recovers it to re-read the row.
func findRunID(t *testing.T, runs []store.Run, want string) int64 {
	t.Helper()
	for _, r := range runs {
		if r.Status == want {
			return r.ID
		}
	}
	t.Fatalf("no run with status %q among %d runs", want, len(runs))
	return 0
}

// TestJ5_ItemsAddedPlusRejectedReconciles is BF-23. It drives a real
// generative run whose provider output is deliberately mixed — one candidate
// that clears every contract.go rule, one whose title is under the 10-rune
// minimum — so the run commits exactly one item and rejects exactly one, and
// then reads the closed run row back to check the two numbers agree with
// what got recorded as the reason.
func TestJ5_ItemsAddedPlusRejectedReconciles(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j5-reconcile"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(llm.Result{
		Items: []llm.GeneratedItem{
			validGeneratedItem("A perfectly valid trivia question title"),
			{ // fails contract.go's titleMinLen=10 runes rule (this is 9).
				Title:       "Too short",
				SummaryText: "A short plain-text summary of the item, safely under the cap",
				BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a></p>`,
			},
		},
		Raw: "{}",
	})

	spec := validSampleSpec()
	spec.ItemsPerRun = 2
	spec.Trigger = "manual" // runs.trigger has a CHECK constraint; RunGeneration always needs a real one
	res, err := w.RunGeneration(ctx, feed, spec)
	if err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}
	if res.Run.Status != generate.StatusCompleted {
		t.Fatalf("run status = %q, want %q (one valid item should still commit)", res.Run.Status, generate.StatusCompleted)
	}

	// See TestJ5_EveryRunReachesTerminalStatus's comment: the store persists
	// 'success', not generate's "completed".
	run := runByID(t, w, feed.ID, findRunID(t, mustListRuns(t, w, feed.ID), "success"))

	// BF-23 (§22 J5): items_added + items_rejected reconciles with the
	// recorded reject reasons. store.RunSummary.reasonsJSON already refuses
	// to persist a mismatched total (runs.go), so reading the reasons back
	// and summing them is itself the reconciliation proof, not a tautology —
	// a bug that dropped a rejection from the map (while still counting it in
	// ItemsRejected) would have failed to WRITE at all, which the earlier
	// RunGeneration error check would have caught.
	if run.ItemsAdded != 1 {
		t.Fatalf("items_added = %d, want 1", run.ItemsAdded)
	}
	if run.ItemsRejected != 1 {
		t.Fatalf("items_rejected = %d, want 1", run.ItemsRejected)
	}
	sum := 0
	for _, n := range run.RejectReasons {
		sum += n
	}
	if sum != run.ItemsRejected {
		t.Fatalf("sum of reject_reasons_json = %d, does not reconcile with items_rejected = %d", sum, run.ItemsRejected)
	}
	if run.RejectReasons["title_too_short"] != 1 {
		t.Fatalf("reject reasons = %v, want title_too_short: 1", run.RejectReasons)
	}
}

func mustListRuns(t *testing.T, w *World, feedID int64) []store.Run {
	t.Helper()
	runs, err := w.Store.ListRuns(t.Context(), feedID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	return runs
}

// TestJ5_FailedRunHasZeroItems is BF-24: a provider-level failure (not a
// validation rejection) must leave the feed with nothing attributable to
// that run.
func TestJ5_FailedRunHasZeroItems(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j5-failed-zero-items"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueGenerateError(errors.New("flowtest: simulated provider outage"))

	spec := validSampleSpec()
	spec.Trigger = "manual" // runs.trigger has a CHECK constraint; RunGeneration always needs a real one
	res, err := w.RunGeneration(ctx, feed, spec)
	if err == nil {
		t.Fatal("RunGeneration succeeded despite a provider error, want an error")
	}
	if res.Run.Status != generate.StatusFailed {
		t.Fatalf("run status = %q, want %q", res.Run.Status, generate.StatusFailed)
	}

	// BF-24 (§22 J5): a run marked failed has zero items attributable to it.
	if len(res.Items) != 0 {
		t.Fatalf("RunResult.Items has %d items on a failed run, want 0", len(res.Items))
	}
	n, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("feed has %d items after a failed run, want 0", n)
	}

	run := runByID(t, w, feed.ID, findRunID(t, mustListRuns(t, w, feed.ID), generate.StatusFailed))
	if run.ItemsAdded != 0 {
		t.Fatalf("failed run's persisted items_added = %d, want 0", run.ItemsAdded)
	}

	// TestJ5_TokensAndCostRecordedOnFailedRun (below) is the same run's other
	// half of BF-24/BF-25; this test only asserts the "zero items" claim so a
	// failure there names exactly the assertion that broke.
}

// TestJ5_TokensAndCostRecordedOnFailedRun is BF-25: a failure that spent
// money must still show the money. estimateTokens (runner.go) counts the
// prompt BEFORE the provider call resolves, so tokens_in and est_cost_usd
// are non-zero even though the call itself errored and produced no output.
func TestJ5_TokensAndCostRecordedOnFailedRun(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j5-cost-on-failure"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueGenerateError(errors.New("flowtest: simulated provider outage"))

	spec := validSampleSpec()
	spec.Trigger = "manual" // runs.trigger has a CHECK constraint; RunGeneration always needs a real one
	res, err := w.RunGeneration(ctx, feed, spec)
	if err == nil {
		t.Fatal("RunGeneration succeeded despite a provider error, want an error")
	}

	// BF-25 (§22 J5): tokens and cost are recorded even for a failed run.
	if res.Run.TokensIn <= 0 {
		t.Fatalf("failed run's TokensIn = %d, want > 0 (the prompt was still built and sent)", res.Run.TokensIn)
	}
	if res.Run.EstCostUSD <= 0 {
		t.Fatalf("failed run's EstCostUSD = %v, want > 0", res.Run.EstCostUSD)
	}

	run := runByID(t, w, feed.ID, findRunID(t, mustListRuns(t, w, feed.ID), generate.StatusFailed))
	if run.TokensIn != res.Run.TokensIn {
		t.Fatalf("persisted runs.tokens_in = %d, want %d (the reported figure)", run.TokensIn, res.Run.TokensIn)
	}
	if run.EstCostUSD != res.Run.EstCostUSD {
		t.Fatalf("persisted runs.est_cost_usd = %v, want %v", run.EstCostUSD, res.Run.EstCostUSD)
	}
}
