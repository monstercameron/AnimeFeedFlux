package publish

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/monstercameron/AnimeFeedFlux/internal/brand"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
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
	// It is a FALLBACK. tagYearFor prefers the feed's own creation year, so a
	// feed created in 2027 keeps a 2027 epoch even though the deployment
	// booted in 2026.
	TagYear int

	// Now is the injectable clock used to stamp cache Last-Modified/pubDate
	// fallbacks. Defaults to time.Now.
	Now func() time.Time

	// HealthFeeds supplies the per-feed staleness/error inputs for /healthz
	// (health.go's BuildHealthReport). nil means "not wired" — handleHealthz
	// then reports bare liveness only ({"status":"ok"}), which is what every
	// caller that predates health.go still gets for free. Whatever backs
	// this must come from a store.Reader, never a writer: §2's claim that the
	// publish plane structurally cannot write does not hold if a health
	// endpoint reaches for one "just to read run history".
	HealthFeeds func(ctx context.Context) ([]FeedHealthInput, error)

	// HealthGrace is the staleness grace multiplier passed to
	// BuildHealthReport. <= 0 uses DefaultHealthGrace, matching ops
	// schedule.go's own default so /healthz and the nightly Slack webhook
	// never disagree about what "stale" means.
	HealthGrace float64

	// Logger receives the canonical http.request wide event (PLAN.md
	// §15.0/§15.0a), one per request. nil defaults to slog.Default() so
	// every existing caller of NewServer/NewServerAndInvalidator that
	// predates this field keeps working unchanged, just observed through
	// the standard logger instead of a configured one.
	Logger *slog.Logger

	// Metrics records aff_http_requests_total{route,status} and
	// aff_cache_hits_total{result} for every request (PLAN.md §15.0a). nil
	// means metrics are skipped — again so existing callers keep working
	// with no telemetry rather than a nil-pointer panic.
	Metrics *obs.Metrics
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// logger returns Deps.Logger, or slog.Default() when unset.
func (d Deps) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
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
	// requestIDMiddleware wraps every route so a request_id is on r.Context()
	// — and therefore on every log line the request produces, including the
	// http.request wide event — before any handler runs. Wrapping once here,
	// rather than each handler minting its own, is what makes "every line
	// from this request shares one id" true by construction instead of by
	// six call sites remembering to do the same thing.
	return requestIDMiddleware(mux)
}

// --- request correlation (PLAN.md §15.0) ----------------------------------
//
// §15.0's field table requires request_id "on anything inside an HTTP
// request, attached by the handler from context, never by hand" — until
// this middleware existed, obs.WithRequestID had zero callers anywhere in
// the tree, so that row was aspirational. This is the wiring: generate one
// id per request, put it on the context, let internal/obs's contextHandler
// (obs.go) do the "attach to every record automatically" part it already
// implements.
//
// The id is always minted here, never read from a client header. This is a
// PUBLIC, unauthenticated plane (§2) — an inbound X-Request-Id is fully
// attacker-controlled, and honouring it would mean writing attacker-chosen
// text into every JSON log line the request produces. That is a log-
// injection surface (a value designed to look like a second field, or to
// break a downstream parser that is less strict than encoding/json) and an
// unbounded-cardinality one (nothing stops a client sending a different
// "id" on every request specifically to defeat correlation, or a fixed one
// designed to make many distinct requests look like the same one). Unlike a
// distributed trace id, there is no upstream hop here whose own id this
// plane needs to preserve — it is the origin server for these routes, not a
// proxy forwarding a chain — so there is no correlation benefit to weigh
// against that risk. The generated id is also echoed back as the
// X-Request-Id response header, purely so a client/operator who hits a
// problem can quote it back for a log search; that direction is safe
// because the value written there is one this server generated, never one
// a client supplied.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(obs.WithRequestID(r.Context(), id)))
	})
}

// requestIDBytes is 128 bits of entropy. That is not a security budget —
// unlike auth.NewToken's 256-bit session token, a request_id is never a
// secret and never gates access to anything, it only has to make two
// requests colliding on the same id practically impossible at this plane's
// traffic volume.
const requestIDBytes = 16

// requestIDFallback backstops newRequestID if crypto/rand itself ever fails
// (the OS CSPRNG unavailable — vanishingly rare on any real deployment
// target, but this is a public read path that must never fail a request
// over an observability id). atomic.Uint64 keeps the fallback path
// lock-free and safe for concurrent requests, same as the primary path.
var requestIDFallback atomic.Uint64

