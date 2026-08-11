package publish

import "testing"

func testEntry(t *testing.T, body string) Entry {
	t.Helper()
	e, err := NewEntry([]byte(body), "text/html; charset=utf-8", "Mon, 11 Aug 2026 00:00:00 GMT")
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

// handleItem caches a permalink under both keys it can be looked up by. This
// mirrors that exactly, because the fix depends on the pair being written
// together — if server.go ever writes one without the other, this test is what
// should start failing.
func putPermalink(t *testing.T, c *Cache, slug, itemKey, body string) {
	t.Helper()
	e := testEntry(t, body)
	c.Put(slug+":item:"+itemKey, e)
	c.Put("item:"+itemKey, e)
}

func TestInvalidateDropsBothPermalinkKeys(t *testing.T) {
	// The bare "item:<key>" copy is the one handleItem's read path checks
	// FIRST, so a surviving copy means every later request serves stale
	// content no matter what the slug-prefixed one says.
	c := NewCache()
	putPermalink(t, c, "trivia-daily", "01JABCDEF", "original body")
	c.Put("trivia-daily:xml", testEntry(t, "<rss/>"))
	c.Put("index", testEntry(t, "<html/>"))

	c.Invalidate("trivia-daily")

	for _, key := range []string{
		"trivia-daily:item:01JABCDEF",
		"item:01JABCDEF",
		"trivia-daily:xml",
		"index",
	} {
		if _, ok := c.Get(key); ok {
			t.Errorf("%q survived Invalidate", key)
		}
	}
}

func TestInvalidateLeavesOtherFeedsAlone(t *testing.T) {
	// The bare key carries no slug, so the sweep has to derive which bare keys
	// belong to this feed from the prefixed ones rather than dropping every
	// "item:" entry it finds — otherwise one feed's edit would evict every
	// other feed's permalinks and turn a targeted invalidation into a global
	// one.
	c := NewCache()
	putPermalink(t, c, "trivia-daily", "01JAAA", "trivia body")
	putPermalink(t, c, "news-roundup", "01JBBB", "news body")
	c.Put("news-roundup:xml", testEntry(t, "<rss/>"))

	c.Invalidate("trivia-daily")

	for _, key := range []string{"news-roundup:item:01JBBB", "item:01JBBB", "news-roundup:xml"} {
		if _, ok := c.Get(key); !ok {
			t.Errorf("%q was evicted by another feed's invalidation", key)
		}
	}
	if _, ok := c.Get("item:01JAAA"); ok {
		t.Error("the invalidated feed's bare permalink key survived")
	}
}

func TestInvalidateIsExactAboutPrefixes(t *testing.T) {
	// "trivia" must not invalidate "trivia-daily": slugs share prefixes, and a
	// substring match here would silently evict a neighbouring feed on every
	// write.
	c := NewCache()
	c.Put("trivia:xml", testEntry(t, "short"))
	putPermalink(t, c, "trivia-daily", "01JAAA", "long")

	c.Invalidate("trivia")

	if _, ok := c.Get("trivia:xml"); ok {
		t.Error("the named feed's own document survived")
	}
	for _, key := range []string{"trivia-daily:item:01JAAA", "item:01JAAA"} {
		if _, ok := c.Get(key); !ok {
			t.Errorf("%q was evicted by a prefix-sharing slug", key)
		}
	}
}

func TestInvalidateAllClearsEverything(t *testing.T) {
	c := NewCache()
	putPermalink(t, c, "trivia-daily", "01JAAA", "body")
	c.Put("index", testEntry(t, "<html/>"))

	c.InvalidateAll()

	if got := c.Stats().Entries; got != 0 {
		t.Errorf("%d entries survived InvalidateAll", got)
	}
}

// --- the LRU ceiling (PLAN.md §16, AFF_CACHE_MAX_BYTES) ---------------------

func bodyOf(t *testing.T, n int) Entry {
	t.Helper()
	// Incompressible-ish payload so GzipBody is not a rounding error and the
	// accounting reflects both representations, as entrySize does.
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	e, err := NewEntry(b, "application/rss+xml", "Mon, 11 Aug 2026 00:00:00 GMT")
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func TestCacheEvictsLeastRecentlyUsedAtTheCeiling(t *testing.T) {
	// Without a ceiling this map grew forever: permalinks are cached one per
	// item, and items accumulate by design (§12.4). On a 2GB box that ends in
	// an OOM months later rather than under test.
	one := bodyOf(t, 4096)
	limit := entrySize(one) * 2 // room for exactly two entries
	c := NewCacheWithLimit(limit)

	c.Put("a:xml", one)
	c.Put("b:xml", bodyOf(t, 4096))
	if got := c.Stats().Entries; got != 2 {
		t.Fatalf("%d entries below the ceiling, want 2", got)
	}

	// Touch "a" so "b" becomes the least recently used, then overflow.
	if _, ok := c.Get("a:xml"); !ok {
		t.Fatal("a:xml went missing before any eviction")
	}
	c.Put("c:xml", bodyOf(t, 4096))

	if _, ok := c.Get("b:xml"); ok {
		t.Error("the least recently used entry survived the ceiling")
	}
	for _, k := range []string{"a:xml", "c:xml"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%q was evicted despite being more recently used than b:xml", k)
		}
	}
}

func TestCacheStaysWithinItsCeiling(t *testing.T) {
	one := bodyOf(t, 2048)
	limit := entrySize(one) * 3
	c := NewCacheWithLimit(limit)

	for i := range 50 {
		c.Put("feed:"+string(rune('a'+i%26))+string(rune('a'+i/26))+":xml", bodyOf(t, 2048))
		st := c.Stats()
		if total := st.BodyBytes + st.GzipBodyBytes; total > limit {
			t.Fatalf("after %d puts the cache holds %d bytes, over its %d ceiling", i+1, total, limit)
		}
	}
	if got := c.Stats().Entries; got == 0 {
		t.Error("the cache evicted everything; the ceiling should keep the most recent entries")
	}
}

func TestCacheWithNoCeilingKeepsEverything(t *testing.T) {
	// NewCache() is what the tests and the e2e harness build; unlimited must
	// stay the behaviour there, or every existing expectation shifts.
	c := NewCache()
	for i := range 40 {
		c.Put("k"+string(rune('a'+i)), bodyOf(t, 4096))
	}
	if got := c.Stats().Entries; got != 40 {
		t.Errorf("%d entries with no ceiling, want all 40", got)
	}
}

func TestReplacingAnEntryDoesNotDoubleCountItsBytes(t *testing.T) {
	// A re-render of the same key (the common case after an invalidation)
	// must replace the accounting, not add to it — otherwise the cache
	// evicts itself into uselessness after a few refreshes.
	c := NewCacheWithLimit(1 << 20)
	for range 10 {
		c.Put("trivia-daily:xml", bodyOf(t, 4096))
	}
	st := c.Stats()
	if st.Entries != 1 {
		t.Fatalf("%d entries after replacing one key ten times, want 1", st.Entries)
	}
	if total := st.BodyBytes + st.GzipBodyBytes; total != entrySize(bodyOf(t, 4096)) {
		t.Errorf("accounting drifted to %d bytes for a single entry", total)
	}
}
