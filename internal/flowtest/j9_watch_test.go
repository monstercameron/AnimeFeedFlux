// J9 — watch a run live (PLAN.md §22 "J9 — Watch a run live", TODOS.md
// BF-40..43).
//
// This file's header used to say RunService.Watch, the bridge, and the
// progress-event type did not exist. That is stale: RunService.Watch is
// real (internal/rpc/run.go), the gRPC-over-WebSocket bridge is real
// (internal/bridge), and RunProgress is a real proto message
// (gen/aff/v1/run.pb.go). What each test below drives is a real
// *rpc.RunServer, wired against this package's own real *store.Store,
// reached over a REAL network connection — a plain loopback grpc.Server
// rather than the WebSocket bridge specifically, because this package may
// not edit internal/bridge or internal/rpc to expose the shared test
// scaffolding internal/e2e/app.go already built for that transport, and
// because BF-41's actual claim ("a dropped socket does not abort the run")
// is a property of RunServer.Watch's own ctx.Done() handling
// (internal/rpc/run.go's Watch doc comment: "a dropped client is detected
// via stream.Context() being done... nothing here ever touches the run
// row on that path") — transport-agnostic, and just as real to prove over
// a plain TCP gRPC stream that gets closed mid-watch as over a WebSocket
// that does. Whether grpctunnel itself correctly forwards a
// server-streaming RPC's frames is a separate question, already answered
// by internal/e2e/watch_test.go's TestWatchOverBridge (TODOS.md B2-06).
//
// BF-42 (reconnecting shows true current state, not stale) needed no RPC at
// all to test honestly — it is a claim about the `runs` row a Watch RPC
// reads from — and stays as it was.
package flowtest

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// j9BuildRunRPCServer stands up a real, minimal *grpc.Server exposing ONLY
// RunService against w's real store, over a real TCP loopback listener. No
// session interceptor is chained in: J9's sanity assertions are about the
// stream/run relationship, not about auth (that is J1's job, and BF-32's
// TestJ7_ElevatedSessionReachesOnlyPasswordAndTOTP already proves the
// interceptor works over a real connection of this same shape). Returns the
// *rpc.RunServer itself, not just its address, because BF-43 needs to call
// its exported ReportCandidate/ReportCommitted directly — see that test's
// own comment for why that is a conforming use of RunProgressReporter, not
// a fabrication of it.
func j9BuildRunRPCServer(t *testing.T, w *World) (runSrv *rpc.RunServer, addr string) {
	t.Helper()
	runSrv = rpc.NewRunServer(w.Store, nil)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for j9's plain grpc server: %v", err)
	}
	gs := grpc.NewServer()
	affv1.RegisterRunServiceServer(gs, runSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return runSrv, lis.Addr().String()
}

// j9DialRunClient dials addr with no transport security, the same shape
// j7DialAuthClient and internal/e2e/app.go's plain listener both use.
func j9DialRunClient(t *testing.T, addr string) (affv1.RunServiceClient, *grpc.ClientConn) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing j9's plain grpc server: %v", err)
	}
	return affv1.NewRunServiceClient(conn), conn
}

