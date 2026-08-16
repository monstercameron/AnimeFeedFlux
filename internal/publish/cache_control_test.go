package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// settings.publishing.default_cache_control persisted, round-tripped through
// the UI, showed "Saved." — and changed nothing: writeEntry hardcoded
// max-age=900 (RS5-01, "at least eleven settings are read by nothing").
func cacheControlDeps(fn func() string) Deps {
	feed := model.Feed{ID: 1, Slug: "trivia-daily", Title: "Trivia", Kind: model.KindGenerative, Language: "en", TTLMinutes: 60}
	item := model.Item{
		ID: 1, FeedID: 1, ItemKey: "01JCACHE0000000000000001",
		Title: "Title", SummaryText: "s", BodyHTML: "<p>b</p>",
		PublishedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC), Origin: model.OriginGenerated,
	}
	return Deps{
		GetFeed:   func(context.Context, string) (model.Feed, bool, error) { return feed, true, nil },
		ListItems: func(context.Context, int64) ([]model.Item, error) { return []model.Item{item}, nil },
		ListFeeds: func(context.Context) ([]model.Feed, error) { return []model.Feed{feed}, nil },
		GetItem: func(context.Context, string) (model.Feed, model.Item, bool, error) {
			return feed, item, true, nil
		},
		BaseURL: "https://feeds.example.com", Generator: "probe",
		DocsURL: "https://www.rssboard.org/rss-specification", TagYear: 2026,
		Now:            func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) },
		CacheControlFn: fn,
	}
}

func cacheControlOf(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	return rec.Header().Get("Cache-Control")
}

func TestCacheControlComesFromTheSetting(t *testing.T) {
	h := NewServer(cacheControlDeps(func() string { return "public, max-age=60" }))
	for _, path := range []string{"/", "/feeds/trivia-daily.xml", "/items/01JCACHE0000000000000001"} {
		if got := cacheControlOf(t, h, path); got != "public, max-age=60" {
			t.Errorf("%s Cache-Control = %q, want the configured value", path, got)
		}
	}
}

// A live read, not one captured at construction: an operator changing the
// setting must not have to restart, and the value must not be frozen into
// already-cached bodies.
func TestCacheControlIsReadPerRequest(t *testing.T) {
	current := "max-age=30"
	h := NewServer(cacheControlDeps(func() string { return current }))

	if got := cacheControlOf(t, h, "/feeds/trivia-daily.xml"); got != "max-age=30" {
		t.Fatalf("first request = %q", got)
	}
	current = "max-age=3600"
	// Second request is served from the render cache — the header must still
	// follow the setting, which is why it is not baked into Entry.
	if got := cacheControlOf(t, h, "/feeds/trivia-daily.xml"); got != "max-age=3600" {
		t.Errorf("after the setting changed, a CACHED response still served %q", got)
	}
}

// Unset must behave exactly as before the setting was wired.
func TestCacheControlFallsBackWhenUnset(t *testing.T) {
	for name, fn := range map[string]func() string{
		"nil":             nil,
		"empty":           func() string { return "" },
		"only whitespace": func() string { return "   " },
	} {
		h := NewServer(cacheControlDeps(fn))
		if got := cacheControlOf(t, h, "/feeds/trivia-daily.xml"); got != defaultCacheControl {
			t.Errorf("%s: Cache-Control = %q, want %q", name, got, defaultCacheControl)
		}
	}
}
