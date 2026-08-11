package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"
)

// resetTokenBytes is 256 bits of entropy, per PLAN.md §4 — the same construction as sessions
// (NewToken in session.go): a plain opaque random token, not a JWT. There is one backend and one
// admin, so there is nothing for a self-contained signed token to buy here that a server-side
// lookup does not already give more simply, and a JWT-shaped reset token would be the one part of
// this system that could be replayed without ever touching the database.
const resetTokenBytes = 32

// resetTokenTTL is deliberately short: a password reset token is a bearer credential that, if
// intercepted (a shared clipboard, a logged URL, a compromised recovery channel), grants a full
// account takeover. Short expiry bounds the window in which an intercepted token is still useful.
const resetTokenTTL = 15 * time.Minute

// NewResetToken generates a fresh password reset token. raw is returned to the caller exactly
// once — it is the value handed to the admin (e.g. shown once, or placed in a recovery flow) and
// goes nowhere else. hash is what gets persisted to password_reset_tokens.token_hash, mirroring
// session tokens: a stolen database does not by itself yield a usable reset token, only its
// SHA-256 preimage-resistant hash.
func NewResetToken() (raw string, hash string, expiresAt time.Time, err error) {
	buf := make([]byte, resetTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", time.Time{}, fmt.Errorf("auth: generating reset token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	hash = hashResetToken(raw)
	// time.Now() directly, unlike the rest of this package, which takes an
	// injected clock. Left as-is deliberately: NewResetToken has four return
	// values and one caller, threading a clock through it would change a
	// signature for a value nothing asserts on, and no test in the suite needs
	// to control this expiry. Recorded so the inconsistency is a decision
	// rather than an oversight (A8-39).
	expiresAt = time.Now().UTC().Add(resetTokenTTL)
	return raw, hash, expiresAt, nil
}

// hashResetToken hashes a raw reset token the same way NewResetToken does, so a lookup by an
// incoming token value can be compared against the stored hash without ever storing the raw
// value. SHA-256 rather than argon2id, for the same reason session tokens use it (see
// session.go's NewToken doc): 256 bits of CSPRNG output has no low-entropy human input to stretch
// against, so a memory-hard KDF would only add latency for no security gain.
func hashResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VerifyResetToken reports whether raw hashes to hash, in constant time. It does NOT check
// expiry or single-use — those depend on the stored row (expires_at, used_at), which the caller
// already has to load to find hash in the first place, so checking them here would just be a
// second, easier-to-forget copy of the same logic. This function is purely the token-material
// comparison, the same separation of concerns as password.Verify vs. Pepper.Verify.
func VerifyResetToken(raw string, hash string) bool {
	candidate := hashResetToken(raw)
	// Constant-time even though both sides are already fixed-length base64 of a SHA-256 sum:
	// the comparison is cheap enough that there's no reason to use the fast path and one more
	// place to accidentally regress into a leaky compare later.
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(hash)) == 1
}

// Using a reset token MUST revoke every existing session for the account (PLAN.md §4 and §12.2).
// This package does not do that revocation itself — it has no access to the sessions table, only
// the token primitives above — but it is documented here, at the point where a caller might
// otherwise think verifying the token and updating the password is the whole job:
//
// A password reset exists because the admin believes the credential may be compromised. If a
// session opened under the OLD password survives the reset, the reset has not actually locked
// anyone out — an attacker who was already inside keeps their seat regardless of how strong the
// new password is. So the caller completing a reset (the RPC handler, not this package) must, in
// the same transaction as the password update: mark the reset token used, write the new
// password_hash, and revoke every row in `sessions` for this account (set revoked_at, not
// delete — see session.go's Valid, which checks revoked_at explicitly). Skipping any one of the
// three leaves a real, exploitable gap: skip the revoke and old sessions survive; skip marking
// the token used and it is replayable within its TTL; skip the password update and nothing
// happened at all.
