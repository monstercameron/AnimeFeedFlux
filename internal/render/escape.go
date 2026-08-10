package render

import "strings"

// EscapeText escapes a string for use inside a plain-text XML element (RSS
// title, description, etc.). It uses hexadecimal character references
// (&#x26;, &#x3C;, ...) rather than named entities (&amp;, &lt;, ...): the
// RSS Advisory Board's Best Practices Profile found hex references more
// widely supported by aggregators, so that's the encoding used everywhere in
// this codebase rather than mixing the two. Everything outside these five
// characters — CJK, emoji, any other valid UTF-8 — passes through unchanged;
// it's text an XML parser accepts as-is, not something to escape.
func EscapeText(s string) string { return textEscaper.Replace(s) }

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
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}
