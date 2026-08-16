//go:build js && wasm

// render_rail.go implements the left rail pane (PLAN.md §12.3, TODOS.md
// D2-01..05): per-feed status, last build, next run, item count, 7-day
// spend, stale flag, enable toggle, Run Now, new feed — across all six
// states of the D-FLOW matrix.
//
// Adopted from web/ui (this task): the six-state switch renders through
// wui.StatePanel/uiListState (nodeutil.go) instead of a hand-rolled
// per-state switch — the PRECEDENCE decision is still SelectListState in
// logic.go, host-tested and unchanged; StatePanel only renders whichever
// state that function already picked. The per-row enable control is now
// wui.Toggle (a real switch, not a verb button) and Run Now/New Feed are
// wui.Button. See render_editor.go's package doc comment for this file's
// hook-ordering discipline — none of the controls here ever vary their
// Disabled state across renders (Toggle/Button here are always enabled),
// so none of them need the isolation render_editor.go's slug field and
// render_sampler.go's controls block require; see those files' comments.
package generatepage

import (
	"time"

	"github.com/monstercameron/GoWebComponents/v5/fetch"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

type railProps struct {
	Connected bool
	// StaleThresholdMinutes is settings.generation.staleness_threshold_minutes.
	//
	// It was a write-only setting: stored, editable, displayed by `aff system`,
	// and read by nothing — this rail used a hardcoded 24h constant instead, so
	// an operator lowering the threshold to catch a stalling feed changed a
	// number on a screen and nothing about which rows flagged stale (A5-01).
	// Zero means unset and falls back to that same constant, so an untouched
	// deployment behaves exactly as before.
	StaleThresholdMinutes int32
	Resource              fetch.AsyncResource[[]*affv1.Feed]
	Stats                 fetch.AsyncResource[*affv1.SystemServiceStatsResponse]
	Selected              string
	// Err is the last toggle-enabled/Run Now mutation failure, distinct
	// from Resource.Error (the list load) — TODOS.md D0-10: a mutation
	// failure and a load failure are different things and must not share
	// one error slot, or a stale load error could mask (or be masked by) a
	// fresh mutation refusal.
	Err             error
	OnSelect        func(slug string)
	OnNew           func()
	OnToggleEnabled func(*affv1.Feed)
	OnRunNow        func(*affv1.Feed)
	// OnDelete removes a feed. It exists because FeedService.Delete has been
	// implemented, version-checked and tested on the server since the RPC
	// layer was written, and no UI anywhere ever called it: a feed created by
	// mistake, or one whose subject died, could be disabled but never removed.
	// The server's delete is a soft one (deleted_at), but there is no Restore
	// RPC for feeds, so from this screen it is irreversible — hence the typed
	// confirmation at the call site.
	OnDelete func(*affv1.Feed)
	// OnHistory opens one feed's run history (/history/runs?feed=...).
	OnHistory func(*affv1.Feed)
	// KebabOpen/OnKebabOpen track which row's overflow menu is open, by slug.
	// Held by the caller rather than per row so opening one closes another.
	KebabOpen   string
	OnKebabOpen func(slug string, open bool)
}

// renderRail is the persistent sidebar (render_workbench.go's two-column
// shell): a compact, independently-scrolling feed list, sibling to the main
// content column rather than a section stacked above/inside it. It fills the
// viewport height on its own (styles.go's `.af-gen__sidebar`) and scrolls
// natively — no cap, no pager. That replaced BOTH earlier attempts at "what
// if you had 20 feeds" (a nested `overflow-y:auto` scrollbox, then
// client-side pagination): a sidebar that just scrolls, the way an inbox
// list does, needs neither, and is the one shape that does not fight the
// page's own scroll — it is not stacked in the same vertical flow as the
// page at all, so there is nothing for it to fight.
func renderRail(p railProps) ui.Node {
	t := deps.I18n
	wt := wui.T(t.T)
	fmtr := deps.Formatters
	rs := p.Resource.Get()
	feeds := rs.Value

	killSwitchReason := ""
	if p.Stats.Get().Ready && !p.Stats.Get().Value.GetGenerationEnabled() {
		killSwitchReason = t.T("generate.rail.killSwitchActive")
	}

	state := SelectListState(p.Connected, "", rs.Loading, rs.Error, rs.Ready && len(feeds) == 0)

	// wui.StatePanel is called inline (not isolated via ui.CreateElement):
	// every one of its non-populated branches (loading/empty/disconnected,
	// and error since OnRetry is always nil here) registers zero On*
	// hooks, and its populated branch's row list is itself hook-isolated
	// per row via h.MapKeyedComponent below — so which branch fires this
	// render never shifts a hook slot in THIS (renderRail's) fiber.
	body := wui.StatePanel(wui.StatePanelProps{
		T:               wt,
		State:           uiListState(state),
		ErrorKey:        "generate.common.labelValue",
		ErrorArgs:       []any{t.T("generate.rail.error"), errorText(rs.Error)},
		EmptyKey:        "generate.rail.empty",
		ReconnectingKey: "generate.rail.disconnected",
		Populated: func() []ui.Node {
			// indexOf backs the numbered gutter (docs/design-direction.md's
			// anchor: "a numbered gutter on sequences" — the rail's own
			// row-order is one, same as a timesheet's frame numbers) —
			// position in the already-loaded, already-ordered feeds slice,
			// not a stable feed id, so it reflects "row 1..N" the way a
			// physical sheet's numbers do.
			listArgs := []any{h.ClassStr("af-rail__list")}
			listArgs = append(listArgs, anyNodes(h.MapKeyedComponent(feeds, func(f *affv1.Feed) any { return f.GetId() }, func(f *affv1.Feed) ui.Node {
				idx := 0
				for i, ff := range feeds {
					if ff.GetId() == f.GetId() {
						idx = i
						break
					}
				}
				return renderRailRow(t, fmtr, f, idx+1, f.GetSlug() == p.Selected,
					p.StaleThresholdMinutes, p.OnSelect, p.OnToggleEnabled,
					p.KebabOpen == f.GetSlug(), p.OnKebabOpen,
					p.OnRunNow, p.OnDelete, p.OnHistory)
			}))...)
			return []ui.Node{h.Ul(listArgs...)}
		},
	})

	return h.Aside(
		h.ClassStr("af-gen__sidebar"),
		h.Div(
			h.ClassStr("af-rail__header"),
			h.Span(h.ClassStr("af-rail__header-title"), t.T("generate.workbench.feedsSummary", formatCount(len(feeds)))),
			wui.Button(wui.ButtonProps{T: wt, ID: "generate-rail-new", LabelKey: "generate.rail.newFeed", Variant: wui.ButtonPrimary, OnClick: p.OnNew}),
		),
		h.If(killSwitchReason != "", h.Div(
			h.ClassStr("af-rail__kill-banner"),
			h.Role("alert"),
			h.Text(killSwitchReason),
		)),
		h.If(p.Err != nil, h.Div(
			h.ClassStr("af-rail__action-error"),
			h.Role("alert"),
			h.Text(mutationErrorText(t, p.Err)),
		)),
		body,
	)
}

// renderRailRow is ONE compact row in the sidebar — two lines, not the
// six-field detail card this used to be. The tape, the stale-flag CHIP
// (staleness itself still shows, as a dot), "Next run: unavailable" (which
// always said exactly that — see the removed comment this file used to
// carry about the missing jittered-time RPC — showing a fact that is never
// anything but "unavailable" earns no space in a compact row) and the 7-day
// spend figure all moved OUT of the row: the tape and stale flag now render
// once, prominently, in the main column's header for whichever feed is
// actually loaded (render.go, docs/design-direction.md's "spend the
// boldness [on the tape]" — spending it on the ONE feed being worked on,
// not diluted across N rows in a 20rem-wide list, is truer to that
// direction than the old wide-card version ever was). Run Now moved into
// this row's own kebab alongside History/Delete — it duplicates the
// strip's Run Now once a feed is selected, but a feed you have NOT selected
// still needs a way to run it without switching to it first.
func renderRailRow(t Translator, fmtr Formatters, f *affv1.Feed, rowNumber int, selected bool, staleThresholdMinutes int32, onSelect func(string), onToggle func(*affv1.Feed), kebabOpen bool, onKebabOpen func(string, bool), onRunNow, onDelete, onHistory func(*affv1.Feed)) ui.Node {
	wt := wui.T(t.T)
	now := time.Now()
	var lastBuilt *time.Time
	if ts := f.GetLastBuiltAt(); ts != nil {
		tm := ts.AsTime()
		lastBuilt = &tm
	}
	threshold := staleThresholdMinutes
	if threshold <= 0 {
		threshold = defaultStaleThresholdMinutes
	}
	stale := IsStale(f.GetEnabled(), lastBuilt, now, threshold)

	lastBuildText := t.T("generate.rail.neverBuilt")
	if lastBuilt != nil {
		lastBuildText = fmtr.RelativeTime(*lastBuilt, now)
	}

	slug := f.GetSlug()
	enabled := f.GetEnabled()
	feed := f
	toggleFn := onToggle

	return h.Li(
		h.ClassStr(h.ClassMap(map[string]bool{
			"af-rail__row":           true,
			"af-rail__row--selected": selected,
			"af-rail__row--disabled": !enabled,
			"af-rail__row--stale":    stale,
		})),
		h.OnClick(func() { onSelect(slug) }),
		// The status dot replaces the old row's stale-flag CHIP and the
		// numbered gutter's own column — one glance says "healthy / stale /
		// off" without spending a whole line on a pill. The number stays,
		// small, ahead of the title (docs/design-direction.md's anchor: a
		// feed row is a timesheet frame, and frame numbers lead the row).
		h.Span(h.ClassStr("af-rail__row-dot"), h.Aria("hidden", "true")),
		h.Div(h.ClassStr("af-rail__row-body"),
			h.Div(h.ClassStr("af-rail__row-title-line"),
				h.Span(h.ClassStr("af-rail__row-number"), h.Aria("hidden", "true"), h.Text(formatCount(rowNumber))),
				h.Span(h.ClassStr("af-rail__row-title"), h.Text(f.GetTitle())),
			),
			h.Div(h.ClassStr("af-rail__row-meta"),
				h.Text(t.T("generate.rail.compactMeta", slug, lastBuildText)),
			),
		),
		// The toggle/kebab sit inside the row's own click target (the whole
		// <li> selects the feed on click) — wui.Toggle/wui.Kebab take a
		// func() OnClick/OnChange with no DOM event to call
		// StopPropagation on, so there is no way to ask the CONTROL itself
		// to stop the bubble (a real web/ui gap). This wrapper div
		// intercepts the bubbled click instead: by the time a click reaches
		// an ancestor's handler, the target control's own handler has
		// already run (DOM "at target" phase happens before bubbling), so
		// stopping propagation here is equivalent to calling
		// StopPropagation on the control and needs no web/ui change.
		h.Div(h.ClassStr("af-rail__row-actions"), h.OnClick(func(ev ui.Event) { ev.StopPropagation() }),
			wui.Toggle(wui.ToggleProps{
				T: wt, ID: "generate-rail-toggle-" + slug, LabelKey: "generate.rail.enabledLabel",
				Checked: enabled, OnChange: func(bool) { toggleFn(feed) },
			}),
			wui.Kebab(wui.KebabProps{
				T: wt, ID: "generate-rail-kebab-" + slug,
				LabelKey: "generate.rail.actionsFor", LabelArgs: []any{f.GetTitle()},
				Open:         kebabOpen,
				OnOpenChange: func(open bool) { onKebabOpen(slug, open) },
				Items: []wui.KebabItem{
					{
						ID: "generate-rail-runnow-" + slug, LabelKey: "generate.rail.runNow",
						OnSelect: func() { onRunNow(feed) },
					},
					{
						ID: "generate-rail-history-" + slug, LabelKey: "generate.rail.history",
						OnSelect: func() { onHistory(feed) },
					},
					{
						ID: "generate-rail-delete-" + slug, LabelKey: "generate.rail.delete",
						Danger: true, OnSelect: func() { onDelete(feed) },
					},
				},
			}),
		),
	)
}

const defaultStaleThresholdMinutes = 24 * 60

// itemsForFeed filters the flat cross-feed items list (render.go's
// itemsRes) down to one feed, for renderTape below.
func itemsForFeed(items []*affv1.Item, feedID int64) []*affv1.Item {
	if feedID == 0 {
		return nil
	}
	out := make([]*affv1.Item, 0, len(items))
	for _, it := range items {
		if it.GetFeedId() == feedID {
			out = append(out, it)
		}
	}
	return out
}

// renderTape draws the signature element (docs/design-direction.md,
// "Signature: the tape"): a horizontal strip showing feedItems' publish
// history as ticks along time, one tick per item positioned by
// published_at within a fixed trailing window ending at now. Positioning is
// inline (percent `left`, via h.Style) rather than a CSS class per
// position, since the position is per-item data, not a fixed style — the
// class-level rules in styles.go only give the strip and each tick their
// SHAPE (the baseline rule, the tick's width/height/color), never their
// horizontal offset.
//
// The window is fixed (tapeWindowDays) rather than derived from the data's
// own span so a feed that stopped publishing renders as an increasingly
// empty strip over time, exactly the "a feed that stopped is instantly
// obvious" effect docs/design-direction.md calls out — a self-scaling
// window would instead keep re-centering on whatever the last tick was and
// never show the gap.
func renderTape(feedItems []*affv1.Item, now time.Time) ui.Node {
	windowStart := now.Add(-tapeWindowDays * 24 * time.Hour)
	windowSeconds := now.Sub(windowStart).Seconds()

	ticks := make([]ui.Node, 0, len(feedItems))
	for _, it := range feedItems {
		ts := it.GetPublishedAt()
		if ts == nil {
			continue
		}
		at := ts.AsTime()
		if at.Before(windowStart) || at.After(now) {
			continue
		}
		pct := at.Sub(windowStart).Seconds() / windowSeconds * 100
		ticks = append(ticks, h.Div(
			h.ClassStr("af-tape__tick"),
			h.Style(map[string]string{"left": tapePercent(pct)}),
		))
	}

	children := []any{
		h.ClassStr("af-tape"),
		h.Aria("hidden", "true"), // decorative alongside the textual last-build/stale state above, not a second source of truth
		h.Div(h.ClassStr("af-tape__baseline")),
	}
	children = append(children, anyNodes(ticks)...)
	return h.Div(children...)
}

// tapePercent formats a 0-100 position as a CSS percentage string, clamped
// at both ends so a boundary timestamp (published_at exactly at windowStart
// or now) never lands a tick fractionally outside the strip.
func tapePercent(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return formatCount(int(pct+0.5)) + "%"
}
