package main

import (
	"context"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// The scheduler's consecutive-failure count and auto-disable were in-memory
// only, which had three consequences worth pinning separately:
//
//   - feeds.consecutive_failures, a real column carried on the Feed proto,
//     was never written, so it read 0 no matter how often a feed failed;
//   - feeds.enabled was never cleared by an auto-disable, so the admin UI's
//     toggle showed a feed as Enabled while the scheduler silently refused to
//     dispatch it;
//   - a restart wiped both, handing a persistently broken feed a clean slate
//     to burn another five runs against.
func TestFeedFailureSinkPersistsCountAndDisable(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "failing-feed", true)
	other := seedFeed(t, st, "healthy-feed", true)
	sink := feedFailureSink{st: st, log: discardLogger()}

	for i := 1; i <= 3; i++ {
		if err := sink.RecordFailure(feedID); err != nil {
			t.Fatalf("RecordFailure #%d: %v", i, err)
		}
		if got := failureCount(t, st, feedID); got != i {
			t.Fatalf("consecutive_failures after %d failures = %d", i, got)
		}
	}
	// Only the feed that failed.
	if got := failureCount(t, st, other); got != 0 {
		t.Errorf("an unrelated feed's count moved to %d", got)
	}

	// A success clears it: "consecutive" means consecutive.
	if err := sink.ResetFailures(feedID); err != nil {
		t.Fatalf("ResetFailures: %v", err)
	}
	if got := failureCount(t, st, feedID); got != 0 {
		t.Errorf("consecutive_failures after a success = %d, want 0", got)
	}

	// The disable has to reach the column the UI reads, or the toggle keeps
	// claiming the feed is enabled while nothing dispatches it.
	if err := sink.DisableFeed(feedID); err != nil {
		t.Fatalf("DisableFeed: %v", err)
	}
	var enabled int
	var version int64
	if err := st.Reader().QueryRowContext(t.Context(),
		`SELECT enabled, version FROM feeds WHERE id = ?`, feedID).Scan(&enabled, &version); err != nil {
		t.Fatalf("reading feed: %v", err)
	}
	if enabled != 0 {
		t.Errorf("feed still enabled=%d after auto-disable — the UI would show it as running", enabled)
	}
	if version <= 1 {
		t.Errorf("version = %d; the disable must bump it so an open tab conflicts instead of overwriting", version)
	}
}

// LoadFailures is what makes the count survive a restart.
func TestFeedFailureSinkLoadsCountsForSeeding(t *testing.T) {
	st := openTestStore(t)
	a := seedFeed(t, st, "feed-a", true)
	b := seedFeed(t, st, "feed-b", true)
	sink := feedFailureSink{st: st, log: discardLogger()}

	for range 4 {
		if err := sink.RecordFailure(a); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	counts, err := sink.LoadFailures()
	if err != nil {
		t.Fatalf("LoadFailures: %v", err)
	}
	if counts[a] != 4 {
		t.Errorf("counts[a] = %d, want 4 — a feed one failure from auto-disable would restart at zero", counts[a])
	}
	if counts[b] != 0 {
		t.Errorf("counts[b] = %d, want 0", counts[b])
	}
}

func failureCount(t *testing.T, st *store.Store, feedID int64) int {
	t.Helper()
	var n int
	if err := st.Reader().QueryRowContext(context.Background(),
		`SELECT consecutive_failures FROM feeds WHERE id = ?`, feedID).Scan(&n); err != nil {
		t.Fatalf("reading consecutive_failures: %v", err)
	}
	return n
}
