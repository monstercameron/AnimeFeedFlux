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

// DefaultLocale is the locale every other one falls back to, and the one a
// browser with no matching language preference gets.
//
// It used to be described here as "the only locale this app ships", because
// D6's point was never multiple languages — it was where strings live (see
// PLAN.md §12.6). That framing is still right about why the catalogue exists
// and is no longer right about what it holds: a second locale (LocaleSpanish,
// see locale.go) now ships alongside it, and the catalogue layer needed
// nothing structural to accommodate that. The work was never in the
// catalogue; it was in the ~550 call sites that asked for DefaultLocale by
// name instead of asking which locale was actually selected.
const DefaultLocale = LocaleEnglish

// enCatalog is the single checked-in source of truth for the "en" locale
// (D6-02). Namespaces the D1 auth pages don't need yet are declared empty
// (D6-04) rather than omitted, so `enCatalog[i18n.NSGenerate]` is always a
// valid, non-nil map for a later wave to add to.
//
// Every non-common namespace is registered as mergeCommonIntoNamespace's
// output, not the bare *Messages map — see that function's doc comment for
// why: adapter.go's NewLabelResolver is the ON-PAPER fix for web/ui's
// shared primitives (StatePanel, Confirm, Button, ...) needing common.*
// keys, but web/main.go's actual wiring (bundleTranslator, one per page,
// fixed to that page's own namespace) hands those same primitives a
// namespace-bound T instead — a wiring gap this package cannot close by
// editing web/main.go (out of scope for this pass), only by making the
// lookup succeed regardless of which namespace it lands in.
var enCatalog = gwci18n.Catalog{
	NSCommon:   commonMessages,
	NSAuth:     mergeCommonIntoNamespace(authMessages),
	NSShell:    mergeCommonIntoNamespace(shellMessages),
	NSGenerate: mergeCommonIntoNamespace(generateMessages),
	NSHistory:  mergeCommonIntoNamespace(historyMessages),
	NSSettings: mergeCommonIntoNamespace(settingsMessages),
}

// mergeCommonIntoNamespace closes the common-key degrade reported against
// D6-14: web/ui's shared primitives (web/ui/state.go's StatePanel, confirm.
// go's Confirm, labels.go-driven Button/Kebab/...) render bare common.*
// keys ("action.cancel", "state.loading", "confirm.typePhrase", ...)
// through whatever T their caller passes — and every page wave (D6-11..13)
// hands those primitives its OWN namespace-bound translator
// (web/main.go's bundleTranslator{ns: NSGenerate/NSHistory/NSSettings/...}
// ), not the NSCommon-fixed one adapter.go's NewLabelResolver was built
// for (see that function's doc comment: "web/ui's primitives ... only ever
// render common.* keys, so this adapter is fixed to NSCommon"). gwci18n's
// Bundle.Translate falls back across LOCALES within one namespace, never
// across namespaces within one locale (see i18n.go's lookup/localeCandidates
// in the GoWebComponents/v5 module), so a bare "state.loading" call routed
// through a NSGenerate-bound translator misses commonMessages entirely and
// falls back to raw "generate.state.loading" text — confirmed by reading
// web/pages/generate/render_rail.go's wui.StatePanel call (T: wt, the
// page's NSGenerate translator) and web/pages/settings/render.go's
// affui.Confirm call (T: t, NSSettings) alongside web/ui/state.go's
// resolve(t, "state.loading") and confirm.go's resolve(p.T,
// "action.cancel"/"confirm.typePhrase") call sites.
//
// The correct fix is wiring web/main.go's shared-primitive call sites to
// NewLabelResolver instead — out of scope here (web/main.go is off-limits
// for this pass) — so this merges a copy of every common.* entry into each
// consuming namespace's catalogue instead: every non-common namespace's
// OWN keys are fully prefixed already (e.g. "generate.editor.slug",
// "settings.security.sessions.revoke" — see keys_generate.go/keys_settings.
// go's doc comments), while every common.* key is bare ("action.cancel",
// not "common.action.cancel"), so the two key spaces cannot collide and a
// dst entry never needs to win over a common one in practice. dst is
// copied over common's entries last anyway, as a defensive tie-break, so a
// future bare key added to a non-common namespace shadows rather than
// silently loses to the merged-in common one.
func mergeCommonIntoNamespace(dst gwci18n.NamespaceCatalog) gwci18n.NamespaceCatalog {
	merged := make(gwci18n.NamespaceCatalog, len(dst)+len(commonMessages))
	for k, v := range commonMessages {
		merged[k] = v
	}
	for k, v := range dst {
		merged[k] = v
	}
	return merged
}

// Catalog returns enCatalog — the real, literal (non-pseudolocalized) "en"
// catalogue NewBundle registers by default — for a caller that needs to
// inspect or diff against it without also linking a *gwci18n.Bundle
// (cmd/affi18n's pseudo-catalog subcommand: diffing real vs.
// PseudoCatalog's placeholder counts per entry). The returned maps are
// enCatalog's own, not a defensive copy — callers must treat it read-only,
// same expectation as PseudoCatalog's freshly-built one being the only
// copy that's actually safe to mutate.
func Catalog() gwci18n.Catalog {
	return enCatalog
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
	// Every shipped locale but the default is registered plainly — no
	// pseudolocale substitution, which is a diagnostic for the SOURCE
	// language (does this string fit when it grows 40%?) and would be
	// meaningless applied to a translation.
	b.Register(LocaleSpanish, esCatalog)

	catalog := enCatalog
	if PseudolocaleEnabled() {
		// D6-24: swap the registered "en" catalogue for its pseudolocalized
		// (en-XA-style) form. Every T() call in the app targets
		// DefaultLocale ("en") directly — see e.g. web/main.go's
		// bundleTranslator and adapter.go's formatters, all of which pass
		// DefaultLocale as both locale and fallback — so there is no
		// separate "en-XA" locale to route to at runtime; the pseudolocale
		// build instead runs the SAME app under DefaultLocale with widened,
		// bracketed text standing in for it. See pseudo.go's doc comment.
		catalog = PseudoCatalog()
	}
	b.Register(DefaultLocale, catalog)
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
