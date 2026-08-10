//go:build js && wasm

// Package wsconn wires the WASM admin shell to the control plane over the
// one WebSocket that carries the whole API (PLAN.md §2's "gRPC over
// WebSocket via GoGRPCBridge"). D0-06 asked for this client "wired to the
// bridge"; D0-09/D0-10 ask for the DISCONNECTED banner's reconnect,
// backoff, and mutation handling to be real, not an afterthought.
//
// Concrete library surface used, since PLAN.md names the dependency only
// generically: github.com/monstercameron/GoGRPCBridge v1.1.1, package
// pkg/grpctunnel. grpctunnel.DialContext (js-build-tagged
// client_wasm.go) opens a *grpc.ClientConn tunneled over a browser
// WebSocket; grpctunnel.WithReconnectPolicy configures grpc-go's own
// ConnectParams.Backoff, so the actual redialing and its delay timing are
// grpc-go's, not hand-rolled here — this package's job is to (a) hand it
// the same curve web/backoff.DefaultPolicy describes and already has
// under test, and (b) translate grpc-go's connectivity.State transitions
// into web/appstate.Event values the shell's state machine understands.
package wsconn

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
)

// DefaultEndpoint is the control-plane WebSocket path this client dials.
//
// CONFIRMED against cmd/animefeedflux/wire.go (buildControlPlane / runAll)
// as of this change: bridge.NewServer's returned http.Handler is installed
// directly as adminSrv.Handler, with no http.ServeMux or other path
// splitting in front of it — the bridge answers every path on the admin
// listener, i.e. it is mounted at the root, not at "/grpc". A previous
// version of this file assumed "/grpc" (matching the path convention used
// by other Flux apps on the same GoGRPCBridge/GWC stack) because at the
// time nothing wired internal/bridge into cmd/animefeedflux's mux at all;
// that assumption is now stale and would have failed to connect silently
// (grpctunnel.DialContext accepts a same-origin path and does not itself
// know which path the server actually answers on) with no useful error
// short of a browser session. "/" is used instead so this client dials
// exactly the path the server actually serves.
const DefaultEndpoint = "/"

// keepaliveInterval/keepaliveTimeout are paired with
// internal/bridge/server.go's defaultKeepaliveEnforcementPolicy (MinTime
// 25s, PermitWithoutStream true) exactly the way that file's own comment
// prescribes for "the admin WASM client": a client ping cadence a few
// seconds above the server's enforcement floor, so ordinary clock skew
// does not itself trip GOAWAY ENHANCE_YOUR_CALM (the "known
// keepalive/GOAWAY flap" both that file and this project's working notes
// call out).
const (
	keepaliveInterval = 30 * time.Second
	keepaliveTimeout  = 20 * time.Second
)

// ErrDisconnected is returned by Guard when the socket is not currently
// Ready. It is a sentinel specifically so callers (page code in later
// waves) can distinguish "refused because disconnected" from any other
// RPC failure and render the right message instead of a generic error.
var ErrDisconnected = errors.New("wsconn: control-plane socket is disconnected")

// Conn holds the tunneled gRPC connection and the typed service clients
// built on it — all six affv1 services PLAN.md §12's pages need
// (web/pages/settings/client.go's SystemClient/AuthClient/FeedClient,
// web/pages/history/client.go's RunsClient/ItemsClient, and
// web/pages/generate/deps.go's Deps all name a subset of these fields'
// types). Every field's value is one of the guarded*Client wrappers in
// clients.go, not the bare affv1.NewXServiceClient(cc) result — see that
// file's header for why every RPC, not just the ones a page remembers to
// route through Guard, needs to refuse while DISCONNECTED. The field
// TYPES are still the plain affv1 interfaces (e.g. affv1.AuthServiceClient)
// so a page can keep depending on that interface, or a subset of it,
// without ever importing this package — the guarded wrapper types
// themselves are unexported and never appear in a page's own signatures.
type Conn struct {
	cc     *grpc.ClientConn
	Auth   affv1.AuthServiceClient
	Feed   affv1.FeedServiceClient
	Item   affv1.ItemServiceClient
	Run    affv1.RunServiceClient
	Sample affv1.SampleServiceClient
	System affv1.SystemServiceClient

	onEvent func(appstate.Event)
}

