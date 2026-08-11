//go:build js && wasm

package history

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// kebabTrigger is the `⋯` button that opens a row's destructive-action menu
// (PLAN.md §12.6: "every destructive action sits behind a ⋯ kebab rather
// than a primary button").
//
// It exists because both tables previously rendered the trigger as
// `h.Button(..., t.T("history.kebab"))` — putting the string "Actions"
// inside a button styles.go sizes at 32x32 for a glyph. Found 2026-08-10 in
// a browser: the word overflowed its button on EVERY row of the runs table
// and ran under the neighbouring Expand control, so twenty-five rows each
// read "Expand Actions" as though "Actions" were a second label on the
// Expand button. It was not a styling near-miss — the control had no glyph
// at all.
//
// The label survives where it belongs: as the accessible name. A screen
// reader still announces "Actions"; the glyph is aria-hidden so it is not
// announced as punctuation beside it.
func kebabTrigger(t Catalog) ui.Node {
	return h.Button(
		h.Type("button"),
		h.ClassStr("history-kebab-trigger"),
		h.Aria("label", t.T("history.kebab", nil)),
		h.Aria("haspopup", "menu"),
		// A glyph, not copy: it is aria-hidden, carries no meaning to
		// translate, and is identical in every locale — the same reasoning
		// web/ui/kebab.go's own "⋯" already relies on.
		h.Span(h.Aria("hidden", "true"), h.Text("⋯")), //nolint:i18n -- decorative glyph, aria-hidden, no translatable content
	)
}
