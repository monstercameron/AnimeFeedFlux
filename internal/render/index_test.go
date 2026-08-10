package render

import (
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// TestIndex_TwoFeeds pins the exact byte output for two deterministic,
// enabled feeds — this is the page that gets pasted into Slack, so its shape
// is a contract, not an implementation detail.
func TestIndex_TwoFeeds(t *testing.T) {
	feeds := []model.Feed{
		{
			Slug:        "alpha-feed",
			Title:       "Alpha Feed",
			Description: "First feed description.",
			Enabled:     true,
		},
		{
			Slug:        "beta-feed",
			Title:       "Beta Feed",
			Description: "Second feed description.",
			Enabled:     true,
		},
	}

	got, err := Index(feeds, "https://feeds.example.test", "AnimeFeedFlux vTest")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}

	want := strings.Join([]string{
		`<!doctype html>`,
		`<html lang="en">`,
		`<head>`,
		`  <meta charset="utf-8">`,
		`  <meta name="viewport" content="width=device-width, initial-scale=1">`,
		`  <title>Feeds</title>`,
		`  <meta name="generator" content="AnimeFeedFlux vTest">`,
		`  <link rel="alternate" type="application/rss+xml" title="Alpha Feed" href="https://feeds.example.test/feeds/alpha-feed.xml">`,
		`  <link rel="alternate" type="application/atom+xml" title="Alpha Feed" href="https://feeds.example.test/feeds/alpha-feed.atom">`,
		`  <link rel="alternate" type="application/feed+json" title="Alpha Feed" href="https://feeds.example.test/feeds/alpha-feed.json">`,
		`  <link rel="alternate" type="application/rss+xml" title="Beta Feed" href="https://feeds.example.test/feeds/beta-feed.xml">`,
		`  <link rel="alternate" type="application/atom+xml" title="Beta Feed" href="https://feeds.example.test/feeds/beta-feed.atom">`,
		`  <link rel="alternate" type="application/feed+json" title="Beta Feed" href="https://feeds.example.test/feeds/beta-feed.json">`,
		`</head>`,
		`<body>`,
		`  <h1>Feeds</h1>`,
		`  <ul>`,
		`    <li>`,
		`      <h2>Alpha Feed</h2>`,
		`      <p>First feed description.</p>`,
		`      <p>`,
		`        <a href="https://feeds.example.test/feeds/alpha-feed.xml">RSS</a>`,
		`        <a href="https://feeds.example.test/feeds/alpha-feed.atom">Atom</a>`,
		`        <a href="https://feeds.example.test/feeds/alpha-feed.json">JSON Feed</a>`,
		`      </p>`,
		`    </li>`,
		`    <li>`,
		`      <h2>Beta Feed</h2>`,
		`      <p>Second feed description.</p>`,
		`      <p>`,
		`        <a href="https://feeds.example.test/feeds/beta-feed.xml">RSS</a>`,
		`        <a href="https://feeds.example.test/feeds/beta-feed.atom">Atom</a>`,
		`        <a href="https://feeds.example.test/feeds/beta-feed.json">JSON Feed</a>`,
		`      </p>`,
		`    </li>`,
		`  </ul>`,
		`</body>`,
		`</html>`,
		``,
	}, "\n")

	if string(got) != want {
		t.Errorf("Index output mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestIndex_DisabledFeedExcluded checks that a disabled feed contributes
// nothing to the page — no alternate tag, no list entry — while an enabled
// one alongside it still renders in full.
func TestIndex_DisabledFeedExcluded(t *testing.T) {
	feeds := []model.Feed{
		{Slug: "live-feed", Title: "Live Feed", Description: "Still running.", Enabled: true},
		{Slug: "dead-feed", Title: "Dead Feed", Description: "Stopped generating.", Enabled: false},
	}

	got, err := Index(feeds, "https://feeds.example.test", "AnimeFeedFlux vTest")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	out := string(got)

	if !strings.Contains(out, "Live Feed") {
		t.Error("expected enabled feed's title to appear in output")
	}
	if strings.Contains(out, "Dead Feed") || strings.Contains(out, "dead-feed") {
		t.Error("disabled feed must not appear anywhere in the index")
	}
}

// TestIndex_AlternatesAllThreeFormats confirms each enabled feed gets an
// autodiscovery <link> for RSS, Atom, and JSON Feed — this is what lets a
// reader offer a subscribe when someone pastes the site URL rather than a
// feed URL.
func TestIndex_AlternatesAllThreeFormats(t *testing.T) {
	feeds := []model.Feed{
		{Slug: "aggregate-all", Title: "Everything", Description: "All items.", Enabled: true, Kind: model.KindAggregate},
	}

	got, err := Index(feeds, "https://feeds.example.test", "gen")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	out := string(got)

	wantAlternates := []string{
		`<link rel="alternate" type="application/rss+xml" title="Everything" href="https://feeds.example.test/feeds/aggregate-all.xml">`,
		`<link rel="alternate" type="application/atom+xml" title="Everything" href="https://feeds.example.test/feeds/aggregate-all.atom">`,
		`<link rel="alternate" type="application/feed+json" title="Everything" href="https://feeds.example.test/feeds/aggregate-all.json">`,
	}
	for _, want := range wantAlternates {
		if !strings.Contains(out, want) {
			t.Errorf("expected alternate tag %q in output, got:\n%s", want, out)
		}
	}
}

// TestIndex_OrderingStableAlphabetical feeds the same two feeds in a
// different input order and confirms the rendered order is always
// alphabetical by title — a page whose byte stream depends on caller/map
// order would defeat caching for no reason.
func TestIndex_OrderingStableAlphabetical(t *testing.T) {
	reversed := []model.Feed{
		{Slug: "zeta-feed", Title: "Zeta Feed", Description: "Z.", Enabled: true},
		{Slug: "alpha-feed", Title: "Alpha Feed", Description: "A.", Enabled: true},
	}

	got, err := Index(reversed, "https://feeds.example.test", "gen")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	out := string(got)

	alphaPos := strings.Index(out, "Alpha Feed")
	zetaPos := strings.Index(out, "Zeta Feed")
	if alphaPos == -1 || zetaPos == -1 {
		t.Fatalf("expected both feed titles present, got:\n%s", out)
	}
	if alphaPos > zetaPos {
		t.Errorf("expected Alpha Feed to render before Zeta Feed regardless of input order")
	}
}

// TestIndex_AmpersandEscaped checks that an unescaped ampersand in a feed
// title is hex-escaped everywhere it's emitted — the autodiscovery tag's
// title attribute and the body heading — since a raw "&" would otherwise
// break the page.
func TestIndex_AmpersandEscaped(t *testing.T) {
	feeds := []model.Feed{
		{Slug: "fun-feed", Title: "Anime & Chill", Description: "Cats & dogs.", Enabled: true},
	}

	got, err := Index(feeds, "https://feeds.example.test", "gen")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	out := string(got)

	if strings.Contains(out, "Anime & Chill") {
		t.Error("raw unescaped ampersand must not appear in output")
	}
	if !strings.Contains(out, "Anime &#x26; Chill") {
		t.Errorf("expected hex-escaped ampersand in title, got:\n%s", out)
	}
	if !strings.Contains(out, "Cats &#x26; dogs.") {
		t.Errorf("expected hex-escaped ampersand in description, got:\n%s", out)
	}
}

// TestIndex_EmptyList checks the no-feeds case renders a clear message
// rather than an empty <ul>, which would otherwise read as broken/loading.
func TestIndex_EmptyList(t *testing.T) {
	got, err := Index(nil, "https://feeds.example.test", "AnimeFeedFlux vTest")
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}

	want := strings.Join([]string{
		`<!doctype html>`,
		`<html lang="en">`,
		`<head>`,
		`  <meta charset="utf-8">`,
		`  <meta name="viewport" content="width=device-width, initial-scale=1">`,
		`  <title>Feeds</title>`,
		`  <meta name="generator" content="AnimeFeedFlux vTest">`,
		`</head>`,
		`<body>`,
		`  <h1>Feeds</h1>`,
		`  <p>No feeds are currently enabled.</p>`,
		`</body>`,
		`</html>`,
		``,
	}, "\n")

	if string(got) != want {
		t.Errorf("Index empty-list output mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
