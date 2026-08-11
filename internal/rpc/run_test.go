package rpc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// runTestStore opens a fresh migrated database in a temp directory, mirroring
// internal/store's own openTemp/newTestStore helpers but built from exported
// API only, since this package must not edit internal/store.
func runTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func runTestFeed(t *testing.T, s *store.Store, slug string) int64 {
	t.Helper()
	id, err := s.CreateFeed(t.Context(), model.Feed{
		Slug:     slug,
		Title:    "Feed " + slug,
		Kind:     model.KindGenerative,
		Timezone: "UTC",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create feed %q: %v", slug, err)
	}
	return id
}

// runStoreStatus reads a run's raw status column directly, since this
// package's own Get/History always translate it through runStatusToProto —
// tests that need to see the untranslated store truth (e.g. "Watch never
// touched the run") must bypass that translation.
func runStoreStatus(t *testing.T, s *store.Store, runID int64) string {
	t.Helper()
	var status string
	if err := s.Writer().QueryRowContext(t.Context(),
		`SELECT status FROM runs WHERE id = ?`, runID).Scan(&status); err != nil {
		t.Fatalf("reading status for run %d: %v", runID, err)
	}
	return status
}

// waitForHubSubscriber blocks until runID has at least one live subscriber
// on h, or fails the test after timeout. It is the real synchronization
// primitive for "Watch has subscribed" (A0-T04): runProgressHub has no
// external "subscribed" signal to select on (subscribe happens deep inside
// Watch, a production method this package must not change to add one), so
// this polls the hub's own internal state — through its own mutex, same
// package — instead of guessing a delay. The poll interval is short and
// bounded by a generous deadline, so it converges to the real event as soon
// as it happens rather than racing a fixed sleep against it.
func waitForHubSubscriber(t *testing.T, h *runProgressHub, runID int64, timeout time.Duration) {
	t.Helper()
	waitForHubSubscriberCount(t, h, runID, 1, timeout)
}

// waitForHubSubscriberCount is waitForHubSubscriber generalized to an exact
// subscriber count, for tests with more than one concurrent watcher.
func waitForHubSubscriberCount(t *testing.T, h *runProgressHub, runID int64, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		h.mu.Lock()
		got := len(h.subs[runID])
		h.mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d: %d subscriber(s) after %s, want >= %d", runID, got, timeout, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForSends blocks until fake has received at least want messages.
//
// Subscriber count alone is NOT enough to say a watcher is ready: Watch
// subscribes to the hub BEFORE its initial poll (deliberately — see Watch's
// comment on why that window is closed in that direction), so a test that
// publishes as soon as the subscriber appears can still have its run commit
// and go terminal before that initial poll reads the row. The watcher then
// sends one terminal snapshot and returns without ever entering its select
// loop, and every progress tick published in between is correctly, and
// invisibly, never delivered. Waiting for the initial snapshot to actually
// land is the real condition (A0-T04) that "the watcher is now live".
func waitForSends(t *testing.T, fake *fakeWatchStream, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := fake.sent(); got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher sent %d message(s) after %s, want >= %d", fake.sent(), timeout, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// fakeWatchStream implements grpc.ServerStreamingServer[RunServiceWatchResponse]
// without a real transport, so Watch can be tested as a plain function call.
// sendErr, when set, is returned by Send once len(got) reaches failAfter —
// simulating a client that drops mid-stream (PLAN.md §22 J9) without ever
// canceling ctx, which is a distinct failure mode from context cancellation.
type fakeWatchStream struct {
	ctx       context.Context
	sendErr   error
	failAfter int

	// mu guards got. Watch runs on its own goroutine in the concurrent
	// tests, so a test that inspects progress WHILE the watcher is still
	// streaming (see waitForSends) would otherwise race the append. Tests
	// that only read got after Watch has returned are ordered by that
	// return and need no lock, but taking it costs nothing and keeps the
	// rule "got is only touched under mu" simple enough to not get wrong.
	mu  sync.Mutex
	got []*affv1.RunServiceWatchResponse
}

func (f *fakeWatchStream) Send(m *affv1.RunServiceWatchResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil && len(f.got) >= f.failAfter {
		return f.sendErr
	}
	f.got = append(f.got, m)
	return nil
}

// sent reports how many messages this stream has received so far, safe to
// call while Watch is still running.
func (f *fakeWatchStream) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}
func (f *fakeWatchStream) Context() context.Context     { return f.ctx }
func (f *fakeWatchStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeWatchStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeWatchStream) SetTrailer(metadata.MD)       {}
func (f *fakeWatchStream) SendMsg(m any) error          { return nil }
func (f *fakeWatchStream) RecvMsg(m any) error          { return nil }

func TestRunWatchTerminatesOnSuccess(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

	// No delay before the commit: Watch's own poll loop (watchPoll above) is
	// what discovers the terminal state, on whatever interleaving actually
	// happens, exactly as TestRunWatchTerminatesBetweenSubscribeAndFirstPoll
	// already proves across 20 forced interleavings — a fixed sleep here
	// would guard nothing a real synchronization primitive doesn't already
	// cover, so it was removed rather than kept as decoration (A0-T04).
	if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not terminate after the run succeeded")
	}

	if len(fake.got) == 0 {
		t.Fatal("Watch sent no updates")
	}
	last := fake.got[len(fake.got)-1]
	if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
		t.Fatalf("last status = %v, want SUCCEEDED", last.GetRun().GetStatus())
	}
}

