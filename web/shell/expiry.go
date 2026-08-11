//go:build js && wasm

package shell

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// keyExpiryMessage/keyExpiryLogin are the shell.* keys this modal needs,
// pointed at web/i18n/keys_shell.go's real KeyShellExpiryBody/
// KeyShellExpiryLogin now that that namespace is populated — these used to
// be stale local keys ("expiry.message"/"expiry.login") that matched
// nothing in the catalogue, so the modal silently rendered the
// missing-key fallback instead of the intended sentence. Fixed alongside
// the route-wiring change (same class of bug as pages.go's placeholder key
// and banner.go's countdown key).
const (
	keyExpiryMessage = afi18n.KeyShellExpiryBody
	keyExpiryLogin   = afi18n.KeyShellExpiryLogin
)

// renderExpiryModal is D0-08's "don't silently lose unsaved work" surface:
// when a session-expiry event arrives while the current page reports
// unsaved work (see session.go's applyEvent/RegisterDirtyCheck), the
// transition to ANON is held and this blocking modal shows instead of the
// shell silently redirecting to /login out from under the admin. The
// "Log in" button applies the held transition explicitly.
func renderExpiryModal() ui.Node {
	pending := state.UseAtomKey(PendingExpiryAtom)
	t := gwci18n.UseI18n().NS(afi18n.NSShell)
	handleUserLogin := ui.UseEvent(func() {
		AcknowledgeExpiry()
	})

	// Return nothing at all when closed — do NOT render it hidden.
	//
	// This used h.Show, which hides by setting the `hidden` ATTRIBUTE. That
	// works only while nothing overrides the user agent's `display:none`,
	// and `af-expiry-modal--visible` sets a display of its own. The result
	// was a full-screen fixed overlay that was invisible, still laid out,
	// and swallowed every pointer event on the page: the login form could
	// not be clicked at all, on a fresh load, for a visitor who had never
	// had a session.
	//
	// No unit test could see it — the node was present and correct in the
	// tree, and only a real browser resolves `hidden` against a competing
	// `display`. A headless Playwright click found it in one run, reporting
	// `<div hidden class="af-expiry-modal af-expiry-modal--visible">
	// intercepts pointer events`.
	//
	// A closed modal has no reason to be in the DOM. The hooks above are
	// called unconditionally before this branch, so the rule of hooks holds.
	if !pending.Get() {
		return h.Fragment()
	}

	return h.Div(
		h.ClassStr("af-expiry-modal af-expiry-modal--visible"),
		h.Div(
			h.P(t.T(keyExpiryMessage)),
			h.Button(
				h.Type("button"),
				h.OnClick(handleUserLogin),
				t.T(keyExpiryLogin),
			),
		),
	)
}
