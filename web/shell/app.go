//go:build js && wasm

// Package shell is the WASM admin app's shell: the GWC router for the
// five routes, the auth guard, the DISCONNECTED banner, and the
// session-expiry hold (TODOS.md D0-01..10).
//
// # Built on GoWebComponents v5, not the hand-rolled version
//
// An earlier pass of this change found that go.mod's then-pinned
// dependency, github.com/monstercameron/GoWebComponents v1.1.0, exports
// no usable render entry point from outside its own package (createElement,
// useState, useEffect, render are all unexported in that module) and built
// a hand-rolled syscall/js DOM layer instead, while flagging that as a real
// defect: full-DOM-rebuild-per-state-change would eventually clobber focus
// and scroll in real forms.
//
// That defect is now avoidable. go.mod pins a DIFFERENT module path,
// github.com/monstercameron/GoWebComponents/v5 v5.0.1 — confirmed by
// reading the module directly (`go list -m -f '{{.Dir}}' .../v5`), not
// assumed from its README — which has the ~70-package surface PLAN.md §12
// actually assumed: `router`, `state`, `ui`, `html`, `a11y`, `i18n`, and so
// on, with a real fiber-based reconciler and hooks (`ui.CreateElement`,
// `ui.UseState`, `ui.UseEffect`, `ui.Render`/`ui.Run`). This shell is
// rebuilt on that surface: `router` owns path matching, guards, and
// `<base href>`-safe navigation; `state.AtomKey`/`GlobalAtom` own the
// shared ANON/AUTH/ELEVATED/DISCONNECTED/KILLED source of truth (see
// session.go); `ui`/`html/shorthand` own DOM authoring and diffing.
//
// One doc/reality gap found while building this: the reference manual's
// own getting-started example imports `html/shorthand` and calls
// `Class("...")`, but v5.0.1's shorthand package has no exported `Class`
// function — only `ClassStr(string) PropOption`. Confirmed by grepping the
// pinned module's actual source and then round-tripping a throwaway
// `GOOS=js GOARCH=wasm go build` of the documented example, which fails
// with `undefined: Class` until swapped for `ClassStr`. Every code sample
// in this package was verified to compile against the pinned v5.0.1
// module the same way before being kept, not copied from the docs on
// faith.
//
// web/route (the previous hand-rolled path matcher) was deleted: GWC's
// `router.Register`/`Mount` does real path matching, and `router.Href` /
// pushState navigation are resolved against `<base href>` by the browser
// itself (confirmed in router/use_route_wasm.go's own doc comments), so
// there is no longer a second, competing implementation of either. This
// project's own web/route/browser.go used to strip `<base href>` by hand
// for exactly the reason PLAN.md §12 warns about ("without it deep links
// and refreshes break") — that concern is now GWC's job, not this
// package's.
//
// web/appstate, web/backoff, and web/guard are unchanged: they were
// already pure and host-testable (no GWC, no syscall/js), which is
// exactly the right shape, and nothing about the real GWC surface
// overlaps what they do (GWC has no concept of this app's five-state
// D-FLOW model or its reconnect-backoff curve). guardadapter.go is the
// only new seam: it converts guard.Decide's pure Decision into a
// router.GuardFunc for router.Options.BeforeEnter.
package shell

import (
	"context"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/router"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/guard"
	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
	"github.com/monstercameron/AnimeFeedFlux/web/wsconn"
)

// routeTable is the fixed five-route admin UI (PLAN.md §12). This is now
// the single source of truth for the route set — web/guard's tests keep
// their own copy for host-testability (see web/guard/guard_test.go's
// routeTable), since web/guard cannot import GWC's router (js-only) or
// this package (which does).
var routeTable = []guard.RouteInfo{
	{Path: "/login", RequiresAuth: false},
	{Path: "/recover", RequiresAuth: false},
	// /setup is unauthenticated by construction — it exists to create the
	// account that authentication checks against. web/guard's existing rules
	// already do the right thing for it with no special case: ANON may open
	// it, an authed-ish session is bounced to /generate, ELEVATED to
	// /recover.
	{Path: "/setup", RequiresAuth: false},
	{Path: "/generate", RequiresAuth: true},
	{Path: "/history", RequiresAuth: true},
	{Path: "/settings", RequiresAuth: true},
	// Each settings section is its own address, so /settings/provider can be
	// bookmarked, pasted, and — the point of the change — survive a reload
	// on the section you were actually looking at. Before this the section
	// was component state, so every refresh dropped you back on Security and
	// there was no way to link anyone (including a future you reading a
	// runbook) at a specific panel.
	//
	// Guarded exactly like /settings: same auth requirement, same redirect.
	{Path: "/settings/:section", RequiresAuth: true},
	// Same treatment for History's two tabs (/history/runs, /history/items),
	// for the same reason and with the same guard. A route the page registers
	// but this table does not list is not merely unguarded — it is treated as
	// unknown and redirected away, which is what sent /history/items to
	// /generate the first time this was wired up.
	{Path: "/history/:tab", RequiresAuth: true},
}

