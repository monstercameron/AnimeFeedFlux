//go:build js

package history

import (
	"strconv"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// filters_ui.go is the Runs tab's filter row.
//
// PLAN.md §12.4 asks for "filter by feed, status, date range". RunFilter has
// carried all four fields since it was written, and BuildRunHistoryRequest
// has always forwarded them — but the only control ever rendered was a bare
// `<input type="number">` bound to FeedID. So two thirds of the specified
// filtering was unreachable, and the third that existed asked the operator
// to type a database id that appears nowhere on the page it filters. Feed
// ids are not shown in the runs table, in the feed picker on /generate, or
// anywhere in the CLI's default output.
//
// The row is built as label-above-control pairs rather than the previous
// label-beside-control, because a wrapping flex row of pairs breaks between
// a label and its own field at exactly the width where it matters.

// runStatusChoices are the filter's status options, in the order an operator
// looks for them: the failures first (the reason anyone opens this page),
// then the successes, then the two states that are not outcomes. SKIPPED is
// included deliberately — it is the status a budget-capped run gets (§13),
// and "show me what the budget stopped" is a question this page should be
// able to answer. RUN_STATUS_UNSPECIFIED is the zero value and means "any",
// so it is the placeholder rather than a choice of its own.
var runStatusChoices = []affv1.RunStatus{
	affv1.RunStatus_RUN_STATUS_FAILED,
	affv1.RunStatus_RUN_STATUS_SUCCEEDED,
	affv1.RunStatus_RUN_STATUS_SKIPPED,
	affv1.RunStatus_RUN_STATUS_RUNNING,
}

// runFiltersProps is the row's state and the callbacks that change it.
type runFiltersProps struct {
	T       Catalog
	Filter  RunFilter
	Feeds   []*affv1.Feed
	OnApply func(RunFilter)
}

// renderRunFilters draws the row. Every control writes a whole RunFilter
// back through OnApply, so the caller has one place to react to a filter
// change rather than four.
func renderRunFilters(p runFiltersProps) ui.Node {
	t := p.T

	feedOpts := make([]any, 0, len(p.Feeds)+4)
	feedOpts = append(feedOpts,
		h.ID("history-runs-feed-filter"),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
			f := p.Filter
			f.FeedID = parseInt64(e.GetValue())
			p.OnApply(f)
		})),
		// Always rendered, selected or not: dropping the "any" option once a
		// feed is chosen shifts every subsequent option's index, and the
		// browser restores its selection BY INDEX across a re-render.
		h.Option(h.Value("0"), h.SelectedIf(p.Filter.FeedID == 0), t.T("history.runs.filter_feed_any", nil)),
	)
	for _, f := range p.Feeds {
		id := f.GetId()
		feedOpts = append(feedOpts, h.Option(
			h.Value(int64Str(id)),
			h.SelectedIf(id == p.Filter.FeedID),
			h.Text(f.GetTitle()),
		))
	}

	statusOpts := make([]any, 0, len(runStatusChoices)+4)
	statusOpts = append(statusOpts,
		h.ID("history-runs-status-filter"),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
			f := p.Filter
			f.Status = affv1.RunStatus(affv1.RunStatus_value[e.GetValue()])
			p.OnApply(f)
		})),
		h.Option(
			h.Value(affv1.RunStatus_RUN_STATUS_UNSPECIFIED.String()),
			h.SelectedIf(p.Filter.Status == affv1.RunStatus_RUN_STATUS_UNSPECIFIED),
			t.T("history.runs.filter_status_any", nil),
		),
	)
	for _, c := range runStatusChoices {
		statusOpts = append(statusOpts, h.Option(
			h.Value(c.String()),
			h.SelectedIf(c == p.Filter.Status),
			h.Text(runStatusLabel(t, c)),
		))
	}

	return h.Div(
		h.ClassStr("history-filters"),
		filterField("history-runs-feed-filter", t.T("history.runs.filter_feed", nil), h.Select(feedOpts...)),
		filterField("history-runs-status-filter", t.T("history.runs.filter_status", nil), h.Select(statusOpts...)),
		filterField("history-runs-after", t.T("history.runs.filter_after", nil), h.Input(
			h.ID("history-runs-after"), h.Type("date"),
			h.Value(dateInputValue(p.Filter.StartedAfter)),
			h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
				f := p.Filter
				f.StartedAfter = parseDateInput(e.GetValue())
				p.OnApply(f)
			})),
		)),
		filterField("history-runs-before", t.T("history.runs.filter_before", nil), h.Input(
			h.ID("history-runs-before"), h.Type("date"),
			h.Value(dateInputValue(p.Filter.StartedBefore)),
			h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
				f := p.Filter
				// The END of the chosen day, not its midnight: a range whose
				// "before" bound silently excludes everything that happened
				// on the day the operator picked reads as missing data.
				f.StartedBefore = endOfDay(parseDateInput(e.GetValue()))
				p.OnApply(f)
			})),
		)),
		// Only offered when something is actually filtered, so the row does
		// not carry a permanently dead control.
		h.Show(!p.Filter.IsZero(), h.Button(
			h.Type("button"),
			h.ClassStr("history-filters__clear"),
			h.OnClick(ui.UseEvent(func() { p.OnApply(RunFilter{}) })),
			t.T("history.runs.filter_clear", nil),
		)),
	)
}

// int64Str formats a feed id for an option value.
func int64Str(v int64) string { return strconv.FormatInt(v, 10) }

// filterField is one label-above-control pair.
func filterField(id, label string, control ui.Node) ui.Node {
	return h.Div(
		h.ClassStr("history-filters__field"),
		h.Label(h.For(id), h.Text(label)),
		control,
	)
}

// IsZero reports whether the filter is entirely unset — used to decide
// whether a "clear" control has anything to do.
func (f RunFilter) IsZero() bool {
	return f.FeedID == 0 &&
		f.Status == affv1.RunStatus_RUN_STATUS_UNSPECIFIED &&
		f.StartedAfter == nil &&
		f.StartedBefore == nil
}

// dateInputValue formats a time for `<input type="date">`, whose value is
// always YYYY-MM-DD. A nil bound renders empty, which is how the control
// shows "unset".
func dateInputValue(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// parseDateInput reads that same format back. An empty or unparseable value
// clears the bound rather than erroring: the only way to produce one from a
// date input is to clear it.
func parseDateInput(v string) *time.Time {
	if v == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil
	}
	return &t
}

// endOfDay moves a date bound to 23:59:59.999999999 of that day.
func endOfDay(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	e := t.Add(24*time.Hour - time.Nanosecond)
	return &e
}

// renderItemFeedFilter is the Items tab's feed filter. ItemFilter.FeedID and
// BuildItemListRequest have always forwarded it; §12.4 has always asked for
// it; no control ever existed.
func renderItemFeedFilter(t Catalog, feeds []*affv1.Feed, selected int64, onChange func(ui.InputEvent)) ui.Node {
	opts := make([]any, 0, len(feeds)+4)
	opts = append(opts,
		h.ID("history-items-feed"),
		h.OnChange(ui.UseEvent(onChange)),
		// Unconditional placeholder — see renderRunFilters for the
		// index-shifting trap this avoids.
		h.Option(h.Value("0"), h.SelectedIf(selected == 0), t.T("history.runs.filter_feed_any", nil)),
	)
	for _, f := range feeds {
		id := f.GetId()
		opts = append(opts, h.Option(h.Value(int64Str(id)), h.SelectedIf(id == selected), h.Text(f.GetTitle())))
	}
	return filterField("history-items-feed", t.T("history.runs.filter_feed", nil), h.Select(opts...))
}
