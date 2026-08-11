package render

import (
	"encoding/xml"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// atomTestChannel builds a small, deterministic two-item Channel. Item A is
// older, Item B is newer, but they are stored in chronological (oldest
// first) order deliberately — Atom must reorder them, not trust the input
// order (PLAN.md §5.5's newest-first rule applies to Atom too, since the
// same Channel feeds every format).
func atomTestChannel() model.Channel {
	itemA := model.Item{
		ItemKey:     "01HXAAAAAAAAAAAAAAAAAAAAAA",
		Title:       "First Item",
		SummaryText: "Summary A",
		BodyHTML:    "<p>Body A</p>",
		Link:        "https://example.com/a",
		PublishedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
	}
	itemB := model.Item{
		ItemKey:     "01HXBBBBBBBBBBBBBBBBBBBBBB",
		Title:       "Second Item",
		SummaryText: "Summary B",
		BodyHTML:    "<p>Body B</p>",
		AnswerHTML:  "42",
		Link:        "https://example.com/b",
		PublishedAt: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
	}

	return model.Channel{
		Feed: model.Feed{
			Slug:        "testfeed",
			Title:       "Test Feed",
			Description: "A test feed",
			Copyright:   "Copyright 2026 Test AnimeFeedFlux",
		},
		SelfURL:   "https://example.com/feed.atom",
		HTMLURL:   "https://example.com/",
		Host:      "example.com",
		TagYear:   2026,
		Items:     []model.Item{itemA, itemB}, // oldest first: input is NOT pre-sorted
		BuildTime: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC),
		Generator: "AnimeFeedFlux vTest",
	}
}

const wantAtomDoc = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:example.com,2026:testfeed</id>
  <title>Test Feed</title>
  <updated>2026-01-02T12:00:00Z</updated>
  <author>
    <name>Test Feed</name>
  </author>
  <link rel="self" href="https://example.com/feed.atom"/>
  <link rel="alternate" href="https://example.com/"/>
  <subtitle>A test feed</subtitle>
  <rights>Copyright 2026 Test AnimeFeedFlux</rights>
  <generator>AnimeFeedFlux vTest</generator>
  <entry>
    <id>tag:example.com,2026:testfeed/01HXBBBBBBBBBBBBBBBBBBBBBB</id>
    <title>Second Item</title>
    <updated>2026-01-02T10:00:00Z</updated>
    <link rel="alternate" href="https://example.com/b"/>
    <summary type="text">Summary B</summary>
    <content type="html">&#x3C;p&#x3E;Body B&#x3C;/p&#x3E;&#x3C;hr class=&#x22;spoiler-break&#x22;/&#x3E;&#x3C;p&#x3E;&#x3C;strong&#x3E;Answer:&#x3C;/strong&#x3E; 42&#x3C;/p&#x3E;</content>
  </entry>
  <entry>
    <id>tag:example.com,2026:testfeed/01HXAAAAAAAAAAAAAAAAAAAAAA</id>
    <title>First Item</title>
    <updated>2026-01-01T10:00:00Z</updated>
    <link rel="alternate" href="https://example.com/a"/>
    <summary type="text">Summary A</summary>
    <content type="html">&#x3C;p&#x3E;Body A&#x3C;/p&#x3E;</content>
  </entry>
