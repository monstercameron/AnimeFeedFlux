package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
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

// --- Pepper (SEC-07/08/10) --------------------------------------------------

// testPepperKey/testPepperVersion/otherPepperKey stand in for AFF_PASSWORD_PEPPER
// / AFF_PASSWORD_PEPPER_VERSION across the pepper tests below.
var (
	testPepperKey     = []byte("unit-test-pepper-not-a-real-secret-key")
	testPepperVersion = 1
	otherPepperKey    = []byte("a-totally-different-pepper-key-value")
)

// newTestServerWithPepper is newTestServer's pepper-configured sibling: same
// admin (hashed and stored UNPEPPERED, exactly like newTestServer, via
// InitAdmin — there is no pepper-aware InitAdmin, deliberately: cmd/aff's
// break-glass path is out of scope for this change), but the returned
// AuthServer has a pepper configured via WithPasswordPepper. This is what
// exercises the "existing unpeppered row, pepper newly configured" upgrade
// path (needsRepepper in Login).
func newTestServerWithPepper(t *testing.T, key []byte, version int) (srv *AuthServer, st *store.Store, totpSecret string) {
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

	srv, err = NewAuthServer(st, testSecretKey, WithPasswordPepper(key, version))
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	return srv, st, secret
}

// TestNoPepperConfiguredVerifiesExactlyAsBefore is the compatibility case
// PLAN.md §4 calls out as mattering most: a deployment with no pepper
// configured must behave identically to one that never had this feature —
// newTestServer already builds exactly that server, so this simply confirms
// login succeeds and the stored row's pepper_version stays 0.
func TestNoPepperConfiguredVerifiesExactlyAsBefore(t *testing.T) {
	srv, st, secret := newTestServer(t)
	ctx := withPeerIP(t.Context(), "10.6.0.1")
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	}); err != nil {
		t.Fatalf("login with no pepper configured: %v", err)
	}
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if admin.PepperVersion != 0 {
		t.Errorf("PepperVersion = %d, want 0 (never peppered)", admin.PepperVersion)
	}
}

// TestPepperUpgradesUnpepperedRowOnLogin: an admin row created before a
// pepper existed (pepper_version 0) must still log in once a pepper is
// configured — the wrong-direction failure PLAN.md §4 explicitly forbids
// ("silently breaking login... is far worse than the threat being
// mitigated") — and the successful login should transparently upgrade the
// row to the now-configured pepper generation (mirroring needsRehash's cost
// migration), so the next login onward is actually peppered.
func TestPepperUpgradesUnpepperedRowOnLogin(t *testing.T) {
	srv, st, secret := newTestServerWithPepper(t, testPepperKey, testPepperVersion)

	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if admin.PepperVersion != 0 {
		t.Fatalf("test setup: PepperVersion = %d, want 0 before the first login", admin.PepperVersion)
	}

	ctx := withPeerIP(t.Context(), "10.6.0.2")
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	}); err != nil {
		t.Fatalf("login against an unpeppered row with a pepper configured: %v", err)
	}

	admin, err = st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin after login: %v", err)
	}
	if admin.PepperVersion != testPepperVersion {
		t.Errorf("PepperVersion after login = %d, want %d (upgraded)", admin.PepperVersion, testPepperVersion)
	}

	// And now that the row IS peppered, a second login (fresh TOTP step)
	// must still succeed against the now-peppered hash.
	ctx2 := withPeerIP(t.Context(), "10.6.0.3")
	if _, err := srv.Login(ctx2, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now().Add(30*time.Second)),
	}); err != nil {
		t.Fatalf("second login against the now-peppered row: %v", err)
	}
}

