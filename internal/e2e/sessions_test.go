package e2e

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// TestChangePasswordRevokesOtherSessions is SEC-46, observed over the real
// bridge: two independent, real sessions are opened (two separate
// grpctunnel connections, exactly as two browser tabs would hold), then
// AuthService.ChangePassword is called on the FIRST. internal/rpc/auth.go's
// ChangePassword calls s.revokeOtherSessions(ctx, cs.TokenHash) — every
// session but the caller's own. The proof that matters here is not a row in
// `sessions`; it is that the SECOND session's very next real RPC over its
// own live bridge connection is refused, and the first session's is not.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: full end-to-end suite skipped in -short mode")
	}

	ctx := context.Background()
	app := New(t)

	totpSecret, err := app.InitAdmin(ctx, adminPassword)
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	// Three logically distinct TOTP steps computed from one reference
	// instant (see App.LoginAt's doc comment for why an offset in TOTP
	// PERIODS, not an arbitrary duration, is what keeps a code valid): the
	// server's real ±1-step skew (internal/auth/totp.go's totpSkew) only
	// accepts a step within one 30s period of whatever "now" genuinely is
	// when the RPC lands, so across this whole fast test only THREE
	// distinct steps are ever simultaneously valid: center-1, center,
	// center+1. Each RFC 6238 step can only be marked used once
	// (store.ErrTOTPReplay), so the three real TOTP-consuming calls below
	// (login1, login2, ChangePassword) are pinned one to each of those
	// three steps, in this specific order, and nothing else in this test
	// consumes a fourth.
	const totpPeriod = 30 * time.Second
	ref := time.Now()
	// The three-step pinning below assumes the server's "now" stays inside
	// ref's own 30s step for the whole test: the ±1 skew window is anchored
	// to the server clock at VALIDATION time, so if a step boundary passes
	// between here and the ChangePassword call (~2-3s of real RPCs away),
	// center-1 falls out of the window and the call fails Unauthenticated.
	// That is exactly how this test flaked on the v0.2.0 release gate
	// (passed dev CI, failed the tag run minutes later). If the boundary is
	// close, start the test on the far side of it instead — a bounded, rare
	// sleep beats a release gate that fails on clock phase.
	if untilBoundary := totpPeriod - time.Duration(ref.UnixNano())%totpPeriod; untilBoundary < 10*time.Second {
		time.Sleep(untilBoundary + time.Second)
		ref = time.Now()
	}

	login1, err := app.LoginAt(ctx, adminPassword, totpSecret, ref)
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	defer login1.Close()

	login2, err := app.LoginAt(ctx, adminPassword, totpSecret, ref.Add(totpPeriod))
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}
	defer login2.Close()

	// Sanity: both sessions are independently live before anything revokes
	// either of them.
	if _, err := login1.Clients.System.Version(ctx, &affv1.SystemServiceVersionRequest{}); err != nil {
		t.Fatalf("session 1 RPC before ChangePassword: %v", err)
	}
	if _, err := login2.Clients.System.Version(ctx, &affv1.SystemServiceVersionRequest{}); err != nil {
		t.Fatalf("session 2 RPC before ChangePassword: %v", err)
	}

	// The third and last available step: center-1.
	changeCode, err := TOTPCode(totpSecret, ref.Add(-totpPeriod))
	if err != nil {
		t.Fatalf("computing ChangePassword's totp code: %v", err)
	}
	const newPassword = "a brand new correct horse battery staple e2e"
	_, err = login1.Clients.Auth.ChangePassword(ctx, &affv1.AuthServiceChangePasswordRequest{
		CurrentPassword: adminPassword,
		TotpCode:        changeCode,
		NewPassword:     newPassword,
	})
	if err != nil {
		t.Fatalf("AuthService.ChangePassword: %v", err)
	}

	// --- the caller's OWN session (login1) must still work ---
	if _, err := login1.Clients.System.Version(ctx, &affv1.SystemServiceVersionRequest{}); err != nil {
		t.Fatalf("session 1 (the caller) RPC after ChangePassword: %v, want success — ChangePassword must not log itself out", err)
	}

	// --- the OTHER session (login2) must be refused on its very next real
	// RPC over the real bridge — not merely a stale row in `sessions` ---
	_, err = login2.Clients.System.Version(ctx, &affv1.SystemServiceVersionRequest{})
	if err == nil {
		t.Fatal("session 2's RPC succeeded after ChangePassword revoked it — SEC-46 is broken")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("session 2's RPC after revocation failed with %v, want Unauthenticated", err)
	}

	// --- confirm the new password actually took effect: a fresh Login
	// attempt with the OLD password must now fail. This does NOT consume a
	// fourth TOTP step: AuthServer.Login checks the password BEFORE the
	// TOTP code (internal/rpc/auth.go, "TOTP AFTER password, per §4"), so a
	// wrong password is refused without ever validating (or marking used)
	// whatever code travels alongside it — confirmed directly by reusing
	// login1's ALREADY-CONSUMED code here on purpose: if Login reached the
	// TOTP check at all, a replayed code would produce the identical
	// generic failure, so this alone would not distinguish "wrong
	// password" from "reused code". The real distinguishing proof is
	// ChangePassword's own success above, which could only have happened
	// against the OLD password (it is the CURRENT one at that point) — this
	// call is just the corroborating negative case.
	loginCode1, err := TOTPCode(totpSecret, ref)
	if err != nil {
		t.Fatalf("recomputing login1's code: %v", err)
	}
	if _, err := app.LoginWithCode(ctx, adminPassword, loginCode1); err == nil {
		t.Fatal("Login with the OLD password succeeded after ChangePassword")
	}
}
