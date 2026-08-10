package auth

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestNewTokenEntropyAndUniqueness(t *testing.T) {
	raw1, hash1, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	raw2, hash2, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw1)
	if err != nil {
		t.Fatalf("raw token is not valid base64url: %v", err)
	}
	if len(decoded) != tokenBytes {
		t.Fatalf("raw token decodes to %d bytes, want %d (256 bits)", len(decoded), tokenBytes)
	}

	if raw1 == raw2 {
		t.Fatal("NewToken: two calls produced the same raw token")
	}
	if hash1 == hash2 {
		t.Fatal("NewToken: two calls produced the same hash")
	}

	// Only the hash should be derivable/storable as "the token": verify the hash is a pure
	// function of the raw value (so a verifier can recompute it) but is not the raw value
	// itself, and that a different raw token cannot pass the wrong hash.
	if hash1 == raw1 {
		t.Fatal("NewToken: hash equals raw token, should be a distinct derived value")
	}
	if HashToken(raw1) != hash1 {
		t.Fatal("HashToken(raw): does not reproduce the hash returned by NewToken")
	}
	if HashToken(raw2) == hash1 {
		t.Fatal("HashToken: a different raw token produced the same stored hash")
	}
}

func TestSessionValidHappyPath(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s := Session{
		TokenHash:  "irrelevant-for-this-check",
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsoluteLifetime),
	}
	if !s.Valid(now, SessionIdleTimeout) {
		t.Fatal("Valid: freshly created session should be valid")
	}
	// Still within idle timeout and absolute lifetime a bit later.
	later := now.Add(30 * time.Minute)
	s.LastSeenAt = now // simulate no activity refresh yet
	if !s.Valid(later, SessionIdleTimeout) {
		t.Fatal("Valid: session within idle timeout and absolute lifetime should be valid")
	}
}

func TestSessionValidExpiredAbsolute(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s := Session{
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsoluteLifetime),
	}
	// Even with constant activity keeping LastSeenAt fresh, absolute lifetime must still cut
	// the session off — that's the whole point of a second, independent clock.
	past := now.Add(SessionAbsoluteLifetime + time.Second)
	s.LastSeenAt = past
	if s.Valid(past, SessionIdleTimeout) {
		t.Fatal("Valid: session past absolute lifetime should be invalid even if recently active")
	}
}

func TestSessionValidExpiredIdle(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s := Session{
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsoluteLifetime), // far in the future
	}
	idleExpired := now.Add(SessionIdleTimeout + time.Second)
	if s.Valid(idleExpired, SessionIdleTimeout) {
		t.Fatal("Valid: session idle beyond idle timeout should be invalid even though absolute lifetime remains")
	}
}

func TestSessionValidRevoked(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s := Session{
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsoluteLifetime),
		RevokedAt:  now, // revoked immediately
	}
	if s.Valid(now, SessionIdleTimeout) {
		t.Fatal("Valid: revoked session should be invalid at the moment of revocation")
	}
	if s.Valid(now.Add(time.Second), SessionIdleTimeout) {
		t.Fatal("Valid: revoked session should stay invalid after RevokedAt")
	}
	// Before RevokedAt, the session hadn't been revoked yet, so it should still be usable —
	// this pins down that Valid checks "is RevokedAt <= now", not some other relation.
	s.LastSeenAt = now.Add(-time.Second)
	if !s.Valid(now.Add(-time.Second), SessionIdleTimeout) {
		t.Fatal("Valid: session should still be valid strictly before its RevokedAt timestamp")
	}
}

func TestSessionValidNotRevokedZeroValue(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	s := Session{
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionAbsoluteLifetime),
		// RevokedAt intentionally left zero-valued.
	}
	if !s.Valid(now, SessionIdleTimeout) {
		t.Fatal("Valid: zero-value RevokedAt must mean not revoked")
	}
}

func TestCookieNameIsHostPrefixed(t *testing.T) {
	name := CookieName()
	const prefix = "__Host-"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		t.Fatalf("CookieName() = %q, want __Host- prefix", name)
	}
}

func TestNewSessionCookieFlags(t *testing.T) {
	expires := time.Now().Add(SessionAbsoluteLifetime)
	c := NewSessionCookie("raw-token-value", expires)

	if c.Name != CookieName() {
		t.Errorf("cookie Name = %q, want %q", c.Name, CookieName())
	}
	if !c.HttpOnly {
		t.Error("cookie missing HttpOnly")
	}
	if !c.Secure {
		t.Error("cookie missing Secure")
	}
	if c.SameSite != 3 { // http.SameSiteStrictMode
		t.Errorf("cookie SameSite = %v, want SameSiteStrictMode", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q, want \"/\" (required for __Host- prefix validity)", c.Path)
	}
	if c.Value != "raw-token-value" {
		t.Errorf("cookie Value = %q, want the raw token", c.Value)
	}
	if !c.Expires.Equal(expires) {
		t.Errorf("cookie Expires = %v, want %v", c.Expires, expires)
	}
}

func TestExpiredSessionCookieClearsSameCookie(t *testing.T) {
	c := ExpiredSessionCookie()
	if c.Name != CookieName() {
		t.Errorf("cookie Name = %q, want %q (must clear the same cookie)", c.Name, CookieName())
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != 3 || c.Path != "/" {
		t.Error("ExpiredSessionCookie must carry the same security flags as NewSessionCookie")
	}
	if c.MaxAge >= 0 {
		t.Errorf("cookie MaxAge = %d, want negative to force immediate deletion", c.MaxAge)
	}
}
