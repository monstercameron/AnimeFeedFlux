// Package bridge implements AnimeFeedFlux's control-plane transport: gRPC
// over WebSocket via GoGRPCBridge (github.com/monstercameron/GoGRPCBridge,
// pkg/grpctunnel), fronted by the session and Origin checks PLAN.md §4
// requires before the protocol switch, plus periodic revalidation of the
// session on an already-open socket.
//
// The library API differs from what PLAN.md §2/§4 assumed when it was
// written (it names "GoGRPCBridge" generically); the concrete surface used
// here is github.com/monstercameron/GoGRPCBridge v1.1.1, package
// pkg/grpctunnel — grpctunnel.BuildBridgeHandler builds the websocket
// http.Handler, and grpctunnel.BridgeConfig.CheckOrigin/Authorize are the
// hooks this package layers its own logic in front of. See NewServer's
// comments for where and why this package does its own thing instead of
// using BridgeConfig.Authorize directly.
package bridge

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// defaultRevalidateInterval bounds how stale a live socket's authorization
// can get before the next check. It only needs to be comfortably shorter
// than the session's absolute (12h) and idle (60m) lifetimes (PLAN.md §4);
// it does not need to be fast, since a closed socket just makes the client
// reconnect and re-authenticate immediately.
const defaultRevalidateInterval = 60 * time.Second

// defaultKeepaliveEnforcementPolicy pairs the server's tolerance for
// unsolicited client keepalive pings with grpctunnel's client-side default
// (30s ping interval, PermitWithoutStream: true — see
// grpctunnel.ApplyTunnelKeepalivePolicy / WithTunnelKeepalive, which the
// admin WASM client is expected to use so idle sockets between generation
// runs are still detected as alive-or-dead).
//
// grpc-go's own zero-value keepalive.EnforcementPolicy is MinTime: 5
// minutes, PermitWithoutStream: false — it assumes a ping only ever
// accompanies an active stream, and anything more frequent than that on an
// idle connection is treated as abuse: the server sends GOAWAY
// ENHANCE_YOUR_CALM and the tunnel drops. That is the "known keepalive/GOAWAY
// flap" PLAN.md §3 calls out. MinTime is set a few seconds below the
// client's ping interval, not equal to it, so ordinary clock skew between
// client and server does not itself trip the enforcement.
var defaultKeepaliveEnforcementPolicy = keepalive.EnforcementPolicy{
	MinTime:             25 * time.Second,
	PermitWithoutStream: true,
}

// defaultKeepaliveServerParams mirrors the client's cadence for the
// server's own keepalive pings, so a peer that vanished without a clean
// close (laptop sleep, a NAT mapping that silently expired) is reclaimed
// instead of pinning a connection slot forever. This only has an effect
// under the native gRPC transport — see the ShouldUseNativeGRPCTransport
// comment in NewServer.
var defaultKeepaliveServerParams = keepalive.ServerParameters{
	Time:    30 * time.Second,
	Timeout: 20 * time.Second,
}

// Config configures NewServer.
type Config struct {
	// SessionCookieName is the cookie carrying the opaque session token,
	// e.g. "__Host-session" per PLAN.md §4. Required.
	SessionCookieName string
	// AllowedOrigins is the exact-match allowlist for the WebSocket
	// handshake's Origin header: "https://admin.anime.earlcameron.com",
	// not a prefix or suffix pattern. Required, non-empty.
	AllowedOrigins []string
	// Validator checks the cookie's token against session store state.
	// Required.
	Validator SessionValidator
	// Clock abstracts time for expiry and revalidation-loop scheduling.
	// Defaults to RealClock{}; tests inject a fake to avoid sleeping.
	Clock Clock
	// RevalidateInterval sets how often a live socket rechecks its
	// session. Defaults to defaultRevalidateInterval.
	RevalidateInterval time.Duration
	// KeepaliveEnforcementPolicy overrides the paired server policy (see
	// defaultKeepaliveEnforcementPolicy). Nil uses the default.
	KeepaliveEnforcementPolicy *keepalive.EnforcementPolicy
	// KeepaliveServerParams overrides the server's own keepalive cadence
	// (see defaultKeepaliveServerParams). Nil uses the default.
	KeepaliveServerParams *keepalive.ServerParameters
	// GRPCOptions are appended after the keepalive options when building
	// the grpc.Server, e.g. for internal/rpc's auth interceptor once it
	// exists. Optional.
	GRPCOptions []grpc.ServerOption
}