// TestChangePasswordAppliesConfiguredPepper: ChangePassword with a pepper
// configured must persist BOTH the peppered hash and its pepper_version —
// SEC-07's "wire it into the hash... path" — and the new password must then
// verify on login.
func TestChangePasswordAppliesConfiguredPepper(t *testing.T) {
	srv, st, secret := newTestServerWithPepper(t, testPepperKey, testPepperVersion)
	now := time.Now()
	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.6.0.4"), affv1.AuthService_Login_FullMethodName)
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, now)}); err != nil {
		t.Fatalf("login: %v", err)
	}
	token := fts.header.Get(SessionTokenHeader)[0]
	authCtx, err := srv.authorize(ContextWithSessionToken(t.Context(), token), affv1.AuthService_ChangePassword_FullMethodName)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	newPassword := "a brand new, sufficiently long passphrase indeed"
	changeAt := now.Add(2 * time.Minute)
	srv.now = func() time.Time { return changeAt }
	if _, err := srv.ChangePassword(authCtx, &affv1.AuthServiceChangePasswordRequest{
		CurrentPassword: testPassword,
		TotpCode:        validCode(t, secret, changeAt),
		NewPassword:     newPassword,
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if admin.PepperVersion != testPepperVersion {
		t.Errorf("PepperVersion after ChangePassword = %d, want %d", admin.PepperVersion, testPepperVersion)
	}
	// The stored hash must NOT verify against the plain (unpeppered) new
	// password directly — proof the pepper was actually mixed in before
	// hashing, not merely recorded in the version column.
	if ok, _, _ := auth.Verify(newPassword, admin.PasswordHash); ok {
		t.Error("new password verifies unpeppered against the stored hash — pepper was not actually applied")
	}
	if ok, _, _ := auth.VerifyPasswordPeppered(newPassword, admin.PasswordHash, testPepperKey); !ok {
		t.Error("new password does not verify once peppered the same way ChangePassword should have peppered it")
	}

	loginCtx := withPeerIP(t.Context(), "10.6.0.5")
	if _, err := srv.Login(loginCtx, &affv1.AuthServiceLoginRequest{
		Password: newPassword,
		TotpCode: validCode(t, secret, changeAt.Add(30*time.Second)),
	}); err != nil {
		t.Fatalf("login with the new peppered password: %v", err)
	}
}

// TestWrongPepperFailsLogin: a row peppered under one key must NOT verify
// against a server configured with a different pepper key (same version
// number, different secret) — SEC-10's "does not permit verification"
// property, exercised end to end through Login rather than only at the
// auth.Pepper primitive level.
func TestWrongPepperFailsLogin(t *testing.T) {
	srv, st, secret := newTestServerWithPepper(t, testPepperKey, testPepperVersion)

	// Upgrade the row to peppered (see TestPepperUpgradesUnpepperedRowOnLogin).
	ctx := withPeerIP(t.Context(), "10.6.0.6")
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, time.Now())}); err != nil {
		t.Fatalf("upgrading login: %v", err)
	}
	admin, err := st.GetAdmin(t.Context())
	if err != nil || admin.PepperVersion == 0 {
		t.Fatalf("test setup: row was not peppered (err=%v, version=%d)", err, admin.PepperVersion)
	}

	// Swap in a server configured with a different pepper KEY but the SAME
	// version number — the case a version number alone cannot catch.
	wrongSrv, err := NewAuthServer(st, testSecretKey, WithPasswordPepper(otherPepperKey, testPepperVersion))
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	ctx2 := withPeerIP(t.Context(), "10.6.0.7")
	_, err = wrongSrv.Login(ctx2, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now().Add(30*time.Second)),
	})
	if err == nil {
		t.Fatal("login succeeded against a row peppered under a different key")
	}
	st2 := status.Convert(err)
	if st2.Message() != "authentication failed" {
		t.Errorf("wrong-pepper failure message = %q, want the generic login failure", st2.Message())
	}
}

// --- Password reset tokens (SEC-31/33/34) -----------------------------------

