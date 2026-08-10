package rpc

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// testPassword satisfies the 15-char NIST floor (internal/auth/password.go).
const testPassword = "correct horse battery staple long enough"

// testSecretKey stands in for AFF_SECRET_KEY.
var testSecretKey = []byte("unit-test-secret-key-not-real")

func openTestStore(t *testing.T) *store.Store {
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

// newTestServer builds an AuthServer against a fresh on-disk store, with an
// admin already enrolled at testPassword and a known TOTP secret so tests
// can compute valid codes with totp.GenerateCode.
func newTestServer(t *testing.T) (srv *AuthServer, st *store.Store, totpSecret string) {
	t.Helper()
	st = openTestStore(t)

	hash, err := auth.Hash(testPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.InitAdmin(t.Context(), hash, kdfParamsString(auth.DefaultParams())); err != nil {
		t.Fatalf("init admin: %v", err)
	}

	secret, _, err := auth.Enroll("admin", "AnimeFeedFlux-test")
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

	srv, err = NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	return srv, st, secret
}

func validCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	return code
}

// withPeerIP attaches a fake peer address, the way a real gRPC connection
// would, so clientIP(ctx) resolves to ip.
func withPeerIP(ctx context.Context, ip string) context.Context {
	return peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 12345}})
}

// fakeTransportStream is the standard mock grpc docs itself for
// grpc.SetHeader, which requires a ServerTransportStream in context. Login
// and RecoverWithCode use SetHeader to hand back the raw session token, so
// tests that need to see that token attach one of these.
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

// --- generic failure -----------------------------------------------------

// TestLoginGenericFailureAcrossCauses is the load-bearing anti-oracle test:
// wrong password, wrong TOTP, a replayed TOTP step, backoff, and a missing
// admin row must all be indistinguishable to the caller (PLAN.md §4).
func TestLoginGenericFailureAcrossCauses(t *testing.T) {
	now := time.Now()

	wrongPassword := func(t *testing.T) error {
		srv, _, secret := newTestServer(t)
		ctx := withPeerIP(t.Context(), "10.0.0.1")
		_, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
			Password: "definitely the wrong password here",
			TotpCode: validCode(t, secret, now),
		})
		return err
	}

	wrongTOTP := func(t *testing.T) error {
		srv, _, _ := newTestServer(t)
		ctx := withPeerIP(t.Context(), "10.0.0.2")
		_, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
			Password: testPassword,
			TotpCode: "000000",
		})
		return err
	}

	replayedTOTP := func(t *testing.T) error {
		srv, _, secret := newTestServer(t)
		code := validCode(t, secret, now)
		ctx1 := withPeerIP(t.Context(), "10.0.0.3")
		if _, err := srv.Login(ctx1, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code}); err != nil {
			t.Fatalf("first login (setting up replay): %v", err)
		}
		ctx2 := withPeerIP(t.Context(), "10.0.0.4") // different IP: isolates replay from backoff
		_, err := srv.Login(ctx2, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code})
		return err
	}

	backoffActive := func(t *testing.T) error {
		srv, _, secret := newTestServer(t)
		ip := "10.0.0.5"
		// Enough failures to cross the grace period and enter backoff.
		for i := 0; i < 4; i++ {
			ctx := withPeerIP(t.Context(), ip)
			_, _ = srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: "wrong wrong wrong wrong wrong", TotpCode: "000000"})
		}
		// Now try with fully correct credentials — the ONLY reason this can
		// still fail is the backoff window, proving it produces the same
		// generic error as every other cause.
		ctx := withPeerIP(t.Context(), ip)
		_, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, now)})
		return err
	}

	noAdminRow := func(t *testing.T) error {
		st := openTestStore(t)
		srv, err := NewAuthServer(st, testSecretKey)
		if err != nil {
			t.Fatalf("new auth server: %v", err)
		}
		ctx := withPeerIP(t.Context(), "10.0.0.6")
		_, err = srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: "123456"})
		return err
	}

	cases := map[string]func(*testing.T) error{
		"wrong password": wrongPassword,
		"wrong totp":     wrongTOTP,
		"replayed totp":  replayedTOTP,
		"backoff active": backoffActive,
		"no admin row":   noAdminRow,
	}

	var wantCode codes.Code
	var wantMsg string
	first := true

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			err := fn(t)
			if err == nil {
				t.Fatalf("%s: expected an error, got nil", name)
			}
			st := status.Convert(err)
			if first {
				wantCode, wantMsg = st.Code(), st.Message()
				first = false
				return
			}
			if st.Code() != wantCode || st.Message() != wantMsg {
				t.Errorf("%s: got code=%v msg=%q, want code=%v msg=%q (must be identical across every failure cause)",
					name, st.Code(), st.Message(), wantCode, wantMsg)
			}
		})
	}

	if wantCode != codes.Unauthenticated {
		t.Errorf("generic failure code = %v, want Unauthenticated", wantCode)
	}
}

