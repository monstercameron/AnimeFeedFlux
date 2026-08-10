package render

// This file is the Slack-compatibility regression suite called for by
// PLAN.md §5.5/§5.6. Slack's native RSS app is stricter than the RSS spec
// and fails SILENTLY on a violation — it simply stops posting, with no error
// visible anywhere — so these checks exist to catch what a spec-validator
// would happily pass.

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// slackCompatChannel builds a small channel using the shared fixtures rather
// than rssTestChannel from rss_test.go: TriviaItem's ANSWER-COWBOY-BEBOP
// token is what makes an answer leak into description unmissable, and
// SampleItems gives several items with distinct, strictly increasing
// PublishedAt values to exercise ordering across more than two entries.
func slackCompatChannel() model.Channel {
	c := testutil.SampleChannel(4)
	// Append the trivia item out of order on purpose, same as rss_test.go's
	// fixture: RSS()'s own sort, not the fixture, must be what produces
	// newest-first output.
	c.Items = append(c.Items, testutil.TriviaItem())
	return c
}

// TestSlack_PubDatesStrictlyDescendingAndUnique protects against Slack's
// bookmark model (§5.5): it fetches only items dated strictly later than the
// last one it saw, and it silently drops any item whose pubDate is not
// strictly greater than the previous item's. An out-of-order or duplicate
// pubDate therefore isn't a cosmetic bug — it's an item Slack will never
// post. Dates are parsed back with the RFC822 layout and compared as
// time.Time, not as strings, so this doesn't accidentally pass on a
// lexicographic fluke.
func TestSlack_PubDatesStrictlyDescendingAndUnique(t *testing.T) {
	c := slackCompatChannel()
	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	dates := extractPubDates(t, got)
	if len(dates) < 2 {
		t.Fatalf("expected at least 2 items to test ordering, got %d", len(dates))
	}

	seen := make(map[int64]bool, len(dates))
	for i, d := range dates {
		if seen[d.Unix()] {
			t.Errorf("duplicate pubDate %s at item index %d: Slack drops all but one item sharing a timestamp", RFC822(d), i)
		}
		seen[d.Unix()] = true

		if i > 0 && !dates[i-1].After(d) {
			t.Errorf("pubDate at index %d (%s) is not strictly before item at index %d (%s): Slack requires strictly newest-first ordering",
				i, RFC822(d), i-1, RFC822(dates[i-1]))
		}
	}
}

// TestSlack_EveryItemHasParseableDate protects against §5.5 requirement 1:
// "no pubDate, no post". A missing or malformed date isn't a partial
// failure — Slack drops that specific item from the feed entirely, with no
// error surfaced anywhere.
func TestSlack_EveryItemHasParseableDate(t *testing.T) {
	c := slackCompatChannel()
	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	dates := extractPubDates(t, got)
	if len(dates) != len(c.Items) {
		t.Fatalf("got %d parseable pubDates, want %d (one per item): a missing/unparseable date means Slack never posts that item", len(dates), len(c.Items))
	}
	for i, d := range dates {
		if d.IsZero() {
			t.Errorf("item %d has a zero-value pubDate", i)
		}
	}
}

// TestSlack_DescriptionIsPlainTextUnderCap protects against §5.5's
// description contract: Slack renders this field verbatim as the message
// snippet with no HTML support, so any markup shows up as a literal,
// mangled string in the channel rather than being rendered. The 500-char
// hard cap is Slack's own truncation point per §5.5/§5.6.
func TestSlack_DescriptionIsPlainTextUnderCap(t *testing.T) {
	c := slackCompatChannel()
	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	descriptions := extractDescriptions(t, got)
	if len(descriptions) != len(c.Items) {
		t.Fatalf("got %d descriptions, want %d", len(descriptions), len(c.Items))
	}
	for i, d := range descriptions {
		if len(d) > 500 {
			t.Errorf("item %d description is %d chars, exceeds Slack's 500-char hard cap: %q", i, len(d), d)
		}
		if strings.ContainsAny(d, "<>") {
			t.Errorf("item %d description contains unescaped markup characters, Slack shows this raw with no HTML rendering: %q", i, d)
		}
	}
}

