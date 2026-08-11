package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// The run lock is the ONLY single-flight between a manual run and a scheduled
// one: FeedService.RunNow's doc says it deliberately does not reimplement
// single-flight and relies on StartRun/ErrRunInProgress. That lock goes stale
// on heartbeat age, and store.Heartbeat — the method whose whole job is
// renewing it — had no caller outside tests. So the lock expired a fixed
// three minutes after a run STARTED, whether or not it was still running, and
// the next caller was waved through to a second concurrent run on the same
// feed. Both call the provider; both bill.
//
// This drives keepRunAlive directly rather than a whole run: what needs
// proving is that the lock keeps being renewed, and a test that waited out
// the real three-minute window would be a three-minute test.
func TestKeepRunAliveRenewsTheLock(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "heartbeat-feed", true)
	ctx := t.Context()

	runID, err := st.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	before := heartbeatOf(t, st, runID)

	stop := keepRunAlive(ctx, st, discardLogger(), feedID, runID)
	defer stop()

	// One tick is 30s away, so drive the renewal directly to keep the test
	// fast; the ticker wiring is what keepRunAlive adds around it.
	//
	// Looped rather than called once: Windows' clock granularity is coarse
	// enough that StartRun's stamp and an immediately following Heartbeat can
	// land on the identical instant, which would fail an "advanced" assertion
	// for a reason that has nothing to do with the lock.
	deadline := time.Now().Add(2 * time.Second)
	var after time.Time
	for {
		if err := st.Heartbeat(ctx, runID); err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		after = heartbeatOf(t, st, runID)
		if after.After(before) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat never advanced: before=%v after=%v", before, after)
		}
	}

	// And while the lock is fresh, a second StartRun for the same feed must
	// be refused — the property RunNow depends on.
	if _, err := st.StartRun(ctx, feedID, "manual", "rpc:feed.RunNow"); !errors.Is(err, store.ErrRunInProgress) {
		t.Fatalf("a second run was allowed while one is live: err=%v — this is the double-billing case", err)
	}
}

// keepRunAlive must stop cleanly, and must not keep renewing a lock after the
// run it belongs to is over — a lock renewed forever would wedge the feed for
// three minutes after every run instead of releasing with the terminal write.
func TestKeepRunAliveStopsCleanly(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "heartbeat-stop-feed", true)
	ctx := t.Context()

	runID, err := st.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	stop := keepRunAlive(ctx, st, discardLogger(), feedID, runID)
	stop()

	// Closing twice would panic on a closed channel; the executors call it
	// through defer exactly once, but the contract is worth pinning.
	if err := st.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}
	// A heartbeat after the run closed is a no-op, not an error the caller
	// has to special-case.
	if err := st.Heartbeat(ctx, runID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Heartbeat on a finished run = %v, want ErrNotFound", err)
	}
}

func heartbeatOf(t *testing.T, st *store.Store, runID int64) time.Time {
	t.Helper()
	var raw string
	if err := st.Reader().QueryRowContext(context.Background(),
		`SELECT heartbeat_at FROM runs WHERE id = ?`, runID).Scan(&raw); err != nil {
		t.Fatalf("reading heartbeat_at: %v", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parsing heartbeat_at %q: %v", raw, err)
	}
	return ts
}
