package render

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// RSS renders c as a complete RSS 2.0 document (PLAN.md §5.1).
//
// It builds the document by writing escaped/CDATA-wrapped strings directly
// to a buffer rather than going through encoding/xml struct marshalling:
// marshalling has no notion of CDATA and no way to force the exact hex
// character-reference escaping the RSS Advisory Board's Best Practices
// Profile expects, so a struct-tagged approach would silently drift from
// spec on both fronts.
func RSS(c model.Channel) ([]byte, error) {
	var b bytes.Buffer

	// No BOM: writing the declaration as the very first bytes, with no
	// leading anything, is what guarantees that.
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:content="http://purl.org/rss/1.0/modules/content/">` + "\n")
	b.WriteString("  <channel>\n")

	writeElem(&b, "    ", "title", c.Feed.Title)
	writeElem(&b, "    ", "link", c.HTMLURL)
	writeElem(&b, "    ", "description", c.Feed.Description)
	writeElem(&b, "    ", "language", c.Feed.Language)
	// pubDate uses the newest item's timestamp (falling back to BuildTime
	// when the feed has no items yet) rather than always being BuildTime:
	// readers that treat channel pubDate as "when did the content change"
	// would otherwise see it tick on every render even when nothing new
	// was published.
	writeElem(&b, "    ", "pubDate", RFC822(c.NewestPublished()))
	writeElem(&b, "    ", "lastBuildDate", RFC822(c.BuildTime))
	writeElem(&b, "    ", "generator", c.Generator)
	writeElem(&b, "    ", "docs", c.DocsURL)
	writeElem(&b, "    ", "ttl", strconv.Itoa(c.Feed.TTLMinutes))

	b.WriteString(`    <atom:link rel="self" type="application/rss+xml" href="`)
	b.WriteString(EscapeText(c.SelfURL))
	b.WriteString("\"/>\n")

	// Newest-first, always — Slack (and the RSS Advisory Board's profile)
	// require it, and the caller's slice order is not trusted (§5.5): a
	// copy is sorted here so RSS never mutates the slice a caller may still
	// be holding a reference to elsewhere (e.g. also feeding it to the Atom
	// or JSON Feed renderers in whatever order it arrived in).
	items := make([]model.Item, len(c.Items))
	copy(items, c.Items)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})

	for _, it := range items {
		writeItem(&b, c, it)
	}

	b.WriteString("  </channel>\n")
	b.WriteString("</rss>\n")

	return b.Bytes(), nil
}

// writeElem writes a plain-text child element, hex-escaped. Every
// channel-level text field routes through here so there is exactly one
// place that decides plain-text elements get EscapeText rather than raw
// interpolation.
func writeElem(b *bytes.Buffer, indent, name, value string) {
	b.WriteString(indent)
	b.WriteByte('<')
	b.WriteString(name)
	b.WriteByte('>')
	b.WriteString(EscapeText(value))
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}

// writeItem writes one <item>, in the element order the Best Practices
// Profile calls out: description before content:encoded (§5.1), and guid
// carrying an explicit isPermaLink="false" so nothing relies on the spec's
// "true" default (§5.1 again — the whole reason this function exists rather
// than a struct tag).
func writeItem(b *bytes.Buffer, c model.Channel, it model.Item) {
	b.WriteString("    <item>\n")

	writeElem(b, "      ", "title", it.Title)
	writeElem(b, "      ", "link", it.Link)

	// description is the PLAIN-TEXT summary and NEVER the HTML body or the
	// trivia answer — Slack renders exactly this field verbatim with no
	// markup support, so leaking either here spoils the channel preview
	// (§5.5). BodyHTML/AnswerHTML only ever reach content:encoded below.
	writeElem(b, "      ", "description", it.SummaryText)

	b.WriteString("      <content:encoded>")
	b.WriteString(CDATA(contentEncodedBody(it)))
	b.WriteString("</content:encoded>\n")

	guid := TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey)
	b.WriteString(`      <guid isPermaLink="false">`)
	b.WriteString(EscapeText(guid))
	b.WriteString("</guid>\n")

	writeElem(b, "      ", "pubDate", RFC822(it.PublishedAt))

	b.WriteString("    </item>\n")
}

// contentEncodedBody is the full item HTML for content:encoded: the body,
// plus — for trivia items — the answer appended after a spoiler break.
// Appending it here, inside the one field that already carries raw HTML
// (rather than as a second sibling element), is what keeps the answer out
// of description/og:description by construction: there is no code path
// that copies content:encoded's contents back into a plain-text field.
func contentEncodedBody(it model.Item) string {
	if !it.HasAnswer() {
		return it.BodyHTML
	}
	return it.BodyHTML + `<hr class="spoiler-break"/><p><strong>Answer:</strong> ` + it.AnswerHTML + `</p>`
}
