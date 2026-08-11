package rpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// withSessionMetadata attaches token as incoming gRPC metadata under
// SessionTokenHeader — the shape cmd/aff's plain-gRPC PerRPCCredentials
// produces on the wire, as opposed to ContextWithSessionToken's explicit
// override or a bridge.Session riding on the context.
func withSessionMetadata(ctx context.Context, token string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(SessionTokenHeader, token))
}

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

// TestElevatedSessionDefaultDeniesUnlistedMethod asserts the default-deny
// property directly, rather than by listing every known RPC: a method name
// that exists nowhere — not in elevatedAllowedMethods, not registered on any
// real service, standing in for an RPC added after this code was written —
// must still be refused for an elevated session. An allowlist has this
// property by construction (unlisted == absent == denied); a denylist would
// not, which is why this test targets a name that is guaranteed to never
// appear on either list rather than one that happens to be missing today.
func TestElevatedSessionDefaultDeniesUnlistedMethod(t *testing.T) {
	srv, st, _ := newTestServer(t)
	plain, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.6.0.2"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]}); err != nil {
		t.Fatalf("recover: %v", err)
	}
	token := fts.header.Get(SessionTokenHeader)[0]

	const unlistedMethod = "/aff.v1.NotYetInventedService/DoSomethingNew"
	_, err = srv.authorize(ContextWithSessionToken(t.Context(), token), unlistedMethod)
	if err == nil {
		t.Fatal("elevated session reached an RPC that has never been added to elevatedAllowedMethods")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", status.Code(err))
	}
}

// TestFullSessionScopeIsUnaffected: an ordinary (non-elevated) session's
// scope round-trips as "full" through the interceptor, and it is unaffected
// by the elevated-scope enforcement path — every method it could reach
// before this change, it can still reach.
func TestFullSessionScopeIsUnaffected(t *testing.T) {
	srv, st, secret := newTestServer(t)
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.6.0.3")

	sess, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	id, err := srv.sessionIDByHash(t.Context(), sess.TokenHash)
	if err != nil {
		t.Fatalf("lookup session id: %v", err)
	}

	for _, method := range []string{
		affv1.AuthService_Session_FullMethodName,
		affv1.AuthService_ListSessions_FullMethodName,
		"/aff.v1.FeedService/List",
	} {
		if _, err := srv.authorize(ContextWithSessionToken(t.Context(), token), method); err != nil {
			t.Errorf("%s: full session should reach this method, got %v", method, err)
		}
	}

	scope, err := st.SessionScope(t.Context(), id)
	if err != nil {
		t.Fatalf("SessionScope: %v", err)
	}
	if scope != store.SessionScopeFull {
		t.Errorf("full session's persisted scope = %q, want %q", scope, store.SessionScopeFull)
	}
}

