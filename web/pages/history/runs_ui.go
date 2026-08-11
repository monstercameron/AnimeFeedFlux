//go:build js

package history

import (
	"context"
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// RunsTabProps is what the Runs tab needs from its parent.
type RunsTabProps struct {
	Client       RunsClient
	Feeds        FeedsClient
	T            Catalog
	Disconnected bool
	// Ready — see RootProps.Ready.
	Ready bool
}

type runsLoadState struct {
	loading bool
	err     error
	runs    []*affv1.Run
	cursor  *PageCursor
	// nextTk is the token the server returned for the page AFTER the one
	// currently displayed, or "" when this is the last page. It is state,
	// not a cursor move: see the "load-ok" case below for why loading a
	// page must not itself advance the cursor.
	nextTk string
	logs   map[int64]string // populated by expand-row fetch
	expand map[int64]bool
}

type runsAction struct {
	kind   string
	err    error
	runs   []*affv1.Run
	nextTk string
	runID  int64
	log    string
}

func runsReducer(s runsLoadState, a runsAction) runsLoadState {
	switch a.kind {
	case "load-start":
		s.loading = true
		s.err = nil
	case "load-ok":
		s.loading = false
		s.runs = a.runs
		// Record the next page's token; do NOT advance.
		//
		// This used to call s.cursor.Advance(a.nextTk) here, which made
		// merely LOADING a page move the cursor onto the following one. The
		// visible consequences: Current() returned the next page's token
		// (so Refresh fetched the wrong page), HasPrevious() went true
		// immediately on first load (so "Previous" was live on page 1 and
		// simply reloaded it), and — because Advance was the only caller —
		// there was no forward control at all, leaving every page past the
		// first unreachable. Found 2026-08-10 by looking at the rendered
		// pager: "Previous" and "Refresh", no "Next".
		s.nextTk = a.nextTk
	case "next-page":
		s.cursor.Advance(a.nextTk)
	case "load-err":
		s.loading = false
		s.err = a.err
	case "toggle-expand":
		next := make(map[int64]bool, len(s.expand))
		for k, v := range s.expand {
			next[k] = v
		}
		next[a.runID] = !next[a.runID]
		s.expand = next
	case "log-loaded":
		next := make(map[int64]string, len(s.logs))
		for k, v := range s.logs {
			next[k] = v
		}
		next[a.runID] = a.log
		s.logs = next
	case "delete-ok":
		filtered := make([]*affv1.Run, 0, len(s.runs))
		for _, r := range s.runs {
			if r.Id != a.runID {
				filtered = append(filtered, r)
			}
		}
		s.runs = filtered
	}
	return s
}

// RunsTab renders the Runs tab (PLAN.md §12.4, TODOS.md D3-02..D3-07).
func RunsTab(props RunsTabProps) ui.Node {
	filter := ui.UseState(RunFilter{})
	store := ui.UseReducer(runsReducer, runsLoadState{cursor: NewPageCursor(), expand: map[int64]bool{}, logs: map[int64]string{}})
	pendingDelete := ui.UseState(int64(0))
	// actionErr is Delete's own mutation error (TODOS.md D0-10) — distinct
	// from s.err, which is the list load's error. deleteRun previously
	// discarded its RPC error entirely (`if err != nil { return }`), so a
	// disconnected delete and a rejected one both looked exactly like
	// nothing happened.
	actionErr := ui.UseState(error(nil))
	// See asyncdispatch.go: reducer dispatches made from a goroutine update
	// the state but schedule no render.
	pump := useRenderPump()

	load := func(pageToken string) {
		store.Dispatch(runsAction{kind: "load-start"})
		req := BuildRunHistoryRequest(filter.Get(), pageToken, DefaultPageSize)
		retryRead(
			func() (*affv1.RunServiceHistoryResponse, error) {
				return props.Client.History(context.Background(), req)
			},
			func(resp *affv1.RunServiceHistoryResponse, err error) {
				if err != nil {
					store.Dispatch(runsAction{kind: "load-err", err: err})
					pump.bump()
					return
				}
				store.Dispatch(runsAction{kind: "load-ok", runs: resp.Runs, nextTk: resp.NextPageToken})
				pump.bump()
			},
		)
	}

	// The feed list behind the filter menu. Loaded once the session is up,
	// and a failure is silent by design: the filter then offers "All feeds"
	// only, which is the unfiltered default and therefore still correct.
	feeds := ui.UseState([]*affv1.Feed(nil))
	// feedsSettled gates the runs load. See the effect below for why these two
	// requests must not be in flight at the same time.
	feedsSettled := ui.UseState(false)
	ui.UseEffect(func() func() {
		if !props.Ready || props.Feeds == nil {
			feedsSettled.Set(true)
			return nil
		}
		go func() {
			resp, err := props.Feeds.List(context.Background(), &affv1.FeedServiceListRequest{PageSize: 200})
			if err == nil {
				feeds.Set(resp.GetFeeds())
			}
			feedsSettled.Set(true)
		}()
		return nil
	}, props.Ready)

	// Keyed on Ready and on feedsSettled, so the run history is requested only
	// AFTER the feed list has come back.
	//
	// Not politeness — the two requests must not be in flight together on a
	// cold page load. Landing directly on /history fired both at once, and
	// RunService.History was reliably the one that never answered: no reply,
	// no error, and (because it is wedged inside a transport whose deadlines
	// are no-ops — see web/wsconn/clients.go) no timeout either. The feed list
	// beside it returned fine, the identical run query returned instantly on
	// the next click, and arriving at the same page by in-app navigation was
	// never affected. Serialising the pair makes a cold load behave like every
	// other path.
	//
	// This is a workaround at the page level for something wrong a layer down,
	// and it is filed as such — the tunnel should not drop a concurrent stream
	// during startup, and nothing here fixes that.
	ui.UseEffect(func() func() {
		if !props.Ready || !feedsSettled.Get() {
			return nil
		}
		store.Get().cursor.Reset()
		load("")
		return nil
	}, filter.Get(), props.Ready, feedsSettled.Get())

	toggleExpand := func(runID int64) {
		wasExpanding := !store.Get().expand[runID]
		store.Dispatch(runsAction{kind: "toggle-expand", runID: runID})
		if wasExpanding && store.Get().logs[runID] == "" {
			go func() {
				resp, err := props.Client.Get(context.Background(), &affv1.RunServiceGetRequest{RunId: runID})
				if err != nil {
					// A failed log fetch used to render as an EMPTY log,
					// which reads as "this run logged nothing" — the single
					// most misleading thing this row can say when someone has
					// expanded it specifically to find out why a run failed.
					store.Dispatch(runsAction{kind: "log-loaded", runID: runID,
						log: props.T.T("history.runs.log_unavailable", nil)})
					pump.bump()
					return
				}
				store.Dispatch(runsAction{kind: "log-loaded", runID: runID, log: resp.Log})
				pump.bump()
			}()
		}
	}

	// deleteRun is irreversible (proto/aff/v1/run.proto: RunService.Delete
	// carries no expected_version and there is no Restore RPC for runs,
	// unlike items), so it only fires from the typed-confirmation dialog
	// below — requestDelete (passed to the table) just opens it.
	deleteRun := func(runID int64) {
		actionErr.Set(nil)
		go func() {
			_, err := props.Client.Delete(context.Background(), &affv1.RunServiceDeleteRequest{RunId: runID})
			if err != nil {
				actionErr.Set(err)
				return
			}
			store.Dispatch(runsAction{kind: "delete-ok", runID: runID})
			pump.bump()
		}()
	}
	requestDelete := func(runID int64) { actionErr.Set(nil); pendingDelete.Set(runID) }

	s := store.Get()
	screen := ComputeScreenState(ScreenInputs{
		Disconnected: props.Disconnected,
		Loading:      s.loading,
		Err:          s.err,
		ItemCount:    len(s.runs),
	})

	return h.Section(
		h.ClassStr("history-runs"),
		renderRunFilters(runFiltersProps{
			T:       props.T,
			Filter:  filter.Get(),
			Feeds:   feeds.Get(),
			OnApply: filter.Set,
		}),
		h.If(actionErr.Get() != nil, h.Div(
			h.ClassStr("history-action-error"),
			mutationErrorText(props.T, actionErr.Get()),
		)),
		renderScreenState(props.T, screen, func() ui.Node {
			return runsTable(props.T, s, toggleExpand, requestDelete)
		}),
		h.Div(
			h.ClassStr("history-pager"),
			h.Button(h.Type("button"), h.Disabled(!s.cursor.HasPrevious()), h.OnClick(func() {
				s.cursor.Back()
				load(s.cursor.Current())
			}), props.T.T("history.pager.previous", nil)),
			// Next: disabled on the last page, which is exactly when the
			// server stops returning a next_page_token.
			h.Button(h.Type("button"), h.Disabled(s.nextTk == ""), h.OnClick(func() {
				s.cursor.Advance(s.nextTk)
				load(s.cursor.Current())
			}), props.T.T("history.pager.next", nil)),
			h.Button(h.Type("button"), h.OnClick(func() {
				load(s.cursor.Current())
			}), props.T.T("history.pager.refresh", nil)),
		),
		ui.CreateElement(TypedConfirm, TypedConfirmProps{
			T:          props.T,
			Open:       pendingDelete.Get() != 0,
			TitleKey:   "history.runs.delete_confirm_title",
			PromptKey:  "history.confirm.type_to_confirm",
			MatchWord:  "DELETE",
			LabelledBy: "history-run-delete-confirm-title",
			OnCancel:   func() { pendingDelete.Set(0) },
			OnConfirm: func() {
				deleteRun(pendingDelete.Get())
				pendingDelete.Set(0)
			},
		}),
	)
}

func runsTable(t Catalog, s runsLoadState, toggleExpand func(int64), deleteRun func(int64)) ui.Node {
	return h.Table(
		h.ClassStr("history-table history-runs-table"),
		h.Thead(h.Tr(
			// The numbered gutter: a timesheet's rows are numbered because
			// their sequence carries information (§5.5 — strictly
			// increasing, never backdated), so the runs table gets a "#"
			// column the same way the /generate rail's frames do. The
			// header cell itself carries no text (mirrors the trailing
			// kebab column's own empty h.Th below) rather than a fabricated
			// label string.
			h.Th(h.ClassStr("history-th-num"), ""),
			h.Th(t.T("history.runs.col_status", nil)),
			h.Th(t.T("history.runs.col_trigger", nil)),
			h.Th(t.T("history.runs.col_duration", nil)),
			h.Th(t.T("history.runs.col_added_rejected", nil)),
			h.Th(t.T("history.runs.col_tokens", nil)),
			h.Th(t.T("history.runs.col_cost", nil)),
			h.Th(t.T("history.runs.col_error", nil)),
			h.Th(""),
		)),
		h.Tbody(
			// h.MapKeyed has no index callback, so the row number is
			// computed here (page position, 1-based) rather than plumbed
			// through as a fifth reducer field — it is purely a display
			// convenience over s.runs' already-fixed order, not state.
			runRowsWithNumbers(t, s, toggleExpand, deleteRun),
		),
	)
}

func runRowsWithNumbers(t Catalog, s runsLoadState, toggleExpand func(int64), deleteRun func(int64)) []ui.Node {
	nodes := make([]ui.Node, 0, len(s.runs))
	for i, r := range s.runs {
		nodes = append(nodes, runRow(t, r, i+1, s.expand[r.Id], s.logs[r.Id], toggleExpand, deleteRun))
	}
	return nodes
}

func runRow(t Catalog, r *affv1.Run, rowNumber int, expanded bool, log string, toggleExpand func(int64), deleteRun func(int64)) ui.Node {
	duration := ""
	if r.StartedAt != nil && r.FinishedAt != nil {
		duration = r.FinishedAt.AsTime().Sub(r.StartedAt.AsTime()).String()
	}
	runID := r.Id
	// A failed run must be "findable by scanning, not by reading" (J5) —
	// the status cell already says "Failed" in text, but a scan needs a
	// color the eye catches before the word registers, so the row itself
	// carries a class keyed off status (styles.go paints it against the
	// existing danger/warning/accent roles, never a new literal color).
	rowClass := h.ClassMap(map[string]bool{
		"history-run-row":          true,
		"history-run-row--failed":  r.Status == affv1.RunStatus_RUN_STATUS_FAILED,
		"history-run-row--running": r.Status == affv1.RunStatus_RUN_STATUS_RUNNING,
	})
	return h.Fragment(
		h.Tr(
			h.ClassStr(rowClass),
			h.Td(h.ClassStr("history-td-num"), i18n.FormatNumber("en", float64(rowNumber), i18n.NumberOptions{MaximumFractionDigits: 0})),
			h.Td(runStatusLabel(t, r.Status)),
			h.Td(runTriggerLabel(t, r.Trigger)),
			h.Td(duration),
			h.Td(t.T("history.runs.added_rejected_value", map[string]any{
				"added":    i18n.FormatNumber("en", float64(r.ItemsAdded), i18n.NumberOptions{MaximumFractionDigits: 0}),
				"rejected": i18n.FormatNumber("en", float64(r.ItemsRejected), i18n.NumberOptions{MaximumFractionDigits: 0}),
			})),
			h.Td(t.T("history.runs.tokens_value", map[string]any{
				"in":  i18n.FormatNumber("en", float64(r.TokensIn), i18n.NumberOptions{MaximumFractionDigits: 0}),
				"out": i18n.FormatNumber("en", float64(r.TokensOut), i18n.NumberOptions{MaximumFractionDigits: 0}),
			})),
			h.Td(i18n.FormatNumber("en", r.EstCostUsd, i18n.NumberOptions{MaximumFractionDigits: 4})),
			h.Td(errorKindLabel(t, r.ErrorKind)),
			h.Td(
				// The label follows the state: a button that says "Expand" on
				// an already-expanded row tells the operator the opposite of
				// what it will do.
				h.Button(h.Type("button"),
					h.Aria("expanded", boolAttr(expanded)),
					h.OnClick(func() { toggleExpand(runID) }),
					t.T(expandLabelKey(expanded), nil)),
				h.Div(h.ClassStr("history-kebab"),
					kebabTrigger(t),
					h.Div(h.ClassStr("history-kebab-menu"),
						h.Button(h.Type("button"), h.ClassStr("history-kebab-danger"), h.OnClick(func() { deleteRun(runID) }), t.T("history.runs.delete", nil)),
					),
				),
			),
		),
		// colspan as a STRING, and a class of its own.
		//
		// h.Attr("colspan", 9) — an int — was silently dropped: the rendered
		// <td> carried no colspan attribute at all, so the expanded panel
		// sat in the first column at 113px instead of spanning all nine at
		// 1352px. It then got squashed further to 89px by styles.go's
		// ".history-table td:last-child { display: flex }" rule, which is
		// meant for the actions cell but also matches this cell, since it is
		// the only cell in its row and therefore :last-child too. Both
		// measured in a browser after the operator reported the expanded
		// content "isn't right"; the class below is what lets that rule
		// exclude this cell.
		h.If(expanded, h.Tr(h.Td(h.ClassStr("history-run-log-cell"), h.Attr("colspan", "9"),
			h.Div(h.ClassStr("history-run-log"),
				h.H3(t.T("history.runs.reject_reasons", nil)),
				h.IfElse(len(r.RejectReasons) == 0,
					h.P(t.T("history.runs.no_rejects", nil)),
					h.Ul(h.MapKeyed(r.RejectReasons, func(rr *affv1.RejectReason) any { return rr.Reason }, func(rr *affv1.RejectReason) ui.Node {
						return h.Li(t.T("history.runs.reject_reason_count", map[string]any{
							"reason": rr.Reason,
							"count":  i18n.FormatNumber("en", float64(rr.Count), i18n.NumberOptions{MaximumFractionDigits: 0}),
						}))
					})),
				),
				h.H3(t.T("history.runs.log", nil)),
				h.Pre(log),
			),
		))),
	)
}

// expandLabelKey picks the row-toggle's label for its current state.
func expandLabelKey(expanded bool) string {
	if expanded {
		return "history.runs.collapse"
	}
	return "history.runs.expand"
}

func intToStr(n int) string {
	return strconv.Itoa(n)
}

func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func runStatusLabel(t Catalog, st affv1.RunStatus) string {
	switch st {
	case affv1.RunStatus_RUN_STATUS_RUNNING:
		return t.T("history.runs.status.running", nil)
	case affv1.RunStatus_RUN_STATUS_SUCCEEDED:
		return t.T("history.runs.status.succeeded", nil)
	case affv1.RunStatus_RUN_STATUS_FAILED:
		return t.T("history.runs.status.failed", nil)
	case affv1.RunStatus_RUN_STATUS_SKIPPED:
		return t.T("history.runs.status.skipped", nil)
	default:
		return t.T("history.runs.status.unspecified", nil)
	}
}

func runTriggerLabel(t Catalog, tr affv1.RunTrigger) string {
	if tr == affv1.RunTrigger_RUN_TRIGGER_CRON {
		return t.T("history.runs.trigger.cron", nil)
	}
	if tr == affv1.RunTrigger_RUN_TRIGGER_MANUAL {
		return t.T("history.runs.trigger.manual", nil)
	}
	return t.T("history.runs.trigger.unspecified", nil)
}

func errorKindLabel(t Catalog, ek affv1.ErrorKind) string {
	switch ek {
	case affv1.ErrorKind_ERROR_KIND_TRANSIENT:
		return t.T("history.runs.error_kind.transient", nil)
	case affv1.ErrorKind_ERROR_KIND_INVALID:
		return t.T("history.runs.error_kind.invalid", nil)
	case affv1.ErrorKind_ERROR_KIND_FATAL:
		return t.T("history.runs.error_kind.fatal", nil)
	default:
		return ""
	}
}

func parseInt64(s string) int64 {
	var v int64
	var neg bool
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int64(c-'0')
	}
	if neg {
		v = -v
	}
	return v
}

// renderScreenState is the shared six-state renderer used by both tabs
// (D-FLOW's per-list matrix). It renders exactly one branch per
// ComputeScreenState outcome, and delegates the populated case to
// renderPopulated so callers only ever describe their own table markup.
func renderScreenState(t Catalog, s ScreenState, renderPopulated func() ui.Node) ui.Node {
	switch s {
	case ScreenLoading:
		return h.P(h.ClassStr("history-state history-state--loading"), t.T("history.state.loading", nil))
	case ScreenEmpty:
		return h.P(h.ClassStr("history-state history-state--empty"), t.T("history.state.empty", nil))
	case ScreenError:
		return h.P(h.ClassStr("history-state history-state--error"), t.T("history.state.error", nil))
	case ScreenDisabledWithReason:
		return h.P(h.ClassStr("history-state history-state--disabled"), t.T("history.state.disabled", nil))
	case ScreenDisconnected:
		return h.P(h.ClassStr("history-state history-state--disconnected"), t.T("history.state.disconnected", nil))
	default:
		return renderPopulated()
	}
}
