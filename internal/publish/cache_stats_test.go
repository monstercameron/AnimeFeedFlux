package publish

import (
	"fmt"
	"math/rand"
	"testing"
)

// Stats' byte counters are maintained incrementally rather than summed on
// demand, so the failure mode they introduce is drift: a mutation path that
// updates one counter and not another reports a number that is wrong by an
// amount nothing else would ever notice. These tests recompute the truth by
// walking the entries and compare, which is the only check that can catch it.

// walkStats is the O(n) computation Stats used to do, kept here as the
// independent oracle.
func walkStats(c *Cache) Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Stats{Entries: len(c.entries)}
	for _, el := range c.entries {
		e := el.Value.(*cacheItem).entry
		s.BodyBytes += int64(len(e.Body))
		s.GzipBodyBytes += int64(len(e.GzipBody))
	}
	return s
}

func requireStatsAgree(t *testing.T, c *Cache, step string) {
	t.Helper()
	got, want := c.Stats(), walkStats(c)
	if got != want {
		t.Fatalf("after %s: Stats() = %+v, walking the entries gives %+v", step, got, want)
	}
	if c.bytes != got.BodyBytes+got.GzipBodyBytes {
		t.Fatalf("after %s: bytes = %d, but body+gzip = %d",
			step, c.bytes, got.BodyBytes+got.GzipBodyBytes)
	}
}

func TestCacheStatsMatchesAWalkAcrossEveryMutation(t *testing.T) {
	c := NewCache()
	requireStatsAgree(t, c, "construction")

	c.Put("feed:xml", testEntry(t, "<rss>one</rss>"))
	c.Put("feed:atom", testEntry(t, "<feed>two</feed>"))
	c.Put("feed:item:abc", testEntry(t, "permalink body"))
	c.Put("item:abc", testEntry(t, "permalink body"))
	c.Put("index", testEntry(t, "index page"))
	requireStatsAgree(t, c, "puts")

	// Replacement: the old entry's bytes must come back out. A longer body
	// than the original, so a missed subtraction shows up as a surplus
	// rather than cancelling out.
	c.Put("feed:xml", testEntry(t, "<rss>one, considerably expanded</rss>"))
	requireStatsAgree(t, c, "replacing an existing key")

	c.Invalidate("feed")
	requireStatsAgree(t, c, "Invalidate")
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("Invalidate left %d entries, want 0", s.Entries)
	}
	if c.bytes != 0 {
		t.Fatalf("Invalidate left bytes = %d, want 0", c.bytes)
	}

	c.Put("a", testEntry(t, "aaa"))
	c.InvalidateAll()
	requireStatsAgree(t, c, "InvalidateAll")
	if c.bodyBytes != 0 || c.gzipBytes != 0 {
		t.Fatalf("InvalidateAll left body=%d gzip=%d, want 0/0", c.bodyBytes, c.gzipBytes)
	}
}

// TestCacheStatsSurvivesEviction drives the LRU ceiling, which is the path
// that removes entries nobody asked to remove — the easiest one for a
// counter update to be missing from.
func TestCacheStatsSurvivesEviction(t *testing.T) {
	c := NewCacheWithLimit(4096)
	rng := rand.New(rand.NewSource(1))

	for i := range 300 {
		body := make([]byte, 1+rng.Intn(400))
		for j := range body {
			body[j] = byte('a' + rng.Intn(26))
		}
		key := fmt.Sprintf("feed:item:%d", rng.Intn(40)) // collisions force replacements too
		c.Put(key, testEntry(t, string(body)))
		requireStatsAgree(t, c, fmt.Sprintf("put #%d", i))
		if c.bytes > c.max {
			t.Fatalf("put #%d left %d bytes over the %d ceiling", i, c.bytes, c.max)
		}
	}

	c.Invalidate("feed")
	requireStatsAgree(t, c, "Invalidate after eviction churn")
	if c.bytes != 0 {
		t.Fatalf("bytes = %d after invalidating everything, want 0", c.bytes)
	}
}
