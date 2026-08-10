package backoff

import (
	"testing"
	"time"
)

func TestDelayMonotonicCapAndBounds(t *testing.T) {
	p := DefaultPolicy
	always1 := func() float64 { return 1 - 1e-9 } // max jitter -> delay == cap

	prev := time.Duration(0)
	for attempt := 0; attempt < 20; attempt++ {
		d := p.Delay(attempt, always1)
		if d < 0 {
			t.Fatalf("attempt %d: negative delay %v", attempt, d)
		}
		if d > p.Max {
			t.Fatalf("attempt %d: delay %v exceeds Max %v", attempt, d, p.Max)
		}
		if d < prev {
			t.Fatalf("attempt %d: delay %v decreased from previous %v", attempt, d, prev)
		}
		prev = d
	}
	// Eventually saturates at (just under, per jitter clamping) Max.
	got := p.Delay(19, always1)
	if delta := p.Max - got; delta < 0 || delta > time.Millisecond {
		t.Fatalf("attempt 19 with max jitter = %v, want close to Max %v", got, p.Max)
	}
}

func TestDelayZeroJitterIsZero(t *testing.T) {
	p := DefaultPolicy
	zero := func() float64 { return 0 }
	for attempt := 0; attempt < 5; attempt++ {
		if got := p.Delay(attempt, zero); got != 0 {
			t.Fatalf("attempt %d with zero jitter = %v, want 0", attempt, got)
		}
	}
}

func TestDelayHalfJitterIsHalfCap(t *testing.T) {
	p := Policy{Base: 1000, Max: 100000, Factor: 2.0}
	half := func() float64 { return 0.5 }

	// attempt 0 cap == Base(1000), half jitter -> 500
	if got := p.Delay(0, half); got != 500 {
		t.Fatalf("attempt 0 half-jitter = %v, want 500", got)
	}
	// attempt 1 cap == Base*Factor = 2000, half jitter -> 1000
	if got := p.Delay(1, half); got != 1000 {
		t.Fatalf("attempt 1 half-jitter = %v, want 1000", got)
	}
}

func TestDelayRand01OutOfRangeClamped(t *testing.T) {
	p := DefaultPolicy
	tooHigh := func() float64 { return 5 }
	tooLow := func() float64 { return -5 }

	if got := p.Delay(0, tooHigh); got > p.Base {
		t.Fatalf("out-of-range-high jitter produced %v > Base %v", got, p.Base)
	}
	if got := p.Delay(0, tooLow); got != 0 {
		t.Fatalf("out-of-range-low jitter produced %v, want 0", got)
	}
}

func TestSequenceLength(t *testing.T) {
	p := DefaultPolicy
	seq := p.Sequence(7, func() float64 { return 0.3 })
	if len(seq) != 7 {
		t.Fatalf("Sequence(7) has len %d, want 7", len(seq))
	}
}

func TestNegativeAttemptClampedToZero(t *testing.T) {
	p := DefaultPolicy
	half := func() float64 { return 0.5 }
	if p.Delay(-3, half) != p.Delay(0, half) {
		t.Fatalf("negative attempt not clamped to 0")
	}
}
