package rpc

import (
	"encoding/json"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// settings.publishing's identity defaults persisted, round-tripped through the
// UI and reported "Saved." while seeding nothing — every new feed started with
// blank author/copyright/og_image however carefully they were filled in
// (A5-01, "at least eleven settings are read by nothing").
func seedPublishingDefaults(t *testing.T, s *FeedServer) {
	t.Helper()
	p := &affv1.Settings_Publishing{
		DefaultAuthor:     "Earl Cameron",
		DefaultCopyright:  "© 2026 Earl Cameron",
		DefaultOgImage:    "https://feeds.example.com/og.png",
		DefaultTtlMinutes: 45,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := s.store.Writer().ExecContext(t.Context(),
		`INSERT INTO settings (key, value) VALUES ('publishing', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(raw)); err != nil {
		t.Fatalf("seeding publishing settings: %v", err)
	}
}

func TestCreateSeedsIdentityFromPublishingDefaults(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)
	seedPublishingDefaults(t, s)

	created := mustCreateFeed(t, s, feedTestFeed("inherits-defaults"))
	if got, want := created.GetAuthor(), "Earl Cameron"; got != want {
		t.Errorf("author = %q, want the configured default %q", got, want)
	}
	if got, want := created.GetCopyright(), "© 2026 Earl Cameron"; got != want {
		t.Errorf("copyright = %q, want %q", got, want)
	}
	if got, want := created.GetOgImage(), "https://feeds.example.com/og.png"; got != want {
		t.Errorf("og_image = %q, want %q", got, want)
	}
	if got, want := created.GetTtlMinutes(), int32(45); got != want {
		t.Errorf("ttl_minutes = %d, want %d", got, want)
	}
}

// A default is a starting point, not an override: what the request states wins.
func TestCreateKeepsExplicitIdentityOverDefaults(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)
	seedPublishingDefaults(t, s)

	in := feedTestFeed("explicit-identity")
	in.Author = "Someone Else"
	in.TtlMinutes = 5
	created := mustCreateFeed(t, s, in)

	if got := created.GetAuthor(); got != "Someone Else" {
		t.Errorf("author = %q, want the request's own value", got)
	}
	if got := created.GetTtlMinutes(); got != 5 {
		t.Errorf("ttl_minutes = %d, want 5", got)
	}
	// Fields the request left empty still take the default.
	if got := created.GetCopyright(); got != "© 2026 Earl Cameron" {
		t.Errorf("copyright = %q, want the default for an unset field", got)
	}
}

// Update must NOT re-impose defaults: a cleared author means the operator
// cleared it, and re-filling it would make the field impossible to empty.
func TestUpdateDoesNotReimposeDefaults(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)
	seedPublishingDefaults(t, s)

	created := mustCreateFeed(t, s, feedTestFeed("clearable-author"))
	if created.GetAuthor() == "" {
		t.Fatal("precondition: the created feed should have inherited an author")
	}

	created.Author = ""
	resp, err := s.Update(t.Context(), &affv1.FeedServiceUpdateRequest{
		Feed: created, ExpectedVersion: created.GetVersion(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := resp.GetFeed().GetAuthor(); got != "" {
		t.Errorf("author = %q after being cleared — the default was re-imposed, "+
			"making the field impossible to empty", got)
	}
}

// With no settings row at all, creation behaves exactly as it did before the
// defaults were wired: a missing default must never block creating a feed.
func TestCreateWithoutPublishingSettings(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	created := mustCreateFeed(t, s, feedTestFeed("no-settings-row"))
	if created.GetAuthor() != "" {
		t.Errorf("author = %q, want empty with no settings configured", created.GetAuthor())
	}
}
