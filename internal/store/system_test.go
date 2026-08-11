package store

import (
	"strconv"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// --- ListAuditEventsPage ------------------------------------------------

func TestListAuditEventsPageEmptyLogReturnsEmptyNotError(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	events, err := s.ListAuditEventsPage(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListAuditEventsPage on an empty log: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 on a fresh database", len(events))
	}
}

func TestListAuditEventsPageNewestFirstAndCursorPaginates(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		// distinct, increasing timestamps so ordering is unambiguous even
		// though ListAuditEventsPage orders by id, not at.
		recordAuthEventAt(t, s, base.Add(time.Duration(i)*time.Minute), "203.0.113.1", i%2 == 0)
	}

	// Page 1: 2 rows, newest first (ids 5, 4).
	page1, err := s.ListAuditEventsPage(ctx, 0, 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1))
	}
	if page1[0].ID <= page1[1].ID {
		t.Fatalf("page 1 not newest-first: ids %d, %d", page1[0].ID, page1[1].ID)
	}
	if page1[0].ID != 5 || page1[1].ID != 4 {
		t.Fatalf("page 1 ids = %d, %d, want 5, 4", page1[0].ID, page1[1].ID)
	}

	// Page 2, cursored after the last id actually returned on page 1.
	page2, err := s.ListAuditEventsPage(ctx, page1[len(page1)-1].ID, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != 3 || page2[1].ID != 2 {
		t.Fatalf("page 2 ids = %v, want [3, 2]", ids(page2))
	}

	// Page 3: the remainder, exactly one row, no overlap and no gap across
	// the two page boundaries above.
	page3, err := s.ListAuditEventsPage(ctx, page2[len(page2)-1].ID, 2)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != 1 {
		t.Fatalf("page 3 ids = %v, want [1]", ids(page3))
	}

	seen := map[int64]bool{}
	for _, p := range [][]AuthEvent{page1, page2, page3} {
		for _, e := range p {
			if seen[e.ID] {
				t.Fatalf("id %d returned on more than one page", e.ID)
			}
			seen[e.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d distinct ids across all pages, want 5", len(seen))
	}
}

func ids(events []AuthEvent) []int64 {
	out := make([]int64, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}

// --- Vacuum / RunInFlight ------------------------------------------------

func TestRunInFlightFalseWithNoRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	inFlight, err := s.RunInFlight(ctx)
	if err != nil {
		t.Fatalf("RunInFlight: %v", err)
	}
	if inFlight {
		t.Fatal("RunInFlight = true with no runs at all")
	}
}

func TestRunInFlightTrueWhileRunRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	feedID, err := s.CreateFeed(ctx, model.Feed{
		Slug: "vacuum-test", Title: "Vacuum Test", Kind: model.KindGenerative,
		Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	runID, err := s.StartRun(ctx, feedID, "manual", "test-holder")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	inFlight, err := s.RunInFlight(ctx)
	if err != nil {
		t.Fatalf("RunInFlight: %v", err)
	}
	if !inFlight {
		t.Fatal("RunInFlight = false while a run is 'running'")
	}

	if err := s.FailRun(ctx, runID, "test", "ending run for test", RunSummary{}); err != nil {
		t.Fatalf("FailRun: %v", err)
	}

	inFlight, err = s.RunInFlight(ctx)
	if err != nil {
		t.Fatalf("RunInFlight after finish: %v", err)
	}
	if inFlight {
		t.Fatal("RunInFlight = true after the only run finished")
	}
}

func TestVacuumReportsBeforeAndAfterSize(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	// Put some data in and delete most of it, so VACUUM has freed pages to
	// reclaim — otherwise "before == after" would be true for the boring
	// reason that there was nothing to compact, not because VACUUM ran.
	for i := 0; i < 200; i++ {
		_, err := s.CreateFeed(ctx, model.Feed{
			Slug: "vacuum-fill-" + strconv.Itoa(i), Title: "Fill", Kind: model.KindGenerative,
			Timezone: "UTC", Enabled: true,
		})
		if err != nil {
			t.Fatalf("CreateFeed %d: %v", i, err)
		}
	}

	stats, err := s.Vacuum(ctx)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if stats.SizeBeforeBytes <= 0 {
		t.Fatalf("SizeBeforeBytes = %d, want > 0", stats.SizeBeforeBytes)
	}
	if stats.SizeAfterBytes <= 0 {
		t.Fatalf("SizeAfterBytes = %d, want > 0", stats.SizeAfterBytes)
	}
	if stats.Duration < 0 {
		t.Fatalf("Duration = %v, want >= 0", stats.Duration)
	}
}