</feed>
`

func TestAtomExactDocument(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	if string(got) != wantAtomDoc {
		t.Fatalf("Atom() mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, wantAtomDoc)
	}
}

// TestAtomFeedLevelSingleIDTitleUpdated pins RFC 4287's "exactly one"
// requirement at the feed level: a renderer bug that duplicated one of these
// (e.g. an accidental extra <title> from a copy-pasted subtitle block) would
// still produce a document that *looks* plausible on a quick read.
func TestAtomFeedLevelSingleIDTitleUpdated(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	// Isolate the feed header: everything before the first <entry>.
	header := doc
	if i := strings.Index(doc, "<entry>"); i >= 0 {
		header = doc[:i]
	}

	for _, tag := range []string{"<id>", "<title>", "<updated>"} {
		if n := strings.Count(header, tag); n != 1 {
			t.Errorf("feed header has %d occurrences of %s, want exactly 1", n, tag)
		}
	}
}

// TestAtomEntryLevelSingleIDTitleUpdated does the same check per <entry>.
func TestAtomEntryLevelSingleIDTitleUpdated(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	entryRe := regexp.MustCompile(`(?s)<entry>(.*?)</entry>`)
	entries := entryRe.FindAllStringSubmatch(doc, -1)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for i, m := range entries {
		body := m[1]
		for _, tag := range []string{"<id>", "<title>", "<updated>"} {
			if n := strings.Count(body, tag); n != 1 {
				t.Errorf("entry %d has %d occurrences of %s, want exactly 1", i, n, tag)
			}
		}
	}
}

// TestAtomEntryIDMatchesTagURI asserts the entry id is not just "a" tag URI
// but the *same string* TagURI would build for the RSS guid from identical
// inputs — §5.2 requires byte-identical ids since consumers compare
// character-by-character, case-sensitively.
func TestAtomEntryIDMatchesTagURI(t *testing.T) {
	c := atomTestChannel()
	got, err := Atom(c)
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	for _, it := range c.Items {
		want := TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey)
		wantTag := "<id>" + EscapeText(want) + "</id>"
		if !strings.Contains(doc, wantTag) {
			t.Errorf("document missing entry id %q built by TagURI", want)
		}
	}
}

// TestAtomDatesAreRFC3339 checks every <updated> value against the exact
// shape RFC 3339 with an uppercase T and uppercase Z requires — a formatter
// that silently fell back to "+00:00" for UTC (a real Go footgun; see
// dates.go) would pass a looser check but fail Slack and other strict
// consumers.
func TestAtomDatesAreRFC3339(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	dateRe := regexp.MustCompile(`<updated>([^<]*)</updated>`)
	shapeRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

	matches := dateRe.FindAllStringSubmatch(doc, -1)
	if len(matches) != 3 { // 1 feed-level + 2 entries
		t.Fatalf("got %d <updated> elements, want 3", len(matches))
	}
	for _, m := range matches {
		if !shapeRe.MatchString(m[1]) {
			t.Errorf("<updated>%s</updated> does not match RFC 3339 with uppercase T/Z", m[1])
		}
	}
}

// TestAtomTriviaAnswerOnlyInContent is the spoiler-prevention test: the
// answer must never reach <summary> (what Slack and plain-text readers
// show) but must reach <content> (§5.5).
func TestAtomTriviaAnswerOnlyInContent(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	entryRe := regexp.MustCompile(`(?s)<entry>(.*?)</entry>`)
	entries := entryRe.FindAllString(doc, -1)

	var triviaEntry string
	for _, e := range entries {
		if strings.Contains(e, "Second Item") {
			triviaEntry = e
		}
	}
	if triviaEntry == "" {
		t.Fatal("could not find the trivia entry (Second Item) in output")
	}

	summaryRe := regexp.MustCompile(`(?s)<summary[^>]*>(.*?)</summary>`)
	contentRe := regexp.MustCompile(`(?s)<content[^>]*>(.*?)</content>`)

	summary := summaryRe.FindStringSubmatch(triviaEntry)
	content := contentRe.FindStringSubmatch(triviaEntry)
	if summary == nil || content == nil {
		t.Fatal("trivia entry missing summary or content element")
	}

	if strings.Contains(summary[1], "42") {
		t.Errorf("summary leaks the trivia answer: %q", summary[1])
	}
	if !strings.Contains(content[1], "42") {
		t.Errorf("content is missing the trivia answer: %q", content[1])
	}
}

// TestAtomWellFormedXML parses the output with encoding/xml, which fails on
// anything from a stray unescaped "&" to a mismatched tag. This is the
// backstop for the hand-built bytes.Buffer approach: nothing here catches
// a typo in a closing tag except actually trying to parse it.
func TestAtomWellFormedXML(t *testing.T) {
	got, err := Atom(atomTestChannel())
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}

	var doc struct {
		XMLName xml.Name `xml:"feed"`
		ID      string   `xml:"id"`
		Title   string   `xml:"title"`
		Updated string   `xml:"updated"`
		Entries []struct {
			ID string `xml:"id"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("output is not well-formed XML: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(doc.Entries))
	}
}

// TestAtomEntriesNewestFirst uses three items in a deliberately scrambled
// input order and asserts the rendered order is strictly newest-first by
// PublishedAt — independent of TestAtomExactDocument, which only exercises
// a two-item reversal.
func TestAtomEntriesNewestFirst(t *testing.T) {
	mk := func(key string, day int) model.Item {
		return model.Item{
			ItemKey:     key,
			Title:       key,
			SummaryText: "s",
			BodyHTML:    "<p>b</p>",
			Link:        "https://example.com/" + key,
			PublishedAt: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC),
		}
	}

	c := atomTestChannel()
	// Scrambled: middle, newest, oldest.
	c.Items = []model.Item{mk("mid", 2), mk("new", 3), mk("old", 1)}

	got, err := Atom(c)
	if err != nil {
		t.Fatalf("Atom() error = %v", err)
	}
	doc := string(got)

	newIdx := strings.Index(doc, "<title>new</title>")
	midIdx := strings.Index(doc, "<title>mid</title>")
	oldIdx := strings.Index(doc, "<title>old</title>")
	if newIdx < 0 || midIdx < 0 || oldIdx < 0 {
		t.Fatalf("missing an expected entry in output:\n%s", doc)
	}
	if !(newIdx < midIdx && midIdx < oldIdx) {
		t.Errorf("entries not newest-first: new=%d mid=%d old=%d", newIdx, midIdx, oldIdx)
	}
}
