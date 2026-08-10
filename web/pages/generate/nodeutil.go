//go:build js && wasm

package generatepage

import "github.com/monstercameron/GoWebComponents/v5/ui"

// anyNodes converts []ui.Node to []any so it can be spread into a
// shorthand tag call's `...any` variadic parameter. Go's `slice...`
// spread requires the slice's element type to match the variadic
// parameter's element type exactly — []ui.Node cannot be spread directly
// into a `...any` call even though every ui.Node satisfies any — so every
// html/shorthand.MapKeyed/MapKeyedComponent/MapKeyedIndexed result
// (all []ui.Node) needs this conversion before being spread as children
// alongside other arguments (h.ClassStr(...), h.OnClick(...), ...).
func anyNodes(nodes []ui.Node) []any {
	out := make([]any, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out
}
