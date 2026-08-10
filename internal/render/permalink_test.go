package render

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

func testChannel() model.Channel {
	return model.Channel{
		Feed: model.Feed{
			Slug:     "anime-news-daily",
			Title:    "Anime News Daily",
			Language: "en",
			OGImage:  "https://example.com/og/anime-news-daily.png",
		},
		HTMLURL: "https://example.com/",
		Host:    "example.com",
	}
}

func TestPermalink_NonTrivia_ExactBytes(t *testing.T) {
	c := testChannel()
	it := model.Item{
		ItemKey:     "01J000000000000000000NEWS",
		Title:       "Studio Announces New Series",
		SummaryText: "A major studio has announced a new anime series for next season.",
		BodyHTML:    "<p>A major studio has announced a new anime series for next season.</p>",
		Link:        "https://example.com/items/01J000000000000000000NEWS",
		PublishedAt: time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}

	want := "<!DOCTYPE html>\n" +
		`<html lang="en">` + "\n" +
		"<head>\n" +
		`<meta charset="utf-8">` + "\n" +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n" +
		"<title>Studio Announces New Series</title>\n" +
		`<meta name="description" content="A major studio has announced a new anime series for next season.">` + "\n" +
		`<meta property="og:title" content="Studio Announces New Series">` + "\n" +
		`<meta property="og:description" content="A major studio has announced a new anime series for next season.">` + "\n" +
		`<meta property="og:image" content="https://example.com/og/anime-news-daily.png">` + "\n" +
		`<meta property="og:type" content="article">` + "\n" +
		`<meta property="og:url" content="https://example.com/items/01J000000000000000000NEWS">` + "\n" +
		`<meta property="article:published_time" content="2026-08-10T12:30:00Z">` + "\n" +
		`<meta name="twitter:card" content="summary_large_image">` + "\n" +
		"</head>\n" +
		"<body>\n" +
		"<article>\n" +
		"<h1>Studio Announces New Series</h1>\n" +
		`<time datetime="2026-08-10T12:30:00Z">2026-08-10T12:30:00Z</time>` + "\n" +
		"<p>A major studio has announced a new anime series for next season.</p>\n" +
		"</article>\n" +
		"</body>\n" +
		"</html>\n"

	if string(got) != want {
		t.Fatalf("Permalink output mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestPermalink_Trivia_DescriptionIsQuestionOnly(t *testing.T) {
	c := testChannel()
	const question = "Which studio animated Cowboy Bebop?"
	const answer = "Sunrise, in 1998."
	it := model.Item{
		ItemKey:     "01J000000000000000TRIVIA",
		Title:       "Anime Trivia of the Day",
		SummaryText: question,
		BodyHTML:    "<p>" + question + "</p>",
		AnswerHTML:  "<p>" + answer + "</p>",
		Link:        "https://example.com/items/01J000000000000000TRIVIA",
		PublishedAt: time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}
	page := string(got)

	if !strings.Contains(page, `<meta property="og:description" content="Which studio animated Cowboy Bebop?">`) {
		t.Fatalf("og:description missing or wrong; page:\n%s", page)
	}

	// The whole point of this renderer (PLAN.md §5.5): the answer must never
	// reach a meta tag, since Slack unfurls read only meta content and have
	// no spoiler mechanism.
	head := page[:strings.Index(page, "</head>")]
	if strings.Contains(head, answer) {
		t.Fatalf("answer text leaked into <head>:\n%s", head)
	}
	if strings.Contains(head, "Sunrise") {
		t.Fatalf("answer text leaked into <head> (partial match):\n%s", head)
	}

	// The answer must still be ON the page, reachable by a real reader,
	// just behind a spoiler.
	if !strings.Contains(page, "<details>\n<summary>Reveal the answer</summary>\n"+it.AnswerHTML+"\n</details>") {
		t.Fatalf("answer not present in a <details> block; page:\n%s", page)
	}
}

func TestPermalink_QuoteAndAmpersandInTitle(t *testing.T) {
	c := testChannel()
	it := model.Item{
		ItemKey:     "01J0000000000000QUOTETST",
		Title:       `The "Best" Anime & Manga List`,
		SummaryText: `A rundown of "must watch" titles & why they matter.`,
		BodyHTML:    "<p>body</p>",
		Link:        "https://example.com/items/01J0000000000000QUOTETST",
		PublishedAt: time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}
	page := string(got)

	wantTitleEscaped := "The &#x22;Best&#x22; Anime &#x26; Manga List"
	wantSummaryEscaped := "A rundown of &#x22;must watch&#x22; titles &#x26; why they matter."

	if !strings.Contains(page, "<title>"+wantTitleEscaped+"</title>") {
		t.Fatalf("escaped title not found; page:\n%s", page)
	}
	if !strings.Contains(page, `content="`+wantTitleEscaped+`"`) {
		t.Fatalf("escaped title not found in a meta content attribute; page:\n%s", page)
	}
	if !strings.Contains(page, `content="`+wantSummaryEscaped+`"`) {
		t.Fatalf("escaped summary not found in a meta content attribute; page:\n%s", page)
	}

	// Every meta content="..." attribute value must be free of raw quotes
	// and ampersands: a raw '"' inside the value would close the attribute
	// early and truncate the tag, and a raw '&' is invalid outside a
	// character reference.
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, "<meta ") && !strings.HasPrefix(line, "<title>") {
			continue
		}
		idx := strings.Index(line, `content="`)
		var attrVal string
		if idx >= 0 {
			rest := line[idx+len(`content="`):]
			end := strings.Index(rest, `">`)
			if end < 0 {
				t.Fatalf("malformed meta line (no closing content attribute): %q", line)
			}
			attrVal = rest[:end]
		} else if strings.HasPrefix(line, "<title>") {
			attrVal = strings.TrimSuffix(strings.TrimPrefix(line, "<title>"), "</title>")
		} else {
			continue
		}
		if strings.ContainsAny(attrVal, `"`) {
			t.Fatalf("raw quote inside attribute/title value: %q (line: %q)", attrVal, line)
		}
	}
}

