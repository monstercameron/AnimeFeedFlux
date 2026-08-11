package publish

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

// fakeBackend is a hand-rolled stand-in for internal/store, so this package
// stays decoupled from it (per the plan: publish must not import
// internal/store). It counts calls so tests can assert a cache hit never
// reaches it.
type fakeBackend struct {
	mu    sync.Mutex
	feeds map[string]model.Feed
	items map[string]itemRow // item_key -> row

	getFeedCalls   int
	listItemsCalls int
	getItemCalls   int
	listFeedsCalls int
}

type itemRow struct {
	feed model.Feed
	item model.Item
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		feeds: map[string]model.Feed{},
		items: map[string]itemRow{},
	}
}

func (b *fakeBackend) deps(now time.Time) Deps {
	return Deps{
		GetFeed: func(_ context.Context, slug string) (model.Feed, bool, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.getFeedCalls++
			f, ok := b.feeds[slug]
			return f, ok, nil
		},
		ListItems: func(_ context.Context, feedID int64) ([]model.Item, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.listItemsCalls++
			var out []model.Item
			for _, row := range b.items {
				if row.feed.ID == feedID && !row.item.IsDeleted() {
					out = append(out, row.item)
				}
			}
			return out, nil
		},
		ListFeeds: func(_ context.Context) ([]model.Feed, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.listFeedsCalls++
			var out []model.Feed
			for _, f := range b.feeds {
				out = append(out, f)
			}
			return out, nil
		},
		GetItem: func(_ context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.getItemCalls++
			row, ok := b.items[itemKey]
			return row.feed, row.item, ok, nil
		},
		BaseURL:   "https://anime.example.com",
		Generator: "AnimeFeedFlux test",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026,
		Now:       func() time.Time { return now },
	}
}

func (b *fakeBackend) calls() (getFeed, listItems, getItem, listFeeds int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getFeedCalls, b.listItemsCalls, b.getItemCalls, b.listFeedsCalls
}

func testFeedAndItems() (model.Feed, []model.Item) {
	feed := model.Feed{
		ID:          1,
		Slug:        "trivia-daily",
		Title:       "Daily Anime Trivia",
		Description: "One question a day.",
		Language:    "en",
		Kind:        model.KindGenerative,
		Enabled:     true,
		TTLMinutes:  15,
	}
	items := []model.Item{
		{
			ID:          1,
			FeedID:      1,
			ItemKey:     "01J000000000000000000ALIVE",
			ContentHash: "h1",
			Title:       "What year did Cowboy Bebop air?",
			SummaryText: "A trivia question about a classic.",
			BodyHTML:    "<p>Guess the year.</p>",
			Link:        "https://anime.example.com/items/01J000000000000000000ALIVE",
			PublishedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			Origin:      model.OriginGenerated,
		},
	}
	return feed, items
}

func newTestServer(t *testing.T) (http.Handler, *fakeBackend) {
	t.Helper()
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	b.items["01J000000000000000000DEAD"] = itemRow{
		feed: feed,
		item: model.Item{
			ID:          2,
			FeedID:      1,
			ItemKey:     "01J000000000000000000DEAD",
			Title:       "Retracted question",
			SummaryText: "gone",
			BodyHTML:    "<p>gone</p>",
			PublishedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			DeletedAt:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Origin:      model.OriginGenerated,
		},
	}

	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	return NewServer(b.deps(now)), b
}

func do(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- content type -----------------------------------------------------

func TestFeedContentTypes(t *testing.T) {
	h, _ := newTestServer(t)
	cases := []struct {
		path, want string
	}{
		{"/feeds/trivia-daily.xml", "application/rss+xml; charset=utf-8"},
		{"/feeds/trivia-daily.atom", "application/atom+xml; charset=utf-8"},
		{"/feeds/trivia-daily.json", "application/feed+json"},
	}
	for _, c := range cases {
		rec := do(h, http.MethodGet, c.path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", c.path, rec.Code, rec.Body.String())
		}
		got := rec.Header().Get("Content-Type")
		if got != c.want {
			t.Errorf("%s: Content-Type = %q, want %q", c.path, got, c.want)
		}
		if got == "text/xml" {
			t.Errorf("%s: Content-Type must never be text/xml", c.path)
		}
	}
}

// --- Vary ---------------------------------------------------------------

func TestVaryAcceptEncodingOnEveryFeedResponse(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/feeds/trivia-daily.xml", "/feeds/trivia-daily.atom", "/feeds/trivia-daily.json"} {
		rec := do(h, http.MethodGet, path, nil)
		if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
			t.Errorf("%s: Vary = %q, want %q", path, v, "Accept-Encoding")
		}
	}
}

// --- Cache-Control --------------------------------------------------------

