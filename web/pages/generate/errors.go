//go:build js && wasm

package generatepage

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/status"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// catalogLocale is the fixed locale passed to gwci18n's own number
// formatter below. This package cannot import web/i18n (owned by another
// concurrent agent — see doc.go's "i18n" section) so it cannot reuse that
// package's DefaultLocale constant; web/pages/history's runs_ui.go already
// establishes the precedent of calling gwci18n's formatters directly with
// a literal "en" for the same reason.
const catalogLocale = "en"

// formatCount renders a non-negative integer count with locale-aware digit
// grouping (PLAN.md §12.6: "numbers ... formatted by locale-aware
// formatters, never fmt.Sprintf") instead of a %d format verb.
func formatCount(n int) string {
	return gwci18n.FormatNumber(catalogLocale, float64(n), gwci18n.NumberOptions{MaximumFractionDigits: 0})
}

// errorText returns err's message, or "" for a nil error — the single
// place that turns a Go error into interpolation-ready text so call sites
// never hand a bare %v to a Textf format string (TODOS.md D6-15: error
// text goes through the catalogue like any other user-visible string).
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// errNotWired is returned by UseResource loaders when Init has not been
// called yet (see deps.go) — surfaced through the normal ListError state
// rather than a panic, so an incremental build-out where the page is
// routed but not yet wired still renders something legible.
var errNotWired = errors.New("generate page: control-plane clients not initialized (see generatepage.Init)")

// errSample wraps a terminal SampleStream error (PLAN.md §8's ErrorKind
// taxonomy, carried on SampleServiceSampleStreamResponse) so the sampler
// pane can show the taxonomy's kind, not just a bare message — "the API
// failed" is exactly what PLAN.md §8 says is not actionable at 4am, and
// that reasoning applies just as much to what the operator sees on
// screen as to a log line.
type errSample struct {
	kind    affv1.ErrorKind
	message string
}

func (e errSample) Error() string {
	return fmt.Sprintf("%s: %s", e.kind.String(), e.message)
}

// mutationErrorText renders any mutation error (rail toggle/Run Now, editor
// Validate/Save, sampler Promote/Discard/Sample) into display text that
// distinguishes TODOS.md D0-10's three cases, rather than every failure
// showing the same undifferentiated "save failed"-style string:
//
//   - disconnected — the guarded client refused to even attempt the RPC
//     (mutationerror.go's ClassifyMutationError against shell.ErrDisconnected)
//     — rendered as "you are offline, this was not sent" via
//     generate.errors.disconnected, never the bare Go error text, since the
//     operator's next move (retry once reconnected) depends on knowing that.
//   - rejected — a gRPC status error the server actually returned; shown as
//     its own message (PLAN.md §12.3) via generate.common.errorText's
//     passthrough, same as before this task.
//   - an errSample terminal-stream error carries PLAN.md §8's ErrorKind
//     taxonomy in its own Error() text (kind: message) and is treated the
//     same way as a server rejection — it did reach the server and the
//     server categorized it, so it gets the plain passthrough too, not the
//     "unexpected" wrapper.
//   - unexpected — anything else (no gRPC status, not the disconnect
//     sentinel), wrapped with generate.errors.unexpected so it reads as
//     genuinely different from a rejection, not merely different text.
func mutationErrorText(t Translator, err error) string {
	if err == nil {
		return ""
	}
	var es errSample
	if errors.As(err, &es) {
		return t.T("generate.common.errorText", errorText(err))
	}
	switch ClassifyMutationError(err, shell.ErrDisconnected) {
	case MutationDisconnected:
		return t.T("generate.errors.disconnected")
	case MutationRejected:
		if st, ok := status.FromError(err); ok {
			return t.T("generate.common.errorText", st.Message())
		}
		return t.T("generate.common.errorText", errorText(err))
	default:
		return t.T("generate.errors.unexpected", errorText(err))
	}
}
