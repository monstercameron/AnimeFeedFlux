package auth

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// The pepper's whole value proposition is that a stolen database is not
// enough: the stored value cannot be reproduced without a secret that never
// touches the database. These tests pin that property, and the compatibility
// claim that makes the feature safe to introduce — with no pepper configured,
// everything is byte-identical to the unpeppered path, so no deployment is
// locked out by upgrading.

func TestHashPepperedRoundTrip(t *testing.T) {
	pepper := []byte("a-server-side-secret")
	encoded, err := HashPeppered(testPassword, weakParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}

	ok, _, err := VerifyPasswordPeppered(testPassword, encoded, pepper)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if !ok {
		t.Fatal("the correct password with the correct pepper did not verify")
	}
}

func TestPepperedHashIsUselessWithoutThePepper(t *testing.T) {
	// This is the attack the ordering exists to defeat: the attacker has the
	// full stored value, the salt and the params, and the right password.
	pepper := []byte("a-server-side-secret")
	encoded, err := HashPeppered(testPassword, weakParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}

	ok, _, err := VerifyPasswordPeppered(testPassword, encoded, nil)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if ok {
		t.Error("a peppered hash verified with no pepper — the database alone was enough")
	}

	ok, _, err = VerifyPasswordPeppered(testPassword, encoded, []byte("the-wrong-secret"))
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if ok {
		t.Error("a peppered hash verified under the wrong pepper")
	}

	// And the plain (pepper-unaware) verifier must not accept it either.
	ok, _, err = Verify(testPassword, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("the unpeppered Verify accepted a peppered hash")
	}
}

func TestNoPepperConfiguredIsIdenticalToTheUnpepperedPath(t *testing.T) {
	// The compatibility claim in both doc comments: an unconfigured
	// deployment's stored hashes and login path are unaffected by these
	// functions existing. If this breaks, upgrading locks every operator out.
	for _, pepper := range [][]byte{nil, {}} {
		encoded, err := HashPeppered(testPassword, weakParams(), pepper)
		if err != nil {
			t.Fatalf("HashPeppered: %v", err)
		}
		// A hash written by HashPeppered-with-no-pepper reads back through the
		// plain verifier...
		ok, _, err := Verify(testPassword, encoded)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !ok {
			t.Errorf("pepper %v: Verify could not read a HashPeppered hash", pepper)
		}

		// ...and a hash written by plain Hash reads back through the peppered
		// verifier. Both directions matter: hashes written before and after
		// the feature landed coexist in one database.
		plain, err := Hash(testPassword, weakParams())
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		ok, _, err = VerifyPasswordPeppered(testPassword, plain, pepper)
		if err != nil {
			t.Fatalf("VerifyPasswordPeppered: %v", err)
		}
		if !ok {
			t.Errorf("pepper %v: VerifyPasswordPeppered could not read a plain Hash", pepper)
		}
	}
}

func TestVerifyPasswordPepperedRejectsTheWrongPassword(t *testing.T) {
	pepper := []byte("secret")
	encoded, err := HashPeppered(testPassword, weakParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}
	ok, _, err := VerifyPasswordPeppered(testPassword+"x", encoded, pepper)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if ok {
		t.Error("the wrong password verified")
	}
}

func TestVerifyPasswordPepperedFlagsAWeakStoredHashForRehash(t *testing.T) {
	// weakParams() is below DefaultParams(), so a successful verify must also
	// report that the stored hash should be upgraded on this login.
	pepper := []byte("secret")
	encoded, err := HashPeppered(testPassword, weakParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}
	ok, needsRehash, err := VerifyPasswordPeppered(testPassword, encoded, pepper)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if !ok || !needsRehash {
		t.Errorf("ok=%v needsRehash=%v, want both true for a below-default hash", ok, needsRehash)
	}

	// A wrong password must NEVER report needsRehash: that would leak which
	// stored hashes are old to anyone who can guess.
	if _, needsRehash, err := VerifyPasswordPeppered("wrong-password-entirely", encoded, pepper); err != nil || needsRehash {
		t.Errorf("a failed verify reported needsRehash=%v (err=%v)", needsRehash, err)
	}
}

func TestHashPepperedRejectsAWeakPasswordBeforeHashing(t *testing.T) {
	// Cheapest possible rejection: no salt read, no argon2 run.
	if _, err := HashPeppered("password123", weakParams(), []byte("secret")); err == nil {
		t.Fatal("a known-breached password was accepted")
	}
}

func TestVerifyPasswordPepperedRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"not PHC":            "just-a-string",
		"wrong variant":      "$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
		"nonsense version":   "$argon2id$v=1$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
		"truncated sections": "$argon2id$v=19$m=8,t=1,p=1",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			ok, needsRehash, err := VerifyPasswordPeppered(testPassword, encoded, []byte("secret"))
			if err == nil {
				t.Fatalf("want an error for %q", encoded)
			}
			if ok || needsRehash {
				t.Errorf("a malformed hash reported ok=%v needsRehash=%v", ok, needsRehash)
			}
		})
	}
}

