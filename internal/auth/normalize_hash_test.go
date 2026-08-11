package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/argon2"
)

// Normalize's doc claimed NFKC was applied before hashing; Hash, HashPeppered,
// Verify and VerifyPasswordPeppered all passed the raw string to argon2, so
// the policy was enforced against one string and the credential derived from
// another, and the stated cross-platform property never held (A8-36).
//
// "ﬁ" (U+FB01, the fi ligature) is NFKC-normalised to "fi" — a realistic way
// for the same passphrase to arrive as different bytes from different input
// methods.
const (
	ligature   = "corre\ufb01cient-passphrase-long-enough"
	decomposed = "correficient-passphrase-long-enough"
)

func TestHashAndVerifyNormalizeEquivalentPassphrases(t *testing.T) {
	if Normalize(ligature) != decomposed {
		t.Fatalf("precondition: NFKC(%q) = %q, want %q", ligature, Normalize(ligature), decomposed)
	}

	encoded, err := Hash(ligature, DefaultParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, _, err := Verify(decomposed, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("the same passphrase in a different normal form failed to verify — " +
			"the cross-platform property Normalize documents")
	}
}

// A hash written before this change derived from the RAW string. Refusing it
// would lock the admin out of a credential that has not changed, so a raw
// match is still accepted — and reported as needing a rehash, so the next
// successful login rewrites it normalised.
func TestVerifyAcceptsLegacyRawHashAndAsksForRehash(t *testing.T) {
	legacy := hashRawForTest(t, ligature)

	ok, needsRehash, err := Verify(ligature, legacy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("a pre-existing hash stopped verifying; this is the lockout the fallback prevents")
	}
	if !needsRehash {
		t.Error("a legacy raw hash did not ask to be rehashed, so it would stay raw forever")
	}

	// Once rehashed, the normalised form verifies and no longer asks.
	rehashed, err := Hash(ligature, DefaultParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, needsRehash, err = Verify(ligature, rehashed)
	if err != nil || !ok {
		t.Fatalf("rehashed verify: ok=%v err=%v", ok, err)
	}
	if needsRehash {
		t.Error("a freshly written hash still asks to be rehashed")
	}
}

// The peppered pair must behave identically — it is the path production uses
// whenever a pepper is configured.
func TestPepperedHashVerifyNormalizes(t *testing.T) {
	pepper := []byte("a-test-pepper-value")

	encoded, err := HashPeppered(ligature, DefaultParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}
	ok, _, err := VerifyPasswordPeppered(decomposed, encoded, pepper)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered: %v", err)
	}
	if !ok {
		t.Error("peppered verify rejected the same passphrase in a different normal form")
	}
}

// A wrong password must still be wrong — the fallback widens what verifies to
// exactly one extra form of the SAME string, nothing else.
func TestVerifyStillRejectsAWrongPassword(t *testing.T) {
	encoded, err := Hash("a-passphrase-that-is-not-breached-42", DefaultParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if ok, _, _ := Verify("a-different-passphrase-entirely-77", encoded); ok {
		t.Error("a wrong password verified")
	}
}

// hashRawForTest reproduces what Hash did BEFORE A8-36: derive from the raw
// string with no normalisation. It exists so the legacy fallback is tested
// against a genuinely old-format hash rather than a hand-written constant
// that would drift from the encoding.
func hashRawForTest(t *testing.T, password string) string {
	t.Helper()
	p := DefaultParams()
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("salt: %v", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Variant, argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}
