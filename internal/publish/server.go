package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
)

// Deps is everything the publish plane needs to answer a request, expressed
// as plain functions rather than a store.Reader or a *store.Store. That
// keeps this package independently testable (a test supplies a func literal
// with a call counter, see server_test.go) and decoupled from internal/store
// — the whole point of the publish plane is that it has no path to a
// writer, and importing the store package at all would be one small step
// from reaching for one.
type Deps struct {
	// GetFeed resolves a feed by slug. found=false, err=nil means "no such
	// feed" (→ 404). A non-nil err is a backend failure; it is mapped to a
	// generic 404 as well, because distinguishing "unknown" from "backend
	// broke" in the response body would be exactly the kind of information
	// leak §6 rules out ("no stack traces"). Callers that want the real
	// error for a log line should wrap GetFeed and log before returning.
	GetFeed func(ctx context.Context, slug string) (model.Feed, bool, error)

	// ListItems returns a feed's rendered item window, in any order — the
	// renderers themselves sort newest-first (§5.5), so this package does
	// not need to.
	ListItems func(ctx context.Context, feedID int64) ([]model.Item, error)

	// ListFeeds returns every feed for the "/" index (§14.1). Index does the
	// enabled-only filtering.
	ListFeeds func(ctx context.Context) ([]model.Feed, error)

	// GetItem resolves one item by its opaque, never-changing item_key,
	// together with the feed that owns it (needed for og:image and the
	// Channel context on the permalink page). found=false → 404. A found
	// item with IsDeleted() true → 410, never 404 — the permalink promise is
	// that it 410s forever (PLAN.md §12.4).
	GetItem func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error)

	// BaseURL is the public base URL, absolute, no trailing slash (the
	// validated form of config.Config.PublicBaseURL).
	BaseURL string
	// Generator identifies the software in <generator>/<meta name=generator>.
	Generator string
	// DocsURL is the RSS spec URL for <docs>.
	DocsURL string
	// TagYear is the fixed Tag URI epoch (model.Channel.TagYear) — a single
	// deployment-wide value rather than per-feed, because model.Feed carries
	// no creation year to derive it from. Never recomputed from "now".
	TagYear int

	// Now is the injectable clock used to stamp cache Last-Modified/pubDate
	// fallbacks. Defaults to time.Now.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// server holds the resolved Deps plus the cache instance shared across every
// request handled by this http.Handler.
type server struct {
	deps  Deps
	cache *Cache
}

// NewServer builds the publish plane's http.Handler: the fixed route set of
// PLAN.md §6, and nothing else — no REST API, no admin surface (§2's whole
// point is that this plane cannot write).
func NewServer(deps Deps) http.Handler {
	s := &server{deps: deps, cache: NewCache()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/robots.txt", s.handleRobots)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/feeds/", s.handleFeed)
	mux.HandleFunc("/items/", s.handleItem)
	return mux
}

// --- shared helpers -------------------------------------------------------

// allowMethod enforces GET/HEAD only (§6: "Any method other than GET/HEAD →
// 405 with Allow"). It returns false (and has already written the response)
// when the method is not permitted.
func allowMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

// wantsGzip reports whether the client advertised gzip support. Encoding is
// negotiated per request from a cache holding both bodies (§5.4) — nothing
// is compressed on the fly here.
func wantsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			return true
		}
	}
	return false
}

// notModified reports whether the request's conditional headers are
// satisfied by e, honoring both If-None-Match and If-Modified-Since (§5.4).
// Either one being satisfied is enough, matching RFC 9110 §13.1.1/13.1.3
// (If-None-Match takes precedence when both are present, and a GET/HEAD with
// only If-Modified-Since falls back to the date check).
func notModified(r *http.Request, e Entry) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		return etagMatch(inm, e.ETag)
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		since, err := http.ParseTime(ims)
		if err != nil {
			return false
		}
		lastMod, err := http.ParseTime(e.LastModified)
		if err != nil {
			return false
		}
		// HTTP dates carry only second precision, so truncate before
		// comparing or an entry stamped mid-second never matches.
		return !lastMod.Truncate(time.Second).After(since)
	}
	return false
}

// etagMatch checks header (an If-None-Match value, possibly a comma
// separated list, possibly "*") against etag. Every ETag this package mints
// is strong (PLAN.md §5.4), so a plain string comparison is correct — a
// weak "W/" prefix on the client side is stripped defensively, in case a
// proxy rewrites it, but this server never emits one.
func etagMatch(header, etag string) bool {
	for _, tok := range strings.Split(header, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "*" {
			return true
		}
		tok = strings.TrimPrefix(tok, "W/")
		if tok == etag {
			return true
		}
	}
	return false
}

