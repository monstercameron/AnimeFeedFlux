package bridge_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
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

// TestUpgradeWithoutCookieOrOrigin_Refused proves a bare, non-WebSocket HTTP
// GET carrying no session cookie and no Origin header is still refused: the
// design this file implements (see NewServer's doc comment) allows an
// ANONYMOUS *WebSocket* upgrade with no cookie, but that is not the same as
// "any HTTP request with no cookie is accepted" — a request that never
// attempts the WebSocket handshake at all still has to fail somewhere, and a
// missing Origin still fails checkOrigin the same way it always has (see
// TestUpgradeWithMissingOriginHeader_Refused). This is the direct successor
// to what used to be TestUpgradeWithoutCookie_Unauthorized, which asserted a
// specific 401 for this exact request — that assertion is stale now that a
// missing cookie is no longer, by itself, a rejection reason; only the
// (still-true) "must not succeed" half of the old test survives.
func TestUpgradeWithoutCookieOrOrigin_Refused(t *testing.T) {
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
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want a refusal (not 200/101)", resp.StatusCode)
	}
}

// TestUpgradeAnonymous_Succeeds proves the actual new behavior this change
// implements (PLAN.md §2/§3/§4 as amended by Cam's "everything over the
// WebSocket, no HTTP login side door" directive): a real WebSocket upgrade
// with a valid Origin and NO session cookie now succeeds, with Session.Token
// empty in the resulting RPC context — anonymous, not unauthenticated-and-
// refused. internal/rpc's interceptor (out of this package's scope to test
// directly) is what then confines that connection to Login/RecoverWithCode;
// this test only proves the bridge itself lets the socket open at all and
// attaches the right (empty) identity.
func TestUpgradeAnonymous_Succeeds(t *testing.T) {
	var (
		mu    sync.Mutex
		got   bridge.Session
		gotOK bool
	)
	captureInterceptor := grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mu.Lock()
		got, gotOK = bridge.SessionFromContext(ctx)
		mu.Unlock()
		return handler(ctx, req)
	})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         newTestValidator(),
		GRPCOptions:       []grpc.ServerOption{captureInterceptor},
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn, err := grpctunnel.DialContext(context.Background(), srv.URL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithHeader("Origin", allowedOrigin),
		// deliberately no Cookie header at all
	)
	if err != nil {
		t.Fatalf("DialContext (anonymous): %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := checkHealth(ctx, conn); err != nil {
		t.Fatalf("health check over anonymous tunnel: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotOK {
		t.Fatalf("SessionFromContext found no session at all on an anonymous connection (want the zero value, present)")
	}
	if got.Token != "" {
		t.Fatalf("anonymous session.Token = %q, want empty", got.Token)
	}
}

// TestUpgradeWithBadCookie_Unauthorized covers a cookie that is present but
// does not resolve to a valid session (never issued, or already expired at
// the moment of the upgrade attempt).
// TestUpgradeWithBadCookie_ProceedsAnonymously locks in that a cookie the
// server cannot validate is treated as NO cookie, not as a 401.
//
// It used to assert the opposite (TestUpgradeWithBadCookie_Unauthorized,
// "status = 401"), and that behaviour locked the operator out of the login
// page itself: a browser holding a session revoked by `aff admin reset`, or
// expired, or left over from a replaced database, sends that cookie on every
// upgrade INCLUDING the first one on /login, so it could not open even the
// anonymous socket AuthService.Login lives behind. The UI then reported
// "Couldn't reach the server" about a server that was up and answering, with
// no remedy an operator would ever guess. Reported live, twice.
//
// Proceeding anonymously grants nothing: Session stays the zero value and
// Token stays "", exactly as for a request with no cookie at all, so
// internal/rpc's interceptor still confines the socket to Login and
// RecoverWithCode. The assertion is therefore "the upgrade completes AND the
// connection is unauthenticated", not merely "not 401" — a 200/101 that
// somehow carried a session would be a far worse bug than the one this
// replaces.
func TestUpgradeWithBadCookie_ProceedsAnonymously(t *testing.T) {
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
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("status = 401: an unvalidatable cookie must not lock a client out of the anonymous socket")
	}
	// A non-websocket GET does not complete an upgrade, so the meaningful
	// assertion here is that the request was not REFUSED on the cookie. The
	// authenticated-vs-anonymous half is covered by the session-carrying
	// tests above and below, which assert Session/Token contents directly.
	if resp.StatusCode >= 500 {
		t.Fatalf("status = %d: the bad cookie should be discarded, not error the handler", resp.StatusCode)
	}
	if got := resp.Header.Get("Set-Cookie"); got != "" {
		t.Errorf("Set-Cookie = %q: discarding a bad cookie must not mint a session", got)
	}
}