// TestSlack_DescriptionNeverLeaksAnswer protects against §5.5's spoiler
// rule: Slack has no spoiler mechanism, so if the trivia answer ever reaches
// description, every subscriber sees the answer in the channel before
// clicking through, spoiling the question outright. It uses
// testutil.TriviaItem(), whose answer carries the distinctive
// ANSWER-COWBOY-BEBOP token so a leak can't be mistaken for a coincidental
// substring match.
func TestSlack_DescriptionNeverLeaksAnswer(t *testing.T) {
	c := slackCompatChannel()
	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	if strings.Contains(doc, "ANSWER-COWBOY-BEBOP") == false {
		t.Fatalf("fixture sanity check failed: expected content:encoded to contain the answer token somewhere in the document")
	}

	descriptions := extractDescriptions(t, got)
	for i, d := range descriptions {
		if strings.Contains(d, "ANSWER-COWBOY-BEBOP") {
			t.Errorf("item %d description leaked the trivia answer: %q", i, d)
		}
	}
}

// TestSlack_FeedIsWellFormedXML protects against §5.6's validator
// requirement: Slack's own troubleshooting advice for a feed that silently
// stopped posting is to run it through the W3C validator, which starts with
// "does this even parse".
func TestSlack_FeedIsWellFormedXML(t *testing.T) {
	got, err := RSS(slackCompatChannel())
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	var doc struct {
		XMLName xml.Name `xml:"rss"`
	}
	if err := xml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("RSS() output is not well-formed XML: %v\n%s", err, got)
	}
}

// TestSlack_GuidIsPermaLinkFalse protects against relying on the RSS spec's
// isPermaLink="true" default: guid values here are Tag URIs, not
// dereferenceable links, so a reader that trusted the default would treat
// the guid itself as a broken permalink.
func TestSlack_GuidIsPermaLinkFalse(t *testing.T) {
	c := slackCompatChannel()
	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}
	doc := string(got)

	n := strings.Count(doc, `isPermaLink="false"`)
	if n != len(c.Items) {
		t.Errorf("expected %d occurrences of isPermaLink=\"false\" (one per item), got %d", len(c.Items), n)
	}
	if strings.Contains(doc, `isPermaLink="true"`) {
		t.Errorf("guid must never rely on isPermaLink=\"true\"")
	}
}

// TestSlack_IdenticalInputTimestampsAreRenderersProblemOrNot documents the
// ownership boundary from §5.5/§10: the renderer sorts by PublishedAt and
// emits whatever PublishedAt each item carries verbatim — it does not, and
// must not, rewrite timestamps to force them apart. Distinctness is
// guaranteed upstream by the store's UNIQUE(feed_id, published_at)
// constraint (PLAN.md §10, §5.5), which retries an insert at +1 second on
// collision. Feeding the renderer two items with an identical PublishedAt
// (a state the store should never actually produce) demonstrates that: the
// renderer emits both dates as-is, duplicate and all, rather than silently
// "fixing" them. This is a deliberate non-goal, not a gap — enforcing
// uniqueness in the renderer would let a caller believe the store's
// constraint doesn't matter.
func TestSlack_IdenticalInputTimestampsAreRenderersProblemOrNot(t *testing.T) {
	c := testutil.SampleChannel(2)
	same := c.Items[0].PublishedAt
	c.Items[1].PublishedAt = same

	got, err := RSS(c)
	if err != nil {
		t.Fatalf("RSS() error = %v", err)
	}

	dates := extractPubDates(t, got)
	if len(dates) != 2 {
		t.Fatalf("got %d pubDates, want 2", len(dates))
	}
	if !dates[0].Equal(same) || !dates[1].Equal(same) {
		t.Errorf("expected the renderer to pass PublishedAt through verbatim (duplicate included), got %s and %s", RFC822(dates[0]), RFC822(dates[1]))
	}
	// The renderer does not enforce distinctness; see the store's
	// UNIQUE(feed_id, published_at) constraint (§10) and its +1s retry
	// (§5.5) for where that guarantee actually lives.
}

// slackTableChannel is one entry in the table-driven multi-channel test
// below: a name plus a Channel built with a different item count/shape, so
// the same invariants are checked against more than one fixture rather than
// just the single hand-built rssTestChannel.
type slackTableChannel struct {
	name string
	c    model.Channel
}

