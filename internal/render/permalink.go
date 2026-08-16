package render

import (
	"bytes"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// Permalink renders the item's own HTML page (PLAN.md §5.5, §6:
// GET /items/{item_key}). Slack and most other readers do not fetch the RSS
// entry at all when deciding what to show in a channel — they unfurl this
// URL and show whatever OpenGraph tags it carries. That makes the tags the
// product here, not decoration bolted onto a human-facing page.
//
// Like RSS, this writes directly to a buffer rather than going through a
// template/html or html/template document tree: the one property that
// matters most — BodyHTML/AnswerHTML must land in the page completely
// unescaped, while every other field must be escaped when it lands in an
// attribute — is easiest to get right, and easiest to see is right, as a
// short sequence of explicit writes rather than a set of template
// pipelines a reader has to trace to know which fields are `html/template`
// safe.
//
// The cost of that choice is that this file has NO safety net: writing an
// unsanitized field here publishes it verbatim, and nothing downstream will
// catch it. AnswerHTML was exactly that bug — every writer into model.Item
// sanitized BodyHTML and none of them sanitized AnswerHTML, while both are
// written raw below. Before adding a field to this page, confirm which of the
// two categories above it is in and that every writer agrees.
func Permalink(c model.Channel, it model.Item) ([]byte, error) {
	var b bytes.Buffer

	lang := c.Feed.Language
	if lang == "" {
		lang = "en"
	}

	// The plain-text summary is what goes in every reader-facing surface
	// that isn't the article body proper: <title>, meta description, and
	// every og:/twitter: field that Slack's unfurl reads. This is not a
	// special case for trivia — it's the only field used here, ever. Model
	// guarantees SummaryText never contains AnswerHTML (§5.5), so the
	// spoiler-safety of this page falls out of using the right field
	// everywhere rather than out of any trivia-specific branch below.
	summary := EscapeText(it.RenderCardText())
	title := EscapeText(it.Title)
	published := RFC3339(it.PublishedAt)

	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="`)
	b.WriteString(EscapeText(lang))
	b.WriteString("\">\n")
	b.WriteString("<head>\n")
	b.WriteString(`<meta charset="utf-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString("<title>")
	b.WriteString(title)
	b.WriteString("</title>\n")
	writeMeta(&b, "name", "description", summary)
	writeMeta(&b, "property", "og:title", title)
	writeMeta(&b, "property", "og:description", summary)
	// Omitted rather than emitted empty: an empty og:image is worse than no
	// tag at all — some unfurlers render a broken-image placeholder for a
	// present-but-empty content attribute instead of falling back the way
	// they would for a missing tag entirely.
	if c.Feed.OGImage != "" {
		writeMeta(&b, "property", "og:image", EscapeText(c.Feed.OGImage))
	}
	writeMeta(&b, "property", "og:type", "article")
	writeMeta(&b, "property", "og:url", EscapeText(it.Link))
	writeMeta(&b, "property", "article:published_time", published)
	writeMeta(&b, "name", "twitter:card", "summary_large_image")
	b.WriteString("</head>\n")
	b.WriteString("<body>\n")
	b.WriteString("<article>\n")
	b.WriteString("<h1>")
	b.WriteString(title)
	b.WriteString("</h1>\n")
	b.WriteString(`<time datetime="`)
	b.WriteString(published)
	b.WriteString(`">`)
	b.WriteString(published)
	b.WriteString("</time>\n")

	// Raw, deliberately: BodyHTML has already been sanitized by the time it
	// reaches a renderer, the same trust boundary content:encoded in rss.go
	// relies on. Escaping it here would double-encode legitimate markup and
	// defeat the point of shipping HTML at all.
	//
	// "Already sanitized" is a claim about TWO ingress paths, not one, and it
	// was only ever true of the first. internal/generate/contract.go sanitizes
	// what the generation pipeline produces (PLAN.md §9); internal/rpc/item.go
	// is an independent door into the same table (Create, Update,
	// PromoteSample, PublishCorrection) and did no sanitizing at all. Both do
	// now. If a third writer to model.Item ever appears, it owes this line the
	// same guarantee — this renderer cannot check it and will not try.
	// RenderPageHTML prefers stage 2's reading-page variant, which went
	// through the same sanitizer (formats.go's validateFormats) as both
	// ingress paths above, so the trust boundary is unchanged.
	b.WriteString(it.RenderPageHTML())
	b.WriteString("\n")

	if it.HasAnswer() {
		// The answer lives on the page — readers get the full experience —
		// but only inside a closed <details>, so the unfurl (which reads
		// meta tags, never body markup) can never surface it. This is the
		// only place AnswerHTML is written; og:description/meta description
		// above are built exclusively from SummaryText and never see it.
		b.WriteString("<details>\n")
		b.WriteString("<summary>Reveal the answer</summary>\n")
		b.WriteString(it.AnswerHTML)
		b.WriteString("\n</details>\n")
	}

	b.WriteString("</article>\n")
	b.WriteString("</body>\n")
	b.WriteString("</html>\n")

	return b.Bytes(), nil
}

// writeMeta writes one <meta attr="key" content="value"> tag. value must
// already be escaped by the caller — this function does not escape it a
// second time — because callers sometimes pass a literal constant (e.g.
// "article") that must not be mistaken for needing escaping, and a helper
// that escaped unconditionally would hide that distinction.
func writeMeta(b *bytes.Buffer, attr, key, value string) {
	b.WriteString(`<meta `)
	b.WriteString(attr)
	b.WriteString(`="`)
	b.WriteString(key)
	b.WriteString(`" content="`)
	b.WriteString(value)
	b.WriteString("\">\n")
}
