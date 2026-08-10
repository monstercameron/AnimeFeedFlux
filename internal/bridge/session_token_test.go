package bridge_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"google.golang.org/grpc"
)

// TestSessionToken_ReachesRPCContext is the direct test the ticket asks for:
// the raw cookie value presented at the WebSocket upgrade must show up on
// bridge.SessionFromContext(ctx).Token inside an actual RPC handler, over a
// real WebSocket connection — not merely asserted against ServeHTTP's
// internals. This is what makes internal/rpc's interceptor able to read a
// token at all without any caller-supplied forwarding step.
func TestSessionToken_ReachesRPCContext(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	var (
		mu  sync.Mutex
		got string
		ok  bool
	)
	captureInterceptor := grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mu.Lock()
		sess, sessOK := bridge.SessionFromContext(ctx)
		got, ok = sess.Token, sessOK
		mu.Unlock()
		return handler(ctx, req)
	})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		GRPCOptions:       []grpc.ServerOption{captureInterceptor},
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := checkHealth(ctx, conn); err != nil {
		t.Fatalf("health check over tunnel: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !ok {
		t.Fatalf("SessionFromContext found no session on the RPC context")
	}
	if got != "good-token" {
		t.Fatalf("Session.Token = %q, want %q", got, "good-token")
	}
}

// TestSessionToken_SetByServeHTTPRegardlessOfValidator proves Session.Token
// is NOT something a SessionValidator implementation can omit: even a
// validator that returns a Session with Token left zero (the shape every
// existing Validator in this tree used before this field existed) still
// produces a populated Token in the RPC context, because ServeHTTP itself
// overwrites it from the cookie it already read. This is the "impossible to
// silently get no token" property — there is no wiring step to forget.
func TestSessionToken_SetByServeHTTPRegardlessOfValidator(t *testing.T) {
	// This Validator deliberately never sets Session.Token, mirroring every
	// pre-existing caller (bridge's own other tests, cmd/animefeedflux's
	// production validator, internal/e2e's) written against the old,
	// token-less Session shape.
	validator := bridge.SessionValidatorFunc(func(_ context.Context, token string, now time.Time) (bridge.Session, error) {
		if token != "good-token" {
			return bridge.Session{}, errors.New("unknown token")
		}
		return bridge.Session{UserID: "cam", ExpiresAt: now.Add(time.Hour)}, nil // no Token set
	})

	var (
		mu  sync.Mutex
		got string
		ok  bool
	)
	captureInterceptor := grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mu.Lock()
		sess, sessOK := bridge.SessionFromContext(ctx)
		got, ok = sess.Token, sessOK
		mu.Unlock()
		return handler(ctx, req)
	})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		GRPCOptions:       []grpc.ServerOption{captureInterceptor},
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := checkHealth(ctx, conn); err != nil {
		t.Fatalf("health check over tunnel: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !ok || got != "good-token" {
		t.Fatalf("Session.Token = %q, ok=%v, want %q, ok=true even though the Validator never set it", got, ok, "good-token")
	}
}

// TestSessionToken_EnablesImmediatePerRPCRevocation is the SEC-41 test: it
// proves that forwarding the raw token per-RPC (rather than a cached
// validity decision) lets a downstream authorizer — modeled here the same
// way internal/rpc's real interceptor works: hash-and-look-up on every call
// — refuse a revoked session on the very NEXT call over an already-open
// stream, without waiting for the connection-level revalidation loop
// (bounded by RevalidateInterval, and in this test set far longer than the
// test would tolerate waiting on). If Session.Token ever became a cached
// "this connection is authorized" flag instead of an inert credential
// re-checked per call, this test would keep passing right through a
// revocation — which is exactly the regression this pins against.
func TestSessionToken_EnablesImmediatePerRPCRevocation(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(24 * time.Hour)})

	// revoked models the store-backed check internal/rpc's real authorize()
	// performs on every call (GetSessionByTokenHash + Valid). It is flipped
	// directly, independent of anything the bridge's own Validator or
	// revalidation loop does, so this test isolates the per-RPC path.
	var revoked atomic.Bool

	perRPCAuthorize := grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		sess, ok := bridge.SessionFromContext(ctx)
		if !ok || sess.Token == "" {
			return nil, errors.New("no session token forwarded")
		}
		if revoked.Load() {
			return nil, errors.New("session revoked")
		}
		return handler(ctx, req)
	})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		// Deliberately long: this test must pass because of the per-RPC
		// check above, not because the connection-level revalidation loop
		// happened to fire first.
		RevalidateInterval: time.Hour,
		GRPCOptions:        []grpc.ServerOption{perRPCAuthorize},
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := checkHealth(ctx, conn); err != nil {
		t.Fatalf("health check before revocation: %v", err)
	}

	revoked.Store(true)

	if err := checkHealth(ctx, conn); err == nil {
		t.Fatalf("RPC after revocation unexpectedly succeeded on the still-open stream")
	}
}