func TestRunWatchTerminatesOnFailure(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

	// See TestRunWatchTerminatesOnSuccess: no artificial delay needed before
	// the terminal write — Watch's poll loop is the real synchronization
	// primitive here (A0-T04).
	if err := s.FailRun(ctx, runID, "transient", "provider timed out", store.RunSummary{}); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not terminate after the run failed")
	}

	last := fake.got[len(fake.got)-1]
	if last.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("last status = %v, want FAILED", last.GetRun().GetStatus())
	}
	if last.GetRun().GetErrorKind() != affv1.ErrorKind_ERROR_KIND_TRANSIENT {
		t.Fatalf("error kind = %v, want TRANSIENT", last.GetRun().GetErrorKind())
	}
}

// TestWatchDroppedStreamDoesNotAbortRun is PLAN.md §22 J9's central
// assertion: "a dropped socket does not abort the run — the run is not the
// stream's to cancel." Send fails immediately, as a broken connection would,
// while the run itself is left running for real, never committed or failed.
func TestWatchDroppedStreamDoesNotAbortRun(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx, sendErr: errors.New("client gone"), failAfter: 0}

	err = srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake)
	if err == nil {
		t.Fatal("Watch should return the Send error when the client is gone")
	}

	if got := runStoreStatus(t, s, runID); got != "running" {
		t.Fatalf("run status after dropped stream = %q, want still \"running\" (Watch must not touch the run)", got)
	}
}

// TestWatchCancellationDoesNotAbortRun covers the other disconnect path: the
// client context is canceled directly (no transport-level Send error).
// Watch must stop polling without ever touching the run.
func TestWatchCancellationDoesNotAbortRun(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: watchCtx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

	// No delay before cancel: the assertion (run status still "running", i.e.
	// Watch never touches the run) holds whether cancellation lands before
	// Watch's first poll, mid-poll, or after — a fixed sleep guarded nothing
	// a real primitive doesn't (A0-T04).
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error on cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not terminate after client cancellation")
	}

	if got := runStoreStatus(t, s, runID); got != "running" {
		t.Fatalf("run status after cancellation = %q, want still \"running\" (Watch must not touch the run)", got)
	}
}

// TestWatchFinishedRunReturnsImmediately is §22 J9's reconnect case: "the
// UI reconnects after a drop and will do exactly this [watch an
// already-finished run]" must not block for up to watchPoll waiting on the
// first tick. watchPoll is set deliberately large so a pass that actually
// waited for a tick would time out this test long before any reasonable CI
// timeout, making a regression to "wait for the ticker" impossible to miss.
func TestWatchFinishedRunReturnsImmediately(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	srv := NewRunServer(s, nil)
	srv.watchPoll = time.Hour
	fake := &fakeWatchStream{ctx: ctx}

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch on an already-finished run did not return immediately")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Watch on an already-finished run took %v, want near-instant (it must not wait for watchPoll)", elapsed)
	}
	if len(fake.got) != 1 {
		t.Fatalf("got %d messages, want exactly 1 (the immediate terminal snapshot)", len(fake.got))
	}
	if fake.got[0].GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
		t.Fatalf("status = %v, want SUCCEEDED", fake.got[0].GetRun().GetStatus())
	}
}

