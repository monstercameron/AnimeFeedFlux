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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// sessionTokenCtxKey is an explicit-override context key: something set it
// deliberately via ContextWithSessionToken, rather than it having arrived by
// riding on a bridge connection. It is unexported so nothing outside this
// package can collide with it or forge a value into it by accident;
// ContextWithSessionToken is the only legitimate way to set it.
type sessionTokenCtxKey struct{}

// ContextWithSessionToken attaches the raw session token (the cookie value,
// not its hash) to ctx, overriding whatever sessionTokenFromContext would
// otherwise find. This exists for two callers, both of which bypass the
// bridge on purpose: this package's own unit tests, which call
// srv.authorize directly against a hand-built context instead of standing up
// a real WebSocket connection; and any future transport that isn't
// internal/bridge (e.g. cmd/aff's plain-gRPC session-metadata path) that
// needs the same authorize/interceptor logic without a bridge.Session to
// draw from. Production bridge connections do NOT need this — see
// sessionTokenFromContext.
func ContextWithSessionToken(ctx context.Context, rawToken string) context.Context {
	return context.WithValue(ctx, sessionTokenCtxKey{}, rawToken)
}

// sessionTokenFromContext resolves the raw session token for this call, in a
// fixed priority order:
//
//  1. An explicit ContextWithSessionToken override. This must stay first:
//     this package's own unit tests build a context by hand and rely on it
//     winning over whatever (if anything) else is attached, and a caller that
//     went out of its way to set this key is asserting a specific token on
//     purpose — a value arriving incidentally by transport must never
//     override a value arriving deliberately by API.
//
//  2. bridge.SessionFromContext(ctx).Token — the value internal/bridge's
//     ServeHTTP attaches to every request on a validated WebSocket connection
//     UNCONDITIONALLY (see bridge.Session.Token's doc comment). That fallback
//     is what closes the gap this package's history records: until it
//     existed, nothing in production ever populated the explicit key, so
//     every RPC over a real bridge connection was Unauthenticated regardless
//     of how valid the underlying session was. No composition root has to
//     remember to call ContextWithSessionToken anymore; a bridge-backed
//     connection supplies the token by construction.
//
//  3. Incoming gRPC metadata under SessionTokenHeader — the path
//     ContextWithSessionToken's own doc comment anticipates for "any future
//     transport that isn't internal/bridge (e.g. cmd/aff's plain-gRPC
//     session-metadata path)". cmd/aff dials AdminAddr directly with
//     grpc.NewClient, bypassing the bridge entirely, so neither of the first
//     two sources is ever populated for it; this is the only source that
//     authenticates it. It comes last, after the bridge check, so a
//     bridge-backed call can never be re-authenticated by attacker-supplied
//     metadata riding along on the same connection — only a connection with
//     no bridge session at all falls through this far.
//
//     That last clause used to be false. The check was `ok && sess.Token !=
//     ""`, and an ANONYMOUS bridge upgrade carries a Session with an empty
//     Token, so it fell through to metadata: a value supplied by the browser's
//     own WASM could authenticate a call on a bridge connection. Not
//     exploitable by itself — the metadata still has to carry a valid session
//     token, which an XSS cannot read out of an HttpOnly cookie — but §4
//     states "the token never touches JavaScript or WASM" as an invariant, and
//     this was a path where a WASM-supplied value did. The presence of a
//     bridge session is now what gates the fallback, not whether that session
//     happens to be authenticated (A8-37).
//
// Whichever source wins, the token is treated identically from here on:
// authorize always hashes it and re-checks the session against the store, so
// arriving by metadata carries no less scrutiny (and no shortcut) than
// arriving by cookie.
func sessionTokenFromContext(ctx context.Context) (string, bool) {
	if v, _ := ctx.Value(sessionTokenCtxKey{}).(string); v != "" {
		return v, true
	}
	// A bridge session PRESENT at all — authenticated or anonymous — settles
	// it. An anonymous upgrade has an empty Token and is simply unauthenticated;
	// falling through to metadata there is what let a WASM-supplied value
	// authenticate a bridge call.
	if sess, ok := bridge.SessionFromContext(ctx); ok {
		if sess.Token != "" {
			return sess.Token, true
		}
		return "", false
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(SessionTokenHeader); len(v) > 0 && v[0] != "" {
			return v[0], true
		}
	}
	return "", false
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

	// The in-memory tracker remains the thing that DECIDES whether this
	// session is elevated (RecoverWithCode marks it, it expires after
	// elevatedSessionTTL, and it resets — safely, to "not elevated" — on a
	// restart; see elevatedTracker's doc comment. That behavior is
	// deliberately unchanged here, per OQ-06: this migration adds a scope
	// column, it does not decide whether a recovery code buys one action or
	// two.) What changes is that the decision is now written onto the
	// session's own row (migrations/0005_session_scope.sql) on every
	// authorized call, so scope is something the session carries rather
	// than something that exists only in this process's memory, and so a
	// default-deny check can be made against it centrally, here, rather
	// than trusting every future handler to remember to ask the tracker.
	// The PERSISTED scope is authoritative, and a session can only ever be
	// narrowed here — never widened.
	//
	// This block used to derive scope purely from the in-memory tracker and
	// then write that decision onto the row. After a process restart the
	// tracker is empty, so isElevated returned false, wantScope became `full`,
	// and the persisted `elevated` was OVERWRITTEN with `full` — after which
	// the default-deny check below passed. A session opened by RecoverWithCode,
	// scoped by §12.2 to ChangePassword and ReenrollTOTP, could then reach
	// every RPC in the system for the rest of its 10-minute life, including
	// RegenerateRecoveryCodes and every feed mutation (A8-31).
	//
	// elevatedTracker's own doc claims a restart defaults to "not elevated
	// (i.e. not trusted with anything extra)". In that code path "not
	// elevated" meant FULL PRIVILEGES, so the failure direction was the exact
	// opposite of what was written — which is what made it hard to see.
	//
	// migrations/0005 exists so scope is "something the session carries rather
	// than something that exists only in this process's memory", and
	// store.SessionScope — the reader — had no callers at all, so the column
	// was write-only and the migration delivered none of that. Reading it is
	// the fix.
	elevated := s.elevated.isElevated(hash, now)
	stored, err := s.store.SessionScope(ctx, id)
	if err != nil {
		return ctx, status.Error(codes.Internal, "reading session scope")
	}
	if stored == store.SessionScopeElevated {
		// The row says this session was opened by a recovery code. A restart
		// losing the tracker entry must not promote it.
		elevated = true
	}

	wantScope := store.SessionScopeFull
	if elevated {
		wantScope = store.SessionScopeElevated
	}
	// Only written when it actually changes, and never from elevated to full:
	// a session leaves elevated scope by expiring, not by being re-read.
	if stored != wantScope && stored != store.SessionScopeElevated {
		if err := s.store.SetSessionScope(ctx, id, wantScope); err != nil {
			return ctx, status.Error(codes.Internal, "updating session scope")
		}
	}

	// Default deny: fullMethod must be explicitly present in
	// elevatedAllowedMethods to be reachable from a non-full scope. A method
	// nobody has opted in yet — including one that does not exist today —
	// is refused, not silently reachable.
	if wantScope != store.SessionScopeFull && !elevatedAllowedMethods[fullMethod] {
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
