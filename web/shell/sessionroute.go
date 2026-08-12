//go:build js && wasm

package shell

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/guard"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// sessionroute.go sends an expired session back to /login.
//
// # The gap this closes
//
// The route guard (guardadapter.go) runs on BeforeEnter — during navigation
// evaluation, and only then. That is exactly right for "can this person open
// this page", and it is blind to the case that actually happens: the session
// dying UNDER a page that is already open. Nothing re-ran the guard when
// SessionAtom changed, and nothing anywhere in the app navigated on expiry.
//
// So an admin sitting on /generate whose session lapsed stayed on /generate.
// The state machine correctly moved to ANON, the header nav went inert, and
// every RPC the page made failed — but the page stayed on screen, looking
// like a working application that had inexplicably stopped working. Even the
// expiry modal's "Sign in" button did not go to /login: AcknowledgeExpiry
// clears the hold and applies the transition, which left the admin on the
// same dead page with the modal gone.
//
// # Re-running the SAME decision, not a second one
//
// This re-evaluates guard.Decide — the same pure function BeforeEnter uses,
// against the same routeTable entry — rather than implementing its own idea
// of "you should not be here". Two rules for one question drift, and the
// resulting bug (the guard and the watcher disagreeing about /recover while
// ELEVATED, say) would be maddening to find. The only thing this adds is a
// trigger: re-decide when the session state changes, not only when the path
// does.
//
// That also means the states it does NOT redirect on come for free.
// DISCONNECTED is authed-ish, so a dropped WebSocket leaves you where you
// are with the reconnect banner up — which is the whole point of having a
// DISCONNECTED state rather than treating every socket blip as a logout.
// ELEVATED still gets sent to /recover, not /login.

// RedirectNoticeAtom carries the catalogue key of the explanation shown on
// the destination screen, or "" for none.
//
// A silent bounce to /login is the thing D0-08's expiry modal was written to
// prevent — "the shell silently redirecting to /login out from under the
// admin" — and adding the redirect without adding the explanation would be
// implementing precisely what that comment argues against. Arriving at a
// login form with no account of why is indistinguishable from having clicked
// something wrong.
//
// A KEY rather than a rendered string, because the notice outlives the render
// that set it and the operator may switch language in between.
var RedirectNoticeAtom = state.NewAtomKey("aff.redirect-notice", "")

// routeInfoFor returns the routeTable entry for a registered path pattern.
//
// props.Path is the PATTERN the route was registered under ("/settings/:section",
// not "/settings/provider") because pageComponent is constructed per
// routeTable entry with that literal — see Mount. So this is an exact match,
// never a matcher.
func routeInfoFor(path string) (guard.RouteInfo, bool) {
	for _, info := range routeTable {
		if info.Path == path {
			return info, true
		}
	}
	return guard.RouteInfo{}, false
}

// renderSessionRoute is the watcher. It renders nothing; it exists for its
// effect, and it is a component rather than a bare function so that its atom
// subscriptions have a fiber of their own — a re-render here must not
// re-render the page body beneath it.
// sessionRouteKey collapses this effect's three deps into one comparable
// value for UseEffectOf, whose typed single-dep form avoids the per-render
// deps slice the variadic form allocates — measurably in wasm (A8-50).
type sessionRouteKey struct {
	current appstate.State
	held    bool
	path    string
}

func renderSessionRoute(props shellWrapperProps) ui.Node {
	sess := state.UseAtomKey(SessionAtom)
	pendingExpiry := state.UseAtomKey(PendingExpiryAtom)

	current := sess.Get()
	held := pendingExpiry.Get()
	path := props.Path

	ui.UseEffectOf(func() func() {
		info, known := routeInfoFor(path)
		act := guard.DecideSessionChange(current, info, known, held)

		if act.ClearNotice {
			// Drop any stale explanation so the next arrival at /login does
			// not inherit the reason for a redirect two sessions ago.
			RedirectNoticeAtom.Global().Set("")
		}
		if act.RedirectTo == "" {
			return nil
		}
		if act.ExplainExpiry {
			RedirectNoticeAtom.Global().Set(afi18n.KeyShellSessionExpiredNotice)
		}
		// Deferred to the next tick, NOT called inline, and this is
		// load-bearing rather than defensive.
		//
		// Router.NavigateReplace opens a "guard attempt"
		// (beginGuardAttempt/evaluateNavigationWithAttempt in GWC's
		// router.go) and returns SILENTLY — no error, no panic, no log the
		// app can see — if that attempt cannot be started because one is
		// already in flight. This effect runs inside the render the router
		// itself triggered, so the attempt it would open is re-entrant and
		// the call is dropped on the floor.
		//
		// Observed exactly that way: with a session revoked server-side, the
		// expiry was detected, the notice was set and rendered, and the URL
		// never moved off /settings/data. Everything looked like it worked
		// except the one thing that mattered.
		//
		// AfterFunc(0) puts the navigation on the JS timer queue, so it runs
		// after the current render/commit has finished and the router is idle
		// enough to accept it.
		//
		// Replace, not push. Back must not be able to land on an authed route
		// belonging to a session that no longer exists — the same reasoning
		// header.go's sign-out handler already documents.
		target := act.RedirectTo
		time.AfterFunc(0, func() {
			router.NavigateReplace(target)
		})
		return nil
	}, sessionRouteKey{current: current, held: held, path: path})

	return h.Fragment()
}

// renderRedirectNotice shows why the operator is looking at this screen
// instead of the one they were on.
//
// Rendered by the shell rather than by web/pages/auth because the shell is
// what performed the redirect and what is mounted on every route including
// /login — putting it in the auth page would mean the page that did not
// cause the redirect owning the message about it, and would need a second
// copy for /recover.
func renderRedirectNotice() ui.Node {
	notice := state.UseAtomKey(RedirectNoticeAtom)
	t := gwci18n.UseI18n().NS(afi18n.NSShell)

	key := notice.Get()
	if key == "" {
		// Nothing in the DOM when there is nothing to say — same rule as the
		// expiry modal, and for the same reason: an element that is present
		// but hidden is an element that can still intercept a click.
		return h.Fragment()
	}

	return h.Div(
		h.ClassStr("af-notice"),
		h.Role("status"),
		h.Text(t.T(key)),
	)
}