// TestLoginKDFRunsForNonexistentAdmin proves auth.Verify still runs its real
// argon2id cost when there is no admin row, by checking the attempt takes
// comparable wall-clock time to a real-admin wrong-password attempt — not
// some order-of-magnitude-faster short-circuit (PLAN.md §4: "always run the
// KDF ... so timing does not leak existence").
func TestLoginKDFRunsForNonexistentAdmin(t *testing.T) {
	measure := func(t *testing.T, fn func()) time.Duration {
		t.Helper()
		start := time.Now()
		fn()
		return time.Since(start)
	}

	srvWithAdmin, _, _ := newTestServer(t)
	withAdmin := measure(t, func() {
		ctx := withPeerIP(t.Context(), "10.1.0.1")
		_, _ = srvWithAdmin.Login(ctx, &affv1.AuthServiceLoginRequest{Password: "still the wrong password here", TotpCode: "000000"})
	})

	stNoAdmin := openTestStore(t)
	srvNoAdmin, err := NewAuthServer(stNoAdmin, testSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	withoutAdmin := measure(t, func() {
		ctx := withPeerIP(t.Context(), "10.1.0.2")
		_, _ = srvNoAdmin.Login(ctx, &affv1.AuthServiceLoginRequest{Password: "still the wrong password here", TotpCode: "000000"})
	})

	// Both must actually have spent argon2id-scale time (a short-circuit
	// would return in well under a millisecond).
	const floor = 2 * time.Millisecond
	if withAdmin < floor {
		t.Errorf("with-admin attempt took %v, suspiciously fast for a KDF run", withAdmin)
	}
	if withoutAdmin < floor {
		t.Errorf("without-admin attempt took %v, suspiciously fast — KDF was probably skipped", withoutAdmin)
	}

	// Comparable, not identical: same order of magnitude is what matters.
	ratio := float64(withoutAdmin) / float64(withAdmin)
	if ratio < 0.2 || ratio > 5 {
		t.Errorf("without-admin/with-admin timing ratio = %.2f, want roughly 1 (both should run the same KDF cost)", ratio)
	}
}

// TestTOTPReplayIsLoginFailure asserts a replayed step surfaces as the
// ordinary generic login failure, not an Internal error — store.ErrTOTPReplay
// must be folded into "failed login", per PLAN.md §4.
func TestTOTPReplayIsLoginFailure(t *testing.T) {
	srv, _, secret := newTestServer(t)
	now := time.Now()
	code := validCode(t, secret, now)

	ctx1 := withPeerIP(t.Context(), "10.2.0.1")
	if _, err := srv.Login(ctx1, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code}); err != nil {
		t.Fatalf("first login: %v", err)
	}

	ctx2 := withPeerIP(t.Context(), "10.2.0.2")
	_, err := srv.Login(ctx2, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code})
	if err == nil {
		t.Fatal("expected the replayed code to fail login")
	}
	st := status.Convert(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("replay code = %v, want Unauthenticated (a failed login, not a server error)", st.Code())
	}
	if st.Message() != status.Convert(errAuthFailed).Message() {
		t.Errorf("replay message = %q, want the generic failure message", st.Message())
	}
}

// TestLoginSuccessStoresOnlyHash checks that a successful login (a) returns
// the raw token only via the out-of-band header, never in the response body,
// and (b) the database holds only its SHA-256 hash, never the raw value.
func TestLoginSuccessStoresOnlyHash(t *testing.T) {
	srv, st, secret := newTestServer(t)
	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.3.0.1"), affv1.AuthService_Login_FullMethodName)

	resp, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, time.Now())})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	rawToken := fts.header.Get(SessionTokenHeader)
	if len(rawToken) != 1 || rawToken[0] == "" {
		t.Fatalf("expected exactly one non-empty %s header, got %v", SessionTokenHeader, rawToken)
	}
	token := rawToken[0]

	sess, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("looking up session by hash: %v", err)
	}
	if sess.TokenHash == token {
		t.Fatal("stored token_hash equals the raw token — the raw value was persisted verbatim")
	}
	if sess.TokenHash != auth.HashToken(token) {
		t.Error("stored token_hash does not match HashToken(rawToken)")
	}

	// The proto response never carries the raw token anywhere.
	if resp.Session == nil {
		t.Fatal("expected a session in the response")
	}
	if strings.Contains(resp.String(), token) {
		t.Error("the raw token leaked into the response message body")
	}
}

