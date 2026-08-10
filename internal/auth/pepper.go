package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
)

// Pepper applies an optional second secret on top of an argon2id output, per PLAN.md §4. It is
// HMAC-SHA256(pepper, argonOutput) — applied AFTER argon2id, never before, because the pepper's
// entire value is that it lives outside the database (in the environment) while argonOutput is
// the thing that gets stored. A stolen database yields the salt, the KDF params, and the argon2id
// output; it does not yield the pepper, so HMAC-ing with it is the step that makes the stolen file
// insufficient on its own.
//
// Peppering is OPTIONAL. len(pepper) == 0 is the "no pepper configured" case, and Pepper returns
// argonOutput unchanged for it — not a zero-keyed HMAC, not an error. That is what lets an
// unconfigured deployment behave exactly as if this function did not exist: the zero value for
// pepper must be a no-op, or every install without AFF_SECRET_KEY set would silently start storing
// a different value than Verify expects, and lock the one admin account out the first time the
// process restarted with a different implicit key.
func Pepper(argonOutput []byte, pepper []byte) []byte {
	if len(pepper) == 0 {
		return argonOutput
	}
	mac := hmac.New(sha256.New, pepper)
	// hmac.Hash.Write never returns an error (see crypto/hmac docs), so the error is safe to
	// discard here.
	_, _ = mac.Write(argonOutput)
	return mac.Sum(nil)
}

// VerifyPeppered does a constant-time comparison of candidate against stored, after applying the
// same optional pepper to candidate that was applied when stored was produced. Callers pass the
// raw argon2id output as candidate; VerifyPeppered peppers it identically to how Hash-time
// peppering worked, so a caller never has to remember to pepper before calling this.
//
// Named VerifyPeppered rather than the bare Verify PLAN.md §4 uses in prose: password.go already
// exports a package-level Verify(password, encoded string) for the argon2id/PHC-decode step, and
// Go has no overloading, so the two functions cannot share a name in one package. This is
// deliberately a separate function from that one — VerifyPeppered only handles the HMAC layer on
// top of an already-derived argon2id output, so the two concerns (KDF correctness vs. pepper
// correctness) stay independently testable and a pepper rotation never needs to touch argon2id
// logic at all.
func VerifyPeppered(candidate, stored []byte, pepper []byte) bool {
	peppered := Pepper(candidate, pepper)
	// subtle.ConstantTimeCompare requires equal-length inputs for its constant-time guarantee;
	// a length mismatch is folded into the comparison result rather than short-circuited, so
	// timing cannot leak the length of the stored value either.
	return subtle.ConstantTimeCompare(peppered, stored) == 1
}
