// mutationerror.go is this page's half of TODOS.md D0-10: "queue or refuse
// mutations while DISCONNECTED, never fail silently." web/wsconn's guarded
// clients already refuse every RPC this page calls with the distinguishable
// wsconn.ErrDisconnected sentinel the instant the socket is not Ready (see
// web/shell/mutationerror.go's own doc comment, which this file mirrors);
// what was missing on this page specifically was any caller actually
// branching on it, so every failure — offline, a rejected save, a genuine
// bug — rendered as the same undifferentiated error text. ClassifyMutationError
// closes that gap as a pure, host-testable function so the render layer
// (errors.go's mutationErrorText, js-tagged) has one place to ask "which of
// the three is this."
//
// No build tag deliberately, same reasoning as web/shell/mutationerror.go:
// this is exercised by plain `go test` (mutationerror_test.go), not only
// provable by compiling the WASM binary.
package generatepage

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// MutationOutcome is the three-way branch D0-10 requires every mutation
// error to resolve to, because the operator's correct next move differs
// for each: disconnected means retry once back online (nothing reached the
// server); rejected means the server itself refused the request and the
// operator must change something; unexpected means neither is known to be
// true, and simply retrying may or may not help.
type MutationOutcome int

const (
	// MutationOK means err was nil — no mutation failure to classify.
	MutationOK MutationOutcome = iota
	// MutationDisconnected means the RPC never reached the server: the
	// control-plane socket was not Ready when the guarded client refused
	// the call (wsconn.ErrDisconnected, or the sentinel this page's caller
	// passes for it).
	MutationDisconnected
	// MutationRejected means the RPC reached the server and the server
	// refused it — a gRPC status error with a message worth showing
	// verbatim (PLAN.md §12.3: "the server's own message").
	MutationRejected
	// MutationUnexpected is everything else: no gRPC status, not the
	// disconnect sentinel — a genuine bug or an error shape this
	// classification does not recognize.
	MutationUnexpected
)

// ClassifyMutationError decides which of MutationDisconnected/
// MutationRejected/MutationUnexpected err represents. disconnectedSentinel
// is threaded through as a parameter (rather than this file importing
// web/wsconn directly, which is js-only) so this stays host-testable the
// same way web/shell.IsDisconnectedError is — mutationerror_js.go's
// wrapper in errors.go pins it to the real wsconn.ErrDisconnected via
// shell.ErrDisconnected for the actual WASM build.
func ClassifyMutationError(err, disconnectedSentinel error) MutationOutcome {
	if err == nil {
		return MutationOK
	}
	if shell.IsDisconnectedError(err, disconnectedSentinel) {
		return MutationDisconnected
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		return MutationRejected
	}
	return MutationUnexpected
}

// ShouldRollbackOptimisticState reports whether a local view update applied
// before a mutation's RPC returned must be undone now that the mutation is
// known to have failed. TODOS.md D0-10: "a disconnected mutation must not
// leave optimistic local state that implies it succeeded... showing an
// item as saved when it never left the browser is worse than an error."
// That reasoning is not actually specific to the disconnected case — an
// optimistic update implying success is exactly as wrong when the server
// rejected the change or something unexpected happened — so this rolls
// back on any non-OK outcome, not disconnected alone.
func ShouldRollbackOptimisticState(outcome MutationOutcome) bool {
	return outcome != MutationOK
}
