// Session enforcement for every RPC on every service (PLAN.md §4: "no
// authenticated at upgrade, trusted forever"). The bridge validates the
// WebSocket upgrade and the Origin header once; this file is what re-checks
// the session on every single call after that, because a session minted at
// 12:01 must stop working the instant it expires, is idle-timed-out, or is
// revoked — not merely at the next reconnect.
//
// This lives on AuthServer (not a free-standing type) because the elevated-
// session tracker it reads is the same in-memory tracker RecoverWithCode
// writes to in auth.go — the two must share one instance, and "share one
// instance" is simplest when they're the same struct.
package rpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// sessionTokenCtxKey is the context key the transport layer (the bridge)
// writes the raw cookie value under before invoking the gRPC stack. It is
// unexported so nothing outside this package can collide with it or forge a
// value into it by accident; ContextWithSessionToken is the only legitimate
// way to set it.
type sessionTokenCtxKey struct{}

// ContextWithSessionToken attaches the raw session token (the cookie value,
// not its hash) to ctx. The bridge calls this once per request/stream after
// reading the __Host-aff_session cookie, and everything downstream —
// authorize, and therefore every RPC handler — reads it back out through
// sessionTokenFromContext. The interceptor hashes it before ever touching
// storage or logs (PLAN.md §4: only the hash is ever persisted or compared).
func ContextWithSessionToken(ctx context.Context, rawToken string) context.Context {
	return context.WithValue(ctx, sessionTokenCtxKey{}, rawToken)
}

func sessionTokenFromContext(ctx context.Context) (string, bool) {
	v, _ := ctx.Value(sessionTokenCtxKey{}).(string)
	return v, v != ""
}

// callerSessionCtxKey carries the already-validated session down to the RPC
// handler, so a handler that needs "is this session elevated" (ChangePassword,
// ReenrollTOTP) or "which session is this" (Logout, Session, ListSessions'
// is_current) doesn't re-run the lookup authorize already did.
type callerSessionCtxKey struct{}

type callerSession struct {
	ID        int64
	TokenHash string
	Elevated  bool
}

func withCallerSession(ctx context.Context, cs callerSession) context.Context {
	return context.WithValue(ctx, callerSessionCtxKey{}, cs)
}

// callerSessionFromContext is used by auth.go's handlers. It only succeeds
// for methods that went through authorize with a session attached — Login
// and RecoverWithCode never populate it, matching that neither needs one.
func callerSessionFromContext(ctx context.Context) (cs callerSession, ok bool) {
	cs, ok = ctx.Value(callerSessionCtxKey{}).(callerSession)
	return cs, ok
}

// noSessionMethods is the exemption list. PLAN.md §4 is explicit that these
// are the only two ways to reach the system without already holding a
// session: signing in, and using a recovery code to start signing back in.
var noSessionMethods = map[string]bool{
	affv1.AuthService_Login_FullMethodName:           true,
	affv1.AuthService_RecoverWithCode_FullMethodName: true,
}

// elevatedAllowedMethods is the ENTIRE reachable surface for a session opened
// by RecoverWithCode (PLAN.md §12.2). Everything else — including every other
// AuthService method, e.g. RegenerateRecoveryCodes — is PermissionDenied for
// an elevated session. Deny-by-default (checking membership in an allowlist,
// not exclusion from a denylist) means a new RPC added anywhere in the future
// is unreachable from an elevated session unless someone deliberately adds it
// here.
var elevatedAllowedMethods = map[string]bool{
	affv1.AuthService_ChangePassword_FullMethodName: true,
	affv1.AuthService_ReenrollTOTP_FullMethodName:   true,
}

// authorize is the shared gate behind both interceptors below: look up the
// session by the hash of the presented token, reject anything Valid refuses
// (expired, idle-timed-out, revoked), reject an elevated session reaching
// outside its allowlist, and otherwise touch last_seen_at and hand the
// handler a context carrying the resolved session.
func (s *AuthServer) authorize(ctx context.Context, fullMethod string) (context.Context, error) {
	if noSessionMethods[fullMethod] {
		return ctx, nil
	}

	raw, ok := sessionTokenFromContext(ctx)
	if !ok {
		return ctx, status.Error(codes.Unauthenticated, "no session")
	}
	hash := auth.HashToken(raw)

	sess, err := s.store.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return ctx, status.Error(codes.Unauthenticated, "invalid session")
	}

	now := s.now()
	if !sess.Valid(now, auth.SessionIdleTimeout) {
		return ctx, status.Error(codes.Unauthenticated, "session expired")
	}

	id, err := s.sessionIDByHash(ctx, hash)
	if err != nil {
		return ctx, status.Error(codes.Unauthenticated, "invalid session")
	}

	elevated := s.elevated.isElevated(hash, now)
	if elevated && !elevatedAllowedMethods[fullMethod] {
		return ctx, status.Error(codes.PermissionDenied, "elevated session cannot reach this method")
	}

	// Every authenticated call renews the idle clock, not just the ones that
	// look like activity — the idle timeout (§4, 60m) measures "was this
	// session used", and a call that reaches this far already proves that.
	if err := s.store.TouchSession(ctx, id, now); err != nil {
		return ctx, status.Error(codes.Internal, "touching session")
	}

	return withCallerSession(ctx, callerSession{ID: id, TokenHash: hash, Elevated: elevated}), nil
}

// UnaryInterceptor validates the session on every unary call. Constructed
// from an *AuthServer (rather than a free function) so it shares the
// elevated-session tracker and store handle with the AuthService
// implementation in auth.go.
func (s *AuthServer) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		newCtx, err := s.authorize(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(newCtx, req)
	}
}

// StreamInterceptor validates the session at stream open. Re-checking
// periodically over the life of a long stream is the bridge's job (PLAN.md
// §4: "the socket revalidates the session periodically"), not this
// interceptor's — this only gates whether the stream is allowed to open at
// all, the same way the unary interceptor gates a single call.
func (s *AuthServer) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		newCtx, err := s.authorize(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &sessionServerStream{ServerStream: ss, ctx: newCtx})
	}
}

// sessionServerStream overrides Context() so handler code downstream sees the
// context authorize produced (carrying callerSession), the standard pattern
// for threading interceptor results into a streaming handler.
type sessionServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *sessionServerStream) Context() context.Context { return w.ctx }
