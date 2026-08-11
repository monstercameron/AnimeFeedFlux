package rpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// The session-management half of AuthService — Logout, Session, ListSessions,
// RevokeSession, RevokeAllSessions, ReenrollTOTP, RegenerateRecoveryCodes —
// had no tests. It is the surface an operator reaches for when they think
// they have been compromised, so "it reported success" is not the property
// that matters; "the session is actually dead afterwards" is.

// loginSession signs in and returns the caller context a real request would
// carry, plus the session's id and token hash.
func loginSession(t *testing.T, srv *AuthServer, st *store.Store, secret string) (context.Context, callerSession) {
	t.Helper()
	ctx, _ := withTransportStream(withPeerIP(t.Context(), "203.0.113.7"), affv1.AuthService_Login_FullMethodName)
	resp, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	id := resp.GetSession().GetId()
	if id == "" {
		t.Fatal("login returned a session with no id")
	}

	// The token hash is what the interceptor would attach; read it back from
	// the row the login just wrote rather than recomputing it here.
	var hash string
	if err := st.Writer().QueryRowContext(t.Context(),
		`SELECT token_hash FROM sessions WHERE id = ?`, id).Scan(&hash); err != nil {
		t.Fatalf("reading session hash: %v", err)
	}
	cs := callerSession{ID: parseSessionID(t, id), TokenHash: hash}
	return withCallerSession(withPeerIP(t.Context(), "203.0.113.7"), cs), cs
}

