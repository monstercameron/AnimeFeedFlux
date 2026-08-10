// Package guard implements the navigation guard decisions from TODOS.md
// D0-07/D1-11/D-FLOW as a pure function of (appstate.State, RouteInfo), so
// the redirect logic is testable without a DOM, a WASM build, or GWC's
// router to drive it.
//
// This package intentionally does not import GoWebComponents/v5/router:
// router.RouteContext and router.GuardResult are only defined under
// `//go:build js && wasm` (checked directly in the pinned v5.0.1 module),
// so a package that wants host-testable logic cannot use those types
// directly. web/shell's js-only guardadapter.go is the thin seam that
// calls Decide/DecideUnknown and converts the result into a
// router.GuardFunc for router.Options.BeforeEnter.
package guard

import "github.com/monstercameron/AnimeFeedFlux/web/appstate"

// DefaultAnon is where an ANON user lands with no other information —
// e.g. an unmatched path. Mirrors PLAN.md §12's /login as the entry point.
const DefaultAnon = "/login"

// DefaultAuthed is J1's landing page: "First login → land on /generate"
// (D-FLOW, PLAN.md §12.3: "default landing page after login").
const DefaultAuthed = "/generate"

// RouteInfo is the minimal shape Decide needs about the route being
// navigated to. web/shell builds one of these per registered route from
// its own route table (there is no separate route-table package anymore
// — GWC's router owns path matching and base-href resolution, so
// web/route was deleted; see web/shell's package doc comment).
type RouteInfo struct {
	// Path is the route's registered path, e.g. "/generate". Only
	// "/recover" is special-cased (the ELEVATED branch below).
	Path string
	// RequiresAuth is true for the three routes reachable only from
	// AUTH (or the AUTH-derived DISCONNECTED/KILLED states):
	// /generate, /history, /settings.
	RequiresAuth bool
}

// Decision is the outcome of evaluating a navigation attempt.
type Decision struct {
	// Allow is true if the target route may be rendered as-is.
	Allow bool
	// RedirectTo is the path to navigate to instead, when Allow is
	// false. Always one of DefaultAnon, DefaultAuthed, or "/recover"
	// (the ELEVATED case).
	RedirectTo string
}

// Decide evaluates whether state may navigate to r.
//
//   - ANON reaching an authed route (D0-07): redirect to /login.
//   - ELEVATED may reach ONLY /recover (D1-11: "ELEVATED cannot navigate
//     to /generate, /history, or /settings" — and by the same logic not
//     /login either, since re-entering the ordinary login flow mid
//     recovery would abandon the elevated session's one job).
//   - AUTH/DISCONNECTED/KILLED reaching /login or /recover: redirect to
//     /generate — an authed-ish session has no business on the
//     unauthenticated pages (mirrors D1-05's "Back does not land on a
//     stale login form").
//   - Otherwise: allowed as requested.
func Decide(state appstate.State, r RouteInfo) Decision {
	if state == appstate.Elevated {
		if r.Path == "/recover" {
			return Decision{Allow: true}
		}
		return Decision{Allow: false, RedirectTo: "/recover"}
	}

	if !appstate.CanNavigate(state, r.RequiresAuth) {
		if state == appstate.Anon {
			return Decision{Allow: false, RedirectTo: DefaultAnon}
		}
		// Authed-ish state hitting /login or /recover.
		return Decision{Allow: false, RedirectTo: DefaultAuthed}
	}

	return Decision{Allow: true}
}

// DecideUnknown is the guard's answer for a path that matched no
// registered route at all (GWC's "*" catch-all route): send ANON to
// /login and everyone else to their default landing page, rather than a
// bare 404 — there is no 404 page in this five-route admin UI (PLAN.md
// §12: "no nested navigation, no hamburger, no breadcrumb").
func DecideUnknown(state appstate.State) string {
	if state == appstate.Elevated {
		return "/recover"
	}
	if appstate.IsAuthedIsh(state) {
		return DefaultAuthed
	}
	return DefaultAnon
}
