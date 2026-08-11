// Package publish is the read-only HTTP plane (PLAN.md §2, §6): the only
// thing exposed to the internet, holding no writer and no dependency on
// internal/store, so a bug here cannot corrupt the database because that
// code path has no writer — not because anyone was careful.
package publish

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Entry is one cached rendered response: the plain body, the pre-gzipped
// body, and the validators derived from it. Gzipping once at Put time
// (rather than per request) is what makes a cache hit cheap — the whole
// point of having a cache in front of SQLite and the renderers (§5.4).
type Entry struct {
	Body         []byte
	GzipBody     []byte
	ETag         string
	LastModified string
	ContentType  string
}

// NewEntry builds an Entry from a rendered body: it gzips once, and computes
// a strong ETag as the hex SHA-256 of the exact body bytes (PLAN.md §5.4 —
// "Strong ETag (hash of the exact rendered body)"). lastModified is passed
// in rather than computed here (e.g. time.Now()) because the caller — not
// the cache — knows the real content timestamp (a feed's lastBuildDate, an
// item's updated_at), and the cache must not invent one.
func NewEntry(body []byte, contentType, lastModified string) (Entry, error) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(body); err != nil {
		return Entry{}, err
	}
	if err := w.Close(); err != nil {
		return Entry{}, err
	}

	return Entry{
		Body:         body,
		GzipBody:     gz.Bytes(),
		ETag:         etag,
		LastModified: lastModified,
		ContentType:  contentType,
	}, nil
}

// Stats is a point-in-time snapshot of cache occupancy.
type Stats struct {
	Entries       int
	BodyBytes     int64
	GzipBodyBytes int64
}

// Cache is an in-memory render cache keyed by an opaque string the caller
// constructs (server.go uses "<slug>:<format>" for feed documents and
// "<slug>:item:<item_key>" for permalinks, so Invalidate(slug) can find
// every entry belonging to a feed by prefix — see below). Safe for
// concurrent use: many readers hit this on every request, and a write
// happens only on control-plane invalidation.
//
// # The size ceiling
//
// PLAN.md §16 specifies AFF_CACHE_MAX_BYTES as a "render-cache LRU ceiling,
// default 64MiB", and internal/config has parsed and validated it since the
// beginning — but nothing read it, and this was a plain map with no eviction
// at all. Feed documents are bounded (three per slug), so that was survivable
// until permalinks: those are cached one per item, items accumulate forever by
// design (§12.4 — the permalink 410s forever and the guid is never reused),
// and anything that walks them (a crawler, Slack unfurling, a bot) pulls every
// item ever published into memory permanently. On the 2GB box this deploys to,
// that is the kind of growth that ends in an OOM months later rather than
// under test.
//
// maxBytes == 0 means unlimited, which is what NewCache still gives you: the
// e2e harness and the tests build caches directly and have no reason to care.
// Production goes through NewCacheWithLimit from the composition root.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
	bytes   int64
	max     int64

	// bodyBytes/gzipBytes are the same total as bytes, split the way Stats
	// reports it. They are maintained incrementally on every mutation
	// instead of being summed on demand because Stats' only production
	// caller is /healthz, which a load balancer hits every few seconds —
	// and summing meant walking every entry while holding the mutex that
	// every cache read needs. That walk grows with the permalink entries
	// this cache exists to bound (see "The size ceiling" above), so the
	// health check got slower, and blocked the hot path for longer, exactly
	// as the cache filled up.
	bodyBytes int64
	gzipBytes int64
}

// cacheItem is what the LRU list carries: the entry plus its key, because
// eviction works from the back of the list and has to delete the map row too.
type cacheItem struct {
	key   string
	entry Entry
	size  int64

	// pair is the key of this entry's twin, or "" when it has none.
	//
	// A permalink is cached twice — "<slug>:item:<key>" so Invalidate(slug)
	// can find it, and a bare "item:<key>" because the read path has only
	// the item key before its database lookup — and Invalidate reaches the
	// bare key ONLY by walking to the prefixed one first. That made the two
	// halves separable by eviction, and not merely in theory: the read path
	// checks the bare key first and returns on a hit, so the prefixed twin
	// is written once and never read again. Its recency never improves, so
	// it is exactly what an LRU evicts first — orphaning a bare key that
	// Invalidate can no longer see, which serves the pre-deletion body for
	// the life of the process. Linking them makes "the two keys leave
	// together" a property of the cache rather than something every removal
	// path has to remember.
	pair string
}