func TestCacheControlMaxAge900(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if got := rec.Header().Get("Cache-Control"); got != "max-age=900" {
		t.Errorf("Cache-Control = %q, want %q", got, "max-age=900")
	}
}

// --- conditional GET ------------------------------------------------------

func TestConditionalGET_IfNoneMatch304(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}

	second := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-None-Match": etag,
	})
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 response has a body: %q", second.Body.String())
	}
}

func TestConditionalGET_IfModifiedSince304(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	lastMod := first.Header().Get("Last-Modified")
	if lastMod == "" {
		t.Fatal("Last-Modified missing on first response")
	}

	second := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-Modified-Since": lastMod,
	})
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 response has a body: %q", second.Body.String())
	}
}

func TestConditionalGET_BothValidatorsWork(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	etag := first.Header().Get("ETag")
	lastMod := first.Header().Get("Last-Modified")

	// Stale If-None-Match but fresh If-Modified-Since: our implementation
	// prefers If-None-Match when present, per RFC 9110 precedence, so a
	// mismatched ETag alone (no If-Modified-Since) must NOT 304.
	miss := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-None-Match": `"not-the-real-etag"`,
	})
	if miss.Code != http.StatusOK {
		t.Fatalf("stale If-None-Match: status = %d, want 200", miss.Code)
	}

	hitByEtag := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-None-Match": etag,
	})
	if hitByEtag.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match hit: status = %d, want 304", hitByEtag.Code)
	}

	hitByDate := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-Modified-Since": lastMod,
	})
	if hitByDate.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since hit: status = %d, want 304", hitByDate.Code)
	}
}

func Test304DoesNotInvokeBackendBeyondCacheLookup(t *testing.T) {
	h, b := newTestServer(t)
	first := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	etag := first.Header().Get("ETag")

	getFeed1, listItems1, _, _ := b.calls()

	do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{"If-None-Match": etag})

	getFeed2, listItems2, _, _ := b.calls()
	if getFeed2 != getFeed1 || listItems2 != listItems1 {
		t.Fatalf("304 touched the backend: GetFeed %d->%d, ListItems %d->%d",
			getFeed1, getFeed2, listItems1, listItems2)
	}
}

// --- HEAD -----------------------------------------------------------------

func TestHEADMatchesGETHeadersWithEmptyBody(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	head := do(h, http.MethodHead, "/feeds/trivia-daily.xml", nil)

	if head.Code != get.Code {
		t.Fatalf("HEAD status = %d, want %d", head.Code, get.Code)
	}
	for _, hdr := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control", "Vary"} {
		if head.Header().Get(hdr) != get.Header().Get(hdr) {
			t.Errorf("HEAD %s = %q, GET %s = %q", hdr, head.Header().Get(hdr), hdr, get.Header().Get(hdr))
		}
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD body not empty: %q", head.Body.String())
	}
}

// --- gzip -------------------------------------------------------------

func TestGzipRoundTripsToSameBytes(t *testing.T) {
	h, _ := newTestServer(t)
	plain := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)

	gz := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{"Accept-Encoding": "gzip"})
	if enc := gz.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if !bytes.Equal(got, plain.Body.Bytes()) {
		t.Fatalf("gzip round-trip mismatch:\n got=%q\nwant=%q", got, plain.Body.Bytes())
	}
}

func TestNoGzipWithoutAcceptEncoding(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding = %q, want empty (client sent no Accept-Encoding)", enc)
	}
}

// --- 405 --------------------------------------------------------------

func Test405ForNonGetHead(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/feeds/trivia-daily.xml", "/", "/items/01J000000000000000000ALIVE", "/healthz", "/robots.txt", "/favicon.ico"} {
		rec := do(h, http.MethodPost, path, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: POST status = %d, want 405", path, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, want %q", path, allow, "GET, HEAD")
		}
	}
}

// --- 404 / 410 --------------------------------------------------------

func TestUnknownSlugIs404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/feeds/does-not-exist.xml", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUnknownFormatIs404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/feeds/trivia-daily.rss", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeletedItemIs410NeverA404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/items/01J000000000000000000DEAD", nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
}

func TestUnknownItemIs404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/items/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestLiveItemServes200(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

// --- cache hit must not touch the backend ------------------------------

func TestCacheHitDoesNotInvokeRenderFunction(t *testing.T) {
	h, b := newTestServer(t)

	first := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}
	getFeed1, listItems1, _, _ := b.calls()
	if getFeed1 == 0 || listItems1 == 0 {
		t.Fatalf("first request did not reach the backend: GetFeed=%d ListItems=%d", getFeed1, listItems1)
	}

	second := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second request status = %d", second.Code)
	}
	getFeed2, listItems2, _, _ := b.calls()
	if getFeed2 != getFeed1 || listItems2 != listItems1 {
		t.Fatalf("cache hit invoked the backend: GetFeed %d->%d, ListItems %d->%d",
			getFeed1, getFeed2, listItems1, listItems2)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("cached response body differs from the original render")
	}
}

