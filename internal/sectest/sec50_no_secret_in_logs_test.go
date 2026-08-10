package sectest

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SEC-50: no secret may appear in any log line, at any level, for any of:
// login (success and failure), password change, or TOTP enrollment. This
// drives all of those through the real store + rpc.AuthServer with logging
// routed to a buffer two ways at once — Store.Options.Log (the only slog
// logger internal/store accepts) AND slog.SetDefault (so a stray
// package-level slog.Info/Error call anywhere else in the call graph, not
// just inside internal/store, is caught too) — then scrapes the combined
// buffer for every secret value the test flow touches.
func TestNoSecretInLogs(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	prevDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	ctx := t.Context()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db"), Log: logger})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// --- secrets collected as the flow runs; checked against buf at the end ---
	var secrets []secretRef

	wrongPassword := "this is definitely the wrong password here"
	secrets = append(secrets, secretRef{"login failure password", wrongPassword})
	secrets = append(secrets, secretRef{"admin enrollment password", testPassword})

	hash, err := auth.Hash(testPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.InitAdmin(ctx, hash, "argon2id m=65536,t=3,p=1"); err != nil {
		t.Fatalf("init admin: %v", err)
	}

	totpSecret, _, err := auth.Enroll("admin", "AnimeFeedFlux-sec50")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	secrets = append(secrets, secretRef{"initial TOTP secret", totpSecret})

	// testSecretKey stands in for AFF_SECRET_KEY / the optional pepper input
	// (PLAN.md §4's pepper feature, SEC-07..SEC-10, is not yet wired into the
	// credential path — this is forward-looking coverage for the day it is,
	// using the one env-supplied secret that already exists today: the TOTP
	// encryption key).
	secrets = append(secrets, secretRef{"AFF_SECRET_KEY / pepper-analogous secret", string(testSecretKey)})

	enc, err := auth.EncryptSecret(totpSecret, testSecretKey)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if err := st.SetTOTPSecret(ctx, enc); err != nil {
		t.Fatalf("set totp secret: %v", err)
	}

	plainCodes, hashes, err := auth.GenerateCodes(3)
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	for i, c := range plainCodes {
		secrets = append(secrets, secretRef{"recovery code " + strconv.Itoa(i), c})
	}
	if err := st.StoreRecoveryCodes(ctx, hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	srv, err := rpc.NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}

	now := time.Now()

	// 1. Failed login (wrong password).
	failCtx := withPeerIP(ctx, "10.50.0.1")
	if _, err := srv.Login(failCtx, &affv1.AuthServiceLoginRequest{Password: wrongPassword, TotpCode: "000000"}); err == nil {
		t.Fatal("expected the wrong-password login to fail")
	}

	// 2. Successful login.
	loginToken := login(t, srv, totpSecret, "10.50.0.2", now)
	secrets = append(secrets, secretRef{"session token from successful login", loginToken})

	// 3. Password change.
	newPassword := "a completely different sec50 test passphrase"
	secrets = append(secrets, secretRef{"new password from ChangePassword", newPassword})
	changeCtx := rpc.ContextWithSessionToken(ctx, loginToken)
	if _, err := callAuthed(changeCtx, srv, affv1.AuthService_ChangePassword_FullMethodName,
		&affv1.AuthServiceChangePasswordRequest{
			CurrentPassword: testPassword,
			TotpCode:        validCode(t, totpSecret, now.Add(30*time.Second)),
			NewPassword:     newPassword,
		},
		func(ctx context.Context, req any) (any, error) {
			return srv.ChangePassword(ctx, req.(*affv1.AuthServiceChangePasswordRequest))
		}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// 4. TOTP re-enrollment (needs a live, non-revoked session — ChangePassword
	// only revokes OTHER sessions, so the caller's own session is still good).
	reenrollResp, err := callAuthed(changeCtx, srv, affv1.AuthService_ReenrollTOTP_FullMethodName,
		&affv1.AuthServiceReenrollTOTPRequest{CurrentPassword: newPassword},
		func(ctx context.Context, req any) (any, error) {
			return srv.ReenrollTOTP(ctx, req.(*affv1.AuthServiceReenrollTOTPRequest))
		})
	if err != nil {
		t.Fatalf("reenroll totp: %v", err)
	}
	newTOTPSecret := extractTOTPSecret(t, reenrollResp.(*affv1.AuthServiceReenrollTOTPResponse).ProvisioningUri)
	secrets = append(secrets, secretRef{"re-enrolled TOTP secret", newTOTPSecret})

	// 5. Recovery-code login (a second, independent path that touches
	// secrets: consumes a code and mints an elevated session token).
	recoverCtx, recoverFts := withTransportStream(withPeerIP(ctx, "10.50.0.3"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(recoverCtx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plainCodes[0]}); err != nil {
		t.Fatalf("recover with code: %v", err)
	}
	if elevatedTok := recoverFts.header.Get(rpc.SessionTokenHeader); len(elevatedTok) == 1 {
		secrets = append(secrets, secretRef{"elevated session token from recovery", elevatedTok[0]})
	}

	// --- assertions ---
	logged := buf.String()
	for _, s := range secrets {
		if s.value == "" {
			continue
		}
		if strings.Contains(logged, s.value) {
			t.Errorf("log output contains the %s (value redacted from this failure message on purpose): found a %d-byte secret verbatim in the log buffer", s.name, len(s.value))
		}
	}

	if t.Failed() {
		t.Logf("full log buffer for debugging (run locally, do not paste into a shared bug tracker as-is):\n%s", logged)
	}
}

type secretRef struct {
	name  string
	value string
}

// extractTOTPSecret pulls the base32 secret out of an otpauth:// provisioning
// URI (the same format an authenticator app's QR code encodes), so the test
// has the actual re-enrolled secret value to search the log buffer for.
func extractTOTPSecret(t *testing.T, provisioningURI string) string {
	t.Helper()
	u, err := url.Parse(provisioningURI)
	if err != nil {
		t.Fatalf("parsing provisioning URI: %v", err)
	}
	secret := u.Query().Get("secret")
	if secret == "" {
		t.Fatalf("provisioning URI has no secret query param: %s", provisioningURI)
	}
	return secret
}
