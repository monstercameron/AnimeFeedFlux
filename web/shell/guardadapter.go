//go:build js && wasm

package shell

import (
	"github.com/monstercameron/GoWebComponents/v5/router"

	"github.com/monstercameron/AnimeFeedFlux/web/guard"
)

// routeGuard adapts guard.Decide (pure, host-testable — web/guard) into a
// router.GuardFunc for router.Options.BeforeEnter. This is the one seam
// between the pure decision logic and GWC's router; it reads
// SessionAtom.Global() rather than state.UseAtomKey because BeforeEnter
// runs during navigation evaluation, not component rendering — calling
// the hook form here would panic (see session.go's package doc comment).
func routeGuard(info guard.RouteInfo) router.GuardFunc {
	return func(router.RouteContext) router.GuardResult {
		d := guard.Decide(SessionAtom.Global().Get(), info)
		if d.Allow {
			return router.AllowNavigation()
		}
		return router.RedirectNavigation(d.RedirectTo)
	}
}

// catchAllGuard is registered on the router's "*" route: PLAN.md §12's
// five routes are the whole app, so any other path is unconditionally
// redirected via guard.DecideUnknown rather than rendered as a 404 page
// (D-FLOW: "no nested navigation, no hamburger, no breadcrumb").
func catchAllGuard(router.RouteContext) router.GuardResult {
	return router.RedirectNavigation(guard.DecideUnknown(SessionAtom.Global().Get()))
}
