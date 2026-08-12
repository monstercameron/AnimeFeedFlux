package wsconn

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sessionexpiry_test.go pins the one judgement call that decides whether an
// admin gets signed out: which Unauthenticated errors mean "your session is
// gone" and which mean "that credential was wrong".
//
// Getting this wrong is expensive in both directions. Too narrow and session
// expiry goes back to being invisible — the state the app was in until
// 2026-08-11, where the machinery existed and nothing ever triggered it. Too
// broad and a mistyped password in Settings signs the admin out and throws
// away whatever else they had open.

func TestSessionGoneErrorsAreRecognised(t *testing.T) {
	// Exactly the messages internal/rpc/interceptor.go returns when the
	// session is missing, unparseable, or past its lifetime.
	for _, msg := range []string{"no session", "invalid session", "session expired"} {
		if !IsSessionExpired(status.Error(codes.Unauthenticated, msg)) {
			t.Errorf("IsSessionExpired(Unauthenticated %q) = false; session expiry would stay invisible", msg)
		}
	}
}

// TestWrongCredentialsAreNotTreatedAsExpiry is the expensive direction. The
// server returns one deliberately generic message for every credential
// failure (PLAN.md §12.1's no-oracle rule), and that message must never sign
// anyone out.
func TestWrongCredentialsAreNotTreatedAsExpiry(t *testing.T) {
	if IsSessionExpired(status.Error(codes.Unauthenticated, "authentication failed")) {
		t.Error("a wrong password would sign the admin out and discard their open work")
	}
}

func TestOtherFailuresAreNotExpiry(t *testing.T) {
	cases := []error{
		nil,
		errors.New("plain error, not a status"),
		status.Error(codes.Unavailable, "no session"),        // right message, wrong code
		status.Error(codes.PermissionDenied, "no session"),   // ditto
		status.Error(codes.Internal, "session expired"),      // ditto
		status.Error(codes.Unauthenticated, "totp required"), // right code, not a session failure
		status.Error(codes.Unauthenticated, ""),
		status.Error(codes.NotFound, "feed not found"),
		status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
	}
	for _, err := range cases {
		if IsSessionExpired(err) {
			t.Errorf("IsSessionExpired(%v) = true; only a genuine session failure may sign the admin out", err)
		}
	}
}

// TestMessageMatchIsExact guards the closed set. A substring match would
// eventually swallow something it should not — an error that merely mentions
// a session in passing is not a statement that this one is gone.
func TestMessageMatchIsExact(t *testing.T) {
	for _, msg := range []string{
		"no session cookie was supplied by the proxy",
		"invalid session id format",
		"the session expired at 12:00",
		"session",
		" no session",
		"No Session",
	} {
		if IsSessionExpired(status.Error(codes.Unauthenticated, msg)) {
			t.Errorf("IsSessionExpired(%q) = true; the match must be exact, not a substring or case fold", msg)
		}
	}
}
