package bridge

import (
	"context"
	"time"
)

// Session is the validated identity attached to a socket after the cookie
// check at upgrade, and re-attached to every RPC's context because it rides
// on the underlying HTTP/2 request context grpctunnel derives per call.
type Session struct {
	// UserID identifies the authenticated admin. Single-user system (PLAN.md
	// §4), so this is informational/logging value today, not an authz key.
	UserID string
	// ExpiresAt is the session's absolute expiry, used only for logging and
	// diagnostics here — the source of truth for "is this session still
	// good right now" is always a fresh call to SessionValidator.Validate,
	// never a comparison against this cached field, so revocation (which
	// changes validity without changing ExpiresAt) is caught too.
	ExpiresAt time.Time
}

// SessionValidator checks a raw session cookie value against store state as
// of now and returns the resolved Session, or a non-nil error if the token
// is missing, malformed, expired, or revoked. Implemented by internal/auth;
// this package only depends on the interface so it stays testable without a
// real store.
type SessionValidator interface {
	Validate(ctx context.Context, token string, now time.Time) (Session, error)
}

// SessionValidatorFunc adapts a plain function to SessionValidator.
type SessionValidatorFunc func(ctx context.Context, token string, now time.Time) (Session, error)

// Validate implements SessionValidator.
func (f SessionValidatorFunc) Validate(ctx context.Context, token string, now time.Time) (Session, error) {
	return f(ctx, token, now)
}

// sessionContextKey is unexported so nothing outside this package can spoof
// or accidentally collide with the value; SessionFromContext/WithSession are
// the only sanctioned door in and out.
type sessionContextKey struct{}

// WithSession returns a context carrying session. NewServer calls this once
// per accepted socket, before handing the request to the bridge handler, so
// every RPC on that connection inherits it via grpctunnel's per-request
// context (derived from the same base context the upgrade handler builds).
func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, session)
}

// SessionFromContext retrieves the Session set by WithSession.
//
// Coupling note (see also the package doc comment): PLAN.md §4/§18 expects
// internal/rpc's auth interceptor to pull the session back out of the RPC
// context. internal/rpc does not exist yet in this tree (no
// internal/rpc/interceptor.go to read), so this accessor pair is this
// package's contract for that future interceptor: call
// bridge.SessionFromContext(ctx) rather than reaching for a raw context key.
// If internal/rpc lands with a different expectation, prefer changing that
// interceptor to use this accessor over duplicating a second key.
func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(Session)
	return session, ok
}
