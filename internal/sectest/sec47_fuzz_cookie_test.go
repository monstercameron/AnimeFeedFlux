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

// SEC-47: arbitrary bytes presented as a session cookie/token must never
// panic the server and must never authenticate. This drives the ACTUAL
// production pipeline a real cookie value travels through: rpc.
// ContextWithSessionToken -> AuthServer.UnaryInterceptor -> auth.HashToken ->
// store.GetSessionByTokenHash — not just the low-level hashing function in
// isolation, so a fuzz failure here means something in that real chain
// panics or (astronomically unlikely, but checked anyway) matches a real
// session.
func FuzzCookieParser_NeverPanicsNeverAuthenticates(f *testing.F) {
	// *testing.F satisfies tHelper (Helper/Fatalf/TempDir/Cleanup/Context),
	// so the same setup helpers ordinary tests use work here directly — no
	// adapter needed.
	srv, _, secret := newTestServer(f)
	// A genuine, currently-valid session exists in the store the whole time
	// this fuzz target runs, so "never authenticate" is a meaningful claim —
	// there is something real to (fail to) collide with.
	_ = login(f, srv, secret, "10.40.0.1", time.Now())

	seeds := []string{
		"",
		"a",
		"not-a-real-token",
		string([]byte{0x00, 0x00, 0x00}),
		"'; DROP TABLE sessions; --",
		"../../../etc/passwd",
		"%00%00%00",
		string(make([]byte, 10000)),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		ctx := rpc.ContextWithSessionToken(t.Context(), raw)
		_, err := callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
			&affv1.AuthServiceSessionRequest{},
			func(ctx context.Context, req any) (any, error) {
				return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
			})
		// The empty string is treated as "no token attached" by
		// ContextWithSessionToken/sessionTokenFromContext, which is a
		// distinct, documented case (also Unauthenticated) — not exercised
		// specially here, just noted so a reader doesn't wonder why an empty
		// fuzz input isn't special-cased.
		if err == nil {
			t.Fatalf("arbitrary token %q was accepted as a valid session", raw)
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("arbitrary token %q produced code %v, want Unauthenticated", raw, status.Code(err))
		}
	})
}

// FuzzHashToken_NeverPanics targets auth.HashToken directly at the byte
// level (arbitrary, possibly invalid-UTF-8 strings), independent of the RPC
// plumbing above.
func FuzzHashToken_NeverPanics(f *testing.F) {
	f.Add("")
	f.Add(string([]byte{0xff, 0xfe, 0x00, 0xd8}))
	f.Fuzz(func(t *testing.T, raw string) {
		_ = auth.HashToken(raw)
	})
}