// newRequestID mints one id per request.
//
// This reads crypto/rand.Reader directly rather than going through
// internal/ids' ULID Source. That Source exists to mint item_key values
// with monotonic entropy (strictly increasing within the same millisecond,
// for insert-order alignment), which it buys with a mutex serializing every
// caller — exactly the kind of shared lock this plane's read path (§2: no
// writer, cache built so a hit costs no allocation beyond what net/http
// itself needs) has none of today. A request_id needs no ordering property
// at all, only uniqueness, so crypto/rand.Read — which needs no
// package-level lock of its own — is sufficient and adds no contention
// between concurrent requests that a shared monotonic source would.
//
// This does add one small allocation per request (16 bytes plus its hex
// encoding) regardless of cache hit or miss. That is a different cost than
// the one Cache exists to avoid: Cache.Get on a hit returns pre-rendered
// response bytes with no re-render/re-marshal allocation, and this change
// does not touch that path or add anything inside it. The 16-byte id is
// orthogonal — paid once per request for the sole purpose of making that
// request's own log lines findable — not a re-introduction of the
// per-response work the cache was built to skip.
func newRequestID() string {
	buf := make([]byte, requestIDBytes)
	if _, err := rand.Read(buf); err != nil {
		n := requestIDFallback.Add(1)
		return fmt.Sprintf("fallback-%d-%d", time.Now().UnixNano(), n)
	}
	return hex.EncodeToString(buf)
}

// --- observability (PLAN.md §15.0, §15.0a) --------------------------------
//
// Every route handler emits exactly one obs.HTTPRequest wide event and (if
// Deps.Metrics is wired) exactly one aff_http_requests_total /
// aff_cache_hits_total pair, via a single deferred call to (*server).observe
// registered at the top of the handler. That "exactly one defer per
// handler" shape is what makes "recorded once per request" true regardless
// of how many early-return branches (405, 404, 410, 500) a handler has —
// they all still return through the same deferred call.
//
// Route is always a fixed PATTERN, never the request's raw path (§15.0a's
// cardinality rule): "/feeds/{slug}.xml", not "/feeds/anime-news-daily.xml".
// A raw path is one label value per feed today and one per item on
// permalinks tomorrow — unbounded by construction. Every routeXxx constant
// below is a compile-time literal that never has a slug, an item_key, or
// any other request-derived text substituted into it, so there is nothing
// for the cardinality guard inside Metrics.RecordHTTPRequest to catch here
// — the bound is structural, not policed at runtime. That guard still runs
// on every call (obs.Metrics.route is a *CardinalityGuard with
// MaxDistinct=200; see internal/obs/metrics.go), so a future edit that
// slips a raw path through would still be caught rather than silently
// shipping a cardinality bomb.
const (
	routeIndex       = "/"
	routeHealthz     = "/healthz"
	routeRobots      = "/robots.txt"
	routeFavicon     = "/favicon.ico"
	routeFeedUnknown = "/feeds/{slug}" // extension didn't match xml/atom/json
	routeItem        = "/items/{item_key}"
)

// routeFeed builds the bounded feed-document route label for a matched
// format ("xml"/"atom"/"json" — one of exactly three values feedFormat can
// return, never the slug feedFormat also returns alongside it).
func routeFeed(format string) string { return "/feeds/{slug}." + format }

// statusRecorder wraps a request's http.ResponseWriter to capture the
// status code actually written, so the http.request wide event reports
// reality rather than a handler's intent (§15.0a: "the recorded status
// matches what was written"). Every handler in this file writes its
// response through writeStatus/writeEntry or a direct
// w.WriteHeader/w.Write — wrapping ResponseWriter once per request here is
// sufficient because Go's method embedding routes every one of those calls
// through WriteHeader/Write below with no other call site needing to know
// this type exists.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

// Write records an implicit 200 if WriteHeader was never called first,
// matching net/http's own default — no handler in this file relies on that
// path today (every one calls WriteHeader explicitly), but the recorder
// must not silently report status 0 if that ever changes.
func (sr *statusRecorder) Write(p []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(p)
}