// --- index / healthz / robots / favicon --------------------------------

func TestIndexServesHTML(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

func TestHealthz(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestHealthzUnwiredIsBareLiveness locks in that a Deps with no HealthFeeds
// (every caller that predates health.go, and every other test in this file)
// keeps getting the original liveness-only body: no stale_feed_count, no
// feeds, status "ok".
func TestHealthzUnwiredIsBareLiveness(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("Status = %q, want ok", body.Status)
	}
	if body.StaleFeedCount != 0 || body.Feeds != nil {
		t.Errorf("expected no health detail when HealthFeeds is unwired, got StaleFeedCount=%d Feeds=%v",
			body.StaleFeedCount, body.Feeds)
	}
}

// TestHealthzWiredHealthyUnchanged: a wired HealthFeeds with nothing stale
// still reports 200 and status "ok", with detail present.
func TestHealthzWiredHealthyUnchanged(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.HealthFeeds = func(context.Context) ([]FeedHealthInput, error) {
		return []FeedHealthInput{{
			FeedStatus: ops.FeedStatus{
				FeedSlug:      feed.Slug,
				Enabled:       true,
				Interval:      24 * time.Hour,
				LastSuccessAt: now.Add(-1 * time.Hour),
			},
		}}, nil
	}
	h := NewServer(deps)

	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("Status = %q, want ok", body.Status)
	}
	if body.StaleFeedCount != 0 {
		t.Errorf("StaleFeedCount = %d, want 0", body.StaleFeedCount)
	}
	if len(body.Feeds) != 1 || body.Feeds[0].FeedSlug != feed.Slug {
		t.Errorf("Feeds = %+v, want one entry for %s", body.Feeds, feed.Slug)
	}
}

// TestHealthzWiredDegradedStillReturns200 is the surprising case that most
// needs a test stating it is intentional: a stale feed flips the JSON
// `status` to "degraded" and populates stale_feed_count/Feeds[].stale, but
// the HTTP status code stays 200 — /healthz is liveness, not readiness (see
// health.go's package doc comment).
func TestHealthzWiredDegradedStillReturns200(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.HealthFeeds = func(context.Context) ([]FeedHealthInput, error) {
		return []FeedHealthInput{{
			FeedStatus: ops.FeedStatus{
				FeedSlug: feed.Slug,
				Enabled:  true,
				Interval: 24 * time.Hour,
				// never succeeded -> stale
			},
			ErrorCount: 4,
		}}, nil
	}
	h := NewServer(deps)

	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even though degraded (liveness, not readiness)", rec.Code)
	}
	var body healthzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", body.Status)
	}
	if body.StaleFeedCount != 1 {
		t.Errorf("StaleFeedCount = %d, want 1", body.StaleFeedCount)
	}
	if len(body.Feeds) != 1 || !body.Feeds[0].Stale {
		t.Fatalf("Feeds = %+v, want one stale entry", body.Feeds)
	}
	if body.Feeds[0].ErrorCount != 4 {
		t.Errorf("ErrorCount = %d, want 4", body.Feeds[0].ErrorCount)
	}
}

// TestHealthzWiredBackendErrorIs500: a genuine failure to read health
// inputs is the "process cannot answer" case health.go's doc comment
// carves out — it must not masquerade as a healthy 200.
func TestHealthzWiredBackendErrorIs500(t *testing.T) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.HealthFeeds = func(context.Context) ([]FeedHealthInput, error) {
		return nil, errors.New("store unavailable")
	}
	handler := NewServer(deps)

	rec := do(handler, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRobots(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/robots.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Allow: /")) {
		t.Errorf("robots.txt does not allow /: %q", rec.Body.String())
	}
}

// TestFavicon covers the real icon this plane now serves from the binary
// (internal/brand's embedded bytes). It used to assert 204 — correct while
// no icon shipped, and updated 2026-08-10 when one did, along with the
// conditional-GET behaviour that matters for an asset every browser,
// crawler and unfurler requests unprompted on every visit.
func TestFavicon(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/favicon.ico", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("Content-Type = %q, want image/x-icon", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("favicon body is empty — the embedded icon did not reach the response")
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing: a browser would re-download the icon on every page load")
	}

	// The 304 path, since this asset is requested on every visit and its
	// bytes never change between builds.
	second := do(h, http.MethodGet, "/favicon.ico", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 response has a body: %d bytes", second.Body.Len())
	}

	// HEAD parity (§5.4: "HEAD behaves as GET minus the body").
	head := do(h, http.MethodHead, "/favicon.ico", nil)
	if head.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD returned a body: %d bytes", head.Body.Len())
	}
}