// Connect dials endpoint (see DefaultEndpoint) over the browser's
// WebSocket transport and starts a background goroutine that watches
// connectivity state for the lifetime of ctx, calling onEvent with
// appstate.EvWSDropped / appstate.EvWSReconnected as the underlying
// grpc.ClientConn's state changes. onEvent may be called from a
// goroutine, not necessarily the "main" one — callers that touch the DOM
// from it must marshal back onto whatever single-threaded event loop
// they use (see web/shell, which does this by construction: js.FuncOf
// callbacks and this callback both only ever run on the browser's single
// JS thread, so no explicit marshaling is actually needed in a
// GOOS=js/wasm build, but the contract is documented here because it
// would matter the moment this code was ever reused outside one).
//
// Connect itself does not block on the handshake completing (grpc-go's
// default dial behavior); watch connectivity state via Ready or onEvent
// to know when it is.
func Connect(ctx context.Context, endpoint string, onEvent func(appstate.Event)) (*Conn, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	reconnect := reconnectConfigFromPolicy(backoff.DefaultPolicy)

	cc, err := grpctunnel.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithReconnectPolicy(reconnect),
		grpctunnel.WithTunnelKeepalive(keepaliveInterval, keepaliveTimeout),
	)
	if err != nil {
		return nil, err
	}

	// c is constructed before its client fields are assigned because each
	// guarded*Client wrapper (clients.go) holds a *Conn back-reference so it
	// can consult c.Ready() on every call — the wrapper cannot be built
	// until c itself exists, and c isn't useful without its clients, so the
	// two steps happen in this order rather than trying to thread cc's
	// readiness through some other channel.
	c := &Conn{cc: cc, onEvent: onEvent}
	c.Auth = guardedAuthClient{conn: c, real: affv1.NewAuthServiceClient(cc)}
	c.Feed = guardedFeedClient{conn: c, real: affv1.NewFeedServiceClient(cc)}
	c.Item = guardedItemClient{conn: c, real: affv1.NewItemServiceClient(cc)}
	c.Run = guardedRunClient{conn: c, real: affv1.NewRunServiceClient(cc)}
	c.Sample = guardedSampleClient{conn: c, real: affv1.NewSampleServiceClient(cc)}
	c.System = guardedSystemClient{conn: c, real: affv1.NewSystemServiceClient(cc)}
	go c.watchConnectivity(ctx)
	return c, nil
}

// watchConnectivity translates grpc-go connectivity.State transitions
// into appstate events. It only emits on the transitions the shell's
// state machine actually models (see web/appstate: EvWSDropped from
// AUTH/KILLED, EvWSReconnected from DISCONNECTED) — every other
// connectivity.State (Idle, Connecting) is an internal detail of getting
// from one of those to the other and is deliberately not surfaced as its
// own appstate.State, matching D-FLOW's five-state list.
func (c *Conn) watchConnectivity(ctx context.Context) {
	state := c.cc.GetState()
	for {
		if !c.cc.WaitForStateChange(ctx, state) {
			return // ctx canceled
		}
		next := c.cc.GetState()
		switch next {
		case connectivity.Ready:
			if state != connectivity.Ready {
				c.emit(appstate.EvWSReconnected)
			}
		case connectivity.TransientFailure, connectivity.Shutdown:
			if state == connectivity.Ready {
				c.emit(appstate.EvWSDropped)
			}
		}
		state = next
	}
}

func (c *Conn) emit(ev appstate.Event) {
	if c.onEvent != nil {
		c.onEvent(ev)
	}
}

// Ready reports whether the socket is currently usable for RPCs.
func (c *Conn) Ready() bool {
	return c.cc != nil && c.cc.GetState() == connectivity.Ready
}

// Guard runs fn only if the socket is Ready, otherwise returns
// ErrDisconnected without attempting the call. D0-10: "queue or refuse
// mutations while DISCONNECTED — never fail silently." This
// implementation refuses rather than queues: with no mutation UI built
// yet (later waves own that), a queue has no defined replay/dedup
// semantics to be correct about, whereas refusing with a distinguishable
// error is unambiguous today and composes with whatever a later wave
// decides to layer on top (e.g. a page-level "retry when reconnected"
// queue built on this same sentinel).
func (c *Conn) Guard(fn func() error) error {
	if !c.Ready() {
		return ErrDisconnected
	}
	return fn()
}

// Close releases the underlying connection and stops the connectivity
// watcher (via its ctx, which callers own and cancel independently).
func (c *Conn) Close() error {
	if c.cc == nil {
		return nil
	}
	return c.cc.Close()
}
