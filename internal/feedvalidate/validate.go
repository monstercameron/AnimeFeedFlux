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
		} else if _, ok := parseAtomTimestamp(updated[0]); !ok {
			findings = append(findings, Finding{Error, "§5.2 updated-rfc3339",
				scope + " <updated> does not parse as RFC 3339 with uppercase T/Z: " + updated[0]})
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
		} else if _, ok := parseAtomTimestamp(e.Updated[0]); !ok {
			findings = append(findings, Finding{Error, "§5.2 updated-rfc3339",
				scope + " <updated> does not parse as RFC 3339 with uppercase T/Z: " + e.Updated[0]})
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
		} else if _, ok := parseAtomTimestamp(*item.DatePub); !ok {
			// date_published shares Atom's RFC 3339 formatter per §5.3, so
			// the same strict parser applies here.
			findings = append(findings, Finding{Error, "§5.3 date-published-rfc3339",
				label + " date_published does not parse as RFC 3339: " + *item.DatePub})
		}
	}

	return findings
}