// --- conditional GET beyond the feed documents -----------------------

// TestConditionalGET_IndexAlsoSupportsIt locks in that "/" goes through the
// same writeEntry path as the feed documents — a poller of the index (small,
// but constant traffic, per PLAN.md §6) gets a 304 too, not just the XML.
func TestConditionalGET_IndexAlsoSupportsIt(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on index response")
	}
	second := do(h, http.MethodGet, "/", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 response has a body: %q", second.Body.String())
	}
}

// TestConditionalGET_PermalinkAlsoSupportsIt is the same check for
// /items/{item_key} — the URL a reader hits when a subscriber clicks
// through, and a Slack unfurl hits repeatedly.
func TestConditionalGET_PermalinkAlsoSupportsIt(t *testing.T) {
	h, _ := newTestServer(t)
	first := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on permalink response")
	}
	second := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 response has a body: %q", second.Body.String())
	}
}

// --- HEAD beyond the feed documents ------------------------------------

func TestHEADMatchesGETOnIndex(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/", nil)
	head := do(h, http.MethodHead, "/", nil)
	if head.Code != get.Code {
		t.Fatalf("HEAD status = %d, want %d", head.Code, get.Code)
	}
	for _, hdr := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control", "Vary"} {
		if head.Header().Get(hdr) != get.Header().Get(hdr) {
			t.Errorf("HEAD %s = %q, GET %s = %q", hdr, head.Header().Get(hdr), hdr, get.Header().Get(hdr))
		}
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD body not empty: %q", head.Body.String())
	}
}

func TestHEADMatchesGETOnPermalink(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	head := do(h, http.MethodHead, "/items/01J000000000000000000ALIVE", nil)
	if head.Code != get.Code {
		t.Fatalf("HEAD status = %d, want %d", head.Code, get.Code)
	}
	for _, hdr := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control", "Vary"} {
		if head.Header().Get(hdr) != get.Header().Get(hdr) {
			t.Errorf("HEAD %s = %q, GET %s = %q", hdr, head.Header().Get(hdr), hdr, get.Header().Get(hdr))
		}
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD body not empty: %q", head.Body.String())
	}
}

// TestHEADOn404HasNoBody and TestHEADOn410HasNoBody guard writeStatus: a GET
// 404/410 carries an explanatory plain-text body, but the matching HEAD must
// carry none, with otherwise identical headers — the same "HEAD mirrors GET
// minus the body" contract writeEntry gives 200s, extended to the error
// paths (PLAN.md §6).
func TestHEADOn404HasNoBody(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/items/does-not-exist", nil)
	head := do(h, http.MethodHead, "/items/does-not-exist", nil)
	if head.Code != http.StatusNotFound || head.Code != get.Code {
		t.Fatalf("GET status = %d, HEAD status = %d, want both 404", get.Code, head.Code)
	}
	if head.Header().Get("Content-Type") != get.Header().Get("Content-Type") {
		t.Errorf("Content-Type mismatch: HEAD=%q GET=%q", head.Header().Get("Content-Type"), get.Header().Get("Content-Type"))
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD 404 body not empty: %q", head.Body.String())
	}
	if get.Body.Len() == 0 {
		t.Errorf("GET 404 body unexpectedly empty")
	}
}

func TestHEADOn410HasNoBody(t *testing.T) {
	h, _ := newTestServer(t)
	get := do(h, http.MethodGet, "/items/01J000000000000000000DEAD", nil)
	head := do(h, http.MethodHead, "/items/01J000000000000000000DEAD", nil)
	if head.Code != http.StatusGone || head.Code != get.Code {
		t.Fatalf("GET status = %d, HEAD status = %d, want both 410", get.Code, head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD 410 body not empty: %q", head.Body.String())
	}
	if get.Body.Len() == 0 {
		t.Errorf("GET 410 body unexpectedly empty")
	}
}

// --- Vary beyond the feed documents -------------------------------------

func TestVaryAcceptEncodingOnIndexAndPermalink(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/", "/items/01J000000000000000000ALIVE"} {
		rec := do(h, http.MethodGet, path, nil)
		if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
			t.Errorf("%s: Vary = %q, want %q", path, v, "Accept-Encoding")
		}
	}
}

// --- content types beyond the feed documents ----------------------------

