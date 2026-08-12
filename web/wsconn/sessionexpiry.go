package wsconn

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sessionexpiry.go answers "did that RPC fail because the session is gone?"
//
// # Why this had to exist at all
//
// appstate.EvSessionExpired has a transition, a "don't lose unsaved work"
// hold (web/shell/session.go), a blocking modal (expiry.go) and catalogue
// copy in two languages — and until 2026-08-11 **nothing in the client ever
// emitted it**. web/wsconn emitted exactly two events, EvWSDropped and
// EvWSReconnected, both derived from grpc-go connectivity state. A session
// dying server-side is invisible to connectivity: the socket stays perfectly
// healthy and every RPC over it starts coming back Unauthenticated.
//
// So the observed behaviour was an admin left on a page that rendered fine,
// with a header that still looked signed in, where every action failed. The
// expiry machinery was complete and unreachable.
//
// # Distinguishing a dead session from a wrong password
//
// Both are codes.Unauthenticated, and treating them the same would be worse
// than doing nothing: a mistyped current password in Settings → Change
// password would sign the admin out and bounce them to /login, losing
// whatever else they had open. So the code alone is not enough.
//
// internal/rpc separates them deliberately, for its own reasons: session
// failures name the session ("no session", "invalid session", "session
// expired" — interceptor.go), while every credential failure returns one
// deliberately generic errAuthFailed ("authentication failed") because
// PLAN.md §12.1 forbids an oracle that distinguishes "no such account" from
// "wrong password". That generic-by-design credential error is what makes
// message matching safe here: the set below cannot accidentally grow to
// include a credential failure, because credential failures are contractually
// not allowed to say anything specific.
//
// # This coupling is on prose, and that is stated rather than hidden
//
// Matching on message text is normally the wrong instinct — this repository
// argues exactly that elsewhere, preferring gRPC codes because "the message
// is prose that may be reworded, the code is the contract". The honest
// version of the trade is: the durable fix is a machine-readable signal from
// the server (a trailer such as `aff-session: expired`), the client half of
// which would be a one-line change here. Until then the coupling lives in ONE
// function with the strings named as constants, so a reword breaks one test
// with a clear message instead of silently disabling session expiry
// everywhere.
const (
	sessionErrNoSession      = "no session"
	sessionErrInvalidSession = "invalid session"
	sessionErrExpiredSession = "session expired"
)

// sessionGoneMessages is the closed set of server messages that mean "the
// session this connection was using is no longer valid". Closed on purpose:
// anything not listed is treated as an ordinary RPC failure for the calling
// page to render, never as a reason to sign the operator out.
var sessionGoneMessages = map[string]bool{
	sessionErrNoSession:      true,
	sessionErrInvalidSession: true,
	sessionErrExpiredSession: true,
}

// IsSessionExpired reports whether err means the caller's session is gone,
// as opposed to any other Unauthenticated failure.
//
// Exported for the test that pins this against internal/rpc's actual errors;
// the wiring itself is in clients.go's guardUnary/guardCall.
func IsSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		return false
	}
	return sessionGoneMessages[st.Message()]
}