// TestElevatedSessionScopeIsPersistedOnTheSessionRow proves the scope an
// elevated session is enforced against is not merely process-local state:
// authorize() writes it onto the session's own row
// (migrations/0005_session_scope.sql), where anything reading the store
// directly — not just this process's in-memory tracker — can see it.
func TestElevatedSessionScopeIsPersistedOnTheSessionRow(t *testing.T) {
	srv, st, _ := newTestServer(t)
	plain, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	ctx, fts := withTransportStream(withPeerIP(t.Context(), "10.6.0.4"), affv1.AuthService_RecoverWithCode_FullMethodName)
	if _, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]}); err != nil {
		t.Fatalf("recover: %v", err)
	}
	token := fts.header.Get(SessionTokenHeader)[0]

	id, err := srv.sessionIDByHash(t.Context(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session id: %v", err)
	}

	// The row says elevated BEFORE any authorize() call.
	//
	// This assertion was the other way round — "the tracker knows this session
	// is elevated, but nothing has written that onto the row yet" — which was
	// true and was the bug. A session that made no call before a restart came
	// back with the schema default of `full`, and authorize, deriving scope
	// from an empty tracker, then confirmed it. RecoverWithCode now persists
	// the scope when it mints the session, so the row is authoritative from
	// the moment the session exists (A8-31).
	before, err := st.SessionScope(t.Context(), id)
	if err != nil {
		t.Fatalf("SessionScope before authorize: %v", err)
	}
	if before != store.SessionScopeElevated {
		t.Errorf("scope before any authorize() call = %q, want %q — a session that makes no call "+
			"before a restart would come back with full privileges", before, store.SessionScopeElevated)
	}

	if _, err := srv.authorize(ContextWithSessionToken(t.Context(), token), affv1.AuthService_ChangePassword_FullMethodName); err != nil {
		t.Fatalf("authorize on an allowed method: %v", err)
	}

	after, err := st.SessionScope(t.Context(), id)
	if err != nil {
		t.Fatalf("SessionScope after authorize: %v", err)
	}
	if after != store.SessionScopeElevated {
		t.Errorf("scope after authorize() = %q, want %q", after, store.SessionScopeElevated)
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

// TestSessionTokenFromMetadataAuthorizes covers the third source
// sessionTokenFromContext checks: a token that arrives as incoming gRPC
// metadata rather than an explicit ContextWithSessionToken override or a
// bridge.Session — the path cmd/aff's plain-gRPC client relies on, since it
// dials AdminAddr directly and never goes through internal/bridge.
func TestSessionTokenFromMetadataAuthorizes(t *testing.T) {
	srv, _, secret := newTestServer(t)
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.11.0.1")

	ctx := withSessionMetadata(t.Context(), token)
	if _, err := srv.authorize(ctx, affv1.AuthService_Session_FullMethodName); err != nil {
		t.Fatalf("authorize with metadata-borne token: %v", err)
	}
}

// TestSessionTokenFromMetadataRevokedRejected proves the metadata path gets
// the exact same re-check-against-the-store treatment as every other source
// — SEC-41 (revocation refused mid-connection) must hold here too, not just
// for the explicit-key and bridge paths.
func TestSessionTokenFromMetadataRevokedRejected(t *testing.T) {
	srv, st, secret := newTestServer(t)
	token := loginAndGetToken(t, srv, secret, time.Now(), "10.11.0.2")

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

	ctx := withSessionMetadata(t.Context(), token)
	_, err = srv.authorize(ctx, affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected a revoked session arriving by metadata to be rejected")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestSessionTokenFromMetadataExpiredRejected is TestExpiredSessionRejected's
// counterpart for the metadata path.
func TestSessionTokenFromMetadataExpiredRejected(t *testing.T) {
	srv, _, _ := newTestServer(t)
	past := time.Now().Add(-1 * time.Hour)
	rawToken, _, _, err := srv.mintSession(t.Context(), "10.11.0.3", "test-agent", past.Add(-13*time.Hour), past)
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}

	ctx := withSessionMetadata(t.Context(), rawToken)
	_, err = srv.authorize(ctx, affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected an expired session arriving by metadata to be rejected")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestExplicitSessionTokenWinsOverMetadata pins the priority order
// sessionTokenFromContext's doc comment commits to: an explicit
// ContextWithSessionToken override must be used even when metadata carrying
// a DIFFERENT (here, invalid) token is also present on the same context —
// tests elsewhere in this package rely on the explicit key always winning.
func TestExplicitSessionTokenWinsOverMetadata(t *testing.T) {
	srv, _, secret := newTestServer(t)
	goodToken := loginAndGetToken(t, srv, secret, time.Now(), "10.11.0.4")

	ctx := withSessionMetadata(t.Context(), "not-a-real-token")
	ctx = ContextWithSessionToken(ctx, goodToken)

	if _, err := srv.authorize(ctx, affv1.AuthService_Session_FullMethodName); err != nil {
		t.Fatalf("explicit context token should have won over bogus metadata: %v", err)
	}
}

// TestNoSessionTokenAnywhereIsUnauthenticatedNotPanic covers the empty case
// across all three sources at once: no explicit key, no bridge session, and
// no incoming metadata at all (not even an empty MD) must return a plain
// Unauthenticated error, never panic — metadata.FromIncomingContext on a
// context that never called NewIncomingContext returns ok=false, which
// sessionTokenFromContext must handle without a nil-map access.
func TestNoSessionTokenAnywhereIsUnauthenticatedNotPanic(t *testing.T) {
	srv, _, _ := newTestServer(t)

	_, err := srv.authorize(t.Context(), affv1.AuthService_Session_FullMethodName)
	if err == nil {
		t.Fatal("expected no session to be rejected")
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