// TestWatchRelaysProgressBeforeTerminal exercises RunProgressReporter (the
// contract §12.4's live progress pane and TODOS.md BF-43 depend on): a
// candidate tick and a committed tick, reported while the run is still
// running, must reach the watcher as distinct RunProgress messages — never
// folded into `run`, and the candidate tick must never carry a committed
// count (BF-43: "progress events never claim items that were not
// committed"). The terminal Run message must still arrive last.
func TestWatchRelaysProgressBeforeTerminal(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	srv := NewRunServer(s, nil)
	// Modest, not huge: Watch must still notice the run finishing via its
	// own periodic poll (nothing wakes it on commit), while staying slow
	// enough that the progress ticks below reliably land as their own
	// distinct messages ahead of the next poll tick.
	srv.watchPoll = 20 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

	// Wait for Watch to actually subscribe to the hub (a real synchronization
	// primitive, not a guessed delay — A0-T04): publishing before that
	// happens would be silently dropped, since runProgressHub.publish only
	// fans out to whatever is subscribed *at that instant* (see its doc
	// comment) and keeps no backlog for a late subscriber.
	waitForHubSubscriber(t, srv.progress, runID, 2*time.Second)

	srv.ReportCandidate(runID, "calling_model", 2)
	srv.ReportCommitted(runID, 1)

	item := model.Item{
		FeedID:      feedID,
		ItemKey:     "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		ContentHash: "hash-progress-test",
		Title:       "Title",
		SummaryText: "summary",
		BodyHTML:    "<p>body</p>",
		PublishedAt: time.Now().UTC(),
		Origin:      model.OriginGenerated,
	}
	if err := s.CommitRun(ctx, runID, []model.Item{item}, store.RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not terminate after the run finished")
	}

	if len(fake.got) < 3 {
		t.Fatalf("got %d messages, want at least 3 (initial snapshot, candidate tick, committed tick, ..., terminal); got %+v", len(fake.got), fake.got)
	}

	// message 0 is always the initial Run snapshot (see Watch's doc comment
	// on why it precedes the tick loop).
	if fake.got[0].GetRun() == nil || fake.got[0].GetProgress() != nil {
		t.Fatalf("message 0 = %+v, want the initial Run snapshot with no progress", fake.got[0])
	}

	var candidateIdx, committedIdx = -1, -1
	for i, m := range fake.got {
		if p := m.GetProgress(); p != nil {
			if p.GetPhase() == "calling_model" {
				candidateIdx = i
				if p.GetCandidatesSeen() != 2 || p.GetItemsCommitted() != 0 {
					t.Fatalf("candidate progress = %+v, want candidates_seen=2 items_committed=0", p)
				}
				if m.GetRun() != nil {
					t.Fatalf("candidate tick unexpectedly carries a Run: %+v", m)
				}
			}
			if p.GetItemsCommitted() == 1 {
				committedIdx = i
				if p.GetCandidatesSeen() != 0 {
					t.Fatalf("committed progress = %+v, want candidates_seen=0 (never claiming a candidate as committed)", p)
				}
				if m.GetRun() != nil {
					t.Fatalf("committed tick unexpectedly carries a Run: %+v", m)
				}
			}
		}
	}
	if candidateIdx == -1 {
		t.Fatalf("never received the candidate tick; got %+v", fake.got)
	}
	if committedIdx == -1 {
		t.Fatalf("never received the committed tick; got %+v", fake.got)
	}
	if committedIdx <= candidateIdx {
		t.Fatalf("committed tick (index %d) did not come after candidate tick (index %d)", committedIdx, candidateIdx)
	}

	terminal := fake.got[len(fake.got)-1]
	if terminal.GetProgress() != nil {
		t.Fatalf("terminal message unexpectedly carries progress: %+v", terminal)
	}
	if terminal.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_SUCCEEDED {
		t.Fatalf("terminal status = %v, want SUCCEEDED", terminal.GetRun().GetStatus())
	}
	if got := terminal.GetRun().GetItemsAdded(); got != 1 {
		t.Fatalf("terminal items_added = %d, want 1 (from the actual commit, not the progress tick)", got)
	}
	if committedIdx >= len(fake.got)-1 {
		t.Fatalf("committed tick must come strictly before the terminal message, got committedIdx=%d of %d messages", committedIdx, len(fake.got))
	}
}

