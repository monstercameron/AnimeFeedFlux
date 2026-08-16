package publish

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// --- routing and validation ---------------------------------------------

func TestEmbedServesHTML(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "What year did Cowboy Bebop air?") {
		t.Error("embed did not render the feed's item")
	}
	if !strings.Contains(body, "Daily Anime Trivia") {
		t.Error("embed did not render the feed title")
	}
}

func TestEmbedUnknownSlug404(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := do(h, http.MethodGet, "/embed/no-such-feed", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestEmbedPathShape: the same exact-match rule handleItem applies. No
// trailing-slash stripping, no case folding, so one document has one URL.
func TestEmbedPathShape(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{
		"/embed/",
		"/embed/trivia-daily/",
		"/embed/Trivia-Daily",
		"/embed/trivia-daily/extra",
	} {
		if rec := do(h, http.MethodGet, path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

// TestEmbedParamsAreAClosedSet is the cache-cardinality guard. A parameter
// value outside the accepted set must be a 404, not a normalization —
// otherwise any visitor can mint unbounded cache entries by varying a query
// string and evict the feed documents this plane exists to serve.
func TestEmbedParamsAreAClosedSet(t *testing.T) {
	h, _ := newTestServer(t)

	accepted := []string{
		"/embed/trivia-daily",
		"/embed/trivia-daily?count=5",
		"/embed/trivia-daily?count=10",
		"/embed/trivia-daily?count=20",
		"/embed/trivia-daily?theme=light",
		"/embed/trivia-daily?theme=dark",
		"/embed/trivia-daily?theme=auto",
		"/embed/trivia-daily?count=5&theme=dark",
	}
	for _, path := range accepted {
		if rec := do(h, http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}

	rejected := []string{
		"/embed/trivia-daily?count=7",
		"/embed/trivia-daily?count=0",
		"/embed/trivia-daily?count=-5",
		"/embed/trivia-daily?count=9999999999999999999999",
		"/embed/trivia-daily?count=five",
		"/embed/trivia-daily?count=", // present but empty is the default
		"/embed/trivia-daily?theme=", // ditto
		"/embed/trivia-daily?theme=chartreuse",
		"/embed/trivia-daily?count=10&theme=neon",
	}
	for _, path := range rejected {
		want := http.StatusNotFound
		// An explicitly empty parameter is absent, and absent means default.
		if strings.HasSuffix(path, "=") {
			want = http.StatusOK
		}
		if rec := do(h, http.MethodGet, path, nil); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestEmbedMethodNotAllowed(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodPost, "/embed/trivia-daily", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}
}

func TestEmbedHEADMirrorsGET(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	head := do(h, http.MethodHead, "/embed/trivia-daily", nil)

	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Error("HEAD returned a body")
	}
	for _, k := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control", "Content-Security-Policy"} {
		if got, want := head.Header().Get(k), get.Header().Get(k); got != want {
			t.Errorf("HEAD %s = %q, GET = %q", k, got, want)
		}
	}
}

// --- the headers that make it embeddable, and safe ------------------------

// TestEmbedIsFramableAndOtherRoutesAreNot is the whole security posture of
// this feature in one test: exactly one route opts in to being framed.
func TestEmbedIsFramableAndOtherRoutesAreNot(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors https: http:") {
		t.Errorf("embed CSP does not permit framing: %q", csp)
	}
	for _, directive := range []string{"default-src 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("embed CSP missing %q: %q", directive, csp)
		}
	}
	// script-src is not stated separately; default-src 'none' covers it, and
	// a policy that listed every directive would be one more thing to keep
	// in step. Assert the property that matters instead.
	if strings.Contains(csp, "script-src") && !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("embed CSP relaxes script-src: %q", csp)
	}

	for _, path := range []string{
		"/",
		"/feeds/trivia-daily.xml",
		"/items/01J000000000000000000ALIVE",
		"/embed/no-such-feed", // the 404 path too
	} {
		got := do(h, http.MethodGet, path, nil).Header().Get("Content-Security-Policy")
		if got != "frame-ancestors 'none'" {
			t.Errorf("%s CSP = %q, want frame-ancestors 'none'", path, got)
		}
	}
}

// TestEmbedCSPIdenticalOnCacheHit: the second request is served from the
// render cache, and a header set only on the miss path would leave the
// document unframable for everyone after the first visitor.
func TestEmbedCSPIdenticalOnCacheHit(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	second := do(h, http.MethodGet, "/embed/trivia-daily", nil)

	if a, b := first.Header().Get("Content-Security-Policy"), second.Header().Get("Content-Security-Policy"); a != b {
		t.Fatalf("CSP differs between miss (%q) and hit (%q)", a, b)
	}
	if !strings.Contains(second.Header().Get("Content-Security-Policy"), "frame-ancestors https: http:") {
		t.Error("cache hit lost the framing permission")
	}
}

// --- caching --------------------------------------------------------------

// TestEmbedCacheHitTouchesNoBackend: an embed is fetched once per page view
// on the embedding site, not once per poll, so the hit path matters more
// here than on any other route.
func TestEmbedCacheHitTouchesNoBackend(t *testing.T) {
	h, b := newTestServer(t)

	do(h, http.MethodGet, "/embed/trivia-daily", nil)
	getFeed1, listItems1, _, _ := b.calls()

	for range 5 {
		do(h, http.MethodGet, "/embed/trivia-daily", nil)
	}
	getFeed2, listItems2, _, _ := b.calls()

	if getFeed2 != getFeed1 || listItems2 != listItems1 {
		t.Fatalf("cache hits reached the backend: GetFeed %d->%d, ListItems %d->%d",
			getFeed1, getFeed2, listItems1, listItems2)
	}
}

// TestEmbedVariantsAreSeparateEntries: distinct documents must not share a
// cache key, or a visitor who asked for dark gets whatever the previous
// visitor asked for.
func TestEmbedVariantsAreSeparateEntries(t *testing.T) {
	h, _ := newTestServer(t)

	light := do(h, http.MethodGet, "/embed/trivia-daily?theme=light", nil)
	dark := do(h, http.MethodGet, "/embed/trivia-daily?theme=dark", nil)

	if light.Body.String() == dark.Body.String() {
		t.Fatal("light and dark returned the same document")
	}
	if light.Header().Get("ETag") == dark.Header().Get("ETag") {
		t.Fatal("distinct documents share an ETag")
	}
	// The default and its explicit spelling ARE the same document, and must
	// share one entry rather than minting two.
	def := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	explicit := do(h, http.MethodGet, "/embed/trivia-daily?count=10&theme=auto", nil)
	if def.Header().Get("ETag") != explicit.Header().Get("ETag") {
		t.Error("the default and its explicit spelling produced different entries")
	}
}

// TestEmbedInvalidatedWithTheFeed: the embed key is prefixed with the slug
// precisely so a control-plane write drops it. An item deleted from the feed
// that lingers in an embed is the retraction failure §12.4 exists to prevent,
// just on somebody else's page.
func TestEmbedInvalidatedWithTheFeed(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	h, inv := NewServerAndInvalidator(b.deps(now))

	for _, path := range []string{
		"/embed/trivia-daily",
		"/embed/trivia-daily?theme=dark",
		"/embed/trivia-daily?count=5",
	} {
		if rec := do(h, http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Fatalf("warm %s = %d", path, rec.Code)
		}
	}
	before, _, _, _ := b.calls()

	// The control plane publishes a new item and invalidates.
	b.mu.Lock()
	b.items["01J00000000000000000NEW01"] = itemRow{feed: feed, item: model.Item{
		ID:          9,
		FeedID:      1,
		ItemKey:     "01J00000000000000000NEW01",
		Title:       "A newer question",
		SummaryText: "Newer.",
		PublishedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	}}
	b.mu.Unlock()
	inv.InvalidateFeed("trivia-daily")

	rec := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	if !strings.Contains(rec.Body.String(), "A newer question") {
		t.Fatal("embed served stale content after invalidation")
	}
	if after, _, _, _ := b.calls(); after == before {
		t.Fatal("embed was not re-rendered after invalidation")
	}
	// Every variant, not just the one that was re-requested.
	if rec := do(h, http.MethodGet, "/embed/trivia-daily?theme=dark", nil); !strings.Contains(rec.Body.String(), "A newer question") {
		t.Fatal("a variant survived invalidation with stale content")
	}
}

// TestEmbedConditionalGET: the validators work, because this route gets the
// most repeat traffic of anything on the plane.
func TestEmbedConditionalGET(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the embed")
	}

	second := do(h, http.MethodGet, "/embed/trivia-daily", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Error("304 carried a body")
	}

	third := do(h, http.MethodGet, "/embed/trivia-daily", map[string]string{
		"If-Modified-Since": first.Header().Get("Last-Modified"),
	})
	if third.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since status = %d, want 304", third.Code)
	}
}