// TestPasswordResetRoundTrip covers the happy path end to end: a raw token
// is issued, used once to set a new password, and that new password then
// verifies on login.
func TestPasswordResetRoundTrip(t *testing.T) {
	srv, st, secret := newTestServer(t)

	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}
	if raw == "" {
		t.Fatal("issued an empty raw token")
	}

	newPassword := "a completely different long passphrase here"
	if err := srv.CompletePasswordReset(t.Context(), raw, newPassword); err != nil {
		t.Fatalf("complete password reset: %v", err)
	}

	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if ok, _, _ := auth.Verify(newPassword, admin.PasswordHash); !ok {
		t.Error("new password does not verify against the stored hash after reset")
	}

	ctx := withPeerIP(t.Context(), "10.7.0.1")
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: newPassword,
		TotpCode: validCode(t, secret, time.Now()),
	}); err != nil {
		t.Fatalf("login with the reset password: %v", err)
	}
}

// TestPasswordResetIsSingleUse: a used token must be refused on a second
// attempt, with the SAME generic failure as an invalid token (SEC-34,
// PLAN.md §12.1's anti-oracle rule).
func TestPasswordResetIsSingleUse(t *testing.T) {
	srv, _, _ := newTestServer(t)

	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}
	if err := srv.CompletePasswordReset(t.Context(), raw, "first new long enough passphrase"); err != nil {
		t.Fatalf("first use: %v", err)
	}

	err = srv.CompletePasswordReset(t.Context(), raw, "second new long enough passphrase")
	if err == nil {
		t.Fatal("a used reset token was accepted a second time")
	}
	st2 := status.Convert(err)
	if st2.Code() != codes.Unauthenticated || st2.Message() != "authentication failed" {
		t.Errorf("used-token failure = %v, want the generic authentication-failed error", err)
	}
}

// TestPasswordResetSingleUseUnderConcurrency is the load-bearing test SEC-34
// demands: two goroutines racing CompletePasswordReset with the identical
// raw token must have EXACTLY ONE succeed, and the database — not a
// check-then-act race in this Go code — must be what decided it. Proven by:
// exactly one of the two errors is nil, and the admin's final password_hash
// matches exactly one of the two candidate new passwords (never a hash from
// neither, and never both somehow "winning").
func TestPasswordResetSingleUseUnderConcurrency(t *testing.T) {
	srv, st, _ := newTestServer(t)

	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}

	const n = 8
	passwords := make([]string, n)
	for i := range passwords {
		passwords[i] = fmt.Sprintf("racer number %d long enough passphrase indeed", i)
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			errs[i] = srv.CompletePasswordReset(t.Context(), raw, passwords[i])
		}(i)
	}
	wg.Wait()

	successes := 0
	winner := -1
	for i, e := range errs {
		if e == nil {
			successes++
			winner = i
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful concurrent resets of the same token, want exactly 1 (errs=%v)", successes, errs)
	}

	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	ok, _, verr := auth.Verify(passwords[winner], admin.PasswordHash)
	if verr != nil || !ok {
		t.Fatalf("stored password hash does not match the one goroutine that won the race (winner=%d)", winner)
	}
	for i, pw := range passwords {
		if i == winner {
			continue
		}
		if ok, _, _ := auth.Verify(pw, admin.PasswordHash); ok {
			t.Fatalf("a losing goroutine's password (%d) also verifies against the stored hash", i)
		}
	}
}

// TestPasswordResetExpiredTokenRefused: a token past its TTL must be
// refused with the same generic message a never-issued token gets — not a
// distinguishable "expired" error, which would tell an attacker a token
// existed at all (PLAN.md §4).
func TestPasswordResetExpiredTokenRefused(t *testing.T) {
	srv, _, _ := newTestServer(t)

	issueAt := time.Now()
	srv.now = func() time.Time { return issueAt }
	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}

	// Past the 15-minute TTL internal/auth/reset.go's NewResetToken uses.
	srv.now = func() time.Time { return issueAt.Add(16 * time.Minute) }
	err = srv.CompletePasswordReset(t.Context(), raw, "a long enough new passphrase here")
	if err == nil {
		t.Fatal("an expired reset token was accepted")
	}
	st2 := status.Convert(err)
	if st2.Code() != codes.Unauthenticated || st2.Message() != "authentication failed" {
		t.Errorf("expired-token failure = %v, want the generic authentication-failed error", err)
	}
}

