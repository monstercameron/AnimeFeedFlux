package sectest

import (
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// SEC-48: arbitrary Unicode — including lone surrogates, embedded NUL bytes,
// and very large strings — fed to the password validator must never panic.
// The validator sits directly on the login/enrollment path (auth.IsWeak is
// called from inside auth.Hash and from ChangePassword's request handling),
// so a panic here is an unauthenticated remote crash of the entire admin
// surface, not a cosmetic bug.
func FuzzPasswordValidator_NeverPanics(f *testing.F) {
	seeds := []string{
		"",
		"short",
		"correct horse battery staple long enough",
		string([]byte{0x00, 0x00, 0x00}),                      // embedded NUL
		"password\x00with\x00nulls\x00padded\x00to\x00length", // NUL mid-string
		"\xed\xa0\x80",           // a lone UTF-16 high surrogate (U+D800), invalid in UTF-8
		"\xed\xbf\xbf",           // a lone low surrogate (U+DFFF), invalid in UTF-8
		"\xc0\x80",               // overlong NUL encoding, invalid UTF-8
		"\xff\xfe\xfd\xfc",       // not valid UTF-8 at all
		strings.Repeat("あ", 20),  // legitimate multi-byte Unicode passphrase
		strings.Repeat("A", 200), // over the 128-char max, ASCII
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, password string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("auth.IsWeak/Normalize panicked on input (len=%d): %v", len(password), r)
			}
		}()
		_ = auth.IsWeak(password)
		_ = auth.Normalize(password)
	})
}

// TestPasswordValidator_TenMegabyteStringNeverPanics is a deterministic,
// non-fuzz-driven check for the specific "10 MB string" case called out in
// the ticket: Go's fuzzing engine mutates toward small inputs and is
// unlikely to ever generate a 10 MB corpus entry on its own, so this is
// pinned as an explicit test rather than left to chance.
func TestPasswordValidator_TenMegabyteStringNeverPanics(t *testing.T) {
	huge := strings.Repeat("a", 10*1024*1024)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("auth.IsWeak panicked on a 10MB input: %v", r)
		}
	}()
	err := auth.IsWeak(huge)
	if err == nil {
		t.Error("a 10MB password should be rejected (exceeds the 128-char maximum), but IsWeak returned nil")
	}
}

// TestPasswordValidator_LoneSurrogateNeverPanics pins the two specific
// "invalid UTF-8 that looks like a UTF-16 surrogate" cases from the ticket as
// deterministic regression tests, independent of what the fuzzer happens to
// explore.
func TestPasswordValidator_LoneSurrogateNeverPanics(t *testing.T) {
	cases := []string{
		"\xed\xa0\x80" + strings.Repeat("x", 20), // high surrogate + padding to clear length floor
		"\xed\xbf\xbf" + strings.Repeat("x", 20), // low surrogate + padding
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked on %q: %v", c, r)
				}
			}()
			_ = auth.IsWeak(c)
			_ = auth.Normalize(c)
		}()
	}
}
