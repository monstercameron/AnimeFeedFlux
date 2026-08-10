package ops

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckFlagsStaleFeedNotFreshOne(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	feeds := []FeedStatus{
		{
			FeedSlug:      "fresh-daily",
			Enabled:       true,
			Interval:      24 * time.Hour,
			LastSuccessAt: now.Add(-6 * time.Hour), // well within grace
		},
		{
			FeedSlug:      "stale-daily",
			Enabled:       true,
			Interval:      24 * time.Hour,
			LastSuccessAt: now.Add(-72 * time.Hour), // 3x interval, grace 2.0
		},
		{
			FeedSlug: "never-run",
			Enabled:  true,
			Interval: 24 * time.Hour,
			// LastSuccessAt zero
		},
		{
			FeedSlug:      "stale-but-disabled",
			Enabled:       false,
			Interval:      24 * time.Hour,
			LastSuccessAt: now.Add(-72 * time.Hour),
		},
	}

	got := Check(feeds, now, 2.0)

	slugs := map[string]bool{}
	for _, s := range got {
		slugs[s.FeedSlug] = true
	}

	if !slugs["stale-daily"] {
		t.Error("expected stale-daily to be flagged stale")
	}
	if !slugs["never-run"] {
		t.Error("expected never-run to be flagged stale")
	}
	if slugs["fresh-daily"] {
		t.Error("fresh-daily must not be flagged stale")
	}
	if slugs["stale-but-disabled"] {
		t.Error("a disabled feed must never be flagged stale")
	}
	if len(got) != 2 {
		t.Errorf("Check returned %d stale feeds, want 2: %+v", len(got), got)
	}
}

func TestCheckIgnoresFeedsWithNoInterval(t *testing.T) {
	now := time.Now()
	feeds := []FeedStatus{
		{FeedSlug: "no-schedule", Enabled: true, Interval: 0},
	}
	if got := Check(feeds, now, 2.0); len(got) != 0 {
		t.Errorf("Check on a feed with no interval: want 0 results, got %d", len(got))
	}
}

func TestNotifyPostsToWebhook(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stale := []Stale{{FeedSlug: "anime-trivia-daily", Age: 50 * time.Hour, Threshold: 48 * time.Hour}}
	if err := Notify(t.Context(), srv.URL, stale); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(gotBody) == 0 {
		t.Fatal("webhook received an empty body")
	}
}

func TestNotifyNoOpOnEmptyStaleList(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	if err := Notify(t.Context(), srv.URL, nil); err != nil {
		t.Fatalf("Notify with no stale feeds: %v", err)
	}
	if called {
		t.Error("Notify must not call the webhook when there is nothing stale to report")
	}
}

func TestNotifyFailureDoesNotPanicAndReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Notify panicked: %v", r)
		}
	}()

	stale := []Stale{{FeedSlug: "anime-fact-daily"}}

	// An unreachable address: connection failure.
	if err := Notify(t.Context(), "http://127.0.0.1:1/webhook", stale); err == nil {
		t.Error("Notify to an unreachable address: want error, got nil")
	}

	// A malformed URL.
	if err := Notify(t.Context(), "://not-a-url", stale); err == nil {
		t.Error("Notify with a malformed URL: want error, got nil")
	}

	// A server that returns a non-2xx status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := Notify(t.Context(), srv.URL, stale); err == nil {
		t.Error("Notify against a 500 response: want error, got nil")
	}
}
