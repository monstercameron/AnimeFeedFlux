// Package testutil also holds the deterministic fixtures every renderer and
// store test builds on (PLAN.md §17.1): a seeded feed, items and channel
// with known, stable values, so that a schema change is one edit here rather
// than twenty edits across the test suite, and so golden files stay stable
// run to run.
package testutil

import (
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// crockford32 is the Crockford base32 alphabet, which excludes I, L, O and U
// to avoid confusion with 1, 1, 0 and V. Real ItemKeys come from ulid.ULID,
// which uses this same alphabet, so a fixture built by hand still LOOKS like
// a genuine ULID in golden files and error messages.
const crockford32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// FixedTime is the one canonical instant every fixture and test in this
// suite anchors to. Using time.Now anywhere in a fixture would make golden
// files and "strictly increasing" assertions flake by the calendar.
func FixedTime() time.Time {
	return time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
}

// SampleFeed returns a populated generative feed with stable identity and
// unfurl fields (§14.1: distinct author/image per feed so subscribers can
// tell feeds apart).
func SampleFeed() model.Feed {
	return model.Feed{
		ID:          1,
		Slug:        "trivia-daily",
		Title:       "Trivia Daily",
		Description: "One anime trivia question a day, generated fresh.",
		Language:    "en-us",
		Kind:        model.KindGenerative,

		Author:     "AnimeFeedFlux Bot",
		Copyright:  "Copyright 2026 AnimeFeedFlux",
		OGImage:    "https://feeds.example.com/trivia-daily/og.png",
		TTLMinutes: 60,

		Enabled:     true,
		Timezone:    "UTC",
		LastBuiltAt: FixedTime(),

		Version: 1,
	}
}

// itemKey returns a deterministic 26-character Crockford base32 string that
// looks and sorts like a real ULID for the given index, without pulling in
// randomness or wall-clock time. It's index encoded directly as a base-32
// number (left-padded with the alphabet's zero digit) rather than mixed
// through an ad-hoc formula, so distinct indices are guaranteed distinct
// keys rather than merely likely to be.
func itemKey(index int) string {
	const length = 26
	b := make([]byte, length)
	for i := range b {
		b[i] = crockford32[0]
	}
	n := index
	base := len(crockford32)
	for i := length - 1; i >= 0 && n > 0; i-- {
		b[i] = crockford32[n%base]
		n /= base
	}
	return string(b)
}

// SampleItems returns n items with deterministic ItemKeys, strictly
// increasing PublishedAt timestamps one second apart starting at FixedTime,
// and deterministic titles/summaries/bodies. Renderer and store tests rely
// on this exact shape to assert ordering and idempotency (§5.1, §5.5).
func SampleItems(n int) []model.Item {
	items := make([]model.Item, n)
	base := FixedTime()
	for i := 0; i < n; i++ {
		items[i] = model.Item{
			ID:     int64(i + 1),
			FeedID: 1,

			ItemKey:     itemKey(i),
			ContentHash: fmt.Sprintf("hash-%02d", i),

			Title:       fmt.Sprintf("Sample Item %02d", i),
			SummaryText: fmt.Sprintf("Summary text for sample item %02d.", i),
			BodyHTML:    fmt.Sprintf("<p>Body HTML for sample item %02d.</p>", i),

			Link:       fmt.Sprintf("https://feeds.example.com/trivia-daily/items/%02d", i),
			SourceName: "AnimeFeedFlux",

			PublishedAt: base.Add(time.Duration(i) * time.Second),

			Tags:   []string{"anime", "trivia"},
			Origin: model.OriginGenerated,

			CreatedAt: base,
			UpdatedAt: base,
			EditedAt:  time.Time{},
			DeletedAt: time.Time{},

			Version: 1,
		}
	}
	return items
}

// TriviaItem returns an item with a question in Title/SummaryText and a
// distinct answer in AnswerHTML, carrying an unmissable token so a test can
// assert the answer text never leaks into a summary or og:description
// (§5.5: Slack renders a snippet from SummaryText, and a leaked answer there
// spoils the question before the reader clicks through).
func TriviaItem() model.Item {
	base := FixedTime()
	return model.Item{
		ID:     100,
		FeedID: 1,

		ItemKey:     itemKey(99),
		ContentHash: "hash-trivia",

		Title:       "Which studio animated Cowboy Bebop?",
		SummaryText: "Which studio animated Cowboy Bebop? Tap through to reveal the answer.",
		BodyHTML:    "<p>Which studio animated Cowboy Bebop? Tap through to reveal the answer.</p>",
		AnswerHTML:  "<p>ANSWER-COWBOY-BEBOP</p>",

		Link:       "https://feeds.example.com/trivia-daily/items/trivia",
		SourceName: "AnimeFeedFlux",

		PublishedAt: base.Add(99 * time.Second),

		Tags:   []string{"anime", "trivia"},
		Origin: model.OriginGenerated,

		CreatedAt: base,
		UpdatedAt: base,
		EditedAt:  time.Time{},
		DeletedAt: time.Time{},

		Version: 1,
	}
}

// SampleChannel returns a fully populated Channel wrapping SampleFeed and
// SampleItems(n), with every URL and build field the renderers need set to
// stable values, so a renderer test never has to know how to construct a
// Channel by hand.
func SampleChannel(n int) model.Channel {
	return model.Channel{
		Feed: SampleFeed(),

		SelfURL: "https://feeds.example.com/trivia-daily.xml",
		HTMLURL: "https://feeds.example.com/trivia-daily",
		Host:    "feeds.example.com",
		TagYear: 2026,

		Items: SampleItems(n),

		BuildTime: FixedTime(),

		Generator: "AnimeFeedFlux v0.0.2-dev",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}
