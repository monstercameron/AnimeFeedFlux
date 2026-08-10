package render

import (
	"strings"
	"unicode/utf8"
)

// EscapeText escapes a string for use inside a plain-text XML element (RSS
// title, description, etc.). It uses hexadecimal character references
// (&#x26;, &#x3C;, ...) rather than named entities (&amp;, &lt;, ...): the
// RSS Advisory Board's Best Practices Profile found hex references more
// widely supported by aggregators, so that's the encoding used everywhere in
// this codebase rather than mixing the two. Everything outside these five
// characters — CJK, emoji, any other valid UTF-8 — passes through unchanged;
// it's text an XML parser accepts as-is, not something to escape.
func EscapeText(s string) string { return textEscaper.Replace(sanitizeXMLText(s)) }

// sanitizeXMLText removes what XML 1.0 cannot represent, no matter how it is
// escaped.
//
// This is the last line before bytes reach a subscriber, and fuzzing proved it
// was missing: a NUL or a C0 control character in a model-generated title
// produced a document encoding/xml refuses to parse, and invalid UTF-8 produced
// one no parser accepts. Escaping does not help — there is no character
// reference for U+0000; the character is simply not allowed in an XML document
// (XML 1.0 §2.2 permits only #x9, #xA, #xD, #x20-#xD7FF, #xE000-#xFFFD and
// #x10000-#x10FFFF).
//
// Upstream validation should reject such input long before here (see
// generate.Validate), but a renderer that can emit an unparseable feed is a
// renderer that eventually will. Dropping the offending runes is the right
// failure: a title missing one invisible control character is still a title,
// whereas a feed that will not parse is nothing at all.
func sanitizeXMLText(s string) string {
	// Line endings are normalised FIRST, and this is not cosmetic. XML 1.0 §2.11
	// requires every parser to translate CRLF and lone CR to LF before the
	// application ever sees the text. Emitting a carriage return therefore
	// guarantees the subscriber reads something different from what we stored —
	// the round-trip is impossible, not merely unlikely. Normalising here makes
	// our stored text agree with what every reader will actually receive.
	// Fuzzing found this; no amount of review would have.
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	}

	if utf8.ValidString(s) && !strings.ContainsFunc(s, isInvalidXMLRune) {
		return s // overwhelmingly the common case; no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// RuneError from range-over-string means an invalid byte sequence.
		if r == utf8.RuneError || isInvalidXMLRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isInvalidXMLRune(r rune) bool {
	switch {
	case r == 0x09, r == 0x0A, r == 0x0D:
		return false
	case r >= 0x20 && r <= 0xD7FF:
		return false
	case r >= 0xE000 && r <= 0xFFFD:
		return false
	case r >= 0x10000 && r <= 0x10FFFF:
		return false
	}
	return true
}

// Built once. EscapeText runs per field, per item, per render, and a
// strings.Replacer is safe for concurrent use — constructing one per call was
// pure waste on the hottest path in the renderer.
var textEscaper = strings.NewReplacer(
	"&", "&#x26;",
	"<", "&#x3C;",
	">", "&#x3E;",
	`"`, "&#x22;",
	"'", "&#x27;",
)

// CDATA wraps HTML content (content:encoded) in a CDATA section. A CDATA
// section is terminated by the first "]]>" it contains, so a literal "]]>"
// inside the content — plausible in HTML that itself embeds a script or
// another CDATA block — would otherwise truncate the section early and spill
// the remainder into the surrounding XML as markup. The standard escape is
// to end the section just before the ">", open a new one starting with
// ">", and let XML concatenate adjacent CDATA sections back into one
// character-data string: "]]>" becomes "]]]]><![CDATA[>". The result is
// always a well-formed sequence of CDATA sections whose concatenated
// character data equals the input exactly.
func CDATA(s string) string {
	s = sanitizeXMLText(s)
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}
