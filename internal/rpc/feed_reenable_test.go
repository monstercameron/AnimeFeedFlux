package rpc

import (
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// Re-enabling a feed must clear its consecutive-failure count.
//
// The scheduler auto-disables a feed after maxConsecutiveFailures (5) and now
// persists both the count and the disable, so they survive a restart — which
// is the point. But the count outliving the disable turns a re-enable into a
// single-shot: the operator switches the feed back on, it fails once, and the
// stored 5 makes that the sixth, so it auto-disables again immediately
// instead of getting the five attempts the threshold promises.
//
// Enabling is the operator saying "try again". The count that caused the
// disable must not be what decides the retry.
//
// Only on enable: disabling by hand leaves the count alone, because that is
// diagnostic history about why the feed was switched off and nothing is being
// retried.
func TestSetEnabledClearsConsecutiveFailures(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)
	created := mustCreateFeed(t, s, feedTestFeed("flaky-feed"))

	// Stand in for the scheduler having auto-disabled it.
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET consecutive_failures = 5, enabled = 0 WHERE id = ?`, created.GetId(),
	); err != nil {
		t.Fatalf("seeding failure state: %v", err)
	}

	resp, err := s.SetEnabled(t.Context(), &affv1.FeedServiceSetEnabledRequest{
		FeedId: created.GetId(), Enabled: true, ExpectedVersion: created.GetVersion(),
	})
	if err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if got := feedFailureCount(t, s, created.GetId()); got != 0 {
		t.Errorf("consecutive_failures after re-enable = %d, want 0 — the feed would auto-disable "+
			"again on its very next failure instead of getting %d attempts", got, 5)
	}

	// Disabling by hand keeps the history.
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET consecutive_failures = 3 WHERE id = ?`, created.GetId(),
	); err != nil {
		t.Fatalf("seeding count: %v", err)
	}
	if _, err := s.SetEnabled(t.Context(), &affv1.FeedServiceSetEnabledRequest{
		FeedId: created.GetId(), Enabled: false, ExpectedVersion: resp.GetFeed().GetVersion(),
	}); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if got := feedFailureCount(t, s, created.GetId()); got != 3 {
		t.Errorf("consecutive_failures after a manual disable = %d, want 3 kept as history", got)
	}
}

func feedFailureCount(t *testing.T, s *FeedServer, feedID int64) int {
	t.Helper()
	var n int
	if err := s.store.Reader().QueryRowContext(t.Context(),
		`SELECT consecutive_failures FROM feeds WHERE id = ?`, feedID).Scan(&n); err != nil {
		t.Fatalf("reading consecutive_failures: %v", err)
	}
	return n
}
