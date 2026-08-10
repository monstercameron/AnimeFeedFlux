package rpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// loginAndGetToken is the interceptor tests' shared setup: log in for real
// (so a genuine session row exists) and return the raw token.
func loginAndGetToken(t *testing.T, srv *AuthServer, secret string, at time.Time, ip string) string {
	t.Helper()
	ctx, fts := withTransportStream(withPeerIP(t.Context(), ip), affv1.AuthService_Login_FullMethodName)
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: validCode(t, secret, at)}); err != nil {
		t.Fatalf("login: %v", err)
	}
	return fts.header.Get(SessionTokenHeader)[0]
}

// TestUnauthenticatedMethodsReachableWithoutSession covers the exemption
// list: Login and RecoverWithCode must be reachable with no session token in
// context at all, and everything else must not be.
func TestUnauthenticatedMethodsReachableWithoutSession(t *testing.T) {
	srv, _, _ := newTestServer(t)

	for _, method := range []string{
		affv1.AuthService_Login_FullMethodName,
		affv1.AuthService_RecoverWithCode_FullMethodName,
	} {
		if _, err := srv.authorize(t.Context(), method); err != nil {
			t.Errorf("%s: expected no session to be required, got %v", method, err)
		}
	}

	for _, method := range []string{
		affv1.AuthService_Session_FullMethodName,
		affv1.AuthService_Logout_FullMethodName,
		"/aff.v1.FeedService/List",
	} {
		_, err := srv.authorize(t.Context(), method)
		if err == nil {
			t.Errorf("%s: expected a session to be required, got none", method)
			continue
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: code = %v, want Unauthenticated", method, status.Code(err))
		}
	}
}

// TestElevatedSessionBlockedFromFeedService is the scoping rule at the heart
// of recovery: an elevated session may reach ONLY ChangePassword and
// ReenrollTOTP; every other method — including ones outside AuthService
// entirely — is PermissionDenied.
func TestElevatedSessionBlockedFromFeedService(t *testing.T) {
	srv, st, _ := newTestServer(t)
	plain, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.6.0.1"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]}); err != nil {
		t.Fatalf("recover: %v", err)
	}
	token := fts.header.Get(SessionTokenHeader)[0]

	blocked := []string{
		"/aff.v1.FeedService/List",
		"/aff.v1.FeedService/Create",
		affv1.AuthService_ListSessions_FullMethodName,
		affv1.AuthService_RegenerateRecoveryCodes_FullMethodName,
	}
	for _, method := range blocked {
		_, err := srv.authorize(ContextWithSessionToken(t.Context(), token), method)
		if err == nil {
			t.Errorf("%s: elevated session reached a method outside its allowlist", method)
			continue
		}
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s: code = %v, want PermissionDenied", method, status.Code(err))
		}
	}

	allowed := []string{
		affv1.AuthService_ChangePassword_FullMethodName,
		affv1.AuthService_ReenrollTOTP_FullMethodName,
	}
	for _, method := range allowed {
		if _, err := srv.authorize(ContextWithSessionToken(t.Context(), token), method); err != nil {
			t.Errorf("%s: elevated session should reach this method, got %v", method, err)
		}
	}
}

// TestExpiredSessionRejected covers the absolute-lifetime kill switch.
func TestExpiredSessionRejected(t *testing.T) {
	srv, _, _ := newTestServer(t)
	past := time.Now().Add(-1 * time.Hour)
	rawToken, _, _, err := srv.mintSession(t.Context(), "10.7.0.1", "test-agent", past.Add(-13*time.Hour), past)
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}

	_, err = srv.authorize(ContextWithSessionToken(t.Context(), rawToken), affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected an expired session to be rejected")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestRevokedSessionRejected covers the explicit-revocation kill switch —
// the property RevokeSession/ChangePassword depend on to have any teeth.
func TestRevokedSessionRejected(t *testing.T) {
	srv, st, secret := newTestServer(t)
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.8.0.1")

	sess, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	id, err := srv.sessionIDByHash(t.Context(), sess.TokenHash)
	if err != nil {
		t.Fatalf("lookup session id: %v", err)
	}
	if err := st.RevokeSession(t.Context(), id); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	_, err = srv.authorize(ContextWithSessionToken(t.Context(), token), affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected a revoked session to be rejected")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestIdleTimeoutRejected covers the idle-timeout kill switch independently
// of absolute expiry.
func TestIdleTimeoutRejected(t *testing.T) {
	srv, _, secret := newTestServer(t)
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.9.0.1")

	// Freeze the server's clock far enough past last_seen_at to cross the
	// idle timeout but nowhere near the absolute lifetime, isolating this
	// from TestExpiredSessionRejected.
	srv.now = func() time.Time { return time.Now().Add(auth.SessionIdleTimeout + time.Minute) }

	_, err := srv.authorize(ContextWithSessionToken(t.Context(), token), affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected an idle-timed-out session to be rejected")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestUnaryInterceptorWiring is a thin smoke test that UnaryInterceptor
// actually plugs authorize into the standard grpc.UnaryServerInterceptor
// shape: a missing session short-circuits before the handler runs, and a
// valid one reaches it with callerSession attached.
func TestUnaryInterceptorWiring(t *testing.T) {
	srv, _, secret := newTestServer(t)
	interceptor := srv.UnaryInterceptor()

	handlerCalled := false
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		if _, ok := callerSessionFromContext(ctx); !ok {
			t.Error("handler context is missing callerSession")
		}
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: affv1.AuthService_Session_FullMethodName}

	// No token: handler must not run.
	_, err := interceptor(t.Context(), nil, info, handler)
	if err == nil {
		t.Error("expected an error with no session token")
	}
	if handlerCalled {
		t.Error("handler ran despite no session")
	}

	// Valid token: handler must run and see the resolved session.
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.10.0.1")
	ctx := ContextWithSessionToken(t.Context(), token)
	_, err = interceptor(ctx, nil, info, handler)
	if err != nil {
		t.Fatalf("interceptor rejected a valid session: %v", err)
	}
	if !handlerCalled {
		t.Error("handler did not run with a valid session")
	}
}