// otherSession inserts a second live session directly, rather than logging in
// twice: a second Login inside the same 30-second TOTP step is correctly
// refused as a replay, and stepping the clock forward to dodge that would be
// testing the TOTP window rather than the session list.
func otherSession(t *testing.T, st *store.Store, hash string) int64 {
	t.Helper()
	now := time.Now()
	id, err := st.CreateSession(t.Context(), auth.Session{
		TokenHash: hash, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		IP: "198.51.100.4", UserAgent: "another-browser",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return id
}

func parseSessionID(t *testing.T, id string) int64 {
	t.Helper()
	var n int64
	for _, r := range id {
		if r < '0' || r > '9' {
			t.Fatalf("session id %q is not numeric", id)
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func sessionRevoked(t *testing.T, st *store.Store, id int64) bool {
	t.Helper()
	var revoked any
	if err := st.Writer().QueryRowContext(context.Background(),
		`SELECT revoked_at FROM sessions WHERE id = ?`, id).Scan(&revoked); err != nil {
		t.Fatalf("reading session %d: %v", id, err)
	}
	return revoked != nil
}

func TestEverySessionRPCRefusesAnUnauthenticatedCaller(t *testing.T) {
	// The interceptor is what normally attaches the caller session, but each
	// method re-checks rather than trusting that it ran — this is the check
	// that keeps a routing mistake from becoming an authentication bypass.
	srv, _, _ := newTestServer(t)
	ctx := t.Context()

	calls := map[string]func() error{
		"Logout":       func() error { _, err := srv.Logout(ctx, &affv1.AuthServiceLogoutRequest{}); return err },
		"Session":      func() error { _, err := srv.Session(ctx, &affv1.AuthServiceSessionRequest{}); return err },
		"ListSessions": func() error { _, err := srv.ListSessions(ctx, &affv1.AuthServiceListSessionsRequest{}); return err },
		"RevokeSession": func() error {
			_, err := srv.RevokeSession(ctx, &affv1.AuthServiceRevokeSessionRequest{SessionId: "1"})
			return err
		},
		"RevokeAllSessions": func() error {
			_, err := srv.RevokeAllSessions(ctx, &affv1.AuthServiceRevokeAllSessionsRequest{})
			return err
		},
		"ReenrollTOTP": func() error { _, err := srv.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{}); return err },
		"RegenerateRecoveryCodes": func() error {
			_, err := srv.RegenerateRecoveryCodes(ctx, &affv1.AuthServiceRegenerateRecoveryCodesRequest{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("%s with no session returned %v, want Unauthenticated", name, err)
			}
		})
	}
}

func TestLogoutRevokesOnlyTheCallingSession(t *testing.T) {
	srv, st, secret := newTestServer(t)

	otherID := otherSession(t, st, "hash-other")
	ctx, caller := loginSession(t, srv, st, secret)

	if _, err := srv.Logout(ctx, &affv1.AuthServiceLogoutRequest{}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !sessionRevoked(t, st, caller.ID) {
		t.Error("the calling session is still live after Logout")
	}
	if sessionRevoked(t, st, otherID) {
		t.Error("Logout revoked another session — that is RevokeAllSessions' job (§11)")
	}
}

func TestSessionReportsTheCallerAndTheRecoveryCodeCount(t *testing.T) {
	// §12.5 requires Settings to show the remaining count, and until this
	// field existed it was only ever reported to someone already locked out
	// and spending one.
	srv, st, secret := newTestServer(t)
	ctx, caller := loginSession(t, srv, st, secret)

	if err := st.StoreRecoveryCodes(t.Context(), []string{"h1", "h2", "h3"}); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}
	if err := st.UseRecoveryCode(t.Context(), 0); err != nil {
		t.Fatalf("UseRecoveryCode: %v", err)
	}

	resp, err := srv.Session(ctx, &affv1.AuthServiceSessionRequest{})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !resp.GetSession().GetIsCurrent() {
		t.Error("the caller's own session did not come back marked current")
	}
	if got := resp.GetRemainingRecoveryCodes(); got != 2 {
		t.Errorf("remaining recovery codes = %d, want 2 (three stored, one spent)", got)
	}
	if resp.GetSession().GetId() == "" {
		t.Error("session response carries no id")
	}
	_ = caller
}

func TestSessionRejectsATokenThatIsNoLongerARow(t *testing.T) {
	// A caller context can outlive the row (a purge, a restored backup). That
	// must read as "invalid session", not as a successful whoami.
	srv, _, _ := newTestServer(t)
	ctx := withCallerSession(t.Context(), callerSession{ID: 999, TokenHash: "hash-that-never-existed"})

	if _, err := srv.Session(ctx, &affv1.AuthServiceSessionRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Session with an unknown token returned %v, want Unauthenticated", err)
	}
}

func TestListSessionsMarksExactlyOneRowAsCurrent(t *testing.T) {
	// The current row is the one the operator must NOT revoke by accident, so
	// mislabelling it is how someone signs themselves out while trying to
	// evict an intruder.
	srv, st, secret := newTestServer(t)
	otherSession(t, st, "hash-other")
	ctx, caller := loginSession(t, srv, st, secret)

	resp, err := srv.ListSessions(ctx, &affv1.AuthServiceListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.GetSessions()) != 2 {
		t.Fatalf("got %d sessions, want 2", len(resp.GetSessions()))
	}

	current := 0
	for _, s := range resp.GetSessions() {
		if s.GetIsCurrent() {
			current++
			if s.GetId() != itoa(caller.ID) {
				t.Errorf("current session id = %q, want %q", s.GetId(), itoa(caller.ID))
			}
		}
		if s.GetIp() == "" {
			t.Error("a session row came back with no IP — the table exists to show where a session came from")
		}
	}
	if current != 1 {
		t.Errorf("%d sessions claim to be current, want exactly 1", current)
	}
}

func TestListSessionsIncludesRevokedRows(t *testing.T) {
	// Revoked rows are shown (sorted below the live ones by the UI), because
	// "a session that was revoked an hour ago" is evidence, not noise.
	srv, st, secret := newTestServer(t)
	goneID := otherSession(t, st, "hash-gone")
	ctx, _ := loginSession(t, srv, st, secret)

	if err := st.RevokeSession(t.Context(), goneID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	resp, err := srv.ListSessions(ctx, &affv1.AuthServiceListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var sawRevoked bool
	for _, s := range resp.GetSessions() {
		if s.GetId() == itoa(goneID) {
			sawRevoked = s.GetRevokedAt() != nil
		}
	}
	if !sawRevoked {
		t.Error("a revoked session was dropped from the list, or came back with no revoked_at")
	}
}

func TestRevokeSessionKillsTheNamedRow(t *testing.T) {
	srv, st, secret := newTestServer(t)
	victimID := otherSession(t, st, "hash-victim")
	ctx, caller := loginSession(t, srv, st, secret)

	if _, err := srv.RevokeSession(ctx, &affv1.AuthServiceRevokeSessionRequest{SessionId: itoa(victimID)}); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if !sessionRevoked(t, st, victimID) {
		t.Error("the named session is still live")
	}
	if sessionRevoked(t, st, caller.ID) {
		t.Error("revoking another session also killed the caller's")
	}
}

func TestRevokeSessionRejectsBadInput(t *testing.T) {
	srv, st, secret := newTestServer(t)
	ctx, _ := loginSession(t, srv, st, secret)

	if _, err := srv.RevokeSession(ctx, &affv1.AuthServiceRevokeSessionRequest{SessionId: "not-a-number"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a non-numeric id returned %v, want InvalidArgument", err)
	}
	if _, err := srv.RevokeSession(ctx, &affv1.AuthServiceRevokeSessionRequest{SessionId: "99999"}); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown id returned %v, want NotFound", err)
	}
}

func TestRevokeAllSessionsIncludesTheCallersOwn(t *testing.T) {
	// "Sign out everywhere" that leaves the caller signed in is the one
	// outcome that makes the button useless in the situation it exists for.
	srv, st, secret := newTestServer(t)
	otherID := otherSession(t, st, "hash-other")
	ctx, caller := loginSession(t, srv, st, secret)

	resp, err := srv.RevokeAllSessions(ctx, &affv1.AuthServiceRevokeAllSessionsRequest{})
	if err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if got := resp.GetRevokedCount(); got != 2 {
		t.Errorf("revoked count = %d, want 2", got)
	}
	if !sessionRevoked(t, st, caller.ID) || !sessionRevoked(t, st, otherID) {
		t.Error("a session survived sign-out-everywhere")
	}

	// Running it again reports zero rather than double-counting rows it
	// already revoked.
	resp, err = srv.RevokeAllSessions(ctx, &affv1.AuthServiceRevokeAllSessionsRequest{})
	if err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if got := resp.GetRevokedCount(); got != 0 {
		t.Errorf("second revoke-all reported %d rows, want 0", got)
	}
}

func TestReenrollTOTPRequiresTheCurrentPassword(t *testing.T) {
	// A stolen but still-live session must not be enough to replace the
	// second factor — that would turn session theft into full account
	// takeover (§4).
	srv, st, secret := newTestServer(t)
	ctx, _ := loginSession(t, srv, st, secret)

	before, err := st.GetTOTPSecret(t.Context())
	if err != nil {
		t.Fatalf("GetTOTPSecret: %v", err)
	}

	if _, err := srv.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{CurrentPassword: "wrong"}); err == nil {
		t.Fatal("re-enrollment succeeded with the wrong password")
	}
	after, err := st.GetTOTPSecret(t.Context())
	if err != nil {
		t.Fatalf("GetTOTPSecret: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("a rejected re-enrollment still replaced the secret")
	}

	resp, err := srv.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{CurrentPassword: testPassword})
	if err != nil {
		t.Fatalf("ReenrollTOTP: %v", err)
	}
	if resp.GetProvisioningUri() == "" {
		t.Error("no provisioning URI came back — there is nothing for the operator to scan")
	}
	rotated, err := st.GetTOTPSecret(t.Context())
	if err != nil {
		t.Fatalf("GetTOTPSecret: %v", err)
	}
	if string(rotated) == string(before) {
		t.Error("the stored secret did not change")
	}
	// Whatever is stored is encrypted at rest: the raw base32 secret must not
	// be sitting in the column (§4 — a stolen DB file is not a second factor).
	if containsBytes(rotated, []byte(secret)) {
		t.Error("the TOTP secret is stored in the clear")
	}
}

func TestReenrollTOTPFromAnElevatedSessionNeedsNoPassword(t *testing.T) {
	// The recovery flow's whole point: the password is the thing that was
	// forgotten, so demanding it here would make recovery impossible.
	srv, st, secret := newTestServer(t)
	_, caller := loginSession(t, srv, st, secret)
	otherSession(t, st, "hash-other")
	elevated := withCallerSession(withPeerIP(t.Context(), "203.0.113.7"),
		callerSession{ID: caller.ID, TokenHash: caller.TokenHash, Elevated: true})

	if _, err := srv.ReenrollTOTP(elevated, &affv1.AuthServiceReenrollTOTPRequest{}); err != nil {
		t.Fatalf("ReenrollTOTP from an elevated session: %v", err)
	}
	// ...and it ends the elevated session, which is what forces the full
	// re-login §12.2 describes.
	if !sessionRevoked(t, st, caller.ID) {
		t.Error("the elevated session survived its own re-enrollment")
	}
}

func TestRegenerateRecoveryCodesReplacesTheWholeSet(t *testing.T) {
	srv, st, secret := newTestServer(t)
	ctx, _ := loginSession(t, srv, st, secret)

	if err := st.StoreRecoveryCodes(t.Context(), []string{"old-1", "old-2"}); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}

	// Wrong credentials change nothing.
	if _, err := srv.RegenerateRecoveryCodes(ctx, &affv1.AuthServiceRegenerateRecoveryCodesRequest{
		CurrentPassword: "wrong", TotpCode: validCode(t, secret, time.Now()),
	}); err == nil {
		t.Fatal("regeneration succeeded with the wrong password")
	}
	if n := countRecoveryCodes(t, st); n != 2 {
		t.Fatalf("a rejected regeneration changed the stored set (%d codes)", n)
	}

	resp, err := srv.RegenerateRecoveryCodes(ctx, &affv1.AuthServiceRegenerateRecoveryCodesRequest{
		CurrentPassword: testPassword,
		TotpCode:        validCode(t, secret, time.Now().Add(31*time.Second)),
	})
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes: %v", err)
	}
	if len(resp.GetRecoveryCodes()) != recoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(resp.GetRecoveryCodes()), recoveryCodeCount)
	}
	// The plaintext codes are returned exactly once, here — and the old set
	// is gone, not appended to.
	remaining, err := st.CountUnusedRecoveryCodes(t.Context())
	if err != nil {
		t.Fatalf("CountUnusedRecoveryCodes: %v", err)
	}
	if remaining != recoveryCodeCount {
		t.Errorf("%d unused codes after regeneration, want %d — the old set was not replaced", remaining, recoveryCodeCount)
	}
	for _, c := range resp.GetRecoveryCodes() {
		if c == "" {
			t.Error("an empty recovery code was handed out")
		}
	}
}

func countRecoveryCodes(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Writer().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM recovery_codes`).Scan(&n); err != nil {
		t.Fatalf("counting recovery codes: %v", err)
	}
	return n
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