// entrySize is what an entry costs: both representations, since both are held
// for the life of the entry (NewEntry gzips once at Put time, which is what
// makes a hit cheap). The key itself is not counted — it is tens of bytes
// against a body measured in kilobytes, and counting it would imply a
// precision this accounting does not have.
func entrySize(e Entry) int64 { return int64(len(e.Body) + len(e.GzipBody)) }

// NewCache returns an empty Cache with no size ceiling.
func NewCache() *Cache { return NewCacheWithLimit(0) }

// NewCacheWithLimit returns an empty Cache that evicts least-recently-used
// entries once the total body bytes it holds would exceed maxBytes. A
// maxBytes of zero or less means unlimited.
func NewCacheWithLimit(maxBytes int64) *Cache {
	return &Cache{
		entries: make(map[string]*list.Element),
		order:   list.New(),
		max:     maxBytes,
	}
}

// Get returns the entry for key, and whether it was present, and marks it as
// most recently used.
//
// This takes an exclusive lock rather than the read lock it used to, because
// recording recency is a write. That is the price of the ceiling above, and
// it is a map lookup plus a list splice — nanoseconds, and still no renderer
// and no store on the hit path, which is what §5.4's "a cache HIT must not
// touch anything else" is actually protecting.
func (c *Cache) Get(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return Entry{}, false
	}
	c.order.MoveToFront(el)
	// A hit on either half refreshes both, so the pair travels the recency
	// list together and a twin nothing ever reads directly does not sink to
	// the eviction end on its own. See cacheItem.pair.
	it := el.Value.(*cacheItem)
	if it.pair != "" {
		if pel, ok := c.entries[it.pair]; ok {
			c.order.MoveToFront(pel)
		}
	}
	return it.entry, true
}

// Put stores (or replaces) the entry for key, then evicts from the
// least-recently-used end until the cache is back inside its ceiling.
//
// An entry larger than the whole ceiling is stored and then immediately
// evicted by the loop below, which is the honest outcome: it cannot be held
// within the configured budget, and silently keeping it would make the
// ceiling a suggestion. The request that produced it is still served — the
// caller already has the entry in hand.
func (c *Cache) Put(key string, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, e)
	c.evictLocked()
}

// PutPair stores one entry under two keys that must live and die together —
// the permalink case described on cacheItem.pair. Both are inserted before
// any eviction runs, so the second insertion can never push the first out
// before the two are linked.
func (c *Cache) PutPair(primary, alias string, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(primary, e)
	c.putLocked(alias, e)
	if el, ok := c.entries[primary]; ok {
		el.Value.(*cacheItem).pair = alias
	}
	if el, ok := c.entries[alias]; ok {
		el.Value.(*cacheItem).pair = primary
	}
	c.evictLocked()
}

// putLocked inserts or replaces one key, leaving any existing pair link
// intact — a re-render of a permalink replaces the bytes, not the pairing.
// The caller holds the lock and is responsible for calling evictLocked.
func (c *Cache) putLocked(key string, e Entry) {
	if el, ok := c.entries[key]; ok {
		it := el.Value.(*cacheItem)
		c.subLocked(it)
		it.entry, it.size = e, entrySize(e)
		c.addLocked(it)
		c.order.MoveToFront(el)
		return
	}
	it := &cacheItem{key: key, entry: e, size: entrySize(e)}
	c.entries[key] = c.order.PushFront(it)
	c.addLocked(it)
}

// evictLocked drops least-recently-used entries until the cache is back
// inside its ceiling. The caller holds the lock.
func (c *Cache) evictLocked() {
	if c.max <= 0 {
		return
	}
	for c.bytes > c.max {
		back := c.order.Back()
		if back == nil {
			return
		}
		// May remove two entries when the victim is half of a pair, which is
		// the point — it cannot leave an orphan behind.
		c.removeLocked(back)
	}
}

