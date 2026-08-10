// Package sources fetches and parses upstream RSS/Atom feeds into the
// Candidate shape consumed by §9's grounded generation step (PLAN.md §9.1).
package sources

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Candidate is one upstream article, normalized into a shape common to both
// RSS 2.0 and Atom 1.0 so the generator (§9) never has to know which format a
// given source used.
type Candidate struct {
	Title      string
	URL        string
	Excerpt    string
	Published  time.Time
	SourceName string
}

// ErrBodyTooLarge is returned when the input exceeds the caller-enforced size
// cap before parsing ever begins (§4: "Upstream XML parsing ... caps body
// size"). Fetch enforces the cap via io.LimitReader; Parse re-checks its
// input length defensively so any future caller of Parse cannot bypass the
// cap by constructing a *sources.Fetcher-free path.
var ErrBodyTooLarge = errors.New("sources: feed body exceeds maximum size")

// ErrUnrecognizedFeed is returned when the body is neither RSS nor Atom.
var ErrUnrecognizedFeed = errors.New("sources: could not identify feed as RSS or Atom")

// MaxParseBytes bounds what Parse will attempt to decode, independent of any
// cap the fetch layer already applied — defense in depth, not a
// substitute for Fetcher.MaxBytes.
const MaxParseBytes = 10 << 20 // 10 MiB

// dateLayouts enumerates every date format Parse will accept on input. Feed
// dates found in the wild are inconsistent — some RSS feeds emit RFC 3339
// where the spec calls for RFC 822, and vice versa for Atom — so this list is
// deliberately permissive on the way in. What we ever *emit* remains strict
// and single-formatted per §5.1/§5.2; this leniency exists only for reading
// other people's feeds, and never leaks into our own output.
var dateLayouts = []string{
	time.RFC1123Z, // Mon, 02 Jan 2006 15:04:05 -0700
	time.RFC1123,  // Mon, 02 Jan 2006 15:04:05 MST
	time.RFC3339,  // 2006-01-02T15:04:05Z07:00
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
}

// parseDate tries every known layout and returns the zero time if none
// match, rather than erroring — a single unparseable date on one item must
// not fail the whole feed.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// newDecoder builds an xml.Decoder hardened against hostile input (§4):
//
//   - Strict is left at its default true, and Entity is explicitly set to an
//     EMPTY map (not nil). encoding/xml never expands *external* entities
//     regardless of this setting, but a nil Entity map still falls back to
//     the decoder's built-in handling of the five predefined XML entities
//     only — setting it explicitly to an empty map here is a documented,
//     visible assertion that no custom entity expansion (e.g. a chain of
//     internal entities escalating to billions of bytes, the "billion
//     laughs" attack) is ever configured for this decoder, now or by a
//     future edit that might otherwise add one without noticing the
//     security implication.
//   - The caller is responsible for capping input size before it reaches
//     here (Fetch does this via io.LimitReader; Parse re-checks below) —
//     entity expansion aside, an unbounded body is its own resource
//     exhaustion vector.
func newDecoder(body []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Strict = true
	d.Entity = map[string]string{}
	return d
}

// Parse sniffs body as RSS 2.0 or Atom 1.0 and returns a unified candidate
// list. sourceName is stamped onto every candidate (Candidate.SourceName)
// since a feed does not reliably self-identify its human-readable source
// name in a way both formats agree on.
func Parse(body []byte, sourceName string) ([]Candidate, error) {
	if int64(len(body)) > MaxParseBytes {
		return nil, fmt.Errorf("%w: %d bytes > %d max", ErrBodyTooLarge, len(body), MaxParseBytes)
	}

	kind, err := sniff(body)
	if err != nil {
		return nil, err
	}

	switch kind {
	case feedKindRSS:
		return parseRSS(body, sourceName)
	case feedKindAtom:
		return parseAtom(body, sourceName)
	default:
		return nil, ErrUnrecognizedFeed
	}
}

type feedKind int

const (
	feedKindUnknown feedKind = iota
	feedKindRSS
	feedKindAtom
)

// sniff walks the top-level XML tokens (via the same hardened decoder Parse
// itself uses) looking for the root element name, rather than doing a
// substring search on raw bytes — a substring match could be fooled by an
// "<rss" appearing inside a CDATA block or comment before the real root.
func sniff(body []byte) (feedKind, error) {
	d := newDecoder(body)
	for {
		tok, err := d.Token()
		if err != nil {
			return feedKindUnknown, fmt.Errorf("sources: sniffing feed type: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch strings.ToLower(start.Name.Local) {
		case "rss":
			return feedKindRSS, nil
		case "feed":
			return feedKindAtom, nil
		default:
			return feedKindUnknown, ErrUnrecognizedFeed
		}
	}
}

// --- RSS 2.0 ---

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

func parseRSS(body []byte, sourceName string) ([]Candidate, error) {
	var feed rssFeed
	if err := newDecoder(body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("sources: parsing RSS: %w", err)
	}

	cands := make([]Candidate, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		cands = append(cands, Candidate{
			Title:      strings.TrimSpace(it.Title),
			URL:        strings.TrimSpace(it.Link),
			Excerpt:    strings.TrimSpace(it.Description),
			Published:  parseDate(it.PubDate),
			SourceName: sourceName,
		})
	}
	return cands, nil
}

// --- Atom 1.0 ---

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// atomExcerpt prefers summary over content, matching §5.2's plain-text vs.
// HTML split — a summary is the closer analog to RSS's description for our
// Excerpt field.
func atomExcerpt(e atomEntry) string {
	if s := strings.TrimSpace(e.Summary); s != "" {
		return s
	}
	return strings.TrimSpace(e.Content)
}

// atomHref picks the entry's alternate link, falling back to the first link
// present if no rel="alternate" is found (some feeds omit rel, in which case
// it defaults to "alternate" per RFC 4287 §4.2.7.2 anyway).
func atomHref(e atomEntry) string {
	for _, l := range e.Links {
		if l.Rel == "" || l.Rel == "alternate" {
			return strings.TrimSpace(l.Href)
		}
	}
	if len(e.Links) > 0 {
		return strings.TrimSpace(e.Links[0].Href)
	}
	return ""
}

// atomDate prefers published (creation) over updated (last-modified), since
// Candidate.Published models "when this article came out" for the §9.1
// "keep entries newer than the last run" filter.
func atomDate(e atomEntry) time.Time {
	if t := parseDate(e.Published); !t.IsZero() {
		return t
	}
	return parseDate(e.Updated)
}

func parseAtom(body []byte, sourceName string) ([]Candidate, error) {
	var feed atomFeed
	if err := newDecoder(body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("sources: parsing Atom: %w", err)
	}

	cands := make([]Candidate, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		cands = append(cands, Candidate{
			Title:      strings.TrimSpace(e.Title),
			URL:        atomHref(e),
			Excerpt:    atomExcerpt(e),
			Published:  atomDate(e),
			SourceName: sourceName,
		})
	}
	return cands, nil
}
