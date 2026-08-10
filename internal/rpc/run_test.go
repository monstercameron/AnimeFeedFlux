package rpc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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

// fakeWatchStream implements grpc.ServerStreamingServer[RunServiceWatchResponse]
// without a real transport, so Watch can be tested as a plain function call.
// sendErr, when set, is returned by Send once len(got) reaches failAfter —
// simulating a client that drops mid-stream (PLAN.md §22 J9) without ever
// canceling ctx, which is a distinct failure mode from context cancellation.
type fakeWatchStream struct {
	ctx       context.Context
	sendErr   error
	failAfter int
	got       []*affv1.RunServiceWatchResponse
}

func (f *fakeWatchStream) Send(m *affv1.RunServiceWatchResponse) error {
	if f.sendErr != nil && len(f.got) >= f.failAfter {
		return f.sendErr
	}
	f.got = append(f.got, m)
	return nil
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

	go func() {
		time.Sleep(30 * time.Millisecond)
		if err := s.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
			t.Errorf("commit run: %v", err)
		}
	}()

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

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

	go func() {
		time.Sleep(30 * time.Millisecond)
		if err := s.FailRun(ctx, runID, "transient", "provider timed out", store.RunSummary{}); err != nil {
			t.Errorf("fail run: %v", err)
		}
	}()

	srv := NewRunServer(s, nil)
	srv.watchPoll = 5 * time.Millisecond
	fake := &fakeWatchStream{ctx: ctx}

	done := make(chan error, 1)
	go func() { done <- srv.Watch(&affv1.RunServiceWatchRequest{RunId: runID}, fake) }()

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

	time.Sleep(15 * time.Millisecond)
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