// observe emits the one canonical http.request wide event for this request,
// closes the root http.request span (A9-20/A9-23), and, when Deps.Metrics is
// wired, records the two §15.0a HTTP metrics (A9-22) — all from a single
// call so nothing is ever recorded twice for one request (A9-24).
//
// cache is one of three distinct outcomes, per the instruction that
// collapsing them loses the number this whole design exists to optimise:
//   - "304": the status actually written was 304 — a cache hit (something
//     already known and unchanged) that additionally transferred no body.
//   - "hit": the render cache (server.cache) had the entry; a body was sent.
//   - "miss": the entry had to be rendered fresh.
//
// The aff_cache_hits_total metric records only the render-cache axis
// (fromCache: "hit"/"miss" — whether server.cache.Get found the entry),
// matching Metrics.RecordCacheResult's closed two-value guard
// (internal/obs/metrics.go's result CardinalityGuard); the finer 304
// distinction lives on the wide event's Cache field, which is log/trace
// detail, not a metric label.
//
// span is the root http.request span opened by the handler via obs.Start
// (obs.KindRequest — §15.0a ratio-samples publish-plane requests, default
// 5%). It is closed here, not by a separate deferred span.End() at the call
// site, so its route/status/cache attributes are always set before it ends
// regardless of which early-return branch a handler took. A 5xx status marks
// the span an error via span.SetStatus(codes.Error, ...) — that is what
// tailSampleProcessor (internal/obs/otel.go) checks to keep the span
// unconditionally, overriding the ratio, satisfying A9-23's "errors always
// sampled" independent of anything this file decides about sampling itself.
func (s *server) observe(rec *statusRecorder, r *http.Request, route string, start time.Time, fromCache bool, span oteltrace.Span) {
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}

	metricResult := "miss"
	if fromCache {
		metricResult = "hit"
	}
	wideCache := metricResult
	if status == http.StatusNotModified {
		wideCache = "304"
	}

	ctx := r.Context()
	obs.HTTPRequest(ctx, s.deps.logger(), obs.HTTPRequestFields{
		Route:    route,
		Status:   status,
		Cache:    wideCache,
		Duration: time.Since(start),
	})
	if m := s.deps.Metrics; m != nil {
		_ = m.RecordHTTPRequest(ctx, route, strconv.Itoa(status))
		_ = m.RecordCacheResult(ctx, metricResult)
	}

	span.SetAttributes(
		attribute.String(obs.FieldRoute, route),
		attribute.Int(obs.FieldStatus, status),
		attribute.String(obs.FieldCache, wideCache),
	)
	if status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(status))
	}
	span.End()
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

