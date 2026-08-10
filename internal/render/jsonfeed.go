package render

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// jsonFeedVersion is exact per the spec (PLAN.md §5.3): a typo here silently
// makes the document a different, unrecognized format rather than an invalid
// JSON Feed, so it is a named constant rather than an inline literal that
// could drift between call sites.
const jsonFeedVersion = "https://jsonfeed.org/version/1.1"

// jsonFeedDoc mirrors JSON Feed 1.1's top-level object. Field order here
// drives encoding/json's output order, which is what the golden-file tests
// pin down.
type jsonFeedDoc struct {
	Version     string           `json:"version"`
	Title       string           `json:"title"`
	HomePageURL string           `json:"home_page_url,omitempty"`
	FeedURL     string           `json:"feed_url,omitempty"`
	Description string           `json:"description,omitempty"`
	Language    string           `json:"language,omitempty"`
	Icon        string           `json:"icon,omitempty"`
	Authors     []jsonFeedAuthor `json:"authors,omitempty"`
	Items       []jsonFeedItem   `json:"items"`
}

// jsonFeedAuthor is 1.1's replacement for the deprecated singular `author`
// field (PLAN.md §5.3). We only ever populate `name`, but the type exists
// separately so a future url/avatar addition doesn't reshape callers.
type jsonFeedAuthor struct {
	Name string `json:"name,omitempty"`
}

// jsonFeedItem mirrors one JSON Feed item. `ID` is a Go string, which is what
// makes it marshal as a JSON string rather than a number — the spec requires
// item id to be a string even when the underlying identifier looks numeric,
// and TagURI already returns a string, so this falls out naturally rather
// than needing an explicit conversion.
type jsonFeedItem struct {
	ID            string   `json:"id"`
	URL           string   `json:"url,omitempty"`
	Title         string   `json:"title,omitempty"`
	ContentHTML   string   `json:"content_html"`
	Summary       string   `json:"summary,omitempty"`
	DatePublished string   `json:"date_published,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// JSONFeed renders c as a JSON Feed 1.1 document (PLAN.md §5.3).
//
// Items are sorted newest-first by PublishedAt independently of c.Items'
// input order: Slack's RSS/JSON consumers require strict newest-first
// ordering (§5.5), and a renderer that trusted caller ordering would be
// correct only as long as every caller remembered to sort first.
func JSONFeed(c model.Channel) ([]byte, error) {
	items := make([]model.Item, len(c.Items))
	copy(items, c.Items)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})

	doc := jsonFeedDoc{
		Version:     jsonFeedVersion,
		Title:       c.Feed.Title,
		HomePageURL: c.HTMLURL,
		FeedURL:     c.SelfURL,
		Description: c.Feed.Description,
		Language:    c.Feed.Language,
		Icon:        c.Feed.OGImage,
		Items:       make([]jsonFeedItem, 0, len(items)),
	}
	if c.Feed.Author != "" {
		doc.Authors = []jsonFeedAuthor{{Name: c.Feed.Author}}
	}

	for _, it := range items {
		// content_html is the only body we ever emit (never content_text), and
		// it is always populated: PLAN.md §5.3 requires at least one of the
		// two, and picking one consistently avoids a per-item branch that
		// could silently produce an item satisfying neither.
		doc.Items = append(doc.Items, jsonFeedItem{
			ID: TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey),
			// The answer, if any, lives only in BodyHTML (already assembled by
			// the caller with SummaryText first, per §5.1's ordering rule) —
			// never in Summary below, or the channel-level preview would spoil
			// a trivia question (§5.5).
			ContentHTML:   it.BodyHTML,
			URL:           it.Link,
			Title:         it.Title,
			Summary:       it.SummaryText,
			DatePublished: RFC3339(it.PublishedAt),
			Tags:          it.Tags,
		})
	}

	// A plain json.Marshal HTML-escapes '<', '>' and '&' (to < etc.) by
	// default, a Go safety default aimed at JSON embedded in <script> tags.
	// content_html is full of those characters, and escaping them would turn
	// "so golden files are readable" into a lie — so SetEscapeHTML(false)
	// here, deliberately, before indenting.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	// json.Encoder.Encode always appends a trailing newline; trimmed so the
	// output matches json.MarshalIndent's convention and golden-file tests
	// don't have to special-case a byte MarshalIndent would never produce.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
