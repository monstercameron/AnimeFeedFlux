// J9 — watch a run live (PLAN.md §22 "J9 — Watch a run live", TODOS.md
// BF-40..43).
//
// This flow is the one this package can do the least for today. Watching a
// run "live" means RunService.Watch streaming over the gRPC-over-WS bridge
// (TODOS.md B1-06, B2-05, B2-06) — none of it exists yet: there is no
// internal/rpc, no internal/bridge, and no progress-event type anywhere in
// this repository (see this file's package doc search — grepping the whole
// tree for "Progress"/"WebSocket"/"Watch" outside TODOS.md and this comment
// turns up nothing). So three of the four sanity assertions have nothing to
// drive them against and are t.Skip'd with the blocking task named, per this
// package's mandate not to fabricate a passing test around missing
// functionality.
//
// What IS real and testable without a socket: the run state a Watch RPC
// would ultimately stream FROM. Any future implementation has to read the
// same runs table generate.Run and store.Store already write to, so proving
// that state is always the true, current state — never a stale snapshot —
// is a real, store-level assertion (BF-42), not a stand-in for the RPC.
package flowtest

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// TestJ9_ReconnectSeesTrueCurrentState_NotStaleSnapshot is BF-42. It does
// not simulate a WebSocket reconnect (there is nothing to reconnect to yet)
// — it simulates the thing a reconnect fundamentally depends on being
// correct: that polling the run's row mid-flight, and again after it
// closes, both return the row's TRUE state at the moment of the read, not a
// cached or stale one.
func TestJ9_ReconnectSeesTrueCurrentState_NotStaleSnapshot(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j9-reconnect-state"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	// "Watching" begins before the run has produced anything: StartRun opens
	// the row the same way the real scheduler/RunNow will, and nothing has
	// closed it yet.
	runID, err := w.Store.StartRun(ctx, feed.ID, "manual", "flowtest-watcher")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// A viewer "reconnecting" right now must see 'running' — the true
	// current state — not whatever it last observed before a hypothetical
	// disconnect.
	mid := runByID(t, w, feed.ID, runID)
	if mid.Status != "running" {
		t.Fatalf("mid-flight run status = %q, want %q", mid.Status, "running")
	}
	if !mid.FinishedAt.IsZero() {
		t.Fatalf("mid-flight run has a non-zero FinishedAt (%v), want zero while still running", mid.FinishedAt)
	}

	// The run finishes for real, through the real commit path.
	if err := w.Store.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	// A viewer reconnecting AFTER completion must see the terminal state,
	// not the 'running' snapshot from before — the stale-snapshot failure
	// mode BF-42 explicitly calls out.
	after := runByID(t, w, feed.ID, runID)
	if after.Status != "success" {
		t.Fatalf("post-completion run status = %q, want %q", after.Status, "success")
	}
	if after.FinishedAt.IsZero() {
		t.Fatal("post-completion run has a zero FinishedAt")
	}
	if after.FinishedAt.Equal(mid.FinishedAt) && !mid.FinishedAt.IsZero() {
		t.Fatal("FinishedAt did not change between the mid-flight and post-completion reads")
	}
}

// TestJ9_StreamTerminatesWithRun_Skip documents BF-40: the stream must
// terminate when the run does, in every branch including failure. There is
// no stream — RunService.Watch (B1-06) and the bridge that would carry it
// (B2-06) do not exist — so there is nothing whose termination could be
// observed.
func TestJ9_StreamTerminatesWithRun_Skip(t *testing.T) {
	t.Skip("BF-40 needs RunService.Watch (B1-06) streamed through the bridge (B2-06); no stream exists yet to observe terminating")
}

// TestJ9_DroppedSocketDoesNotAbortRun_Skip documents BF-41: a dropped socket
// must not abort the run it was watching. There is no socket — the bridge's
// WebSocket upgrade and its session revalidation (§4, B2-05) do not exist —
// so "dropped" has no concrete event to simulate; RunGeneration already
// takes no client-connection-derived context by construction (Deps carries
// no socket handle at all), which is necessary for this property but not
// sufficient to prove it against a real bridge that doesn't exist yet.
func TestJ9_DroppedSocketDoesNotAbortRun_Skip(t *testing.T) {
	t.Skip("BF-41 needs the bridge's WebSocket session (B2-05/B2-06) to actually drop mid-run; no socket exists yet to drop")
}

// TestJ9_ProgressEventsNeverClaimUncommittedItems_Skip documents BF-43:
// progress events must never claim an item that was not actually committed.
// There is no progress-event type anywhere in this repository yet
// (RunService.Watch, B1-06, is what would define and emit them), so there is
// nothing to assert this claim against without inventing the very type this
// package's mandate forbids fabricating.
func TestJ9_ProgressEventsNeverClaimUncommittedItems_Skip(t *testing.T) {
	t.Skip("BF-43 needs RunService.Watch's progress-event stream (B1-06), which does not exist yet — no event type to assert against")
}