func TestContentTypesOfNonFeedRoutes(t *testing.T) {
	h, _ := newTestServer(t)
	cases := []struct {
		path, want string
	}{
		{"/", "text/html; charset=utf-8"},
		{"/items/01J000000000000000000ALIVE", "text/html; charset=utf-8"},
		{"/robots.txt", "text/plain; charset=utf-8"},
		{"/healthz", "application/json; charset=utf-8"},
	}
	for _, c := range cases {
		rec := do(h, http.MethodGet, c.path, nil)
		if got := rec.Header().Get("Content-Type"); got != c.want {
			t.Errorf("%s: Content-Type = %q, want %q", c.path, got, c.want)
		}
	}
}

// --- trailing slash and case: decided once, held everywhere ------------

// TestTrailingSlashNotResolved locks in the decision documented on
// feedFormat/handleItem: a trailing slash is a different, unmatched path,
// not a redirect to the canonical URL. One URL per resource, one cache
// entry, one ETag.
func TestTrailingSlashNotResolved(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{
		"/feeds/trivia-daily.xml/",
		"/items/01J000000000000000000ALIVE/",
	} {
		rec := do(h, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (trailing slash must not resolve)", path, rec.Code)
		}
	}
}

// TestSlugAndItemKeyAreCaseSensitive locks in the other half of that
// decision: no case-folding lookup, so an upper/lower-cased variant of a
// valid slug or item_key is a 404, not a second URL for the same resource.
func TestSlugAndItemKeyAreCaseSensitive(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{
		"/feeds/TRIVIA-DAILY.xml",
		"/feeds/Trivia-Daily.xml",
		"/items/01j000000000000000000alive",
	} {
		rec := do(h, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (case must not resolve to the canonical slug/key)", path, rec.Code)
		}
	}
}

// --- item permalink cache hit must not touch the backend ---------------

func TestItemCacheHitDoesNotInvokeBackend(t *testing.T) {
	h, b := newTestServer(t)

	first := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}
	_, _, getItem1, _ := b.calls()
	if getItem1 == 0 {
		t.Fatalf("first request did not reach the backend")
	}

	second := do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second request status = %d", second.Code)
	}
	_, _, getItem2, _ := b.calls()
	if getItem2 != getItem1 {
		t.Fatalf("cache hit invoked the backend: GetItem %d->%d", getItem1, getItem2)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("cached response body differs from the original render")
	}
}

// --- no leaked internals ------------------------------------------------

// TestIndexOmitsDisabledFeedAndInternalIDs is the HTTP-level companion to
// render.TestIndex_DisabledFeedExcluded: it drives the same exclusion
// through the real handler and backend wiring, and additionally checks the
// numeric primary key never appears in the served bytes — the index is a
// public read-only surface (§2), so anything that reaches it is published,
// disabled/internal-id or not.
func TestIndexOmitsDisabledFeedAndInternalIDs(t *testing.T) {
	b := newFakeBackend()
	live := model.Feed{ID: 1, Slug: "live-feed", Title: "Live Feed", Description: "Still running.", Enabled: true}
	dead := model.Feed{ID: 999999, Slug: "dead-feed", Title: "Dead Feed", Description: "Stopped generating.", Enabled: false}
	b.feeds[live.Slug] = live
	b.feeds[dead.Slug] = dead

	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	h := NewServer(b.deps(now))

	rec := do(h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !bytes.Contains(rec.Body.Bytes(), []byte("Live Feed")) {
		t.Error("expected enabled feed to appear on the index")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("Dead Feed")) || bytes.Contains(rec.Body.Bytes(), []byte("dead-feed")) {
		t.Errorf("disabled feed leaked onto the public index:\n%s", body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("999999")) {
		t.Errorf("internal feed ID leaked onto the public index:\n%s", body)
	}
}

func TestBackendErrorDoesNotLeakDetail(t *testing.T) {
	b := newFakeBackend()
	deps := b.deps(time.Now())
	deps.GetFeed = func(context.Context, string) (model.Feed, bool, error) {
		return model.Feed{}, false, errors.New("dial tcp 10.0.0.1:5432: connection refused")
	}
	h := NewServer(deps)
	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if bytes.Contains(rec.Body.Bytes(), []byte("10.0.0.1")) {
		t.Fatalf("response leaked backend error detail: %q", rec.Body.String())
	}
}

// --- observability (PLAN.md §15.0, §15.0a) --------------------------------

// newObservedTestServer is newTestServer plus a Logger writing JSON to buf
// and a Metrics instance backed by a ManualReader, so tests can inspect both
// the http.request wide event and the aff_http_requests_total /
// aff_cache_hits_total metrics for real requests through the real handler.
func newObservedTestServer(t *testing.T) (h http.Handler, buf *bytes.Buffer, m *obs.Metrics, reader *sdkmetric.ManualReader, b *fakeBackend) {
	t.Helper()
	b = newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	b.items["01J000000000000000000DEAD"] = itemRow{
		feed: feed,
		item: model.Item{
			ID:          2,
			FeedID:      1,
			ItemKey:     "01J000000000000000000DEAD",
			Title:       "Retracted question",
			SummaryText: "gone",
			BodyHTML:    "<p>gone</p>",
			PublishedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			DeletedAt:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Origin:      model.OriginGenerated,
		},
	}

	buf = &bytes.Buffer{}
	log := obs.NewLogger(obs.Options{Level: slog.LevelDebug, Writer: buf})

	reader = sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	var err error
	m, err = obs.NewMetrics(mp)
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}

	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.Logger = log
	deps.Metrics = m
	return NewServer(deps), buf, m, reader, b
}

// httpRequestEvents decodes every JSON record in buf whose msg is
// "http.request", in order.
func httpRequestEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
		}
		if rec["msg"] == obs.EventHTTPRequest {
			out = append(out, rec)
		}
	}
	return out
}

