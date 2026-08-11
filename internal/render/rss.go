// Package render serializes an internal/model.Channel into the formats
// subscribers and Slack actually consume: RSS 2.0, Atom 1.0, JSON Feed 1.1,
// the "/" index page, and a per-item permalink page (PLAN.md §5, §6, §14).
//
// It deliberately does NOT sanitize. Item and feed text is assumed already
// safe HTML/plain-text by the time it reaches this package — that is
// internal/sanitize's job, run once at the LLM/upstream boundary before
// storage. Re-sanitizing here would mean every serializer re-implements (and
// can independently drift from) the allowlist, and it would hide a
// sanitizer bug behind a second layer instead of surfacing it. What this
// package does own is XML/JSON *escaping* — turning already-safe text into
// well-formed output — which is a distinct, narrower job from stripping
// unsafe markup.
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

	// Omitted rather than emitted empty when the item has no URL. Every RSS
	// item element is optional provided title or description is present (§5.1),
	// and an empty <link></link> is worse than none: a reader that follows it
	// resolves the empty string against the feed URL and navigates to the feed.
	if it.Link != "" {
		writeElem(b, "      ", "link", it.Link)
	}

	// description is the PLAIN-TEXT summary and NEVER the HTML body or the
	// trivia answer — Slack renders exactly this field verbatim with no
	// markup support, so leaking either here spoils the channel preview
	// (§5.5). BodyHTML/AnswerHTML only ever reach content:encoded below.
	writeElem(b, "      ", "description", it.SummaryText)

	b.WriteString("      <content:encoded>")
	b.WriteString(CDATA(itemBodyWithAnswer(it)))
	b.WriteString("</content:encoded>\n")

	guid := TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey)
	b.WriteString(`      <guid isPermaLink="false">`)
	b.WriteString(EscapeText(guid))
	b.WriteString("</guid>\n")

	writeElem(b, "      ", "pubDate", RFC822(it.PublishedAt))

	b.WriteString("    </item>\n")
}

// itemBodyWithAnswer is the full item HTML body: the item's BodyHTML, plus —
// for trivia items — the answer appended after a spoiler-break marker.
//
// This is the ONE place that decides what "the item's HTML content" is. RSS
// (content:encoded), Atom (content) and JSON Feed (content_html) all render
// the same underlying document for the same item; a subscriber who happens
// to pick one format's URL over another's must never see different content
// for it. Each renderer differs only in *how* it serializes this same
// string (CDATA vs escaped character data vs a JSON string, per §5.1/§5.2/
// §5.3) — never in what the string says. Appending the answer here, inside
// the one field that already carries raw HTML (rather than as a second
// sibling element), is also what keeps the answer out of
// description/summary/og:description by construction: there is no code path
// that copies this field's contents back into a plain-text field.
func itemBodyWithAnswer(it model.Item) string {
	if !it.HasAnswer() {
		return it.BodyHTML
	}
	return it.BodyHTML + `<hr class="spoiler-break"/><p><strong>Answer:</strong> ` + it.AnswerHTML + `</p>`
}
