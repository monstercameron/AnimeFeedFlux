package auth

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct-horse-battery-staple-9!"

func weakParams() Params {
	// Deliberately below DefaultParams() so needsRehash tests have something to detect, and
	// small enough to keep the test suite fast.
	return Params{Time: 1, Memory: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	encoded, err := Hash(testPassword, DefaultParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, needsRehash, err := Verify(testPassword, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify: correct password reported as not matching")
	}
	if needsRehash {
		t.Fatal("Verify: freshly hashed with default params should not need rehash")
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	encoded, err := Hash(testPassword, DefaultParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, _, err := Verify("not-the-right-password-99", encoded)
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Verify: wrong password reported as matching")
	}
}

func TestEncodedFormContainsParams(t *testing.T) {
	p := DefaultParams()
	encoded, err := Hash(testPassword, p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=") {
		t.Fatalf("encoded hash missing variant/version prefix: %q", encoded)
	}
	wantParams := []string{
		"m=" + strconv.FormatUint(uint64(p.Memory), 10),
		"t=" + strconv.FormatUint(uint64(p.Time), 10),
		"p=" + strconv.FormatUint(uint64(p.Threads), 10),
	}
	for _, want := range wantParams {
		if !strings.Contains(encoded, want) {
			t.Errorf("encoded hash %q missing param %q", encoded, want)
		}
	}
	// Six $-delimited fields: "", argon2id, v=.., params, salt, key.
	if parts := strings.Split(encoded, "$"); len(parts) != 6 {
		t.Errorf("encoded hash has %d $-delimited fields, want 6: %q", len(parts), encoded)
	}
}

func TestVerifyNeedsRehashForWeakerParams(t *testing.T) {
	encoded, err := Hash(testPassword, weakParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, needsRehash, err := Verify(testPassword, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify: correct password against weak-param hash should still match")
	}
	if !needsRehash {
		t.Fatal("Verify: hash made with weaker-than-default params should report needsRehash")
	}
}

func TestVerifyRejectsMalformedEncoding(t *testing.T) {
	cases := []string{
		"",
		"not-even-close-to-phc-format",
		"$argon2id$v=19$m=65536,t=3,p=4$onlyonemore",
		"$argon2id$v=19$m=notanumber,t=3,p=4$c2FsdA$a2V5",
		"$argon2id$v=notanumber$m=65536,t=3,p=4$c2FsdA$a2V5",
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$a2V5",
		"$argon2id$v=19$m=65536,t=3$c2FsdA$a2V5", // missing p
		"$argon2id$v=19$m=65536,t=3,p=4$not-base64!!$a2V5",
	}
	for _, encoded := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Verify panicked on malformed input %q: %v", encoded, r)
				}
			}()
			_, _, err := Verify(testPassword, encoded)
			if err == nil {
				t.Errorf("Verify(%q): expected error for malformed encoding, got nil", encoded)
			}
		}()
	}
}

// TestVerifyTimingComparable is a smoke test, not a rigorous timing-attack measurement: it runs
// Verify many times with a right and a wrong password against the same hash and checks the
// medians land within a generous tolerance of each other. The property under test is "Verify
// does not short-circuit its comparison based on how much of the password matches" — a gross
// regression (e.g. swapping ConstantTimeCompare for bytes.Equal, or an early return on a
// mismatched prefix) should show up as a large, consistent gap; ordinary scheduler jitter should
// not.
func TestVerifyTimingComparable(t *testing.T) {
	if testing.Short() {
		t.Skip("timing smoke test skipped in -short mode")
	}
	encoded, err := Hash(testPassword, weakParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	const iterations = 200
	right := make([]time.Duration, 0, iterations)
	wrong := make([]time.Duration, 0, iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()
		if _, _, err := Verify(testPassword, encoded); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		right = append(right, time.Since(start))

		start = time.Now()
		if _, _, err := Verify("totally-different-wrong-password", encoded); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		wrong = append(wrong, time.Since(start))
	}

	rightMedian := median(right)
	wrongMedian := median(wrong)

	ratio := float64(rightMedian) / float64(wrongMedian)
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("Verify timing diverges beyond tolerance: right median=%v wrong median=%v ratio=%.2f",
			rightMedian, wrongMedian, ratio)
	}
}

func median(d []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func TestIsWeakRejectsShortAndLowEntropy(t *testing.T) {
	cases := []string{
		"",
		"short1!",                   // under the 15-char floor
		"aaaaaaaaaaaaaaaaaaaa",      // long, one distinct character
		"abababababababababab",      // long, a two-character cycle
		"abcdefghijklmnopqrst",      // long, purely sequential
		"password123",               // straight off every wordlist
		"Password2026!",             // a common password wearing composition rules
		"correcthorsebatterystaple", // famous enough to be in every wordlist
	}
	for _, pw := range cases {
		if err := IsWeak(pw); err == nil {
			t.Errorf("IsWeak(%q): expected rejection, got nil", pw)
		}
	}
}

func TestIsWeakAcceptsReasonablePassphrase(t *testing.T) {
	// Letters only, spaces, no digit and no symbol. Under the OLD composition
	// rule this was REJECTED — the rule actively selected the weaker password,
	// which is precisely why NIST SP 800-63B dropped composition requirements.
	// This case exists to stop anyone reinstating them.
	for _, pw := range []string{
		"correct battery dinosaur tennis",
		testPassword,
		"すもももももももものうち すもも", // Unicode and spaces are allowed
	} {
		if err := IsWeak(pw); err != nil {
			t.Errorf("IsWeak(%q): unexpected rejection: %v", pw, err)
		}
	}
}

func TestIsWeakRejectsOverMaximum(t *testing.T) {
	// A maximum exists only to bound the work argon2id is asked to do; 128 is
	// far above anything a human types and far below a denial-of-service.
	long := strings.Repeat("a b c d ", 40) // > 128 runes, not a simple repeat
	if err := IsWeak(long); err == nil {
		t.Error("IsWeak: expected rejection over the maximum length")
	}
}

func TestNormalizeMakesEquivalentFormsVerify(t *testing.T) {
	// The same passphrase typed on two platforms can differ byte-for-byte (NFC
	// vs NFD). Without normalisation the second one fails to verify and the
	// admin is locked out by their own keyboard. Written as escapes so the two
	// forms stay visibly different in source.
	nfc := "éclair passphrase for testing"  // precomposed e-acute
	nfd := "éclair passphrase for testing" // e + combining acute
	if Normalize(nfc) != Normalize(nfd) {
		t.Errorf("normalisation did not converge: %q vs %q", Normalize(nfc), Normalize(nfd))
	}
}

func TestHashRejectsWeakPassword(t *testing.T) {
	if _, err := Hash("short", DefaultParams()); err == nil {
		t.Fatal("Hash: expected error for weak password, got nil")
	}
}