// TestHTTPRequestWideEventCarriesRequiredFields checks a single feed GET
// emits exactly one http.request record carrying route, status, cache and
// duration_ms with the canonical field names from internal/obs/fields.go —
// no bare string keys, no missing field.
func TestHTTPRequestWideEventCarriesRequiredFields(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	events := httpRequestEvents(t, buf)
	if len(events) != 1 {
		t.Fatalf("http.request emitted %d times, want exactly 1: %+v", len(events), events)
	}
	got := events[0]

	if got[obs.FieldRoute] != "/feeds/{slug}.xml" {
		t.Errorf("route = %v, want %q", got[obs.FieldRoute], "/feeds/{slug}.xml")
	}
	if got[obs.FieldStatus] != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d", got[obs.FieldStatus], http.StatusOK)
	}
	if got[obs.FieldCache] != "miss" {
		t.Errorf("cache = %v, want %q", got[obs.FieldCache], "miss")
	}
	if _, ok := got[obs.FieldDurationMs]; !ok {
		t.Errorf("duration_ms missing from http.request: %+v", got)
	}
	if dur, ok := got[obs.FieldDurationMs].(float64); !ok || dur < 0 {
		t.Errorf("duration_ms = %v, want a non-negative number", got[obs.FieldDurationMs])
	}
}

// TestHTTPRequestRoutePatternNeverRawPath is the cardinality-guard
// regression: a permalink request must produce the fixed route pattern
// "/items/{item_key}", never the item_key itself. An item_key is unbounded
// by construction (one per published item), so a route label that embedded
// it would defeat the whole point of labeling by pattern.
func TestHTTPRequestRoutePatternNeverRawPath(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	const itemKey = "01J000000000000000000ALIVE"
	rec := do(h, http.MethodGet, "/items/"+itemKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	events := httpRequestEvents(t, buf)
	if len(events) != 1 {
		t.Fatalf("http.request emitted %d times, want exactly 1", len(events))
	}
	route, _ := events[0][obs.FieldRoute].(string)
	if route != "/items/{item_key}" {
		t.Errorf("route = %q, want the bounded pattern %q", route, "/items/{item_key}")
	}
	if bytes.Contains([]byte(route), []byte(itemKey)) {
		t.Fatalf("route embeds the item_key, an unbounded label: %q", route)
	}
	// Also confirm across the whole log line, not just the route field: no
	// call site should have smuggled the item_key into the record any other
	// way (e.g. as part of a different field this test doesn't check).
	if bytes.Contains(buf.Bytes(), []byte(itemKey)) {
		t.Fatalf("item_key %q leaked somewhere into the http.request log line:\n%s", itemKey, buf.String())
	}
}

// TestHTTPRequestCacheThreeStates drives the same feed URL through miss,
// hit, and 304 and checks the wide event's cache field distinguishes all
// three, per §15.0a: collapsing 304 into hit loses the number this design
// exists to measure.
func TestHTTPRequestCacheThreeStates(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	miss := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if miss.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", miss.Code)
	}
	etag := miss.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}

	hit := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if hit.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", hit.Code)
	}

	notModified := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"If-None-Match": etag,
	})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("third request status = %d, want 304", notModified.Code)
	}

	events := httpRequestEvents(t, buf)
	if len(events) != 3 {
		t.Fatalf("http.request emitted %d times, want 3", len(events))
	}
	want := []string{"miss", "hit", "304"}
	for i, w := range want {
		if got := events[i][obs.FieldCache]; got != w {
			t.Errorf("request %d: cache = %v, want %q", i, got, w)
		}
	}
}

