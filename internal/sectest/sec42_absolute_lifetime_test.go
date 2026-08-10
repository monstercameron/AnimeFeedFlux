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

// SEC-42: a session past its absolute lifetime must be refused even if it
// was JUST used — "recently active" must not override the hard 12h ceiling
// (PLAN.md §4). This is the attacker scenario where a stolen, still-being-
// actively-replayed token should nonetheless stop working once the absolute
// clock runs out, regardless of how "alive" the session looks by activity.
func TestAbsoluteLifetime_RecentlyActiveSessionStillExpires(t *testing.T) {
	srv, st, _ := newTestServer(t)

	rawToken, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	now := time.Now()
	// ExpiresAt is one second in the past; LastSeenAt is one second in the
	// FUTURE relative to that, i.e. more recent than the expiry itself —
	// this is the exact "recently active" case §4 calls out. A buggy
	// implementation that only checks idle timeout, or that treats a fresh
	// LastSeenAt as proof of validity, would wrongly accept this.
	sess := auth.Session{
		TokenHash:  hash,
		CreatedAt:  now.Add(-13 * time.Hour),
		LastSeenAt: now.Add(-1 * time.Second), // "just used"
		ExpiresAt:  now.Add(-2 * time.Second), // but already past absolute lifetime
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
		t.Fatal("a session past its absolute lifetime was accepted because it was recently active")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}
