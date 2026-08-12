//go:build js && wasm

package shell

import (
	"github.com/monstercameron/GoWebComponents/v5/interop"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// theme.go turns the dark palette on.
//
// web/tokens has carried a complete DarkTheme since D0-12 was written, and
// tokens.Emit has always emitted it under `:root[data-theme="dark"]` — but
// nothing in this application ever called ui.SetTheme, and `data-theme` is
// the only thing that selector can match. So every one of those roles was
// unreachable: the app rendered light for everybody, including an operator
// whose OS is set to dark, and a screenshot in dark mode came back
// byte-identical to one in light mode. The palette was not wrong, it was
// disconnected. This file is the connection.
//
// # Three states, not two
//
// The preference is `system` (default), `light`, or `dark`. "System" is a
// real third state rather than a starting value that decays into one of the
// other two: it means "keep following the OS", so an operator who works
// through a scheduled light-to-dark switch gets it, and it is the only state
// that can express "I have not chosen". A two-state toggle has to guess an
// initial value, and whichever it guesses is wrong for half the people and
// silently stops tracking the OS for all of them.
//
// # Why the preference is in localStorage and the session token is not
//
// PLAN.md §4 is emphatic that the session token never touches storage. This
// is the opposite kind of value: it is not a credential, it is not secret,
// it is worthless to an attacker who reads it, and it must outlive the tab
// (a preference that resets every time you open a window is not a
// preference). sessionStorage — correct for the login ticket, which has no
// business surviving its tab — would be exactly wrong here.

// themePrefKey is the localStorage key holding the operator's choice.
// Namespaced, because this origin also serves the admin bundle and any
// future page on the same host.
const themePrefKey = "aff.theme"

// Theme preference values. These strings are persisted, so they are API:
// changing one silently resets every existing operator's choice back to
// system on their next load.
//
// Exported as of 2026-08-11, when the control that offers them moved out of
// this package into Settings → Appearance (web/pages/settings/
// render_appearance.go). The unexported aliases below are kept because this
// file's own internals read them a dozen times and `themeDark` says the same
// thing as `ThemeDark` with less noise inside the package that owns them.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"

	themeSystem = ThemeSystem
	themeLight  = ThemeLight
	themeDark   = ThemeDark
)

// StoredThemePref exposes the persisted preference to the Appearance panel,
// which needs the current value to render its control's selected state.
//
// Read-only by design: the panel changes the theme through SelectTheme, never
// by writing storage itself, so the "record and apply in one step" guarantee
// SelectTheme documents cannot be bypassed from outside this package.
func StoredThemePref() string { return storedThemePref() }

// ApplyStoredTheme resolves the stored preference against the OS setting and
// stamps <html data-theme>, and is called by Mount BEFORE the first render.
//
// Before-first-render is the whole point: applying the theme from inside a
// component effect means the browser paints one frame of light theme and
// then repaints dark, which on the login screen is a white flash in a dark
// room. Everything this function touches (localStorage, matchMedia, the
// documentElement attribute) is available synchronously at boot, so there is
// no reason to defer it to a render pass.
func ApplyStoredTheme() {
	ui.SetTheme(resolveTheme(storedThemePref()))
}

// resolveTheme maps a preference onto the theme name the token layer
// understands. `system` has no token layer of its own — it is a rule about
// which of the other two to use, resolved here and re-resolved whenever the
// OS setting changes (see WatchSystemTheme).
func resolveTheme(pref string) string {
	switch pref {
	case themeDark:
		return themeDark
	case themeLight:
		return themeLight
	default:
		if systemPrefersDark() {
			return themeDark
		}
		return themeLight
	}
}

// storedThemePref reads the persisted preference, falling back to `system`
// for anything it does not recognise — including the case where localStorage
// is unavailable outright (private-mode restrictions, a storage-partitioned
// context). A preference that cannot be read is not an error worth surfacing
// to the operator; it just means "no choice recorded", which is `system`.
func storedThemePref() string {
	store, err := interop.LocalStorage()
	if err != nil {
		return themeSystem
	}
	value, found, err := store.GetItem(themePrefKey)
	if err != nil || !found {
		return themeSystem
	}
	switch value {
	case themeLight, themeDark, themeSystem:
		return value
	default:
		return themeSystem
	}
}

// storeThemePref persists a choice. Failures are deliberately swallowed: the
// theme has already been applied to the live document by the caller, so a
// storage write that fails costs the operator their choice on the NEXT load
// and nothing on this one. Interrupting them with an error about a colour
// preference would be a worse trade than quietly not remembering it.
func storeThemePref(pref string) {
	store, err := interop.LocalStorage()
	if err != nil {
		return
	}
	_ = store.SetItem(themePrefKey, pref)
}

// systemPrefersDark reports the OS colour-scheme preference.
//
// Read through matchMedia directly rather than through ui.UsePrefersColorScheme,
// which is a hook: it can only be called from inside a render, and this runs
// at boot before any component exists, and again from a media-query callback
// that is not a render either.
func systemPrefersDark() bool {
	list, err := interop.GetMediaQuery(prefersDarkQuery)
	if err != nil {
		return false
	}
	return list.Matches()
}

const prefersDarkQuery = "(prefers-color-scheme: dark)"

// WatchSystemTheme keeps the `system` preference honest for the life of the
// page: when the OS flips (a scheduled sunset switch, or the operator
// changing it in another window), the app follows.
//
// It re-reads the STORED preference on every event rather than capturing it,
// because the operator may have chosen light or dark since this listener was
// installed — in which case an OS change must be ignored, which is the whole
// meaning of having chosen. Returns a cleanup function; Mount holds it for
// the life of the page, which is why it is never called.
func WatchSystemTheme() func() {
	list, err := interop.GetMediaQuery(prefersDarkQuery)
	if err != nil {
		return func() {}
	}
	sub, err := list.Subscribe(func(interop.MediaQueryEvent) {
		if storedThemePref() == themeSystem {
			ui.SetTheme(resolveTheme(themeSystem))
		}
	})
	if err != nil {
		return func() {}
	}
	return func() { sub.Cancel() }
}

// SelectTheme records a choice and applies it in one step, so the two can
// never disagree — a stored preference the live document does not reflect is
// a bug that only shows up on the next reload, which is the worst place to
// find it.
func SelectTheme(pref string) {
	storeThemePref(pref)
	ui.SetTheme(resolveTheme(pref))
}
