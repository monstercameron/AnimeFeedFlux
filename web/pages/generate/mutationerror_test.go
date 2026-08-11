package generatepage

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errSentinel = errors.New("disconnected sentinel")

func TestClassifyMutationErrorNil(t *testing.T) {
	if got := ClassifyMutationError(nil, errSentinel); got != MutationOK {
		t.Fatalf("got %v, want MutationOK", got)
	}
}

func TestClassifyMutationErrorDisconnected(t *testing.T) {
	wrapped := errors.Join(errors.New("guard refused"), errSentinel)
	if got := ClassifyMutationError(errSentinel, errSentinel); got != MutationDisconnected {
		t.Fatalf("got %v, want MutationDisconnected", got)
	}
	if got := ClassifyMutationError(wrapped, errSentinel); got != MutationDisconnected {
		t.Fatalf("wrapped sentinel: got %v, want MutationDisconnected", got)
	}
}

func TestClassifyMutationErrorDisconnectedTakesPrecedenceOverStatus(t *testing.T) {
	// A disconnect sentinel is never also a gRPC status error in practice,
	// but the precedence must hold regardless: disconnected is checked
	// first because "nothing was fetched, an error might just be no
	// socket" (logic.go's SelectListState carries the same precedence
	// reasoning for the six-state matrix).
	if got := ClassifyMutationError(errSentinel, errSentinel); got != MutationDisconnected {
		t.Fatalf("got %v, want MutationDisconnected", got)
	}
}

func TestClassifyMutationErrorRejected(t *testing.T) {
	err := status.Error(codes.InvalidArgument, "slug already in use")
	if got := ClassifyMutationError(err, errSentinel); got != MutationRejected {
		t.Fatalf("got %v, want MutationRejected", got)
	}
}

func TestClassifyMutationErrorRejectedRegardlessOfCode(t *testing.T) {
	for _, c := range []codes.Code{codes.NotFound, codes.AlreadyExists, codes.FailedPrecondition, codes.PermissionDenied, codes.Internal, codes.Unavailable} {
		err := status.Error(c, "boom")
		if got := ClassifyMutationError(err, errSentinel); got != MutationRejected {
			t.Fatalf("code %v: got %v, want MutationRejected", c, got)
		}
	}
}

func TestClassifyMutationErrorUnexpected(t *testing.T) {
	err := errors.New("some non-status error")
	if got := ClassifyMutationError(err, errSentinel); got != MutationUnexpected {
		t.Fatalf("got %v, want MutationUnexpected", got)
	}
}

func TestClassifyMutationErrorNilSentinelNeverClassifiesDisconnected(t *testing.T) {
	err := errors.New("anything")
	if got := ClassifyMutationError(err, nil); got == MutationDisconnected {
		t.Fatalf("a nil sentinel must never match as disconnected, got %v", got)
	}
}

func TestShouldRollbackOptimisticState(t *testing.T) {
	cases := []struct {
		outcome MutationOutcome
		want    bool
	}{
		{MutationOK, false},
		{MutationDisconnected, true},
		{MutationRejected, true},
		{MutationUnexpected, true},
	}
	for _, c := range cases {
		if got := ShouldRollbackOptimisticState(c.outcome); got != c.want {
			t.Fatalf("outcome %v: got %v, want %v", c.outcome, got, c.want)
		}
	}
}
