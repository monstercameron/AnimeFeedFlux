package render

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// Atom renders c as an Atom 1.0 document (RFC 4287, PLAN.md §5.2).
//
// It builds the document by hand with a bytes.Buffer rather than
// encoding/xml struct marshalling: the MUST-level shape here (exactly one
// id/title/updated at two different levels, an id that has to be
// byte-identical to a string built elsewhere by TagURI, a summary/content
// split that must never let the trivia answer leak into summary) is easier
// to get right, and easier to verify by inspection, as a fixed sequence of
// Write calls than as struct tags a reader has to cross-reference against
// the spec.
func Atom(c model.Channel) ([]byte, error) {
	var b bytes.Buffer

	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">` + "\n")

	b.WriteString("  <id>" + EscapeText(feedID(c)) + "</id>\n")
	b.WriteString("  <title>" + EscapeText(c.Feed.Title) + "</title>\n")
	b.WriteString("  <updated>" + RFC3339(c.BuildTime) + "</updated>\n")

	// RFC 4287 §4.1.1: feed MUST have an author unless every entry carries
	// its own. We never emit a per-entry author, so this one is load-bearing.
	// Falling back to the feed title keeps the MUST satisfied even for a feed
	// whose config never set an explicit author.
	author := c.Feed.Author
	if author == "" {
		author = c.Feed.Title
	}
	b.WriteString("  <author>\n")
	b.WriteString("    <name>" + EscapeText(author) + "</name>\n")
	b.WriteString("  </author>\n")

	if c.SelfURL != "" {
		b.WriteString(`  <link rel="self" href="` + EscapeText(c.SelfURL) + `"/>` + "\n")
	}
	if c.HTMLURL != "" {
		b.WriteString(`  <link rel="alternate" href="` + EscapeText(c.HTMLURL) + `"/>` + "\n")
	}
	if c.Feed.Description != "" {
		b.WriteString("  <subtitle>" + EscapeText(c.Feed.Description) + "</subtitle>\n")
	}
	if c.Feed.Copyright != "" {
		b.WriteString("  <rights>" + EscapeText(c.Feed.Copyright) + "</rights>\n")
	}
	if c.Generator != "" {
		b.WriteString("  <generator>" + EscapeText(c.Generator) + "</generator>\n")
	}

	for _, it := range newestFirst(c.Items) {
		writeAtomEntry(&b, c, it)
	}

	b.WriteString("</feed>\n")

	return b.Bytes(), nil
}

// feedID builds the feed-level id. It deliberately does not call TagURI:
// TagURI's shape is "authority:slug/itemKey" because every caller needs an
// item component, but the feed itself has no item to hang off — appending
// an empty itemKey would leave a dangling trailing slash on every feed id.
// It still reuses stripHostDecoration so the same host-normalization rule
// (scheme and trailing slash stripped) applies to both the feed id and every
// entry id built from the same Channel.
func feedID(c model.Channel) string {
	return "tag:" + stripHostDecoration(c.Host) + "," + strconv.Itoa(c.TagYear) + ":" + c.Feed.Slug
}

// newestFirst returns items sorted strictly newest-first by PublishedAt,
// without mutating the caller's slice. §5.5 makes this a hard requirement
// for Slack specifically (out-of-sequence items are silently mishandled),
// not just a cosmetic default, so callers must not be able to rely on
// Channel.Items already being sorted.
func newestFirst(items []model.Item) []model.Item {
	sorted := make([]model.Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].PublishedAt.After(sorted[j].PublishedAt)
	})
	return sorted
}

// writeAtomEntry writes one <entry>. id is TagURI(...) with the exact same
// arguments the RSS renderer uses for guid, so the two stay byte-identical
// by construction (§5.2: Atom consumers compare ids character-by-character,
// case-sensitively).
func writeAtomEntry(b *bytes.Buffer, c model.Channel, it model.Item) {
	id := TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey)

	b.WriteString("  <entry>\n")
	b.WriteString("    <id>" + EscapeText(id) + "</id>\n")
	b.WriteString("    <title>" + EscapeText(it.Title) + "</title>\n")
	b.WriteString("    <updated>" + RFC3339(it.PublishedAt) + "</updated>\n")
	// §5.2: an entry with no content MUST carry link rel="alternate". We always
	// emit content below, so the link is optional — and it is OMITTED when the
	// item has no URL rather than emitted with href="".
	//
	// RFC 4287 requires href to be an IRI reference, and the empty string is
	// not one. An empty href is also worse than no link at all in practice: a
	// reader that follows it navigates to the feed document itself. Skipping it
	// keeps the entry valid, because content is present.
	if it.Link != "" {
		b.WriteString(`    <link rel="alternate" href="` + EscapeText(it.Link) + `"/>` + "\n")
	}
	b.WriteString(`    <summary type="text">` + EscapeText(it.SummaryText) + "</summary>\n")

	// type="html" means the value is escaped character data whose content
	// happens to be HTML markup, not raw XML — so this is EscapeText, the
	// same plain-text escaper as everywhere else, not CDATA (that's the RSS
	// content:encoded convention, a different spec with different rules).
	// The underlying HTML string itself — including, for trivia items, the
	// answer after its spoiler-break marker — comes from itemBodyWithAnswer,
	// the same helper RSS's content:encoded and JSON Feed's content_html use,
	// so the three formats never disagree about what an item's body says.
	// SummaryText is what Slack and every plain-text consumer shows, and
	// leaking the answer there would spoil the question before a reader
	// clicks through (§5.5) — that stays untouched here.
	b.WriteString(`    <content type="html">` + EscapeText(itemBodyWithAnswer(it)) + "</content>\n")

	b.WriteString("  </entry>\n")
}