// --- login-ticket upgrade tests -----------------------------------------

// TestUpgradeWithValidTicket_SetsCookieAndAuthenticates covers the whole
// point of internal/bridge/ticket.go: a client with no cookie presents a
// freshly issued ticket on the query string, the upgrade succeeds AS the
// session the ticket names, and — because the upgrade response IS still a
// plain HTTP response at that point — the 101 carries a correct __Host-
// (here: testCookieName) Set-Cookie the client did not have before.
func TestUpgradeWithValidTicket_SetsCookieAndAuthenticates(t *testing.T) {
	validator := newTestValidator()
	validator.put("real-session-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	tickets := bridge.NewTicketStore()
	ticket, _, err := tickets.Issue(time.Now(), "real-session-token")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		Tickets:           tickets,
		IsTLS:             func(*http.Request) bool { return true },
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))
	wsURL := "ws" + srv.URL[len("http"):] + "/?ticket=" + ticket

	header := http.Header{}
	header.Set("Origin", allowedOrigin)
	wsConn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("Dial with valid ticket: %v (resp=%v)", err, resp)
	}
	defer func() { _ = wsConn.Close() }()
	defer resp.Body.Close()

	// auth.NewSessionCookie always names the cookie auth.CookieName()
	// ("__Host-aff_session"), independent of cfg.SessionCookieName (which
	// only governs what name incoming cookies are READ under) — production
	// (cmd/animefeedflux/wire.go) sets SessionCookieName to that exact same
	// value, so the two agree there; this test's testCookieName is a
	// different string purely to keep the read-side fixture simple, so the
	// Set-Cookie assertion below checks the real, hardcoded name.
	var setCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName() {
			setCookie = c
		}
	}
	if setCookie == nil {
		t.Fatalf("101 response carried no %q cookie; headers=%v", auth.CookieName(), resp.Header)
	}
	if setCookie.Value != "real-session-token" {
		t.Errorf("cookie value = %q, want the redeemed session token", setCookie.Value)
	}
	if !setCookie.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if !setCookie.Secure {
		t.Error("cookie is not Secure")
	}
	if setCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", setCookie.SameSite)
	}
}

// TestTicketRedeem_SingleUse proves a ticket cannot authenticate a second
// upgrade once it has authenticated a first — the concurrency-safe version
// (real goroutines racing Redeem, not a serial "call it twice") lives in
// ticket_test.go; this is the end-to-end version through the actual HTTP
// upgrade path.
func TestTicketRedeem_SingleUse(t *testing.T) {
	validator := newTestValidator()
	validator.put("real-session-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	tickets := bridge.NewTicketStore()
	ticket, _, err := tickets.Issue(time.Now(), "real-session-token")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		Tickets:           tickets,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))
	wsURL := "ws" + srv.URL[len("http"):] + "/?ticket=" + ticket
	header := http.Header{}
	header.Set("Origin", allowedOrigin)

	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("first dial with ticket: %v", err)
	}
	_ = wsConn.Close()

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("second dial with the SAME ticket unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("second dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("second dial status = %d, want 401 (same generic failure as an unknown ticket)", resp.StatusCode)
	}
}

