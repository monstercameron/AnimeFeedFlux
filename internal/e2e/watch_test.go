package e2e

import (
	"context"
	"io"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// TestWatchOverBridge drives RunService.Watch over the real gRPC-over-
// WebSocket bridge (PLAN.md §11 "Watch (server-stream of live progress)",
// §22 J9) and asserts the terminal event arrives with the right outcome.
//
// A "real run" here is a real `runs` row created and closed through
// internal/store's own public API — StartRun then CommitRun/FailRun — the
// same shape internal/e2e/killswitch_test.go's realExecutor/runStoreAdapter
// drive generate.Run's actual commit path through, and the same shape
// internal/rpc/run_test.go's own Watch tests use directly against the RPC
// layer. It is not routed through generate.Run here because doing so would
// require internal/generate's runner to report progress mid-run — see
// below for exactly why that half of this file is a t.Skip, not a fake.
//
// What this test does NOT cover, and why: live RunProgress ticks
// (ReportCandidate/ReportCommitted) require a caller in internal/generate to
// invoke RunProgressReporter mid-run — internal/rpc/run.go's own doc comment
// on RunProgressReporter says plainly "nothing in this codebase calls it
// yet." internal/generate is owned by another agent concurrently with this
// change (this file's HARD RULES forbid editing it), and even wiring it in
// through this package's own reach would require internal/e2e/app.go to
// expose *rpc.RunServer (or its ReportCandidate/ReportCommitted methods) to
// a test — app.go is not in this task's editable file list either. Faking a
// progress emitter here would not be end-to-end coverage of the real
// contract; it exercises exactly the wiring path this file cannot touch. So
// the live-progress half of Watch is left to internal/rpc/run_test.go's
// TestWatchRelaysProgressBeforeTerminal (already exercises it directly
// against the RPC layer, real store, real hub) and is explicitly skipped
// here with that reasoning, per this task's own instruction to name the
// blocker rather than fake it.
func TestWatchOverBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: full end-to-end suite skipped in -short mode")
	}

	ctx := context.Background()
	app := New(t)

	totpSecret, err := app.InitAdmin(ctx, adminPassword)
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	login, err := app.Login(ctx, adminPassword, totpSecret)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer login.Close()

	const slug = "e2e-watch-daily"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug:        slug,
			Kind:        affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title:       "E2E Watch Daily",
			Description: "Fixture feed for RunService.Watch over the real bridge.",
			Language:    "en",
			Spec: &affv1.FeedSpec{
				Cron:                 "0 12 * * *",
				Timezone:             "UTC",
				ItemsPerRun:          1,
				FeedWindow:           50,
				Model:                "gpt-4o-mini",
				SystemPromptTemplate: "You write concise anime trivia questions.",
				UserPromptTemplate:   "Write one trivia question about {{.FeedTitle}}.",
				Novelty:              &affv1.NoveltySettings{NoveltyWindowItems: 50, SimilarityThreshold: 0.9},
				DailyTokenBudget:     1000000,
				DailyRunBudget:       50,
			},
		},
	})
	if err != nil {
		t.Fatalf("FeedService.Create: %v", err)
	}
	feedID := createResp.GetFeed().GetId()
	if feedID == 0 {
		t.Fatal("created feed has no id")
	}

	t.Run("succeeded run terminates the stream with SUCCEEDED", func(t *testing.T) {
		runID, err := app.Store.StartRun(ctx, feedID, "manual", "e2e-watch-worker")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		// No artificial delay before the commit: Watch's own poll loop over
		// the real bridge is the synchronization primitive, and this
		// assertion holds regardless of interleaving — a fixed sleep here
		// guarded nothing (A0-T04).
		go func() {
			if err := app.Store.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
				t.Errorf("CommitRun: %v", err)
			}
		}()

		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := login.Clients.Run.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
		if err != nil {
			t.Fatalf("RunService.Watch (over the real bridge): %v", err)
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
			t.Fatal("Watch over the bridge delivered no messages before EOF")
		}
		if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
			t.Fatalf("terminal status = %v, want SUCCEEDED (run_id=%d)", last.GetRun().GetStatus(), runID)
		}
		if last.GetRun().GetId() != runID {
			t.Fatalf("terminal Run.id = %d, want %d", last.GetRun().GetId(), runID)
		}
	})

	t.Run("failed run terminates the stream with FAILED and error kind", func(t *testing.T) {
		runID, err := app.Store.StartRun(ctx, feedID, "manual", "e2e-watch-worker")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		// See the succeeded-run case above: no artificial delay needed (A0-T04).
		go func() {
			if err := app.Store.FailRun(ctx, runID, "transient", "provider timed out", store.RunSummary{}); err != nil {
				t.Errorf("FailRun: %v", err)
			}
		}()

		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := login.Clients.Run.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
		if err != nil {
			t.Fatalf("RunService.Watch (over the real bridge): %v", err)
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
			t.Fatal("Watch over the bridge delivered no messages before EOF")
		}
		if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_FAILED {
			t.Fatalf("terminal status = %v, want FAILED", last.GetRun().GetStatus())
		}
		if last.GetRun().GetErrorKind() != affv1.ErrorKind_ERROR_KIND_TRANSIENT {
			t.Fatalf("terminal error kind = %v, want TRANSIENT", last.GetRun().GetErrorKind())
		}
	})

	t.Run("already-finished run returns immediately over the bridge", func(t *testing.T) {
		runID, err := app.Store.StartRun(ctx, feedID, "manual", "e2e-watch-worker")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if err := app.Store.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
			t.Fatalf("CommitRun: %v", err)
		}

		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		start := time.Now()
		stream, err := login.Clients.Run.Watch(watchCtx, &affv1.RunServiceWatchRequest{RunId: runID})
		if err != nil {
			t.Fatalf("RunService.Watch: %v", err)
		}
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		if msg.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
			t.Fatalf("status = %v, want SUCCEEDED", msg.GetRun().GetStatus())
		}
		if _, err := stream.Recv(); err != io.EOF {
			t.Fatalf("expected EOF after the single terminal snapshot, got: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Watch on an already-finished run took %v over the bridge, want near-instant", elapsed)
		}
	})

	// Live progress ticks (RunProgress messages carrying candidate/committed
	// counts, as opposed to the terminal Run snapshot asserted above) cannot
	// be produced here: see this test function's doc comment. Skipped with
	// the precise blocker rather than faked.
	t.Run("live progress ticks", func(t *testing.T) {
		t.Skip("e2e: RunProgressReporter (internal/rpc/run.go) has no production caller yet — " +
			"internal/generate's runner is the intended emitter and is owned by another agent " +
			"concurrently with this change, and this package's own harness (internal/e2e/app.go) " +
			"does not expose *rpc.RunServer's ReportCandidate/ReportCommitted to a test either. " +
			"Faking an emitter here would test this file's own fake, not the real contract. " +
			"Covered directly against the RPC layer by " +
			"internal/rpc/run_test.go's TestWatchRelaysProgressBeforeTerminal.")
	})
}
