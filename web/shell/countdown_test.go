package shell

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
)

func fixedRand(v float64) func() float64 {
	return func() float64 { return v }
}

func TestNextReconnectPhaseHoldsBeforeDeadline(t *testing.T) {
	now := time.Now()
	deadline := now.Add(5 * time.Second)
	attempt, next := nextReconnectPhase(backoff.DefaultPolicy, 2, deadline, now, fixedRand(0.5))
	if attempt != 2 {
		t.Errorf("attempt = %d, want unchanged 2", attempt)
	}
	if !next.Equal(deadline) {
		t.Errorf("deadline = %v, want unchanged %v", next, deadline)
	}
}

func TestNextReconnectPhaseAdvancesAtDeadline(t *testing.T) {
	now := time.Now()
	deadline := now.Add(-time.Millisecond) // already passed
	attempt, next := nextReconnectPhase(backoff.DefaultPolicy, 0, deadline, now, fixedRand(0.5))
	if attempt != 1 {
		t.Fatalf("attempt = %d, want 1", attempt)
	}
	wantDelay := backoff.DefaultPolicy.Delay(1, fixedRand(0.5))
	wantDeadline := now.Add(wantDelay)
	if !next.Equal(wantDeadline) {
		t.Errorf("deadline = %v, want %v", next, wantDeadline)
	}
}

func TestNextReconnectPhaseAdvancesExactlyAtDeadline(t *testing.T) {
	// now == deadline counts as "passed" (!now.Before(deadline) is true
	// when they're equal), matching the interval tick semantics: a tick
	// that lands exactly on the deadline should still advance rather than
	// wait a full extra second.
	now := time.Now()
	attempt, next := nextReconnectPhase(backoff.DefaultPolicy, 3, now, now, fixedRand(0.1))
	if attempt != 4 {
		t.Fatalf("attempt = %d, want 4", attempt)
	}
	if !next.After(now) && !next.Equal(now) {
		t.Errorf("deadline %v should be at or after now %v", next, now)
	}
}

func TestNextReconnectPhaseSequenceMonotonicAttempts(t *testing.T) {
	// Simulate a tick landing exactly on each computed deadline in turn:
	// the attempt counter should advance by exactly one each call, never
	// skip or repeat.
	attempt := 0
	now := time.Now()
	deadline := now
	for i := 1; i <= 5; i++ {
		var next time.Time
		attempt, next = nextReconnectPhase(backoff.DefaultPolicy, attempt, deadline, now, fixedRand(0.3))
		if attempt != i {
			t.Fatalf("iteration %d: attempt = %d, want %d", i, attempt, i)
		}
		now = next // pretend the clock advanced exactly to the new deadline
		deadline = next
	}
}

func TestReconnectSecondsLeftRoundsUpAndClamps(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		deadline time.Time
		want     int
	}{
		{"exactly 5s", now.Add(5 * time.Second), 5},
		{"4.2s rounds up to 5", now.Add(4200 * time.Millisecond), 5},
		{"0.2s clamps to 1 via ceil", now.Add(200 * time.Millisecond), 1},
		{"already past clamps to 1", now.Add(-time.Second), 1},
		{"exactly 0 clamps to 1", now, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reconnectSecondsLeft(tc.deadline, now)
			if got != tc.want {
				t.Errorf("reconnectSecondsLeft() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReconnectSecondsLeftNeverZeroOrNegative(t *testing.T) {
	now := time.Now()
	for _, delta := range []time.Duration{-10 * time.Second, -time.Nanosecond, 0, time.Nanosecond, 999 * time.Millisecond} {
		got := reconnectSecondsLeft(now.Add(delta), now)
		if got < 1 {
			t.Errorf("reconnectSecondsLeft(delta=%v) = %d, want >= 1 (D6-06 plural clamp)", delta, got)
		}
	}
}
