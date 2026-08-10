//go:build js

package history

import (
	"context"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// RunsTabProps is what the Runs tab needs from its parent.
type RunsTabProps struct {
	Client       RunsClient
	T            Catalog
	Disconnected bool
}

type runsLoadState struct {
	loading bool
	err     error
	runs    []*affv1.Run
	cursor  *PageCursor
	logs    map[int64]string // populated by expand-row fetch
	expand  map[int64]bool
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

	load := func(pageToken string) {
		store.Dispatch(runsAction{kind: "load-start"})
		go func() {
			req := BuildRunHistoryRequest(filter.Get(), pageToken, DefaultPageSize)
			resp, err := props.Client.History(context.Background(), req)
			if err != nil {
				store.Dispatch(runsAction{kind: "load-err", err: err})
				return
			}
			store.Dispatch(runsAction{kind: "load-ok", runs: resp.Runs, nextTk: resp.NextPageToken})
		}()
	}

	ui.UseEffect(func() func() {
		store.Get().cursor.Reset()
		load("")
		return nil
	}, filter.Get())

	handleFeedFilter := func(ev ui.InputEvent) {
		f := filter.Get()
		f.FeedID = parseInt64(ev.GetValue())
		filter.Set(f)
	}

	toggleExpand := func(runID int64) {
		wasExpanding := !store.Get().expand[runID]
		store.Dispatch(runsAction{kind: "toggle-expand", runID: runID})
		if wasExpanding && store.Get().logs[runID] == "" {
			go func() {
				resp, err := props.Client.Get(context.Background(), &affv1.RunServiceGetRequest{RunId: runID})
				if err != nil {
					return
				}
				store.Dispatch(runsAction{kind: "log-loaded", runID: runID, log: resp.Log})
			}()
		}
	}

	// deleteRun is irreversible (proto/aff/v1/run.proto: RunService.Delete
	// carries no expected_version and there is no Restore RPC for runs,
	// unlike items), so it only fires from the typed-confirmation dialog
	// below — requestDelete (passed to the table) just opens it.
	deleteRun := func(runID int64) {
		go func() {
			_, err := props.Client.Delete(context.Background(), &affv1.RunServiceDeleteRequest{RunId: runID})
			if err != nil {
				return
			}
			store.Dispatch(runsAction{kind: "delete-ok", runID: runID})
		}()
	}
	requestDelete := func(runID int64) { pendingDelete.Set(runID) }

	s := store.Get()
	screen := ComputeScreenState(ScreenInputs{
		Disconnected: props.Disconnected,
		Loading:      s.loading,
		Err:          s.err,
		ItemCount:    len(s.runs),
	})

	return h.Section(
		h.ClassStr("history-runs"),
		h.Div(
			h.ClassStr("history-filters"),
			h.Label(h.For("history-runs-feed-filter"), props.T.T("history.runs.filter_feed", nil)),
			h.Input(h.ID("history-runs-feed-filter"), h.Type("number"), h.OnInput(handleFeedFilter)),
		),
		renderScreenState(props.T, screen, func() ui.Node {
			return runsTable(props.T, s, toggleExpand, requestDelete)
		}),
		h.Div(
			h.ClassStr("history-pager"),
			h.Button(h.Type("button"), h.Disabled(!s.cursor.HasPrevious()), h.OnClick(func() {
				s.cursor.Back()
				load(s.cursor.Current())
			}), props.T.T("history.pager.previous", nil)),
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
		h.ClassStr("history-table"),
		h.Thead(h.Tr(
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
			h.MapKeyed(s.runs, func(r *affv1.Run) any { return r.Id }, func(r *affv1.Run) ui.Node {
				return runRow(t, r, s.expand[r.Id], s.logs[r.Id], toggleExpand, deleteRun)
			}),
		),
	)
}

func runRow(t Catalog, r *affv1.Run, expanded bool, log string, toggleExpand func(int64), deleteRun func(int64)) ui.Node {
	duration := ""
	if r.StartedAt != nil && r.FinishedAt != nil {
		duration = r.FinishedAt.AsTime().Sub(r.StartedAt.AsTime()).String()
	}
	runID := r.Id
	return h.Fragment(
		h.Tr(
			h.Td(runStatusLabel(t, r.Status)),
			h.Td(runTriggerLabel(t, r.Trigger)),
			h.Td(duration),
			h.Td(h.Textf("%d / %d", r.ItemsAdded, r.ItemsRejected)),
			h.Td(h.Textf("%d / %d", r.TokensIn, r.TokensOut)),
			h.Td(i18n.FormatNumber("en", r.EstCostUsd, i18n.NumberOptions{MaximumFractionDigits: 4})),
			h.Td(errorKindLabel(t, r.ErrorKind)),
			h.Td(
				h.Button(h.Type("button"), h.OnClick(func() { toggleExpand(runID) }), t.T("history.runs.expand", nil)),
				h.Div(h.ClassStr("history-kebab"),
					h.Button(h.Type("button"), h.ClassStr("history-kebab-trigger"), t.T("history.kebab", nil)),
					h.Div(h.ClassStr("history-kebab-menu"),
						h.Button(h.Type("button"), h.ClassStr("history-kebab-danger"), h.OnClick(func() { deleteRun(runID) }), t.T("history.runs.delete", nil)),
					),
				),
			),
		),
		h.If(expanded, h.Tr(h.Td(h.Attr("colspan", 8),
			h.Div(h.ClassStr("history-run-log"),
				h.H3(t.T("history.runs.reject_reasons", nil)),
				h.IfElse(len(r.RejectReasons) == 0,
					h.P(t.T("history.runs.no_rejects", nil)),
					h.Ul(h.MapKeyed(r.RejectReasons, func(rr *affv1.RejectReason) any { return rr.Reason }, func(rr *affv1.RejectReason) ui.Node {
						return h.Li(h.Textf("%s: %d", rr.Reason, rr.Count))
					})),
				),
				h.H3(t.T("history.runs.log", nil)),
				h.Pre(log),
			),
		))),
	)
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
