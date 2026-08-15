package rpc

import (
	"net/url"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// newUninitializedServer is newTestServer minus the enrolled admin: a fresh
// store with no admin row at all, which is the ONLY state Setup may succeed
// in.
func newUninitializedServer(t *testing.T) *AuthServer {
	t.Helper()
	st := openTestStore(t)
	srv, err := NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}
	return srv
}

// secretFromProvisioningURI pulls the shared secret back out of the
// otpauth:// URI Setup returns, so the test can compute valid TOTP codes the
// way an authenticator app would.
func secretFromProvisioningURI(t *testing.T, uri string) string {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parsing provisioning URI %q: %v", uri, err)
	}
	secret := u.Query().Get("secret")
	if secret == "" {
		t.Fatalf("provisioning URI %q carries no secret parameter", uri)
	}
	return secret
}

// TestSetupClaimsInstanceAndLoginWorks is the whole point of the feature in
// one test: Setup on an empty store succeeds exactly once, returns a
// scannable TOTP secret and a full recovery-code set, and the credentials it
// enrolled actually sign in through the ordinary Login path.
func TestSetupClaimsInstanceAndLoginWorks(t *testing.T) {
	srv := newUninitializedServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.7")

	resp, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(resp.RecoveryCodes) != recoveryCodeCount {
		t.Fatalf("recovery codes: got %d, want %d", len(resp.RecoveryCodes), recoveryCodeCount)
	}
	secret := secretFromProvisioningURI(t, resp.ProvisioningUri)

	login, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	})
	if err != nil {
		t.Fatalf("login after setup: %v", err)
	}
	if login.Session == nil {
		t.Fatal("login after setup returned no session")
	}
}

// TestSetupRecoveryCodesAreLive proves the codes Setup hands back were
// actually stored (hashed) — one of them must open an elevated recovery
// session.
func TestSetupRecoveryCodesAreLive(t *testing.T) {
	srv := newUninitializedServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.8")

	resp, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	rec, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{
		RecoveryCode: resp.RecoveryCodes[0],
	})
	if err != nil {
		t.Fatalf("recover with setup-issued code: %v", err)
	}
	if rec.Session == nil {
		t.Fatal("recovery returned no session")
	}
	if got, want := rec.RemainingRecoveryCodes, int32(recoveryCodeCount-1); got != want {
		t.Fatalf("remaining codes: got %d, want %d", got, want)
	}
}

// TestSetupRefusedOnceAdminExists covers both refusal shapes with one
// assertion each: a second Setup call after a successful one, and Setup
// against an instance that was initialized some other way (newTestServer's
// direct store writes standing in for `aff admin init`). Both must return
// the one generic FailedPrecondition — never a distinct message per cause.
func TestSetupRefusedOnceAdminExists(t *testing.T) {
	t.Run("second call", func(t *testing.T) {
		srv := newUninitializedServer(t)
		ctx := withPeerIP(t.Context(), "203.0.113.9")
		if _, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword}); err != nil {
			t.Fatalf("first setup: %v", err)
		}
		_, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword + " again"})
		assertSetupUnavailable(t, err)
	})
	t.Run("cli-initialized instance", func(t *testing.T) {
		srv, _, _ := newTestServer(t)
		ctx := withPeerIP(t.Context(), "203.0.113.10")
		_, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword})
		assertSetupUnavailable(t, err)
	})
}

func assertSetupUnavailable(t *testing.T, err error) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("got %v, want FailedPrecondition", err)
	}
	if st.Message() != "setup unavailable" {
		t.Fatalf("refusal message %q leaks a cause; want the one generic string", st.Message())
	}
}

// TestSetupWeakPasswordRejectedWithoutClaiming: a policy rejection must not
// burn the one-time window — the instance stays claimable afterward.
func TestSetupWeakPasswordRejectedWithoutClaiming(t *testing.T) {
	srv := newUninitializedServer(t)
	ctx := withPeerIP(t.Context(), "203.0.113.11")

	_, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: "short"})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("weak password: got %v, want InvalidArgument", err)
	}

	if _, err := srv.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: testPassword}); err != nil {
		t.Fatalf("setup after a rejected weak attempt: %v", err)
	}
}

// TestSetupReachableWithoutSession pins the interceptor exemption: Setup
// must authorize with no token at all, exactly like Login/RecoverWithCode —
// and stay refused-by-its-own-guard rather than refused-by-the-interceptor
// once the instance is claimed.
func TestSetupReachableWithoutSession(t *testing.T) {
	srv, _, _ := newTestServer(t)
	if _, err := srv.authorize(t.Context(), affv1.AuthService_Setup_FullMethodName); err != nil {
		t.Fatalf("authorize(Setup) with no session: %v", err)
	}
}
