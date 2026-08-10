package history

// Catalog is the minimal i18n lookup this page depends on. web/i18n is
// owned by another agent and this package deliberately does not import it
// (see doc.go); Catalog is the narrow shape this page needs from whatever
// that package ends up exposing. Every user-visible string in the
// js-build files is a Catalog.T call with a key under "history.*"
// (PLAN.md §12.6: "keys named for meaning rather than for the English
// text").
type Catalog interface {
	// T resolves key to localized text. args carries named interpolation
	// values (e.g. {"count": 3}); pass nil when the key takes none.
	T(key string, args map[string]any) string
}