// --- LoadBreachedSHA1 -----------------------------------------------------

func sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s)) //nolint:gosec // SHA-1 is the HIBP corpus format, not a security choice here
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestLoadBreachedSHA1ParsesTheHIBPFormat(t *testing.T) {
	// The downloadable list ships as HASH:COUNT, uppercase, CRLF-terminated.
	// Each of those details is a way to load a corpus that silently matches
	// nothing — which looks exactly like "no breached passwords found".
	t.Cleanup(resetBreachedCorpus)

	pw := "an-unlikely-passphrase-for-a-test-42"
	corpus := strings.Join([]string{
		sha1Hex(pw) + ":31337",
		strings.ToLower(sha1Hex("another-one")) + ":2", // lowercase must be normalized
		"", // blank lines are skipped, not stored
		"   ",
		sha1Hex("no-count-suffix"),
	}, "\r\n")

	n, err := LoadBreachedSHA1(strings.NewReader(corpus))
	if err != nil {
		t.Fatalf("LoadBreachedSHA1: %v", err)
	}
	// Blank and whitespace-only lines: one of them ("   ") trims to empty and
	// is skipped, so only the three real hashes land.
	if n != 3 {
		t.Errorf("loaded %d hashes, want 3", n)
	}
	if !IsBreached(pw) {
		t.Error("a password in the loaded corpus was not reported breached")
	}
	if !IsBreached("another-one") {
		t.Error("a lowercase corpus entry was not matched (the corpus is uppercased on load)")
	}
	if IsBreached("a-password-that-is-in-no-corpus-anywhere-1") {
		t.Error("a password absent from the corpus was reported breached")
	}
}

func TestLoadBreachedSHA1ReplacesRatherThanMerges(t *testing.T) {
	// The set is swapped, not appended to: reloading a corrected corpus must
	// not leave the old entries behind.
	t.Cleanup(resetBreachedCorpus)

	first := "first-corpus-passphrase-here-77"
	second := "second-corpus-passphrase-here-88"
	if _, err := LoadBreachedSHA1(strings.NewReader(sha1Hex(first))); err != nil {
		t.Fatalf("LoadBreachedSHA1: %v", err)
	}
	if _, err := LoadBreachedSHA1(strings.NewReader(sha1Hex(second))); err != nil {
		t.Fatalf("LoadBreachedSHA1: %v", err)
	}
	if IsBreached(first) {
		t.Error("an entry from the replaced corpus is still matching")
	}
	if !IsBreached(second) {
		t.Error("the newly loaded corpus is not matching")
	}
}

func TestLoadBreachedSHA1EmptyCorpusIsNotAnError(t *testing.T) {
	t.Cleanup(resetBreachedCorpus)
	n, err := LoadBreachedSHA1(strings.NewReader(""))
	if err != nil {
		t.Fatalf("LoadBreachedSHA1: %v", err)
	}
	if n != 0 {
		t.Errorf("loaded %d hashes from an empty corpus, want 0", n)
	}
}

func TestLoadBreachedSHA1ReportsAReadFailure(t *testing.T) {
	// A truncated download must fail loudly. Silently loading a partial
	// corpus is the failure mode that matters: it weakens the check without
	// telling anyone.
	t.Cleanup(resetBreachedCorpus)
	sentinel := errors.New("connection reset")
	if _, err := LoadBreachedSHA1(failingReader{after: []byte("ABC123\n"), err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("LoadBreachedSHA1 error = %v, want %v", err, sentinel)
	}
}

func TestLoadBreachedSHA1DoesNotWeakenTheBuiltInList(t *testing.T) {
	// The built-in common-password floor is independent of the loaded corpus,
	// so loading an empty file must not turn "password123" into an acceptable
	// password.
	t.Cleanup(resetBreachedCorpus)
	if _, err := LoadBreachedSHA1(strings.NewReader("")); err != nil {
		t.Fatalf("LoadBreachedSHA1: %v", err)
	}
	if !IsBreached("password123") {
		t.Error("the built-in list stopped matching after an empty corpus was loaded")
	}
}

// resetBreachedCorpus restores the package-level corpus so these tests cannot
// leak state into the rest of the package's tests.
func resetBreachedCorpus() {
	breachedMu.Lock()
	breachedSHA1 = nil
	breachedMu.Unlock()
}

type failingReader struct {
	after []byte
	err   error
	done  bool
}

func (r failingReader) Read(p []byte) (int, error) {
	if r.done || len(r.after) == 0 {
		return 0, r.err
	}
	n := copy(p, r.after)
	return n, r.err
}
