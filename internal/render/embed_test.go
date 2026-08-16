package render

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// The embed is the first surface this project renders INTO a page it does
// not control, so these tests are weighted toward what leaks and what
// executes rather than toward layout.

// TestEmbedNeverLeaksTheAnswer is the one that must never be deleted. §5.5
// makes SummaryText the only answer-free field; an embed reaching for
// BodyHTML or AnswerHTML spoils the trivia on every page that embeds it.
func TestEmbedNeverLeaksTheAnswer(t *testing.T) {
	c := testutil.SampleChannel(2)
	c.Items = append(c.Items, testutil.TriviaItem())

	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)

	if strings.Contains(body, "ANSWER-COWBOY-BEBOP") {
		t.Fatal("embed leaked AnswerHTML; the answer must never leave the permalink's <details>")
	}
	// The question itself must be there — proving the item rendered at all,
	// so the assertion above cannot pass by rendering nothing.
	if !strings.Contains(body, "Which studio animated Cowboy Bebop?") {
		t.Fatal("embed did not render the trivia item's title")
	}
}

// TestEmbedRendersNoItemHTML pins the stronger rule the answer test is one
// case of: no item-authored markup reaches this document at all, so the
// sanitiser is not the only thing standing between model output and a third
// party's page.
func TestEmbedRendersNoItemHTML(t *testing.T) {
	c := testutil.SampleChannel(1)
	c.Items[0].BodyHTML = `<p>Body HTML that must not appear.</p>`
	c.Items[0].SummaryText = "Summary that must appear."

	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)

	if strings.Contains(body, "Body HTML that must not appear") {
		t.Error("embed rendered BodyHTML")
	}
	if !strings.Contains(body, "Summary that must appear.") {
		t.Error("embed did not render SummaryText")
	}
}

// TestEmbedEscapesHostileText proves the escaping applies to the fields that
// do render. Model output is hostile input (CONTRIBUTING.md standing rules),
// and this document goes somewhere a script tag would be somebody else's
// incident.
func TestEmbedEscapesHostileText(t *testing.T) {
	c := testutil.SampleChannel(1)
	c.Items[0].Title = `<script>alert(1)</script> & "quotes"`
	c.Items[0].SummaryText = `<img src=x onerror=alert(1)>`
	c.Feed.Title = `Feed <b>title</b>`

	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)

	for _, raw := range []string{"<script>", "<img ", "<b>title"} {
		if strings.Contains(body, raw) {
			t.Errorf("embed emitted %q unescaped", raw)
		}
	}
	if !strings.Contains(body, "&#x3C;script&#x3E;") {
		t.Error("embed did not hex-escape the title's markup")
	}
}

// TestEmbedLinkSchemes: only http/https become anchors. A javascript: link
// cannot reach an item through the generation pipeline, but this renderer
// must not be the reason that stays true.
func TestEmbedLinkSchemes(t *testing.T) {
	cases := []struct {
		name    string
		link    string
		wantURL bool
	}{
		{"https", "https://example.com/a", true},
		{"http", "http://example.com/a", true},
		{"javascript", "javascript:alert(1)", false},
		{"data", "data:text/html,<script>alert(1)</script>", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testutil.SampleChannel(1)
			c.Items[0].Link = tc.link
			c.Items[0].Title = "Linked item"

			got, err := Embed(c, EmbedOptions{})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			body := string(got)

			// The feed's own SelfURL/HTMLURL are https links that always
			// render, so count only anchors carrying the item's title.
			linked := strings.Contains(body, `>Linked item</a>`)
			if linked != tc.wantURL {
				t.Fatalf("item linked = %v, want %v (link %q)", linked, tc.wantURL, tc.link)
			}
			if !strings.Contains(body, "Linked item") {
				t.Fatal("title vanished entirely; an unlinkable item must still render as text")
			}
			if !tc.wantURL && strings.Contains(body, "javascript:") {
				t.Fatal("embed emitted a javascript: URL")
			}
		})
	}
}

