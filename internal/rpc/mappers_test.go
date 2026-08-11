package rpc

import (
	"strings"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

func TestFeedSourcesRoundTrip(t *testing.T) {
	// Sources are what a grounded feed reads before generating. A mapping
	// that drops Kind turns "this RSS source" into "some source", and the
	// fetcher then treats it as the default kind — quietly, at run time.
	in := []feedspec.Source{
		{URL: "https://example.com/feed.xml", Kind: "rss"},
		{URL: "https://example.org/atom", Kind: "atom"},
	}
	round := feedSourcesFromProto(feedSourcesToProto(in))
	if len(round) != len(in) {
		t.Fatalf("round trip produced %d sources, want %d", len(round), len(in))
	}
	for i := range in {
		if round[i] != in[i] {
			t.Errorf("source %d round-tripped to %+v, want %+v", i, round[i], in[i])
		}
	}

	// Empty and nil both mean "no sources", and must not become a one-element
	// slice of zero values — a source with an empty URL would be fetched.
	if got := feedSourcesToProto(nil); got != nil {
		t.Errorf("feedSourcesToProto(nil) = %+v, want nil", got)
	}
	if got := feedSourcesFromProto(nil); got != nil {
		t.Errorf("feedSourcesFromProto(nil) = %+v, want nil", got)
	}
	if got := feedSourcesToProto([]feedspec.Source{}); got != nil {
		t.Errorf("feedSourcesToProto(empty) = %+v, want nil", got)
	}
	if got := feedSourcesFromProto([]*affv1.SourceSpec{}); got != nil {
		t.Errorf("feedSourcesFromProto(empty) = %+v, want nil", got)
	}
}

func TestApplyRevisionFieldCoversEveryDiffableField(t *testing.T) {
	// Revert reconstructs an item by replaying these field names. A field
	// that itemDiff records but this switch does not handle would make revert
	// fail outright — which is the safe direction, and is what the default
	// case is for — so the test that matters is that every recorded field IS
	// handled, and that nothing outside the list can be written.
	when := time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC)
	cases := []struct {
		field string
		value string
		check func(model.Item) bool
	}{
		{"title", "Old Title", func(i model.Item) bool { return i.Title == "Old Title" }},
		{"summary_text", "old summary", func(i model.Item) bool { return i.SummaryText == "old summary" }},
		{"body_html", "<p>old</p>", func(i model.Item) bool { return i.BodyHTML == "<p>old</p>" }},
		{"answer_html", "<p>42</p>", func(i model.Item) bool { return i.AnswerHTML == "<p>42</p>" }},
		{"link", "https://example.com/old", func(i model.Item) bool { return i.Link == "https://example.com/old" }},
		{"source_name", "Old Source", func(i model.Item) bool { return i.SourceName == "Old Source" }},
		{"published_at", itemFormatTime(when), func(i model.Item) bool { return i.PublishedAt.Equal(when) }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			item := model.Item{ItemKey: "key-1", FeedID: 7}
			if err := itemApplyRevisionField(&item, tc.field, tc.value); err != nil {
				t.Fatalf("itemApplyRevisionField(%q): %v", tc.field, err)
			}
			if !tc.check(item) {
				t.Errorf("field %q was not applied: %+v", tc.field, item)
			}
			// The identity fields are never restorable: an old guid must not
			// be resurrectable through a revert (§5.1).
			if item.ItemKey != "key-1" || item.FeedID != 7 {
				t.Errorf("applying %q moved the item's identity: %+v", tc.field, item)
			}
		})
	}
}

func TestApplyRevisionFieldRefusesIdentityAndUnknownFields(t *testing.T) {
	// An unknown field errors rather than silently doing nothing, so a
	// mismatch between what itemDiff writes and what revert can replay
	// surfaces as a failed revert instead of a partially-reverted item.
	for _, field := range []string{"item_key", "feed_id", "id", "", "Title"} {
		item := model.Item{ItemKey: "key-1", FeedID: 7}
		err := itemApplyRevisionField(&item, field, "anything")
		if err == nil {
			t.Errorf("field %q was accepted; item is now %+v", field, item)
			continue
		}
		if !strings.Contains(err.Error(), "unrecognized revision field") {
			t.Errorf("field %q produced %q, want an unrecognized-field error", field, err)
		}
	}
}

func TestApplyRevisionFieldRejectsAnUnparseableTimestamp(t *testing.T) {
	item := model.Item{}
	err := itemApplyRevisionField(&item, "published_at", "yesterday-ish")
	if err == nil {
		t.Fatal("an unparseable published_at was accepted")
	}
	if !item.PublishedAt.IsZero() {
		t.Errorf("a rejected timestamp was still applied: %s", item.PublishedAt)
	}
}