// TestHTTPRequestStatusMatchesWritten checks the recorded status is the one
// actually written, across every non-2xx path the handler has, not just the
// success path — a wrapper that always reported 200 because that was a
// route's usual outcome would be worse than no metric at all.
func TestHTTPRequestStatusMatchesWritten(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"feed 200", http.MethodGet, "/feeds/trivia-daily.xml", http.StatusOK},
		{"unknown feed 404", http.MethodGet, "/feeds/no-such-feed.xml", http.StatusNotFound},
		{"item 410 gone", http.MethodGet, "/items/01J000000000000000000DEAD", http.StatusGone},
		{"unknown route 404", http.MethodGet, "/no/such/route", http.StatusNotFound},
		{"method not allowed 405", http.MethodPost, "/feeds/trivia-daily.xml", http.StatusMethodNotAllowed},
	}

	for _, c := range cases {
		buf.Reset()
		rec := do(h, c.method, c.path, nil)
		if rec.Code != c.wantStatus {
			t.Fatalf("%s: response status = %d, want %d", c.name, rec.Code, c.wantStatus)
		}
		events := httpRequestEvents(t, buf)
		if len(events) != 1 {
			t.Fatalf("%s: http.request emitted %d times, want exactly 1", c.name, len(events))
		}
		got, ok := events[0][obs.FieldStatus].(float64)
		if !ok || int(got) != c.wantStatus {
			t.Errorf("%s: recorded status = %v, want %d", c.name, events[0][obs.FieldStatus], c.wantStatus)
		}
	}
}

// TestHTTPRequestRecordedExactlyOnce drives several requests, including ones
// that exit through an early-return branch (404, 405), and checks the wide
// event count matches the request count exactly — no handler path may
// record twice, and none may fail to record.
func TestHTTPRequestRecordedExactlyOnce(t *testing.T) {
	h, buf, _, _, _ := newObservedTestServer(t)

	do(h, http.MethodGet, "/", nil)
	do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	do(h, http.MethodGet, "/feeds/does-not-exist.xml", nil)
	do(h, http.MethodPost, "/feeds/trivia-daily.xml", nil)
	do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
	do(h, http.MethodGet, "/healthz", nil)
	do(h, http.MethodGet, "/robots.txt", nil)
	do(h, http.MethodGet, "/favicon.ico", nil)

	events := httpRequestEvents(t, buf)
	if len(events) != 8 {
		t.Fatalf("http.request emitted %d times for 8 requests, want exactly 8: %+v", len(events), events)
	}
}

// TestHTTPRequestMetricsRecordRouteStatusAndCacheResult checks
// Metrics.RecordHTTPRequest and Metrics.RecordCacheResult are actually
// reached (not just the log-side wide event), and that the cardinality
// guard on the route label passes for the bounded route patterns this
// package emits.
func TestHTTPRequestMetricsRecordRouteStatusAndCacheResult(t *testing.T) {
	h, _, _, reader, _ := newObservedTestServer(t)

	do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil) // miss
	do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil) // hit

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var httpTotal, cacheTotal *metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			switch sm.Metrics[i].Name {
			case obs.MetricHTTPRequestsTotal:
				httpTotal = &sm.Metrics[i]
			case obs.MetricCacheHitsTotal:
				cacheTotal = &sm.Metrics[i]
			}
		}
	}
	if httpTotal == nil {
		t.Fatal("aff_http_requests_total was never recorded")
	}
	if cacheTotal == nil {
		t.Fatal("aff_cache_hits_total was never recorded")
	}

	sum, ok := httpTotal.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) == 0 {
		t.Fatalf("aff_http_requests_total has no data points: %#v", httpTotal.Data)
	}
	var sawRoute, sawStatus bool
	for _, dp := range sum.DataPoints {
		for _, attr := range dp.Attributes.ToSlice() {
			if string(attr.Key) == "route" && attr.Value.AsString() == "/feeds/{slug}.xml" {
				sawRoute = true
			}
			if string(attr.Key) == "status" && attr.Value.AsString() == strconv.Itoa(http.StatusOK) {
				sawStatus = true
			}
		}
	}
	if !sawRoute {
		t.Error("aff_http_requests_total never carried route=/feeds/{slug}.xml")
	}
	if !sawStatus {
		t.Error("aff_http_requests_total never carried status=200")
	}

	cacheSum, ok := cacheTotal.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("aff_cache_hits_total has unexpected data shape: %#v", cacheTotal.Data)
	}
	results := map[string]bool{}
	for _, dp := range cacheSum.DataPoints {
		for _, attr := range dp.Attributes.ToSlice() {
			if string(attr.Key) == "result" {
				results[attr.Value.AsString()] = true
			}
		}
	}
	if !results["hit"] || !results["miss"] {
		t.Errorf("aff_cache_hits_total did not record both hit and miss: %+v", results)
	}
}

// --- request_id correlation (PLAN.md §15.0) --------------------------------