// TestEmbedNewestFirstAndCapped: the window is sorted and cut here, not
// trusted from the caller — ListItems is documented as returning items in
// any order.
func TestEmbedNewestFirstAndCapped(t *testing.T) {
	c := testutil.SampleChannel(20)
	// Reverse, so a renderer that trusted the caller's order would fail.
	for i, j := 0, len(c.Items)-1; i < j; i, j = i+1, j-1 {
		c.Items[i], c.Items[j] = c.Items[j], c.Items[i]
	}

	got, err := Embed(c, EmbedOptions{Count: 5})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)

	if n := strings.Count(body, `<li class="aff-item">`); n != 5 {
		t.Fatalf("rendered %d items, want 5", n)
	}
	// SampleItems timestamps increase with the index, so the newest five are
	// 19..15 and item 14 must not appear.
	for _, want := range []string{"Sample Item 19", "Sample Item 15"} {
		if !strings.Contains(body, want) {
			t.Errorf("newest window missing %q", want)
		}
	}
	if strings.Contains(body, "Sample Item 14") {
		t.Error("window included an item beyond the requested count")
	}
	if idx19, idx15 := strings.Index(body, "Sample Item 19"), strings.Index(body, "Sample Item 15"); idx19 > idx15 {
		t.Error("items are not newest-first")
	}
}

// TestEmbedCallerSliceUntouched: Embed sorts a copy. The same slice is
// handed to the feed renderers on other requests.
func TestEmbedCallerSliceUntouched(t *testing.T) {
	c := testutil.SampleChannel(3)
	before := make([]string, len(c.Items))
	for i, it := range c.Items {
		before[i] = it.ItemKey
	}

	if _, err := Embed(c, EmbedOptions{}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for i, it := range c.Items {
		if it.ItemKey != before[i] {
			t.Fatalf("Embed reordered the caller's slice at %d", i)
		}
	}
}

// TestEmbedOptionsResolve: invalid values that reach the renderer fall back
// to the defaults rather than producing a document with no CSS or a negative
// window.
func TestEmbedOptionsResolve(t *testing.T) {
	cases := []EmbedOptions{
		{},
		{Count: -1, Theme: "chartreuse"},
		{Count: 7},
		{Count: 5, Theme: "dark"},
	}
	c := testutil.SampleChannel(12)
	for _, o := range cases {
		got, err := Embed(c, o)
		if err != nil {
			t.Fatalf("Embed(%+v): %v", o, err)
		}
		body := string(got)
		if !strings.Contains(body, "--bg:") {
			t.Errorf("Embed(%+v) produced no palette", o)
		}
		wantItems := DefaultEmbedCount
		if ValidEmbedCount(o.Count) {
			wantItems = o.Count
		}
		if n := strings.Count(body, `<li class="aff-item">`); n != wantItems {
			t.Errorf("Embed(%+v) rendered %d items, want %d", o, n, wantItems)
		}
	}
}

// TestEmbedThemes: each theme emits a complete palette, and "auto" defines
// light unconditionally so a client with no prefers-color-scheme support
// still gets one rather than undefined custom properties.
func TestEmbedThemes(t *testing.T) {
	c := testutil.SampleChannel(2)

	light, _ := Embed(c, EmbedOptions{Theme: "light"})
	dark, _ := Embed(c, EmbedOptions{Theme: "dark"})
	auto, _ := Embed(c, EmbedOptions{Theme: "auto"})

	if strings.Contains(string(light), "prefers-color-scheme") {
		t.Error("theme=light must not carry a colour-scheme media query")
	}
	if strings.Contains(string(dark), "prefers-color-scheme") {
		t.Error("theme=dark must not carry a colour-scheme media query")
	}
	if !strings.Contains(string(auto), "prefers-color-scheme:dark") {
		t.Error("theme=auto must follow the reader's colour scheme")
	}
	autoBody := string(auto)
	if !strings.Contains(autoBody, "color-scheme:light") {
		t.Error("theme=auto must define the light palette unconditionally")
	}
	if strings.Contains(string(dark), "#fff") {
		t.Error("theme=dark still carries the light background")
	}
}

// TestEmbedNoExternalReferences: the document fetches nothing. A widget that
// blocks on a third party is a widget that makes its host's page slower, and
// the CSP the handler sends (default-src 'none') would break it anyway.
func TestEmbedNoExternalReferences(t *testing.T) {
	c := testutil.SampleChannel(3)
	got, _ := Embed(c, EmbedOptions{})
	body := string(got)

	for _, bad := range []string{"<script", "<link ", "@import", "url(", "<img"} {
		if strings.Contains(body, bad) {
			t.Errorf("embed document references %q; it must be self-contained", bad)
		}
	}
}

// TestEmbedNoindex: this document duplicates content that has a canonical
// home. One indexed copy per embedding site is a feed competing with itself.
func TestEmbedNoindex(t *testing.T) {
	got, _ := Embed(testutil.SampleChannel(1), EmbedOptions{})
	if !strings.Contains(string(got), `<meta name="robots" content="noindex">`) {
		t.Error("embed is missing its noindex")
	}
}

// TestEmbedEmptyFeed: a sentence, not a blank panel.
func TestEmbedEmptyFeed(t *testing.T) {
	c := testutil.SampleChannel(0)
	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)
	if !strings.Contains(body, "No items yet.") {
		t.Error("empty feed did not render its empty state")
	}
	if strings.Contains(body, "<ol") {
		t.Error("empty feed rendered an empty list")
	}
	if !strings.Contains(body, "Subscribe by RSS") {
		t.Error("empty feed dropped the subscribe link")
	}
}

