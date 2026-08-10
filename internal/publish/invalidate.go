// This file exists to close the gap PLAN.md §11 describes and TODOS.md
// RULE-6 states: every write that changes a feed's published representation
// must invalidate that feed's render cache, and nothing outside this package
// has ever been able to do that — server.go's NewServer returns a bare
// http.Handler, and the *Cache it builds internally is never handed back.
package publish

import "net/http"

// Invalidator is the narrow surface a writer outside this package needs: it
// must be able to name a feed by slug and say "your published shape may have
// changed" without knowing anything else about how the publish plane caches
// or renders. internal/store's Hooked type (internal/store/hooks.go) is built
// against exactly this interface, not against *Cache directly, so the store
// package never needs to import internal/publish.
type Invalidator interface {
	// InvalidateFeed drops every cached representation of the feed slug
	// identifies — every format, its permalinks, and (per Cache.Invalidate)
	// the "/" index, since the index renders every enabled feed's title and
	// description (PLAN.md §14.1). Any aggregate containing this feed must
	// also be invalidated; that is the caller's responsibility (this
	// interface has no notion of aggregate membership — see the wiring note
	// below), typically by calling InvalidateFeed once per member slug plus
	// once for the aggregate's own slug.
	InvalidateFeed(slug string)
	// InvalidateAll drops every cached entry, for a global change such as
	// the publishing defaults in PLAN.md §12.5 that every feed's rendered
	// output can inherit.
	InvalidateAll()
}

// cacheInvalidator adapts *Cache's existing method names (Invalidate,
// InvalidateAll — chosen in cache.go before this file existed) to the
// Invalidator interface's InvalidateFeed name. The rename exists on this
// side, not by touching cache.go, per the constraint that cache.go is
// read-only from here.
type cacheInvalidator struct{ c *Cache }

func (a cacheInvalidator) InvalidateFeed(slug string) { a.c.Invalidate(slug) }
func (a cacheInvalidator) InvalidateAll()             { a.c.InvalidateAll() }

// NewServerAndInvalidator is a drop-in variant of NewServer (server.go) that
// additionally returns an Invalidator wired to the *same* Cache instance the
// returned handler answers requests from.
//
// Why this is possible without editing server.go: this file lives in
// package publish too, so it has the same access to the unexported `server`
// type and its `cache *Cache` field that server.go itself has — the
// unreachability PLAN.md §11 complains about is a property of NewServer's
// signature (it returns the *interface* http.Handler, and the concrete value
// underneath is an *http.ServeMux with no exported hook back to `s`), not a
// property of the package boundary. So rather than trying to claw the cache
// back out of an http.Handler after the fact, this constructor builds the
// same *server and the same route table NewServer does, keeps a reference to
// the cache alongside the handler, and returns both.
//
// The cost of this approach, stated plainly: the six mux.HandleFunc lines
// below are a duplicate of NewServer's route table. If a route is ever added
// to or removed from NewServer, this constructor must be updated in the same
// change or the two handlers silently diverge — one route set answering
// requests, the other believed to be the same. That risk was judged smaller
// than the alternative (a Registry that owns a *Cache nobody's NewServer call
// actually uses, which would invalidate a cache that no request ever reads —
// silently doing nothing) because Deps carries no Cache field for a Registry
// to hand to NewServer; adding one would mean editing server.go, which is out
// of scope here. A comment identical to this one is not present in
// server.go's NewServer, because server.go must not be edited; whoever next
// touches NewServer's route table should grep this package for "HandleFunc"
// and update both.
//
// Wiring for a caller (e.g. cmd/animefeedflux or internal/rpc's service
// constructors):
//
//	handler, inv := publish.NewServerAndInvalidator(deps)
//	// handler is served on the publish listener, exactly as NewServer's
//	// result would be.
//	hooked := store.NewHooked(st, func(slug string) {
//	        inv.InvalidateFeed(slug)
//	        // plus once per aggregate containing slug, if any — the control
//	        // plane, not this package or internal/store, is what knows feed
//	        // membership (feed_members), so that fan-out belongs at the
//	        // call site that constructs this closure, not inside Hooked or
//	        // Invalidator.
//	})
//	// every RPC service now writes through `hooked` instead of `st`.
func NewServerAndInvalidator(deps Deps) (http.Handler, Invalidator) {
	s := &server{deps: deps, cache: NewCache()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/robots.txt", s.handleRobots)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/feeds/", s.handleFeed)
	mux.HandleFunc("/items/", s.handleItem)
	return mux, cacheInvalidator{c: s.cache}
}
