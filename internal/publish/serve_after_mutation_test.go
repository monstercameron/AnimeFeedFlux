package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// This is the chain no per-package test was watching: mutate, invalidate the
// way production does, serve again, and check the BYTES actually changed.
//
// internal/e2e's journey tests assert on database state and never serve HTTP
// after a mutation; cache_invalidate_test.go proves Invalidate drops the right
// keys but not that the handler then serves the new truth. The gap between
// those two is exactly where a permalink kept serving a deleted item: both
// halves looked correct in isolation.
//
// Neutering Cache.Invalidate's paired-key deletion makes this fail with
// "got 200, want 410" and a stale title, which is the defect as a subscriber
// would have experienced it.
func TestServeAfterMutationServesTheNewTruth(t *testing.T) {
	feed := model.Feed{ID: 1, Slug: "trivia-daily", Title: "Trivia", Kind: model.KindGenerative, Language: "en", TTLMinutes: 60}
	item := model.Item{
		ID: 1, FeedID: 1, ItemKey: "01JPROBE0000000000000001",
		Title: "The original title", SummaryText: "summary", BodyHTML: "<p>body</p>",
		PublishedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC), Origin: model.OriginGenerated,
	}

	deps := Deps{
		GetFeed:   func(context.Context, string) (model.Feed, bool, error) { return feed, true, nil },
		ListItems: func(context.Context, int64) ([]model.Item, error) { return []model.Item{item}, nil },
		ListFeeds: func(context.Context) ([]model.Feed, error) { return []model.Feed{feed}, nil },
		GetItem: func(context.Context, string) (model.Feed, model.Item, bool, error) {
			return feed, item, true, nil
		},
		BaseURL: "https://feeds.example.com", Generator: "probe",
		DocsURL: "https://www.rssboard.org/rss-specification", TagYear: 2026,
		Now: func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) },
	}
	handler, inv := NewServerAndInvalidator(deps)

	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}

	// Populate the cache under BOTH permalink keys, the way a real request does.
	if code, body := get("/items/01JPROBE0000000000000001"); code != 200 || !strings.Contains(body, "The original title") {
		t.Fatalf("permalink before mutation: %d / %.80s", code, body)
	}
	if code, body := get("/feeds/trivia-daily.xml"); code != 200 || !strings.Contains(body, "The original title") {
		t.Fatalf("feed before mutation: %d / %.80s", code, body)
	}

	// Soft-delete, then invalidate exactly as ItemServer.invalidate does.
	item.DeletedAt = time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)
	inv.InvalidateFeed("trivia-daily")

	if code, _ := get("/items/01JPROBE0000000000000001"); code != http.StatusGone {
		t.Errorf("PERMALINK STILL SERVING A DELETED ITEM: got %d, want 410 — the 410 IS the retraction mechanism (PLAN.md 6, 12.4)", code)
	}

	// And an edit must reach the permalink, not just the feed.
	item.DeletedAt = time.Time{}
	item.Title = "The corrected title"
	inv.InvalidateFeed("trivia-daily")

	if code, body := get("/items/01JPROBE0000000000000001"); code != 200 || !strings.Contains(body, "The corrected title") {
		t.Errorf("PERMALINK SERVING STALE CONTENT AFTER AN EDIT: %d / %.120s", code, body)
	}
	if code, body := get("/feeds/trivia-daily.xml"); code != 200 || !strings.Contains(body, "The corrected title") {
		t.Errorf("feed serving stale content after an edit: %d / %.120s", code, body)
	}
}
