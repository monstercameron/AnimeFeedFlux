package render

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// fuzzChannel wraps title/summary/body — arbitrary, fuzzer-controlled text —
// into a single-item Channel with everything else held fixed. Every other
// field (host, slug, dates, link) is a stable constant so a failure can only
// be attributed to the three fields under fuzz, never to fixture noise.
func fuzzChannel(title, summary, body string) model.Channel {
	item := model.Item{
		ItemKey:     "01J8XYZABCDEFGHJKMNPQRSTVW",
		Title:       title,
		SummaryText: summary,
		BodyHTML:    body,
		Link:        "https://anime.earlcameron.com/items/01J8XYZABCDEFGHJKMNPQRSTVW",
		PublishedAt: time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC),
	}
	return model.Channel{
		Feed: model.Feed{
			Slug:        "fuzz-feed",
			Title:       "Fuzz Feed",
			Description: "Fuzz target fixture",
			Language:    "en-us",
			TTLMinutes:  15,
		},
		SelfURL:   "https://anime.earlcameron.com/feeds/fuzz-feed.xml",
		HTMLURL:   "https://anime.earlcameron.com/feeds/fuzz-feed",
		Host:      "anime.earlcameron.com",
		TagYear:   2026,
		Items:     []model.Item{item},
		BuildTime: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		Generator: "AnimeFeedFlux v0.0.2-dev",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}

// fuzzSeeds are the nasty cases every one of the three targets seeds with:
// the characters EscapeText/CDATA specifically handle (&, <, ]]>), text that
// exercises multi-byte UTF-8 (CJK, emoji), a NUL byte and other C0 control
// characters, an unpaired UTF-16 surrogate (invalid as UTF-8 — Go strings
// permit the byte sequence anyway), and a very long string.
func fuzzSeeds() []string {
	return []string{
		"",
		"plain text",
		"&",
		"<",
		">",
		`"`,
		"'",
		"]]>",
		"<![CDATA[nested]]>",
		"&amp;&lt;&gt;",
		"日本語アニメ",       // CJK
		"アニメ 😀🎉🍥",      // CJK + emoji
		"\x00",         // NUL byte
		"a\x00b",       // NUL embedded mid-string
		"\x01\x02\x03", // C0 control characters
		"line1\nline2\ttabbed",
		string([]byte{0xED, 0xA0, 0x80}), // unpaired UTF-16 surrogate (U+D800), invalid UTF-8
		string([]byte{0xED, 0xBF, 0xBF}), // unpaired low surrogate, invalid UTF-8
		strings.Repeat("A very long title segment. ", 2000), // very long string
	}
}

// addSeeds registers the cross-product's diagonal (each seed in the title
// slot, paired with a fixed summary/body) plus a couple of seeds combined
// together, rather than the full cross-product — which would be seed^3 and
// mostly redundant, since each field goes through the exact same escapers.
func addSeeds(f *testing.F) {
	seeds := fuzzSeeds()
	for _, s := range seeds {
		f.Add(s, "a plain summary", "<p>a plain body</p>")
	}
	// A couple of cases with the nasty content in every field at once, since
	// CDATA (body) and hex-escaping (title/summary) are different code paths
	// and a bug could exist in only one.
	f.Add("Tom & Jerry <crossover>", "Summary with & and <tags>", "<p>Body ]]> with CDATA terminator</p>")
	f.Add("\x00\x01", "\x00\x01", "\x00\x01")
}

