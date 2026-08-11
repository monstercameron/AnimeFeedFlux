package publish

import (
	"fmt"
	"testing"
)

// A permalink is cached under two keys (handleItem): "<slug>:item:<key>" so
// Invalidate(slug) can find it, and a bare "item:<key>" because the read path
// has only the item key before its database lookup. Invalidate reaches the
// bare key ONLY by walking to the prefixed one first.
//
// The LRU ceiling therefore has a way to break invalidation that has nothing
// to do with staleness limits: the read path checks the bare key first and
// returns on a hit, so the prefixed twin is written once and never read
// again. Its recency never improves, which makes it a preferred eviction
// victim — and every eviction of a prefixed twin permanently orphans a bare
// key that Invalidate can no longer see. The permalink then serves the
// original bytes forever, which is precisely the 410-retraction and
// published-correction failure the paired delete exists to prevent.
func TestEvictingOneHalfOfAPermalinkPairDoesNotOrphanTheOther(t *testing.T) {
	// A ceiling that fits the pair plus a little, so pushing more entries
	// through evicts from the least-recently-used end.
	c := NewCacheWithLimit(1200)

	perma := testEntry(t, "the original permalink body")
	c.PutPair("trivia:item:K", "item:K", perma)

	if _, ok := c.Get("item:K"); !ok {
		t.Fatal("the permalink is not cached at all")
	}

	// Traffic: other feeds' documents flow through the cache, and the
	// permalink keeps getting hit on its bare key — exactly what the read
	// path does.
	for i := range 40 {
		c.Put(fmt.Sprintf("other:xml:%d", i), testEntry(t, fmt.Sprintf("other feed document %d", i)))
		if _, ok := c.Get("item:K"); !ok {
			// Either both halves are gone (fine — no orphan) or the bare one
			// survives, which is what we go on to test.
			break
		}
	}

	// The retraction: the item is deleted, and the control plane invalidates
	// the feed.
	c.Invalidate("trivia")

	if e, ok := c.Get("item:K"); ok {
		t.Fatalf("after Invalidate(\"trivia\") the bare permalink key still serves %q — "+
			"its prefixed twin was evicted, so Invalidate could not see it, and this "+
			"URL now serves the pre-deletion body for the life of the process",
			string(e.Body))
	}
}

// TestPutPairEvictsAsAUnit is the direct statement of the invariant: the two
// keys enter and leave the cache together, whatever the reason for leaving.
func TestPutPairEvictsAsAUnit(t *testing.T) {
	c := NewCacheWithLimit(600)
	c.PutPair("trivia:item:K", "item:K", testEntry(t, "body"))

	// Push until something has to go.
	for i := range 20 {
		c.Put(fmt.Sprintf("filler:%d", i), testEntry(t, fmt.Sprintf("filler body %d", i)))
	}

	_, prefixed := c.Get("trivia:item:K")
	_, bare := c.Get("item:K")
	if prefixed != bare {
		t.Fatalf("the pair split under eviction: prefixed present=%v, bare present=%v", prefixed, bare)
	}
	requireStatsAgree(t, c, "eviction of a paired entry")
}
