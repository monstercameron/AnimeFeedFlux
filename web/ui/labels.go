// Package ui is AnimeFeedFlux's shared component layer: primitives (button,
// input, select, toggle, table, tabs, modal, toast, kebab menu) and the six
// shared state components (loading/empty/populated/error/disabled-with-
// reason/disconnected) every list in Phase D is built from (PLAN.md §12.6,
// TODOS.md D0-13/D0-14).
//
// It is built on github.com/monstercameron/GoWebComponents/v5 — see that
// module's ui/html/css/a11y packages, which this package imports under the
// gwc/html/css/a11y aliases to avoid colliding with this package's own name.
package ui

// T resolves an i18n key (with optional interpolation/plural args) to
// display text. Every label a primitive in this package renders is taken as
// a KEY plus a T, never a literal (PLAN.md §12.6, TODOS.md D0-22/D0-23) — this
// package does not import web/i18n directly (that package is owned and
// developed independently); it only needs something that can translate a
// key, so it depends on this minimal function type instead.
//
// web/i18n's provider is expected to expose a value satisfying this exact
// signature (e.g. a method value from its catalogue/hook,
// `i18n.UseT()` returning a `func(string, ...any) string`) so callers can
// pass it straight through:
//
//	t := i18n.UseT()
//	ui.Button(ui.ButtonProps{T: t, LabelKey: "action.save"})
//
// A nil T is tolerated defensively (falls back to the key itself, never a
// hardcoded English literal, so a wiring bug is visible as an unresolved key
// rather than as silently-wrong text) but every real call site must supply
// one — see TODOS.md D6-14.
type T func(key string, args ...any) string

// resolve calls t(key, args...), or returns key when t is nil.
func resolve(t T, key string, args ...any) string {
	if t == nil {
		return key
	}
	return t(key, args...)
}