// j9BuildItem is a minimal, valid model.Item for a direct store.CommitRun
// call — the same "commit a real run through the real transaction, not a
// mock" approach TestJ9_ReconnectSeesTrueCurrentState_NotStaleSnapshot
// (BF-42) already established in this file, extended here to include real
// items so BF-41/BF-43 can assert on items surviving, not just the run row.
func j9BuildItem(w *World, feedID int64, key string, publishedAt time.Time) model.Item {
	now := w.Clock.Now()
	return model.Item{
		FeedID:      feedID,
		ItemKey:     w.IDs.NewItemKey(publishedAt),
		ContentHash: "j9-flowtest-" + key,
		Title:       "J9 flowtest item " + key,
		SummaryText: "A short plain-text summary of the item, safely under the cap",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a></p>`,
		PublishedAt: publishedAt,
		Origin:      model.OriginGenerated,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

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

// TestJ9_StreamTerminatesWithRun is BF-40: the stream must terminate when
// the run does, in every branch including failure. Both branches drive a
// real *rpc.RunServer.Watch over a real TCP connection and assert the
// stream ends (io.EOF) carrying the run's true terminal status — not that
// a mock's Send was called some number of times.
func TestJ9_StreamTerminatesWithRun(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j9-stream-terminates"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	_, addr := j9BuildRunRPCServer(t, w)

	t.Run("succeeded run terminates the stream with SUCCEEDED", func(t *testing.T) {
		runID, err := w.Store.StartRun(ctx, feed.ID, "manual", "j9-watcher")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		go func() {
			time.Sleep(30 * time.Millisecond)
			it := j9BuildItem(w, feed.ID, "succeeded-branch", w.Clock.Now())
			if err := w.Store.CommitRun(ctx, runID, []model.Item{it}, store.RunSummary{}); err != nil {
				t.Errorf("CommitRun: %v", err)
			}
		}()

		client, conn := j9DialRunClient(t, addr)
		defer func() { _ = conn.Close() }()
		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := client.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}

		var last *affv1.RunServiceWatchResponse
		count := 0
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("stream.Recv: %v", err)
			}
			count++
			last = msg
		}
		if count == 0 {
			t.Fatal("Watch delivered no messages before EOF")
		}
		if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
			t.Fatalf("terminal status = %v, want SUCCEEDED", last.GetRun().GetStatus())
		}
	})

	t.Run("failed run terminates the stream with FAILED", func(t *testing.T) {
		runID, err := w.Store.StartRun(ctx, feed.ID, "manual", "j9-watcher")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		go func() {
			time.Sleep(30 * time.Millisecond)
			if err := w.Store.FailRun(ctx, runID, "transient", "provider timed out", store.RunSummary{}); err != nil {
				t.Errorf("FailRun: %v", err)
			}
		}()

		client, conn := j9DialRunClient(t, addr)
		defer func() { _ = conn.Close() }()
		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := client.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}

		var last *affv1.RunServiceWatchResponse
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("stream.Recv: %v", err)
			}
			last = msg
		}
		if last == nil {
			t.Fatal("Watch delivered no messages before EOF")
		}
		if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_FAILED {
			t.Fatalf("terminal status = %v, want FAILED", last.GetRun().GetStatus())
		}
	})
}

// TestJ9_DroppedSocketDoesNotAbortRun is BF-41. It proves the run SURVIVES a
// dropped watcher, not merely that nothing panicked: the run must reach its
// terminal state with its items intact, provable only by reading that state
// back from the store AFTER the connection that was watching it is long
// gone, through the same commit path a real crash-recovery-adjacent run
// would use.
func TestJ9_DroppedSocketDoesNotAbortRun(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j9-dropped-socket"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	_, addr := j9BuildRunRPCServer(t, w)

	runID, err := w.Store.StartRun(ctx, feed.ID, "manual", "j9-watcher")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	client, conn := j9DialRunClient(t, addr)
	watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := client.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Receive the initial snapshot — proves a watcher was genuinely attached
	// to this run (status 'running') before it gets dropped, so what follows
	// is a real mid-watch disconnect, not a connection that never watched
	// anything.
	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("receiving the initial snapshot: %v", err)
	}
	if first.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_RUNNING {
		t.Fatalf("initial snapshot status = %v, want RUNNING", first.GetRun().GetStatus())
	}

	// The dropped socket: close the underlying connection outright, exactly
	// what a laptop closing its lid or a browser tab closing does at the
	// transport level — RunServer.Watch observes this as stream.Context()
	// becoming Done (internal/rpc/run.go's Watch doc comment).
	if err := conn.Close(); err != nil {
		t.Fatalf("closing the watcher's connection: %v", err)
	}
	cancel()

	// Give the server side a moment to actually notice the drop before
	// proceeding — not required for correctness (the commit below does not
	// depend on it), only so this test is genuinely exercising "watcher
	// already gone" rather than racing it.
	time.Sleep(30 * time.Millisecond)

	// The run continues and commits for real, with real items, entirely
	// server-side — nothing here is watching anymore.
	items := []model.Item{
		j9BuildItem(w, feed.ID, "dropped-socket-1", w.Clock.Now()),
		j9BuildItem(w, feed.ID, "dropped-socket-2", w.Clock.Now().Add(time.Second)),
	}
	if err := w.Store.CommitRun(ctx, runID, items, store.RunSummary{}); err != nil {
		t.Fatalf("CommitRun after the watcher dropped: %v", err)
	}

	// BF-41 (§22 J9): the run reaches its terminal state with its items
	// intact, read back from the store — not from anything the dropped
	// connection could have observed.
	final := runByID(t, w, feed.ID, runID)
	if final.Status != "success" {
		t.Fatalf("run status after a dropped watcher = %q, want %q", final.Status, "success")
	}
	if final.ItemsAdded != len(items) {
		t.Fatalf("run.ItemsAdded = %d, want %d", final.ItemsAdded, len(items))
	}
	stored, err := w.Store.ListItems(ctx, feed.ID, 0, false)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(stored) != len(items) {
		t.Fatalf("feed has %d items after a dropped watcher, want %d", len(stored), len(items))
	}
}

// TestJ9_ProgressEventsNeverClaimUncommittedItems is BF-43, the sharpest of
// the four: PLAN.md §9 commits a run's items and its closed run row in ONE
// transaction, so a progress event claiming "N items added" before that
// transaction returns would show an operator a number that a crash could
// immediately contradict. This drives real progress ticks through
// *rpc.RunServer's exported RunProgressReporter methods (ReportCandidate/
// ReportCommitted) — the same interface internal/generate's runner will call
// once wired (RunProgressReporter's own doc comment: "nothing in this
// codebase calls it yet") — as a CONFORMING caller: ReportCommitted is only
// ever invoked here after store.CommitRun has actually returned success,
// exactly the contract RunProgressReporter's doc comment states. This is not
// a fabrication of the caller (this package cannot and does not edit
// internal/generate to wire the real one in), it proves the property the
// interceptor's own mechanical check (publishCommitted's non-regression
// guard) cannot: that a watcher observing a tick over a REAL stream, at the
// REAL moment it arrives, can query the store and find that count already
// true — never ahead of the database.
func TestJ9_ProgressEventsNeverClaimUncommittedItems(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j9-progress-uncommitted"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	runSrv, addr := j9BuildRunRPCServer(t, w)

	runID, err := w.Store.StartRun(ctx, feed.ID, "manual", "j9-watcher")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	client, conn := j9DialRunClient(t, addr)
	defer func() { _ = conn.Close() }()
	watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := client.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Drain the initial 'running' snapshot before the commit below, so the
	// hub subscription (which Watch establishes before this snapshot is
	// even sent) is provably already active when ReportCommitted fires.
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receiving the initial snapshot: %v", err)
	}

	// The real §9 commit transaction — items and the closed run row, one
	// transaction, exactly as §9 requires — happens BEFORE the progress
	// report, matching RunProgressReporter's documented contract.
	items := []model.Item{
		j9BuildItem(w, feed.ID, "progress-1", w.Clock.Now()),
		j9BuildItem(w, feed.ID, "progress-2", w.Clock.Now().Add(time.Second)),
	}
	if err := w.Store.CommitRun(ctx, runID, items, store.RunSummary{}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}
	runSrv.ReportCommitted(runID, int32(len(items)))

	var sawProgress bool
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		if prog := msg.GetProgress(); prog != nil && prog.GetItemsCommitted() > 0 {
			sawProgress = true
			// BF-43 (§22 J9): at the exact moment this tick is observed,
			// the database must already contain AT LEAST as many items for
			// this run as the tick claims — never a number the commit
			// transaction has not actually produced yet.
			actual, err := w.Store.ListItems(ctx, feed.ID, 0, false)
			if err != nil {
				t.Fatalf("ListItems at the moment of a progress tick: %v", err)
			}
			if int32(len(actual)) < prog.GetItemsCommitted() {
				t.Fatalf("progress tick claimed %d items committed, but the store only has %d at the moment the tick was observed",
					prog.GetItemsCommitted(), len(actual))
			}
		}
	}
	if !sawProgress {
		t.Fatal("never observed a RunProgress tick carrying ItemsCommitted — the assertion above never ran, which would make this test vacuous")
	}
}
