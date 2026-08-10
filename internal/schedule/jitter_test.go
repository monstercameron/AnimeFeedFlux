package schedule

import (
	"fmt"
	"testing"
	"time"
)

func TestOffset_Deterministic(t *testing.T) {
	window := 10 * time.Minute
	want := Offset("naruto-daily", window)
	for i := 0; i < 100; i++ {
		if got := Offset("naruto-daily", window); got != want {
			t.Fatalf("call %d: Offset returned %v, want %v (must be stable across calls)", i, got, want)
		}
	}
}

func TestOffset_DifferentSlugsDiffer(t *testing.T) {
	window := 10 * time.Minute
	seen := map[time.Duration]int{}
	for i := 0; i < 20; i++ {
		slug := fmt.Sprintf("feed-%d", i)
		seen[Offset(slug, window)]++
	}
	if len(seen) < 15 {
		t.Errorf("20 distinct slugs produced only %d distinct offsets, expected most to differ", len(seen))
	}
}

func TestOffset_WithinWindow(t *testing.T) {
	window := 7 * time.Minute
	for i := 0; i < 500; i++ {
		slug := fmt.Sprintf("slug-%d", i)
		off := Offset(slug, window)
		if off < 0 || off >= window {
			t.Fatalf("Offset(%q, %v) = %v, out of range [0,%v)", slug, window, off, window)
		}
	}
}

func TestOffset_ZeroWindow(t *testing.T) {
	if got := Offset("anything", 0); got != 0 {
		t.Errorf("Offset with zero window = %v, want 0", got)
	}
	if got := Offset("anything", -time.Minute); got != 0 {
		t.Errorf("Offset with negative window = %v, want 0", got)
	}
}

// TestOffset_Distribution checks the offsets over ~1000 slugs spread across
// the window rather than clumping in one bucket — the whole point of using a
// hash instead of, say, len(slug)%window.
func TestOffset_Distribution(t *testing.T) {
	const (
		n         = 1000
		numBucket = 10
	)
	window := 10 * time.Minute
	buckets := make([]int, numBucket)
	bucketWidth := window / numBucket

	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("feed-slug-%d", i)
		off := Offset(slug, window)
		b := int(off / bucketWidth)
		if b >= numBucket { // guard the top edge (off can equal window-1ns worth of rounding)
			b = numBucket - 1
		}
		buckets[b]++
	}

	// Expect roughly n/numBucket per bucket; allow generous slack (uniform
	// hash distribution, not a strict RNG guarantee) but catch gross clumping.
	wantPer := n / numBucket
	minAcceptable := wantPer / 3
	for b, count := range buckets {
		if count < minAcceptable {
			t.Errorf("bucket %d got only %d of %d slugs (want at least %d) — offsets are clumping, not spreading", b, count, n, minAcceptable)
		}
	}
}