func TestPermalink_OGImageOmittedWhenEmpty(t *testing.T) {
	c := testChannel()
	c.Feed.OGImage = ""
	it := model.Item{
		ItemKey:     "01J00000000000000NOIMAGE",
		Title:       "No Image Item",
		SummaryText: "This item's feed has no default OG image.",
		BodyHTML:    "<p>no image</p>",
		Link:        "https://example.com/items/01J00000000000000NOIMAGE",
		PublishedAt: time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}
	if strings.Contains(string(got), "og:image") {
		t.Fatalf("og:image tag present despite empty Feed.OGImage:\n%s", got)
	}
}

func TestPermalink_OGImagePresentWhenSet(t *testing.T) {
	c := testChannel()
	it := model.Item{
		ItemKey:     "01J0000000000000HASIMAGE",
		Title:       "Has Image Item",
		SummaryText: "This item's feed has a default OG image.",
		BodyHTML:    "<p>has image</p>",
		Link:        "https://example.com/items/01J0000000000000HASIMAGE",
		PublishedAt: time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}
	want := `<meta property="og:image" content="https://example.com/og/anime-news-daily.png">`
	if !strings.Contains(string(got), want) {
		t.Fatalf("expected og:image tag %q not found:\n%s", want, got)
	}
}

func TestPermalink_ExactlyOneTitleElement(t *testing.T) {
	c := testChannel()
	it := model.Item{
		ItemKey:     "01J000000000000000ONETTL",
		Title:       "Only One Title",
		SummaryText: "Summary for the one-title check.",
		BodyHTML:    "<p>body</p>",
		Link:        "https://example.com/items/01J000000000000000ONETTL",
		PublishedAt: time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC),
	}

	got, err := Permalink(c, it)
	if err != nil {
		t.Fatalf("Permalink returned error: %v", err)
	}
	if n := strings.Count(string(got), "<title>"); n != 1 {
		t.Fatalf("expected exactly one <title> element, got %d:\n%s", n, got)
	}
}
