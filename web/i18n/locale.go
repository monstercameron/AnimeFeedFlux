package i18n

import (
	"strings"
	"sync/atomic"
)

// locale.go is the app's answer to "which language is the interface in
// right now?", and it is a package-level value rather than a parameter
// threaded through every call site.
//
// # Why a package var and not a prop
//
// Every translated string in this app is produced by a `t(key, args...)`
// closure that a page package received through its own Deps at boot
// (web/main.go's bundleTranslator/bundleCatalog) and then stored in a
// package var. There are ~550 call sites across six namespaces and none of
// them takes a locale: `t("settings.about.title")` is the whole contract,
// deliberately, so that no page package has to import web/i18n or know a
// locale exists (see each page's doc.go). Threading a locale into that
// contract would mean touching every one of those call sites and every
// intermediate props struct in web/ui — a change to the shape of the whole
// UI layer in order to carry one value that is global by nature.
//
// So the locale lives here, the translator closures read it at TRANSLATE
// time (not at wiring time), and switching languages is one write here plus
// one re-render. What makes that sound is that reading is genuinely global:
// there is exactly one operator looking at exactly one document, and no
// context in this program wants a different language from any other.
//
// # Why atomic
//
// A WASM build is not single-threaded from Go's point of view: every RPC in
// this app runs in its own goroutine (see any section's UseEffect in
// web/pages/settings) and those goroutines format text. A plain string var
// written by a click handler while a goroutine reads it is a data race that
// `-race` would catch in CI and that a browser would express as torn or
// stale text. atomic.Value costs one indirection per translation and
// removes the whole category.
//
// # What this is NOT
//
// It is not the feed's language. A feed carries its own `language` field
// (PLAN.md §12.3's recipe editor, published into RSS/Atom/JSON Feed), which
// is DATA about what a feed contains and is completely independent of the
// language the operator reads the admin UI in. PLAN.md §12.6 draws that
// line explicitly; nothing in this file should ever be consulted when
// rendering a feed.

// LocaleEnglish and LocaleSpanish are the BCP-47 tags this app ships
// catalogues for. They are persisted (web/shell/locale.go writes the chosen
// tag to localStorage), so they are API in the same sense theme.go's
// preference strings are: changing one silently resets every operator's
// choice to the default on their next load.
const (
	LocaleEnglish = "en"
	LocaleSpanish = "es"
)

// LocaleOption describes one choice for the language selector.
//
// Endonym is the language's name IN THAT LANGUAGE ("Español", not
// "Spanish"), and it is a literal here rather than a catalogue key on
// purpose: a language list whose entries are translated is a list where
// someone stranded in a language they cannot read has no way to find their
// own. "Español" says the same thing to everyone regardless of which
// catalogue is loaded, which is the one property this particular control
// cannot do without. Every shipping UI does this for the same reason.
type LocaleOption struct {
	Tag     string
	Endonym string
}

// supportedLocales is the ordered list the selector renders. English first
// because it is the default and the fallback, then alphabetical by tag.
var supportedLocales = []LocaleOption{
	{Tag: LocaleEnglish, Endonym: "English"},
	{Tag: LocaleSpanish, Endonym: "Español"},
}

// SupportedLocales returns the shippable locales in display order.
//
// Returns a fresh slice: the package's own copy is the source of truth for
// what NewBundle registers, and a caller that appended to it would produce a
// selector offering a language with no catalogue behind it — every string
// would silently fall back to English while the control claimed otherwise.
func SupportedLocales() []LocaleOption {
	out := make([]LocaleOption, len(supportedLocales))
	copy(out, supportedLocales)
	return out
}

// IsSupportedLocale reports whether tag has a registered catalogue. Callers
// that read a locale from anywhere untrusted — localStorage, a URL, a stored
// preference written by an older build — must gate on this rather than
// passing the value straight to SetCurrentLocale.
func IsSupportedLocale(tag string) bool {
	for _, l := range supportedLocales {
		if l.Tag == tag {
			return true
		}
	}
	return false
}

// EndonymFor returns the display name for tag, or the tag itself if it is
// not one this app ships. Returning the raw tag rather than "" matters in
// the selector: an unknown stored preference should render as something the
// operator can see and correct, not as a blank option.
func EndonymFor(tag string) string {
	for _, l := range supportedLocales {
		if l.Tag == tag {
			return l.Endonym
		}
	}
	return tag
}

// NegotiateLocale matches a browser's ordered language preferences (the
// contents of navigator.languages, most-preferred first) against what this
// app ships, returning "" when nothing matches.
//
// Matching is on the PRIMARY SUBTAG: "es-419", "es-MX" and "es" all select
// the Spanish catalogue. Shipping one Spanish and then refusing to serve it
// to a Latin American Spanish browser would be a worse answer than a
// regional-vocabulary mismatch — the operator can still read every word.
// Comparison is case-insensitive because navigator values are not normalised
// consistently across browsers, and both "-" and "_" are accepted as the
// subtag separator for the same reason.
//
// This lives here rather than in web/shell (whose locale.go is its only
// caller) purely so it can be tested: web/shell is js-and-wasm-only, so a
// pure function that happens to sit in it is a pure function no host test
// can reach.
func NegotiateLocale(preferences []string) string {
	for _, pref := range preferences {
		primary := strings.ToLower(strings.TrimSpace(pref))
		if i := strings.IndexAny(primary, "-_"); i >= 0 {
			primary = primary[:i]
		}
		if primary == "" {
			continue
		}
		for _, supported := range supportedLocales {
			if strings.EqualFold(supported.Tag, primary) {
				return supported.Tag
			}
		}
	}
	return ""
}

// currentLocale holds the active tag. Declared as atomic.Value rather than
// a string for the reason in this file's doc comment; seeded at init so
// CurrentLocale never has to answer "unset" and no caller needs a nil check.
var currentLocale atomic.Value

func init() {
	currentLocale.Store(DefaultLocale)
}

// CurrentLocale returns the tag every translation and every formatter
// should be resolved against right now.
//
// This is the function that replaced a hardcoded DefaultLocale at every
// lookup site (web/main.go's two adapters, adapter.go's five formatters).
// Those sites passing the constant is exactly what made the app
// single-language: the catalogue layer had supported multiple registered
// locales from the day it was written, and nothing ever asked it for one.
func CurrentLocale() string {
	if v, ok := currentLocale.Load().(string); ok && v != "" {
		return v
	}
	return DefaultLocale
}

// SetCurrentLocale switches the active language and reports whether it
// took. An unsupported tag is REFUSED rather than stored-and-fallen-back:
// storing it would leave CurrentLocale returning a tag with no catalogue, so
// every lookup would take the fallback path and log a missing key
// (logMissingKey, D6-07) for every string on screen — a loud, useless flood
// describing a state the caller could have prevented.
//
// This does not persist anything and does not re-render. Persistence is
// web/shell/locale.go's job (localStorage, the same place the theme
// preference lives) and re-rendering is the locale atom's; keeping all three
// separate is what lets this file stay host-testable with no build tag.
func SetCurrentLocale(tag string) bool {
	if !IsSupportedLocale(tag) {
		return false
	}
	currentLocale.Store(tag)
	return true
}
