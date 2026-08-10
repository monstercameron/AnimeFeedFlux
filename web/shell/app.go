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
	{Path: "/generate", RequiresAuth: true},
	{Path: "/history", RequiresAuth: true},
	{Path: "/settings", RequiresAuth: true},
}

// initialSessionTimeout bounds the boot-time "whoami" call (D0-06/D0-07):
// Mount blocks on it so the very first render already knows ANON vs AUTH
// (a deep link into /history must not flash a /login redirect before the
// real session state is known — PLAN.md §12: "deep links... must resolve
// correctly"). An unreachable control plane must not leave the admin
// tool blank forever, so this is bounded rather than an unbounded block.
const initialSessionTimeout = 5 * time.Second

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
	dialedConn, err := wsconn.Connect(ctx, wsconn.DefaultEndpoint, applyEvent)
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
