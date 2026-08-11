package rpc

import (
	"fmt"
	"testing"
	"time"
)

// backoffTracker's map was never evicted: one entry per distinct peer
// address, retained for the life of the process. Inert behind a proxy, where
// every request arrives from one address; unbounded on a directly-reachable
// listener, where a slow scan from many sources grows it without limit
// (A8-38).
func TestBackoffTrackerEvictsExpiredEntries(t *testing.T) {
	tr := newBackoffTracker()
	start := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	for i := range 500 {
		tr.recordFailure(fmt.Sprintf("10.0.0.%d", i), start)
	}
	if got := len(tr.m); got != 500 {
		t.Fatalf("tracked %d addresses, want 500", got)
	}

	// One new failure long after those windows closed sweeps them.
	later := start.Add(backoffRetention + time.Hour)
	tr.recordFailure("10.9.9.9", later)
	if got := len(tr.m); got != 1 {
		t.Errorf("after a sweep the map holds %d entries, want 1 — it grows without limit", got)
	}
}

// An attacker pausing between attempts must not get a free reset: the entry,
// and the failure count that makes the next delay longer, has to outlive the
// window itself.
func TestBackoffTrackerKeepsRecentlyExpiredEntries(t *testing.T) {
	tr := newBackoffTracker()
	start := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	for range 5 {
		tr.recordFailure("10.0.0.1", start)
	}
	before := tr.m["10.0.0.1"].failures

	// Past the 60s cap, well inside retention.
	tr.recordFailure("10.0.0.2", start.Add(2*time.Minute))
	st, ok := tr.m["10.0.0.1"]
	if !ok {
		t.Fatal("an entry whose window just closed was swept; the next attempt would start from zero")
	}
	if st.failures != before {
		t.Errorf("failures = %d, want %d preserved", st.failures, before)
	}
}

// Sweeping must not disturb an address currently being blocked.
func TestBackoffTrackerSweepKeepsActiveWindows(t *testing.T) {
	tr := newBackoffTracker()
	start := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	for range 6 {
		tr.recordFailure("10.0.0.1", start)
	}
	if !tr.blocked("10.0.0.1", start.Add(time.Second)) {
		t.Fatal("precondition: the address should be inside its window")
	}
	tr.recordFailure("10.0.0.2", start.Add(time.Second))
	if !tr.blocked("10.0.0.1", start.Add(2*time.Second)) {
		t.Error("a sweep released an address that was still inside its backoff window")
	}
}
