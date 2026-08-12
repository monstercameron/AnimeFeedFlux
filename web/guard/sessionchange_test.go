package guard

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
)

// sessionchange_test.go covers DecideSessionChange, which answers a question
// Decide never got asked: what to do when the SESSION changes under a route
// that is already open.
//
// The bug it exists to prevent is the one Cam reported — an expired session
// leaving the admin parked on /generate with a dead page. The tests below pin
// both halves of that: the redirect must happen, and it must NOT happen in
// the three situations where redirecting would be its own bug (a dropped
// socket, an unsaved-work hold, and a recovery session).

var authedRoute = RouteInfo{Path: "/generate", RequiresAuth: true}
var loginRoute = RouteInfo{Path: "/login", RequiresAuth: false}

func TestExpiredSessionOnAnAuthedRouteRedirectsToLogin(t *testing.T) {
	got := DecideSessionChange(appstate.Anon, authedRoute, true, false)

	if got.RedirectTo != DefaultAnon {
		t.Errorf("RedirectTo = %q, want %q — an expired session must not be left on an authed page", got.RedirectTo, DefaultAnon)
	}
	if !got.ExplainExpiry {
		t.Error("ExplainExpiry = false; an admin bounced off a page they were legitimately using is owed the reason")
	}
}

// TestDisconnectedStaysPut is the most important negative case. DISCONNECTED
// is a dropped WebSocket, not a lost session — the reconnect banner handles
// it, and bouncing to /login on every socket blip would make a laptop
// closing-and-opening look like a logout.
func TestDisconnectedStaysPut(t *testing.T) {
	got := DecideSessionChange(appstate.Disconnected, authedRoute, true, false)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect — DISCONNECTED is a transport problem, not a session one", got.RedirectTo)
	}
}

func TestAuthedSessionStaysPutAndClearsTheNotice(t *testing.T) {
	got := DecideSessionChange(appstate.Auth, authedRoute, true, false)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect for a live session", got.RedirectTo)
	}
	if !got.ClearNotice {
		t.Error("ClearNotice = false; the expiry explanation must not follow the admin into the next session")
	}
}

// TestUnsavedWorkHoldSuppressesTheRedirect is D0-08. The expiry modal is up
// because the page reported unsaved work; redirecting now would destroy the
// very thing the modal promised to keep.
func TestUnsavedWorkHoldSuppressesTheRedirect(t *testing.T) {
	got := DecideSessionChange(appstate.Anon, authedRoute, true, true)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect while the unsaved-work hold is up", got.RedirectTo)
	}
	if got.ExplainExpiry {
		t.Error("ExplainExpiry = true while held; the modal is already explaining")
	}
}

// And the moment the hold is released, the same inputs must redirect — this
// is what makes the modal's "Sign in" button actually go to the login screen
// instead of just dismissing itself.
func TestReleasingTheHoldThenRedirects(t *testing.T) {
	held := DecideSessionChange(appstate.Anon, authedRoute, true, true)
	released := DecideSessionChange(appstate.Anon, authedRoute, true, false)

	if held.RedirectTo != "" {
		t.Fatalf("precondition: held state redirected to %q", held.RedirectTo)
	}
	if released.RedirectTo != DefaultAnon {
		t.Errorf("after the hold clears, RedirectTo = %q, want %q", released.RedirectTo, DefaultAnon)
	}
}

// TestUnknownRouteIsLeftToItsOwnGuard covers the router's "*" catch-all,
// which has no RouteInfo. Inventing one here would be a second rule for a
// question the catch-all guard already answers on entry.
func TestUnknownRouteIsLeftToItsOwnGuard(t *testing.T) {
	got := DecideSessionChange(appstate.Anon, RouteInfo{}, false, false)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect for a path with no RouteInfo", got.RedirectTo)
	}
}

// TestElevatedGoesToRecoveryNotLogin: a recovery session is mid-flow. Sending
// it to /login would abandon the one job it exists for, and the notice would
// be wrong — nothing expired.
func TestElevatedGoesToRecoveryNotLogin(t *testing.T) {
	got := DecideSessionChange(appstate.Elevated, authedRoute, true, false)

	if got.RedirectTo != "/recover" {
		t.Errorf("RedirectTo = %q, want /recover for an ELEVATED session", got.RedirectTo)
	}
	if got.ExplainExpiry {
		t.Error("ExplainExpiry = true for ELEVATED; nothing expired, this is a routing rule")
	}
}

// TestAnonOnLoginIsLeftAlone is the case that would otherwise loop: an ANON
// admin sitting on /login must not be redirected to /login.
func TestAnonOnLoginIsLeftAlone(t *testing.T) {
	got := DecideSessionChange(appstate.Anon, loginRoute, true, false)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect — ANON belongs on /login", got.RedirectTo)
	}
	if got.ClearNotice {
		t.Error("ClearNotice = true on /login while ANON; that would erase the explanation the admin just arrived to read")
	}
}

// TestAuthedOnLoginIsSentHomeWithoutAnExpiryNotice: signing in redirects off
// /login, and that is not an expiry.
func TestAuthedOnLoginIsSentHomeWithoutAnExpiryNotice(t *testing.T) {
	got := DecideSessionChange(appstate.Auth, loginRoute, true, false)

	if got.RedirectTo != DefaultAuthed {
		t.Errorf("RedirectTo = %q, want %q", got.RedirectTo, DefaultAuthed)
	}
	if got.ExplainExpiry {
		t.Error("ExplainExpiry = true when a fresh session leaves /login; nothing expired")
	}
}

// TestKilledStaysPut: the kill switch stops generation, it does not end the
// session. Bouncing to /login would be both wrong and alarming.
func TestKilledStaysPut(t *testing.T) {
	got := DecideSessionChange(appstate.Killed, authedRoute, true, false)
	if got.RedirectTo != "" {
		t.Errorf("RedirectTo = %q, want no redirect — KILLED is an authed session with generation disabled", got.RedirectTo)
	}
}

// TestEveryStateAgreesWithDecide is the anti-drift check: whenever this
// function reports a redirect, plain Decide must refuse the same route, and
// whenever it stays put on a known route, Decide must allow it. Two rules for
// one question is the failure mode worth guarding against by construction.
func TestEveryStateAgreesWithDecide(t *testing.T) {
	states := []appstate.State{
		appstate.Anon, appstate.Auth, appstate.Elevated,
		appstate.Disconnected, appstate.Killed,
	}
	routes := []RouteInfo{authedRoute, loginRoute, {Path: "/recover"}}

	for _, st := range states {
		for _, r := range routes {
			change := DecideSessionChange(st, r, true, false)
			decide := Decide(st, r)

			if decide.Allow && change.RedirectTo != "" {
				t.Errorf("state %v route %q: Decide allows but DecideSessionChange redirects to %q",
					st, r.Path, change.RedirectTo)
			}
			if !decide.Allow && change.RedirectTo != decide.RedirectTo {
				t.Errorf("state %v route %q: Decide redirects to %q but DecideSessionChange to %q",
					st, r.Path, decide.RedirectTo, change.RedirectTo)
			}
		}
	}
}
