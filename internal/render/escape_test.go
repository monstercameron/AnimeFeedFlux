package render

import (
	"encoding/xml"
	"testing"
)

// TestEscapeText pins the exact hex-reference output for each special
// character, since the choice of hex over named entities (§5.1) is only
// meaningful if the output is actually "&#x26;" and not "&amp;".
func TestEscapeText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ampersand", "&", "&#x26;"},
		{"lt", "<", "&#x3C;"},
		{"gt", ">", "&#x3E;"},
		{"double_quote", `"`, "&#x22;"},
		{"single_quote", "'", "&#x27;"},
		{"empty", "", ""},
		{"cjk_passthrough", "新しいアニメ", "新しいアニメ"},
		{"emoji_passthrough", "🎌🍜", "🎌🍜"},
		{"mixed", `Tom & Jerry <s>"cat"</s> it's fun`,
			"Tom &#x26; Jerry &#x3C;s&#x3E;&#x22;cat&#x22;&#x3C;/s&#x3E; it&#x27;s fun"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EscapeText(tc.in)
			if got != tc.want {
				t.Errorf("EscapeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCDATANoSpecialSequence checks the plain wrapping case, where no split
// is needed.
func TestCDATANoSpecialSequence(t *testing.T) {
	in := "<p>hello & welcome</p>"
	want := "<![CDATA[<p>hello & welcome</p>]]>"
	got := CDATA(in)
	if got != want {
		t.Errorf("CDATA(%q) = %q, want %q", in, got, want)
	}
}

// TestCDATAEmpty checks the empty-string edge case produces an empty CDATA
// section rather than, say, nothing at all.
func TestCDATAEmpty(t *testing.T) {
	want := "<![CDATA[]]>"
	if got := CDATA(""); got != want {
		t.Errorf("CDATA(\"\") = %q, want %q", got, want)
	}
}

// roundTripCDATA is the assertion that actually matters for the "]]>"
// split: a string-equality check on the escaped form would let a subtly
// wrong split pass silently, since the split points are a matter of taste
// as long as concatenation is preserved. Instead this parses the CDATA
// output as real XML character data and compares the decoded result to the
// original input.
func roundTripCDATA(t *testing.T, in string) {
	t.Helper()
	doc := "<root>" + CDATA(in) + "</root>"

	var out struct {
		Data string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("xml.Unmarshal(%q) failed: %v\ndoc: %s", in, err, doc)
	}
	if out.Data != in {
		t.Errorf("round-trip mismatch: got %q, want %q\ndoc: %s", out.Data, in, doc)
	}
}

// TestCDATAWithTerminatorSequence is the critical case: content containing
// "]]>" must survive an actual XML parse unchanged.
func TestCDATAWithTerminatorSequence(t *testing.T) {
	in := `before ]]> after`
	roundTripCDATA(t, in)

	want := "<![CDATA[before ]]]]><![CDATA[> after]]>"
	if got := CDATA(in); got != want {
		t.Errorf("CDATA(%q) = %q, want %q", in, got, want)
	}
}

// TestCDATARoundTripTable exercises the tricky inputs called out in §5.6's
// golden-file list: ampersands, angle brackets, CJK, emoji, and "]]>" in
// body HTML, plus a few adversarial variants of the terminator sequence
// itself (leading, trailing, adjacent, and repeated).
func TestCDATARoundTripTable(t *testing.T) {
	cases := []string{
		"",
		"plain text, no markup",
		"<p>HTML with an ampersand & a <b>bold</b> tag</p>",
		"新しいアニメエピソードが公開されました",
		"trivia night 🎉🍿 spoilers below",
		"]]>",
		"]]>]]>",
		"a]]>b]]>c",
		"leading ]]> then text",
		"text then trailing ]]>",
		"]]]]>",
		"nested <![CDATA[ looks like cdata ]]> but isn't",
		"multiple ]]> terminators ]]> in one string ]]> here",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			roundTripCDATA(t, in)
		})
	}
}