func (c Config) validate() error {
	if strings.TrimSpace(c.SessionCookieName) == "" {
		return fmt.Errorf("bridge: SessionCookieName is required")
	}
	if len(c.AllowedOrigins) == 0 {
		return fmt.Errorf("bridge: AllowedOrigins must be non-empty")
	}
	for _, origin := range c.AllowedOrigins {
		if strings.TrimSpace(origin) == "" {
			return fmt.Errorf("bridge: AllowedOrigins entries must not be blank")
		}
	}
	if c.Validator == nil {
		return fmt.Errorf("bridge: Validator is required")
	}
	if c.RevalidateInterval < 0 {
		return fmt.Errorf("bridge: RevalidateInterval must be >= 0")
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.Clock == nil {
		c.Clock = RealClock{}
	}
	if c.RevalidateInterval == 0 {
		c.RevalidateInterval = defaultRevalidateInterval
	}
	if c.KeepaliveEnforcementPolicy == nil {
		policy := defaultKeepaliveEnforcementPolicy
		c.KeepaliveEnforcementPolicy = &policy
	}
	if c.KeepaliveServerParams == nil {
		params := defaultKeepaliveServerParams
		c.KeepaliveServerParams = &params
	}
	return c
}

// normalizedOrigins returns AllowedOrigins as an exact-match set, each
// entry trimmed of a trailing slash so "https://host" and "https://host/"
// are treated the same way browsers treat them.
func (c Config) normalizedOrigins() map[string]struct{} {
	origins := make(map[string]struct{}, len(c.AllowedOrigins))
	for _, origin := range c.AllowedOrigins {
		origins[strings.TrimSuffix(strings.TrimSpace(origin), "/")] = struct{}{}
	}
	return origins
}

// server is the http.Handler NewServer returns.
type server struct {
	cfg            Config
	allowedOrigins map[string]struct{}
	bridgeHandler  http.Handler
}

// NewServer builds the gRPC server, lets register attach services to it,
// and exposes it over a WebSocket endpoint via GoGRPCBridge (grpctunnel).
//
// Auth happens in two places, deliberately not delegated to
// grpctunnel.BridgeConfig.Authorize:
//  1. Session cookie + Origin are checked in this handler's ServeHTTP,
//     before grpctunnel ever sees the request, so a rejection is a plain
//     401 written by us. BridgeConfig.Authorize exists for exactly this
//     kind of pre-upgrade check, but a non-nil error from it always
//     produces 403 Forbidden — PLAN.md §4/the ticket calls for 401 on a
//     missing/invalid session specifically, so that check is done here
//     instead, before grpctunnel.BuildBridgeHandler's handler is ever
//     invoked. (grpctunnel's own CheckOrigin hook IS used, below, since
//     its failure mode — refusing the upgrade — doesn't need a specific
//     status code from this ticket, and it runs at exactly the same "before
//     switching protocols" point.)
//  2. Once the socket is open, a background loop re-validates the session
//     against Config.Clock/Validator every RevalidateInterval and force-closes
//     the underlying connection (via a hijacked-net.Conn capture — see
//     hijack.go, and its doc comment for why that indirection exists) the
//     moment the session stops validating. The client then reconnects, hits
//     step 1 again, and gets 401 — the intended visible outcome is the
//     login screen reappearing (PLAN.md §4), not a silent hang.
func NewServer(cfg Config, register func(*grpc.Server)) (http.Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()

	grpcOpts := make([]grpc.ServerOption, 0, len(cfg.GRPCOptions)+2)
	grpcOpts = append(grpcOpts,
		grpc.KeepaliveEnforcementPolicy(*cfg.KeepaliveEnforcementPolicy),
		grpc.KeepaliveParams(*cfg.KeepaliveServerParams),
	)
	grpcOpts = append(grpcOpts, cfg.GRPCOptions...)
	grpcServer := grpc.NewServer(grpcOpts...)
	if register != nil {
		register(grpcServer)
	}

	s := &server{cfg: cfg, allowedOrigins: cfg.normalizedOrigins()}

	bridgeHandler, err := grpctunnel.BuildBridgeHandler(grpcServer, grpctunnel.BridgeConfig{
		CheckOrigin: s.checkOrigin,
		// Deliberately NOT ShouldUseNativeGRPCTransport, and this needs
		// explaining because native mode looks like the obviously-correct
		// choice (it's what makes KeepaliveEnforcementPolicy/
		// KeepaliveServerParams below actually run — see those vars'
		// comments). Measured directly against this package's own tests:
		// in native mode grpcServer.Serve(nativeListener) drives gRPC's
		// transport from a bare net.Conn with a listener-level base
		// context, completely disconnected from the *http.Request this
		// handler validated the session against — the Session this
		// ServeHTTP attaches via WithSession(r.Context(), ...) never
		// reaches the RPC handlers at all under native mode
		// (TestUpgradeValidCookie_SessionInContext fails against it).
		// Non-native mode calls http2Server.ServeConn with
		// ServeConnOpts.Context set to that same session-bearing context,
		// and golang.org/x/net/http2 derives every per-request context
		// from it, so the session survives into every RPC — which is a
		// hard requirement here (internal/rpc's interceptor reads it back
		// via SessionFromContext) and outranks native transport's
		// keepalive-enforcement benefit. Net effect: the
		// KeepaliveEnforcementPolicy/KeepaliveServerParams configured below
		// are set correctly and left in place (see their comments for why
		// the pairing matters), but grpc-go's keepalive-enforcement engine
		// lives in its own transport package and is simply not consulted
		// under this handler-based serving path — so today they are inert.
		// That statement is itself useful: it means the GOAWAY flap
		// PLAN.md §3 warns about cannot reproduce in the current
		// configuration (x/net/http2's generic server has no equivalent
		// "ping abuse" enforcement), and it flags exactly what would need
		// to change (moving to native transport) if that ever needs to be
		// load-bearing rather than documentation.
	})
	if err != nil {
		return nil, err
	}
	s.bridgeHandler = bridgeHandler
	return s, nil
}

// checkOrigin exact-matches the Origin header against the configured
// allowlist. No wildcarding, no prefix/suffix matching: an allowlist entry
// of "https://app.example.com" must not match
// "https://app.example.com.evil.tld" (a real string, not a subdomain of
// app.example.com — the browser sends the Origin of the *attacking* page
// here, not of the cookie's owner) and must not match a scheme or port
// mismatch either. A missing Origin header is refused rather than allowed:
// this is the admin control plane, and every legitimate client (the WASM
// admin app) is a browser context that always sends one.
func (s *server) checkOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSuffix(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		return false
	}
	_, ok := s.allowedOrigins[origin]
	return ok
}