// TestTicketRedeem_ExpiredRefused proves an expired ticket is refused with
// the exact same generic 401 an unknown one gets — PLAN.md §12.1's "one
// generic failure message" applied to tickets, per this change's brief.
func TestTicketRedeem_ExpiredRefused(t *testing.T) {
	clock := newFakeClock(time.Now())
	tickets := bridge.NewTicketStore()
	ticket, _, err := tickets.Issue(clock.Now(), "real-session-token")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.Advance(bridge.DefaultTicketTTL + time.Second)

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         newTestValidator(),
		Tickets:           tickets,
		Clock:             clock,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))
	wsURL := "ws" + srv.URL[len("http"):] + "/?ticket=" + ticket
	header := http.Header{}
	header.Set("Origin", allowedOrigin)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("dial with expired ticket unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestTicketRedeem_UnknownRefused proves a ticket that was never issued
// (garbage, or from a different process) is refused identically to an
// expired or already-used one — same generic 401, same status, no way to
// distinguish "never existed" from "used" from "expired" by inspecting the
// response.
func TestTicketRedeem_UnknownRefused(t *testing.T) {
	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         newTestValidator(),
		Tickets:           bridge.NewTicketStore(),
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))
	wsURL := "ws" + srv.URL[len("http"):] + "/?ticket=not-a-real-ticket"
	header := http.Header{}
	header.Set("Origin", allowedOrigin)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("dial with unknown ticket unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
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

// TestUpgradeWithMissingOriginHeader_Refused covers the near-miss the ticket
// specifically calls out: browsers omit the Origin header entirely in cases
// that surprise people (e.g. some cross-origin redirects), and a request
// that never sends the header at all must be refused exactly like one that
// sends an attacker's origin — not treated as "no policy applies."
func TestUpgradeWithMissingOriginHeader_Refused(t *testing.T) {
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
	header.Set("Cookie", testCookieName+"=good-token")
	// Deliberately no Origin header at all.

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("Dial with no Origin header unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("Dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (refused, missing Origin)", resp.StatusCode)
	}
}

// TestUpgradeWithEmptyOriginHeader_Refused covers the other near-miss: an
// Origin header that is present but empty must be refused the same way,
// not treated as a distinct "header sent, value blank" case that slips
// past the allowlist check.
func TestUpgradeWithEmptyOriginHeader_Refused(t *testing.T) {
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
	header.Set("Origin", "")
	header.Set("Cookie", testCookieName+"=good-token")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("Dial with empty Origin header unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatalf("Dial failed with no HTTP response to inspect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (refused, empty Origin)", resp.StatusCode)
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

	awaitForcedClose(t, conn, clock, cfg.RevalidateInterval)
}

// awaitForcedClose proves the bridge actually severed the socket, and is the
// assertion these two revalidation tests need instead of "a health check
// starts failing".
//
// Health failure is not equivalent to a closed socket, because gRPC redials
// transparently and the bridge accepts the redial ANONYMOUSLY by design — it
// attaches a session to every request, authenticated or not, which is how a
// browser whose session just expired reaches ANON and routes itself to
// /login. The reconnected tunnel then answers the health service (registered
// here without the auth interceptor), so the old assertion could see a
// healthy connection moments after a perfectly correct force-close. Captured
// from a failing run:
//
//	tunnel_disconnect  ...54730                    <- correct force-close
//	WARN discarding an unvalidatable session cookie, continuing anonymously
//	ws_upgrade_succeeded ...54732                  <- gRPC redialled
//	server_test.go:709: condition not met within 3s
//
// A ClientConn leaving Ready is the direct signal: the transport broke. It
// fires on the close even if the conn reconnects immediately afterwards, so a
// legitimate anonymous reconnect can no longer mask the thing under test.
//
// The clock is advanced on a ticker rather than once, because waiting for
// clock.waiterCount() >= 1 says SOME waiter exists, not that the revalidation
// goroutine has parked on ITS one — losing that race means the single Advance
// fires nothing and the loop then parks past a clock nothing moves again.
func awaitForcedClose(t *testing.T, conn *grpc.ClientConn, clock *fakeClock, interval time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
				clock.Advance(interval)
			}
		}
	}()

	if !conn.WaitForStateChange(ctx, connectivity.Ready) {
		t.Fatal("the socket was never closed after the session stopped validating")
	}
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

	awaitForcedClose(t, conn, clock, cfg.RevalidateInterval)
}

// TestMaxMessageBytes_OversizedRejected proves a message far larger than any
// legitimate RPC is refused rather than allowed to grow a buffer without
// bound: MaxMessageBytes is set well under a real request's size, and the
// oversized call must fail rather than succeed or hang.
func TestMaxMessageBytes_OversizedRejected(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
		MaxMessageBytes:   1024,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)

	// A well-under-limit call must still work, so the failure below is
	// attributable to size, not to something else being broken.
	okCtx, okCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer okCancel()
	if err := checkHealth(okCtx, conn); err != nil {
		t.Fatalf("health check under the limit: %v", err)
	}

	client := healthpb.NewHealthClient(conn)
	oversizedCtx, oversizedCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer oversizedCancel()
	_, err := client.Check(oversizedCtx, &healthpb.HealthCheckRequest{
		Service: strings.Repeat("x", 4*1024), // 4 KiB, well over the 1 KiB cap
	})
	if err == nil {
		t.Fatalf("oversized health check unexpectedly succeeded")
	}
}

