//go:build js && wasm

package shell

import (
	"github.com/monstercameron/GoWebComponents/v5/state"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
)

// SessionAtom is the single shared source of truth for the D-FLOW
// application state (TODOS.md D-FLOW / PLAN.md §12), read by every
// component that needs to know ANON/AUTH/ELEVATED/DISCONNECTED/KILLED —
// the banner, the route guards, the expiry modal, page placeholders.
// state.UseAtom/UseAtomKey is GWC's tool for exactly this: "unrelated
// components need the same shared source of truth" (06-state-and-
// reactivity.md).
//
// # The "no UseAtom in a render-only path" trap
//
// state.UseAtomKey (the hook form, used inside a component's render) may
// ONLY be called while a component is actively rendering — it panics
// otherwise, because it needs a live fiber to subscribe to. Two places in
// this shell are NOT a render: GWC router BeforeEnter guards (navigation
// evaluation, not rendering — see guardadapter.go) and wsconn's
// background connectivity-watcher goroutine (see conn.go's watchConnectivity).
// Both of those, plus the one-time boot whoami call in app.go, read and
// write this atom exclusively through SessionAtom.Global() (a
// GlobalAtom[T], "shared state read/written from OUTSIDE a render" per
// the same doc chapter) — never through UseAtomKey. Only the actual
// rendered components in banner.go, expiry.go, and pages.go call
// state.UseAtomKey(SessionAtom).
var SessionAtom = state.NewAtomKey("aff.session-state", appstate.Anon)

// PendingExpiryAtom tracks D0-08's "don't silently lose unsaved work" hold:
// true while a session-expired event has been received but is being kept
// back from actually applying (see applyEvent) because RegisterDirtyCheck's
// predicate said the current page has unsaved work. The expiry modal
// (expiry.go) subscribes to this via UseAtomKey; AcknowledgeExpiry (called
// from that modal's "Log in" button, itself inside a render-triggered event
// handler, i.e. fine to call Global() from) clears it and applies the held
// transition.
var PendingExpiryAtom = state.NewAtomKey("aff.pending-expiry", false)

// dirtyCheck is page code's answer to "does the current page have unsaved
// work" (D0-08). Registered by later waves via RegisterDirtyCheck; nil
// (the zero value, meaning "never dirty") until a page registers one.
var dirtyCheck func() bool

// RegisterDirtyCheck installs the predicate applyEvent consults before
// silently applying a session-expiry transition. Call this from a page
// component's own mount/effect wiring once it owns editable state (the
// /generate editor, in a later wave).
func RegisterDirtyCheck(fn func() bool) {
	dirtyCheck = fn
}

// applyTransition runs appstate.Transition against SessionAtom's current
// value and writes back the result, ignoring rejected (invalid)
// transitions. Safe to call from outside a render — see the package doc
// comment above.
func applyTransition(ev appstate.Event) {
	SessionAtom.Global().Update(func(current appstate.State) appstate.State {
		next, ok := appstate.Transition(current, ev)
		if !ok {
			return current
		}
		return next
	})
}

// applyEvent is wsconn's onEvent callback and the single place
// EvSessionExpired's D0-08 hold applies: if the current page reports
// unsaved work, the transition is held (PendingExpiryAtom is set, the
// modal shows) rather than applied immediately and silently. Every other
// event applies immediately.
func applyEvent(ev appstate.Event) {
	if ev == appstate.EvSessionExpired && dirtyCheck != nil && dirtyCheck() {
		PendingExpiryAtom.Global().Set(true)
		return
	}
	applyTransition(ev)
}

// ApplyEvent runs ev through appstate.Transition against SessionAtom and
// writes back the result, exactly like the unexported applyTransition
// above. Exported so page packages outside this one (auth's /login and
// /recover success callbacks — EvLoginSuccess, EvRecoveryCodeAccepted,
// EvRecoveryComplete) can drive the shared D-FLOW state machine from their
// own event handlers/goroutines without this package handing out
// SessionAtom.Global() itself, which would let a caller Set() an arbitrary
// state instead of going through a validated Transition. Safe to call from
// a goroutine (an RPC callback) or a render-triggered event handler alike,
// per the same "Global() outside a render, UseAtomKey inside one" rule
// documented on SessionAtom above — ApplyEvent never touches UseAtomKey.
func ApplyEvent(ev appstate.Event) {
	applyTransition(ev)
}

// AcknowledgeExpiry is the expiry modal's "Log in" button handler: clears
// the hold and applies the session-expired transition the admin just
// confirmed they've seen. Persisting the draft itself (e.g. to
// localStorage) is the editor page's job via the same RegisterDirtyCheck
// seam — this only stops the shell from discarding it silently out from
// under the page before the admin had a chance to notice.
func AcknowledgeExpiry() {
	PendingExpiryAtom.Global().Set(false)
	applyTransition(appstate.EvSessionExpired)
}