// initialSessionTimeout bounds the boot-time "whoami" call (D0-06/D0-07):
// Mount blocks on it so the very first render already knows ANON vs AUTH
// (a deep link into /history must not flash a /login redirect before the
// real session state is known — PLAN.md §12: "deep links... must resolve
// correctly"). An unreachable control plane must not leave the admin
// tool blank forever, so this is bounded rather than an unbounded block.
const initialSessionTimeout = 5 * time.Second

// ticketDialTimeout bounds how long Mount waits for a ticket-carrying boot
// dial (see wsconn.PendingTicket/TicketEndpoint and ticket.go's package doc
// comment for why the boot dial itself, not a later swap, has to be the one
// that redeems the ticket) to reach Ready before giving up and falling back
// to a plain anonymous dial. Generous relative to one WebSocket handshake
// but short enough that a genuinely stuck/refused ticket redemption does
// not leave the operator staring at a blank screen for long.
const ticketDialTimeout = 10 * time.Second

// conn is the shell's one control-plane connection, set by Mount.
var conn *wsconn.Conn

// Conn returns the shell's one control-plane connection for wiring page
// packages' own Init(...) seams from outside this package — e.g.
// web/main.go, once it constructs the other typed clients this page
// package's Conn.Auth-only surface doesn't yet cover, calling
// generatepage.Init(generatepage.Deps{...}) or settings.Init(settings.
// Deps{...}) (see those packages' doc.go: "web/wsconn.Conn exposes only
// Conn.Auth ... web/wsconn and web/shell are off limits to this change").
// Before this accessor there was no exported way to reach the connection
// this package holds AT ALL, from outside the package — Conn.Guard's D0-10
// "queue or refuse mutations while DISCONNECTED, never fail silently"
// mechanism (web/wsconn/conn.go) was unreachable by any page. This closes
// that half of the gap; the other half — wsconn.Conn having only .Auth and
// no .Feed/.Sample/.Run/.System/.Item fields — is a change to web/wsconn
// itself, which is out of this package's allowed paths.
//
// Returns nil before Mount has run, or if the initial dial in Mount failed
// outright (see Mount's DISCONNECTED fallback) — callers must nil-check
// rather than assume a live connection.
func Conn() *wsconn.Conn {
	return conn
}

