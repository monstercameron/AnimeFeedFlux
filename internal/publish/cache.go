// Package publish is the read-only HTTP plane (PLAN.md §2, §6): the only
// thing exposed to the internet, holding no writer and no dependency on
// internal/store, so a bug here cannot corrupt the database because that
// code path has no writer — not because anyone was careful.
package publish

import (
	"bytes"
	"compress/gzip"
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
type Cache struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]Entry)}
}

// Get returns the entry for key, and whether it was present. This is the
// entire cost of a cache hit — no lock beyond an RWMutex read lock, no call
// out to a renderer or a store (§5.4: "A cache HIT must not touch anything
// else — that is the whole point").
func (c *Cache) Get(key string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	return e, ok
}

// Put stores (or replaces) the entry for key.
func (c *Cache) Put(key string, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = e
}

// Invalidate drops every cached format for slug: every key equal to slug, or
// prefixed "slug:" (feed documents "slug:xml"/"slug:atom"/"slug:json" and
// permalinks "slug:item:<key>" all share that prefix by the key convention
// server.go uses). It also drops the "index" key, because the "/" page
// (PLAN.md §14.1) lists every enabled feed's title and description — a
// write to any one feed can change what the index renders, so scoping this
// to only that feed's own keys would leave a stale index page after an
// admin edit.
func (c *Cache) Invalidate(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := slug + ":"
	for k := range c.entries {
		if k == slug || k == prefix || len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
	delete(c.entries, "index")
}

// InvalidateAll drops every cached entry. Used for a global change (e.g. the
// publishing defaults in §12.5, which every feed's rendered output can
// inherit) and by tests.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]Entry)
}

// Stats reports current occupancy, for /healthz and dashboards.
func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Stats{Entries: len(c.entries)}
	for _, e := range c.entries {
		s.BodyBytes += int64(len(e.Body))
		s.GzipBodyBytes += int64(len(e.GzipBody))
	}
	return s
}
