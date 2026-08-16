package sectest

import (
	"context"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// SEC-46: changing the password must revoke every OTHER live session. If a
// stolen-but-not-yet-detected session survives a deliberate password change,
// the change accomplished nothing against the actual threat it exists for —
// an attacker who already has a session keeps it regardless of how strong
// the new password is.
func TestChangePassword_RevokesOtherSessions(t *testing.T) {
	// TOTP replay rejection is DISABLED for this one test — the same
	// rpc.WithDevInsecureAuth production refuses on non-loopback listeners —
	// because this test's subject is session revocation, not replay, and
	// replay rejection is precisely what made it flaky: three TOTP-checked
	// calls need three DISTINCT steps, one of them necessarily the previous
	// step with only ~15s of drift-window slack, and each call spends
	// multiple argon2id verifications before its code is even looked at.
	// Under CI's -race and a parallel suite that slack was intermittently
	// exceeded and the test failed with a generic "authentication failed"
	// that reads exactly like a real auth bug (observed locally 2026-08-15
	// and twice in CI the same day). With replay off, every call uses a
	// current-step code computed at call time — deterministic at any
	// machine speed. Replay rejection itself keeps its own dedicated,
	// single-call test (SEC-44), which has no such timing shape.
	srv, st, secret := newTestServer(t, rpc.WithDevInsecureAuth())
	tokenA := login(t, srv, secret, "10.30.0.1", time.Now())
	tokenB := login(t, srv, secret, "10.30.0.2", time.Now())

	changeAt := time.Now()
	newPassword := "a brand new much longer passphrase for sec46"

	ctx := rpc.ContextWithSessionToken(t.Context(), tokenB)
	_, err := callAuthed(ctx, srv, affv1.AuthService_ChangePassword_FullMethodName,
		&affv1.AuthServiceChangePasswordRequest{
			CurrentPassword: testPassword,
			TotpCode:        validCode(t, secret, changeAt),
			NewPassword:     newPassword,
		},
		func(ctx context.Context, req any) (any, error) {
			return srv.ChangePassword(ctx, req.(*affv1.AuthServiceChangePasswordRequest))
		})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}

	sessA, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(tokenA))
	if err != nil {
		t.Fatalf("looking up session A: %v", err)
	}
	if sessA.RevokedAt.IsZero() {
		t.Error("session A (not the caller) should have been revoked by the password change, but is still live")
	}

	sessB, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(tokenB))
	if err != nil {
		t.Fatalf("looking up session B: %v", err)
	}
	if !sessB.RevokedAt.IsZero() {
		t.Error("session B (the caller) should NOT have been revoked by its own ChangePassword call")
	}

	// The new password must actually be the one now stored, and the OLD one
	// must no longer work — a password "change" that keeps the old password
	// verifying too would be a much worse bug than any of the above.
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if ok, _, verr := auth.Verify(newPassword, admin.PasswordHash); verr != nil || !ok {
		t.Error("the new password does not verify against the stored hash")
	}
	if ok, _, verr := auth.Verify(testPassword, admin.PasswordHash); verr == nil && ok {
		t.Error("the OLD password still verifies against the stored hash after a change")
	}
}