// TestEmbedLastModifiedIsContentNotNow: Last-Modified must come from the
// newest item, not the clock, or every request is a full 200 and the
// conditional path never fires.
func TestEmbedLastModifiedIsContentNotNow(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/embed/trivia-daily", nil)

	got := rec.Header().Get("Last-Modified")
	want := httpTime(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)) // the item's PublishedAt
	if got != want {
		t.Fatalf("Last-Modified = %q, want %q (the newest item, not now)", got, want)
	}
}

func TestEmbedGzip(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/embed/trivia-daily", map[string]string{"Accept-Encoding": "gzip"})
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
		t.Errorf("Vary = %q", v)
	}
}

// --- content rules --------------------------------------------------------

// TestEmbedSoftDeletedItemAbsent: ListItems already excludes deleted items,
// and this asserts the embed inherits that rather than reaching around it.
func TestEmbedSoftDeletedItemAbsent(t *testing.T) {
	h, _ := newTestServer(t)
	body := do(h, http.MethodGet, "/embed/trivia-daily", nil).Body.String()
	if strings.Contains(body, "Retracted question") {
		t.Fatal("embed rendered a soft-deleted item")
	}
}

// TestEmbedDisabledFeedStillRenders: §6's rule for feed documents applies
// here for the same reason — a page that embedded this feed should not
// develop a hole because generation was paused.
func TestEmbedDisabledFeedStillRenders(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	feed.Enabled = false
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		it.FeedID = feed.ID
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	h := NewServer(b.deps(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)))

	rec := do(h, http.MethodGet, "/embed/trivia-daily", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled feed embed = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "What year did Cowboy Bebop air?") {
		t.Error("disabled feed embed lost its items")
	}
}