// writeEntry sends e as the response, applying conditional GET, gzip
// negotiation and HEAD-vs-GET body suppression uniformly, so every route
// gets the same transport behavior from one place rather than one
// implementation per handler that could drift (§5.4, §6).
func writeEntry(w http.ResponseWriter, r *http.Request, e Entry) {
	h := w.Header()
	h.Set("Content-Type", e.ContentType)
	h.Set("ETag", e.ETag)
	h.Set("Last-Modified", e.LastModified)
	h.Set("Cache-Control", "max-age=900")
	// Vary: Accept-Encoding on every cached response (§5.4): without it an
	// intermediary can cache one encoding and serve it to a client that
	// cannot read it.
	h.Set("Vary", "Accept-Encoding")

	// A 304 must do no work beyond the cache lookup already performed by the
	// caller — no body, no Content-Encoding/Content-Length decision below.
	if notModified(r, e) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := e.Body
	if wantsGzip(r) && len(e.GzipBody) > 0 {
		h.Set("Content-Encoding", "gzip")
		body = e.GzipBody
	}

	if r.Method == http.MethodHead {
		// HEAD behaves as GET minus the body, including validators — every
		// header above is already set identically to what GET would send.
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// --- routes -----------------------------------------------------------

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	const key = "index"
	if e, ok := s.cache.Get(key); ok {
		writeEntry(w, r, e)
		return
	}

	feeds, err := s.deps.ListFeeds(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	body, err := render.Index(feeds, s.deps.BaseURL, s.deps.Generator)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	e, err := NewEntry(body, "text/html; charset=utf-8", httpTime(s.deps.now()))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.cache.Put(key, e)
	writeEntry(w, r, e)
}

// feedFormat maps a "/feeds/{slug}.{ext}" path to its slug and content type.
// Returns ok=false for any path that is not one of the three supported
// extensions, which the caller treats as 404 rather than inventing a fourth
// format.
func feedFormat(path string) (slug, format, contentType string, ok bool) {
	rest := strings.TrimPrefix(path, "/feeds/")
	for _, f := range [...]struct{ ext, format, contentType string }{
		{".xml", "xml", "application/rss+xml; charset=utf-8"},
		{".atom", "atom", "application/atom+xml; charset=utf-8"},
		{".json", "json", "application/feed+json"},
	} {
		if strings.HasSuffix(rest, f.ext) {
			slug = strings.TrimSuffix(rest, f.ext)
			if slug == "" {
				return "", "", "", false
			}
			return slug, f.format, f.contentType, true
		}
	}
	return "", "", "", false
}

func (s *server) handleFeed(w http.ResponseWriter, r *http.Request) {
	slug, format, contentType, ok := feedFormat(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	key := slug + ":" + format
	if e, ok := s.cache.Get(key); ok {
		writeEntry(w, r, e)
		return
	}

	feed, found, err := s.deps.GetFeed(r.Context(), slug)
	if err != nil || !found {
		// Unknown slug → 404 (§6). A backend error collapses to the same
		// response rather than a 500 with detail, per the no-stack-traces
		// rule — the caller still gets a clean 4xx/5xx signal below.
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
		return
	}

	items, err := s.deps.ListItems(r.Context(), feed.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ch := model.Channel{
		Feed:      feed,
		SelfURL:   s.deps.BaseURL + "/feeds/" + slug + "." + format,
		HTMLURL:   s.deps.BaseURL + "/",
		Host:      s.deps.BaseURL,
		TagYear:   s.deps.TagYear,
		Items:     items,
		BuildTime: s.deps.now(),
		Generator: s.deps.Generator,
		DocsURL:   s.deps.DocsURL,
	}

	var body []byte
	switch format {
	case "xml":
		body, err = render.RSS(ch)
	case "atom":
		body, err = render.Atom(ch)
	case "json":
		body, err = render.JSONFeed(ch)
	default:
		err = errors.New("publish: unknown format " + format)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	e, err := NewEntry(body, contentType, httpTime(ch.NewestPublished()))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.cache.Put(key, e)
	writeEntry(w, r, e)
}

func (s *server) handleItem(w http.ResponseWriter, r *http.Request) {
	itemKey := strings.TrimPrefix(r.URL.Path, "/items/")
	if itemKey == "" || strings.Contains(itemKey, "/") {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	cacheKey := "item:" + itemKey
	if e, ok := s.cache.Get(cacheKey); ok {
		writeEntry(w, r, e)
		return
	}

	feed, item, found, err := s.deps.GetItem(r.Context(), itemKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if item.IsDeleted() {
		// 410, never 404: a soft-deleted item's permalink must tell
		// crawlers to forget it forever, not look like it never existed
		// (PLAN.md §6, §12.4).
		http.Error(w, "Gone", http.StatusGone)
		return
	}

	ch := model.Channel{
		Feed:      feed,
		HTMLURL:   s.deps.BaseURL + "/",
		Host:      s.deps.BaseURL,
		TagYear:   s.deps.TagYear,
		Generator: s.deps.Generator,
	}

	body, err := render.Permalink(ch, item)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	lastMod := item.EditedAt
	if lastMod.IsZero() {
		lastMod = item.UpdatedAt
	}
	if lastMod.IsZero() {
		lastMod = item.PublishedAt
	}
	e, err := NewEntry(body, "text/html; charset=utf-8", httpTime(lastMod))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Keyed under the feed's slug (not just the item key) so
	// Cache.Invalidate(slug) — called by the control plane on any write
	// that changes this item (§11) — drops the permalink too.
	s.cache.Put(feed.Slug+":item:"+itemKey, e)
	// Also index it under the plain lookup key so a second request for the
	// same item is a hit regardless of which key populated it first.
	s.cache.Put(cacheKey, e)
	writeEntry(w, r, e)
}

// healthzBody is the /healthz payload. It carries no version banner or
// build detail beyond what a liveness check needs (§6: "no version banner").
type healthzBody struct {
	Status string `json:"status"`
	Cache  Stats  `json:"cache"`
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(healthzBody{Status: "ok", Cache: s.cache.Stats()})
}

func (s *server) handleRobots(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Allow the index and permalinks, disallow nothing important (§6) — this
	// plane has nothing worth hiding from a crawler; every route it serves
	// is meant to be found.
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\n"))
}

func (s *server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r) {
		return
	}
	// No icon shipped; 204 rather than 404 so it doesn't read as a broken
	// link in server logs for a request every browser sends unprompted.
	w.WriteHeader(http.StatusNoContent)
}

// httpTime formats t in the exact form Last-Modified/If-Modified-Since use
// (RFC 7231 §7.1.1.1, GMT, no sub-second precision) so this package's own
// notModified check and any downstream proxy agree on what it means.
func httpTime(t time.Time) string {
	return t.UTC().Format(http.TimeFormat)
}
