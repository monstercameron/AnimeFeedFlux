//go:build js && wasm

package shell

import (
	"math/rand"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// keyBannerReconnecting is the shell.* key for D0-09's DISCONNECTED banner,
// a PLURAL message keyed on {seconds} whole seconds (D6-06: "plurals
// through the library's plural rules, not `if n == 1`"). Points at
// web/i18n/keys_shell.go's real KeyShellBannerReconnectingIn
// ("banner.reconnectingIn") now that that namespace is populated — this
// used to be a stale local "banner.reconnecting" that matched nothing in
// the catalogue, so the banner silently rendered the missing-key fallback
// text instead of the intended countdown sentence. Fixed alongside the
// route-wiring change since it's the same class of bug (component built
// and tested against the wrong key, never caught because the fallback
// looks like plausible text).
const keyBannerReconnecting = afi18n.KeyShellBannerReconnectingIn

// renderBanner is the DISCONNECTED banner (D0-09). It is a real rendered
// component, so it uses the hook form state.UseAtomKey — this is the one
// place in the shell allowed to (contrast with guardadapter.go and
// wsconn's connectivity watcher, which read SessionAtom.Global() because
// they are not rendering).
//
// h.ClassMap builds the class string from a map[string]bool rather than
// hand-concatenating strings, which keeps this readable without
// reintroducing the html-authoring traps this project was warned about
// (a css.Rule value passed as a JSX-like child renders as literal text
// instead of being applied; the html.Style map form silently drops
// "--custom-property" keys, so a component that ever needs a CSS custom
// property must use html.StyleVar instead of stuffing it into Style{}).
// This banner needs neither — it's plain utility classes — but the rule
// applies to every component later waves add on top of this shell.
//
// # The countdown, and its honesty limits
//
// D0-09/§12.6 ask for the reconnect countdown to go through the library's
// plural rules, not `if n == 1`. This component runs its own local
// attempt/deadline countdown, seeded from the SAME web/backoff.DefaultPolicy
// curve wsconn hands to grpctunnel.WithReconnectPolicy (see
// web/wsconn/conn.go), so the numbers shown track the real curve's shape
// (quick at first, settling at a 30s ceiling).
//
// IMPORTANT CAVEAT, since this package is restricted to editing files under
// web/shell/ only: web/wsconn.Conn does not itself expose the real
// reconnect-attempt count or the real next-retry deadline — grpc-go owns
// that schedule internally once it has the policy. So the seconds-remaining
// figure here is a faithful, same-curve SIMULATION, not a read of the live
// timer; making it authoritative would require adding an attempt/deadline
// accessor to web/wsconn, which is out of this package's allowed scope (see
// this task's report). What is NOT simulated, and must not be: whether the
// banner shows at all. `visible` comes straight from SessionAtom, which
// wsconn's real watchConnectivity only flips via genuine
// EvWSDropped/EvWSReconnected transitions — the banner never lies about
// whether the socket is actually down, only its countdown is approximate.
func renderBanner() ui.Node {
	sess := state.UseAtomKey(SessionAtom)
	visible := sess.Get() == appstate.Disconnected

	attempt := ui.UseState(0)
	deadline := ui.UseState(time.Time{})
	now := ui.UseState(time.Now())

	// A fresh disconnect restarts the local countdown at attempt 0. The
	// dep is the primitive bool `visible`, not a freshly allocated
	// closure/slice, so identity comparison across renders is meaningful
	// (the "UseEffect deps compare by IDENTITY" trap this shell was
	// warned about).
	ui.UseEffect(func() func() {
		if !visible {
			return nil
		}
		attempt.Set(0)
		deadline.Set(time.Now().Add(backoff.DefaultPolicy.Delay(0, rand.Float64)))
		return nil
	}, visible)

	// Tick once a second — same pattern as web/pages/auth/login.go's own
	// backoff-countdown ticker — so the number actually counts down
	// rather than freezing at whatever it first showed. The tick/advance
	// arithmetic itself lives in countdown.go (host-testable, no js build
	// tag); this hook is only responsible for wiring it to real time and
	// component state.
	ui.UseInterval(func() {
		if !visible {
			return
		}
		nowTime := time.Now()
		a, d := nextReconnectPhase(backoff.DefaultPolicy, attempt.Get(), deadline.Get(), nowTime, rand.Float64)
		attempt.Set(a)
		deadline.Set(d)
		now.Set(nowTime)
	}, time.Second, visible)

	secondsLeft := 0
	if visible {
		secondsLeft = reconnectSecondsLeft(deadline.Get(), now.Get())
	}

	t := gwci18n.UseI18n().NS(afi18n.NSShell)

	return h.Div(
		h.ClassStr(h.ClassMap(map[string]bool{
			"af-banner":          true,
			"af-banner--visible": visible,
			"af-banner--hidden":  !visible,
		})),
		h.Text(t.T(keyBannerReconnecting, gwci18n.Arguments{"seconds": secondsLeft})),
	)
}
