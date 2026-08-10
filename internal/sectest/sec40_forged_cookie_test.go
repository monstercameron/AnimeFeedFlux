package sectest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// SEC-40: a forged cookie whose token has the exact right shape (256 bits,
// base64.RawURLEncoding, same length as a real one) but was never minted by
// NewToken must be refused, the same as any other invalid session. This is
// the attacker's most plausible forgery attempt — not random garbage (that's
// SEC-47's job) but a token that would pass any naive "looks like a session
// token" sanity check.
func TestForgedCookie_PlausibleTokenRefused(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// auth.NewToken is the exact production constructor — this forged token
	// is indistinguishable in SHAPE from a real one, and simply was never
	// persisted as a session's token_hash.
	forged, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generating a plausible token: %v", err)
	}

	ctx := rpc.ContextWithSessionToken(t.Context(), forged)
	_, err = callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
		&affv1.AuthServiceSessionRequest{},
		func(ctx context.Context, req any) (any, error) {
			return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
		})
	if err == nil {
		t.Fatal("forged cookie with a plausible token was accepted")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}
