//go:build js && wasm

// nodeutil.go's helpers are browser-side only (js && wasm), so tests that
// touch them cannot live in the package's untagged test files — those build
// for the host, where viewTabID does not exist.
package generatepage

import "testing"

// TestCandidateViewIDsRoundTripIncludingEmbed: web/ui.Tabs hands back the
// tab's DOM id, so a view whose id does not round-trip silently selects
// something else — and the default branch means it would silently select
// "Rendered" rather than failing.
func TestCandidateViewIDsRoundTripIncludingEmbed(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range CandidateViews {
		id := viewTabID(v)
		if id == "view-unknown" {
			t.Errorf("view %d has no stable tab id", v)
		}
		if seen[id] {
			t.Errorf("two views share the tab id %q", id)
		}
		seen[id] = true
		if got := viewFromTabID(id); got != v {
			t.Errorf("tab id %q round-tripped to view %d, want %d", id, got, v)
		}
	}
	if !seen["view-embed"] {
		t.Error("the embed view is missing from the tab strip")
	}
}
