package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	// tokenBytes is 256 bits of entropy per PLAN.md §4 — large enough that guessing a live
	// session token is not a viable attack even against a very long-lived, high-traffic cookie.
	tokenBytes = 32

	// SessionAbsoluteLifetime and SessionIdleTimeout are the §4 lifetimes: 12h no matter what,
	// 60m of inactivity kills the session sooner. Two separate clocks because "still being used"
	// and "hasn't gone on too long regardless of use" are different risk properties — a forgotten
	// browser tab logged in on a shared machine is the idle timeout's job; a stolen-but-actively-
	// replayed token is the absolute lifetime's job.
	SessionAbsoluteLifetime = 12 * time.Hour
	SessionIdleTimeout      = 60 * time.Minute
)

// NewToken generates a fresh 256-bit session token. raw is returned to the caller exactly once —
// it goes into the Set-Cookie response and nowhere else. hash is what gets persisted: only the
// hash is stored, so a stolen database (backup, SQL injection, careless export) does not by
// itself yield a usable session — the attacker would still need to reverse a SHA-256 preimage.
// This mirrors the same "store what you'd be comfortable leaking" posture as password hashing,
// except session tokens use a plain fast hash rather than argon2id because, unlike a password,
// the token already has 256 bits of uniform entropy — there is no low-entropy human input to
// stretch against, so a memory-hard KDF would only cost latency on every request for no security
// gain.
func NewToken() (raw string, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generating session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	hash = HashToken(raw)
	return raw, hash, nil
}

// HashToken hashes a raw session token the same way NewToken does, so a lookup by an incoming
// cookie value can be compared against the stored hash without ever storing the raw value.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Session is the server-side record of an authenticated session. Only TokenHash is ever
// persisted from the token material; the raw token lives solely in the cookie on the client.
type Session struct {
	TokenHash  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	RevokedAt  time.Time // zero value means "not revoked"
}

// Valid reports whether the session may still be used as of now, enforcing all three independent
// kill switches: absolute expiry, idle timeout, and explicit revocation. All three are checked on
// every use (not just at login) per §4 — "authenticated once, trusted forever" is exactly what
// this guards against, since revocation (e.g. on password change) must take effect on the very
// next request, not just future ones.
func (s Session) Valid(now time.Time, idleTimeout time.Duration) bool {
	if !s.RevokedAt.IsZero() && !s.RevokedAt.After(now) {
		return false
	}
	if !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Sub(s.LastSeenAt) >= idleTimeout {
		return false
	}
	return true
}

// cookieName is unexported so CookieName() is the single source of truth for it, keeping the
// literal string in exactly one place.
const cookieName = "__Host-aff_session"

// CookieName returns the session cookie's name. It is deliberately __Host- prefixed: browsers
// enforce that a __Host- cookie was set with Secure, no Domain attribute, and Path=/, which
// means a subdomain or a network attacker who can't get a cookie accepted for the exact origin
// cannot forge one that this host will accept either — the prefix turns "did we remember to set
// the right flags" into something the browser itself refuses to violate.
func CookieName() string {
	return cookieName
}

// NewSessionCookie builds the Set-Cookie value for a raw session token. HttpOnly blocks any
// XSS-injected script from reading the token; Secure keeps it off plaintext HTTP; SameSite=Strict
// (per §4) means the cookie is never attached to a cross-site request at all, which is the
// primary CSRF defense described in the plan; Path=/ is required for the __Host- prefix to be
// legal and matches the admin surface being served from the same origin's root.
func NewSessionCookie(rawToken string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName(),
		Value:    rawToken,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// ExpiredSessionCookie builds a Set-Cookie value that instructs the browser to delete the
// session cookie immediately (logout / revocation), using the same flags as NewSessionCookie so
// browsers recognize it as clearing the same __Host- cookie rather than setting a different one.
func ExpiredSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName(),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// ErrTokenEmpty is returned by callers that look up a session from an empty cookie value, kept
// here so session-lookup code across the package (and its future callers in the control plane)
// shares one sentinel rather than each inventing its own "no token" error.
var ErrTokenEmpty = errors.New("auth: empty session token")
