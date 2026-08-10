package i18nlint

import (
	"strings"
	"testing"
)

func TestPseudolocalize_Bracketed(t *testing.T) {
	got := Pseudolocalize("Save")
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Fatalf("Pseudolocalize(%q) = %q, want bracketed", "Save", got)
	}
}

func TestPseudolocalize_Lengthens(t *testing.T) {
	in := "Create a new feed"
	got := Pseudolocalize(in)
	// Brackets add 2 bytes on top of the ~35% widen; assert it's
	// meaningfully longer without pinning an exact byte count (accented
	// substitutes and the input are not always 1 rune == 1 byte).
	if len([]rune(got)) <= len([]rune(in)) {
		t.Fatalf("Pseudolocalize(%q) = %q (len %d), want longer than input (len %d)",
			in, got, len([]rune(got)), len([]rune(in)))
	}
}

func TestPseudolocalize_PreservesPrintfPlaceholders(t *testing.T) {
	cases := []string{
		"Rendered %d items",
		"%s failed with %d errors (%v)",
		"literal percent %% sign",
		"positional %[1]s placeholder",
		"width and precision %5.2f",
	}
	for _, in := range cases {
		got := Pseudolocalize(in)
		for _, verb := range extractPlaceholders(in) {
			if !strings.Contains(got, verb) {
				t.Errorf("Pseudolocalize(%q) = %q, missing verbatim placeholder %q", in, got, verb)
			}
		}
	}
}

func TestPseudolocalize_PreservesBracePlaceholders(t *testing.T) {
	cases := []string{
		"Hello, {name}!",
		"{{count}} items remaining",
		"{first} and {second}",
	}
	for _, in := range cases {
		got := Pseudolocalize(in)
		for _, ph := range extractPlaceholders(in) {
			if !strings.Contains(got, ph) {
				t.Errorf("Pseudolocalize(%q) = %q, missing verbatim placeholder %q", in, got, ph)
			}
		}
	}
}

func TestPseudolocalize_PreservesLeadingTrailingWhitespace(t *testing.T) {
	in := "  padded on both sides  \t"
	got := Pseudolocalize(in)
	if !strings.HasPrefix(got, "  ") {
		t.Fatalf("Pseudolocalize(%q) = %q, lost leading whitespace", in, got)
	}
	if !strings.HasSuffix(got, "  \t") {
		t.Fatalf("Pseudolocalize(%q) = %q, lost trailing whitespace", in, got)
	}
}

func TestPseudolocalize_EmptyAndWhitespaceOnly(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if got := Pseudolocalize(in); got != in {
			t.Errorf("Pseudolocalize(%q) = %q, want unchanged", in, got)
		}
	}
}

func extractPlaceholders(s string) []string {
	matches := placeholderRe.FindAllString(s, -1)
	return matches
}
