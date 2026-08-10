package auth

import (
	"testing"
	"time"
)

func TestEstimateBackoffMatchesServerCurve(t *testing.T) {
	// internal/rpc/auth.go's backoffDelay: failures<=2 -> 0; then
	// 2^(failures-2) seconds, capped at 60s. Table mirrors that exactly.
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 0},
		{2, 0},
		{3, 2 * time.Second},  // n=1: 2^1
		{4, 4 * time.Second},  // n=2: 2^2
		{5, 8 * time.Second},  // n=3: 2^3
		{6, 16 * time.Second}, // n=4: 2^4
		{7, 32 * time.Second}, // n=5: 2^5
		{8, 60 * time.Second}, // n=6: 2^6=64s, clamped to 60s
		{9, 60 * time.Second}, // n clamped to 6: 64s -> 60s
		{20, 60 * time.Second},
	}
	for _, tc := range cases {
		if got := EstimateBackoff(tc.failures); got != tc.want {
			t.Errorf("EstimateBackoff(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

func TestEstimateBackoffMonotonicallyNonDecreasing(t *testing.T) {
	prev := time.Duration(0)
	for f := 0; f < 30; f++ {
		got := EstimateBackoff(f)
		if got < prev {
			t.Fatalf("failures=%d: backoff %v decreased from previous %v", f, got, prev)
		}
		if got > backoffCap {
			t.Fatalf("failures=%d: backoff %v exceeds cap %v", f, got, backoffCap)
		}
		prev = got
	}
}

func TestRemainingBackoffZeroValueMeansUnblocked(t *testing.T) {
	now := time.Now()
	if got := RemainingBackoff(now, time.Time{}); got != 0 {
		t.Fatalf("RemainingBackoff with zero-value until = %v, want 0", got)
	}
}

func TestRemainingBackoffCountsDownToZero(t *testing.T) {
	now := time.Now()
	until := now.Add(5 * time.Second)
	if got := RemainingBackoff(now, until); got != 5*time.Second {
		t.Fatalf("RemainingBackoff = %v, want 5s", got)
	}
	if got := RemainingBackoff(until, until); got != 0 {
		t.Fatalf("RemainingBackoff at exactly `until` = %v, want 0", got)
	}
	if got := RemainingBackoff(until.Add(time.Second), until); got != 0 {
		t.Fatalf("RemainingBackoff after `until` = %v, want 0", got)
	}
}

func TestBackoffSecondsCeilRoundsUp(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{-time.Second, 0},
		{500 * time.Millisecond, 1},
		{1 * time.Second, 1},
		{1500 * time.Millisecond, 2},
		{59500 * time.Millisecond, 60},
	}
	for _, tc := range cases {
		if got := BackoffSecondsCeil(tc.d); got != tc.want {
			t.Errorf("BackoffSecondsCeil(%v) = %d, want %d", tc.d, got, tc.want)
		}
	}
}