// TestWatchConcurrentWatchersBothReceiveProgress documents and asserts the
// two-watchers decision: independent subscriptions, both fanned out to
// identically (runProgressHub's doc comment). Neither watcher's presence,
// absence, or disconnect affects the other or the run.
func TestWatchConcurrentWatchersBothReceiveProgress(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	srv := NewRunServer(s, nil)
	srv.watchPoll = 20 * time.Millisecond
	fakeA := &fakeWatchStream{ctx: ctx}
	fakeB := &fakeWatchStream{ctx: ctx}

	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() { doneA <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fakeA) }()
	go func() { doneB <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fakeB) }()

	// Wait for both watchers to actually subscribe (A0-T04: a real condition,
	// not a guessed delay) — see TestWatchRelaysProgressBeforeTerminal for
	// why publishing before that would be silently dropped.
	waitForHubSubscriberCount(t, srv.progress, runID, 2, 2*time.Second)
	// ...and past their initial poll, or CommitRun below can beat that poll
	// and the watcher terminates on the snapshot without ever reading the
	// hub. See waitForSends.
	waitForSends(t, fakeA, 1, 2*time.Second)
	waitForSends(t, fakeB, 1, 2*time.Second)
	srv.ReportCandidate(runID, "calling_model", 1)

	if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	for name, done := range map[string]chan error{"A": doneA, "B": doneB} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("watcher %s: Watch returned error: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("watcher %s: Watch did not terminate", name)
		}
	}

	for name, fake := range map[string]*fakeWatchStream{"A": fakeA, "B": fakeB} {
		var sawProgress bool
		for _, m := range fake.got {
			if p := m.GetProgress(); p != nil && p.GetPhase() == "calling_model" {
				sawProgress = true
			}
		}
		if !sawProgress {
			t.Fatalf("watcher %s never received the candidate tick; got %+v", name, fake.got)
		}
	}
}

// --- runProgressHub failure-mode tests -------------------------------------
//
// These exercise the hub directly (bypassing Watch/RunServer entirely) for
// the failure modes that matter in production: they are hub invariants, not
// Watch behavior, so testing them at the hub avoids incidental timing from
// watchPoll ticks and stream sends.

// TestRunProgressHubSlowWatcherNeverBlocksPublish is the central promise
// runProgressHub's own doc comment makes: "a slow or gone watcher must
// never stall the generator." A watcher that never reads fills its
// runProgressChanBuf-sized channel after a handful of ticks; every publish
// after that must still return immediately (select/default drops the tick)
// rather than blocking on a full channel, in both the candidate and
// committed paths.
func TestRunProgressHubSlowWatcherNeverBlocksPublish(t *testing.T) {
	h := newRunProgressHub(nil)
	const runID = int64(1)
	ch := h.subscribe(runID)
	defer h.unsubscribe(runID, ch)

	const attempts = runProgressChanBuf * 4 // well past the buffer, on both paths
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < attempts; i++ {
			h.publish(runID, &affv1.RunProgress{Phase: "calling_model", CandidatesSeen: int32(i)})
			h.publishCommitted(runID, int32(i))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish/publishCommitted blocked on a watcher that never reads its channel")
	}

	// The channel itself must never have grown past its declared capacity —
	// proof the drop happened via select/default, not a data race that
	// happened to finish before the timeout.
	if len(ch) > runProgressChanBuf {
		t.Fatalf("watcher channel holds %d buffered ticks, want <= %d (runProgressChanBuf)", len(ch), runProgressChanBuf)
	}
}