// TestSlack_InvariantsAcrossChannels is the table-driven check called for by
// §5.6: a Slack-compatibility test that holds across several channels, not
// just one hand-picked fixture, since a bug that only shows up with a
// particular item count (e.g. a single-item feed, or many items) would
// otherwise slip through.
func TestSlack_InvariantsAcrossChannels(t *testing.T) {
	tables := []slackTableChannel{
		{name: "single item", c: testutil.SampleChannel(1)},
		{name: "several items", c: testutil.SampleChannel(5)},
		{name: "trivia item mixed with generated items", c: slackCompatChannel()},
	}

	for _, tc := range tables {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RSS(tc.c)
			if err != nil {
				t.Fatalf("RSS() error = %v", err)
			}
			doc := string(got)

			// Invariant 5: well-formed XML, or nothing else below means
			// anything.
			var x struct {
				XMLName xml.Name `xml:"rss"`
			}
			if err := xml.Unmarshal(got, &x); err != nil {
				t.Fatalf("RSS() output is not well-formed XML: %v\n%s", err, got)
			}

			// Invariants 1+2: every item has a parseable date, strictly
			// descending and unique across the feed.
			dates := extractPubDates(t, got)
			if len(dates) != len(tc.c.Items) {
				t.Fatalf("got %d parseable pubDates, want %d", len(dates), len(tc.c.Items))
			}
			seen := make(map[int64]bool, len(dates))
			for i, d := range dates {
				if d.IsZero() {
					t.Errorf("item %d has a zero-value pubDate", i)
				}
				if seen[d.Unix()] {
					t.Errorf("duplicate pubDate %s at index %d", RFC822(d), i)
				}
				seen[d.Unix()] = true
				if i > 0 && !dates[i-1].After(d) {
					t.Errorf("pubDate ordering violated at index %d: %s is not after %s", i, RFC822(dates[i-1]), RFC822(d))
				}
			}

			// Invariant 3: plain-text description, under the hard cap.
			descriptions := extractDescriptions(t, got)
			for i, d := range descriptions {
				if len(d) > 500 {
					t.Errorf("item %d description exceeds 500-char cap (%d chars)", i, len(d))
				}
				if strings.ContainsAny(d, "<>") {
					t.Errorf("item %d description contains unescaped markup: %q", i, d)
				}
			}

			// Invariant 4: no answer text in description. Only meaningful
			// for channels carrying a trivia item, but harmless to run
			// against every table entry — a feed with no trivia item simply
			// never matches.
			for i, it := range tc.c.Items {
				if !it.HasAnswer() {
					continue
				}
				for _, d := range descriptions {
					if strings.Contains(d, it.AnswerHTML) {
						t.Errorf("item %d description leaked its trivia answer", i)
					}
				}
			}

			// Invariant 6: guid always isPermaLink="false".
			n := strings.Count(doc, `isPermaLink="false"`)
			if n != len(tc.c.Items) {
				t.Errorf("expected %d isPermaLink=\"false\" guids, got %d", len(tc.c.Items), n)
			}
			if strings.Contains(doc, `isPermaLink="true"`) {
				t.Errorf("guid must never be isPermaLink=\"true\"")
			}
		})
	}
}

// extractPubDates walks the raw RSS bytes for every <pubDate> that appears
// inside an <item> (skipping the channel-level one, which is index 0 by
// construction) and parses each with the RFC822 layout, in document order.
// Parsing back through RFC822 rather than string-comparing is the point:
// Slack compares dates as dates, so a test that only string-compared could
// pass on a coincidental lexicographic ordering that isn't actually
// chronological (this can't happen with the fixed-width RFC822 layout used
// here, but parsing is what actually proves the values are valid dates at
// all, which is invariant 2's real requirement).
func extractPubDates(t *testing.T, doc []byte) []time.Time {
	t.Helper()

	var parsed struct {
		Channel struct {
			PubDate string `xml:"pubDate"`
			Items   []struct {
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("failed to parse RSS document: %v", err)
	}

	dates := make([]time.Time, 0, len(parsed.Channel.Items))
	for i, it := range parsed.Channel.Items {
		d, err := time.Parse(rfc822Layout, it.PubDate)
		if err != nil {
			t.Fatalf("item %d pubDate %q does not parse with the RFC822 layout: %v", i, it.PubDate, err)
		}
		dates = append(dates, d)
	}
	return dates
}

// extractDescriptions walks the raw RSS bytes for every item's
// <description>, in document order, via encoding/xml so the value comes
// back already un-escaped the way a real feed reader (and Slack) would see
// it — checking the raw bytes for hex escapes would test the wrong layer.
func extractDescriptions(t *testing.T, doc []byte) []string {
	t.Helper()

	var parsed struct {
		Channel struct {
			Items []struct {
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("failed to parse RSS document: %v", err)
	}

	out := make([]string, len(parsed.Channel.Items))
	for i, it := range parsed.Channel.Items {
		out[i] = it.Description
	}
	return out
}