// FuzzRSSWellFormed asserts RSS() output is always well-formed XML, whatever
// title/summary/body content goes in, and that the title text round-trips
// byte-identical through render+decode. "Parses" alone would pass on a
// document that silently mangled or dropped the content; the round-trip is
// what actually proves nothing was lost, and it is the cheapest possible
// guard against an escaping bug reaching a subscriber (PLAN.md §17.3).
func FuzzRSSWellFormed(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, title, summary, body string) {
		c := fuzzChannel(title, summary, body)
		out, err := RSS(c)
		if err != nil {
			t.Fatalf("RSS() error = %v", err)
		}

		// Well-formedness: decode the whole document token by token so a
		// syntax error anywhere (not just in the fields Unmarshal happens to
		// reach) fails the fuzz case.
		dec := xml.NewDecoder(bytes.NewReader(out))
		for {
			_, terr := dec.Token()
			if terr != nil {
				if terr.Error() == "EOF" {
					break
				}
				t.Fatalf("RSS() output is not well-formed XML: %v\n%s", terr, out)
			}
		}

		var doc struct {
			Channel struct {
				Item struct {
					Title string `xml:"title"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if err := xml.Unmarshal(out, &doc); err != nil {
			t.Fatalf("RSS() output failed Unmarshal: %v\n%s", err, out)
		}
		// Compared against the SANITIZED title, not the raw fuzz input.
		//
		// The renderers deliberately strip what a feed cannot carry: XML 1.0
		// forbids NUL and most C0 controls outright — no character reference
		// exists for them — and no encoder can round-trip invalid UTF-8 through
		// JSON at all. Asserting the raw bytes survive would assert something
		// impossible rather than something desirable. What IS guaranteed, and
		// what this checks, is that every character a feed CAN carry comes back
		// byte-identical.
		want := sanitizeXMLText(title)
		if doc.Channel.Item.Title != want {
			t.Fatalf("RSS() title did not round-trip: got %q, want %q\n%s", doc.Channel.Item.Title, want, out)
		}
	})
}

// FuzzAtomWellFormed is FuzzRSSWellFormed's twin for Atom().
func FuzzAtomWellFormed(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, title, summary, body string) {
		c := fuzzChannel(title, summary, body)
		out, err := Atom(c)
		if err != nil {
			t.Fatalf("Atom() error = %v", err)
		}

		dec := xml.NewDecoder(bytes.NewReader(out))
		for {
			_, terr := dec.Token()
			if terr != nil {
				if terr.Error() == "EOF" {
					break
				}
				t.Fatalf("Atom() output is not well-formed XML: %v\n%s", terr, out)
			}
		}

		var doc struct {
			Entry struct {
				Title string `xml:"title"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(out, &doc); err != nil {
			t.Fatalf("Atom() output failed Unmarshal: %v\n%s", err, out)
		}
		// Sanitized, for the reason spelled out in FuzzRSSWellFormed.
		want := sanitizeXMLText(title)
		if doc.Entry.Title != want {
			t.Fatalf("Atom() title did not round-trip: got %q, want %q\n%s", doc.Entry.Title, want, out)
		}
	})
}

// FuzzJSONFeedValid asserts JSONFeed() output is always valid JSON, whatever
// title/summary/body content goes in, and that the title text round-trips
// byte-identical.
func FuzzJSONFeedValid(f *testing.F) {
	addSeeds(f)

	f.Fuzz(func(t *testing.T, title, summary, body string) {
		c := fuzzChannel(title, summary, body)
		out, err := JSONFeed(c)
		if err != nil {
			t.Fatalf("JSONFeed() error = %v", err)
		}

		if !json.Valid(out) {
			t.Fatalf("JSONFeed() output is not valid JSON: %s", out)
		}

		var doc struct {
			Items []struct {
				Title string `json:"title"`
			} `json:"items"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("JSONFeed() output failed Unmarshal: %v\n%s", err, out)
		}
		if len(doc.Items) != 1 {
			t.Fatalf("JSONFeed() got %d items, want 1\n%s", len(doc.Items), out)
		}
		// Sanitized, for the reason spelled out in FuzzRSSWellFormed. JSON adds
		// its own version of the problem: encoding/json substitutes U+FFFD for
		// invalid UTF-8 rather than erroring, so without sanitizing first the
		// three renderers would disagree about what the same item contains.
		title = sanitizeXMLText(title)
		if doc.Items[0].Title != title {
			t.Fatalf("JSONFeed() title did not round-trip after sanitizing: got %q, want %q\n%s", doc.Items[0].Title, title, out)
		}
	})
}
