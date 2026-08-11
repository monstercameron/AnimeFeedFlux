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

// SEC-20: prove the STORED value (SHA-256 of the session token, the only
// thing the `sessions` table ever holds — PLAN.md §4, SEC-18) cannot be
// replayed as a cookie. If presenting that stored hash as the cookie value
// authenticated, a database read alone would be a login, and every property
// of hashing the token before storing it (SEC-18/SEC-19) would be void.
//
// This drives the real production path: rpc.AuthServer.UnaryInterceptor via
// callAuthed, exactly like a live gRPC call, not a hand-rolled shortcut.
func TestSessionHash_StoredValueCannotBeReplayedAsCookie(t *testing.T) {
	srv, st, secret := newTestServer(t)
	raw := login(t, srv, secret, "203.0.113.5", time.Now())

	storedHash := auth.HashToken(raw)

	// Sanity check: storedHash is genuinely what a stolen `sessions` table
	// row contains for this session — not a value this test invented.
	if _, err := st.GetSessionByTokenHash(t.Context(), storedHash); err != nil {
		t.Fatalf("sanity: no session found under its own stored hash: %v", err)
	}

	callSession := func(ctx context.Context) (any, error) {
		return callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
			&affv1.AuthServiceSessionRequest{},
			func(ctx context.Context, req any) (any, error) {
				return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
			})
	}

	// --- The attack: present the DB-stored hash itself as the cookie value. ---
	//
	// A reader of `sessions.token_hash` (backup, SQL injection, careless
	// export — SEC-18's whole threat model) tries pasting that value straight
	// in as __Host-aff_session. The server hashes whatever it receives
	// (SHA-256) before comparing against the stored column, so replaying the
	// already-hashed value must produce a second, different hash that never
	// matches any row.
	forgedCtx := rpc.ContextWithSessionToken(t.Context(), storedHash)
	if _, err := callSession(forgedCtx); err == nil {
		t.Fatal("the stored session-token hash was accepted as a cookie value — a DB read is a login")
	} else if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}

	// --- The complement: the real raw token still authenticates. ---
	//
	// Without this half, a server that rejects every cookie (broken
	// entirely) would pass the assertion above for the wrong reason.
	realCtx := rpc.ContextWithSessionToken(t.Context(), raw)
	if _, err := callSession(realCtx); err != nil {
		t.Fatalf("the real raw session token was rejected: %v", err)
	}
}
