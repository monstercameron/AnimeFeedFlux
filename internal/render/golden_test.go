package render

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// This file locks the four renderers' exact byte output down with golden
// files (PLAN.md §5.6, §17.1): a deliberate format change is a reviewed diff
// via `go test -run Golden ./internal/render/ -update`, an accidental one is
// a test failure. Cases cover a plain channel, a channel carrying a trivia
// item (proving the answer never leaks into description/summary and lands
// only where §5.5 says it may), and an "ugly" channel exercising the
// specific adversarial inputs §5.6 names by name.

// triviaChannel returns a 2-item channel with testutil.TriviaItem appended,
// so every trivia golden exercises the answer-placement rules alongside
// ordinary items rather than in isolation.
func triviaChannel() model.Channel {
	c := testutil.SampleChannel(2)
	c.Items = append(c.Items, testutil.TriviaItem())
	return c
}

// uglyChannel is built from model types directly, per instruction, rather
// than through the testutil fixtures: it exists solely to carry the
// adversarial cases PLAN.md §5.6 calls out by name, so it should look
// nothing like a "normal" fixture. One item carries all of them at once:
// an ampersand and a '<' in the title, CJK text, an emoji, a "]]>" sequence
// inside the body HTML, a summary sitting exactly at the 500-char hard cap,
// and no link at all (the "item with no link" case).
func uglyChannel() model.Channel {
	fixed := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)

	summary500 := strings.Repeat("x", 497) + "end" // exactly 500 chars.
	if len(summary500) != 500 {
		panic("uglyChannel: summary fixture is not exactly 500 chars")
	}

	item := model.Item{
		ID:     200,
		FeedID: 1,

		ItemKey:     "UGLYUGLYUGLYUGLYUGLYUGLY01",
		ContentHash: "hash-ugly",

		Title:       `Ugly & <Weird> 日本語タイトル 🎌 Title`,
		SummaryText: summary500,
		BodyHTML:    `<p>Body containing a raw ]]> sequence, an & ampersand, and a <b>bold</b> tag with 日本語 and 🎌.</p>`,

		// Deliberately empty: the "item with no link" case (§5.6).
		Link:       "",
		SourceName: "AnimeFeedFlux",

		PublishedAt: fixed,

		Tags:   []string{"ugly", "edge-case"},
		Origin: model.OriginGenerated,

		CreatedAt: fixed,
		UpdatedAt: fixed,

		Version: 1,
	}

	return model.Channel{
		Feed: model.Feed{
			ID:          2,
			Slug:        "ugly-feed",
			Title:       "Ugly Feed & <Edge Cases>",
			Description: "Adversarial inputs for renderer goldens (§5.6).",
			Language:    "en-us",
			Kind:        model.KindGenerative,

			Author:     "AnimeFeedFlux Bot",
			Copyright:  "Copyright 2026 AnimeFeedFlux",
			OGImage:    "https://feeds.example.com/ugly-feed/og.png",
			TTLMinutes: 60,

			Enabled:     true,
			Timezone:    "UTC",
			LastBuiltAt: fixed,

			Version: 1,
		},

		SelfURL: "https://feeds.example.com/ugly-feed.xml",
		HTMLURL: "https://feeds.example.com/ugly-feed",
		Host:    "feeds.example.com",
		TagYear: 2026,

		Items: []model.Item{item},

		BuildTime: fixed,

		Generator: "AnimeFeedFlux v0.0.2-dev",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}

func TestGoldenRSS(t *testing.T) {
	cases := map[string]model.Channel{
		"rss_basic":  testutil.SampleChannel(3),
		"rss_trivia": triviaChannel(),
		"rss_ugly":   uglyChannel(),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := RSS(c)
			if err != nil {
				t.Fatalf("RSS(%s): %v", name, err)
			}
			testutil.Assert(t, name, got)
		})
	}
}

func TestGoldenAtom(t *testing.T) {
	cases := map[string]model.Channel{
		"atom_basic":  testutil.SampleChannel(3),
		"atom_trivia": triviaChannel(),
		"atom_ugly":   uglyChannel(),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Atom(c)
			if err != nil {
				t.Fatalf("Atom(%s): %v", name, err)
			}
			testutil.Assert(t, name, got)
		})
	}
}

func TestGoldenJSONFeed(t *testing.T) {
	cases := map[string]model.Channel{
		"jsonfeed_basic":  testutil.SampleChannel(3),
		"jsonfeed_trivia": triviaChannel(),
		"jsonfeed_ugly":   uglyChannel(),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := JSONFeed(c)
			if err != nil {
				t.Fatalf("JSONFeed(%s): %v", name, err)
			}
			testutil.Assert(t, name, got)
		})
	}
}

func TestGoldenPermalink(t *testing.T) {
	basic := testutil.SampleChannel(3)
	trivia := testutil.TriviaItem()

	cases := []struct {
		name string
		c    model.Channel
		it   model.Item
	}{
		// Plain 3-item channel, permalink for its first item.
		{"permalink_basic", basic, basic.Items[0]},
		// Trivia item's own permalink: the answer must appear behind
		// <details>, and never in the <meta>/og: tags built from
		// SummaryText (§5.5).
		{"permalink_trivia", triviaChannel(), trivia},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Permalink(tc.c, tc.it)
			if err != nil {
				t.Fatalf("Permalink(%s): %v", tc.name, err)
			}
			testutil.Assert(t, tc.name, got)
		})
	}
}
