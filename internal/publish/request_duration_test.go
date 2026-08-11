package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// duration_ms is the field §15.0's wide event exists to make chartable, and
// it was being computed by subtracting an INJECTED clock's fixed date from
// the real present — so every request this package logged under a test or the
// e2e harness carried a latency that was not a latency. It showed up as
// duration_ms=-5051976, which is at least obviously wrong; a clock injected a
// few minutes off would have produced plausible nonsense instead.
//
// Deps.Now is deliberately injected here, set to a date hours away from the
// real present, because that is the exact condition that produced it.
func TestRequestDurationIsMeasuredOnTheMonotonicClock(t *testing.T) {
	feed := model.Feed{ID: 1, Slug: "trivia-daily", Title: "Trivia", Kind: model.KindGenerative, Language: "en", TTLMinutes: 60}
	item := model.Item{
		ID: 1, FeedID: 1, ItemKey: "01JPROBE0000000000000001",
		Title: "Title", SummaryText: "summary", BodyHTML: "<p>body</p>",
		PublishedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC), Origin: model.OriginGenerated,
	}

	var buf bytes.Buffer
	deps := Deps{
		GetFeed:   func(context.Context, string) (model.Feed, bool, error) { return feed, true, nil },
		ListItems: func(context.Context, int64) ([]model.Item, error) { return []model.Item{item}, nil },
		ListFeeds: func(context.Context) ([]model.Feed, error) { return []model.Feed{feed}, nil },
		GetItem: func(context.Context, string) (model.Feed, model.Item, bool, error) {
			return feed, item, true, nil
		},
		BaseURL: "https://feeds.example.com", Generator: "probe",
		DocsURL: "https://www.rssboard.org/rss-specification", TagYear: 2026,
		// Far from the real present, in both directions across the run.
		Now:    func() time.Time { return time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC) },
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	handler := NewServer(deps)

	for _, path := range []string{
		"/", "/feeds/trivia-daily.xml", "/items/01JPROBE0000000000000001",
		"/healthz", "/robots.txt", "/favicon.ico",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	var checked int
	for line := range bytes.Lines(buf.Bytes()) {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["msg"] != "http.request" {
			continue
		}
		raw, ok := rec["duration_ms"]
		if !ok {
			t.Fatalf("http.request record has no duration_ms: %v", rec)
		}
		ms, ok := raw.(float64)
		if !ok {
			t.Fatalf("duration_ms is %T (%v), want a number — a string duration cannot be charted (A0-L03)", raw, raw)
		}
		if ms < 0 {
			t.Errorf("route %v logged duration_ms=%v: a request cannot take negative time. "+
				"start must come from time.Now(), not the injected Deps.Now", rec["route"], ms)
		}
		// A handful of in-process handler calls cannot plausibly take a
		// minute; anything that large means a wall-clock date leaked into the
		// subtraction again, with the sign happening to land the other way.
		if ms > 60_000 {
			t.Errorf("route %v logged duration_ms=%v, which is not a latency", rec["route"], ms)
		}
		checked++
	}
	if checked == 0 {
		t.Fatalf("no http.request records were emitted at all; log was:\n%s", buf.String())
	}
}
