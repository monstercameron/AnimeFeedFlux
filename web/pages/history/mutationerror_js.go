//go:build js

package history

import (
	"google.golang.org/grpc/status"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// mutationErrorText renders a mutation error into display text that
// distinguishes TODOS.md D0-10's three cases, so a disconnected refusal, a
// server rejection, and a genuine bug never share the same
// "delete failed"-style wording — see mutationerror.go's package doc
// comment for the callers this closes the gap for.
func mutationErrorText(t Catalog, err error) string {
	if err == nil {
		return ""
	}
	switch ClassifyMutationError(err, shell.ErrDisconnected) {
	case MutationDisconnected:
		return t.T("history.errors.disconnected", nil)
	case MutationRejected:
		msg := err.Error()
		if st, ok := status.FromError(err); ok {
			msg = st.Message()
		}
		return t.T("history.errors.rejected", map[string]any{"message": msg})
	default:
		return t.T("history.errors.unexpected", map[string]any{"message": err.Error()})
	}
}
