package render

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// rssTestChannel builds a small, deterministic two-item Channel. Item order in
// the slice is deliberately NOT newest-first, so RSS()'s own sort is what
// makes the output correct rather than the fixture doing RSS's job for it.
// One item is a trivia item with an answer (exercising the spoiler-break
// rule and the description/content:encoded split); the other has an empty
// link and a title with characters that must be hex-escaped.
func rssTestChannel() model.Channel {
	itemA := model.Item{
		ItemKey:     "01J8XYZABCDEFGHJKMNPQRSTVW",
		Title:       "What studio animated Cowboy Bebop?",
		SummaryText: "Test your knowledge of legendary anime studios.",
		BodyHTML:    "<p>Which studio animated <strong>Cowboy Bebop</strong>?</p>",
		AnswerHTML:  "Sunrise",
		Link:        "https://anime.earlcameron.com/items/01J8XYZABCDEFGHJKMNPQRSTVW",
		PublishedAt: time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC),
	}
	itemB := model.Item{
		ItemKey:     "01J8XYZABCDEFGHJKMNPQRSTVX",
		Title:       "Anime News: Tom & Jerry <crossover>?",
		SummaryText: "This weeks anime news, summarized.",
		BodyHTML:    "<p>Full roundup here.</p>",
		Link:        "",
		PublishedAt: time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC),
	}

	feed := model.Feed{
		Slug:        "anime-trivia-daily",
		Title:       "Anime Trivia Daily",
		Description: "A daily anime trivia question",
		Language:    "en-us",
		TTLMinutes:  15,
	}

	return model.Channel{
		Feed:      feed,
		SelfURL:   "https://anime.earlcameron.com/feeds/anime-trivia-daily.xml",
		HTMLURL:   "https://anime.earlcameron.com/feeds/anime-trivia-daily",
		Host:      "anime.earlcameron.com",
		TagYear:   2026,
		Items:     []model.Item{itemA, itemB}, // NOT newest-first on purpose
		BuildTime: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		Generator: "AnimeFeedFlux v0.0.2-dev",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}

