package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// es_catalog.go assembles the Spanish catalogue from the six per-namespace
// maps in the es_*.go files beside it, mirroring enCatalog exactly —
// including the mergeCommonIntoNamespace treatment, which is not optional
// here for the same reason it is not optional there: web/ui's shared
// primitives render bare common.* keys through whichever namespace-bound
// translator their caller handed them, so every namespace needs its own copy
// of the common entries or a Spanish operator gets Spanish prose with
// English buttons on it.
//
// # Where these translations came from, stated plainly
//
// They were written by the model that built this feature, not by a
// professional translator and not by a native speaker. They are consistent
// and idiomatic enough to use, and they have NOT been reviewed by anyone who
// speaks Spanish as a first language. Two specific things a reviewer should
// look at before this is put in front of a Spanish-speaking operator:
//
//   - Register. These use informal second person ("Revisa tus datos"),
//     matching the English source's direct address. Spanish enterprise
//     software often prefers the impersonal ("Revise sus datos" / "Se
//     produjo un error"). It is applied consistently, so changing it is a
//     mechanical pass rather than a rewrite.
//   - Domain vocabulary. "feed", "slug", "cron" and "prompt" are left in
//     English throughout, because they are identifiers and operator surface
//     rather than prose — the same rule TODOS.md D6-19 already states for
//     byte-size units. "Fuente" for feed would read more naturally and would
//     stop matching the URLs, the TOML, and the API the operator is looking
//     at on the next screen.
//
// # Placeholders are load-bearing
//
// Every {arg1}/{count}/{seconds}/{path} placeholder must survive
// translation with its NAME intact — they are looked up by name, not by
// position, so a translated "{cuenta}" silently renders as literal text.
// TestEveryLocaleHasEveryKey (catalog_test.go) checks presence; the
// placeholder-parity check beside it is what catches this specific mistake.
var esCatalog = gwci18n.Catalog{
	NSCommon:   esCommonMessages,
	NSAuth:     mergeCommonIntoNamespaceFrom(esCommonMessages, esAuthMessages),
	NSShell:    mergeCommonIntoNamespaceFrom(esCommonMessages, esShellMessages),
	NSGenerate: mergeCommonIntoNamespaceFrom(esCommonMessages, esGenerateMessages),
	NSHistory:  mergeCommonIntoNamespaceFrom(esCommonMessages, esHistoryMessages),
	NSSettings: mergeCommonIntoNamespaceFrom(esCommonMessages, esSettingsMessages),
}

// mergeCommonIntoNamespaceFrom is mergeCommonIntoNamespace with the common
// source made explicit instead of hardcoded to the English one.
//
// The original takes commonMessages from the package scope, which was
// correct while one locale shipped and silently wrong the moment a second
// did: every Spanish namespace would have been merged with ENGLISH common
// entries, so "Guardar" and "Cancel" would sit on the same form. Rather than
// change that function's signature (it has callers in enCatalog's
// initialiser above and reads better without the parameter), this is its
// two-argument sibling and the one every non-default locale must use.
func mergeCommonIntoNamespaceFrom(common, dst gwci18n.NamespaceCatalog) gwci18n.NamespaceCatalog {
	merged := make(gwci18n.NamespaceCatalog, len(dst)+len(common))
	for k, v := range common {
		merged[k] = v
	}
	for k, v := range dst {
		merged[k] = v
	}
	return merged
}
