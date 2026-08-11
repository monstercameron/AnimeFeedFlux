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
	// Items/Runs feed the tape and the 7-day spend column — see render.go's
	// itemsRes/runsRes doc comments for why they are fetched flat (one
	// request across every feed) rather than per-row.
	Items    fetch.AsyncResource[[]*affv1.Item]
	Runs     fetch.AsyncResource[[]*affv1.Run]
	Selected string
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

func renderRail(p railProps) ui.Node {
	t := deps.I18n
	wt := wui.T(t.T)
	fmtr := deps.Formatters
	rs := p.Resource.Get()
	feeds := rs.Value
	items := p.Items.Get().Value
	runs := p.Runs.Get().Value

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
			// not a stable feed id, so it reflects "row 1..N of what's on
			// screen" the way a physical sheet's numbers do.
			indexOf := make(map[int64]int, len(feeds))
			for i, f := range feeds {
				indexOf[f.GetId()] = i
			}
			listArgs := []any{h.ClassStr("af-rail__list")}
			listArgs = append(listArgs, anyNodes(h.MapKeyedComponent(feeds, func(f *affv1.Feed) any { return f.GetId() }, func(f *affv1.Feed) ui.Node {
				return renderRailRow(t, fmtr, f, indexOf[f.GetId()]+1, f.GetSlug() == p.Selected,
					itemsForFeed(items, f.GetId()), feedSpend(runs, f.GetId()),
					p.StaleThresholdMinutes,
					p.OnSelect, p.OnToggleEnabled, p.OnRunNow, p.OnDelete, p.OnHistory,
					p.KebabOpen == f.GetSlug(), p.OnKebabOpen)
			}))...)
			return []ui.Node{h.Ul(listArgs...)}
		},
	})

	return h.Aside(
		h.ClassStr("af-generate__rail"),
		h.Div(
			h.ClassStr("af-rail__header"),
			h.H2(h.Text(t.T("generate.rail.title"))),
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

func renderRailRow(t Translator, fmtr Formatters, f *affv1.Feed, rowNumber int, selected bool, feedItems []*affv1.Item, spend7d float64, staleThresholdMinutes int32, onSelect func(string), onToggle func(*affv1.Feed), onRunNow func(*affv1.Feed), onDelete func(*affv1.Feed), onHistory func(*affv1.Feed), kebabOpen bool, onKebabOpen func(string, bool)) ui.Node {
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
	runNowFn := onRunNow

	return h.Li(
		h.ClassStr(h.ClassMap(map[string]bool{
			"af-rail__row":           true,
			"af-rail__row--selected": selected,
			"af-rail__row--disabled": !enabled,
			"af-rail__row--stale":    stale,
		})),
		h.OnClick(func() { onSelect(slug) }),
		// The numbered gutter (docs/design-direction.md's anchor: a feed
		// row IS a timesheet frame) plus title/slug — kept in one head row
		// so the number reads as belonging to the frame beside it, not as a
		// stray column.
		h.Div(h.ClassStr("af-rail__row-head"),
			h.Span(h.ClassStr("af-rail__row-number"), h.Aria("hidden", "true"), h.Text(formatCount(rowNumber))),
			h.Div(h.ClassStr("af-rail__row-headtext"),
				h.Div(h.ClassStr("af-rail__row-title"), h.Text(f.GetTitle())),
				h.Div(h.ClassStr("af-rail__row-slug"), h.Text(t.T("generate.rail.slugPath", slug))),
			),
		),
		h.If(stale, h.Span(h.ClassStr("af-rail__stale-flag"), h.Text(t.T("generate.rail.stale")))),
		// The tape: this feed's recent publish history as ticks along time,
		// one per item, positioned by published_at
		// (docs/design-direction.md, "Signature: the tape") — the one
		// element this whole visual direction is built to earn.
		renderTape(feedItems, now),
		h.Div(h.ClassStr("af-rail__row-meta"),
			h.Span(h.Text(t.T("generate.common.labelValue", t.T("generate.rail.lastBuild"), lastBuildText))),
			// "Next run" honestly says it cannot compute a jittered time
			// today rather than showing a fabricated or nominal one — see
			// logic.go's JitteredRuns doc comment: the Feed proto carries
			// jitter_offset_seconds but no nominal next-fire-time field or
			// RPC to derive one from, which TODOS.md D2-09/PLAN.md §14.3
			// assume exists. Recorded in this task's final report as a
			// §12.3 assumption that did not hold.
			h.Span(h.Text(t.T("generate.common.labelValue", t.T("generate.rail.nextRun"), t.T("generate.rail.nextRunUnavailable")))),
			// 7-day spend, unlabeled: this page cannot import web/i18n (see
			// deps.go/doc.go) and no generate.rail.* catalogue key exists
			// for "7-day spend" today — inventing English text here would
			// be exactly the second, competing catalogue i18n.go's own doc
			// comment warns against, and this task's file scope excludes
			// web/i18n, where the key would have to be added. Shown as a
			// bare tabular currency figure (matching the sampler's own
			// unlabeled-adjacent-to-labelled convention is not available
			// either, so this is a known gap — see this task's final
			// report) rather than omitted outright, since PLAN.md's "make
			// spending money deliberate" still wants the number on screen.
			h.Span(h.ClassStr("af-rail__row-spend"), h.Text(fmtr.Currency(spend7d))),
		),
		// The toggle/Run Now controls sit inside the row's own click target
		// (the whole <li> selects the feed on click) — wui.Toggle/wui.Button
		// take a func() OnClick/OnChange with no DOM event to call
		// StopPropagation on, so there is no way to ask the CONTROL itself
		// to stop the bubble (a real web/ui gap for "interactive control
		// nested in a clickable row" — see this task's final report). This
		// wrapper div intercepts the bubbled click instead: by the time a
		// click reaches an ancestor's handler, the target control's own
		// handler has already run (DOM "at target" phase happens before
		// bubbling), so stopping propagation here is equivalent to calling
		// StopPropagation on the control and needs no web/ui change.
		h.Div(h.ClassStr("af-rail__row-actions"), h.OnClick(func(ev ui.Event) { ev.StopPropagation() }),
			wui.Toggle(wui.ToggleProps{
				T: wt, ID: "generate-rail-toggle-" + slug, LabelKey: "generate.rail.enabledLabel",
				Checked: enabled, OnChange: func(bool) { toggleFn(feed) },
			}),
			wui.Button(wui.ButtonProps{
				T: wt, ID: "generate-rail-runnow-" + slug, LabelKey: "generate.rail.runNow",
				Variant: wui.ButtonSecondary, OnClick: func() { runNowFn(feed) },
			}),
			// Delete lives behind the ⋯, never as a button beside Run Now
			// (§12.6 / D0-15): the two would sit adjacent in a dense list of
			// rows that all look alike.
			wui.Kebab(wui.KebabProps{
				T: wt, ID: "generate-rail-kebab-" + slug,
				LabelKey: "generate.rail.actionsFor", LabelArgs: []any{f.GetTitle()},
				Open:         kebabOpen,
				OnOpenChange: func(open bool) { onKebabOpen(slug, open) },
				Items: []wui.KebabItem{
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

// feedSpend sums Run.EstCostUsd (the only cost figure this page has any RPC
// for — Feed and SystemService carry none, see render.go's runsRes doc
// comment) for one feed across whatever window runsRes was itself fetched
// over (tapeWindowDays, currently 14 — "7-day spend" per the task brief is
// approximated by this same window rather than a second, narrower RPC).
func feedSpend(runs []*affv1.Run, feedID int64) float64 {
	if feedID == 0 {
		return 0
	}
	var total float64
	for _, r := range runs {
		if r.GetFeedId() == feedID {
			total += r.GetEstCostUsd()
		}
	}
	return total
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
