//go:build js && wasm

package history

import (
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"

	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// rowKebab is the shared web/ui.Kebab, adapted for this page's tables.
//
// It replaces a hand-rolled `.history-kebab` menu that opened on CSS
// :hover/:focus-within and positioned itself `absolute`. Two things were
// wrong with that, and both are the kind that only show up in use rather
// than in a screenshot: a hover-only menu cannot be opened by touch at all
// and closes the moment the pointer strays on the way to the item; and an
// absolutely positioned menu inside these tables — which are their own
// horizontal-scroll containers — is clipped by the scroll box, so the last
// row's menu was cut off by the edge of the table rather than floating over
// it.
//
// The shared primitive measures its trigger and positions itself fixed, and
// brings a real open/close state, Escape, outside-click and focus handling
// with it. One kebab implementation in the app instead of two.
// items is a thunk, not a slice, because a closed menu must not pay to build
// one (TODOS.md A8-44). On /history/items the table renders 25 rows, each
// with a five-entry menu and five closures, on every selection toggle,
// debounced search keystroke and filter change — all of it discarded,
// because at most one menu is open. web/ui.Kebab makes the same skip
// internally; this stops the allocation one level earlier, at the caller
// that knows the item set.
func rowKebab(t Catalog, id, ownerLabel string, open bool, onOpen func(bool), items func() []affui.KebabItem) ui.Node {
	var built []affui.KebabItem
	if open && items != nil {
		built = items()
	}
	return affui.Kebab(affui.KebabProps{
		// history.Catalog takes a map for args; web/ui.T takes variadic.
		// None of the kebab labels interpolate anything, so this adapter
		// simply drops the (always empty) argument list.
		T:            func(key string, _ ...any) string { return t.T(key, nil) },
		ID:           id,
		LabelKey:     "history.kebab.for",
		LabelArgs:    []any{ownerLabel},
		Items:        built,
		Open:         open,
		OnOpenChange: onOpen,
	})
}