// TestEmbedSubscribeURLFollowsBaseURL: the footer link must track the
// operator's configured base URL like every other absolute URL on this
// plane, not the value the process booted with.
func TestEmbedSubscribeURLFollowsBaseURL(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	deps := b.deps(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	deps.BaseURLFn = func() string { return "https://anime.earlcameron.com/" }
	h := NewServer(deps)

	body := do(h, http.MethodGet, "/embed/trivia-daily", nil).Body.String()
	if !strings.Contains(body, "https://anime.earlcameron.com/feeds/trivia-daily.xml") {
		t.Error("embed subscribe link ignored the configured base URL")
	}
	if strings.Contains(body, "anime.earlcameron.com//") {
		t.Error("embed built a URL with a doubled slash")
	}
}

// TestEmbedNeverLeaksAnswerOverHTTP repeats the renderer's guarantee at the
// route, because this is the URL a third party actually fetches. If these
// two ever disagree, this is the one that describes reality.
func TestEmbedNeverLeaksAnswerOverHTTP(t *testing.T) {
	b := newFakeBackend()
	feed, _ := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	b.items["01J0000000000000000TRIVIA"] = itemRow{feed: feed, item: model.Item{
		ID:          3,
		FeedID:      1,
		ItemKey:     "01J0000000000000000TRIVIA",
		Title:       "Which studio animated Cowboy Bebop?",
		SummaryText: "Tap through to reveal the answer.",
		BodyHTML:    "<p>Which studio animated Cowboy Bebop?</p>",
		AnswerHTML:  "<p>ANSWER-SUNRISE</p>",
		PublishedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}}
	h := NewServer(b.deps(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)))

	body := do(h, http.MethodGet, "/embed/trivia-daily", nil).Body.String()
	if strings.Contains(body, "ANSWER-SUNRISE") {
		t.Fatal("the embed route served a trivia answer")
	}
	if !strings.Contains(body, "Which studio animated Cowboy Bebop?") {
		t.Fatal("the trivia item did not render at all")
	}
}

// --- observability --------------------------------------------------------

// TestEmbedRouteLabelIsBounded: §15.0a's cardinality rule. The slug must
// never reach a metric label, and neither should count/theme.
func TestEmbedRouteLabelIsBounded(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	do(h, http.MethodGet, "/embed/trivia-daily?count=5&theme=dark", nil)

	logged := buf.String()
	if !strings.Contains(logged, `"route":"/embed/{slug}"`) {
		t.Fatalf("embed did not log the bounded route label:\n%s", logged)
	}
	if strings.Contains(logged, `"route":"/embed/trivia-daily`) {
		t.Fatal("embed logged a raw path as its route label")
	}
}

// TestEmbedCacheOutcomeLogged: miss then hit, so the number this design
// exists to optimise is actually visible.
func TestEmbedCacheOutcomeLogged(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	do(h, http.MethodGet, "/embed/trivia-daily", nil)
	if !strings.Contains(buf.String(), `"cache":"miss"`) {
		t.Fatalf("first embed request was not logged as a miss:\n%s", buf.String())
	}
	buf.Reset()

	do(h, http.MethodGet, "/embed/trivia-daily", nil)
	if !strings.Contains(buf.String(), `"cache":"hit"`) {
		t.Fatalf("second embed request was not logged as a hit:\n%s", buf.String())
	}
}
