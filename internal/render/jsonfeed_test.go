package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// jsonFeedTestChannel builds a small, deterministic two-item Channel. Item order in
// the returned Channel.Items is oldest-first — the reverse of the expected
// output — so tests exercise JSONFeed's own sort rather than trusting caller
// order (PLAN.md §5.5 requires strict newest-first regardless of input).
func jsonFeedTestChannel() model.Channel {
	itemA := model.Item{
		ItemKey:     "01K2XA1TRIVIAKEYA000000001",
		Title:       "Item A",
		SummaryText: "Summary A",
		BodyHTML:    "<p>Body A</p>",
		Link:        "https://anime.earlcameron.com/i/a",
		PublishedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		Tags:        []string{"anime", "review"},
	}
	itemB := model.Item{
		ItemKey:     "01K2XA1TRIVIAKEYB000000002",
		Title:       "Item B Trivia",
		SummaryText: "What is the answer?",
		BodyHTML:    "<p>What is the answer?</p>",
		AnswerHTML:  "Answer: 42",
		Link:        "https://anime.earlcameron.com/i/b",
		PublishedAt: time.Date(2026, 8, 9, 9, 30, 0, 0, time.UTC),
		Tags:        []string{"quiz"},
	}

	return model.Channel{
		Feed: model.Feed{
			Slug:        "trivia",
			Title:       "Trivia Feed",
			Description: "Daily trivia",
			Language:    "en-us",
			Author:      "AnimeFeedFlux",
			OGImage:     "https://anime.earlcameron.com/og.png",
		},
		SelfURL: "https://anime.earlcameron.com/feeds/trivia.json",
		HTMLURL: "https://anime.earlcameron.com/trivia",
		Host:    "anime.earlcameron.com",
		TagYear: 2026,
		Items:   []model.Item{itemB, itemA},
	}
}

const expectedJSONFeed = `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "Trivia Feed",
  "home_page_url": "https://anime.earlcameron.com/trivia",
  "feed_url": "https://anime.earlcameron.com/feeds/trivia.json",
  "description": "Daily trivia",
  "language": "en-us",
  "icon": "https://anime.earlcameron.com/og.png",
  "authors": [
    {
      "name": "AnimeFeedFlux"
    }
  ],
  "items": [
    {
      "id": "tag:anime.earlcameron.com,2026:trivia/01K2XA1TRIVIAKEYA000000001",
      "url": "https://anime.earlcameron.com/i/a",
      "title": "Item A",
      "content_html": "<p>Body A</p>",
      "summary": "Summary A",
      "date_published": "2026-08-10T12:00:00Z",
      "tags": [
        "anime",
        "review"
      ]
    },
    {
      "id": "tag:anime.earlcameron.com,2026:trivia/01K2XA1TRIVIAKEYB000000002",
      "url": "https://anime.earlcameron.com/i/b",
      "title": "Item B Trivia",
      "content_html": "<p>What is the answer?</p><hr class=\"spoiler-break\"/><p><strong>Answer:</strong> Answer: 42</p>",
      "summary": "What is the answer?",
      "date_published": "2026-08-09T09:30:00Z",
      "tags": [
        "quiz"
      ]
    }
  ]
}`

func TestJSONFeedExactBytes(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	if string(got) != expectedJSONFeed {
		t.Fatalf("JSONFeed output mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, expectedJSONFeed)
	}
}

func TestJSONFeedVersionExact(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	version, ok := doc["version"].(string)
	if !ok {
		t.Fatalf("version field is not a string: %#v", doc["version"])
	}
	// A typo here silently produces a document some readers won't recognize
	// as JSON Feed 1.1 at all, so the exact spec string is asserted verbatim.
	if version != "https://jsonfeed.org/version/1.1" {
		t.Errorf("version = %q, want %q", version, "https://jsonfeed.org/version/1.1")
	}
}

func TestJSONFeedItemIDIsString(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	items, ok := doc["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items missing or empty: %#v", doc["items"])
	}
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("item %d is not an object: %#v", i, raw)
		}
		if _, ok := item["id"].(string); !ok {
			t.Errorf("item %d id is not a JSON string: %#v", i, item["id"])
		}
	}
}

func TestJSONFeedNoDeprecatedAuthorField(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	// A raw substring check: "author" must not appear anywhere, including as
	// a key fragment of "authors", so this checks for the exact quoted key
	// rather than doing a plain strings.Contains(got, "author").
	if strings.Contains(string(got), `"author"`) {
		t.Errorf("output contains deprecated \"author\" field:\n%s", got)
	}
	if !strings.Contains(string(got), `"authors"`) {
		t.Errorf("output missing \"authors\" field:\n%s", got)
	}
}

func TestJSONFeedEveryItemHasContentHTML(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	items, ok := doc["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items missing or empty: %#v", doc["items"])
	}
	for i, raw := range items {
		item := raw.(map[string]any)
		html, ok := item["content_html"].(string)
		if !ok || html == "" {
			t.Errorf("item %d missing non-empty content_html: %#v", i, item["content_html"])
		}
	}
}

// TestJSONFeedTriviaAnswerNotInSummary is the spoiler guard from PLAN.md
// §5.5: the channel-level summary must never carry the trivia answer, only
// content_html may.
func TestJSONFeedTriviaAnswerNotInSummary(t *testing.T) {
	ch := jsonFeedTestChannel()
	var trivia model.Item
	for _, it := range ch.Items {
		if it.HasAnswer() {
			trivia = it
		}
	}
	if trivia.ItemKey == "" {
		t.Fatal("test fixture has no trivia item with an answer")
	}

	got, err := JSONFeed(ch)
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	items := doc["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		id, _ := item["id"].(string)
		if !strings.HasSuffix(id, trivia.ItemKey) {
			continue
		}
		summary, _ := item["summary"].(string)
		if strings.Contains(summary, trivia.AnswerHTML) {
			t.Errorf("summary leaks the trivia answer: %q", summary)
		}
		html, _ := item["content_html"].(string)
		if !strings.Contains(html, trivia.AnswerHTML) {
			t.Errorf("content_html missing the trivia answer: %q", html)
		}
	}
}

func TestJSONFeedRoundTripsThroughUnmarshal(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("output does not round-trip through json.Unmarshal: %v", err)
	}
}

// TestJSONFeedItemsNewestFirst feeds JSONFeed items already reordered (via
// jsonFeedTestChannel, oldest-first) and asserts the output corrects that: Slack's
// RSS/JSON consumer requires strict newest-first ordering and drops
// out-of-sequence items (PLAN.md §5.5).
func TestJSONFeedItemsNewestFirst(t *testing.T) {
	got, err := JSONFeed(jsonFeedTestChannel())
	if err != nil {
		t.Fatalf("JSONFeed returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	items := doc["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	first := items[0].(map[string]any)["date_published"].(string)
	second := items[1].(map[string]any)["date_published"].(string)
	firstTime, err := time.Parse(time.RFC3339, first)
	if err != nil {
		t.Fatalf("parsing first date_published: %v", err)
	}
	secondTime, err := time.Parse(time.RFC3339, second)
	if err != nil {
		t.Fatalf("parsing second date_published: %v", err)
	}
	if !firstTime.After(secondTime) {
		t.Errorf("items not newest-first: %s is not after %s", first, second)
	}
}
