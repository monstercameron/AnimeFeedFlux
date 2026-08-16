// Package feedvalidate is an OFFLINE stand-in for the W3C / RSS Advisory
// Board feed validator that PLAN.md §5.6 names as the `make validate` gate.
//
// This is a deliberate substitution, not an equivalent. The real validator
// is a hosted third-party service: depending on it from CI means the build
// now depends on someone else's uptime, rate limits, and network egress from
// the CI runner, none of which this project controls. What this package
// checks instead is narrower and different in kind — it encodes only the
// rules this project actually depends on (the RSS/Atom/JSON Feed profile
// requirements cited in PLAN.md §5.1-§5.3, plus the Slack-compatibility
// requirements in §5.5) rather than the full RSS/Atom specifications the
// hosted validator enforces. Passing here does not mean the hosted validator
// would also pass, and a reader relying on this package should still run a
// release candidate through the real W3C / RSS Advisory Board validator by
// hand before a release, per §5.6.
package feedvalidate

import (
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"
	"time"
)

// Level distinguishes findings CI must fail on (both, per §5.6 — "CI asserts
// zero errors *and* zero warnings") from findings a human triage might want
// to see separately in output. The distinction is informational only; see
// cmd/affvalidate for why both gate the exit code identically.
type Level string

const (
	Error   Level = "error"
	Warning Level = "warning"
)

// Finding is one rule violation. Rule cites the PLAN.md section it encodes,
// so a failing check can be traced back to the requirement that motivated
// it.
type Finding struct {
	Level   Level
	Rule    string
	Message string
}

// atomLinkEl models an Atom-namespaced <link>, reused for RSS's
// <atom:link rel="self"> (§5.1) and Atom's own <link> elements (§5.2).
type atomLinkEl struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

// ---- RSS 2.0 (§5.1) ----

type rssRoot struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Description string   `xml:"description"`
	Link        string   `xml:"link"`
	Guid        *rssGuid `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
}

type rssGuid struct {
	IsPermaLink *string `xml:"isPermaLink,attr"`
	Value       string  `xml:",chardata"`
}

// rfc822Layout mirrors internal/render's format exactly (four-digit year,
// numeric offset, no military zone abbreviation). It is redefined here
// rather than imported so this validator has zero dependency on the
// renderer it is meant to independently check — see the package doc and the
// test file's header comment for why that independence is the point.
const rfc822Layout = "Mon, 02 Jan 2006 15:04:05 -0700"

// schemePattern matches an absolute URI's leading "scheme:" (RFC 3986
// §3.1), which both "https://..." and "tag:..." satisfy and a relative path
// like "/feeds/x.xml" does not. Used for every "no relative URL" check
// across RSS, Atom, and JSON Feed.
var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

func isAbsoluteURI(s string) bool {
	return schemePattern.MatchString(s)
}

// htmlTagPattern looks for a literal, unescaped HTML tag in already-decoded
// XML character data. It deliberately runs against the raw document slice
// (see descriptionRawText) rather than the xml-decoded string, because a
// hex character reference like "&#x3C;" decodes to a literal "<" too —
// checking post-decode text cannot tell "wrote a real tag" apart from
// "wrote the escaped character the profile recommends" (§5.1's escaping
// note).
var htmlTagPattern = regexp.MustCompile(`<[/]?[A-Za-z][^>]*>`)

// descriptionTagPattern extracts the raw (pre-unescape) inner text of every
// <description>...</description> element, at either channel or item level.
var descriptionTagPattern = regexp.MustCompile(`(?s)<description>(.*?)</description>`)

// atomSelfLinkPattern finds a <atom:link .../> (or any other namespace
// prefix bound to the Atom namespace) element and captures its attributes
// as one blob. It is a raw-byte scan rather than an encoding/xml field,
// because encoding/xml cannot cleanly disambiguate an unqualified <link>
// field from a namespace-qualified one sharing the same local name when
// both appear in the same document — see validate_test.go's history for
// the concrete failure this sidesteps.
var atomSelfLinkPattern = regexp.MustCompile(`<[A-Za-z][\w.\-]*:link\b([^>]*)/?>`)

// stripAtomSelfLinkPattern matches only the self-closing form (the shape
// this project's renderer always emits), which is what needs removing
// before decode — see the comment in RSS for why.
var stripAtomSelfLinkPattern = regexp.MustCompile(`<[A-Za-z][\w.\-]*:link\b[^>]*/>`)
var relAttrPattern = regexp.MustCompile(`\brel="([^"]*)"`)
var typeAttrPattern = regexp.MustCompile(`\btype="([^"]*)"`)
var hrefAttrPattern = regexp.MustCompile(`\bhref="([^"]*)"`)