// TestRunProgressHubConcurrentWatchersIndependent asserts the hub-level
// version of runProgressHub's documented decision: two subscriptions to the
// same run are fanned out to identically, and one disconnecting (unsubscribe)
// does not touch the other's channel or the hub's bookkeeping for it.
func TestRunProgressHubConcurrentWatchersIndependent(t *testing.T) {
	h := newRunProgressHub(nil)
	const runID = int64(1)
	chA := h.subscribe(runID)
	chB := h.subscribe(runID)

	h.publish(runID, &affv1.RunProgress{Phase: "calling_model", CandidatesSeen: 1})
	if got := <-chA; got.GetCandidatesSeen() != 1 {
		t.Fatalf("watcher A: candidates_seen = %d, want 1", got.GetCandidatesSeen())
	}
	if got := <-chB; got.GetCandidatesSeen() != 1 {
		t.Fatalf("watcher B: candidates_seen = %d, want 1", got.GetCandidatesSeen())
	}

	// Disconnect A. B must still receive subsequent ticks, and the hub's
	// subscriber set for this run must still contain exactly B.
	h.unsubscribe(runID, chA)
	if _, stillThere := h.subs[runID][chA]; stillThere {
		t.Fatal("unsubscribed channel A is still present in the hub's subscriber set")
	}
	if _, stillThere := h.subs[runID][chB]; !stillThere {
		t.Fatal("watcher B was removed by watcher A's unsubscribe")
	}

	h.publishCommitted(runID, 1)
	select {
	case got := <-chB:
		if got.GetItemsCommitted() != 1 {
			t.Fatalf("watcher B: items_committed = %d, want 1", got.GetItemsCommitted())
		}
	case <-time.After(time.Second):
		t.Fatal("watcher B did not receive the committed tick after A disconnected")
	}
	select {
	case v, ok := <-chA:
		if ok {
			t.Fatalf("unsubscribed watcher A unexpectedly received a tick: %+v", v)
		}
	default:
	}

	h.unsubscribe(runID, chB)
}

// TestRunProgressHubCommittedCountNeverRegresses is the direct test of the
// contract-enforcement decision documented on RunProgressReporter and
// runProgressHub.publishCommitted: a committed count that would go backwards
// for the same run is dropped, never delivered to a watcher, while a
// non-decreasing (including equal, including a jump) sequence is delivered
// in full.
func TestRunProgressHubCommittedCountNeverRegresses(t *testing.T) {
	h := newRunProgressHub(nil)
	const runID = int64(1)
	ch := h.subscribe(runID)
	defer h.unsubscribe(runID, ch)

	h.publishCommitted(runID, 2)
	if got := <-ch; got.GetItemsCommitted() != 2 {
		t.Fatalf("first tick items_committed = %d, want 2", got.GetItemsCommitted())
	}

	// A regression: must be dropped, not delivered.
	h.publishCommitted(runID, 1)

	// A later, valid, higher tick: must still arrive, and must be the very
	// next thing on the channel (proving the regression above was dropped,
	// not merely delayed/reordered).
	h.publishCommitted(runID, 5)
	select {
	case got := <-ch:
		if got.GetItemsCommitted() != 5 {
			t.Fatalf("next delivered tick items_committed = %d, want 5 (the regressing 1 must have been dropped)", got.GetItemsCommitted())
		}
	case <-time.After(time.Second):
		t.Fatal("valid tick after a dropped regression was never delivered")
	}

	select {
	case v := <-ch:
		t.Fatalf("unexpected extra tick delivered: %+v (want exactly 2 delivered ticks total)", v)
	default:
	}
}

// TestRunProgressHubCommittedWithNoWatcherIsNotTracked documents the other
// half of the lastCommitted lifecycle decision: with no subscriber, nothing
// is observable, so publishCommitted must not silently start tracking a
// baseline that would need cleanup later (see lastCommitted's doc comment
// for why that matters — most runs are never watched at all).
func TestRunProgressHubCommittedWithNoWatcherIsNotTracked(t *testing.T) {
	h := newRunProgressHub(nil)
	const runID = int64(1)

	h.publishCommitted(runID, 100) // nobody watching yet
	if _, tracked := h.lastCommitted[runID]; tracked {
		t.Fatal("a committed tick with no subscriber must not create a lastCommitted entry")
	}

	// A late, lower value now that still nobody is watching must be
	// accepted the same way (there is nothing to protect), not rejected as
	// a false "regression" against the earlier untracked 100.
	ch := h.subscribe(runID)
	defer h.unsubscribe(runID, ch)
	h.publishCommitted(runID, 3)
	select {
	case got := <-ch:
		if got.GetItemsCommitted() != 3 {
			t.Fatalf("items_committed = %d, want 3 (must not be rejected against an untracked earlier value)", got.GetItemsCommitted())
		}
	case <-time.After(time.Second):
		t.Fatal("tick never delivered")
	}
}