// ServeHTTP validates the session cookie before any protocol switch, then
// hands off to the bridge handler with the validated Session attached to
// the request context and a background revalidation loop watching the
// resulting connection.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(s.cfg.SessionCookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	now := s.cfg.Clock.Now()
	session, err := s.cfg.Validator.Validate(r.Context(), cookie.Value, now)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	ctx := WithSession(r.Context(), session)
	r = r.WithContext(ctx)

	capture := newHijackCapture(w)
	stop := make(chan struct{})
	go s.revalidate(ctx, cookie.Value, capture, stop)
	defer close(stop)

	s.bridgeHandler.ServeHTTP(capture, r)
}

// revalidate re-checks the session on Config.RevalidateInterval ticks
// (driven by Config.Clock so tests can advance a fake clock instead of
// sleeping) and force-closes the socket the moment the session no longer
// validates — expired or revoked, Validator doesn't distinguish and neither
// does this loop. stop is closed by ServeHTTP once the bridge handler
// returns (the socket already closed on its own), which both ends this
// goroutine and unblocks CloseWhenReady if it never got to see a Hijack.
func (s *server) revalidate(ctx context.Context, token string, capture *hijackCapture, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case now := <-s.cfg.Clock.After(s.cfg.RevalidateInterval):
			if _, err := s.cfg.Validator.Validate(ctx, token, now); err != nil {
				capture.CloseWhenReady(stop)
				return
			}
		}
	}
}
