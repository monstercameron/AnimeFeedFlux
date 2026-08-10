package bridge_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	testCookieName = "session"
	allowedOrigin  = "https://admin.example.com"
	evilOrigin     = "https://admin.example.com.evil.tld"
)

// fakeClock is a hand-rolled fake satisfying bridge.Clock: Now() reads back
// whatever Advance moved it to, and After hands back a channel that only
// ever fires when a test calls Advance past its deadline — never on a
// real-time timer. This is what makes the expiry/revocation tests
// deterministic instead of sleep-based.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	fire time.Time
	ch   chan time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	fire := c.now.Add(d)
	if !fire.After(c.now) {
		ch <- fire
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{fire: fire, ch: ch})
	return ch
}

// Advance moves the clock forward and fires every waiter whose deadline is
// now in the past.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	remaining := c.waiters[:0]
	var fired []fakeWaiter
	for _, w := range c.waiters {
		if !w.fire.After(now) {
			fired = append(fired, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
	for _, w := range fired {
		w.ch <- w.fire
	}
}

func (c *fakeClock) waiterCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// testValidator is a stub bridge.SessionValidator backed by an in-memory
// token table, with a Revoke hook so the revocation test can flip a live
// session invalid without touching its ExpiresAt.
type testValidator struct {
	mu       sync.Mutex
	sessions map[string]bridge.Session
	revoked  map[string]bool
}

func newTestValidator() *testValidator {
	return &testValidator{sessions: map[string]bridge.Session{}, revoked: map[string]bool{}}
}

func (v *testValidator) put(token string, session bridge.Session) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sessions[token] = session
}

func (v *testValidator) revoke(token string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.revoked[token] = true
}

func (v *testValidator) Validate(_ context.Context, token string, now time.Time) (bridge.Session, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session, ok := v.sessions[token]
	if !ok {
		return bridge.Session{}, errors.New("bridge_test: unknown session token")
	}
	if v.revoked[token] {
		return bridge.Session{}, errors.New("bridge_test: session revoked")
	}
	if !now.Before(session.ExpiresAt) {
		return bridge.Session{}, errors.New("bridge_test: session expired")
	}
	return session, nil
}

// registerHealth wires the stock grpc health service so tests have an RPC
// to call without hand-writing protobufs. serviceOpts lets a test attach an
// interceptor (e.g. to capture bridge.SessionFromContext).
func registerHealthServer(t *testing.T) func(*grpc.Server) {
	t.Helper()
	return func(s *grpc.Server) {
		hs := health.NewServer()
		hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		healthpb.RegisterHealthServer(s, hs)
	}
}

func mustNewServer(t *testing.T, cfg bridge.Config, register func(*grpc.Server)) *httptest.Server {
	t.Helper()
	handler, err := bridge.NewServer(cfg, register)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func dialTunnel(t *testing.T, addr string, cookieValue string, origin string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpctunnel.DialContext(context.Background(), addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithHeader("Cookie", testCookieName+"="+cookieValue),
		grpctunnel.WithHeader("Origin", origin),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func checkHealth(ctx context.Context, conn *grpc.ClientConn) error {
	client := healthpb.NewHealthClient(conn)
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return errors.New("bridge_test: health check not serving")
	}
	return nil
}

// waitUntil polls cond every 10ms up to timeout. Used to synchronize on
// goroutine state (the revalidation loop registering its next wait, or a
// connection actually going down) without a library-level hook to await
// directly — the tests still never sleep past what real work requires.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
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

// TestUpgradeWithoutCookie_Unauthorized proves the session check happens
// before any WebSocket upgrade is attempted: a plain HTTP GET carrying no
// session cookie must get a 401 without needing to speak the WebSocket
// handshake at all.
func TestUpgradeWithoutCookie_Unauthorized(t *testing.T) {
	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         newTestValidator(),
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestUpgradeWithBadCookie_Unauthorized covers a cookie that is present but
// does not resolve to a valid session (never issued, or already expired at
// the moment of the upgrade attempt).
func TestUpgradeWithBadCookie_Unauthorized(t *testing.T) {
	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         newTestValidator(), // empty: every token is unknown
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: testCookieName, Value: "not-a-real-token"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestUpgradeWithBadOrigin_Refused is the ticket's named case: an Origin
// that has the allowed origin as a strict prefix, with an attacker-owned
// suffix tacked on, must be refused — not treated as a match.
func TestUpgradeWithBadOrigin_Refused(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))
	wsURL := "ws" + srv.URL[len("http"):]

	header := http.Header{}
	header.Set("Origin", evilOrigin)
	header.Set("Cookie", testCookieName+"=good-token")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("Dial with bad Origin unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("Dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (refused by Origin check)", resp.StatusCode)
	}
}

// TestUpgradeValidCookie_SessionInContext exercises the full path: a valid
// cookie and an exact-matching Origin succeed, and the Session lands in the
// context that reaches an RPC handler — the coupling point internal/rpc's
// future auth interceptor depends on.
func TestUpgradeValidCookie_SessionInContext(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	var (
		mu      sync.Mutex
		got     bridge.Session
		gotOK   bool
		sawCall bool
	)
	captureInterceptor := grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mu.Lock()
		got, gotOK = bridge.SessionFromContext(ctx)
		sawCall = true
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
	if !sawCall {
		t.Fatalf("interceptor never observed an RPC")
	}
	if !gotOK {
		t.Fatalf("SessionFromContext found no session on the RPC context")
	}
	if got.UserID != "cam" {
		t.Fatalf("session.UserID = %q, want %q", got.UserID, "cam")
	}
}

// TestRevalidate_ExpiryClosesSocket is the injected-clock version of the
// "authenticated at 12:01, session expires at 18:00, socket still serving
// at 23:00" bug PLAN.md §4 describes: the clock is advanced past the
// session's ExpiresAt without any real sleep, and the live connection must
// go down as a result.
func TestRevalidate_ExpiryClosesSocket(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)

	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: start.Add(30 * time.Second)})

	cfg := bridge.Config{
		SessionCookieName:  testCookieName,
		AllowedOrigins:     []string{allowedOrigin},
		Validator:          validator,
		Clock:              clock,
		RevalidateInterval: time.Minute, // > 30s ExpiresAt margin: first tick lands past expiry
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)

	// Prove the connection is actually alive (and the session actually
	// valid) before letting the clock move at all.
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	if err := checkHealth(firstCtx, conn); err != nil {
		t.Fatalf("health check before expiry: %v", err)
	}

	// Wait for the revalidation loop to actually be parked on its next
	// tick before advancing, so Advance is guaranteed to reach it.
	waitUntil(t, 2*time.Second, func() bool { return clock.waiterCount() >= 1 })
	clock.Advance(cfg.RevalidateInterval)

	waitUntil(t, 3*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		return checkHealth(ctx, conn) != nil
	})
}

// TestRevalidate_RevocationClosesSocket mirrors the expiry test but flips
// validity via explicit revocation instead of the clock crossing
// ExpiresAt — the two are meant to be indistinguishable to the
// revalidation loop, both routed through the same Validator.Validate call.
func TestRevalidate_RevocationClosesSocket(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)

	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: start.Add(24 * time.Hour)})

	cfg := bridge.Config{
		SessionCookieName:  testCookieName,
		AllowedOrigins:     []string{allowedOrigin},
		Validator:          validator,
		Clock:              clock,
		RevalidateInterval: time.Minute,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	if err := checkHealth(firstCtx, conn); err != nil {
		t.Fatalf("health check before revocation: %v", err)
	}

	validator.revoke("good-token")

	waitUntil(t, 2*time.Second, func() bool { return clock.waiterCount() >= 1 })
	clock.Advance(cfg.RevalidateInterval)

	waitUntil(t, 3*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		return checkHealth(ctx, conn) != nil
	})
}