// TestRunProgressHubNoLeakAcrossManySubscribeCycles is the leak check this
// task calls out explicitly: a fan-out hub that grows a map entry per
// reconnect is a slow death on a 2 GB box, and reconnects are routine here
// (the whole control-plane API is one WebSocket — any drop reconnects).
// Subscribing and unsubscribing many times, for many distinct run ids, and
// with committed ticks reported in between (to exercise lastCommitted's
// lifecycle too) must leave the hub's internal maps completely empty
// afterward, not merely small.
func TestRunProgressHubNoLeakAcrossManySubscribeCycles(t *testing.T) {
	h := newRunProgressHub(nil)

	const cycles = 500
	for i := 0; i < cycles; i++ {
		runID := int64(i % 7) // several distinct runs, each reconnected many times
		ch := h.subscribe(runID)
		h.publish(runID, &affv1.RunProgress{Phase: "calling_model", CandidatesSeen: 1})
		h.publishCommitted(runID, int32(i))
		h.unsubscribe(runID, ch)
	}

	h.mu.Lock()
	subsLen, lastCommittedLen := len(h.subs), len(h.lastCommitted)
	h.mu.Unlock()

	if subsLen != 0 {
		t.Fatalf("hub.subs has %d entries after every watcher unsubscribed, want 0", subsLen)
	}
	if lastCommittedLen != 0 {
		t.Fatalf("hub.lastCommitted has %d entries after every watcher unsubscribed, want 0", lastCommittedLen)
	}
}

// TestRunWatchTerminatesBetweenSubscribeAndFirstPoll covers the narrow
// window Watch's own doc comment calls out: subscribe happens before the
// initial poll specifically so a run finishing in that gap is not missed.
// It races CommitRun against the very start of Watch — launched with no
// delay at all, so the commit can land before subscribe, between subscribe
// and the initial read, or after it — repeated many times so every
// interleaving is exercised across the run, not just one lucky ordering.
// watchPoll is kept small (as in the other Watch tests) because the
// property under test is "always terminates promptly, regardless of when
// the race lands," not "the initial read alone must catch it" — a run that
// finishes just after the initial read is still bounded by the next poll
// tick, never left hanging.
func TestRunWatchTerminatesBetweenSubscribeAndFirstPoll(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond

	for i := 0; i < 20; i++ {
		runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		go func() {
			if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
				t.Errorf("commit run %d: %v", i, err)
			}
		}()

		fake := &fakeWatchStream{ctx: ctx}
		done := make(chan error, 1)
		go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("run %d: Watch returned error: %v", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("run %d: Watch did not terminate", i)
		}
		if len(fake.got) == 0 {
			t.Fatalf("run %d: Watch sent no updates", i)
		}
	}
}

func TestRunHistoryFiltersByFeedStatusAndDateRange(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedA := runTestFeed(t, s, "feed-a")
	feedB := runTestFeed(t, s, "feed-b")

	// feedA: one success, one failure.
	okRun, err := s.StartRun(ctx, feedA, "manual", "w1")
	if err != nil {
		t.Fatalf("start ok run: %v", err)
	}
	if err := s.CommitRun(ctx, okRun, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit ok run: %v", err)
	}

	failRun, err := s.StartRun(ctx, feedA, "manual", "w1")
	if err != nil {
		t.Fatalf("start fail run: %v", err)
	}
	if err := s.FailRun(ctx, failRun, "fatal", "boom", store.RunSummary{}); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	// feedB: one success, outside feedA's filter.
	otherFeedRun, err := s.StartRun(ctx, feedB, "manual", "w2")
	if err != nil {
		t.Fatalf("start feedB run: %v", err)
	}
	if err := s.CommitRun(ctx, otherFeedRun, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit feedB run: %v", err)
	}

	srv := NewRunServer(s, nil)

	t.Run("filters by feed", func(t *testing.T) {
		resp, err := srv.History(ctx, &affv1.RunServiceHistoryRequest{FeedId: feedA})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(resp.GetRuns()) != 2 {
			t.Fatalf("got %d runs for feedA, want 2", len(resp.GetRuns()))
		}
		for _, r := range resp.GetRuns() {
			if r.GetFeedId() != feedA {
				t.Fatalf("run %d has feed_id %d, want %d", r.GetId(), r.GetFeedId(), feedA)
			}
		}
	})

	t.Run("filters by status", func(t *testing.T) {
		resp, err := srv.History(ctx, &affv1.RunServiceHistoryRequest{
			FeedId: feedA,
			Status: affv1.RunStatus_RUN_STATUS_FAILED,
		})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(resp.GetRuns()) != 1 || resp.GetRuns()[0].GetId() != failRun {
			t.Fatalf("filtered by FAILED = %+v, want exactly run %d", resp.GetRuns(), failRun)
		}
	})

	t.Run("filters by date range excludes everything before now", func(t *testing.T) {
		future := timestamppb.New(time.Now().Add(time.Hour))
		resp, err := srv.History(ctx, &affv1.RunServiceHistoryRequest{StartedAfter: future})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(resp.GetRuns()) != 0 {
			t.Fatalf("got %d runs started after the future, want 0", len(resp.GetRuns()))
		}
	})

	t.Run("newest first", func(t *testing.T) {
		resp, err := srv.History(ctx, &affv1.RunServiceHistoryRequest{})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(resp.GetRuns()) != 3 {
			t.Fatalf("got %d runs, want 3", len(resp.GetRuns()))
		}
		for i := 1; i < len(resp.GetRuns()); i++ {
			if resp.GetRuns()[i-1].GetId() < resp.GetRuns()[i].GetId() {
				t.Fatalf("runs not newest-first: %+v", resp.GetRuns())
			}
		}
	})
}

