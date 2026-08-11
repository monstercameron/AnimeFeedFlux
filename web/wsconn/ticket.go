//go:build js && wasm

// ticket.go is the client half of the login-ticket mechanism internal/bridge/
// ticket.go and internal/rpc/auth.go's SessionTicketHeader implement server-
// side. See that pair's doc comments for the full design; the short version
// from this side: web/pages/auth's Login call comes back with a single-use
// ticket in a gRPC response header (never the raw session token — that must
// never reach WASM), and CompleteLogin below is what starts turning that
// ticket into an authenticated connection.
//
// # Why this reloads instead of hot-swapping the existing connection
//
// grpctunnel's WASM dialer bakes the target WebSocket URL (and therefore
// this connection's query string) into the grpc.ClientConn at Dial time
// (client_wasm.go's BuildTunnelConn passes a FIXED tunnelURL into
// dialer.NewWithConfig, consulted on every redial grpc-go's own reconnect
// logic performs) — there is no supported way to hand an already-dialed
// *grpc.ClientConn a different URL for its next attempt. So the existing
// anonymous connection can never itself become the authenticated one; a
// second, differently-addressed connection is unavoidable.
//
// # Why a reload, and why the reload's own boot dial carries the ticket
// (not a Set-Cookie)
//
// An earlier version of this file dialed a short-lived SEPARATE connection
// purely to redeem the ticket (causing the server to set a __Host- session
// cookie on that connection's 101 response — internal/bridge/server.go),
// closed it, and reloaded so the reload's fresh wsconn.Connect(...,
// DefaultEndpoint, ...) would pick up the cookie a browser automatically
// attaches to same-origin requests, landing in internal/bridge's
// authenticated branch with no ticket involved.
//
// That does not work: verified directly against this project's live server
// with Chromium (Playwright, CDP Network.webSocketHandshakeResponseReceived
// on the raw handshake) AND against an isolated minimal Node WebSocket
// server carrying nothing but a Set-Cookie header on its 101 response —
// Chromium's WebSocket client does not apply Set-Cookie from a WebSocket
// upgrade response to its cookie jar at all. document.cookie and
// BrowserContext.cookies() stay empty in both cases. This is not a bug in
// hijack.go's header splice (internal/bridge's own
// TestUpgradeWithValidTicket proves the server writes the header correctly)
// — it is Chromium's WebSocket implementation simply never consulting
// Set-Cookie on this particular response, unlike an ordinary HTTP response.
// So a plain reload-and-hope-the-cookie-stuck cannot ever deliver an
// authenticated connection.
//
// The fix used here: CompleteLogin does not dial or wait for anything. It
// stashes the ticket in sessionStorage (cleared the moment it's read — see
// PendingTicket) and reloads immediately. web/shell.Mount's boot dial then
// checks PendingTicket BEFORE calling wsconn.Connect and, if one is
// present, dials DefaultEndpoint+"?ticket=..." (TicketEndpoint) directly
// instead of the plain endpoint — so the ONE connection this reload ever
// creates, the one every page package's Init(...) gets wired to
// (web/main.go's wirePages, out of this task's allowed paths), is already
// the ticket-authenticated connection from the moment it exists. There is
// no second connection to discard and no in-place swap to perform:
// swapping web/shell's *wsconn.Conn pointer after the fact would not help
// anyway, because generate/history/settings' Deps already hold copies of
// the OLD connection's typed clients (guardedFeedClient etc.), each closed
// over the OLD *grpc.ClientConn directly — reassigning fields on the
// shared Conn struct after wirePages has already run would not reach them.
//
// The cost this leaves, stated plainly per this task's brief: the session
// cookie internal/bridge still sets on the ticket-redeeming 101 response is
// real and correctly formed, but Chromium never stores it, so it buys
// nothing today. A plain page reload/refresh after this point has no
// cookie and no stashed ticket to present, dials anonymously, and the
// operator is logged out — there is no durable, ordinary-HTTP-reload path
// to a still-authenticated session under "everything over WebSocket," only
// the one just-completed login flow's own reload. Fixing that would need a
// mechanism this task's allowed paths cannot provide (e.g. a plain HTTP
// endpoint outside the bridge that could carry a normal Set-Cookie
// response, which is exactly what PLAN.md §2/§3's "one transport, no HTTP
// side door" rules out).
package wsconn