// TestEmbedSubscribeLinkIsTheFeed: the footer points at the RSS document,
// not at the embed itself — an embed linking to itself is a dead end on
// somebody else's page.
func TestEmbedSubscribeLinkIsTheFeed(t *testing.T) {
	c := testutil.SampleChannel(1)
	got, _ := Embed(c, EmbedOptions{})
	if !strings.Contains(string(got), `href="`+c.SelfURL+`"`) {
		t.Errorf("subscribe link does not point at %s", c.SelfURL)
	}
}

// TestEmbedTimestampsAreExact: the visible date is a label; the <time>
// element carries the instant. §5.5 makes PublishedAt load-bearing, so it
// must survive rendering unrounded.
func TestEmbedTimestampsAreExact(t *testing.T) {
	c := testutil.SampleChannel(1)
	c.Items[0].PublishedAt = time.Date(2026, time.August, 9, 12, 34, 56, 0, time.UTC)

	got, _ := Embed(c, EmbedOptions{})
	body := string(got)

	if !strings.Contains(body, `datetime="2026-08-09T12:34:56Z"`) {
		t.Error("embed did not carry the exact RFC 3339 instant")
	}
	if !strings.Contains(body, ">9 Aug 2026 · 12:34 UTC</time>") {
		t.Error("embed did not render the human-readable stamp")
	}
}

// TestEmbedStampsDistinguishSameDayItems: a feed that runs more than once a
// day would otherwise render every item of that day with the identical
// label. Items within a SINGLE run are one second apart and do share a
// stamp, which is correct — they were published together.
func TestEmbedStampsDistinguishSameDayItems(t *testing.T) {
	c := testutil.SampleChannel(0)
	day := time.Date(2026, time.August, 9, 0, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{
		day.Add(9 * time.Hour),
		day.Add(9*time.Hour + 31*time.Minute),
		day.Add(17 * time.Hour),
	} {
		c.Items = append(c.Items, model.Item{
			ItemKey:     "K" + string(rune('a'+i)),
			Title:       "Item",
			SummaryText: "Summary.",
			PublishedAt: at,
		})
	}

	got, _ := Embed(c, EmbedOptions{})
	body := string(got)
	for _, want := range []string{"9 Aug 2026 · 09:00 UTC", "9 Aug 2026 · 09:31 UTC", "9 Aug 2026 · 17:00 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("stamp %q missing; same-day items are indistinguishable", want)
		}
	}
}

