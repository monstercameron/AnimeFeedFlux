package i18nlint

import (
	"math"
	"regexp"
	"strings"
)

// placeholderRe matches the two interpolation shapes this repo's strings
// are expected to use: printf verbs (%s, %d, %.2f, %[1]s, %%) and
// brace-delimited placeholders ({name} or {{name}}, non-nested). Anything
// matched here is copied into the pseudolocalized output byte-for-byte;
// everything else is fair game for widening.
var placeholderRe = regexp.MustCompile(`%%|%[-+ 0#]*\[?[0-9]*\]?\.?[0-9]*[a-zA-Z]|\{\{[^{}]*\}\}|\{[^{}]*\}`)

// accentMap substitutes each ASCII letter for a single accented look-alike,
// the standard pseudolocalization trick for flushing out code that assumes
// Latin-1/ASCII-only text (§17, D6-24). It is length-preserving per input
// rune, which matters: it runs on the same string the padding step then
// lengthens, so the two steps compose without fighting over how long the
// "real" text is supposed to be.
var accentMap = map[rune]rune{
	'a': 'á', 'b': 'ƀ', 'c': 'ç', 'd': 'đ', 'e': 'é', 'f': 'ƒ', 'g': 'ĝ',
	'h': 'ĥ', 'i': 'í', 'j': 'ĵ', 'k': 'ķ', 'l': 'ĺ', 'm': 'ɱ', 'n': 'ñ',
	'o': 'ó', 'p': 'ƥ', 'q': 'ɋ', 'r': 'ŕ', 's': 'ś', 't': 'ţ', 'u': 'ú',
	'v': 'ṽ', 'w': 'ŵ', 'x': 'ẋ', 'y': 'ý', 'z': 'ź',
	'A': 'Á', 'B': 'Ɓ', 'C': 'Ç', 'D': 'Đ', 'E': 'É', 'F': 'Ƒ', 'G': 'Ĝ',
	'H': 'Ĥ', 'I': 'Í', 'J': 'Ĵ', 'K': 'Ķ', 'L': 'Ĺ', 'M': 'Ṁ', 'N': 'Ñ',
	'O': 'Ó', 'P': 'Ƥ', 'Q': 'Ɋ', 'R': 'Ŕ', 'S': 'Ś', 'T': 'Ţ', 'U': 'Ú',
	'V': 'Ṽ', 'W': 'Ŵ', 'X': 'Ẋ', 'Y': 'Ý', 'Z': 'Ź',
}

// padChars cycles to build the ~35% length padding. Accented and visually
// distinct from the widened text itself, so a truncation bug is obvious at
// a glance rather than blending into the rest of the string.
const padChars = "ĩñţŀőяệм"

// Pseudolocalize implements TODOS.md D6-24: an en-XA-style transform that
// widens each string by roughly 35% and brackets it, so a UI clipped or
// overflowing under real translation is caught long before a translator
// ever sees the string. Interpolation placeholders (printf verbs and
// {brace} tokens) pass through unmodified — a pseudolocalizer that mangles
// a placeholder produces a false test failure on every run that touches
// it, which is how a pseudolocale build gets disabled. Leading and
// trailing whitespace is preserved outside the bracketed, widened core.
func Pseudolocalize(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s // empty or whitespace-only: nothing to widen or bracket
	}
	leadLen := strings.Index(s, trimmed)
	lead := s[:leadLen]
	trail := s[leadLen+len(trimmed):]
	core := trimmed

	matches := placeholderRe.FindAllStringIndex(core, -1)

	var widened strings.Builder
	textLen := 0
	pos := 0
	for _, m := range matches {
		text := core[pos:m[0]]
		widened.WriteString(widenText(text))
		textLen += len([]rune(text))
		widened.WriteString(core[m[0]:m[1]]) // placeholder, verbatim
		pos = m[1]
	}
	tail := core[pos:]
	widened.WriteString(widenText(tail))
	textLen += len([]rune(tail))

	padLen := int(math.Round(float64(textLen) * 0.35))
	for i := 0; i < padLen; i++ {
		widened.WriteRune(rune(padChars[i%len(padChars)]))
	}

	return lead + "[" + widened.String() + "]" + trail
}

func widenText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if replacement, ok := accentMap[r]; ok {
			b.WriteRune(replacement)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
