//go:build js && wasm

// render_rail.go implements the left rail pane (PLAN.md §12.3, TODOS.md
// D2-01..05): per-feed status, last build, next run, item count, 7-day
// spend, stale flag, enable toggle, Run Now, new feed — across all six
// states of the D-FLOW matrix.
package generatepage

import (
	"time"

	"github.com/monstercameron/GoWebComponents/v5/fetch"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

type railProps struct {
	Connected       bool
	Resource        fetch.AsyncResource[[]*affv1.Feed]
	Stats           fetch.AsyncResource[*affv1.SystemServiceStatsResponse]
	Selected        string
	OnSelect        func(slug string)
	OnNew           func()
	OnToggleEnabled func(*affv1.Feed)
	OnRunNow        func(*affv1.Feed)
}

func renderRail(p railProps) ui.Node {
	t := deps.I18n
	fmtr := deps.Formatters
	rs := p.Resource.Get()
	feeds := rs.Value

	killSwitchReason := ""
	if p.Stats.Get().Ready && !p.Stats.Get().Value.GetGenerationEnabled() {
		killSwitchReason = t.T("generate.rail.killSwitchActive")
	}

	state := SelectListState(p.Connected, "", rs.Loading, rs.Error, rs.Ready && len(feeds) == 0)

	var body ui.Node
	switch state {
	case ListDisconnected:
		body = h.P(h.ClassStr("af-rail__status"), h.Text(t.T("generate.rail.disconnected")))
	case ListLoading:
		body = h.P(h.ClassStr("af-rail__status"), h.Text(t.T("generate.rail.loading")))
	case ListError:
		body = h.P(h.ClassStr("af-rail__status af-rail__status--error"), h.Textf("%s: %v", t.T("generate.rail.error"), rs.Error))
	case ListEmpty:
		body = h.P(h.ClassStr("af-rail__status"), h.Text(t.T("generate.rail.empty")))
	default:
		listArgs := []any{h.ClassStr("af-rail__list")}
		listArgs = append(listArgs, anyNodes(h.MapKeyedComponent(feeds, func(f *affv1.Feed) any { return f.GetId() }, func(f *affv1.Feed) ui.Node {
			return renderRailRow(t, fmtr, f, f.GetSlug() == p.Selected, p.OnSelect, p.OnToggleEnabled, p.OnRunNow)
		}))...)
		body = h.Ul(listArgs...)
	}

	return h.Aside(
		h.ClassStr("af-generate__rail"),
		h.Div(
			h.ClassStr("af-rail__header"),
			h.H2(h.Text(t.T("generate.rail.title"))),
			h.Button(h.Type("button"), h.OnClick(func() { p.OnNew() }), h.Text(t.T("generate.rail.newFeed"))),
		),
		h.If(killSwitchReason != "", h.Div(
			h.ClassStr("af-rail__kill-banner"),
			h.Text(killSwitchReason),
		)),
		body,
	)
}

func renderRailRow(t Translator, fmtr Formatters, f *affv1.Feed, selected bool, onSelect func(string), onToggle func(*affv1.Feed), onRunNow func(*affv1.Feed)) ui.Node {
	now := time.Now()
	var lastBuilt *time.Time
	if ts := f.GetLastBuiltAt(); ts != nil {
		tm := ts.AsTime()
		lastBuilt = &tm
	}
	stale := IsStale(f.GetEnabled(), lastBuilt, now, defaultStaleThresholdMinutes)

	lastBuildText := t.T("generate.rail.neverBuilt")
	if lastBuilt != nil {
		lastBuildText = fmtr.RelativeTime(*lastBuilt, now)
	}

	return h.Li(
		h.ClassStr(h.ClassMap(map[string]bool{
			"af-rail__row":           true,
			"af-rail__row--selected": selected,
			"af-rail__row--disabled": !f.GetEnabled(),
			"af-rail__row--stale":    stale,
		})),
		h.OnClick(func() { onSelect(f.GetSlug()) }),
		h.Div(h.ClassStr("af-rail__row-title"), h.Text(f.GetTitle())),
		h.Div(h.ClassStr("af-rail__row-slug"), h.Textf("/%s", f.GetSlug())),
		h.If(stale, h.Span(h.ClassStr("af-rail__stale-flag"), h.Text(t.T("generate.rail.stale")))),
		h.Div(h.ClassStr("af-rail__row-meta"),
			h.Span(h.Textf("%s: %s", t.T("generate.rail.lastBuild"), lastBuildText)),
			// "Next run" honestly says it cannot compute a jittered time
			// today rather than showing a fabricated or nominal one — see
			// logic.go's JitteredRuns doc comment: the Feed proto carries
			// jitter_offset_seconds but no nominal next-fire-time field or
			// RPC to derive one from, which TODOS.md D2-09/PLAN.md §14.3
			// assume exists. Recorded in this task's final report as a
			// §12.3 assumption that did not hold.
			h.Span(h.Textf("%s: %s", t.T("generate.rail.nextRun"), t.T("generate.rail.nextRunUnavailable"))),
		),
		h.Div(h.ClassStr("af-rail__row-actions"),
			h.Button(h.Type("button"), h.OnClick(func(ev ui.Event) { ev.StopPropagation(); onToggle(f) }),
				h.Text(t.T(enableToggleKey(f.GetEnabled())))),
			h.Button(h.Type("button"), h.OnClick(func(ev ui.Event) { ev.StopPropagation(); onRunNow(f) }),
				h.Text(t.T("generate.rail.runNow"))),
		),
	)
}

func enableToggleKey(enabled bool) string {
	if enabled {
		return "generate.rail.disable"
	}
	return "generate.rail.enable"
}

const defaultStaleThresholdMinutes = 24 * 60