// TestRSS_ExactDocument is the golden-document test. Date and guid strings
// are computed through RFC822/TagURI rather than hand-typed (e.g. the
// weekday name), since those functions have their own dedicated,
// independently-passing tests — hand-deriving a weekday here would just be
// a second, error-prone implementation of logic already covered elsewhere.
// Everything else (tag names, indentation, attribute order, escaping) is
// spelled out literally, because pinning that shape is the point of the
// test.
func TestRSS_ExactDocument(t *testing.T) {
	c := rssTestChannel()

	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	pubDateChannel := RFC822(c.NewestPublished())
	lastBuildDate := RFC822(c.BuildTime)
	pubDateB := RFC822(c.Items[1].PublishedAt)
	pubDateA := RFC822(c.Items[0].PublishedAt)
	guidB := TagURI(c.Host, c.TagYear, c.Feed.Slug, c.Items[1].ItemKey)
	guidA := TagURI(c.Host, c.TagYear, c.Feed.Slug, c.Items[0].ItemKey)

	want := "" +
		`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:content="http://purl.org/rss/1.0/modules/content/">` + "\n" +
		"  <channel>\n" +
		"    <title>Anime Trivia Daily</title>\n" +
		"    <link>https://anime.earlcameron.com/feeds/anime-trivia-daily</link>\n" +
		"    <description>A daily anime trivia question</description>\n" +
		"    <language>en-us</language>\n" +
		fmt.Sprintf("    <pubDate>%s</pubDate>\n", pubDateChannel) +
		fmt.Sprintf("    <lastBuildDate>%s</lastBuildDate>\n", lastBuildDate) +
		"    <generator>AnimeFeedFlux v0.0.2-dev</generator>\n" +
		"    <docs>https://www.rssboard.org/rss-specification</docs>\n" +
		"    <ttl>15</ttl>\n" +
		`    <atom:link rel="self" type="application/rss+xml" href="https://anime.earlcameron.com/feeds/anime-trivia-daily.xml"/>` + "\n" +
		// itemB (Aug 10) is newer than itemA (Aug 9) and must render first,
		// even though it is second in c.Items.
		"    <item>\n" +
		"      <title>Anime News: Tom &#x26; Jerry &#x3C;crossover&#x3E;?</title>\n" +
		"      <link></link>\n" +
		"      <description>This weeks anime news, summarized.</description>\n" +
		"      <content:encoded><![CDATA[<p>Full roundup here.</p>]]></content:encoded>\n" +
		fmt.Sprintf(`      <guid isPermaLink="false">%s</guid>`+"\n", guidB) +
		fmt.Sprintf("      <pubDate>%s</pubDate>\n", pubDateB) +
		"    </item>\n" +
		"    <item>\n" +
		"      <title>What studio animated Cowboy Bebop?</title>\n" +
		"      <link>https://anime.earlcameron.com/items/01J8XYZABCDEFGHJKMNPQRSTVW</link>\n" +
		"      <description>Test your knowledge of legendary anime studios.</description>\n" +
		`      <content:encoded><![CDATA[<p>Which studio animated <strong>Cowboy Bebop</strong>?</p><hr class="spoiler-break"/><p><strong>Answer:</strong> Sunrise</p>]]></content:encoded>` + "\n" +
		fmt.Sprintf(`      <guid isPermaLink="false">%s</guid>`+"\n", guidA) +
		fmt.Sprintf("      <pubDate>%s</pubDate>\n", pubDateA) +
		"    </item>\n" +
		"  </channel>\n" +
		"</rss>\n"

	if string(got) != want {
		t.Fatalf("RSS() mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRSS_GuidIsPermaLinkFalse guards §5.1's "never rely on the true
// default" rule: the attribute must be present and explicitly false on
// every item, not merely absent.
func TestRSS_GuidIsPermaLinkFalse(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	if n := strings.Count(doc, `isPermaLink="false"`); n != 2 {
		t.Errorf(`expected 2 occurrences of isPermaLink="false", got %d in:\n%s`, n, doc)
	}
	if strings.Contains(doc, `isPermaLink="true"`) {
		t.Errorf("guid must never be isPermaLink=\"true\"")
	}
	if !strings.Contains(doc, "tag:anime.earlcameron.com,2026:anime-trivia-daily/") {
		t.Errorf("guid must be a Tag URI, got:\n%s", doc)
	}
}

// TestRSS_DescriptionExcludesBodyAndAnswer is §5.5's spoiler and
// HTML-in-description rule: the plain-text summary must never carry the
// trivia answer or the HTML body, or Slack spoils the question / shows a
// mangled HTML dump.
func TestRSS_DescriptionExcludesBodyAndAnswer(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	descStart := strings.Index(doc, "<description>Test your knowledge")
	if descStart < 0 {
		t.Fatalf("could not locate trivia item's description in:\n%s", doc)
	}
	// Search for the closing tag starting from descStart, not from the
	// start of the document — the non-trivia item's </description> comes
	// first in render order and would otherwise produce a bogus (empty or
	// negative-length) slice.
	descEnd := strings.Index(doc[descStart:], "</description>")
	if descEnd < 0 {
		t.Fatalf("could not locate closing </description> in:\n%s", doc)
	}
	description := doc[descStart : descStart+descEnd]

	if strings.Contains(description, "Sunrise") {
		t.Errorf("description leaked the trivia answer: %q", description)
	}
	if strings.Contains(description, "<strong>") || strings.Contains(description, "<p>") {
		t.Errorf("description leaked HTML body markup: %q", description)
	}
}

// TestRSS_ContentEncodedContainsBody checks the other side of the split:
// content:encoded carries the full HTML body and, for a trivia item, the
// answer behind a spoiler break.
func TestRSS_ContentEncodedContainsBody(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	if !strings.Contains(doc, "<p>Which studio animated <strong>Cowboy Bebop</strong>?</p>") {
		t.Errorf("content:encoded missing body HTML, got:\n%s", doc)
	}
	if !strings.Contains(doc, `spoiler-break`) || !strings.Contains(doc, "Sunrise") {
		t.Errorf("content:encoded missing spoiler-break answer, got:\n%s", doc)
	}
	if !strings.Contains(doc, "<p>Full roundup here.</p>") {
		t.Errorf("content:encoded missing non-trivia item's body HTML, got:\n%s", doc)
	}
}

// TestRSS_ItemsOrderedNewestFirst re-asserts ordering independently of the
// golden-document test, so a future refactor that keeps the exact document
// test passing (e.g. by also reordering the fixture) can't silently drop
// the sort.
func TestRSS_ItemsOrderedNewestFirst(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	newerIdx := strings.Index(doc, "Anime News: Tom")
	olderIdx := strings.Index(doc, "What studio animated Cowboy Bebop?")
	if newerIdx < 0 || olderIdx < 0 {
		t.Fatalf("could not locate both item titles in:\n%s", doc)
	}
	if newerIdx > olderIdx {
		t.Errorf("newer item (Aug 10) must render before older item (Aug 9); got newer at %d, older at %d", newerIdx, olderIdx)
	}
}

// TestRSS_WellFormedXML parses the output with encoding/xml, independent of
// the byte-for-byte golden test — a document could match expected bytes and
// still be malformed if the golden string itself were wrong, so this is the
// check that actually proves "a feed reader can parse this".
func TestRSS_WellFormedXML(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	var doc struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("RSS() output is not well-formed XML: %v\n%s", err, got)
	}
	if doc.Channel.Title != "Anime Trivia Daily" {
		t.Errorf("channel title = %q, want %q", doc.Channel.Title, "Anime Trivia Daily")
	}
	if len(doc.Channel.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(doc.Channel.Items))
	}
}

// TestRSS_TitleHexEscaped pins §5.1's escaping rule for a title containing
// both "&" and "<".
func TestRSS_TitleHexEscaped(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	if !strings.Contains(doc, "Anime News: Tom &#x26; Jerry &#x3C;crossover&#x3E;?") {
		t.Errorf("title was not hex-escaped as expected, got:\n%s", doc)
	}
	if strings.Contains(doc, "Tom & Jerry <crossover>") {
		t.Errorf("title appeared unescaped in output:\n%s", doc)
	}
}

// TestRSS_EmptyLinkRenders is the "at least title/description present"
// tolerance from §5.1 applied to link specifically: an item with no link
// (plausible for a pure-generative item with no permalink source) must
// still render a well-formed, empty <link> rather than being skipped or
// breaking the document.
func TestRSS_EmptyLinkRenders(t *testing.T) {
	got, err := RSS(rssTestChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	if !strings.Contains(doc, "      <link></link>\n") {
		t.Errorf("expected an empty <link></link> element for the linkless item, got:\n%s", doc)
	}
}