// addLocked/subLocked keep the three byte counters in step with one item
// joining or leaving the cache. They exist so no mutation path can update
// bytes and forget the Stats split — the counters are only correct if every
// site changes all three, and there is now exactly one site per direction.
// The caller holds the lock.
func (c *Cache) addLocked(it *cacheItem) {
	c.bytes += it.size
	c.bodyBytes += int64(len(it.entry.Body))
	c.gzipBytes += int64(len(it.entry.GzipBody))
}

func (c *Cache) subLocked(it *cacheItem) {
	c.bytes -= it.size
	c.bodyBytes -= int64(len(it.entry.Body))
	c.gzipBytes -= int64(len(it.entry.GzipBody))
}

// removeLocked drops an element and, if it is half of a pair, its twin with
// it — for any reason an entry leaves, eviction included. The twin's link is
// cleared before it is dropped so the two cannot chase each other. The
// caller holds the lock.
func (c *Cache) removeLocked(el *list.Element) {
	it := el.Value.(*cacheItem)
	c.dropLocked(el)
	if it.pair == "" {
		return
	}
	if pel, ok := c.entries[it.pair]; ok {
		pel.Value.(*cacheItem).pair = ""
		c.dropLocked(pel)
	}
}

// dropLocked removes exactly one element from both the list and the map,
// following no links. The caller holds the lock.
func (c *Cache) dropLocked(el *list.Element) {
	it := el.Value.(*cacheItem)
	c.order.Remove(el)
	delete(c.entries, it.key)
	c.subLocked(it)
}

// deleteLocked drops key if present. The caller holds the lock.
func (c *Cache) deleteLocked(key string) {
	if el, ok := c.entries[key]; ok {
		c.removeLocked(el)
	}
}

// Invalidate drops every cached format for slug: every key equal to slug, or
// prefixed "slug:" (feed documents "slug:xml"/"slug:atom"/"slug:json" and
// permalinks "slug:item:<key>" all share that prefix by the key convention
// server.go uses). It also drops the "index" key, because the "/" page
// (PLAN.md §14.1) lists every enabled feed's title and description — a
// write to any one feed can change what the index renders, so scoping this
// to only that feed's own keys would leave a stale index page after an
// admin edit.
//
// # The unprefixed permalink key
//
// handleItem caches one permalink under TWO keys: "slug:item:<key>", so this
// function can find it, and a bare "item:<key>", because the read path has
// only the item key before it has done the database lookup that would tell it
// the slug. The bare key matches none of the conditions above — and it is the
// one the read path checks FIRST — so dropping only the prefixed copy left the
// stale one serving every subsequent request, permanently.
//
// That is not a cosmetic staleness. A soft-deleted item's permalink must
// return 410 forever (PLAN.md §6, §12.4) — that IS the retraction mechanism,
// since RSS has none — and a published correction must reach the URL someone
// already shared. With the bare key surviving, deleting or correcting an item
// changed the feed documents and left the permalink serving the original
// content until the process restarted.
//
// The two keys are always written together (server.go's handleItem), so
// deleting the pair together is exact: for every "slug:item:<key>" dropped
// here, the matching "item:<key>" goes with it. Deleting from a map while
// ranging over it is defined behaviour in Go.
func (c *Cache) Invalidate(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := slug + ":"
	itemPrefix := prefix + "item:"
	for k := range c.entries {
		if k == slug || k == prefix || len(k) > len(prefix) && k[:len(prefix)] == prefix {
			if len(k) > len(itemPrefix) && k[:len(itemPrefix)] == itemPrefix {
				c.deleteLocked("item:" + k[len(itemPrefix):])
			}
			c.deleteLocked(k)
		}
	}
	c.deleteLocked("index")
}

// InvalidateAll drops every cached entry. Used for a global change (e.g. the
// publishing defaults in §12.5, which every feed's rendered output can
// inherit) and by tests.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.order.Init()
	c.bytes, c.bodyBytes, c.gzipBytes = 0, 0, 0
}

// Stats reports current occupancy, for /healthz and dashboards. It is O(1):
// see the counters on Cache for why that matters on a path a load balancer
// polls.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Entries:       len(c.entries),
		BodyBytes:     c.bodyBytes,
		GzipBodyBytes: c.gzipBytes,
	}
}
