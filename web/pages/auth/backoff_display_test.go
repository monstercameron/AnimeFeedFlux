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

func TestAuthErrorKeyPreservesGenericStringUnlessDisconnected(t *testing.T) {
	const generic = "genericAuthError"
	if got := AuthErrorKey(false, generic); got != generic {
		t.Fatalf("AuthErrorKey(false, ...) = %q, want the generic key unchanged (D1-02)", got)
	}
	if got := AuthErrorKey(true, generic); got != keyConnectionUnreachable {
		t.Fatalf("AuthErrorKey(true, ...) = %q, want %q", got, keyConnectionUnreachable)
	}
	// The two keys must actually differ, or a disconnected attempt would
	// be indistinguishable from a credential rejection — the opposite of
	// what this function exists to fix.
	if keyConnectionUnreachable == generic {
		t.Fatal("keyConnectionUnreachable must not equal the generic auth-error key")
	}
}

func TestShouldAnnounceBackoffStarted(t *testing.T) {
	if ShouldAnnounceBackoffStarted(0) {
		t.Fatal("ShouldAnnounceBackoffStarted(0) = true, want false (nothing to count down)")
	}
	if ShouldAnnounceBackoffStarted(-time.Second) {
		t.Fatal("ShouldAnnounceBackoffStarted(negative) = true, want false")
	}
	if !ShouldAnnounceBackoffStarted(2 * time.Second) {
		t.Fatal("ShouldAnnounceBackoffStarted(2s) = false, want true")
	}
}

func TestShouldAnnounceBackoffCleared(t *testing.T) {
	cases := []struct {
		name       string
		blockedNow bool
		wasActive  bool
		want       bool
	}{
		{"still blocked, was active", true, true, false},
		{"cleared, was active", false, true, true},
		{"cleared, was never announced (initial mount)", false, false, false},
		{"still blocked, was never announced", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldAnnounceBackoffCleared(c.blockedNow, c.wasActive); got != c.want {
				t.Errorf("ShouldAnnounceBackoffCleared(%v, %v) = %v, want %v", c.blockedNow, c.wasActive, got, c.want)
			}
		})
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