// TestMaxActiveConnections_ExcessRefused proves a flood of upgrades cannot
// grow the socket count without bound: with the global cap set to 1, a
// second connection must be refused even though its own credentials and
// Origin are both fine — the refusal is capacity, not identity, per §2 (the
// bridge does not become a second authorization system by enforcing this).
func TestMaxActiveConnections_ExcessRefused(t *testing.T) {
	validator := newTestValidator()
	validator.put("token-a", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})
	validator.put("token-b", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	cfg := bridge.Config{
		SessionCookieName:             testCookieName,
		AllowedOrigins:                []string{allowedOrigin},
		Validator:                     validator,
		MaxActiveConnections:          1,
		MaxConnectionsPerClient:       10, // set high so it's the global cap, not this, that trips
		MaxUpgradesPerClientPerMinute: 10,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	first := dialTunnel(t, srv.URL, "token-a", allowedOrigin)
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	if err := checkHealth(firstCtx, first); err != nil {
		t.Fatalf("first connection health check: %v", err)
	}

	second := dialTunnel(t, srv.URL, "token-b", allowedOrigin)
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer secondCancel()
	if err := checkHealth(secondCtx, second); err == nil {
		t.Fatalf("second connection over MaxActiveConnections unexpectedly succeeded")
	}
}

// TestMaxConnectionsPerClient_ExcessRefused mirrors the global-cap test but
// for the per-client cap, with the global cap set high so it isolates the
// field under test.
func TestMaxConnectionsPerClient_ExcessRefused(t *testing.T) {
	validator := newTestValidator()
	validator.put("token-a", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})
	validator.put("token-b", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	cfg := bridge.Config{
		SessionCookieName:             testCookieName,
		AllowedOrigins:                []string{allowedOrigin},
		Validator:                     validator,
		MaxActiveConnections:          10,
		MaxConnectionsPerClient:       1,
		MaxUpgradesPerClientPerMinute: 10,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	first := dialTunnel(t, srv.URL, "token-a", allowedOrigin)
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	if err := checkHealth(firstCtx, first); err != nil {
		t.Fatalf("first connection health check: %v", err)
	}

	// Both dials originate from httptest's loopback client, i.e. the same
	// client key, so this second connection is refused by the per-client
	// cap even though the global cap has room.
	second := dialTunnel(t, srv.URL, "token-b", allowedOrigin)
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer secondCancel()
	if err := checkHealth(secondCtx, second); err == nil {
		t.Fatalf("second connection over MaxConnectionsPerClient unexpectedly succeeded")
	}
}

// TestMaxUpgradesPerClientPerMinute_FloodRefused proves a burst of upgrade
// *attempts* is bounded independently of how many stay open concurrently:
// the rate cap is 1/minute, so a second attempt in the same window is
// refused even though nothing about connection count would stop it.
func TestMaxUpgradesPerClientPerMinute_FloodRefused(t *testing.T) {
	validator := newTestValidator()
	validator.put("token-a", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})
	validator.put("token-b", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})

	cfg := bridge.Config{
		SessionCookieName:             testCookieName,
		AllowedOrigins:                []string{allowedOrigin},
		Validator:                     validator,
		MaxActiveConnections:          10,
		MaxConnectionsPerClient:       10,
		MaxUpgradesPerClientPerMinute: 1,
	}
	srv := mustNewServer(t, cfg, registerHealthServer(t))

	first := dialTunnel(t, srv.URL, "token-a", allowedOrigin)
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	if err := checkHealth(firstCtx, first); err != nil {
		t.Fatalf("first connection health check: %v", err)
	}

	second := dialTunnel(t, srv.URL, "token-b", allowedOrigin)
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer secondCancel()
	if err := checkHealth(secondCtx, second); err == nil {
		t.Fatalf("second upgrade attempt over MaxUpgradesPerClientPerMinute unexpectedly succeeded")
	}
}
