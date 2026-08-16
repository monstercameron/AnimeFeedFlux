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
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// ticketQueryParam is where a reconnecting browser presents its login
// ticket (Login/RecoverWithCode's response — see internal/rpc/auth.go's
// SessionTicketHeader). A query parameter, not a subprotocol or a header,
// for a concrete reason: this project's WASM client dials via
// grpctunnel.DialContext, whose WASM build REJECTS ClientOption(WithHeaders)
// outright ("Headers are not supported in WASM; browser manages websocket
// handshake headers" — client_wasm.go), because a browser's WebSocket
// constructor has no header API at all; Sec-WebSocket-Protocol subprotocol
// negotiation is technically available (WithSubprotocols) but would require
// this package to also select and echo back a matching subprotocol through
// grpctunnel's own BridgeConfig, internals this package deliberately leaves
// alone elsewhere (see NewServer's doc comment on why CheckOrigin, not
// Authorize, does the pre-upgrade auth check). A query parameter needs
// none of that: it arrives on r.URL exactly like the existing Origin/cookie
// checks read r.Header, is read by ServeHTTP itself before grpctunnel's
// handler is ever invoked, and costs nothing beyond a single
// r.URL.Query().Get call.
const ticketQueryParam = "ticket"

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

// defaultMaxMessageBytes bounds a single inbound WebSocket frame, which
// bounds the size of a single gRPC message this tunnel accepts. This is an
// admin control plane, not a file upload endpoint: legitimate traffic is
// recipe text, sampled-generation deltas, and rendered feed XML/JSON
// previews — all comfortably under a megabyte in normal use. 8 MiB is
// generous headroom above that (a pathological sample with many large
// items, say) while staying well under grpctunnel's own 16 MiB default, so
// a client that sends something far larger than any legitimate RPC is
// refused instead of allowed to allocate an ever-growing buffer on the
// 2 GB box. Set explicitly rather than left to the library default so the
// number is visible and tested here, not implicit.
const defaultMaxMessageBytes int64 = 8 << 20

// defaultMaxActiveConnections caps total concurrent bridge sockets across
// every client. Single admin, a handful of browser tabs/devices at most —
// 16 is generous slack for that usage (including overlap during a
// reconnect) while still bounding a flood of upgrades from consuming
// unbounded per-connection memory (goroutines, websocket buffers, gRPC
// transport state) before the process notices anything is wrong.
const defaultMaxActiveConnections = 16

// defaultMaxConnectionsPerClient caps concurrent sockets from one remote
// address. A handful of tabs/devices behind one NAT/IP is normal; far more
// than that from a single peer is not legitimate admin usage.
const defaultMaxConnectionsPerClient = 6

