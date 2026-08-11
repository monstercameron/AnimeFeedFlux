// mutationerror.go is this page's half of TODOS.md D0-10: "queue or refuse
// mutations while DISCONNECTED, never fail silently." web/wsconn's guarded
// clients already refuse every RPC this page calls (RunService.Delete,
// ItemService.Create/Update/Delete/Restore/PublishCorrection) with the
// distinguishable wsconn.ErrDisconnected sentinel the instant the socket is
// not Ready — see web/shell/mutationerror.go's doc comment, which this
// mirrors, and web/pages/generate/mutationerror.go's twin file for the
// sibling page. What was missing on this page specifically was any caller
// actually branching on the sentinel: runs_ui.go's deleteRun and every one
// of items_ui.go's saveItem/deleteItem/restoreItem/publishCorrection
// previously discarded their RPC's error entirely (`if err != nil { return
// }`, no state write at all), so a disconnected delete, a rejected save,
// and a genuine bug were all, in effect, silently invisible.
//
// No build tag deliberately — this is exercised by plain `go test`
// (mutationerror_test.go), the same reasoning web/shell/mutationerror.go
// and generatepage's twin file give for staying host-testable rather than
// only provable by compiling the WASM binary.
package history

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// MutationOutcome is the three-way branch D0-10 requires every mutation
// error to resolve to: disconnected (never reached the server — retry once
// back online), rejected (the server refused it — something must change),
// or unexpected (neither is known to be true).
type MutationOutcome int

const (
	// MutationOK means err was nil.
	MutationOK MutationOutcome = iota
	// MutationDisconnected means the guarded client refused the RPC
	// outright because the control-plane socket was not Ready.
	MutationDisconnected
	// MutationRejected means the RPC reached the server and the server
	// returned a gRPC status error worth showing verbatim.
	MutationRejected
	// MutationUnexpected is everything else.
	MutationUnexpected
)

// ClassifyMutationError decides which of the three err represents.
// disconnectedSentinel is threaded through as a parameter (rather than
// this file importing web/wsconn, which is js-only) so it stays
// host-testable — the js-tagged caller in mutationerror_js.go pins it to
// shell.ErrDisconnected, the real wsconn.ErrDisconnected.
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
// known to have failed. TODOS.md D0-10: an optimistic update implying
// success is wrong regardless of *why* the mutation failed — disconnected,
// rejected, or unexpected all warrant undoing it, not disconnected alone.
func ShouldRollbackOptimisticState(outcome MutationOutcome) bool {
	return outcome != MutationOK
}

// IsVersionConflict reports whether err is specifically the optimistic-
// concurrency rejection RevertRevision/Update/Delete/Restore return when
// expected_version does not match the row's current version
// (internal/rpc/item.go's itemVersionConflictErr: codes.FailedPrecondition,
// message "... expected_version %d does not match the current version").
// There is no dedicated gRPC code for this — every other rejection this
// package classifies as MutationRejected is also FailedPrecondition or
// another 4xx-equivalent code — so this is a narrower, message-text-scoped
// check layered on top of ClassifyMutationError, used only where a caller
// needs to tell "someone else changed this row first" apart from every
// other server rejection: PLAN.md §12.4's revert must show that specific
// case "as a real choice rather than a silent clobber" (reload the latest
// state, then decide), not the generic rejected-mutation text.
func IsVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return false
	}
	return strings.Contains(st.Message(), "expected_version")
}
