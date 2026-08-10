// Package sanitize strips untrusted HTML (LLM-generated summaries, upstream
// RSS content) down to a small, fixed allowlist before it is stored or
// rendered into feeds, and again before it reaches Slack or third-party
// feed readers.
//
// This is a security boundary, not a formatting convenience. The rule is an
// allowlist of elements and attributes, never a denylist of "known bad"
// constructs — a denylist is a game played against every future attacker
// idea, and it is a game the defender loses. Any construct this package does
// not explicitly recognize as safe is dropped. When a code path is unsure
// whether something is safe, it MUST fail closed (drop it), never fail open
// (pass it through). A sanitizer that occasionally lets something through is
// worse than no sanitizer at all, because callers trust its output.
//
// The contract is about MARKUP, not about which bytes happen to appear in
// plain text. A string like "javascript:" or "onclick=" occurring as
// ordinary text content (an article discussing XSS, a variable named
// onClick) is not a disallowed construct — it is only dangerous once it is
// parsed as an href scheme or an attribute assignment, and this package
// guarantees neither can happen: no non-allowlisted tag ever appears, no
// attribute other than href on <a> ever appears, and href is scheme-checked
// before it survives. Do not "harden" this into a substring blocklist over
// the whole output; that mangles legitimate prose to catch nothing that the
// structural guarantees don't already catch, and reintroduces exactly the
// denylist failure mode this package exists to avoid.
package sanitize

import (
	"strconv"
	"strings"
)

// allowedTags is the complete set of elements that survive sanitization.
// Every other element's tags are stripped but its text content is kept,
// except for script and style (see stripTagsKeepText).
var allowedTags = map[string]bool{
	"p":          true,
	"em":         true,
	"strong":     true,
	"a":          true,
	"ul":         true,
	"ol":         true,
	"li":         true,
	"blockquote": true,
	"code":       true,
	"br":         true,
}

// rawTextTags are elements whose entire text content (not just the tags) is
// discarded. Keeping the "text" of a <script> is how a blocked script
// becomes visible garbage on the page, or a fresh injection if a downstream
// consumer re-parses it (e.g. a feed reader that is itself sloppy about
// escaping). Style content can carry expression()/url(javascript:) tricks
// in some parsers, so it gets the same treatment.
var rawTextTags = map[string]bool{
	"script": true,
	"style":  true,
}

// token kinds produced by the tokenizer.
type tokenKind int

const (
	tokenText tokenKind = iota
	tokenStartTag
	tokenEndTag
	tokenSelfClose // e.g. <br/> or <br>
	tokenComment
)

type token struct {
	kind  tokenKind
	name  string // lowercase tag name, for tag tokens
	attrs map[string]string
	text  string // raw text, for text tokens
}

// HTML sanitizes s, returning HTML containing only the allowlisted elements
// and attributes described in the package doc.
func HTML(s string) string {
	toks := tokenize(s)

	var out strings.Builder
	// stack of open *allowed* tag names, used to auto-close mismatched
	// or dangling tags at the end of input.
	var openStack []string
	// skipDepth > 0 means we are inside a rawTextTags element and must
	// discard everything — text and nested tags alike — until it closes.
	skipDepth := 0
	skipTag := ""

	for _, t := range toks {
		switch t.kind {
		case tokenComment:
			// Comments are dropped unconditionally: a comment can hide a
			// conditional-comment attack in some legacy renderers and
			// carries no legitimate content for our use case.
			continue

		case tokenText:
			if skipDepth > 0 {
				continue
			}
			// A genuine text run (the common case) never contains '<' —
			// tokenize() splits on it. The exception is the fallback
			// paths for malformed markup (an unclosed tag, a lone stray
			// '<'), which deliberately preserve the raw bytes rather
			// than guess at intent. Escaping '<' here closes the gap
			// that would otherwise let a mangled "<style" or "<script"
			// survive as literal text, ready for a lenient downstream
			// parser to reinterpret as a real tag.
			out.WriteString(strings.ReplaceAll(t.text, "<", "&lt;"))

		case tokenStartTag, tokenSelfClose:
			if skipDepth > 0 {
				// A tag nested inside <script>/<style> does not end the
				// skip; only its matching end tag (handled below) does.
				continue
			}
			if rawTextTags[t.name] {
				if t.kind == tokenStartTag {
					skipDepth = 1
					skipTag = t.name
				}
				continue
			}
			if !allowedTags[t.name] {
				// Not allowed and not a raw-text tag: drop the tag, keep
				// scanning (its text content, if any, is emitted by the
				// tokenText branch above/below).
				continue
			}
			out.WriteString(renderStartTag(t))
			if t.kind == tokenStartTag && t.name != "br" {
				openStack = append(openStack, t.name)
			}

		case tokenEndTag:
			if skipDepth > 0 {
				if t.name == skipTag {
					skipDepth = 0
					skipTag = ""
				}
				continue
			}
			if rawTextTags[t.name] {
				continue
			}
			if !allowedTags[t.name] || t.name == "br" {
				continue
			}
			// Only emit an end tag if it matches the innermost open
			// allowed tag; this prevents a stray/mismatched end tag from
			// closing something it didn't open.
			if idx := lastIndex(openStack, t.name); idx >= 0 {
				// Close everything opened after the matching tag too,
				// so output nests correctly even if the input didn't.
				for i := len(openStack) - 1; i >= idx; i-- {
					out.WriteString("</" + openStack[i] + ">")
				}
				openStack = openStack[:idx]
			}
			// else: unmatched end tag, drop silently.
		}
	}

	// Auto-close anything still open at end of input (unclosed tags).
	for i := len(openStack) - 1; i >= 0; i-- {
		out.WriteString("</" + openStack[i] + ">")
	}

	return out.String()
}

