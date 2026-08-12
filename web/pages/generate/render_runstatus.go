//go:build js && wasm

package generatepage

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// render_runstatus.go is what "Run now" tells you.
//
// # What it used to tell you
//
// Nothing. The click called FeedService.RunNow, which opens a run row, hands
// it to the executor and returns a run_id — and the page discarded that id,
// reloaded the feed list, and showed no indication that anything had happened.
// The run then went off and spent real money against the provider, and the
// only way to find out what it did was to navigate to History, remember which
// feed it was, and pick that feed out of a filter.
//
// That is a poor deal for the one control on this page that starts a billable
// job. §12.3 asks for the estimate to be visible at the moment of spending;
// the same reasoning applies to the outcome.
//
// # What it tells you now
//
// The run's status, inline, next to the feed it belongs to: running, then the
// outcome — items added and rejected, tokens, cost — and a link to that feed's
// full history. The status is polled rather than streamed: RunService.Watch
// exists and would be tidier, but the streaming path in this app has needed
// its own care (see web/ui/pump.go), and a run this page just started is worth
// two seconds of polling rather than a second streaming implementation to
// maintain.

// runStatusPollInterval is how often a just-started run is re-checked.
const runStatusPollInterval = 2

// runStatusMaxPolls bounds the polling so a run that never reports finished
// cannot leave a timer going for the life of the page. At 2s this is ten
// minutes, which is longer than any generation this app performs.
const runStatusMaxPolls = 300

// runStatus is the state of the run this page most recently started.
type runStatus struct {
	// FeedID/FeedSlug identify what was run, so the line can say so and the
	// history link knows where to point.
	FeedID   int64
	FeedSlug string
	RunID    int64
	// Run is the latest snapshot from the server, nil until the first poll
	// comes back.
	Run *affv1.Run
	// Err is a failure to START the run (a refusal, a budget cap, a run
	// already in progress) as opposed to a run that started and then failed —
	// the second is reported by Run.Status and reads very differently.
	Err error
}

// Finished reports whether polling should stop.
func (r runStatus) Finished() bool {
	if r.Err != nil {
		return true
	}
	if r.Run == nil {
		return false
	}
	switch r.Run.GetStatus() {
	case affv1.RunStatus_RUN_STATUS_RUNNING, affv1.RunStatus_RUN_STATUS_UNSPECIFIED:
		return false
	default:
		return true
	}
}

// runStatusProps is what the status line needs.
type runStatusProps struct {
	Status runStatus
	// HistoryHref is the address of this feed's run history.
	HistoryHref string
	OnDismiss   func()
}

// renderRunStatus draws the line. It renders nothing when no run has been
// started from this page.
func renderRunStatus(p runStatusProps) ui.Node {
	t := deps.I18n
	st := p.Status
	if st.RunID == 0 && st.Err == nil {
		return h.Fragment()
	}

	var body ui.Node
	switch {
	case st.Err != nil:
		body = h.Span(h.ClassStr("af-run-status__text af-run-status__text--error"),
			h.Text(t.T("generate.runStatus.refused", mutationErrorText(t, st.Err))))
	case st.Run == nil:
		body = h.Span(h.ClassStr("af-run-status__text"),
			h.Text(t.T("generate.runStatus.starting", st.FeedSlug)))
	case !st.Finished():
		body = h.Span(h.ClassStr("af-run-status__text"),
			h.Text(t.T("generate.runStatus.running", st.FeedSlug)))
	default:
		body = h.Span(h.ClassStr(runOutcomeClass(st.Run.GetStatus())),
			h.Text(runOutcomeText(t, st.Run)))
	}

	return h.Div(
		h.ClassStr("af-run-status"),
		h.Role("status"),
		h.Aria("live", "polite"),
		body,
		// The link is the answer to "where do I see the rest of this?", and it
		// is always offered — including while the run is still going, because
		// that is when someone most wants the log.
		h.A(h.ClassStr("af-run-status__link"), h.Href(p.HistoryHref),
			h.Text(t.T("generate.runStatus.viewHistory"))),
		h.Button(h.Type("button"), h.ClassStr("af-run-status__dismiss"),
			h.Aria("label", t.T("generate.runStatus.dismiss")),
			h.OnClick(ui.UseEvent(func() {
				if p.OnDismiss != nil {
					p.OnDismiss()
				}
			})),
			h.Text("×"), //nolint:i18n -- glyph; the accessible name is the aria-label above
		),
	)
}

// runOutcomeText is the one-line summary of a finished run: what it produced
// and what it cost, in that order, because those are the two questions.
func runOutcomeText(t Translator, r *affv1.Run) string {
	fmtr := deps.Formatters
	tokens := fmtr.Number(int64(r.GetTokensIn()) + int64(r.GetTokensOut()))
	switch r.GetStatus() {
	case affv1.RunStatus_RUN_STATUS_SUCCEEDED:
		return t.T("generate.runStatus.succeeded",
			strconv.Itoa(int(r.GetItemsAdded())),
			strconv.Itoa(int(r.GetItemsRejected())),
			tokens,
			fmtr.Currency(r.GetEstCostUsd()))
	case affv1.RunStatus_RUN_STATUS_SKIPPED:
		// SKIPPED is what a budget cap looks like (§13), and it is not a
		// failure — saying so plainly stops it being read as one.
		return t.T("generate.runStatus.skipped")
	default:
		return t.T("generate.runStatus.failed", errorKindText(t, r.GetErrorKind()))
	}
}

func runOutcomeClass(s affv1.RunStatus) string {
	switch s {
	case affv1.RunStatus_RUN_STATUS_SUCCEEDED:
		return "af-run-status__text af-run-status__text--ok"
	case affv1.RunStatus_RUN_STATUS_SKIPPED:
		return "af-run-status__text af-run-status__text--warn"
	default:
		return "af-run-status__text af-run-status__text--error"
	}
}

// errorKindText names the failure class a run recorded (§8's taxonomy), so a
// transient provider blip does not read the same as a broken recipe.
func errorKindText(t Translator, k affv1.ErrorKind) string {
	switch k {
	case affv1.ErrorKind_ERROR_KIND_TRANSIENT:
		return t.T("generate.runStatus.errorKind.transient")
	case affv1.ErrorKind_ERROR_KIND_INVALID:
		return t.T("generate.runStatus.errorKind.invalid")
	case affv1.ErrorKind_ERROR_KIND_FATAL:
		return t.T("generate.runStatus.errorKind.fatal")
	default:
		return t.T("generate.runStatus.errorKind.unknown")
	}
}