// hasAtomSelfLink reports whether doc contains a prefixed <ns:link> element
// with rel="self", type="application/rss+xml", and an absolute href — the
// §5.1 atom:link requirement.
func hasAtomSelfLink(doc []byte) bool {
	for _, m := range atomSelfLinkPattern.FindAllSubmatch(doc, -1) {
		attrs := string(m[1])
		rel := firstSubmatch(relAttrPattern, attrs)
		typ := firstSubmatch(typeAttrPattern, attrs)
		href := firstSubmatch(hrefAttrPattern, attrs)
		if rel == "self" && typ == "application/rss+xml" && isAbsoluteURI(href) {
			return true
		}
	}
	return false
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// dateRangeFinding returns a Finding if t is the zero time.Time, or its year
// falls outside the four-digit range [0001, 9999], or nil if t is fine.
//
// Shared across RSS pubDate, Atom updated, and JSON Feed date_published:
// the defect this exists to catch — a year >= 10000 formatting into a
// string its OWN reference layout then rejects on Parse (internal/render/
// dates.go's rfc822Layout/rfc3339Layout) — is symmetric across every format
// this package checks, and internal/rpc/item.go's itemValidatePublishedAt
// closes the same gap at the point a date enters the system. This is the
// second line of defense: a document that somehow acquires an out-of-range
// or zero date anyway (legacy data written before that check existed,
// generation/other write paths this validator does not gate) must still
// fail `make validate` as an ERROR, per PLAN.md §5.6 — not a warning, since
// the document is unreadable or silently wrong, not merely suboptimal.
//
// The zero-value check is separate from the range check for the same
// reason it is separate in itemValidatePublishedAt: 0001-01-01T00:00:00Z is
// INSIDE the four-digit-year range and parses fine, but is overwhelmingly
// more likely to be an unset Go time.Time that reached the renderer than an
// intentional publish date — and it renders as a plausible-looking
// timestamp instead of failing loudly, so a dedicated rule and message
// distinguishes it from a genuine out-of-range year.
func dateRangeFinding(t time.Time, zeroRule, rangeRule, label string) *Finding {
	if t.IsZero() {
		return &Finding{Error, zeroRule,
			label + " is the zero time (0001-01-01T00:00:00Z) — almost certainly an unset published_at that reached the renderer, not an intended date"}
	}
	if y := t.Year(); y < 1 || y > 9999 {
		return &Finding{Error, rangeRule,
			label + " year is outside the four-digit range [0001, 9999] required by RFC 822/RFC 3339"}
	}
	return nil
}

// RSS validates an RSS 2.0 document against the subset of the RSS Advisory
// Board Best Practices Profile this project's renderer targets (§5.1) plus
// the Slack-compatibility ordering rule (§5.5).
func RSS(doc []byte) []Finding {
	// Well-formedness is checked against the original document: any error
	// here is a genuine parse failure, not an artifact of the stripping
	// below.
	if err := xml.Unmarshal(doc, new(rssRoot)); err != nil {
		return []Finding{{Error, "§5.1 well-formed-xml", "document does not parse as XML: " + err.Error()}}
	}

	// encoding/xml matches an unqualified struct tag like `xml:"link"`
	// against a <link> local name in ANY namespace, not just the default
	// one — so with both a plain <link> and a namespaced <atom:link> in the
	// same channel, the atom:link (processed second) silently clobbers the
	// plain Link field. Stripping the namespaced self-link before decoding
	// sidesteps that; hasAtomSelfLink checks it separately against the
	// original, unstripped doc. Since the original doc already parsed
	// above, removing a well-formed self-closing element keeps it
	// well-formed, so this second decode is not re-checked for errors.
	var root rssRoot
	_ = xml.Unmarshal(stripAtomSelfLinkPattern.ReplaceAll(doc, nil), &root)

	var findings []Finding
	ch := root.Channel

	if trim(ch.Title) == "" {
		findings = append(findings, Finding{Error, "§5.1 channel-required-elements", "channel is missing title"})
	}
	if trim(ch.Link) == "" {
		findings = append(findings, Finding{Error, "§5.1 channel-required-elements", "channel is missing link"})
	}
	if trim(ch.Description) == "" {
		findings = append(findings, Finding{Error, "§5.1 channel-required-elements", "channel is missing description"})
	}

	if !hasAtomSelfLink(doc) {
		findings = append(findings, Finding{Warning, "§5.1 atom-link-self",
			"channel is missing an atom:link rel=\"self\" type=\"application/rss+xml\" with an absolute href"})
	}

	if !isAbsoluteURI(ch.Link) && trim(ch.Link) != "" {
		findings = append(findings, Finding{Error, "§5.1 no-relative-urls", "channel link is not an absolute URL: " + ch.Link})
	}

	var pubDates []time.Time
	havePubDates := true

	for i, item := range ch.Items {
		if trim(item.Title) == "" && trim(item.Description) == "" {
			findings = append(findings, Finding{Error, "§5.1 item-title-or-description",
				itemLabel(i) + " has neither title nor description"})
		}

		if item.Guid == nil {
			findings = append(findings, Finding{Error, "§5.1 guid-isPermaLink-explicit", itemLabel(i) + " is missing guid"})
		} else if item.Guid.IsPermaLink == nil {
			findings = append(findings, Finding{Error, "§5.1 guid-isPermaLink-explicit",
				itemLabel(i) + " guid does not set isPermaLink explicitly"})
		} else if *item.Guid.IsPermaLink != "false" && !isAbsoluteURI(item.Guid.Value) {
			findings = append(findings, Finding{Error, "§5.1 no-relative-urls",
				itemLabel(i) + " guid is a permalink but not an absolute URL: " + item.Guid.Value})
		}

		if trim(item.Link) != "" && !isAbsoluteURI(item.Link) {
			findings = append(findings, Finding{Error, "§5.1 no-relative-urls", itemLabel(i) + " link is not an absolute URL: " + item.Link})
		}

		if trim(item.PubDate) == "" {
			findings = append(findings, Finding{Error, "§5.1 pubdate-rfc822", itemLabel(i) + " is missing pubDate"})
			havePubDates = false
			continue
		}
		t, err := time.Parse(rfc822Layout, item.PubDate)
		if err != nil {
			findings = append(findings, Finding{Error, "§5.1 pubdate-rfc822",
				itemLabel(i) + " pubDate does not parse as RFC 822 with a four-digit year and numeric offset: " + item.PubDate})
			havePubDates = false
			continue
		}
		if f := dateRangeFinding(t, "§5.1 pubdate-not-zero", "§5.1 pubdate-year-range", itemLabel(i)+" pubDate"); f != nil {
			findings = append(findings, *f)
			havePubDates = false
			continue
		}
		pubDates = append(pubDates, t)
	}

	if havePubDates {
		for i := 1; i < len(pubDates); i++ {
			if !pubDates[i-1].After(pubDates[i]) {
				findings = append(findings, Finding{Error, "§5.5 pubdate-strictly-descending-unique",
					"item pubDates are not strictly descending and unique (Slack drops out-of-order or duplicate-dated items)"})
				break
			}
		}
	}

	for _, m := range descriptionTagPattern.FindAllSubmatch(doc, -1) {
		if htmlTagPattern.Match(m[1]) {
			findings = append(findings, Finding{Error, "§5.5 description-plain-text",
				"description contains a raw HTML tag; description must be plain text (Slack renders it verbatim, no markup)"})
			break
		}
	}

	return findings
}

func itemLabel(i int) string {
	return "item[" + itoa(i) + "]"
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ---- Atom 1.0 (§5.2) ----

type atomFeedRoot struct {
	XMLName xml.Name     `xml:"http://www.w3.org/2005/Atom feed"`
	ID      []string     `xml:"http://www.w3.org/2005/Atom id"`
	Title   []string     `xml:"http://www.w3.org/2005/Atom title"`
	Updated []string     `xml:"http://www.w3.org/2005/Atom updated"`
	Links   []atomLinkEl `xml:"http://www.w3.org/2005/Atom link"`
	Entries []atomEntry  `xml:"http://www.w3.org/2005/Atom entry"`
}

type atomEntry struct {
	ID      []string     `xml:"http://www.w3.org/2005/Atom id"`
	Title   []string     `xml:"http://www.w3.org/2005/Atom title"`
	Updated []string     `xml:"http://www.w3.org/2005/Atom updated"`
	Content *string      `xml:"http://www.w3.org/2005/Atom content"`
	Links   []atomLinkEl `xml:"http://www.w3.org/2005/Atom link"`
}

// rfc3339Pattern requires the literal uppercase "T" separator and, absent a
// numeric offset, a literal uppercase "Z" — RFC 3339 itself permits lowercase,
// but §5.2 pins the renderer to uppercase specifically so a byte-diff against
// a golden catches drift, and this check enforces the same pin.
var rfc3339Pattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+\-]\d{2}:\d{2})$`)

func parseAtomTimestamp(s string) (time.Time, bool) {
	if !rfc3339Pattern.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// Atom validates an Atom 1.0 (RFC 4287) document against §5.2.
func Atom(doc []byte) []Finding {
	var root atomFeedRoot
	if err := xml.Unmarshal(doc, &root); err != nil {
		return []Finding{{Error, "§5.2 well-formed-xml", "document does not parse as XML: " + err.Error()}}
	}

	var findings []Finding
	ids := map[string]bool{}
	dupIDReported := false

	checkSingletons := func(scope string, id, title, updated []string) {
		if len(id) != 1 {
			findings = append(findings, Finding{Error, "§5.2 feed-required-singletons",
				scope + " must have exactly one <id> element, found " + itoa(len(id))})
		} else {
			if ids[id[0]] && !dupIDReported {
				findings = append(findings, Finding{Error, "§5.2 id-unique", "duplicate Atom id: " + id[0]})
				dupIDReported = true
			}
			ids[id[0]] = true
		}
		if len(title) != 1 {
			findings = append(findings, Finding{Error, "§5.2 feed-required-singletons",
				scope + " must have exactly one <title> element, found " + itoa(len(title))})
		}
		if len(updated) != 1 {
			findings = append(findings, Finding{Error, "§5.2 feed-required-singletons",
				scope + " must have exactly one <updated> element, found " + itoa(len(updated))})
		} else if t, ok := parseAtomTimestamp(updated[0]); !ok {
			findings = append(findings, Finding{Error, "§5.2 updated-rfc3339",
				scope + " <updated> does not parse as RFC 3339 with uppercase T/Z: " + updated[0]})
		} else if f := dateRangeFinding(t, "§5.2 updated-not-zero", "§5.2 updated-year-range", scope+" <updated>"); f != nil {
			findings = append(findings, *f)
		}
	}

	checkSingletons("feed", root.ID, root.Title, root.Updated)

	for _, l := range root.Links {
		if l.Href == "" {
			findings = append(findings, Finding{Error, "§5.2 no-empty-href", "feed <link> has an empty href attribute"})
		}
	}

	for i, e := range root.Entries {
		scope := "entry[" + itoa(i) + "]"
		// checkSingletons above uses "feed"/"entry" wording generically for
		// the shared rule string; per-entry singleton violations still cite
		// §5.2 entry-required-singletons specifically below rather than
		// reusing the feed-level helper's rule string, so tests can target
		// each independently.
		if len(e.ID) != 1 {
			findings = append(findings, Finding{Error, "§5.2 entry-required-singletons",
				scope + " must have exactly one <id> element, found " + itoa(len(e.ID))})
		} else {
			if ids[e.ID[0]] && !dupIDReported {
				findings = append(findings, Finding{Error, "§5.2 id-unique", "duplicate Atom id: " + e.ID[0]})
				dupIDReported = true
			}
			ids[e.ID[0]] = true
		}
		if len(e.Title) != 1 {
			findings = append(findings, Finding{Error, "§5.2 entry-required-singletons",
				scope + " must have exactly one <title> element, found " + itoa(len(e.Title))})
		}
		if len(e.Updated) != 1 {
			findings = append(findings, Finding{Error, "§5.2 entry-required-singletons",
				scope + " must have exactly one <updated> element, found " + itoa(len(e.Updated))})
		} else if t, ok := parseAtomTimestamp(e.Updated[0]); !ok {
			findings = append(findings, Finding{Error, "§5.2 updated-rfc3339",
				scope + " <updated> does not parse as RFC 3339 with uppercase T/Z: " + e.Updated[0]})
		} else if f := dateRangeFinding(t, "§5.2 updated-not-zero", "§5.2 updated-year-range", scope+" <updated>"); f != nil {
			findings = append(findings, *f)
		}

		for _, l := range e.Links {
			if l.Href == "" {
				findings = append(findings, Finding{Error, "§5.2 no-empty-href", scope + " <link> has an empty href attribute"})
			}
		}

		if e.Content == nil {
			hasAlternate := false
			for _, l := range e.Links {
				if l.Rel == "alternate" && l.Href != "" {
					hasAlternate = true
				}
			}
			if !hasAlternate {
				findings = append(findings, Finding{Error, "§5.2 entry-content-or-alternate-link",
					scope + " has no <content> and no <link rel=\"alternate\">"})
			}
		}
	}

	return findings
}

// ---- JSON Feed 1.1 (§5.3) ----

const jsonFeedVersion = "https://jsonfeed.org/version/1.1"

type jsonFeedDoc struct {
	Version string            `json:"version"`
	Title   string            `json:"title"`
	Items   []json.RawMessage `json:"items"`
}

type jsonFeedItem struct {
	ID          json.RawMessage `json:"id"`
	ContentHTML *string         `json:"content_html"`
	ContentText *string         `json:"content_text"`
	DatePub     *string         `json:"date_published"`
}

// JSONFeed validates a JSON Feed 1.1 document against §5.3.
func JSONFeed(doc []byte) []Finding {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(doc, &raw); err != nil {
		return []Finding{{Error, "§5.3 well-formed-json", "document does not parse as JSON: " + err.Error()}}
	}

	var feed jsonFeedDoc
	if err := json.Unmarshal(doc, &feed); err != nil {
		return []Finding{{Error, "§5.3 well-formed-json", "document does not parse as JSON: " + err.Error()}}
	}

	var findings []Finding

	if feed.Version != jsonFeedVersion {
		findings = append(findings, Finding{Error, "§5.3 version-exact", "version is not exactly " + jsonFeedVersion + ": " + feed.Version})
	}
	if trim(feed.Title) == "" {
		findings = append(findings, Finding{Error, "§5.3 title-present", "feed is missing title"})
	}
	if _, ok := raw["author"]; ok {
		findings = append(findings, Finding{Warning, "§5.3 no-deprecated-author",
			"feed uses the deprecated \"author\" key; JSON Feed 1.1 replaced it with \"authors\""})
	}

	for i, itemRaw := range feed.Items {
		var itemMap map[string]json.RawMessage
		_ = json.Unmarshal(itemRaw, &itemMap)
		var item jsonFeedItem
		_ = json.Unmarshal(itemRaw, &item)

		label := "item[" + itoa(i) + "]"

		if len(item.ID) == 0 {
			findings = append(findings, Finding{Error, "§5.3 item-id-string", label + " is missing id"})
		} else {
			var s string
			if err := json.Unmarshal(item.ID, &s); err != nil {
				findings = append(findings, Finding{Error, "§5.3 item-id-string", label + " id is not a JSON string"})
			}
		}

		if (item.ContentHTML == nil || *item.ContentHTML == "") && (item.ContentText == nil || *item.ContentText == "") {
			findings = append(findings, Finding{Error, "§5.3 item-content-present",
				label + " has neither content_html nor content_text"})
		}

		if _, ok := itemMap["author"]; ok {
			findings = append(findings, Finding{Warning, "§5.3 no-deprecated-author",
				label + " uses the deprecated \"author\" key; JSON Feed 1.1 replaced it with \"authors\""})
		}

		if item.DatePub == nil || trim(*item.DatePub) == "" {
			findings = append(findings, Finding{Error, "§5.3 date-published-rfc3339", label + " is missing date_published"})
		} else if t, ok := parseAtomTimestamp(*item.DatePub); !ok {
			// date_published shares Atom's RFC 3339 formatter per §5.3, so
			// the same strict parser applies here.
			findings = append(findings, Finding{Error, "§5.3 date-published-rfc3339",
				label + " date_published does not parse as RFC 3339: " + *item.DatePub})
		} else if f := dateRangeFinding(t, "§5.3 date-published-not-zero", "§5.3 date-published-year-range", label+" date_published"); f != nil {
			findings = append(findings, *f)
		}
	}

	return findings
}

// ---- Permalink page OG/unfurl tags (§5.5 "Link unfurling") ----

// metaTagPattern matches internal/render/permalink.go's writeMeta output
// exactly: `<meta attr="key" content="value">`, one tag per line, attr value
// unescaped (it is always a literal like "og:title" or "name"). Read against
// the raw document rather than an HTML parser for the same independence
// reason internal/render's own doc comment gives for RSS/Atom above: this
// package must not import an HTML tree-builder that could paper over a
// malformed tag the real unfurler would choke on.
var metaTagPattern = regexp.MustCompile(`<meta (name|property)="([^"]+)" content="([^"]*)">`)

// Permalink validates an item permalink page (PLAN.md §5.5 "Link
// unfurling", §6 GET /items/{item_key}) against the OpenGraph/Twitter-card
// contract Slack's unfurler reads. Slack never fetches the RSS entry when
// deciding what to show for a shared link — it unfurls this page and reads
// only these meta tags, never the <body> — so a missing or malformed tag
// here is exactly as silent a failure as a bad pubDate: the unfurl just
// renders a bare URL with no title/summary/image, with nothing else in the
// pipeline reporting an error.
func Permalink(doc []byte) []Finding {
	var findings []Finding

	meta := map[string]string{}
	for _, m := range metaTagPattern.FindAllSubmatch(doc, -1) {
		meta[string(m[2])] = string(m[3])
	}

	for _, key := range []string{"og:title", "og:description", "og:type", "og:url", "article:published_time"} {
		if _, ok := meta[key]; !ok {
			findings = append(findings, Finding{Error, "§5.5 permalink-og-tags-present",
				"permalink page is missing meta tag: " + key + " (Slack's unfurl reads only these tags, never the page body)"})
		}
	}

	if v, ok := meta["og:type"]; ok && v != "article" {
		findings = append(findings, Finding{Error, "§5.5 permalink-og-type-article", `og:type must be "article", got: ` + v})
	}

	if v, ok := meta["og:url"]; ok && trim(v) != "" && !isAbsoluteURI(v) {
		findings = append(findings, Finding{Error, "§5.1 no-relative-urls", "og:url is not an absolute URL: " + v})
	}
	if v, ok := meta["og:image"]; ok && trim(v) != "" && !isAbsoluteURI(v) {
		findings = append(findings, Finding{Error, "§5.1 no-relative-urls", "og:image is not an absolute URL: " + v})
	}

	if v, ok := meta["article:published_time"]; ok {
		if t, parseOK := parseAtomTimestamp(v); !parseOK {
			findings = append(findings, Finding{Error, "§5.5 permalink-published-time-rfc3339",
				"article:published_time does not parse as RFC 3339 with uppercase T/Z: " + v})
		} else if f := dateRangeFinding(t, "§5.5 permalink-published-time-not-zero", "§5.5 permalink-published-time-year-range", "article:published_time"); f != nil {
			findings = append(findings, *f)
		}
	}

	// og:description is what Slack's unfurl shows as the summary line. It
	// must be the same plain-text contract as RSS <description> (§5.5,
	// description-plain-text above) — a raw tag here would render literally
	// in the unfurl, exactly like it would in a Slack channel message.
	//
	// NOTE what this check cannot do: it has no oracle for which substring
	// of the page is "the trivia answer", so it cannot detect a semantic
	// spoiler leak the way internal/render/slack_test.go's
	// TestSlack_DescriptionNeverLeaksAnswer can (that test knows the literal
	// answer token going in). This check only catches raw markup landing in
	// og:description, the same structural class of bug as the RSS
	// description-plain-text rule.
	if v, ok := meta["og:description"]; ok && htmlTagPattern.MatchString(v) {
		findings = append(findings, Finding{Error, "§5.5 description-plain-text",
			"og:description contains a raw HTML tag; Slack's unfurl shows this verbatim, no markup"})
	}

	return findings
}

// ---- Embed document (§6.1 GET /embed/{slug}) ----

// embedExternalRefPatterns are the ways an HTML document can reach off-host.
// The embed must use none of them (§6.1): it is rendered inside a page this
// project does not control, under a
// `default-src 'none'; style-src 'unsafe-inline'` policy, so a reference that
// slipped in would either be blocked by the header (a broken widget) or, if
// the header were ever relaxed, become a request made from a stranger's page
// on our behalf.
var embedExternalRefPatterns = []struct {
	needle string
	what   string
}{
	{"<script", "a <script> element"},
	{"<link ", "a <link> element (stylesheet, preload, or icon)"},
	{"<img", "an <img> element"},
	{"@import", "a CSS @import"},
	{"url(", "a CSS url() reference"},
}

// Embed validates an embed document (PLAN.md §6.1). The properties checked
// here are the ones that make it safe to put on somebody else's page, and
// they are structural — a validator has no oracle for whether the styling
// looks right, but it can prove the document asks for nothing and runs
// nothing.
//
// This is validated rather than skipped for the reason the permalink case
// gives above: skipping the one surface that renders into a third party's
// document would leave `make validate` blind to exactly the artefact whose
// failure mode is somebody else's incident.
func Embed(doc []byte) []Finding {
	var findings []Finding
	s := string(doc)
	lower := strings.ToLower(s)

	if !strings.HasPrefix(lower, "<!doctype html>") {
		findings = append(findings, Finding{Error, "§6.1 embed-complete-document",
			"embed document does not begin with <!doctype html>; it is framed as a whole document, not injected as a fragment"})
	}

	// noindex: the embed duplicates content whose canonical home is the
	// permalink and the feed, and one indexed copy per embedding site is a
	// feed competing with itself in search results.
	if !strings.Contains(lower, `<meta name="robots" content="noindex">`) {
		findings = append(findings, Finding{Error, "§6.1 embed-noindex",
			"embed document is missing its robots noindex tag"})
	}

	for _, p := range embedExternalRefPatterns {
		if strings.Contains(lower, p.needle) {
			findings = append(findings, Finding{Error, "§6.1 embed-self-contained",
				"embed document contains " + p.what + "; it must fetch nothing"})
		}
	}

	// Inline event handlers. The embed renders escaped text only, so an
	// on*= attribute means either a renderer bug or an escaping failure —
	// and the CSP that would neutralise it is a header, not a property of
	// the bytes, so the bytes are checked too.
	if inlineHandlerPattern.MatchString(s) {
		findings = append(findings, Finding{Error, "§6.1 embed-no-inline-handlers",
			"embed document carries an inline event handler attribute"})
	}

	return findings
}

// inlineHandlerPattern matches an on*= attribute in a tag, e.g. onerror= or
// onclick=. Deliberately loose about the quoting: anything matching this in a
// document that should contain none is worth failing on.
var inlineHandlerPattern = regexp.MustCompile(`(?i)<[a-z][^>]*\son[a-z]+\s*=`)
