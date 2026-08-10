package publish

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
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

func TestFavicon(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/favicon.ico", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

// --- no leaked internals ------------------------------------------------

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
