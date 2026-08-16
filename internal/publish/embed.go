package publish

// embed.go serves GET/HEAD /embed/{slug}: a small, self-contained HTML
// document listing a feed's newest items, meant to be pulled into a page
// this project does not control via an <iframe> (PLAN.md §6).
//
// It is a read-only document like every other route on this plane — no
// writer, no store import, same cache, same validators — and that is what
// makes it admissible here at all. The rejected alternative was a
// script-tag widget; internal/render/embed.go's doc comment carries that
// argument, because the reasoning is about what gets rendered, not about
// how it is routed.
//
// The two query parameters are validated against a closed set and anything
// else is a 404, not a normalization. That is the same rule feedFormat
// applies to extensions and handleItem applies to trailing slashes: one URL
// per document. It also bounds the render cache, which matters more here
// than anywhere else on this plane — an embed is fetched once per page view
// by every visitor to the embedding site, not once per poll by a feed
// reader, so a query string that could mint unbounded cache entries would
// be a way for any visitor to evict the feed documents this plane exists to
// serve.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
)

// routeEmbed is the bounded metrics/log label for this route. Like every
// other routeXxx constant it is a compile-time literal with nothing
// request-derived in it (§15.0a's cardinality rule) — and the count/theme
// parameters are deliberately NOT part of it either: they are bounded, but
// nine label values per route buys nothing a single one does not.
const routeEmbed = "/embed/{slug}"

// embedCSP is the embed document's Content-Security-Policy.
//
// The frame-ancestors list is the point of the route: this document exists
// to be framed by sites we have never heard of, so it opts out of the
// plane's default (see defaultCSP). Everything else is shut off.
//
// "https: http:" rather than the obvious "*", and a browser is what settled
// that. CSP's "*" matches only URLs whose scheme is a network scheme OR
// matches the document's own scheme — so a page opened from disk cannot
// frame an http: embed under "*", and Chrome refuses with "the scheme
// 'http:' must be added explicitly". Naming both schemes says what was meant
// all along ("any website"), and it is the same policy under TLS in
// production as it is on a developer's box, which "*" is not. A unit test
// asserting the header string could not have found this; loading the page in
// a real browser did, first try. The document loads
// no script, no font, no image and no stylesheet — it is text plus one
// inline <style> — so 'none' is not aspirational hardening, it is a
// description of the page. style-src 'unsafe-inline' is the one concession,
// and it is what "inline <style>" means to CSP; the alternative is a hash
// that has to be recomputed whenever the stylesheet changes, on a page whose
// stylesheet is a compile-time constant emitted by one function.
//
// The value of script-src 'none' here is specific: this is the first thing
// this project renders into somebody else's page, so it is the first place
// where a sanitiser regression would be their problem rather than ours. The
// renderer already refuses to emit item HTML at all (only escaped text), and
// this header means that even if that changed, nothing in the document could
// execute.
const embedCSP = "default-src 'none'; style-src 'unsafe-inline'; " +
	"frame-ancestors https: http:; base-uri 'none'; form-action 'none'"

func (s *server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, span := obs.Start(r.Context(), "http.request", obs.KindRequest)
	r = r.WithContext(ctx)
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	var fromCache bool
	defer func() { s.observe(rec, r, routeEmbed, start, fromCache, span) }()

	// Same exact-match rule as handleItem: no trailing-slash stripping, no
	// case folding. "/embed/foo/" keeps its slash and does not resolve.
	slug := strings.TrimPrefix(r.URL.Path, "/embed/")
	if slug == "" || strings.Contains(slug, "/") {
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}
	if !allowMethod(w, r) {
		return
	}

	opts, ok := embedOptions(r)
	if !ok {
		// 404 rather than 400: an unrecognized value makes this a URL the
		// route set does not contain, which is the same answer /feeds/x.rss
		// gets. It also keeps §6's no-information-leak posture — the response
		// does not enumerate what the accepted values are.
		writeStatus(w, r, "404 page not found", http.StatusNotFound)
		return
	}

	// Prefixed with the slug so Cache.Invalidate(slug) drops it along with
	// the feed documents — a promoted, edited or deleted item must not linger
	// in an embed after it has left the feed. The count/theme suffix is what
	// keeps the nine possible documents distinct.
	key := slug + ":embed:" + strconv.Itoa(opts.Count) + ":" + opts.Theme
	if e, ok := s.cache.Get(key); ok {
		fromCache = true
		writeEmbedEntry(w, r, e, s.cacheControl())
		return
	}

	e, err := s.renderEmbed(r.Context(), slug, opts)
	if err != nil {
		if errors.Is(err, errFeedNotFound) {
			writeStatus(w, r, "404 page not found", http.StatusNotFound)
			return
		}
		writeStatus(w, r, "internal error", http.StatusInternalServerError)
		return
	}
	s.cache.Put(key, e)
	writeEmbedEntry(w, r, e, s.cacheControl())
}

