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
