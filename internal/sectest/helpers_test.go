// Package sectest is the adversarial security suite for PLAN.md §4 (TODOS.md
// SEC-39..SEC-50). Every test here is written the way an attacker would probe
// the system, not as a happy path with a negative assertion bolted on: forged
// tokens, raced database writes, fuzzed parsers, and log output scraped for
// secrets.
//
// This package deliberately only reaches the PUBLIC surface of internal/auth,
// internal/store, internal/rpc and internal/bridge — the same surface a real
// attacker (or a real caller wired through the actual gRPC/bridge stack) is
// limited to. Where a test needs to fabricate state that only a compromised
// or racing legitimate client could produce (a backdated session row, a
// forged-but-well-shaped cookie), it does so through the same store/auth
// constructors production code uses, never by reaching into unexported
// fields.
package sectest

import (
	"context"
	"net"
	"path/filepath"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// testPassword clears the 15-char NIST floor (internal/auth/password.go).
const testPassword = "correct horse battery staple long enough"

// testSecretKey stands in for AFF_SECRET_KEY.
var testSecretKey = []byte("sectest-secret-key-not-a-real-secret")

// tHelper is the slice of *testing.T / *testing.F / *testing.B that setup
// helpers need. *testing.F deliberately does NOT implement testing.TB (to
// stop seed-corpus code from being misused inside the fuzz body), but it has
// every method used below, so this narrower interface lets openTestStore /
// newTestServer / login / validCode double as both ordinary test setup and
// fuzz-target one-time setup (SEC-47, SEC-48) without duplicating either.
type tHelper interface {
	Helper()
	Fatalf(format string, args ...any)
	TempDir() string
	Cleanup(func())
	Context() context.Context
}

// openTestStore builds a fresh on-disk SQLite database with migrations
// applied — one per test, so concurrency tests (SEC-44, SEC-45) never share
// state across t.Run subtests by accident.
func openTestStore(t tHelper) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// newTestServer builds an *rpc.AuthServer against a fresh store with an admin
// already enrolled at testPassword and a known TOTP secret, so tests can
// compute valid codes with totp.GenerateCode the same way a legitimate
// authenticator app would.
// opts pass through to NewAuthServer — used by exactly one test (SEC-46) to
// disable TOTP replay rejection; see that test for why. Every other test
// runs the production configuration.
func newTestServer(t tHelper, opts ...rpc.AuthServerOption) (srv *rpc.AuthServer, st *store.Store, totpSecret string) {
	t.Helper()
	st = openTestStore(t)

	hash, err := auth.Hash(testPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// kdf_params is operator-facing record-keeping only (auth.Verify/Hash
	// re-derive params from the PHC string itself), so any placeholder text
	// is fine here.
	if err := st.InitAdmin(t.Context(), hash, "argon2id m=65536,t=3,p=1"); err != nil {
		t.Fatalf("init admin: %v", err)
	}

	secret, _, err := auth.Enroll("admin", "AnimeFeedFlux-sectest")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	enc, err := auth.EncryptSecret(secret, testSecretKey)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if err := st.SetTOTPSecret(t.Context(), enc); err != nil {
		t.Fatalf("set totp secret: %v", err)
	}

	srv, err = rpc.NewAuthServer(st, testSecretKey, opts...)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	return srv, st, secret
}

func validCode(t tHelper, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	return code
}

// withPeerIP attaches a fake peer address the way a real gRPC connection
// would, so the server's per-IP backoff/audit log keys resolve to ip.
func withPeerIP(ctx context.Context, ip string) context.Context {
	return peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 12345}})
}

// fakeTransportStream is the standard shape grpc.SetHeader needs in context
// (a grpc.ServerTransportStream); Login/RecoverWithCode use SetHeader to hand
// back the raw session token out-of-band (PLAN.md §4: never in the body), so
// any test that needs to see the minted token attaches one of these.
type fakeTransportStream struct {
	method string
	header metadata.MD
}

func (f *fakeTransportStream) Method() string { return f.method }
func (f *fakeTransportStream) SetHeader(md metadata.MD) error {
	f.header = metadata.Join(f.header, md)
	return nil
}
func (f *fakeTransportStream) SendHeader(md metadata.MD) error { return f.SetHeader(md) }
func (f *fakeTransportStream) SetTrailer(metadata.MD) error    { return nil }

func withTransportStream(ctx context.Context, method string) (context.Context, *fakeTransportStream) {
	fts := &fakeTransportStream{method: method}
	return grpc.NewContextWithServerTransportStream(ctx, fts), fts
}

// login performs a real Login call and returns the raw session token minted
// for it — the same token a legitimate browser would receive as the
// __Host-aff_session cookie value.
func login(t tHelper, srv *rpc.AuthServer, secret, ip string, at time.Time) string {
	t.Helper()
	ctx, fts := withTransportStream(withPeerIP(t.Context(), ip), affv1.AuthService_Login_FullMethodName)
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, at)}); err != nil {
		t.Fatalf("login: %v", err)
	}
	tok := fts.header.Get(rpc.SessionTokenHeader)
	if len(tok) != 1 || tok[0] == "" {
		t.Fatalf("expected exactly one session token header, got %v", tok)
	}
	return tok[0]
}

// callAuthed drives a call through the real session-enforcement path
// (rpc.AuthServer.UnaryInterceptor) rather than invoking the RPC method
// directly, because every session-guarded handler reads its callerSession out
// of a context key only authorize (unexported, invoked by the interceptor)
// populates. This is also exactly how a real gRPC server dispatches a call,
// so a test using it is exercising the production enforcement path, not a
// shortcut around it.
func callAuthed(ctx context.Context, srv *rpc.AuthServer, method string, req any, fn func(ctx context.Context, req any) (any, error)) (any, error) {
	info := &grpc.UnaryServerInfo{FullMethod: method}
	return srv.UnaryInterceptor()(ctx, req, info, fn)
}
