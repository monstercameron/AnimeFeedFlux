package render

import (
	"bytes"
	"net/url"
	"slices"
	"sort"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// Embed renders the standalone HTML document served at GET /embed/{slug}
// (PLAN.md §6): the feed's newest items as a short list, styled, complete,
// and meant to be pulled into somebody else's page inside an <iframe>.
//
// # Why a whole document rather than a script or a fragment
//
// The obvious alternative — ship an embed.js that injects nodes into the
// host page — was rejected. It would put an executable asset on the public
// plane and make this project a script vendor for pages it does not control:
// a sanitiser regression would then be an XSS in someone else's origin
// rather than in ours, and the file could never change shape again without
// breaking pages already carrying it. An iframe document has none of that:
// it renders in its own origin, carries its own CSS, needs no CORS, and the
// publish plane stays what §2 says it is — a fixed set of read-only
// documents.
//
// # The one rule that matters here
//
// This page renders SummaryText and nothing else from the item body. Not
// BodyHTML, and emphatically not AnswerHTML. §5.5 makes SummaryText the
// only field guaranteed to be answer-free; the permalink page relies on the
// same guarantee for og:description. An embed that reached for BodyHTML "to
// show a little more" would spoil every trivia answer on every page that
// embedded it, silently and everywhere at once. There is deliberately no
// option to render more.
func Embed(c model.Channel, o EmbedOptions) ([]byte, error) {
	o = o.resolve()

	lang := c.Feed.Language
	if lang == "" {
		lang = "en"
	}
	title := EscapeText(c.Feed.Title)

	// Newest-first and capped here rather than trusted from the caller, for
	// the reason RSS gives at the same point: the caller's slice order is not
	// part of any contract, and ListItems is documented as returning the
	// window "in any order". A copy is sorted so Embed never mutates a slice
	// the caller may still be feeding to another renderer.
	items := make([]model.Item, len(c.Items))
	copy(items, c.Items)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if len(items) > o.Count {
		items = items[:o.Count]
	}

	var b bytes.Buffer

	b.WriteString("<!doctype html>\n")
	b.WriteString(`<html lang="`)
	b.WriteString(EscapeText(lang))
	b.WriteString("\">\n")
	b.WriteString("<head>\n")
	b.WriteString(`<meta charset="utf-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	// noindex, because this document duplicates content that already has a
	// canonical home at the permalink and the feed. It is a component, not a
	// page, and letting search engines index one copy per embedding site is
	// how a feed competes with itself.
	b.WriteString(`<meta name="robots" content="noindex">` + "\n")
	b.WriteString("<title>")
	b.WriteString(title)
	b.WriteString("</title>\n")
	writeMeta(&b, "name", "generator", EscapeText(c.Generator))
	b.WriteString("<style>\n")
	b.WriteString(embedCSS(o.Theme))
	b.WriteString("</style>\n")
	b.WriteString("</head>\n")

	b.WriteString("<body>\n")
	b.WriteString(`<section class="aff">` + "\n")

	b.WriteString(`<h1 class="aff-feed">`)
	writeEmbedLink(&b, c.HTMLURL, title)
	b.WriteString("</h1>\n")

	if len(items) == 0 {
		// A sentence, not an empty list: an empty <ol> renders as a blank
		// panel on the host's page and reads as broken rather than as "this
		// feed has not published yet".
		b.WriteString(`<p class="aff-empty">No items yet.</p>` + "\n")
	} else {
		b.WriteString(`<ol class="aff-items">` + "\n")
		for _, it := range items {
			writeEmbedItem(&b, it)
		}
		b.WriteString("</ol>\n")
	}

	b.WriteString(`<p class="aff-sub">`)
	writeEmbedLink(&b, c.SelfURL, "Subscribe by RSS")
	b.WriteString("</p>\n")

	b.WriteString("</section>\n")
	b.WriteString("</body>\n")
	b.WriteString("</html>\n")

	return b.Bytes(), nil
}

// EmbedOptions are the two knobs an embedding page may set, as a closed set
// of values rather than free-form input.
//
// Bounded on purpose. Every distinct combination is a separate render-cache
// entry, and the cache is already the thing standing between a popular
// embed and SQLite; free-form counts and colours would let any visitor mint
// unbounded entries by varying a query string, evicting the feed documents
// this plane exists to serve. Three counts and three themes is nine
// documents per feed, worst case.
type EmbedOptions struct {
	// Count is how many items to show: one of EmbedCounts. Zero means
	// DefaultEmbedCount.
	Count int
	// Theme is "light", "dark" or "auto". Empty means DefaultEmbedTheme.
	Theme string
}

// EmbedCounts and EmbedThemes are the accepted values. The publish handler
// rejects anything else rather than rounding it to the nearest permitted
// value, so one URL means one document and there is nothing to guess about
// which entry a request hit.
var (
	EmbedCounts = []int{5, 10, 20}
	EmbedThemes = []string{"light", "dark", "auto"}
)

const (
	DefaultEmbedCount = 10
	// "auto" follows the embedding reader's own prefers-color-scheme, which
	// is right far more often than either fixed choice: an embed's job is to
	// not look like a foreign object in the page around it.
	DefaultEmbedTheme = "auto"
)

// ValidEmbedCount and ValidEmbedTheme report whether a caller-supplied value
// is one this renderer will honour. They live here, next to the values they
// check, so the HTTP layer cannot drift from the renderer about what is
// accepted.
func ValidEmbedCount(n int) bool { return slices.Contains(EmbedCounts, n) }

func ValidEmbedTheme(s string) bool { return slices.Contains(EmbedThemes, s) }

// resolve fills in defaults and clamps anything invalid that reached this
// far. The handler validates first and 404s on bad input; this is the second
// line, so a future caller (a test, the e2e harness, a CLI preview) cannot
// produce a document with a count of -1 or a theme that matches no CSS.
func (o EmbedOptions) resolve() EmbedOptions {
	if !ValidEmbedCount(o.Count) {
		o.Count = DefaultEmbedCount
	}
	if !ValidEmbedTheme(o.Theme) {
		o.Theme = DefaultEmbedTheme
	}
	return o
}

// writeEmbedItem writes one item: a timestamp caption, the title, and the
// summary.
//
// The timestamp leads the item but no longer occupies a column of its own.
// It was a fixed rail on the argument that §5.5 makes PublishedAt the item's
// real identity — true of the data, wrong about the reader. A feed publishes
// a run at a time, so consecutive items carry the same date by construction,
// and the rail spent a fifth of the width repeating it beside every title.
func writeEmbedItem(b *bytes.Buffer, it model.Item) {
	b.WriteString(`<li class="aff-item">` + "\n")

	// Omitted entirely when there is no date, rather than printed as the
	// zero time. A missing published_at is always a bug somewhere upstream,
	// but "1 Jan 0001, 00:00 UTC" is the worst available way to report it:
	// it is indistinguishable from real content, so the page looks broken
	// instead of looking like a page with one fact missing. This is also why
	// the stamp is a caption rather than a fixed column — a column leaves a
	// visible hole when its content is absent, and a caption just is not
	// there.
	if !it.PublishedAt.IsZero() {
		b.WriteString(`<time class="aff-when" datetime="`)
		b.WriteString(RFC3339(it.PublishedAt))
		b.WriteString(`">`)
		b.WriteString(embedStamp(it.PublishedAt))
		b.WriteString("</time>\n")
	}

	b.WriteString(`<span class="aff-title">`)
	writeEmbedLink(b, it.Link, EscapeText(it.Title))
	b.WriteString("</span>\n")

	// RenderEmbedText prefers stage 2's widget-optimized one-liner (§9,
	// two-stage generation) and falls back to SummaryText — both plain
	// text, both under the same never-the-answer guarantee this page's
	// header requires.
	if summary := it.RenderEmbedText(); summary != "" {
		b.WriteString(`<p class="aff-summary">`)
		b.WriteString(EscapeText(summary))
		b.WriteString("</p>\n")
	}

	b.WriteString("</li>\n")
}

// writeEmbedLink writes text as a link to href, or as plain text when href
// is missing or is not a scheme this will link to. label must already be
// escaped; href must not be (it is escaped here).
//
// Only http and https become anchors. An item's Link is meant to be either
// our own permalink or a URL verified against the fetched corpus (§9.6), so
// nothing else should ever appear here — but this document is the first
// thing this project renders INTO a page belonging to somebody else, and
// "the pipeline guarantees it" is a weaker property than "this function
// cannot emit it". A javascript: or data: href is inert under the embed's
// CSP anyway; refusing to write one at all means that safety does not depend
// on a header a future edit could relax. Target _blank because the document
// is expected to be framed: a link that navigated the iframe would replace
// the widget with an article inside a box the size of a widget.
func writeEmbedLink(b *bytes.Buffer, href, label string) {
	if !linkableURL(href) {
		b.WriteString(label)
		return
	}
	b.WriteString(`<a href="`)
	b.WriteString(EscapeText(href))
	b.WriteString(`" target="_blank" rel="noopener noreferrer">`)
	b.WriteString(label)
	b.WriteString("</a>")
}

func linkableURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// embedStamp is the caption above each item's title: one short line, not the
// two-line block this used to be.
//
// The time is there because a day-only label labels a whole day's output
// identically, and a feed that runs twice a day then looks like it ran once.
// Minute precision, not second: generation spaces a single run's items
// exactly one second apart (internal/generate/runner.go) purely so §5.5's
// uniqueness rule holds, so items within one run legitimately share a
// visible stamp — they were published together. Rendering the seconds would
// dress that mechanical spacing up as information the reader can use.
//
// UTC, and labelled as such, rather than the feed's own timezone: the <time>
// element carries the exact instant in RFC 3339 for anything that parses, and
// rendering the label in Feed.Timezone would make the document's bytes depend
// on a per-feed setting for no reader-visible gain — every cached copy would
// then have to be reasoned about twice. An unlabelled local-looking time
// would be worse than either, because it would be wrong for most readers
// without ever looking wrong.
func embedStamp(t time.Time) string {
	u := t.UTC()
	return u.Format("2 Jan 2006") + " · " + u.Format("15:04") + " UTC"
}

// embedCSS returns the stylesheet for one theme.
//
// Everything is inline and self-contained — no font file, no stylesheet, no
// image is fetched. That is partly the embed's Content-Security-Policy
// (default-src 'none'), and partly that a widget which blocks on a third
// party's font CDN is a widget that makes its host's page slower.
//
// The type stack is the system UI stack for prose and a monospace stack for
// the timestamps, so the widget picks up the reader's platform rather than
// importing an identity into a page that already has one. font-size is
// absolute (not em) because an em would inherit nothing useful — an iframe
// document does not see the host's font-size — and a widget that renders at
// the host's mercy is a widget that renders wrong.
func embedCSS(theme string) string {
	const base = `*,*::before,*::after{box-sizing:border-box}
html,body{height:100%}
body{margin:0;background:var(--bg);color:var(--fg);
  font:15px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  -webkit-text-size-adjust:100%;text-rendering:optimizeLegibility}
/* The frame's height is fixed by whoever embedded it and its content is
   not, so the list takes the overflow and scrolls while the heading and
   the subscribe link stay put. Letting the document overflow instead
   sliced the subscribe link in half at the default height — which is what
   it looked like in a browser, and it read as a broken widget rather than
   as a widget with more content than room. */
.aff{padding:18px 20px;height:100%;display:flex;flex-direction:column}
.aff-feed{margin:0 0 4px;font-size:11px;font-weight:700;letter-spacing:.09em;
  text-transform:uppercase;color:var(--muted);flex:none}
.aff-items{margin:0;padding:0;list-style:none;
  flex:1 1 auto;min-height:0;overflow-y:auto;overscroll-behavior:contain}
/* One column, not a date rail beside the text. The rail was a fifth of the
   width printing the same date on every row — a feed publishes a run at a
   time, so those labels repeat by construction — and it left a hole
   whenever an item had no date at all. As a caption it costs nothing when
   it is absent and stops competing with the title when it is present. */
.aff-item{padding:14px 0;border-top:1px solid var(--line)}
.aff-item:first-child{border-top:0}
.aff-when{display:block;margin-bottom:5px;
  font:11px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  font-variant-numeric:tabular-nums;letter-spacing:.02em;color:var(--muted)}
.aff-title{display:block;font-size:15.5px;font-weight:650;line-height:1.35;
  letter-spacing:-.005em;text-wrap:balance}
/* Clamped, not truncated by the frame. Summaries are model-authored and
   their length is not a fixed quantity — one long one would otherwise push
   every other item out of a fixed-height frame. Three lines is enough to
   judge an item and short enough that the rows stay a list. */
.aff-summary{margin:5px 0 0;color:var(--muted);font-size:13.5px;line-height:1.5;
  display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;
  overflow:hidden}
.aff-empty{margin:0;color:var(--muted);flex:1 1 auto}
.aff-sub{margin:0;padding-top:12px;margin-top:auto;
  border-top:1px solid var(--line);
  font-size:11px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;
  flex:none}
a{color:inherit;text-decoration:none}
.aff-title a{text-decoration-color:transparent;text-decoration-line:underline;
  text-decoration-thickness:1.5px;text-underline-offset:3px;
  transition:text-decoration-color .12s ease}
.aff-title a:hover{text-decoration-color:var(--accent)}
.aff-sub a{color:var(--accent)}
.aff-sub a:hover{text-decoration:underline;text-underline-offset:3px}
a:focus-visible{outline:2px solid var(--accent);outline-offset:3px;
  border-radius:2px}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
`
	const light = `:root{color-scheme:light;--bg:#fff;--fg:#16161a;--muted:#6b6b76;
  --line:#e6e6ea;--accent:#3a5ccc}
`
	const dark = `:root{color-scheme:dark;--bg:#131317;--fg:#ecedf1;--muted:#9a9aa6;
  --line:#2a2a33;--accent:#8fa6ff}
`
	switch theme {
	case "light":
		return light + base
	case "dark":
		return dark + base
	default:
		// auto: light is the unconditional definition so a client that does
		// not support prefers-color-scheme still gets a complete palette
		// rather than unstyled text with undefined custom properties.
		return light + "@media (prefers-color-scheme:dark){" + dark + "}\n" + base
	}
}

// EmbedSnippet returns the iframe markup an operator copies into their page,
// as literal unescaped markup — a caller displaying it inside an HTML
// document escapes it exactly once (index.go does).
//
// It lives here because it must agree with what the route actually accepts,
// and a snippet printed from a different file is one that drifts.
//
// No query string. The bare URL is the defaults (DefaultEmbedCount,
// DefaultEmbedTheme), so the shortest snippet is also the canonical one, and
// the reader is not handed an "&" that has to survive being escaped for
// display, copied, and pasted back into a document — a round trip that
// produces "&amp;amp;" about as often as it works. count and theme are
// documented for anyone who wants to edit the src by hand.
//
// The height is fixed and visible in the snippet: an iframe cannot size
// itself to its content without script on both sides of the frame boundary,
// and that script is exactly what this design refused to ship (see Embed's
// doc comment). A number the embedding author can edit is the honest
// version of that limitation.
func EmbedSnippet(baseURL, slug string) string {
	return `<iframe src="` + baseURL + "/embed/" + slug +
		`" title="` + slug +
		`" width="100%" height="420" loading="lazy" style="border:0"></iframe>`
}
