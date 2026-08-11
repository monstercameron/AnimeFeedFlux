//go:build js

package history

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// RootProps wires the History page to its dependencies. Nothing here
// imports web/i18n or web/wsconn directly — see doc.go for why, and for
// what those packages need to satisfy.
type RootProps struct {
	Runs  RunsClient
	Items ItemsClient
	Feeds FeedsClient
	T     Catalog

	// Ready reports that the session can actually carry an RPC (AUTH or
	// KILLED). It is NOT the negation of Disconnected: appstate.Anon is the
	// zero value, so `state != Disconnected` is already true before there is
	// any session at all. A tab that loads on mount without consulting this
	// fires its request into a socket that is still handshaking, which is how
	// this page came to sit on "Loading…" forever whenever it was opened
	// directly rather than navigated to.
	Ready bool

	// Disconnected mirrors the shell's DISCONNECTED application state
	// (TODOS.md D-FLOW); this page never dials the socket itself.
	Disconnected bool
	// DisabledReason, non-empty, means the KILLED application state (or
	// any other whole-page disablement the shell decides) applies here —
	// rendered instead of the tabs entirely per D-FLOW's
	// disabled-with-reason state.
	DisabledReason string
}

type historyTab string

const (
	tabRuns  historyTab = "runs"
	tabItems historyTab = "items"
)

// History is the root component for the /history route (PLAN.md §12.4,
// TODOS.md D3-01: "Two tabs over one page: Runs and Items").
func History(props RootProps) ui.Node {
	activeTab := ui.UseState(tabRuns)

	selectRuns := ui.UseEvent(func() { activeTab.Set(tabRuns) })
	selectItems := ui.UseEvent(func() { activeTab.Set(tabItems) })

	if props.DisabledReason != "" {
		return h.Main(
			h.ClassStr("history-page history-page--disabled"),
			h.H1(props.T.T("history.title", nil)),
			h.P(h.ClassStr("history-disabled-reason"), props.DisabledReason),
		)
	}

	return h.Main(
		h.ClassStr("history-page"),
		h.H1(props.T.T("history.title", nil)),
		h.Div(
			h.ClassStr("history-tabs"),
			h.Attr("role", "tablist"),
			historyTabButton(props.T, tabRuns, activeTab.Get(), selectRuns),
			historyTabButton(props.T, tabItems, activeTab.Get(), selectItems),
		),
		h.If(activeTab.Get() == tabRuns, ui.CreateElement(RunsTab, RunsTabProps{
			Client:       props.Runs,
			Feeds:        props.Feeds,
			T:            props.T,
			Disconnected: props.Disconnected,
			Ready:        props.Ready,
		})),
		h.If(activeTab.Get() == tabItems, ui.CreateElement(ItemsTab, ItemsTabProps{
			Client:       props.Items,
			Feeds:        props.Feeds,
			T:            props.T,
			Disconnected: props.Disconnected,
			Ready:        props.Ready,
		})),
	)
}

func historyTabButton(t Catalog, tab historyTab, active historyTab, onClick ui.Handler) ui.Node {
	key := "history.tabs.runs"
	if tab == tabItems {
		key = "history.tabs.items"
	}
	classes := "history-tab"
	selected := "false"
	if tab == active {
		classes += " history-tab--active"
		selected = "true"
	}
	return h.Button(
		h.Type("button"),
		h.ClassStr(classes),
		h.Attr("role", "tab"),
		h.Attr("aria-selected", selected),
		h.OnClick(onClick),
		t.T(key, nil),
	)
}