func TestRunHistoryPaginates(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	const total = 5
	var ids []int64
	for i := 0; i < total; i++ {
		id, err := s.StartRun(ctx, feedID, "manual", fmt.Sprintf("w%d", i))
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if err := s.CommitRun(ctx, id, nil, store.RunSummary{}); err != nil {
			t.Fatalf("commit run %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	srv := NewRunServer(s, nil)

	var seen []int64
	token := ""
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatal("pagination did not terminate")
		}
		resp, err := srv.History(ctx, &affv1.RunServiceHistoryRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatalf("history page: %v", err)
		}
		for _, r := range resp.GetRuns() {
			seen = append(seen, r.GetId())
		}
		if resp.GetNextPageToken() == "" {
			break
		}
		token = resp.GetNextPageToken()
	}

	if len(seen) != total {
		t.Fatalf("paginated through %d runs, want %d (got %v, seeded %v)", len(seen), total, seen, ids)
	}
	seenSet := map[int64]bool{}
	for _, id := range seen {
		if seenSet[id] {
			t.Fatalf("run %d returned twice across pages", id)
		}
		seenSet[id] = true
	}
}

func TestRunDeleteWorksAndNoUpdateExists(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "w1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	srv := NewRunServer(s, nil)
	if _, err := srv.Delete(ctx, &affv1.RunServiceDeleteRequest{RunId: runID}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := srv.Get(ctx, &affv1.RunServiceGetRequest{RunId: runID}); err == nil {
		t.Fatal("run should be gone after Delete")
	}

	// Deleting again must fail rather than silently succeed a second time.
	if _, err := srv.Delete(ctx, &affv1.RunServiceDeleteRequest{RunId: runID}); err == nil {
		t.Fatal("deleting an already-deleted run should error")
	}

	// PLAN.md §12.4: "Runs support delete ... but not edit". Asserted
	// structurally: the generated server interface has no Update method at
	// all, not merely one this type declines to implement.
	typ := reflect.TypeOf((*affv1.RunServiceServer)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		if typ.Method(i).Name == "Update" {
			t.Fatal("RunServiceServer must not have an Update method")
		}
	}
}

func TestRunDeleteDetachesItemsWithoutRemovingThem(t *testing.T) {
	s := runTestStore(t)
	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")

	runID, err := s.StartRun(ctx, feedID, "manual", "w1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	item := model.Item{
		FeedID:      feedID,
		ItemKey:     "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		ContentHash: "hash-1",
		Title:       "Title",
		SummaryText: "summary",
		BodyHTML:    "<p>body</p>",
		PublishedAt: time.Now().UTC(),
		Origin:      model.OriginGenerated,
	}
	if err := s.CommitRun(ctx, runID, []model.Item{item}, store.RunSummary{}); err != nil {
		t.Fatalf("commit run with item: %v", err)
	}

	srv := NewRunServer(s, nil)
	if _, err := srv.Delete(ctx, &affv1.RunServiceDeleteRequest{RunId: runID}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var itemCount int
	if err := s.Writer().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE item_key = ?`, item.ItemKey,
	).Scan(&itemCount); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("item count after deleting its run = %d, want 1 (deleting a run must not delete published items)", itemCount)
	}
}