// writeEmbedEntry is writeEntry with the embed's own Content-Security-Policy
// in front of it.
//
// It exists so the policy is attached to the DOCUMENT rather than to the
// route: the 404 an unknown slug gets is a plain-text error page, not
// something anyone should be framing, and setting the header early enough to
// cover every branch of the handler also advertised "frame me" on that error.
// Both the cache hit and the cache miss go through here, so a hit and a miss
// send byte-identical headers — a policy set only on the miss path would
// leave the document unframable for every visitor after the first.
func writeEmbedEntry(w http.ResponseWriter, r *http.Request, e Entry, cacheControl string) {
	w.Header().Set("Content-Security-Policy", embedCSP)
	writeEntry(w, r, e, cacheControl)
}

// embedOptions reads and validates the two query parameters. An absent
// parameter takes the renderer's default; a present one must be exactly one
// of the accepted values.
//
// Validation lives against render.ValidEmbedCount/ValidEmbedTheme rather
// than a second list here, so the route cannot come to accept a value the
// renderer has no CSS or no window size for.
func embedOptions(r *http.Request) (render.EmbedOptions, bool) {
	q := r.URL.Query()
	o := render.EmbedOptions{Count: render.DefaultEmbedCount, Theme: render.DefaultEmbedTheme}

	if raw := q.Get("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !render.ValidEmbedCount(n) {
			return render.EmbedOptions{}, false
		}
		o.Count = n
	}
	if raw := q.Get("theme"); raw != "" {
		if !render.ValidEmbedTheme(raw) {
			return render.EmbedOptions{}, false
		}
		o.Theme = raw
	}
	return o, true
}

// renderEmbed is the cache-miss path: resolve the feed, list its window,
// render. It mirrors renderFeed, including the "unknown slug is not a span
// error" rule — a crawler guessing at embed URLs is an ordinary outcome, not
// a backend failure.
//
// A DISABLED feed still renders, exactly as its feed documents still serve
// (§6): a page that has embedded this feed should not develop a hole because
// generation was paused.
func (s *server) renderEmbed(ctx context.Context, slug string, o render.EmbedOptions) (Entry, error) {
	ctx, span := obs.Start(ctx, "render.embed", obs.KindRequest)
	defer span.End()
	span.SetAttributes(
		attribute.Int("count", o.Count),
		attribute.String("theme", o.Theme),
	)

	feed, found, err := s.deps.GetFeed(ctx, slug)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}
	if !found {
		return Entry{}, errFeedNotFound
	}

	items, err := s.deps.ListItems(ctx, feed.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}

	ch := model.Channel{
		Feed: feed,
		// SelfURL is the RSS URL, not this document's own: the only link in
		// the embed's footer is "subscribe", and the thing worth subscribing
		// to is the feed. An embed that linked to itself would be a dead end
		// on somebody else's page.
		SelfURL:   s.baseURL() + "/feeds/" + slug + ".xml",
		HTMLURL:   s.baseURL() + "/",
		Host:      s.baseURL(),
		TagYear:   s.tagYearFor(feed),
		Items:     items,
		BuildTime: s.deps.now(),
		Generator: s.deps.Generator,
		DocsURL:   s.deps.DocsURL,
	}

	body, err := render.Embed(ch, o)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}

	// Last-Modified from the newest item, like a feed document — not from
	// now(), which would defeat the conditional GET on the one route that
	// gets hit once per page view rather than once per poll.
	e, err := NewEntry(body, "text/html; charset=utf-8", httpTime(ch.NewestPublished()))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Entry{}, err
	}
	span.SetAttributes(attribute.Int("bytes", len(body)))
	return e, nil
}
