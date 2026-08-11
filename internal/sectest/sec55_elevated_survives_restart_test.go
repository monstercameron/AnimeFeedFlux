package sectest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// A8-31: an elevated recovery session became a FULL session across a process
// restart.
//
// authorize decided scope purely from the in-memory elevatedTracker and then
// WROTE that decision onto sessions.scope. After a restart the tracker is
// empty, so isElevated returned false, wantScope became `full`, the persisted
// `elevated` was overwritten with `full`, and the default-deny check passed.
// The session RecoverWithCode opened — scoped by §12.2 to ChangePassword and
// ReenrollTOTP — could then reach every RPC in the system for the rest of its
// 10-minute life, including RegenerateRecoveryCodes.
//
// A second AuthServer over the SAME store is exactly a restart: fresh
// tracker, persisted rows intact.
func TestElevatedSessionStaysElevatedAcrossRestart(t *testing.T) {
	srv, st, _ := newTestServer(t)

	plain, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	recoverCtx, fts := withTransportStream(
		withPeerIP(t.Context(), "10.55.0.1"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(recoverCtx, &affv1.AuthServiceRecoverWithCodeRequest{
		RecoveryCode: plain[0],
	}); err != nil {
		t.Fatalf("recover with code: %v", err)
	}
	tokens := fts.header.Get(rpc.SessionTokenHeader)
	if len(tokens) != 1 {
		t.Fatalf("expected one session token from recovery, got %d", len(tokens))
	}
	elevatedToken := tokens[0]

	// The restart.
	restarted, err := rpc.NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("restarting the server: %v", err)
	}

	// A method OUTSIDE §12.2's elevated allowlist must still be refused.
	ctx := rpc.ContextWithSessionToken(t.Context(), elevatedToken)
	_, err = callAuthed(ctx, restarted, affv1.AuthService_RegenerateRecoveryCodes_FullMethodName,
		&affv1.AuthServiceRegenerateRecoveryCodesRequest{},
		func(ctx context.Context, req any) (any, error) {
			return restarted.RegenerateRecoveryCodes(ctx, req.(*affv1.AuthServiceRegenerateRecoveryCodesRequest))
		})
	if err == nil {
		t.Fatal("a recovery-scoped session reached RegenerateRecoveryCodes after a restart — " +
			"it was silently promoted to full privileges")
	}
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}

	// And the row still says elevated: the restart must not have rewritten it.
	if scope := scopeOf(t, st, elevatedToken); scope != "elevated" {
		t.Errorf("sessions.scope = %q after a restart, want \"elevated\" — the persisted "+
			"scope was overwritten, which is the escalation itself", scope)
	}

	// The other half: the session must still REACH its allowed methods. A fix
	// that locked a recovery session out of ChangePassword would strand the
	// admin mid-recovery, which is the failure §12.2's whole flow exists to
	// avoid — so the interceptor has to admit it, whatever ChangePassword
	// itself then decides about the request body.
	_, err = callAuthed(ctx, restarted, affv1.AuthService_ChangePassword_FullMethodName,
		&affv1.AuthServiceChangePasswordRequest{},
		func(ctx context.Context, req any) (any, error) {
			return restarted.ChangePassword(ctx, req.(*affv1.AuthServiceChangePasswordRequest))
		})
	if status.Code(err) == codes.PermissionDenied {
		t.Error("a recovery-scoped session was refused ChangePassword after a restart — " +
			"it is now locked out of the one thing recovery exists to let it do")
	}
}

func scopeOf(t *testing.T, st *store.Store, token string) string {
	t.Helper()
	var scope string
	if err := st.Writer().QueryRowContext(context.Background(),
		`SELECT scope FROM sessions WHERE token_hash = ?`, auth.HashToken(token)).Scan(&scope); err != nil {
		t.Fatalf("reading session scope: %v", err)
	}
	return scope
}
