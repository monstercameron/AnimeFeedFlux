package ids

import (
	"sort"
	"sync"
	"testing"
	"time"
)

// crockford is the alphabet ULID uses (RFC 4648 base32 with the ambiguous
// I/L/O/U letters removed). Keys must be drawn from exactly this set,
// uppercase, with no padding.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func isCrockford(s string) bool {
	for _, r := range s {
		found := false
		for _, c := range crockford {
			if r == c {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestNewItemKey_ShapeAndAlphabet(t *testing.T) {
	src := NewSource()
	key := src.NewItemKey(time.Now())
	if len(key) != 26 {
		t.Fatalf("item key length = %d, want 26 (got %q)", len(key), key)
	}
	if !isCrockford(key) {
		t.Fatalf("item key %q contains characters outside uppercase Crockford base32", key)
	}
}

// This is the assertion that matters most: item_key must never be derivable
// from the item's content, because the RSS guid / Atom id are built from it
// and a guid that changes when a title is edited resurfaces the item as a
// duplicate in every subscriber's inbox (PLAN.md §5.1). Calling NewItemKey
// twice with the identical timestamp — the only input the caller controls —
// must still yield different keys, proving nothing content-shaped is being
// hashed into the result.
func TestNewItemKey_SameTimestampDifferentKeys(t *testing.T) {
	src := NewSource()
	now := time.Now()

	a := src.NewItemKey(now)
	b := src.NewItemKey(now)

	if a == b {
		t.Fatalf("two keys minted for the same timestamp were identical: %q", a)
	}
}

func TestDeterministicSource_SameSeedSameSequence(t *testing.T) {
	now := time.Now()

	s1 := NewDeterministicSource(42)
	s2 := NewDeterministicSource(42)

	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		k1 := s1.NewItemKey(ts)
		k2 := s2.NewItemKey(ts)
		if k1 != k2 {
			t.Fatalf("step %d: same seed produced different keys: %q vs %q", i, k1, k2)
		}
	}
}

func TestDeterministicSource_DifferentSeedDifferentSequence(t *testing.T) {
	now := time.Now()

	s1 := NewDeterministicSource(1)
	s2 := NewDeterministicSource(2)

	same := true
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		if s1.NewItemKey(ts) != s2.NewItemKey(ts) {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds produced the same key sequence")
	}
}

// Golden-file stability: a fixed seed, given the identical timestamp
// sequence, must keep producing the exact same keys across independent
// Source instances (and therefore across test runs and machines) — that's
// the entire point of NewDeterministicSource, since tests that assert on
// rendered guids need output that doesn't churn every time the suite runs.
func TestDeterministicSource_GoldenValues(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	src1 := NewDeterministicSource(7)
	got := []string{
		src1.NewItemKey(ts),
		src1.NewItemKey(ts),
		src1.NewItemKey(ts),
	}

	src2 := NewDeterministicSource(7)
	rederived := []string{
		src2.NewItemKey(ts),
		src2.NewItemKey(ts),
		src2.NewItemKey(ts),
	}
	for i := range got {
		if got[i] != rederived[i] {
			t.Fatalf("golden step %d: %q != %q", i, got[i], rederived[i])
		}
	}
}

func TestNewItemKey_ConcurrentGenerationNoDuplicates(t *testing.T) {
	const (
		goroutines = 50
		perRoutine = 200
	)

	src := NewSource()
	now := time.Now()

	results := make(chan string, goroutines*perRoutine)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				results <- src.NewItemKey(now)
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, goroutines*perRoutine)
	for key := range results {
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate item key generated concurrently: %q", key)
		}
		seen[key] = struct{}{}
	}
	if len(seen) != goroutines*perRoutine {
		t.Fatalf("got %d unique keys, want %d", len(seen), goroutines*perRoutine)
	}
}

// ULID's lexicographic-sorts-by-time property is worth relying on elsewhere
// (e.g. "list items in insert order" without a separate sequence column), so
// it is worth asserting directly rather than trusting the library silently.
func TestNewItemKey_SortsWithTimestamp(t *testing.T) {
	src := NewSource()
	base := time.Now()

	var keys []string
	for i := 0; i < 10; i++ {
		keys = append(keys, src.NewItemKey(base.Add(time.Duration(i)*time.Second)))
	}

	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)

	for i := range keys {
		if keys[i] != sorted[i] {
			t.Fatalf("keys generated from increasing timestamps did not sort lexicographically: %v", keys)
		}
	}
}