// Mount dials the control plane, resolves the initial ANON/AUTH state,
// hands the dialed connection (possibly nil — see below) to wire, then
// registers the five routes (plus "*") with their guards and mounts the
// router at selector. It blocks briefly (see initialSessionTimeout) before
// the first render so the guard's very first decision is correct.
//
// wire is the client-side composition root's hook (web/main.go): it runs
// AFTER the connection is dialed but BEFORE the router renders anything,
// specifically so that by the time any page's own Render is first called,
// its Init has already installed real dependencies — a page's Init has no
// way to force a re-render of an already-mounted fiber (GWC re-renders on
// state changes, not on an unrelated package var being reassigned), so
// calling wire any later would leave the first-rendered route stuck
// showing its "not wired" state until the next navigation. wire receives
// the dialed *wsconn.Conn directly, or nil if the initial dial failed
// outright (see the DISCONNECTED fallback just below) — callers must
// nil-check before reading any field off it, same contract Conn() already
// documents. wire may be nil (e.g. a future non-page caller of Mount that
// has nothing to wire).
func Mount(ctx context.Context, selector string, wire func(*wsconn.Conn)) {
	// Emit the design tokens BEFORE anything renders.
	//
	// This had no caller anywhere, and the consequence was total: web/tokens
	// defines the :root custom properties, and web/ui's every rule reads them
	// with css.Var("--color-…"). With Emit unwired, those variables were
	// simply undefined, so every var() resolved to nothing and the entire app
	// rendered as unstyled browser defaults — no colours, no spacing, no dark
	// mode. The token layer's careful work (both themes, the WCAG contrast
	// corrections, the reduced-motion collapse, the :root[data-theme] selector
	// fix) was all real and all reaching no one.
	//
	// Nothing caught it: the tokens have unit tests that assert on the rules
	// they GENERATE, which pass whether or not anyone emits them, and no host
	// test loads a document. It took a screenshot.
	tokens.Emit()

	// Resolve and stamp <html data-theme> immediately after the tokens are
	// emitted and before anything renders. Order matters in both directions:
	// after Emit, because the dark block has to exist for the attribute to
	// select anything; before the first render, because applying it later
	// means painting a frame of light theme first — a white flash on the
	// login screen, in what may well be a dark room. See theme.go.
	ApplyStoredTheme()
	// Held for the life of the page: while the preference is "system", an OS
	// theme change follows through to the document. The returned cleanup is
	// deliberately dropped — this listener's lifetime IS the page's.
	_ = WatchSystemTheme()

	// The language, resolved and installed on the same before-anything-renders
	// footing as the theme and for a sharper version of the same reason: a
	// locale applied from inside a component effect paints one frame of
	// English before switching, so every single load flickers through a
	// language the operator may not read. See locale.go.
	ApplyStoredLocale()

	// This package's own rules (#app, .af-banner, .af-expiry-modal,
	// .af-content/.af-placeholder) — see styles.go. Emitted right after
	// tokens.Emit() so the shell's own root/banner/modal styling is in
	// place before the router's first render, same reasoning as
	// tokens.Emit() itself: nothing here should render unstyled even for
	// one frame.
	emitShellStyles()

	// A ticket left by a just-completed CompleteLogin (web/wsconn/ticket.go)
	// makes THIS the connection every page package's Init(...) gets wired
	// to with an already-authenticated session — see ticket.go's package
	// doc comment for why the boot dial has to be the one carrying it
	// (Chromium does not store the Set-Cookie a ticket redemption sets on a
	// WebSocket 101 response, and there is no supported way to redial an
	// already-dialed *grpc.ClientConn with a different URL later).
	endpoint := wsconn.DefaultEndpoint
	ticket := wsconn.PendingTicket()
	if ticket != "" {
		endpoint = wsconn.TicketEndpoint(ticket)
	}

	dialedConn, err := wsconn.Connect(ctx, endpoint, applyEvent)
	if err == nil && ticket != "" {
		waitCtx, cancel := context.WithTimeout(ctx, ticketDialTimeout)
		waitErr := dialedConn.WaitReady(waitCtx)
		cancel()
		if waitErr != nil {
			// The ticket didn't redeem (already used, expired, or some
			// other upgrade refusal) — fall back to a plain anonymous dial
			// rather than leaving the operator on a connection that will
			// only ever retry the same now-permanently-invalid ticket URL.
			_ = dialedConn.Close()
			dialedConn, err = wsconn.Connect(ctx, wsconn.DefaultEndpoint, applyEvent)
		}
	}
	if err != nil {
		// The initial dial failing outright (not merely "not yet Ready")
		// still needs an honest DISCONNECTED banner rather than a login
		// form that cannot possibly work — bypass Transition here (ANON
		// -> DISCONNECTED is not itself a modeled edge, since D-FLOW
		// only reaches DISCONNECTED from AUTH/KILLED) because this is a
		// boot-time exception, not a runtime transition.
		SessionAtom.Global().Set(appstate.Disconnected)
	} else {
		conn = dialedConn
		resolveInitialSession(ctx)
	}

	if wire != nil {
		wire(conn)
	}

	r := router.NewHistoryRouter(router.RouterOptions{DefaultRoute: guard.DefaultAnon})
	for _, info := range routeTable {
		r.Register(info.Path, pageComponent(info.Path), router.Options{
			BeforeEnter: routeGuard(info),
		})
	}
	r.Register("*", pageComponent("*"), router.Options{BeforeEnter: catchAllGuard})
	r.Mount(selector)
}

// resolveInitialSession calls AuthService.Session once at boot to decide
// ANON vs AUTH before the router's first guard evaluation. Runs outside
// any component render (Mount is not a render), so it writes through
// SessionAtom.Global() rather than the UseAtomKey hook form — see
// session.go's package doc comment.
func resolveInitialSession(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, initialSessionTimeout)
	defer cancel()

	resp, err := conn.Auth.Session(callCtx, &affv1.AuthServiceSessionRequest{})
	if err != nil {
		// PLAN.md §12.1: /login "must never break." The fail-safe
		// direction for the entire defense-is-authentication model
		// (PLAN.md §4) is toward ANON, never toward assuming an
		// unverified session is valid — a timed-out or errored whoami
		// call is treated exactly like "no session."
		SessionAtom.Global().Set(appstate.Anon)
		return
	}
	if resp.GetSession() != nil {
		SessionAtom.Global().Set(appstate.Auth)
	} else {
		SessionAtom.Global().Set(appstate.Anon)
	}
}