// defaultMaxUpgradesPerClientPerMinute caps upgrade *attempts* (not just
// successful ones) from one client per minute. Generous enough to absorb a
// reconnect storm — e.g. the revalidation loop force-closing a socket right
// as a session is expiring, or a flaky network retrying quickly — while
// still bounding a client that just keeps re-attempting the handshake.
const defaultMaxUpgradesPerClientPerMinute = 30

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
	// Tickets redeems the single-use login tickets AuthServer.Login/
	// RecoverWithCode mint (internal/rpc/auth.go, internal/bridge/ticket.go)
	// so an anonymous upgrade that presents one can become an authenticated
	// one, with the real session cookie set on the 101 response. Optional:
	// nil disables ticket-based upgrades entirely (a ticket query parameter
	// is then simply ignored and the connection stays anonymous), which is
	// the safe degraded state, not a config error — every test that only
	// cares about the cookie or anonymous-upgrade paths need not construct
	// one.
	Tickets *TicketStore
	// IsTLS decides the Secure cookie attribute for the cookie a ticket
	// redemption sets. Nil uses defaultIsTLS (tls.go) — see that function's
	// doc comment for the dev-vs-production reasoning. Tests override this
	// to simulate a TLS-terminating proxy without standing up real TLS.
	IsTLS func(r *http.Request) bool
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
	// MaxMessageBytes bounds a single inbound WebSocket frame (see
	// defaultMaxMessageBytes for the reasoning). Zero uses the default;
	// negative is a config error.
	MaxMessageBytes int64
	// MaxActiveConnections caps total concurrent bridge sockets across all
	// clients (see defaultMaxActiveConnections). Zero uses the default;
	// negative is a config error. This is a hard cap, not an authorization
	// decision — a client refused here has not been evaluated for identity
	// at all, the socket is simply full.
	MaxActiveConnections int
	// MaxConnectionsPerClient caps concurrent bridge sockets from one
	// remote-address client (see defaultMaxConnectionsPerClient). Zero uses
	// the default; negative is a config error.
	MaxConnectionsPerClient int
	// MaxUpgradesPerClientPerMinute caps upgrade attempts (successful or
	// not) from one client per minute (see
	// defaultMaxUpgradesPerClientPerMinute). Zero uses the default;
	// negative is a config error.
	MaxUpgradesPerClientPerMinute int
	// GRPCOptions are appended after the keepalive options when building
	// the grpc.Server — this is where a caller chains internal/rpc's
	// per-RPC auth interceptor (authSrv.UnaryInterceptor()/
	// StreamInterceptor()). That interceptor reads the raw session token
	// back out via bridge.SessionFromContext(ctx).Token, which ServeHTTP
	// populates unconditionally (see Session.Token's doc comment) — no
	// additional GRPCOptions entry or HTTP middleware is needed to make the
	// token reach it. Optional.
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
	if c.MaxMessageBytes < 0 {
		return fmt.Errorf("bridge: MaxMessageBytes must be >= 0")
	}
	if c.MaxActiveConnections < 0 {
		return fmt.Errorf("bridge: MaxActiveConnections must be >= 0")
	}
	if c.MaxConnectionsPerClient < 0 {
		return fmt.Errorf("bridge: MaxConnectionsPerClient must be >= 0")
	}
	if c.MaxUpgradesPerClientPerMinute < 0 {
		return fmt.Errorf("bridge: MaxUpgradesPerClientPerMinute must be >= 0")
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
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
	if c.MaxActiveConnections == 0 {
		c.MaxActiveConnections = defaultMaxActiveConnections
	}
	if c.MaxConnectionsPerClient == 0 {
		c.MaxConnectionsPerClient = defaultMaxConnectionsPerClient
	}
	if c.MaxUpgradesPerClientPerMinute == 0 {
		c.MaxUpgradesPerClientPerMinute = defaultMaxUpgradesPerClientPerMinute
	}
	return c
}

// isTLS returns the configured Secure-cookie decision function, or
// defaultIsTLS if none was given.
func (c Config) isTLS() func(r *http.Request) bool {
	if c.IsTLS != nil {
		return c.IsTLS
	}
	return defaultIsTLS
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
	isTLS          func(r *http.Request) bool
}