// TestPasswordResetUnknownTokenRefused: a syntactically valid but never-
// issued token gets the same generic failure as every other cause.
func TestPasswordResetUnknownTokenRefused(t *testing.T) {
	srv, _, _ := newTestServer(t)
	err := srv.CompletePasswordReset(t.Context(), "not-a-real-issued-token-at-all", "a long enough new passphrase here")
	if err == nil {
		t.Fatal("an unrecognized reset token was accepted")
	}
	st2 := status.Convert(err)
	if st2.Code() != codes.Unauthenticated || st2.Message() != "authentication failed" {
		t.Errorf("unknown-token failure = %v, want the generic authentication-failed error", err)
	}
}

// TestPasswordResetRevokesAllSessionsIncludingPreExisting: SEC-33's hard
// rule. A session opened BEFORE the reset — under the old, possibly
// compromised password — must be revoked by the reset, not merely sessions
// opened after.
func TestPasswordResetRevokesAllSessionsIncludingPreExisting(t *testing.T) {
	srv, st, secret := newTestServer(t)
	now := time.Now()
	srv.now = func() time.Time { return now }

	// A session opened under the OLD password, before any reset happens —
	// this is the attacker's seat SEC-33 exists to close.
	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.7.0.2"), affv1.AuthService_Login_FullMethodName)
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, now)}); err != nil {
		t.Fatalf("pre-reset login: %v", err)
	}
	preResetToken := fts.header.Get(SessionTokenHeader)[0]
	preResetHash := auth.HashToken(preResetToken)

	sessBefore, err := st.GetSessionByTokenHash(t.Context(), preResetHash)
	if err != nil {
		t.Fatalf("looking up pre-reset session: %v", err)
	}
	if !sessBefore.RevokedAt.IsZero() {
		t.Fatal("test setup: pre-reset session is already revoked")
	}

	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}
	if err := srv.CompletePasswordReset(t.Context(), raw, "yet another sufficiently long new passphrase"); err != nil {
		t.Fatalf("complete password reset: %v", err)
	}

	sessAfter, err := st.GetSessionByTokenHash(t.Context(), preResetHash)
	if err != nil {
		t.Fatalf("looking up pre-reset session after reset: %v", err)
	}
	if sessAfter.RevokedAt.IsZero() {
		t.Fatal("a session opened BEFORE the reset survived it — SEC-33 requires it revoked")
	}
}

// TestPasswordResetTokenNeverLogged: the raw token must never appear in any
// auth_events row this whole flow writes — the same "no secret in any log"
// property internal/sectest/sec50_no_secret_in_logs_test.go enforces for
// session tokens.
func TestPasswordResetTokenNeverLogged(t *testing.T) {
	srv, st, _ := newTestServer(t)

	raw, err := srv.IssuePasswordResetToken(t.Context())
	if err != nil {
		t.Fatalf("issue reset token: %v", err)
	}
	if err := srv.CompletePasswordReset(t.Context(), raw, "one more sufficiently long new passphrase"); err != nil {
		t.Fatalf("complete password reset: %v", err)
	}
	// Also exercise the used-token failure path, which logs too.
	_ = srv.CompletePasswordReset(t.Context(), raw, "reused-attempt long enough passphrase")

	events, err := st.ListAuthEvents(t.Context(), 0)
	if err != nil {
		t.Fatalf("list auth events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one auth event from issuing/completing the reset")
	}
	for _, e := range events {
		if strings.Contains(e.Detail, raw) {
			t.Fatalf("raw reset token leaked into auth_events.detail: %q", e.Detail)
		}
		if strings.Contains(e.Kind, raw) {
			t.Fatalf("raw reset token leaked into auth_events.kind: %q", e.Kind)
		}
	}
}