// TestEmbedDeterministic: the same channel renders the same bytes. The ETag
// is a hash of this output, so a document that varied per call would produce
// a validator that never matched and a cache that never hit.
func TestEmbedDeterministic(t *testing.T) {
	c := testutil.SampleChannel(4)
	first, _ := Embed(c, EmbedOptions{})
	second, _ := Embed(c, EmbedOptions{})
	if string(first) != string(second) {
		t.Fatal("Embed is not deterministic")
	}
}

// TestEmbedSnippetShape: the snippet is what an operator pastes, so it has
// to be complete and it has to point at the canonical URL.
func TestEmbedSnippetShape(t *testing.T) {
	got := EmbedSnippet("https://anime.example.com", "trivia-daily")

	for _, want := range []string{
		`src="https://anime.example.com/embed/trivia-daily"`,
		`width="100%"`,
		`height=`,
		`loading="lazy"`,
		`title="trivia-daily"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet missing %s\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "&") {
		t.Errorf("snippet carries a query string ampersand, which does not survive display-escape and copy: %s", got)
	}
}

// TestEmbedLanguageFallback: an unset feed language must still produce a
// valid lang attribute.
func TestEmbedLanguageFallback(t *testing.T) {
	c := testutil.SampleChannel(1)
	c.Feed.Language = ""
	got, _ := Embed(c, EmbedOptions{})
	if !strings.Contains(string(got), `<html lang="en">`) {
		t.Error("embed did not fall back to lang=en")
	}
}

// TestEmbedWellFormed parses the document to catch a malformed tag the
// string assertions above would miss.
func TestEmbedWellFormed(t *testing.T) {
	c := testutil.SampleChannel(3)
	c.Items = append(c.Items, testutil.TriviaItem())
	c.Items = append(c.Items, model.Item{
		Title:       `Ugly & <Weird> 日本語 🎌`,
		SummaryText: "Summary with an & ampersand and a <tag>.",
		PublishedAt: time.Date(2026, time.August, 10, 1, 2, 3, 0, time.UTC),
	})

	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	assertBalancedTags(t, string(got))
}

// assertBalancedTags is a cheap structural check: every element this
// renderer opens is closed, in order. html.Parse would accept almost
// anything (that is what an HTML parser does), so it would not catch the
// mistake this is guarding against.
func assertBalancedTags(t *testing.T, doc string) {
	t.Helper()
	for _, tag := range []string{"html", "head", "style", "body", "section", "h1", "ol", "li", "time", "div", "span", "p", "a"} {
		open := strings.Count(doc, "<"+tag+" ") + strings.Count(doc, "<"+tag+">")
		closed := strings.Count(doc, "</"+tag+">")
		if open != closed {
			t.Errorf("<%s>: %d opened, %d closed", tag, open, closed)
		}
	}
}

// TestEmbedOmitsAZeroTimestamp: an item with no published_at renders with no
// caption at all, never as "1 Jan 0001". A sampled candidate carries the zero
// time (generate.Sample skips stampItems), and a preview that printed it made
// the whole widget look broken rather than showing one missing fact.
func TestEmbedOmitsAZeroTimestamp(t *testing.T) {
	c := testutil.SampleChannel(0)
	c.Items = append(c.Items, model.Item{
		ItemKey:     "NODATE",
		Title:       "An item with no publish time",
		SummaryText: "It should still render.",
	})

	got, err := Embed(c, EmbedOptions{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	body := string(got)

	if strings.Contains(body, "0001") {
		t.Error("embed rendered the zero time as a date")
	}
	if strings.Contains(body, "<time") {
		t.Error("embed emitted an empty <time> element rather than omitting it")
	}
	if !strings.Contains(body, "An item with no publish time") {
		t.Error("the item itself vanished along with its missing date")
	}
	if !strings.Contains(body, "It should still render.") {
		t.Error("the summary vanished along with the missing date")
	}
}