func lastIndex(stack []string, name string) int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == name {
			return i
		}
	}
	return -1
}

// renderStartTag emits an allowed start tag with only its allowed,
// validated attributes.
func renderStartTag(t token) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(t.name)

	if t.name == "a" {
		if href, ok := t.attrs["href"]; ok {
			if safe, ok2 := safeHref(href); ok2 {
				b.WriteString(` href="`)
				b.WriteString(escapeAttr(safe))
				b.WriteString(`" rel="nofollow noopener" target="_blank"`)
			}
		}
	}
	// All other attributes (including every on* handler and style) are
	// dropped unconditionally: there is no allowlisted use for them here.

	if t.kind == tokenSelfClose || t.name == "br" {
		b.WriteString(" />")
	} else {
		b.WriteByte('>')
	}
	return b.String()
}

// safeHref validates href against the allowlist: only absolute http/https
// URLs survive. Scheme-relative ("//evil.com"), relative ("/x", "x"),
// javascript:, data:, vbscript:, and any other scheme are rejected — a
// missing or non-http(s) scheme is not "probably fine", it is unproven,
// and unproven fails closed here.
func safeHref(href string) (string, bool) {
	h := strings.TrimSpace(href)
	if h == "" {
		return "", false
	}
	// Strip control characters and stray whitespace that browsers are
	// historically lenient about (e.g. "java\tscript:") before scheme
	// inspection, so an attacker cannot smuggle a scheme past a naive
	// prefix check.
	var cleaned strings.Builder
	for _, r := range h {
		if r <= 0x20 {
			continue
		}
		cleaned.WriteRune(r)
	}
	h = cleaned.String()

	lower := strings.ToLower(h)
	switch {
	case strings.HasPrefix(lower, "http://"):
	case strings.HasPrefix(lower, "https://"):
	default:
		return "", false
	}
	return h, true
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// decodeEntities resolves the small set of HTML entities that matter for
// attribute values, before the value is inspected or stored. Two reasons:
//
//  1. escapeAttr always re-encodes "&" as "&amp;" when an href is emitted.
//     Without decoding on the way in, sanitizing already-sanitized output
//     (HTML(HTML(s))) would double-escape a legitimate "?a=1&amp;b=2" into
//     "&amp;amp;", changing the output on every re-sanitization pass —
//     breaking idempotence, which callers are entitled to rely on.
//  2. Decoding before safeHref's scheme check means an attacker cannot
//     use an entity-encoded character to alter how the scheme reads
//     (e.g. an encoded character inside "http://..." must still decode to
//     something safeHref would accept unescaped; it gains nothing).
//
// Unknown or malformed entities are left as literal text — decoding is
// conservative, never a guess.
func decodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 12 { // longest handled entity is short; bail early on runaway scans
			b.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i : i+semi+1] // includes leading '&' and trailing ';'
		if r, ok := namedEntities[ent]; ok {
			b.WriteRune(r)
			i += len(ent)
			continue
		}
		if strings.HasPrefix(ent, "&#") {
			body := ent[2 : len(ent)-1]
			var code int64
			var err error
			if len(body) > 1 && (body[0] == 'x' || body[0] == 'X') {
				code, err = strconv.ParseInt(body[1:], 16, 32)
			} else {
				code, err = strconv.ParseInt(body, 10, 32)
			}
			if err == nil && code > 0 && code <= 0x10FFFF {
				b.WriteRune(rune(code))
				i += len(ent)
				continue
			}
		}
		// Not a recognized entity: emit the '&' literally and continue —
		// do not consume input we didn't understand.
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

var namedEntities = map[string]rune{
	"&amp;":  '&',
	"&lt;":   '<',
	"&gt;":   '>',
	"&quot;": '"',
	"&apos;": '\'',
}

// tokenize performs a minimal, conservative HTML tokenization pass. It is
// not a general HTML parser: it recognizes tags, comments, and text, and it
// is deliberately strict about what counts as a well-formed tag. Anything
// ambiguous (an unclosed tag at end of input, a tag name it cannot cleanly
// read) is treated as plain text rather than guessed at, because guessing
// wrong here means guessing open.
func tokenize(s string) []token {
	var toks []token
	i := 0
	n := len(s)

	for i < n {
		if s[i] != '<' {
			j := strings.IndexByte(s[i:], '<')
			if j < 0 {
				toks = append(toks, token{kind: tokenText, text: s[i:]})
				break
			}
			toks = append(toks, token{kind: tokenText, text: s[i : i+j]})
			i += j
			continue
		}

		// s[i] == '<'
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i:], "-->")
			if end < 0 {
				// Unterminated comment: drop the rest of the input rather
				// than risk emitting a fragment that could smuggle
				// content out of "comment" context in a lenient parser.
				toks = append(toks, token{kind: tokenComment, text: s[i:]})
				break
			}
			toks = append(toks, token{kind: tokenComment, text: s[i : i+end+3]})
			i += end + 3
			continue
		}

		// Any other "<!" construct (DOCTYPE, CDATA, etc.) — not part of
		// the allowlist grammar; treat as an unrecognized tag-like thing
		// and drop it wholesale up to '>' if found, else drop the rest.
		if strings.HasPrefix(s[i:], "<!") {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}

		// Processing instructions / anything starting "<?"
		if strings.HasPrefix(s[i:], "<?") {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}

		closing := false
		j := i + 1
		if j < n && s[j] == '/' {
			closing = true
			j++
		}

		// Whitespace is allowed before a tag name in real HTML ("< a>"
		// is not, but "</ a>" and leading space inside are handled by
		// consumers loosely); we accept optional whitespace here too so
		// a deliberately-mangled "< script>" doesn't slip past as text
		// and get emitted verbatim.
		nameStart := j
		for nameStart < n && isSpace(s[nameStart]) {
			nameStart++
		}

		nameEnd := nameStart
		for nameEnd < n && isNameByte(s[nameEnd]) {
			nameEnd++
		}

		if nameEnd == nameStart {
			// No tag name at all ("<>", "< >", "<3 oclock" style stray
			// '<') — not a tag; treat the '<' itself as literal text and
			// move on one byte at a time so we don't lose input.
			toks = append(toks, token{kind: tokenText, text: "<"})
			i++
			continue
		}

		name := strings.ToLower(s[nameStart:nameEnd])

		// Find the end of the tag, respecting quoted attribute values so
		// a '>' inside a quoted string doesn't terminate the tag early.
		k := nameEnd
		var attrs map[string]string
		var quote byte
		tagClosed := false
		selfClose := false
		for k < n {
			c := s[k]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				k++
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
				k++
				continue
			}
			if c == '>' {
				tagClosed = true
				break
			}
			k++
		}

		if !tagClosed {
			// Malformed / unclosed tag: conservatively treat the whole
			// remainder as text rather than guess where it ends, so
			// nothing after an unterminated '<' is ever interpreted as
			// markup.
			toks = append(toks, token{kind: tokenText, text: s[i:]})
			break
		}

		raw := s[nameEnd:k] // attribute region, before the closing '>'
		if strings.HasSuffix(strings.TrimRight(raw, " \t\r\n"), "/") {
			selfClose = true
			raw = strings.TrimRight(raw, " \t\r\n")
			raw = raw[:len(raw)-1]
		}
		attrs = parseAttrs(raw)

		switch {
		case closing:
			toks = append(toks, token{kind: tokenEndTag, name: name})
		case selfClose:
			toks = append(toks, token{kind: tokenSelfClose, name: name, attrs: attrs})
		default:
			toks = append(toks, token{kind: tokenStartTag, name: name, attrs: attrs})
		}
		i = k + 1
	}

	return toks
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isNameByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == ':' || c == '_'
}

