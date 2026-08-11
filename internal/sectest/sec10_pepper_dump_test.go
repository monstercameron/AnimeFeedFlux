package sectest

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// SEC-10: prove a database dump ALONE — everything a stolen `admin` row
// yields (salt, argon2id params, and the stored hash, all PHC-encoded
// together by auth.HashPeppered) — does not permit verification. The pepper
// lives only in the environment (AFF_SECRET_KEY), never in the database, so
// this is the entire justification for the pepper existing at all: without
// this test the feature is an assertion, not a proven property.
//
// The complement is asserted too: WITH the pepper the same stored value
// verifies the same correct password, so this test fails loudly (rather than
// vacuously passing) if peppering silently became a no-op — e.g. if
// HashPeppered/VerifyPasswordPeppered regressed back to applying the pepper
// to the password string instead of the argon2id output (the exact bug
// TODOS.md SEC-07 describes having existed).
func TestPepper_DatabaseDumpAloneDoesNotVerify(t *testing.T) {
	const password = "correct horse battery staple long enough"
	pepper := []byte("attacker-does-not-have-this-256-bit-secret-value")

	// This is production's real construction: HMAC-SHA256(pepper, argon2idOutput),
	// PHC-encoded exactly like a real `admin.password_hash` row.
	stored, err := auth.HashPeppered(password, auth.DefaultParams(), pepper)
	if err != nil {
		t.Fatalf("HashPeppered: %v", err)
	}

	// --- The attack: a database dump yields `stored` and nothing else. ---
	//
	// An attacker who has only the DB file has salt, params, and this stored
	// value, but not AFF_SECRET_KEY. Their best move is to run the standard
	// (unpeppered) argon2id verification against the correct password — it
	// must fail, because `stored` is not the raw argon2id output, it is that
	// output run through an HMAC the attacker cannot reproduce.
	if ok, _, err := auth.Verify(password, stored); err != nil {
		t.Fatalf("Verify (unpeppered) on a peppered hash: unexpected error: %v", err)
	} else if ok {
		t.Fatal("database dump alone verified the correct password via unpeppered Verify — pepper is not protecting anything")
	}

	// Equivalent attack via the peppered verifier itself, but with no pepper
	// available (nil) — the realistic worst case for someone who compromised
	// the DB but not the environment/secret store.
	if ok, _, err := auth.VerifyPasswordPeppered(password, stored, nil); err != nil {
		t.Fatalf("VerifyPasswordPeppered(pepper=nil): unexpected error: %v", err)
	} else if ok {
		t.Fatal("database dump alone verified the correct password with pepper=nil — pepper is not protecting anything")
	}

	// A wrong guess at the pepper must fail too — ruling out "any non-empty
	// pepper works" as a degenerate implementation.
	wrongPepper := []byte("a-different-256-bit-value-entirely")
	if ok, _, err := auth.VerifyPasswordPeppered(password, stored, wrongPepper); err != nil {
		t.Fatalf("VerifyPasswordPeppered(wrong pepper): unexpected error: %v", err)
	} else if ok {
		t.Fatal("verification succeeded with the wrong pepper")
	}

	// --- The complement: WITH the pepper, the correct password verifies. ---
	//
	// Without this half, a broken implementation that always returns false
	// would pass everything above. This is what makes the negative result
	// above mean "the pepper is doing its job" rather than "verification is
	// just broken."
	ok, needsRehash, err := auth.VerifyPasswordPeppered(password, stored, pepper)
	if err != nil {
		t.Fatalf("VerifyPasswordPeppered(correct pepper): unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("correct password with the correct pepper failed to verify — peppering is broken, not just protective")
	}
	if needsRehash {
		t.Fatal("hash produced with auth.DefaultParams() unexpectedly reported needsRehash")
	}
}
