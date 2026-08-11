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
	// Two independent logins ("two browsers/devices"), then ChangePassword
	// from the second one. ValidateCode accepts the current TOTP step and its
	// immediate ±1 neighbours (§4's drift window), so the three calls need
	// three DISTINCT steps to avoid a same-step replay — while each stays
	// inside the window around the wall clock at the moment the SERVER
	// validates.
	//
	// Each offset is therefore computed at call time, not from one instant
	// captured up front. Steps are aligned to absolute unix time (unix/30),
	// not to any captured `base`: once the wall clock crosses a boundary
	// between capturing base and the server validating — which argon2id at
	// DefaultParams under a parallel suite run easily takes — a base-30s code
	// sits two steps from the server's centre and is refused. That surfaced
	// as an intermittent "authentication failed" that reads exactly like a
	// real auth bug. Anchoring mid-step leaves 15s of slack either side.
	tokenA := login(t, srv, secret, "10.30.0.1", stepTime(0))
	tokenB := login(t, srv, secret, "10.30.0.2", stepTime(1))

	changeAt := stepTime(-1)
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

// stepTime returns an instant mid-way through the TOTP step `steps` away
// from the one the wall clock is in right now. See the comment in
// TestChangePassword_RevokesOtherSessions for why the offset is taken from
// the live clock rather than a captured instant.
func stepTime(steps int64) time.Time {
	step := time.Now().Unix()/30 + steps
	return time.Unix(step*30+15, 0)
}
