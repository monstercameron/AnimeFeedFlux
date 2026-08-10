package sectest

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SEC-41: a token whose session has been explicitly revoked must be refused
// on every path that checks it — a plain RPC call, and a stream that was
// already open when the revocation happened.

// TestRevokedSession_UnaryRefused covers the ordinary RPC path via the real
// production interceptor (rpc.AuthServer.UnaryInterceptor), not a
// hand-rolled substitute.
func TestRevokedSession_UnaryRefused(t *testing.T) {
	srv, st, secret := newTestServer(t)
	token := login(t, srv, secret, "10.20.0.1", time.Now())

	sess, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	id := sessionIDFromHash(t, st, sess.TokenHash)
	if err := st.RevokeSession(t.Context(), id); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	ctx := rpc.ContextWithSessionToken(t.Context(), token)
	_, err = callAuthed(ctx, srv, affv1.AuthService_Session_FullMethodName,
		&affv1.AuthServiceSessionRequest{},
		func(ctx context.Context, req any) (any, error) {
			return srv.Session(ctx, req.(*affv1.AuthServiceSessionRequest))
		})
	if err == nil {
		t.Fatal("revoked session was accepted on a unary call")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// sessionIDFromHash resolves a session row id from its token hash via
// Store.Writer() — the same reach-through internal/rpc's own
// sessionIDByHash uses, since internal/store exposes sessions only by hash
// or by id, never a direct hash->id lookup.
func sessionIDFromHash(t *testing.T, st *store.Store, tokenHash string) int64 {
	t.Helper()
	var id int64
	err := st.Writer().QueryRowContext(t.Context(),
		`SELECT id FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&id)
	if err != nil {
		t.Fatalf("resolving session id for hash %q: %v", tokenHash, err)
	}
	return id
}

// TestRevokedSession_AlreadyOpenStreamRefused is the "including on an
// already-open stream" half: a real WebSocket-tunneled gRPC connection is
// opened while the session is valid, proven alive with a health check, then
// the session is revoked through the real store.RevokeSession path (not a
// test double), and the live socket must go down on its next periodic
// revalidation — PLAN.md §4's "authenticated at 12:01 ... socket still
// serving at 23:00" bug, but for revocation instead of expiry.
func TestRevokedSession_AlreadyOpenStreamRefused(t *testing.T) {
	st := openTestStore(t)
	start := time.Now()

	rawToken, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	sess := auth.Session{
		TokenHash:  hash,
		CreatedAt:  start,
		LastSeenAt: start,
		ExpiresAt:  start.Add(auth.SessionAbsoluteLifetime),
		IP:         "10.20.0.2",
	}
	sessionID, err := st.CreateSession(t.Context(), sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	clock := newFakeClock(start)
	validator := bridge.SessionValidatorFunc(func(ctx context.Context, token string, now time.Time) (bridge.Session, error) {
		s, err := st.GetSessionByTokenHash(ctx, auth.HashToken(token))
		if err != nil {
			return bridge.Session{}, err
		}
		if !s.Valid(now, auth.SessionIdleTimeout) {
			return bridge.Session{}, errors.New("sectest: session invalid")
		}
		return bridge.Session{UserID: "admin", ExpiresAt: s.ExpiresAt}, nil
	})

	cfg := bridge.Config{
		SessionCookieName:  "session",
		AllowedOrigins:     []string{"https://admin.example.com"},
		Validator:          validator,
		Clock:              clock,
		RevalidateInterval: time.Minute,
	}
	handler, err := bridge.NewServer(cfg, func(s *grpc.Server) {
		hs := health.NewServer()
		hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		healthpb.RegisterHealthServer(s, hs)
	})
	if err != nil {
		t.Fatalf("bridge.NewServer: %v", err)
	}
	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)

	conn, err := grpctunnel.DialContext(context.Background(), httpSrv.URL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithHeader("Cookie", "session="+rawToken),
		grpctunnel.WithHeader("Origin", "https://admin.example.com"),
	)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	checkHealth := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		client := healthpb.NewHealthClient(conn)
		resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			return err
		}
		if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			return errors.New("not serving")
		}
		return nil
	}

	if err := checkHealth(); err != nil {
		t.Fatalf("health check before revocation: %v", err)
	}

	// Revoke through the real production path, mid-connection.
	if err := st.RevokeSession(t.Context(), sessionID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	waitUntilFake(t, 2*time.Second, func() bool { return clock.waiterCount() >= 1 })
	clock.Advance(cfg.RevalidateInterval)

	waitUntilFake(t, 3*time.Second, func() bool { return checkHealth() != nil })
}

func waitUntilFake(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
