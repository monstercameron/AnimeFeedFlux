package render

import (
	"bytes"
	"sort"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// Index renders the HTML page served at "/": every enabled feed, its
// description, and subscribe links for all three formats (PLAN.md §6,
// §14.1).
//
// It is deliberately not a debug dump: this is the page a human pastes into
// Slack — or opens directly — when they've forgotten a slug, so every feed
// needs to be readable and every link clickable, not just present in the
// markup.
func Index(feeds []model.Feed, baseURL string, generator string) ([]byte, error) {
	var b bytes.Buffer

	// Sorted by Title, not slug or table order: map/slice order isn't stable
	// across builds, and an index whose byte stream changes when nothing
	// actually changed defeats caching (ETag/Last-Modified) for no reason.
	// A copy is sorted here so Index never mutates the slice a caller may
	// still be holding a reference to elsewhere.
	sorted := make([]model.Feed, 0, len(feeds))
	for _, f := range feeds {
		// Disabled feeds keep serving their last-built content at their own
		// URL (§6) but have no place on the index — an index entry implies
		// "you can subscribe to this now", which isn't true for something
		// generation has stopped touching. Aggregates ARE included: they are
		// ordinary subscribable URLs, just with no generator of their own
		// (§14.2).
		if !f.Enabled {
			continue
		}
		sorted = append(sorted, f)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Title < sorted[j].Title
	})

	b.WriteString("<!doctype html>\n")
	b.WriteString(`<html lang="en">` + "\n")
	b.WriteString("<head>\n")
	b.WriteString(`  <meta charset="utf-8">` + "\n")
	b.WriteString(`  <meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString("  <title>Feeds</title>\n")
	b.WriteString(`  <meta name="generator" content="`)
	b.WriteString(EscapeText(generator))
	b.WriteString("\">\n")

	// Autodiscovery: one <link rel="alternate"> per feed per format, so
	// pasting the *site* URL (not a feed URL) into a reader is enough for it
	// to offer a subscribe — that's the whole point of this element existing
	// in <head> rather than only as a body link.
	for _, f := range sorted {
		writeAlternate(&b, baseURL, f, "xml", "application/rss+xml")
		writeAlternate(&b, baseURL, f, "atom", "application/atom+xml")
		writeAlternate(&b, baseURL, f, "json", "application/feed+json")
	}

	b.WriteString("</head>\n")
	b.WriteString("<body>\n")
	b.WriteString("  <h1>Feeds</h1>\n")

	if len(sorted) == 0 {
		// An empty <ul> reads as broken/loading; a sentence reads as
		// intentional — there just aren't any enabled feeds right now.
		b.WriteString("  <p>No feeds are currently enabled.</p>\n")
	} else {
		b.WriteString("  <ul>\n")
		for _, f := range sorted {
			writeFeedEntry(&b, baseURL, f)
		}
		b.WriteString("  </ul>\n")
	}

	b.WriteString("</body>\n")
	b.WriteString("</html>\n")

	return b.Bytes(), nil
}

// writeAlternate writes one <link rel="alternate"> autodiscovery tag for
// feed f in the given extension/MIME type.
func writeAlternate(b *bytes.Buffer, baseURL string, f model.Feed, ext, mimeType string) {
	b.WriteString(`  <link rel="alternate" type="`)
	b.WriteString(mimeType)
	b.WriteString(`" title="`)
	b.WriteString(EscapeText(f.Title))
	b.WriteString(`" href="`)
	b.WriteString(EscapeText(feedURL(baseURL, f.Slug, ext)))
	b.WriteString("\">\n")
}

// writeFeedEntry writes one feed's list item: title, description, and its
// three subscribe links.
func writeFeedEntry(b *bytes.Buffer, baseURL string, f model.Feed) {
	b.WriteString("    <li>\n")
	b.WriteString("      <h2>")
	b.WriteString(EscapeText(f.Title))
	b.WriteString("</h2>\n")
	b.WriteString("      <p>")
	b.WriteString(EscapeText(f.Description))
	b.WriteString("</p>\n")

	b.WriteString("      <p>\n")
	writeSubscribeLink(b, baseURL, f, "xml", "RSS")
	writeSubscribeLink(b, baseURL, f, "atom", "Atom")
	writeSubscribeLink(b, baseURL, f, "json", "JSON Feed")
	b.WriteString(`        <a href="`)
	b.WriteString(EscapeText(baseURL + "/embed/" + f.Slug))
	b.WriteString(`">Embed</a>` + "\n")
	b.WriteString("      </p>\n")

	// The iframe markup, spelled out rather than described. This is the only
	// surface that tells anyone the embed route exists, and "there is an
	// embed at /embed/{slug}" is not the same as knowing the attributes that
	// make it render at a sensible size — a reader who has to guess at those
	// will guess wrong and conclude the feature is broken.
	b.WriteString("      <pre><code>")
	b.WriteString(EscapeText(EmbedSnippet(baseURL, f.Slug)))
	b.WriteString("</code></pre>\n")

	b.WriteString("    </li>\n")
}

// writeSubscribeLink writes one clickable subscribe link for feed f.
func writeSubscribeLink(b *bytes.Buffer, baseURL string, f model.Feed, ext, label string) {
	url := feedURL(baseURL, f.Slug, ext)
	b.WriteString(`        <a href="`)
	b.WriteString(EscapeText(url))
	b.WriteString(`">`)
	b.WriteString(EscapeText(label))
	b.WriteString("</a>\n")
}

// feedURL builds a feed's absolute subscribe URL from baseURL, its slug, and
// a format extension ("xml", "atom", "json"). One place decides this shape
// so the <head> autodiscovery tags and the body's subscribe links can never
// disagree on it.
func feedURL(baseURL, slug, ext string) string {
	return baseURL + "/feeds/" + slug + "." + ext
}
