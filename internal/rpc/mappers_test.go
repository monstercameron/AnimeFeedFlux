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

func TestFeedKindMappingRoundTripsAndRefusesToGuess(t *testing.T) {
	// A kind that maps to the wrong value silently changes what a feed IS —
	// aggregates never spend, generative ones do — so the unknown case must
	// produce an empty/unspecified value rather than defaulting to a real one.
	for _, k := range []model.FeedKind{model.KindGenerative, model.KindGrounded, model.KindAggregate} {
		if got := feedKindFromProto(feedKindToProto(k)); got != k {
			t.Errorf("%q round-tripped to %q", k, got)
		}
	}
	for _, p := range []affv1.FeedKind{
		affv1.FeedKind_FEED_KIND_GENERATIVE,
		affv1.FeedKind_FEED_KIND_GROUNDED,
		affv1.FeedKind_FEED_KIND_AGGREGATE,
	} {
		if got := feedKindToProto(feedKindFromProto(p)); got != p {
			t.Errorf("%v round-tripped to %v", p, got)
		}
	}
	if got := feedKindFromProto(affv1.FeedKind_FEED_KIND_UNSPECIFIED); got != "" {
		t.Errorf("an unspecified kind became %q, want empty", got)
	}
	if got := feedKindFromProto(affv1.FeedKind(99)); got != "" {
		t.Errorf("an unknown kind became %q, want empty", got)
	}
	if got := feedKindToProto(model.FeedKind("something-else")); got != affv1.FeedKind_FEED_KIND_UNSPECIFIED {
		t.Errorf("an unknown kind became %v, want UNSPECIFIED", got)
	}
}

func TestEveryValidationReasonHasAMessage(t *testing.T) {
	// The token is the machine-readable half and is already on the field
	// error; this is what the operator reads. A reason with no case falls
	// through to the default, and a form that says nothing useful next to a
	// red field is how someone concludes the app is broken.
	reasons := []string{
		feedspec.ReasonSlugShape,
		feedspec.ReasonSlugReserved,
		feedspec.ReasonCronInvalid,
		feedspec.ReasonTimezoneInvalid,
		feedspec.ReasonTemplateInvalid,
		feedspec.ReasonKindInvalid,
		feedspec.ReasonGroundedRequiresSource,
		feedspec.ReasonGenerativeForbidsSource,
		feedspec.ReasonAggregateRequiresMember,
		feedspec.ReasonAggregateForbidsSource,
		feedspec.ReasonAggregateForbidsPrompt,
		feedspec.ReasonAggregateSelfMember,
		feedspec.ReasonBudgetDailyTokensZero,
		feedspec.ReasonItemsPerRunRange,
	}
	seen := map[string]string{}
	for _, r := range reasons {
		msg := feedProblemMessage(r)
		if msg == "" {
			t.Errorf("reason %q has no message", r)
			continue
		}
		if msg == r {
			t.Errorf("reason %q rendered as its own token", r)
		}
		if prev, dup := seen[msg]; dup {
			t.Errorf("reasons %q and %q share the message %q", prev, r, msg)
		}
		seen[msg] = r
	}
	// An unrecognised token still says something rather than rendering blank.
	if got := feedProblemMessage("some_future_reason"); got == "" {
		t.Error("an unknown reason produced an empty message")
	}
}

func TestEditStampIsStrictlyIncreasingAndSortsAsText(t *testing.T) {
	// Two properties, both load-bearing for item history.
	//
	// (1) Strictly increasing: `at` is the GROUP key — item_revisions holds
	// one row per changed FIELD and everything that reads history treats
	// rows sharing an `at` as one edit — so two edits on the same stamp merge
	// into a single entry, and reverting one reverts both. time.Now() alone
	// does not prevent that: Windows' timer resolution is ~15ms, so an edit
	// and an immediate revert read the same clock. That is what made the
	// revert tests flake.
	//
	// (2) Sorts as text: the column is TEXT ordered with SQL string
	// operators, and RFC3339Nano drops trailing zeros, so "…00Z" and
	// "…00.000000001Z" compare in the WRONG order ('.' < 'Z'). Every stamp
	// must therefore print a full nine-digit fraction.
	srv := NewItemServer(nil, nil, nil)

	// A clock frozen at a whole second is the worst case for both properties
	// and exactly what a pinned test clock looks like.
	frozen := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	srv.now = func() time.Time { return frozen }

	var stamps []time.Time
	var texts []string
	for range 5 {
		at := srv.editStamp()
		stamps = append(stamps, at)
		texts = append(texts, itemFormatTime(at))
	}

	for i := 1; i < len(stamps); i++ {
		if !stamps[i].After(stamps[i-1]) {
			t.Errorf("stamp %d (%s) is not after stamp %d (%s)", i, stamps[i], i-1, stamps[i-1])
		}
		if texts[i] <= texts[i-1] {
			t.Errorf("text %q does not sort after %q — SQL would order these backwards", texts[i], texts[i-1])
		}
	}
	for i, s := range texts {
		if len(s) != len("2026-08-11T04:00:00.000000001Z") {
			t.Errorf("stamp %d formats as %q (%d chars), want a full nine-digit fraction", i, s, len(s))
		}
	}

	// A clock that moves normally is passed through, not held back.
	moving := frozen.Add(time.Hour)
	srv.now = func() time.Time { return moving }
	if got := srv.editStamp(); got.Before(moving) {
		t.Errorf("editStamp = %s, want at least the current clock %s", got, moving)
	}
	if got := srv.editStamp(); !got.After(moving) {
		t.Errorf("a second stamp on a frozen clock = %s, want strictly after %s", got, moving)
	}
}
