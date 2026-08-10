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
	srv, st, secret := newTestServer(t)
	base := time.Now()

	// Two independent logins ("two browsers/devices"), then ChangePassword
	// from the second one. ValidateCode accepts the current TOTP step and its
	// immediate ±1 neighbors (§4's drift window), and this whole test runs in
	// well under 30s of wall-clock time, so offsetting by exactly one period
	// each way lands each call on a DISTINCT step (base-30s, base, base+30s)
	// while staying inside the real skew window around whatever the actual
	// wall clock is when each RPC executes — avoiding a same-step TOTP replay
	// between the three calls without needing to fake the server's clock
	// (srv.now is unexported and unreachable from this package).
	tokenA := login(t, srv, secret, "10.30.0.1", base)
	tokenB := login(t, srv, secret, "10.30.0.2", base.Add(30*time.Second))

	changeAt := base.Add(-30 * time.Second)
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