// NewServer builds the gRPC server, lets register attach services to it,
// and exposes it over a WebSocket endpoint via GoGRPCBridge (grpctunnel).
//
// The upgrade gate is NOT "cookie or 401" anymore — that was the bug this
// package's login flow was built around and had to be undone (see the
// package doc comment above). ServeHTTP now resolves one of three outcomes,
// in this priority order, before ever calling grpctunnel's handler:
//
//  1. A valid session cookie: authenticate normally, exactly as before.
//  2. No cookie, but a valid single-use login TICKET on the query string
//     (Login/RecoverWithCode's response — internal/rpc/auth.go's
//     SessionTicketHeader, redeemed here via Config.Tickets): authenticate
//     as the session the ticket names, consume the ticket (it cannot be
//     used again — internal/bridge/ticket.go), and — because this upgrade
//     IS an ordinary HTTP request/response before it becomes a WebSocket —
//     set the real __Host- session cookie on THIS response. A WebSocket
//     cannot set a cookie mid-stream, which is exactly why this has to
//     happen here, at the one point where the connection is still plain
//     HTTP.
//  3. Neither: the socket opens ANONYMOUS. Session carries the zero value
//     (no token), which reaches internal/rpc's interceptor the same way an
//     authenticated Session's token does (bridge.SessionFromContext), and
//     that interceptor's default-deny allowlist (noSessionMethods) is what
//     keeps an anonymous connection confined to Login/RecoverWithCode —
//     this package does not itself decide which RPCs an anonymous socket
//     may reach, on purpose: PLAN.md §2's layering is "the bridge
//     transports, internal/rpc decides", and duplicating that decision here
//     would be a second place for the allowlist to drift out of sync.
//
// Auth happens in two places, deliberately not delegated to
// grpctunnel.BridgeConfig.Authorize:
//  1. The three-way resolution above runs in this handler's ServeHTTP,
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
//     login screen reappearing (PLAN.md §4), not a silent hang. This loop
//     only ever runs for an AUTHENTICATED socket (outcome 1 or 2 above) — an
//     anonymous socket has no token to revalidate, and is already confined
//     to almost nothing by the interceptor, so there is nothing this loop
//     would protect against by also polling it.
//
// This background loop is a coarse, connection-level backstop bounded by
// RevalidateInterval — it is NOT what SEC-41 (a session revoked mid-connection
// must be refused on the already-open stream) relies on for its guarantee.
// The tight guarantee comes from step 1's Session.Token riding into every
// single RPC context (see Session.Token's doc comment): internal/rpc's
// interceptor re-derives validity from current store state on every call
// using that token, so a revocation is caught on the very next RPC, not on
// the next RevalidateInterval tick.
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

	s := &server{cfg: cfg, allowedOrigins: cfg.normalizedOrigins(), isTLS: cfg.isTLS()}

	bridgeHandler, err := grpctunnel.BuildBridgeHandler(grpcServer, grpctunnel.BridgeConfig{
		CheckOrigin: s.checkOrigin,
		// Backpressure and limits (§4/§17 harden the bridge, not just the
		// upgrade auth path): ReadLimitBytes bounds a single inbound
		// message so an oversized RPC can't grow a buffer without limit;
		// MaxActiveConnections/MaxConnectionsPerClient/
		// MaxUpgradesPerClientPerMinute bound how many sockets — and how
		// fast new ones can be opened — before a flood of upgrades starts
		// consuming memory. All four are refused at or before the upgrade
		// (429/no-socket), which is the same "refuse before the socket
		// exists" property this package already gives the auth checks —
		// see checkOrigin's and ServeHTTP's comments. None of these are
		// authorization decisions (§2): a client tripping a limit has not
		// been evaluated for identity, there is simply no room.
		ReadLimitBytes:                cfg.MaxMessageBytes,
		MaxActiveConnections:          cfg.MaxActiveConnections,
		MaxConnectionsPerClient:       cfg.MaxConnectionsPerClient,
		MaxUpgradesPerClientPerMinute: cfg.MaxUpgradesPerClientPerMinute,
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
		//
		// Keepalive finding (re-verified 2026-08-10, after Session.Token
		// propagation landed): Session.Token propagation does NOT lift the
		// native-transport tradeoff above — it changes nothing about it,
		// because the constraint is about *which context* the gRPC
		// transport is served from, not about what the session carries.
		// Native transport still severs the request context (and therefore
		// still loses the session) regardless of Session.Token existing, so
		// grpc-go's KeepaliveEnforcementPolicy/KeepaliveParams remain inert
		// under this handler-based path exactly as PLAN.md §3 records.
		//
		// A genuinely new fact, though: grpctunnel v1.1.1 (the version
		// actually pinned — PLAN.md §3 predates this) has its OWN
		// websocket-level keepalive, entirely separate from grpc-go's
		// EnforcementPolicy and NOT gated by ShouldUseNativeGRPCTransport —
		// see BridgeConfig.PingInterval/IdleTimeout. Left unset (as here),
		// it defaults to a 30s server-initiated WebSocket ping and a 120s
		// read deadline reset on each pong, applied directly to the
		// *websocket.Conn before either transport mode is reached. That is
		// a different mechanism than the one §3 discusses (it detects a
		// silently-dead peer — laptop sleep, a NAT mapping that expired —
		// rather than policing excessive client pings on an idle stream),
		// but it means "a peer that vanished without a clean close pins a
		// connection slot forever" is NOT an open problem here: it is
		// already handled, just not by the mechanism PLAN.md §3 named.
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

// ServeHTTP resolves the upgrade's identity (cookie, ticket, or anonymous —
// see NewServer's doc comment for the three-way priority) before any
// protocol switch, then hands off to the bridge handler with the resulting
// Session attached to the request context and, for an authenticated
// connection, a background revalidation loop watching it.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := s.cfg.Clock.Now()

	var (
		session       Session
		rawToken      string
		upgradeHeader http.Header // set only on a successful ticket redemption
	)

	// ORDER MATTERS: ticket first, then cookie, then anonymous.
	//
	// A ticket is only ever on the URL for the one reconnect that
	// immediately follows a successful Login/RecoverWithCode, so it is both
	// the fresher and the stronger assertion. Checking the cookie first is
	// what a `switch` reads most naturally and it is wrong here: Go cases do
	// not fall through, so an operator arriving with a STALE cookie AND a
	// valid ticket had the cookie discarded (correctly) and then the ticket
	// ignored (not correctly) — the reconnect landed anonymous and they
	// bounced straight back to /login having just authenticated. Caught by
	// the stale-cookie browser test after the first version of this fix made
	// the socket connect but left login still failing.
	switch {
	case s.cfg.Tickets != nil && r.URL.Query().Get(ticketQueryParam) != "":
		raw, ok := s.cfg.Tickets.Redeem(now, r.URL.Query().Get(ticketQueryParam))
		if !ok {
			// Unknown, already-used, and expired all collapse to the exact
			// same response — an enumeration oracle here (PLAN.md §12.1's
			// "one generic failure message" applies to a ticket exactly as
			// it applies to a password) would let a client distinguish "this
			// ticket never existed" from "this ticket was already redeemed
			// by someone else," which is precisely the kind of signal a
			// stolen or guessed ticket attempt should not get.
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		// The ticket named a real session at Issue time, but that session
		// could have been revoked or expired in the seconds since — re-run
		// the exact same Validator check a cookie gets, rather than trusting
		// the ticket's mere existence as proof the session is still good.
		sess, err := s.cfg.Validator.Validate(r.Context(), raw, now)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		session, rawToken = sess, raw
		upgradeHeader = s.sessionCookieHeader(r, raw, sess)

	case hasCookie(r, s.cfg.SessionCookieName):
		cookie, _ := r.Cookie(s.cfg.SessionCookieName)
		sess, err := s.cfg.Validator.Validate(r.Context(), cookie.Value, now)
		if err != nil {
			// A cookie that fails validation is treated as NO cookie — an
			// anonymous upgrade — not as a 401.
			//
			// This used to return 401 here, and it locked the operator out
			// of the login page itself. A stale cookie (expired, revoked by
			// `aff admin reset` or by ChangePassword's revoke-all, or left
			// over from a database that has since been replaced) is sent by
			// the browser on EVERY upgrade attempt, including the very
			// first one on /login. So the client could not open even the
			// anonymous socket that AuthService.Login lives behind, and the
			// UI reported "Couldn't reach the server" about a server that
			// was up and answering — with no way out except clearing site
			// data, which no operator is going to guess. Reported live
			// twice, right after an `aff admin reset` revoked the session
			// this very browser was holding.
			//
			// Falling through grants nothing: `session` stays the zero
			// value and `rawToken` stays "", exactly as in the `default`
			// branch below, so internal/rpc's interceptor still confines
			// this socket to Login/RecoverWithCode. A failed validation
			// carries no authority, so treating it as absent is the honest
			// reading — and it is strictly less permissive than any
			// alternative, because the only other option that unblocks the
			// operator would be trusting the cookie.
			//
			// 401 remains correct for the ticket branch below: a ticket is
			// only ever presented by a client that just authenticated and
			// is asserting it has a session, so a bad one is a real
			// failure, not a leftover.
			// obs's context handler picks up request_id/trace_id here, so
			// this lands correlated with the request that produced it
			// rather than as a bare line.
			slog.WarnContext(r.Context(), "bridge: discarding an unvalidatable session cookie, continuing anonymously",
				"remote_addr", r.RemoteAddr, "reason", err.Error())
			break
		}
		session, rawToken = sess, cookie.Value

	default:
		// Anonymous: Session stays the zero value, Token stays "". This
		// upgrade is allowed to proceed — internal/rpc's interceptor is what
		// confines it to Login/RecoverWithCode, not this package (see
		// NewServer's doc comment).
	}

	// Set unconditionally, overriding anything a Validator implementation
	// might have put in Token: this is the one place in the whole request
	// lifecycle that has the raw token value (cookie- or ticket-derived) in
	// hand, so it is also the only place allowed to decide what
	// Session.Token is. Doing this here, rather than documenting it as
	// something every Validator must remember to do, is what makes "wire up
	// a bridge that authenticates but forwards no token" impossible rather
	// than merely discouraged. For an anonymous upgrade rawToken is "", so
	// Session.Token is "" too — sessionTokenFromContext (internal/rpc/
	// interceptor.go) then correctly reports no token present.
	session.Token = rawToken
	// Set on the same unconditional terms and for the same reason as Token
	// above: this handler is the only place in the request lifecycle holding
	// the *http.Request, so it is the only place that can see the forwarding
	// headers at all. By the time a call reaches an RPC handler the HTTP
	// request is gone and peer.FromContext reports nginx. Anonymous upgrades
	// get it too — a failed login attempt is exactly the case where knowing
	// the origin matters most, and an anonymous socket is the only kind a
	// pre-login attempt can arrive on.
	session.ClientIP = clientIPFromRequest(r)

	ctx := WithSession(r.Context(), session)
	r = r.WithContext(ctx)

	capture := newHijackCapture(w)
	if upgradeHeader != nil {
		capture.SetUpgradeResponseHeader(upgradeHeader)
	}
	stop := make(chan struct{})
	if rawToken != "" {
		// Only an authenticated socket has anything to revalidate — see the
		// package/NewServer doc comments for why an anonymous one does not
		// need this loop.
		go s.revalidate(ctx, rawToken, capture, stop)
	}
	defer close(stop)

	s.bridgeHandler.ServeHTTP(capture, r)
}

// sessionCookieHeader builds the Set-Cookie header a successful ticket
// redemption must return.
//
// This IS the one place a __Host- session cookie can be set for a browser
// that arrived with none: the upgrade's 101 response is the only response
// this connection will ever get. auth.NewSessionCookie applies the same
// __Host-/Secure/HttpOnly/SameSite=Strict/no-Domain construction PLAN.md §4
// requires everywhere else this project sets this cookie.
//
// The result does NOT go through w.Header()/http.SetCookie(w, ...), even
// though the upgrade is still nominally a plain HTTP request/response at
// that point — gorilla/websocket's Upgrade, which grpctunnel calls with no
// header hook of its own, hijacks the connection and hand-writes the entire
// 101 response as raw bytes straight to the net.Conn, never consulting
// w.Header() at all. A Set-Cookie set the "normal" way there would compile,
// look correct, and silently never reach the client — verified directly
// against this project's pinned grpctunnel/gorilla versions. Instead the
// caller registers this header on the hijackCapture and headerInjectingConn
// splices it into the raw upgrade response bytes — see hijack.go.
func (s *server) sessionCookieHeader(r *http.Request, raw string, sess Session) http.Header {
	cookieOut := auth.NewSessionCookie(raw, sess.ExpiresAt)
	// auth.NewSessionCookie hardcodes Secure=true; isTLS(r) downgrades it
	// for a non-TLS-terminating loopback/dev context — see defaultIsTLS's
	// own doc comment.
	cookieOut.Secure = s.isTLS(r)
	return http.Header{"Set-Cookie": []string{cookieOut.String()}}
}

// clientIPFromRequest resolves the real client address for r, for Session.ClientIP.
//
// X-Real-IP ONLY, deliberately not X-Forwarded-For. The two are not
// interchangeable here: X-Forwarded-For is a client-appendable LIST, and
// nginx's $proxy_add_x_forwarded_for appends $remote_addr to whatever the
// client already sent — so an attacker who sends `X-Forwarded-For: 1.2.3.4`
// gets `1.2.3.4, <their real ip>` forwarded, and any parser that reads the
// FIRST entry (the conventional one) reads a value they chose. That would
// hand an attacker control of their own backoff bucket, which is strictly
// worse than the single shared bucket this is fixing — they could rotate the
// header per request and never be rate-limited at all. nginx sets X-Real-IP
// from $remote_addr with no append, so it is exactly one hop of information
// and cannot be influenced by the client.
//
// Trusting a forwarded header at all is only sound because nothing reaches
// this listener except through nginx: compose binds the admin port to
// 127.0.0.1 (deploy/compose.yaml), so there is no path from the internet to
// this handler that skips the proxy and could therefore forge the header. If
// that ever stops being true, this must become a trusted-proxy check rather
// than an unconditional read.
//
// The value is validated as an IP literal before use. An unparseable header
// is discarded rather than passed through, so nothing attacker-shaped can
// reach a log field or a map key — a name is not an address, and a map keyed
// on arbitrary header text is an unbounded-cardinality problem the backoff
// sweep should not have to think about.
func clientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	raw := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if raw == "" {
		return ""
	}
	if net.ParseIP(raw) == nil {
		return ""
	}
	return raw
}

// hasCookie reports whether r carries a non-empty cookie named name, without
// the caller having to juggle http.ErrNoCookie itself at every call site.
func hasCookie(r *http.Request, name string) bool {
	cookie, err := r.Cookie(name)
	return err == nil && cookie.Value != ""
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
