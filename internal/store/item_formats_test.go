package store

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// TestItemFormatsRoundTripAndClearOnEdit covers the two properties
// items.formats_json promises: stage-2 variants survive insert->read
// unchanged, and an EDIT clears them (stale variants would show old content
// on every surface while the raw fields say otherwise).
func TestItemFormatsRoundTripAndClearOnEdit(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("fmt"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	item := makeItem(feedID, "key-fmt", time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	item.Formats = model.ItemFormats{
		FeedHTML:  "<p>reader-optimized</p>",
		CardText:  "card teaser",
		EmbedText: "widget line",
		PageHTML:  "<p>page body</p>",
	}
	if _, err := s.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.GetItem(ctx, "key-fmt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Formats != item.Formats {
		t.Fatalf("formats round trip mismatch:\n got %+v\nwant %+v", got.Formats, item.Formats)
	}

	// An item with no variants stores and reads back as the zero value —
	// the pre-stage-2 state, and the fallback state.
	plain := makeItem(feedID, "key-plain", time.Date(2026, 8, 15, 13, 0, 0, 0, time.UTC))
	if _, err := s.InsertItem(ctx, plain); err != nil {
		t.Fatalf("insert plain: %v", err)
	}
	gotPlain, err := s.GetItem(ctx, "key-plain")
	if err != nil {
		t.Fatalf("get plain: %v", err)
	}
	if !gotPlain.Formats.Empty() {
		t.Fatalf("formats for a plain item = %+v, want empty", gotPlain.Formats)
	}

	// Editing clears the variants.
	got.Title = "Edited title"
	if err := s.UpdateItem(ctx, got, got.Version); err != nil {
		t.Fatalf("update: %v", err)
	}
	edited, err := s.GetItem(ctx, "key-fmt")
	if err != nil {
		t.Fatalf("get after edit: %v", err)
	}
	if !edited.Formats.Empty() {
		t.Fatalf("formats survived an edit: %+v — they must clear so no surface serves stale content", edited.Formats)
	}
}