// parseAttrs parses a raw "name=value name2='v2' bare" attribute region
// into a lowercase-keyed map. It is deliberately tolerant of mismatched or
// missing quotes (attackers rely on parser-differential leniency), but
// tolerance here only ever means "extract something plausible", never
// "extract something we then trust" — every value still goes through
// safeHref before use.
func parseAttrs(raw string) map[string]string {
	attrs := map[string]string{}
	i := 0
	n := len(raw)
	for i < n {
		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && !isSpace(raw[i]) && raw[i] != '=' {
			i++
		}
		if start == i {
			i++
			continue
		}
		key := strings.ToLower(raw[start:i])

		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n || raw[i] != '=' {
			attrs[key] = ""
			continue
		}
		i++ // consume '='
		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n {
			attrs[key] = ""
			break
		}

		var val string
		if raw[i] == '"' || raw[i] == '\'' {
			q := raw[i]
			i++
			vstart := i
			for i < n && raw[i] != q {
				i++
			}
			val = raw[vstart:i]
			if i < n {
				i++ // consume closing quote
			}
			// Mismatched quote (opened with " but never closed before
			// end of region, or vice versa): val already captured
			// whatever was available; nothing further to recover.
		} else {
			vstart := i
			for i < n && !isSpace(raw[i]) {
				i++
			}
			val = raw[vstart:i]
		}
		attrs[key] = decodeEntities(val)
	}
	return attrs
}