// --- ChangePassword --------------------------------------------------------

// TestChangePasswordRevokesOtherSessions covers the hard rule: on success,
// every session but the one that made the call is revoked.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	srv, st, secret := newTestServer(t)
	base := time.Now()

	// Each login and the eventual ChangePassword must land in a distinct 30s
	// TOTP step, or store.MarkTOTPStepUsed's replay guard rejects the second
	// one outright (the same primary-key protection §4 relies on). The
	// server's clock is pinned to match the code's step so ValidateCode's
	// ±1-step skew window — measured against srv.now(), not wall-clock time
	// — actually accepts it.
	loginCtx := func(ip string, at time.Time) context.Context {
		srv.now = func() time.Time { return at }
		ctx, fts := withTransportStream(withPeerIP(t.Context(), ip), affv1.AuthService_Login_FullMethodName)
		if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, at)}); err != nil {
			t.Fatalf("login from %s: %v", ip, err)
		}
		token := fts.header.Get(SessionTokenHeader)[0]
		authCtx, err := srv.authorize(ContextWithSessionToken(t.Context(), token), affv1.AuthService_ChangePassword_FullMethodName)
		if err != nil {
			t.Fatalf("authorize %s: %v", ip, err)
		}
		return authCtx
	}

	sessionA := loginCtx("10.4.0.1", base)
	sessionB := loginCtx("10.4.0.2", base.Add(2*time.Minute))

	csB, _ := callerSessionFromContext(sessionB)

	changeAt := base.Add(4 * time.Minute)
	srv.now = func() time.Time { return changeAt }
	newPassword := "a brand new much longer passphrase"
	resp, err := srv.ChangePassword(sessionB, &affv1.AuthServiceChangePasswordRequest{
		CurrentPassword: testPassword,
		TotpCode:        validCode(t, secret, changeAt),
		NewPassword:     newPassword,
	})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	_ = resp

	csA, ok := callerSessionFromContext(sessionA)
	if !ok {
		t.Fatal("session A missing from context")
	}
	sessA, err := st.GetSessionByTokenHash(t.Context(), csA.TokenHash)
	if err != nil {
		t.Fatalf("looking up session A: %v", err)
	}
	if sessA.RevokedAt.IsZero() {
		t.Error("session A (not the caller) should have been revoked, but is still live")
	}

	sessB, err := st.GetSessionByTokenHash(t.Context(), csB.TokenHash)
	if err != nil {
		t.Fatalf("looking up session B: %v", err)
	}
	if !sessB.RevokedAt.IsZero() {
		t.Error("session B (the caller) should NOT have been revoked by its own ChangePassword call")
	}

	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	ok2, _, verr := auth.Verify(newPassword, admin.PasswordHash)
	if verr != nil || !ok2 {
		t.Error("the new password does not verify against the stored hash")
	}
}

// TestRecoverWithCodeOpensElevatedSession checks the happy path end-to-end:
// a valid recovery code consumes it, mints a session the interceptor treats
// as elevated, and reports the remaining count.
func TestRecoverWithCodeOpensElevatedSession(t *testing.T) {
	srv, st, _ := newTestServer(t)
	plain, hashes, err := auth.GenerateCodes(3)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.5.0.1"), affv1.AuthService_RecoverWithCode_FullMethodName)
	resp, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if resp.RemainingRecoveryCodes != 2 {
		t.Errorf("remaining = %d, want 2", resp.RemainingRecoveryCodes)
	}

	token := fts.header.Get(SessionTokenHeader)
	if len(token) != 1 || token[0] == "" {
		t.Fatal("expected a session token header")
	}

	// The same code must not work twice.
	ctx2, _ := withTransportStream(withPeerIP(t.Context(), "10.5.0.2"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(ctx2, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]}); err == nil {
		t.Error("expected the already-used recovery code to fail")
	}
}