import (
	"context"
	"fmt"
	"net/url"
	"syscall/js"

	"github.com/monstercameron/GoWebComponents/v5/interop"
)

// ticketQueryParam MUST match internal/bridge/server.go's own constant of
// the identical name — that file is this one's only consumer on the wire,
// and the two cannot share a Go const across the server/WASM build boundary
// (internal/bridge is not part of a GOOS=js/wasm build), so this is
// duplicated deliberately rather than silently drifting.
const ticketQueryParam = "ticket"

// pendingTicketStorageKey is the sessionStorage key CompleteLogin stashes a
// redeemed-but-not-yet-consumed ticket under, for Mount's boot dial to pick
// up after the reload CompleteLogin triggers. sessionStorage (not
// localStorage) deliberately: this value has no business surviving past
// the tab/session that produced it, matching the ticket's own seconds-long
// server-side TTL (internal/bridge/ticket.go's DefaultTicketTTL) in spirit
// even though sessionStorage itself has no expiry of its own.
const pendingTicketStorageKey = "aff.pending-login-ticket"

// TicketEndpoint returns DefaultEndpoint with ticket attached as the query
// parameter internal/bridge/server.go's ServeHTTP reads (ticketQueryParam)
// — the URL web/shell.Mount dials when PendingTicket reports a stashed
// ticket at boot.
func TicketEndpoint(ticket string) string {
	return DefaultEndpoint + "?" + ticketQueryParam + "=" + url.QueryEscape(ticket)
}

// CompleteLogin hands ticket to the NEXT page load rather than redeeming it
// itself: it stashes ticket in sessionStorage under pendingTicketStorageKey
// and forces a full page reload via ReloadPage. See this file's package doc
// comment for why redemption has to happen on the reload's own boot dial
// (web/shell.Mount + PendingTicket), not here.
//
// ctx is accepted (and ignored) only to keep this function's signature
// identical to the version web/pages/auth/wiring.go already calls —
// nothing here does I/O worth cancelling.
//
// A non-nil error means the ticket could not even be STASHED (empty ticket,
// or sessionStorage genuinely unavailable) — CompleteLogin does not reload
// in that case, leaving the caller's existing (anonymous) connection
// exactly as it was, same contract the previous dial-based version
// documented. Once the ticket is stashed successfully this always reloads;
// whether the stashed ticket actually redeems is discovered on the next
// boot (Mount), not here.
func CompleteLogin(ctx context.Context, ticket string) error {
	_ = ctx
	if ticket == "" {
		return fmt.Errorf("wsconn: CompleteLogin: empty ticket")
	}

	store, err := interop.SessionStorage()
	if err != nil {
		return fmt.Errorf("wsconn: CompleteLogin: sessionStorage unavailable: %w", err)
	}
	if err := store.SetItem(pendingTicketStorageKey, ticket); err != nil {
		return fmt.Errorf("wsconn: CompleteLogin: storing ticket: %w", err)
	}

	ReloadPage()
	return nil
}

// PendingTicket returns and CONSUMES a ticket a prior CompleteLogin call
// left in sessionStorage for this boot to redeem, or "" if there is none
// (the ordinary case: no login just happened, or sessionStorage is
// unavailable). It removes the stored value before returning it — a ticket
// is single-use on the server (internal/bridge/ticket.go's Redeem) and this
// is the one place on the client that ever reads it, so leaving it behind
// would only let a later, unrelated boot try to reuse an already-redeemed
// (or, worse, someone else's still-valid) ticket for no benefit.
//
// Called once, by web/shell.Mount, before its boot dial — see that
// function and this file's package doc comment.
func PendingTicket() string {
	store, err := interop.SessionStorage()
	if err != nil {
		return ""
	}
	ticket, found, err := store.GetItem(pendingTicketStorageKey)
	if err != nil || !found || ticket == "" {
		return ""
	}
	_ = store.RemoveItem(pendingTicketStorageKey)
	return ticket
}

// ReloadPage forces a full browser reload, discarding all in-memory Go/WASM
// state. Exported (rather than folded silently into CompleteLogin) so a test
// or a future caller can distinguish "the ticket was stashed" from "and then
// the page reloaded" without a real browser location object.
func ReloadPage() {
	location := js.Global().Get("location")
	if location.Truthy() {
		location.Call("reload")
	}
}