// newRequestIDTestServer wires GetFeed to also emit a log line from inside
// the request path (via the same r.Context() the handler passed down), so
// tests below can check request_id reaches arbitrary application logging
// through context — not merely the one obs.HTTPRequest call site this
// package controls directly.
func newRequestIDTestServer(t *testing.T) (h http.Handler, buf *bytes.Buffer) {
	t.Helper()
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}

	buf = &bytes.Buffer{}
	log := obs.NewLogger(obs.Options{Level: slog.LevelDebug, Writer: buf})

	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.Logger = log
	inner := deps.GetFeed
	deps.GetFeed = func(ctx context.Context, slug string) (model.Feed, bool, error) {
		log.InfoContext(ctx, "backend.get_feed")
		return inner(ctx, slug)
	}
	return NewServer(deps), buf
}

// allRequestIDs returns the request_id field of every JSON log line in buf,
// in order, failing the test outright if any line lacks the field.
func allRequestIDs(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var ids []string
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
		}
		id, ok := rec[obs.FieldRequestID].(string)
		if !ok || id == "" {
			t.Fatalf("log line missing request_id: %+v", rec)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestRequestIDSharedAcrossEveryLogLineInOneRequest is the core correlation
// claim: every log line one request produces — not just the http.request
// wide event, but a line logged from deep inside the request path — carries
// the same request_id, and that same id is echoed on the response so an
// operator can be handed it directly.
func TestRequestIDSharedAcrossEveryLogLineInOneRequest(t *testing.T) {
	h, buf := newRequestIDTestServer(t)

	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ids := allRequestIDs(t, buf)
	if len(ids) < 2 {
		t.Fatalf("got %d log lines, want at least 2 (backend read + http.request): %v", len(ids), ids)
	}
	first := ids[0]
	for i, id := range ids {
		if id != first {
			t.Errorf("log line %d request_id = %q, want %q (every line from one request must share it)", i, id, first)
		}
	}

	if got := rec.Header().Get("X-Request-Id"); got != first {
		t.Errorf("X-Request-Id response header = %q, want %q (the id every log line above carried)", got, first)
	}
}

// TestRequestIDDiffersAcrossConcurrentRequests checks the id is per-request,
// not per-process or per-route: N concurrent requests to the same route
// must produce N distinct ids, or two unrelated requests' log lines would be
// indistinguishable during an incident.
func TestRequestIDDiffersAcrossConcurrentRequests(t *testing.T) {
	h, buf := newRequestIDTestServer(t)

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
		}()
	}
	wg.Wait()

	events := httpRequestEvents(t, buf)
	if len(events) != n {
		t.Fatalf("got %d http.request events, want %d", len(events), n)
	}
	seen := map[string]bool{}
	for _, e := range events {
		id, _ := e[obs.FieldRequestID].(string)
		if id == "" {
			t.Fatal("http.request event missing request_id")
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Errorf("%d concurrent requests produced only %d distinct request_ids, want %d (a shared id would make two unrelated requests' logs indistinguishable)", n, len(seen), n)
	}
}

// TestRequestIDOnHTTPRequestWideEvent checks the specific field the task
// calls out by name: the canonical http.request record itself carries
// request_id, not just narrower lines.
func TestRequestIDOnHTTPRequestWideEvent(t *testing.T) {
	h, buf := newRequestIDTestServer(t)

	do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)

	events := httpRequestEvents(t, buf)
	if len(events) != 1 {
		t.Fatalf("got %d http.request events, want 1", len(events))
	}
	id, ok := events[0][obs.FieldRequestID].(string)
	if !ok || id == "" {
		t.Errorf("http.request event missing request_id: %+v", events[0])
	}
}

// TestInboundRequestIDHeaderIsIgnored is the log-injection / cardinality
// regression requestIDMiddleware's design note exists for: a client can
// send any X-Request-Id it likes — arbitrary length, embedded newlines and
// quotes — and none of it may reach a log line or become the id this plane
// hands back. The server always mints its own, regardless of what arrived.
func TestInboundRequestIDHeaderIsIgnored(t *testing.T) {
	h, buf := newRequestIDTestServer(t)

	attacker := "attacker\"controlled\nvalue" + strings.Repeat("x", 10000)
	rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{
		"X-Request-Id": attacker,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if bytes.Contains(buf.Bytes(), []byte("attacker")) {
		t.Fatalf("attacker-supplied X-Request-Id leaked into a log line:\n%s", buf.String())
	}
	if got := rec.Header().Get("X-Request-Id"); got == attacker || len(got) > 100 {
		t.Errorf("response X-Request-Id = %q, want a short server-generated id, never the attacker's value", got)
	}
	for _, id := range allRequestIDs(t, buf) {
		if len(id) > 100 {
			t.Errorf("request_id %q is unbounded in length", id)
		}
	}
}
