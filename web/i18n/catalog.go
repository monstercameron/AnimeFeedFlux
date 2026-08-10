// Package i18n is AnimeFeedFlux's own message catalogue, built on top of
// GoWebComponents/v5's generic i18n package (TODOS.md D6-01: "adopt GWC
// v5's i18n package; record ... what it does and does not do").
//
// # What gwci18n gives us
//
//   - Catalog/Bundle: a namespace->key->Message map with locale fallback
//     (Bundle.Register per locale; Translate walks locale -> primary
//     language -> fallback -> default locale before giving up).
//   - Interpolation ({name}-style placeholders) and CLDR-ish plural
//     category resolution (Message.Plural keyed by PluralCategory),
//     driven off a numeric arg named by Message.PluralArg (default
//     "count") — see gwci18n.Runtime.T / gwci18n.interpolateTemplate.
//   - Locale-aware FormatNumber/FormatDate helpers and PrefixPath/
//     ResolvePath for locale-in-URL routing.
//   - A Provider/UseI18n context pair: gwci18n.Provider mounted once
//     (D0-21: "in the root component, above the router" — that mount
//     point is web/shell's, not this package's) and read anywhere below
//     it via gwci18n.UseI18n().T(namespace, key, args) or
//     gwci18n.UseI18n().NS(namespace).T(key, args).
//
// # What it does NOT give us
//
//   - It does not load or parse a message file format. Register/
//     RegisterNamespace take an in-memory Catalog literal — this
//     package's enCatalog below IS "the single checked-in source of
//     truth, loaded at build time" (D6-02): a Go literal, compiled in,
//     not a JSON/TOML file read at runtime.
//   - It does not enforce key-naming, namespace boundaries, the missing
//     -key policy, or the zero-literal rule. Those are this package's
//     job (Bundle's OnMissing hook below) and D6-20's lint, respectively
//     — gwci18n only calls OnMissing if one is supplied.
//   - It does not generate typed accessors by default (that is the
//     opt-in `gwc i18n gen` codegen mentioned in the v5 reference manual,
//     chapter 11). This package uses plain Go string constants instead
//     (see keys_auth.go) — cheaper to introduce, weaker than generated
//     accessors (a key constant with the wrong namespace still compiles),
//     but D6-22/D6-23-style tests in catalog_test.go and
//     web/pages/auth/catalog_coverage_test.go close most of that gap for
//     the namespaces populated so far.
//   - It does not itself decide what a "user-visible string literal" is
//     for D6-20/D6-21's lint gate — that lint is a separate, not-yet-
//     built piece of tooling; nothing here implements it.
package i18n

import (
	"fmt"
	"os"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// Namespace names (D6-04: "namespace keys by surface ... so an unused key
// is findable and a missing one is obvious"). Every namespace PLAN.md §12
// names is created here — even the ones with no content yet — so a later
// wave adds keys to an existing map instead of deciding where a fifth
// namespace should live.
const (
	NSAuth     = "auth"
	NSCommon   = "common"
	NSShell    = "shell"
	NSGenerate = "generate"
	NSHistory  = "history"
	NSSettings = "settings"
)

// DefaultLocale is the only locale this app ships (D6's point is not
// multiple languages — see PLAN.md §12.6 — but where strings live).
const DefaultLocale = "en"

// enCatalog is the single checked-in source of truth for the "en" locale
// (D6-02). Namespaces the D1 auth pages don't need yet are declared empty
// (D6-04) rather than omitted, so `enCatalog[i18n.NSGenerate]` is always a
// valid, non-nil map for a later wave to add to.
var enCatalog = gwci18n.Catalog{
	NSCommon:   commonMessages,
	NSAuth:     authMessages,
	NSShell:    shellMessages,
	NSGenerate: generateMessages,
	NSHistory:  historyMessages,
	NSSettings: settingsMessages,
}

// NewBundle builds the app's Bundle with the "en" catalogue registered and
// OnMissing wired to logMissingKey (D6-07). Called once by whatever wave
// mounts gwci18n.Provider (web/shell, per D0-21) — this package does not
// mount the provider itself, since that placement decision ("above the
// router") belongs to the root component web/shell owns, not to the
// catalogue.
func NewBundle() *gwci18n.Bundle {
	b := gwci18n.NewBundle(gwci18n.BundleOptions{
		DefaultLocale:  DefaultLocale,
		FallbackLocale: DefaultLocale,
		OnMissing:      logMissingKey,
	})
	b.Register(DefaultLocale, enCatalog)
	return b
}

// logMissingKey is the Bundle's MissingHandler (D6-07: "a missing key
// renders the key itself and logs loudly ... never an empty string — a
// blank label is the one failure mode that looks like a styling bug").
// It writes to stderr, which both `go test` (host) and a GOOS=js/wasm
// build (Go's js/wasm runtime forwards os.Stderr writes to the browser's
// console.error) surface loudly, and returns the same "namespace.key"
// fallback text gwci18n's own defaultMissingText would have produced, so
// a caller does not also need a nil/empty check.
func logMissingKey(locale, namespace, key string) string {
	fmt.Fprintf(os.Stderr, "i18n: missing key %q in namespace %q for locale %q\n", key, namespace, locale)
	if namespace == "" {
		return key
	}
	return namespace + "." + key
}