// TestPasswordResetNotOnGRPCSurface is the structural half of the
// reachability decision documented on IssuePasswordResetToken/
// CompletePasswordReset above: neither is a proto RPC, so no network caller
// — authenticated or not — can reach them through the bridge at all. The
// only caller is cmd/aff/admin_cmd.go's `aff admin reset-password`, which
// requires direct filesystem access to the SQLite file (the same local-only
// authorization tier as `aff admin init`/`aff admin reset`). This asserts
// that fact against the generated grpc.ServiceDesc rather than trusting a
// comment to stay true across future proto edits: if a later change adds
// these to auth.proto, this test fails and forces the reachability decision
// to be revisited deliberately instead of silently.
func TestPasswordResetNotOnGRPCSurface(t *testing.T) {
	for _, m := range affv1.AuthService_ServiceDesc.Methods {
		if m.MethodName == "IssuePasswordResetToken" || m.MethodName == "CompletePasswordReset" {
			t.Fatalf("%q is registered as a gRPC method on AuthService — password reset issuance/completion "+
				"must stay local-only (CLI, direct DB access), never reachable over the bridge "+
				"by any caller, authenticated or not", m.MethodName)
		}
	}
}

// --- re-proof backoff -------------------------------------------------------
//
// verifyCurrentCredentials is the gate behind ChangePassword,
// RegenerateRecoveryCodes, ReenrollTOTP's non-elevated path and
// SystemServer.Backup. It exists because §4 says a live session token must
// not be sufficient on its own for those actions — but it counted no failures
// and consulted no backoff, so a caller holding a stolen session could
// brute-force the password and the TOTP code against it at full speed. What
// that buys an attacker is not reach (they already had the session) but the
// PASSWORD, which survives session revocation.

func TestVerifyCurrentCredentials_RecordsFailuresAgainstTheBackoff(t *testing.T) {
	srv, _, secret := newTestServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.9")
	now := time.Now()

	// The tracker forgives the first two failures (a real operator mistyping),
	// so drive past the grace window and then assert the gate actually closes.
	for i := range 4 {
		err := srv.verifyCurrentCredentials(ctx, "not-the-password", validCode(t, secret, now), now)
		if !errors.Is(err, errCredentialCheckFailed) {
			t.Fatalf("attempt %d: err = %v, want errCredentialCheckFailed", i, err)
		}
	}

	if !srv.backoff.blocked("203.0.113.9", now) {
		t.Fatal("repeated wrong-password re-proofs did not trip the backoff")
	}
}

func TestVerifyCurrentCredentials_BlockedIPIsRefusedEvenWithTheRightPassword(t *testing.T) {
	srv, _, secret := newTestServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.10")
	now := time.Now()

	for range 4 {
		_ = srv.verifyCurrentCredentials(ctx, "not-the-password", validCode(t, secret, now), now)
	}

	// Correct credentials, still refused: being inside the window is what the
	// window is for. And it is reported as an ordinary credential failure —
	// "you are being throttled" is exactly the distinguishable signal §4's
	// one-generic-message rule withholds.
	err := srv.verifyCurrentCredentials(ctx, testPassword, validCode(t, secret, now), now)
	if !errors.Is(err, errCredentialCheckFailed) {
		t.Fatalf("err = %v, want errCredentialCheckFailed while backed off", err)
	}
}

func TestVerifyCurrentCredentials_SuccessClearsTheCounter(t *testing.T) {
	srv, _, secret := newTestServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.11")
	now := time.Now()

	// One failure, inside the grace window so nothing is blocked yet.
	if err := srv.verifyCurrentCredentials(ctx, "wrong", validCode(t, secret, now), now); !errors.Is(err, errCredentialCheckFailed) {
		t.Fatalf("err = %v, want errCredentialCheckFailed", err)
	}
	if err := srv.verifyCurrentCredentials(ctx, testPassword, validCode(t, secret, now), now); err != nil {
		t.Fatalf("correct credentials were refused: %v", err)
	}
	if srv.backoff.blocked("203.0.113.11", now.Add(time.Second)) {
		t.Fatal("a successful re-proof left the caller backed off")
	}
}