// writeStatus writes a plain-text status response (404, 405-adjacent errors,
// 410, 500), suppressing the body on HEAD so every non-2xx path gets the same
// "HEAD mirrors GET, minus the body" guarantee writeEntry gives the success
// path (§6). http.Error/http.NotFound don't know about HEAD — they write the
// same body regardless of method — so every call site in this file goes
// through here instead of calling them directly.
func writeStatus(w http.ResponseWriter, r *http.Request, msg string, code int) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(msg + "\n"))
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
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	var fromCache bool
	defer func() { s.observe(rec, r, routeIndex, start, fromCache, span) }()

	if r.URL.Path != "/" {
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	const key = "index"
	if e, ok := s.cache.Get(key); ok {
		fromCache = true
		writeEntry(w, r, e)
		return
	}

	feeds, err := s.deps.ListFeeds(r.Context())
	if err != nil {
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	body, err := render.Index(feeds, s.deps.BaseURL, s.deps.Generator)
	if err != nil {
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	e, err := NewEntry(body, "text/html; charset=utf-8", httpTime(s.deps.now()))
	if err != nil {
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	s.cache.Put(key, e)
	writeEntry(w, r, e)
}

// feedFormat maps a "/feeds/{slug}.{ext}" path to its slug and content type.
// Returns ok=false for any path that is not one of the three supported
// extensions, which the caller treats as 404 rather than inventing a fourth
// format.
//
// Trailing slash and case, decided once here: a slug is looked up by exact
// byte match against r.URL.Path, with no case-folding and no trailing-slash
// stripping. "/feeds/Foo.xml" and "/feeds/foo.xml/" are each a distinct,
// unmatched path — neither is silently normalized to "/feeds/foo.xml" — so
// there is exactly one canonical URL per feed document. §14.1 already
// requires slugs to be `[a-z0-9-]`, lowercase by construction, so this never
// costs a legitimate slug a match; it only refuses to mint a second cache
// entry / second ETag / second apparent feed for what a case- or
// slash-insensitive lookup would treat as "the same" URL. handleItem below
// applies the identical rule to item_key.
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
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	var fromCache bool
	// route defaults to the unmatched-extension pattern; reassigned below
	// once format is known, so the deferred call always has a bounded
	// value even on the earliest 404 branch.
	route := routeFeedUnknown
	defer func() { s.observe(rec, r, route, start, fromCache, span) }()

	slug, format, contentType, ok := feedFormat(r.URL.Path)
	if !ok {
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}
	route = routeFeed(format)
	if !allowMethod(w, r) {
		return
	}

	key := slug + ":" + format
	if e, ok := s.cache.Get(key); ok {
		// A cache hit must stay cheap (A9-21/A9-04): no render.feed span is
		// opened here — opening one only to close it immediately would add
		// span-creation cost to the one path §5's whole design exists to
		// keep free of it.
		fromCache = true
		writeEntry(w, r, e)
		return
	}

	e, err := s.renderFeed(r.Context(), slug, format, contentType)
	if err != nil {
		if errors.Is(err, errFeedNotFound) {
			writeStatus(w, r, "404 page not found", http.StatusNotFound)
			return
		}
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	s.cache.Put(key, e)
	writeEntry(w, r, e)
}

// errFeedNotFound distinguishes "unknown slug" (→ 404) from any other
// renderFeed failure (→ 500) without renderFeed's caller needing to inspect
// the underlying backend error, which §6's no-stack-traces rule keeps out of
// the response either way.
var errFeedNotFound = errors.New("publish: unknown feed slug")

// renderFeed does the on-a-cache-miss work handleFeed needs: resolve the
// feed, list its items, and render the requested format. It is the ONLY
// caller of obs.Start("render.feed", ...) in this package (A9-21), and it is
// only ever called from handleFeed's cache-miss branch — the hit branch
// above returns before renderFeed is reached, which is what keeps a hit
// "cheap" in the sense §5's design and A9-21 both require: no span opened
// and immediately closed on that path.
//
// The span nests under the http.request root span already active on ctx
// (handleFeed passes r.Context(), which carries it) via obs.KindRequest, so
// §15.0a's ratio-sampling and "errors always sampled" rules apply to it the
// same way they apply to the root — a failure recorded here (RecordError +
// SetStatus(codes.Error, ...)) makes tailSampleProcessor keep the whole
// trace regardless of the sample ratio.
func (s *server) renderFeed(ctx context.Context, slug, format, contentType string) (Entry, error) {
	ctx, span := obs.Start(ctx, "render.feed", obs.KindRequest)
	defer span.End()
	span.SetAttributes(attribute.String("format", format))

	feed, found, err := s.deps.GetFeed(ctx, slug)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}
	if !found {
		// Unknown slug → 404 (§6), not a span error: this is an ordinary,
		// expected outcome of a crawler or reader guessing at a URL, not a
		// backend failure worth flagging via SetStatus(codes.Error, ...).
		return Entry{}, errFeedNotFound
	}

	items, err := s.deps.ListItems(ctx, feed.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}
	span.SetAttributes(attribute.Int("items", len(items)))

	ch := model.Channel{
		Feed:      feed,
		SelfURL:   s.deps.BaseURL + "/feeds/" + slug + "." + format,
		HTMLURL:   s.deps.BaseURL + "/",
		Host:      s.deps.BaseURL,
		TagYear:   s.tagYearFor(feed),
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}

	e, err := NewEntry(body, contentType, httpTime(ch.NewestPublished()))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}
	span.SetAttributes(attribute.Int("bytes", len(body)))
	return e, nil
}

func (s *server) handleItem(w http.ResponseWriter, r *http.Request) {
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	var fromCache bool
	defer func() { s.observe(rec, r, routeItem, start, fromCache, span) }()

	itemKey := strings.TrimPrefix(r.URL.Path, "/items/")
	// Same trailing-slash/case rule as feedFormat above: an exact match
	// against the ULID item_key only. "/items/<key>/" has a "/" left over
	// after the trim and is rejected here, and a lower/upper-cased key is a
	// different string that GetItem simply will not find — neither path is
	// normalized into the canonical one, so a permalink resolves at exactly
	// one URL forever, matching the "resolves forever" promise this route
	// exists for.
	if itemKey == "" || strings.Contains(itemKey, "/") {
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	cacheKey := "item:" + itemKey
	if e, ok := s.cache.Get(cacheKey); ok {
		fromCache = true
		writeEntry(w, r, e)
		return
	}

	feed, item, found, err := s.deps.GetItem(r.Context(), itemKey)
	if err != nil {
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}
	if item.IsDeleted() {
		// 410, never 404: a soft-deleted item's permalink must tell
		// crawlers to forget it forever, not look like it never existed
		// (PLAN.md §6, §12.4).
		writeStatus(w, r, "Gone", http.StatusGone)
		return
	}

	ch := model.Channel{
		Feed:      feed,
		HTMLURL:   s.deps.BaseURL + "/",
		Host:      s.deps.BaseURL,
		TagYear:   s.tagYearFor(feed),
		Generator: s.deps.Generator,
	}

	body, err := render.Permalink(ch, item)
	if err != nil {
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
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
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
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
// StaleFeedCount/Feeds are populated only when Deps.HealthFeeds is wired;
// omitempty keeps the body identical to the pre-health.go shape otherwise.
type healthzBody struct {
	Status         string       `json:"status"`
	Cache          Stats        `json:"cache"`
	StaleFeedCount int          `json:"stale_feed_count,omitempty"`
	Feeds          []FeedHealth `json:"feeds,omitempty"`
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	defer func() { s.observe(rec, r, routeHealthz, start, false, span) }()

	if !allowMethod(w, r) {
		return
	}

	body := healthzBody{Status: "ok", Cache: s.cache.Stats()}
	status := http.StatusOK
	if s.deps.HealthFeeds != nil {
		inputs, err := s.deps.HealthFeeds(r.Context())
		if err != nil {
			writeStatus(w, r, "internal error", http.StatusInternalServerError)
			return
		}
		report := BuildHealthReport(inputs, s.deps.now(), s.deps.HealthGrace)
		body.Status = report.Status
		body.StaleFeedCount = report.StaleFeedCount
		body.Feeds = report.Feeds
		// report.HTTPStatus() is always 200 — a stale/degraded feed does not
		// flip liveness to a non-2xx status; see health.go's package doc
		// comment for the full argument. Called here rather than hardcoded
		// so this call site has nothing left to decide.
		status = report.HTTPStatus()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *server) handleRobots(w http.ResponseWriter, r *http.Request) {
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	defer func() { s.observe(rec, r, routeRobots, start, false, span) }()

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
	start := s.deps.now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	defer func() { s.observe(rec, r, routeFavicon, start, false, span) }()

	if !allowMethod(w, r) {
		return
	}
	// Served from the binary (internal/brand), not from disk: this plane is
	// the internet-facing one and runs on a read-only root filesystem, so
	// "the icon file is missing" must not be a state it can be in.
	//
	// A long max-age is safe in a way it is not for a feed: unlike feed XML
	// this asset changes only when the brand does, and a stale icon in a
	// cache is a cosmetic wrong, not a subscriber missing an item.
	hdr := w.Header()
	hdr.Set("Content-Type", "image/x-icon")
	hdr.Set("Cache-Control", "public, max-age=604800")
	hdr.Set("ETag", faviconETag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, faviconETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Length", strconv.Itoa(len(brand.FaviconICO)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(brand.FaviconICO)
}

// faviconETag is derived from the embedded bytes at init, so it changes when
// and only when the artwork does — a hand-written version string would go
// stale silently the first time the icon is replaced without touching it.
var faviconETag = func() string {
	sum := sha256.Sum256(brand.FaviconICO)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}()

// httpTime formats t in the exact form Last-Modified/If-Modified-Since use
// (RFC 7231 §7.1.1.1, GMT, no sub-second precision) so this package's own
// notModified check and any downstream proxy agree on what it means.
func httpTime(t time.Time) string {
	return t.UTC().Format(http.TimeFormat)
}

// tagYearFor resolves a feed's Tag URI epoch.
//
// Per feed, not per deployment: §5.1's guid embeds a year and must be stable
// forever, so it comes from when THIS feed was created. Deps.TagYear is only a
// fallback for a feed with no recorded creation time — falling back to
// time.Now().Year() would silently rewrite every guid at midnight on 1 January.
func (s *server) tagYearFor(f model.Feed) int {
	if !f.CreatedAt.IsZero() {
		return f.CreatedAt.UTC().Year()
	}
	return s.deps.TagYear
}
