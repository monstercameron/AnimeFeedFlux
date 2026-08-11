//go:build js && wasm

package generatepage

import (
	"github.com/monstercameron/GoWebComponents/v5/ui"

	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

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

// uiListState maps this package's own ListState onto web/ui's ListState so
// wui.StatePanel (adopted in render_rail.go/render_sampler.go) can render
// the already-decided state. The PRECEDENCE decision itself still lives in,
// and is still host-tested from, logic.go's SelectListState — this is a
// pure relabeling for the render layer, not a second copy of that logic.
func uiListState(s ListState) wui.ListState {
	switch s {
	case ListLoading:
		return wui.StateLoading
	case ListEmpty:
		return wui.StateEmpty
	case ListError:
		return wui.StateError
	case ListDisabledWithReason:
		return wui.StateDisabledWithReason
	case ListDisconnected:
		return wui.StateDisconnected
	default:
		return wui.StatePopulated
	}
}

// viewTabID/viewFromTabID give each CandidateView a short, stable DOM id for
// web/ui.Tabs' Tab.ID/PanelID wiring — an HTML id token, not prose, kept
// separate from CandidateView.TranslationKey() (types.go), which is the
// i18n key for the same view's visible tab label.
func viewTabID(v CandidateView) string {
	switch v {
	case ViewRendered:
		return "view-rendered"
	case ViewRawFields:
		return "view-raw-fields"
	case ViewFeedXML:
		return "view-feed-xml"
	case ViewSlackCard:
		return "view-slack-card"
	default:
		return "view-unknown"
	}
}

func viewFromTabID(id string) CandidateView {
	switch id {
	case "view-rendered":
		return ViewRendered
	case "view-raw-fields":
		return ViewRawFields
	case "view-feed-xml":
		return ViewFeedXML
	case "view-slack-card":
		return ViewSlackCard
	default:
		return ViewRendered
	}
}

// candidateTabLabelKey returns the i18n key for the Nth (0-indexed)
// candidate tab's label. web/ui.Tab has no LabelArgs field (see this task's
// final report: Tabs cannot interpolate an ordinal into one shared key), so
// each position 1-5 gets its own static catalogue key instead of one key
// plus an interpolated number — bounded by ValidSampleSize's 1-5 range
// (logic.go), which is also SampleStream's own request-size ceiling, so a
// sixth candidate can never occur here.
func candidateTabLabelKey(index int) string {
	switch index {
	case 0:
		return "generate.sampler.candidateTab.1"
	case 1:
		return "generate.sampler.candidateTab.2"
	case 2:
		return "generate.sampler.candidateTab.3"
	case 3:
		return "generate.sampler.candidateTab.4"
	default:
		return "generate.sampler.candidateTab.5"
	}
}
