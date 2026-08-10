package sectest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// SEC-43: a session past its 60-minute idle timeout must be refused even
// though it is nowhere near its 12h absolute lifetime — the inverse of
// SEC-42's scenario, proving the two kill switches are actually independent
// rather than one silently subsuming the other.
func TestIdleTimeout_WithinAbsoluteLifetimeStillRejected(t *testing.T) {
	srv, st, _ := newTestServer(t)

	rawToken, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	now := time.Now()
	sess := auth.Session{
		TokenHash:  hash,
		CreatedAt:  now.Add(-2 * time.Hour),
		LastSeenAt: now.Add(-(auth.SessionIdleTimeout + time.Minute)), // idle-timed-out
		ExpiresAt:  now.Add(10 * time.Hour),                           // comfortably inside the 12h absolute lifetime
	}
	if _, err := st.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	ctx := rpc.ContextWithSessionToken(t.Context(), rawToken)
	_, err = callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
		&affv1.AuthServiceSessionRequest{},
		func(ctx context.Context, req any) (any, error) {
			return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
		})
	if err == nil {
		t.Fatal("an idle-timed-out session was accepted because it was inside the absolute lifetime")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestIdleTimeout_JustInsideWindowStillAccepted is the boundary check: a
// session touched a moment before the idle timeout must still work, so the
// test above is proven to be exercising the idle clock and not some
// unrelated rejection (e.g. a bug that rejects every session).
func TestIdleTimeout_JustInsideWindowStillAccepted(t *testing.T) {
	srv, st, _ := newTestServer(t)

	rawToken, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	now := time.Now()
	sess := auth.Session{
		TokenHash:  hash,
		CreatedAt:  now.Add(-2 * time.Hour),
		LastSeenAt: now.Add(-(auth.SessionIdleTimeout - 5*time.Second)),
		ExpiresAt:  now.Add(10 * time.Hour),
	}
	if _, err := st.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	ctx := rpc.ContextWithSessionToken(t.Context(), rawToken)
	_, err = callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
		&affv1.AuthServiceSessionRequest{},
		func(ctx context.Context, req any) (any, error) {
			return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
		})
	if err != nil {
		t.Errorf("a session inside its idle window was rejected: %v", err)
	}
}
