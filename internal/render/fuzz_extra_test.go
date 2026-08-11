package render

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedvalidate"
)

// minYear1Sec/maxYear9999Sec bound the Unix-seconds domain to the UTC year
// range PLAN.md §5.1 promises ("RFC 822 with four-digit years") — 0001-01-01
// through 9999-12-31 23:59:59. See FuzzDateFormattersNeverCross's doc
// comment for why the domain is clamped there rather than left unbounded.
const (
	minYear1Sec    = -62135596800
	maxYear9999Sec = 253402300799
)

// ---- Round-trip + cross-format agreement (PLAN.md §17.3) ----
//
// fuzz_test.go's existing targets prove each renderer's output is
// well-formed and that TITLE alone survives. That leaves the stronger claim
// unchecked: that description/summary, the HTML body, the guid/id, and the
// published instant all survive too, and that the three formats agree with
// each other about every one of those fields for the SAME channel. A
// document can be perfectly well-formed and still say the wrong thing, and
// a subscriber who picks the Atom URL over the RSS one should never see
// different content.

// rssParsedDoc/atomParsedDoc/jsonParsedDoc pull every field this target
// checks out of each format's actual wire shape (namespaced content:encoded,
// namespaced Atom elements, JSON Feed's item object) rather than a
// convenience subset, so a divergence in any one of them is caught.
type rssParsedDoc struct {
	Channel struct {
		Item struct {
			Title   string `xml:"title"`
			Desc    string `xml:"description"`
			Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
			Guid    string `xml:"guid"`
			PubDate string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomParsedDoc struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	Entry   struct {
		ID      string `xml:"http://www.w3.org/2005/Atom id"`
		Title   string `xml:"http://www.w3.org/2005/Atom title"`
		Updated string `xml:"http://www.w3.org/2005/Atom updated"`
		Summary string `xml:"http://www.w3.org/2005/Atom summary"`
		Content string `xml:"http://www.w3.org/2005/Atom content"`
	} `xml:"http://www.w3.org/2005/Atom entry"`
}

type jsonParsedDoc struct {
	Items []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		ContentHTML   string `json:"content_html"`
		Summary       string `json:"summary"`
		DatePublished string `json:"date_published"`
	} `json:"items"`
}

// FuzzCrossFormatRoundTrip renders the same channel three ways and checks,
// per field, both that the value round-tripped from the fuzzer's input AND
// that all three formats agree with each other. The independent "want"
// values (sanitizeXMLText(input), TagURI(...)) are computed the same way
// the renderers themselves compute them so this is checking the renderers
// against their own documented contract, not against each other's possibly
// shared bug.
//
// The fourth fuzzed field, answer, is what makes this target actually
// exercise the trivia-answer path. Before this field existed, every item
// this target built had AnswerHTML == "" (fuzzChannel never set it), so
// model.Item.HasAnswer() was false for every single case the fuzzer ever
// tried — the "answer appended" branch in each renderer's content-building
// code never ran here at all, cross-format agreement or not. That's exactly
// how this target passed while RSS/Atom appended the trivia answer to their
// content and JSON Feed's content_html silently omitted it (verified against
// the live dev feeds, PLAN.md §5.1/§5.2/§5.3): the divergent branch was
// simply never reached. An `answer` seed of "" still covers the no-answer
// path this target always covered; non-empty seeds (including the nasty
// escaping cases) now cover the answer path too, and wantContent below
// folds it in via the exact same itemBodyWithAnswer helper the renderers
// use, so a renderer that special-cases the answer differently from that
// helper — or omits it, as JSON Feed did — fails here.
func FuzzCrossFormatRoundTrip(f *testing.F) {
	seeds := fuzzSeeds()
	for _, s := range seeds {
		f.Add(s, "a plain summary", "<p>a plain body</p>", "")
		f.Add("a plain title", "a plain summary", "<p>a plain body</p>", s)
	}
	f.Add("Tom & Jerry <crossover>", "Summary with & and <tags>", "<p>Body ]]> with CDATA terminator</p>", "<p>Answer with ]]> and & and <tags></p>")
	f.Add("\x00\x01", "\x00\x01", "\x00\x01", "\x00\x01")

	f.Fuzz(func(t *testing.T, title, summary, body, answer string) {
		c := fuzzChannel(title, summary, body)
		c.Items[0].AnswerHTML = answer
		it := c.Items[0]

		rssOut, err := RSS(c)
		if err != nil {
			t.Fatalf("RSS() error = %v", err)
		}
		atomOut, err := Atom(c)
		if err != nil {
			t.Fatalf("Atom() error = %v", err)
		}
		jsonOut, err := JSONFeed(c)
		if err != nil {
			t.Fatalf("JSONFeed() error = %v", err)
		}

		var rssDoc rssParsedDoc
		if err := xml.Unmarshal(rssOut, &rssDoc); err != nil {
			t.Fatalf("RSS() output failed Unmarshal: %v\n%s", err, rssOut)
		}
		var atomDoc atomParsedDoc
		if err := xml.Unmarshal(atomOut, &atomDoc); err != nil {
			t.Fatalf("Atom() output failed Unmarshal: %v\n%s", err, atomOut)
		}
		var jsonDoc jsonParsedDoc
		if err := json.Unmarshal(jsonOut, &jsonDoc); err != nil {
			t.Fatalf("JSONFeed() output failed Unmarshal: %v\n%s", err, jsonOut)
		}
		if len(jsonDoc.Items) != 1 {
			t.Fatalf("JSONFeed() got %d items, want 1\n%s", len(jsonDoc.Items), jsonOut)
		}
		jsonItem := jsonDoc.Items[0]

		wantTitle := sanitizeXMLText(title)
		wantSummary := sanitizeXMLText(summary)
		// wantBody is computed through itemBodyWithAnswer, the same helper
		// RSS/Atom/JSONFeed all call, so this asserts the renderers against
		// their own documented contract (body plus, for trivia items, the
		// answer after a spoiler-break marker) rather than against a "want"
		// that silently ignores AnswerHTML the way the pre-fix renderers'
		// content-building code disagreed about.
		wantBody := sanitizeXMLText(itemBodyWithAnswer(it))
		wantGuid := TagURI(c.Host, c.TagYear, c.Feed.Slug, it.ItemKey)

		// --- Round-trip: each format's own text against the fuzzer's input ---
		if rssDoc.Channel.Item.Title != wantTitle {
			t.Fatalf("RSS title round-trip: got %q, want %q", rssDoc.Channel.Item.Title, wantTitle)
		}
		if rssDoc.Channel.Item.Desc != wantSummary {
			t.Fatalf("RSS description round-trip: got %q, want %q", rssDoc.Channel.Item.Desc, wantSummary)
		}
		if rssDoc.Channel.Item.Content != wantBody {
			t.Fatalf("RSS content:encoded round-trip: got %q, want %q", rssDoc.Channel.Item.Content, wantBody)
		}
		if rssDoc.Channel.Item.Guid != wantGuid {
			t.Fatalf("RSS guid: got %q, want %q", rssDoc.Channel.Item.Guid, wantGuid)
		}

		if atomDoc.Entry.Title != wantTitle {
			t.Fatalf("Atom title round-trip: got %q, want %q", atomDoc.Entry.Title, wantTitle)
		}
		if atomDoc.Entry.Summary != wantSummary {
			t.Fatalf("Atom summary round-trip: got %q, want %q", atomDoc.Entry.Summary, wantSummary)
		}
		if atomDoc.Entry.Content != wantBody {
			t.Fatalf("Atom content round-trip: got %q, want %q", atomDoc.Entry.Content, wantBody)
		}
		if atomDoc.Entry.ID != wantGuid {
			t.Fatalf("Atom id: got %q, want %q", atomDoc.Entry.ID, wantGuid)
		}

		if jsonItem.Title != wantTitle {
			t.Fatalf("JSONFeed title round-trip: got %q, want %q", jsonItem.Title, wantTitle)
		}
		if jsonItem.Summary != wantSummary {
			t.Fatalf("JSONFeed summary round-trip: got %q, want %q", jsonItem.Summary, wantSummary)
		}
		if jsonItem.ContentHTML != wantBody {
			t.Fatalf("JSONFeed content_html round-trip: got %q, want %q", jsonItem.ContentHTML, wantBody)
		}
		if jsonItem.ID != wantGuid {
			t.Fatalf("JSONFeed id: got %q, want %q", jsonItem.ID, wantGuid)
		}

		// --- Cross-format agreement: all three must say the same thing ---
		if rssDoc.Channel.Item.Guid != atomDoc.Entry.ID || atomDoc.Entry.ID != jsonItem.ID {
			t.Fatalf("guid/id disagree across formats: rss=%q atom=%q json=%q",
				rssDoc.Channel.Item.Guid, atomDoc.Entry.ID, jsonItem.ID)
		}

		// The published instant: RSS emits RFC822, Atom/JSON emit RFC3339 —
		// different formatters (RULE-5), but they must describe the exact
		// same moment for the same item.
		rssTime, err := time.Parse(rfc822Layout, rssDoc.Channel.Item.PubDate)
		if err != nil {
			t.Fatalf("RSS pubDate does not parse as RFC822: %q: %v", rssDoc.Channel.Item.PubDate, err)
		}
		atomTime, err := time.Parse(rfc3339Layout, atomDoc.Entry.Updated)
		if err != nil {
			t.Fatalf("Atom updated does not parse as RFC3339: %q: %v", atomDoc.Entry.Updated, err)
		}
		jsonTime, err := time.Parse(rfc3339Layout, jsonItem.DatePublished)
		if err != nil {
			t.Fatalf("JSONFeed date_published does not parse as RFC3339: %q: %v", jsonItem.DatePublished, err)
		}
		if !rssTime.Equal(atomTime) || !atomTime.Equal(jsonTime) {
			t.Fatalf("published instant disagrees across formats: rss=%v atom=%v json=%v", rssTime, atomTime, jsonTime)
		}
		if !rssTime.Equal(it.PublishedAt) {
			t.Fatalf("published instant does not match source: got %v, want %v", rssTime, it.PublishedAt)
		}
	})
}

// ---- Date formatters never cross (RULE-5, PLAN.md §17.3) ----

// FuzzDateFormattersNeverCross fuzzes arbitrary instants — including the
// edge cases that actually break a hand-rolled layout string: pre-1970
// (negative Unix seconds), year > 9999, sub-second precision the layout
// does not print, and non-UTC zones with unusual offsets (fractional-hour,
// negative, > 12h) — and asserts three things hold for every one of them:
//
//  1. RFC822's output parses back with the RFC822 layout and RFC3339's
//     output parses back with the RFC3339 layout.
//  2. Neither output parses as the OTHER format (time.Parse is lenient
//     about extra/missing components in ways a byte-diff would catch but a
//     naive "did it parse" check would not).
//  3. Both, reparsed, describe the exact same instant as the input
//     (truncated to whole seconds, since neither layout prints a fraction) —
//     so the two formatters never silently disagree about "when".
//
// A manual probe (not asserted here, deliberately — see the domain-clamping
// comment below) found that BOTH formatters fail to round-trip a five-digit
// year (year 10000): RFC822(instant) and RFC3339(instant) each produce a
// string their own layout's time.Parse then rejects, because Go's reference
// layouts ("2006"/"2006") read a fixed four digits for the year no matter
// how many Format actually wrote. This is a real, reportable limitation —
// but it is symmetric (both formatters break identically, so it can never
// silently disagree with itself the way RULE-5 cares about) and it is also
// squarely outside what PLAN.md §5.1 promises ("RFC 822 with four-digit
// years"): a five-digit year is out of spec by definition, not a
// spec-conformant instant our formatters mishandle. Nothing upstream bounds
// PublishedAt, so it is not impossible for a bad clock or a store bug to
// hand a renderer such a value; if that ever needs hardening, the fix
// belongs in validation before storage (like generate.Validate's UTF-8/
// control-char checks), not in these formatters. Flagging it here rather
// than silently: fix if/when it matters, this is not forgotten.
func FuzzDateFormattersNeverCross(f *testing.F) {
	seeds := []struct {
		sec        int64
		nsec       int32
		offsetMins int32
	}{
		{0, 0, 0},                    // epoch
		{minYear1Sec, 0, 0},          // year 0001-01-01, the four-digit-year floor
		{-1, 999999999, 0},           // just before epoch, sub-second
		{-86400, 0, -300},            // pre-1970, non-UTC negative offset
		{maxYear9999Sec, 0, 0},       // 9999-12-31 23:59:59 UTC, the four-digit-year ceiling
		{maxYear9999Sec, 0, 570},     // same ceiling, +09:30 (unusual half-hour offset)
		{1719999999, 500000000, 825}, // sub-second + +13:45 (Chatham Islands-style offset)
		{1719999999, 1, -720},        // 1ns, -12:00
		{1719999999, 0, -570},        // -09:30
	}
	for _, s := range seeds {
		f.Add(s.sec, s.nsec, s.offsetMins)
	}

	f.Fuzz(func(t *testing.T, sec int64, nsec int32, offsetMins int32) {
		// Clamp nsec into time.Unix's valid range and offset to something a
		// real zone could plausibly use (real-world zones run -12:00..+14:00;
		// widen slightly to also exercise values just outside that to make
		// sure the formatter still normalizes to UTC rather than misbehaving).
		nsec = nsec % 1000000000
		if nsec < 0 {
			nsec += 1000000000
		}
		offsetMins = offsetMins % (20 * 60)

		// Clamp sec into the year range PLAN.md §5.1 actually promises
		// ("four-digit years"), rather than the full int64 domain. Both
		// formatters call .UTC() before formatting (dates.go), so the year
		// that matters is the UTC year, independent of offsetMins — clamping
		// sec alone is sufficient. See the five-digit-year note above for why
		// this boundary is deliberate and not a cop-out: it is the actual
		// contract, and both formatters were manually confirmed to fail
		// identically (not divergently) just outside it.
		var yearRange int64 = maxYear9999Sec - minYear1Sec + 1
		mod := sec % yearRange
		if mod < 0 {
			mod += yearRange
		}
		sec = minYear1Sec + mod

		loc := time.FixedZone("FUZZ", int(offsetMins)*60)
		instant := time.Unix(sec, int64(nsec)).In(loc)

		got822 := RFC822(instant)
		got3339 := RFC3339(instant)

		if got822 == got3339 {
			t.Fatalf("RFC822 and RFC3339 produced identical output %q for %v", got822, instant)
		}

		parsed822, err := time.Parse(rfc822Layout, got822)
		if err != nil {
			t.Fatalf("RFC822(%v) = %q does not parse back with the RFC822 layout: %v", instant, got822, err)
		}
		parsed3339, err := time.Parse(rfc3339Layout, got3339)
		if err != nil {
			t.Fatalf("RFC3339(%v) = %q does not parse back with the RFC3339 layout: %v", instant, got3339, err)
		}

		if _, err := time.Parse(rfc3339Layout, got822); err == nil {
			t.Fatalf("RFC822 output %q unexpectedly parsed as RFC3339", got822)
		}
		if _, err := time.Parse(rfc822Layout, got3339); err == nil {
			t.Fatalf("RFC3339 output %q unexpectedly parsed as RFC822", got3339)
		}

		wantUTC := instant.UTC().Truncate(time.Second)
		if !parsed822.Equal(wantUTC) {
			t.Fatalf("RFC822 round-trip instant mismatch: got %v, want %v (input %v, formatted %q)", parsed822, wantUTC, instant, got822)
		}
		if !parsed3339.Equal(wantUTC) {
			t.Fatalf("RFC3339 round-trip instant mismatch: got %v, want %v (input %v, formatted %q)", parsed3339, wantUTC, instant, got3339)
		}
	})
}

// ---- The validator agrees with the renderers (PLAN.md §17.3) ----

// FuzzValidatorAgreesWithRenderers asserts that whatever internal/render
// emits from arbitrary title/summary/body content, internal/feedvalidate
// raises no Error-level finding against it. A failure here is a real bug in
// one of the two packages — either the renderer produced something that
// violates the very spec it claims to implement, or the validator is
// flagging output that is actually fine — and either answer is worth
// knowing (PLAN.md §17.3).
//
// Two preconditions are skipped rather than asserted, both for the same
// reason: they are not spec-valid inputs the renderer is contracted to
// handle, because generate.Validate rejects them before an item ever
// reaches the store, let alone a renderer — and render's own package doc
// states it "deliberately does NOT sanitize... Item and feed text is
// assumed already safe... by the time it reaches this package":
//
//   - Title AND summary both sanitizing to empty. generate.Validate rejects
//     an empty title (ReasonTitleRequired) and an empty summary_text
//     (ReasonSummaryRequired). Confirmed: without this skip, RSS genuinely
//     fails §5.1's item-title-or-description rule for this input — a real
//     effect, but of feeding render() something generate.Validate would
//     never let through, not of render and feedvalidate disagreeing about
//     a real item.
//   - Body sanitizing to empty. generate.Validate rejects an empty
//     body_html (ReasonBodyRequired) — confirmed independently: fuzzing
//     this very target found body="" makes JSON Feed's content_html an
//     empty string, which fails §5.3's item-content-present rule, because
//     JSONFeed() (jsonfeed.go) always emits content_html and never
//     content_text on the stated assumption that BodyHTML is always
//     populated by the time it arrives.
//
// Both checks trim whitespace before comparing to "", matching
// generate.Validate's own strings.TrimSpace(...) == "" checks: fuzzing this
// target also found title=" "/summary=" " (a lone space, not empty)
// tripping the same §5.1 finding, because sanitizeXMLText does not strip
// plain spaces and an untrimmed check missed it.
//
// Skipping these specific combinations is scoping the property to its
// actual domain, not weakening what it asserts within that domain.
func FuzzValidatorAgreesWithRenderers(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, title, summary, body string) {
		if strings.TrimSpace(sanitizeXMLText(title)) == "" && strings.TrimSpace(sanitizeXMLText(summary)) == "" {
			t.Skip("title and summary both sanitize to blank: outside render's contracted input domain (generate.Validate rejects this upstream)")
		}
		if strings.TrimSpace(sanitizeXMLText(body)) == "" {
			t.Skip("body sanitizes to blank: outside render's contracted input domain (generate.Validate rejects an empty body_html upstream)")
		}
		c := fuzzChannel(title, summary, body)

		rssOut, err := RSS(c)
		if err != nil {
			t.Fatalf("RSS() error = %v", err)
		}
		if findings := errorFindings(feedvalidate.RSS(rssOut)); len(findings) > 0 {
			t.Fatalf("feedvalidate.RSS found errors in renderer output: %v\n%s", findings, rssOut)
		}

		atomOut, err := Atom(c)
		if err != nil {
			t.Fatalf("Atom() error = %v", err)
		}
		if findings := errorFindings(feedvalidate.Atom(atomOut)); len(findings) > 0 {
			t.Fatalf("feedvalidate.Atom found errors in renderer output: %v\n%s", findings, atomOut)
		}

		jsonOut, err := JSONFeed(c)
		if err != nil {
			t.Fatalf("JSONFeed() error = %v", err)
		}
		if findings := errorFindings(feedvalidate.JSONFeed(jsonOut)); len(findings) > 0 {
			t.Fatalf("feedvalidate.JSONFeed found errors in renderer output: %v\n%s", findings, jsonOut)
		}
	})
}

func errorFindings(findings []feedvalidate.Finding) []feedvalidate.Finding {
	var errs []feedvalidate.Finding
	for _, f := range findings {
		if f.Level == feedvalidate.Error {
			errs = append(errs, f)
		}
	}
	return errs
}
