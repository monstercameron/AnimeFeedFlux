//go:build js

package history

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/router"

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

// tabFromSlug maps a URL segment to a tab. Anything unrecognised — including
// the empty segment at bare /history — is Runs, which is the tab this page
// opens on.
func tabFromSlug(slug string) historyTab {
	if historyTab(slug) == tabItems {
		return tabItems
	}
	return tabRuns
}

// FeedRunsPath is the address of one feed's run history. Exported so the
// pages that link here (the feed rows on /generate) and the tab that reads it
// cannot disagree about the spelling.
func FeedRunsPath(feedID int64) string {
	return TabPath(tabRuns) + "?feed=" + strconv.FormatInt(feedID, 10)
}

// TabPath is the address of one tab. Exported so a runbook or a bookmark can
// name it, and so the tab buttons and the router cannot disagree about the
// spelling.
func TabPath(tab historyTab) string { return "/history/" + string(tab) }

// History is the root component for the /history route (PLAN.md §12.4,
// TODOS.md D3-01: "Two tabs over one page: Runs and Items").
func History(props RootProps) ui.Node {
	// The active tab is READ FROM THE URL, not held as state.
	//
	// It was ui.UseState(tabRuns), so the tab existed only inside this
	// component: a reload always landed on Runs however you got there, the
	// back button walked out of the page instead of back to the other tab,
	// and there was no way to send anyone a link to the Items list. Settings
	// already worked this way (/settings/provider IS the provider panel);
	// this is the same treatment.
	loc := router.UseRoute()
	nav := router.UseNavigate()
	activeTab := tabFromSlug(loc.Param("tab"))

	// Replace, not Push, so switching tabs does not build a history stack of
	// tab flips that the back button then has to be clicked through.
	selectRuns := ui.UseEvent(func() { nav.Replace(TabPath(tabRuns)) })
	selectItems := ui.UseEvent(func() { nav.Replace(TabPath(tabItems)) })

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
			historyTabButton(props.T, tabRuns, activeTab, selectRuns),
			historyTabButton(props.T, tabItems, activeTab, selectItems),
		),
		h.If(activeTab == tabRuns, ui.CreateElement(RunsTab, RunsTabProps{
			Client:       props.Runs,
			Feeds:        props.Feeds,
			T:            props.T,
			Disconnected: props.Disconnected,
			Ready:        props.Ready,
		})),
		h.If(activeTab == tabItems, ui.CreateElement(ItemsTab, ItemsTabProps{
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
