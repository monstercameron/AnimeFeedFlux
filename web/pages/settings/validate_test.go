package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAbsoluteURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{"empty", "", ErrURLEmpty},
		{"whitespace only", "   ", ErrURLEmpty},
		{"relative path", "/feeds/anime-trivia.xml", ErrURLNotAbsolute},
		{"scheme-less host", "example.com/feed", ErrURLNotAbsolute},
		{"disallowed scheme", "ftp://example.com/feed", ErrURLBadScheme},
		{"javascript scheme rejected", "javascript://alert(1)", ErrURLBadScheme},
		{"valid https", "https://feed.earlcameron.com/anime-trivia.xml", nil},
		{"valid http", "http://example.com/og.png", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateAbsoluteURL(c.raw, PublishingURLSchemes...)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("ValidateAbsoluteURL(%q) = %v, want %v", c.raw, err, c.wantErr)
			}
		})
	}
}

func TestValidateAbsoluteURL_NoSchemeRestriction(t *testing.T) {
	if err := ValidateAbsoluteURL("ftp://example.com/file"); err != nil {
		t.Errorf("expected no scheme restriction when none supplied, got %v", err)
	}
}

func TestValidatePasswordLength(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr error
	}{
		{"too short", strings.Repeat("a", 14), ErrPasswordTooShort},
		{"exactly minimum", strings.Repeat("a", 15), nil},
		{"exactly maximum", strings.Repeat("a", 128), nil},
		{"too long", strings.Repeat("a", 129), ErrPasswordTooLong},
		{"passphrase with spaces at minimum", "correct battery dinosaur tennis", nil},
		{"unicode counted by rune not byte", strings.Repeat("日", 15), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePasswordLength(c.pw)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("ValidatePasswordLength(len=%d) = %v, want %v", len([]rune(c.pw)), err, c.wantErr)
			}
		})
	}
}

// Guards against reintroducing a composition rule: a password made
// entirely of one repeated letter, containing no symbol and no digit,
// must pass length validation as long as it meets the length bound. This
// is the exact anti-pattern PLAN.md §4/SEC-12 calls out by name
// ("`correct battery dinosaur tennis` rejected, `P@ssw0rd2026!` accepted"
// was the old, wrong behavior).
func TestValidatePasswordLength_NoCompositionRule(t *testing.T) {
	noSymbolsOrDigits := "abcdefghijklmnopqrstuvwxyz"[:20]
	if err := ValidatePasswordLength(noSymbolsOrDigits); err != nil {
		t.Errorf("a 20-char all-lowercase password must pass length-only validation, got %v", err)
	}
}

func TestPasswordGuidanceArgs_MatchesConstants(t *testing.T) {
	args := PasswordGuidanceArgs()
	if args["min"] != PasswordMinLength {
		t.Errorf("guidance min = %v, want %v", args["min"], PasswordMinLength)
	}
	if args["max"] != PasswordMaxLength {
		t.Errorf("guidance max = %v, want %v", args["max"], PasswordMaxLength)
	}
}
