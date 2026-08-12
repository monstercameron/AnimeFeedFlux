//go:build js && wasm

package shell

import (
	"syscall/js"

	"github.com/monstercameron/GoWebComponents/v5/interop"
	"github.com/monstercameron/GoWebComponents/v5/state"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// locale.go is to language what theme.go is to colour: it owns where the
// operator's choice is STORED, when it is applied, and what has to happen
// for the running page to reflect a change. web/i18n owns which locales
// exist and which one is active for lookups; those two halves are kept
// apart so web/i18n stays host-testable with no build tag while everything
// that needs a browser lives here.
//
// # Three things have to move together
//
// A language switch is not one write, it is three, and skipping any one of
// them produces a specific bug:
//
//  1. afi18n.SetCurrentLocale — what every t() call reads. Skip it and the
//     UI keeps rendering the old language while the control claims otherwise.
//  2. LocaleAtom — what forces the re-render. Skip it and the new language
//     appears only on the next navigation, so the selector looks broken at
//     exactly the moment it is used.
//  3. localStorage + <html lang> — what survives a reload, and what screen
//     readers and browser translation prompts read. Skip these and the
//     choice evaporates on refresh and assistive tech announces Spanish text
//     in an English voice.
//
// SelectLocale does all three, in that order, and is the only function any
// caller should need — the same "record and apply in one step so the two can
// never disagree" rule theme.go's SelectTheme documents.
//
// # Why the whole tree re-renders
//
// GWC's reconciler has no props-equality bailout (confirmed by reading
// internal/runtime: there is no React.memo-equivalent short-circuit for
// function components), so re-rendering the component that mounts the i18n
// Provider re-runs every component beneath it, which is every page and every
// control in the app. That is what makes a locale switch total rather than
// patchy: no page has to subscribe to anything, and a page added later
// inherits the behaviour without knowing this file exists. renderShellRoot
// (pages.go) is that component; it reads LocaleAtom and feeds the result to
// gwci18n.Provider.

// localePrefKey is the localStorage key holding the chosen language tag.
// Namespaced exactly like themePrefKey, and for the same reason.
const localePrefKey = "aff.locale"

// LocaleAtom carries the active language tag for RENDERING purposes.
//
// It duplicates afi18n.CurrentLocale() by design, and the duplication is
// the mechanism rather than an oversight: web/i18n's copy is what lookups
// read (a plain atomic, readable from any goroutine, no GWC dependency),
// while this atom is what the renderer SUBSCRIBES to. A component cannot
// subscribe to an atomic.Value, and web/i18n cannot import GWC's state
// package without acquiring a build tag it must not have. SelectLocale is
// the one writer, and it writes both, so they cannot drift.
//
// Read inside a render with state.UseAtomKey(LocaleAtom); write from
// outside one through .Global().Set — the same discipline session.go
// documents for SessionAtom, and for the same reason.
var LocaleAtom = state.NewAtomKey("aff.locale-state", afi18n.DefaultLocale)

// ApplyStoredLocale resolves the operator's language and installs it BEFORE
// the first render, called from Mount alongside ApplyStoredTheme.
//
// Before-first-render matters here for the same reason it does for the
// theme, in a louder form: applying the locale from inside a component
// effect means the first paint is English and the second is Spanish, so
// every load flickers through a language the operator did not choose. All
// three inputs (localStorage, navigator.languages, the document element) are
// available synchronously at boot, so there is nothing to wait for.
func ApplyStoredLocale() {
	tag := resolveInitialLocale()
	afi18n.SetCurrentLocale(tag)
	LocaleAtom.Global().Set(tag)
	setDocumentLang(tag)
}

// resolveInitialLocale picks the language for a fresh page load, in
// precedence order: an explicit stored choice, then the browser's own
// language preferences, then English.
//
// The middle step is the one worth defending. An operator whose browser is
// configured for Spanish has already stated a language preference; making
// them find a setting to state it again is asking twice. It is only ever a
// DEFAULT — the moment they choose explicitly, storedLocalePref wins
// forever after, including when they explicitly choose English on a
// Spanish-configured browser. That case is exactly why the stored value is
// checked first rather than merged: "I want English despite my browser" has
// to be expressible, and a scheme that re-derives from navigator on every
// load cannot express it.
func resolveInitialLocale() string {
	if pref := storedLocalePref(); pref != "" {
		return pref
	}
	if tag := afi18n.NegotiateLocale(browserLanguages()); tag != "" {
		return tag
	}
	return afi18n.DefaultLocale
}

// storedLocalePref reads the persisted choice, returning "" for absent,
// unreadable (private-mode storage restrictions), or unrecognised values.
//
// Unrecognised covers the real case of a tag written by a build that
// shipped a locale this one does not: dropping back to negotiation is
// better than honouring a tag with no catalogue behind it, which would make
// every string in the app take the missing-key fallback path.
func storedLocalePref() string {
	store, err := interop.LocalStorage()
	if err != nil {
		return ""
	}
	value, found, err := store.GetItem(localePrefKey)
	if err != nil || !found {
		return ""
	}
	if !afi18n.IsSupportedLocale(value) {
		return ""
	}
	return value
}

// storeLocalePref persists a choice. Failures are swallowed for the same
// reason theme.go's storeThemePref swallows them: the language has already
// been applied to the live page by the caller, so a failed write costs the
// choice on the NEXT load and nothing now. An error dialog about a
// preference is a worse outcome than quietly not remembering it.
func storeLocalePref(tag string) {
	store, err := interop.LocalStorage()
	if err != nil {
		return
	}
	_ = store.SetItem(localePrefKey, tag)
}

// browserLanguages returns navigator.languages (most-preferred first),
// falling back to navigator.language, or nil when neither is readable.
func browserLanguages() []string {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() {
		return nil
	}
	if list := nav.Get("languages"); list.Truthy() && list.Length() > 0 {
		out := make([]string, 0, list.Length())
		for i := 0; i < list.Length(); i++ {
			if v := list.Index(i); v.Type() == js.TypeString {
				out = append(out, v.String())
			}
		}
		return out
	}
	if single := nav.Get("language"); single.Type() == js.TypeString {
		return []string{single.String()}
	}
	return nil
}

// setDocumentLang stamps <html lang>, which is not decoration: it is what a
// screen reader consults to choose a voice and pronunciation rules, and what
// the browser's own translation offer keys off. Spanish prose announced by
// an English synthesiser is close to unintelligible, so this is an
// accessibility requirement rather than a nicety — WCAG 3.1.1 (Language of
// Page) is exactly this attribute.
func setDocumentLang(tag string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	root := doc.Get("documentElement")
	if !root.Truthy() {
		return
	}
	root.Set("lang", tag)
}

// SelectLocale switches the interface language: validate, persist, install,
// stamp the document, re-render. Unsupported tags are ignored outright
// rather than partially applied — see afi18n.SetCurrentLocale on why storing
// a tag with no catalogue is worse than refusing it.
//
// Safe to call from an event handler (it is not a hook and touches no fiber
// state directly); the atom write is what schedules the render.
func SelectLocale(tag string) {
	if !afi18n.SetCurrentLocale(tag) {
		return
	}
	storeLocalePref(tag)
	setDocumentLang(tag)
	LocaleAtom.Global().Set(tag)
}
