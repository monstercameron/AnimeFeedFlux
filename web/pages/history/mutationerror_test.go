package history

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errHistorySentinel = errors.New("disconnected sentinel")

func TestClassifyMutationErrorNil(t *testing.T) {
	if got := ClassifyMutationError(nil, errHistorySentinel); got != MutationOK {
		t.Fatalf("got %v, want MutationOK", got)
	}
}

func TestClassifyMutationErrorDisconnected(t *testing.T) {
	if got := ClassifyMutationError(errHistorySentinel, errHistorySentinel); got != MutationDisconnected {
		t.Fatalf("got %v, want MutationDisconnected", got)
	}
	wrapped := errors.Join(errors.New("guard refused"), errHistorySentinel)
	if got := ClassifyMutationError(wrapped, errHistorySentinel); got != MutationDisconnected {
		t.Fatalf("wrapped sentinel: got %v, want MutationDisconnected", got)
	}
}

func TestClassifyMutationErrorRejected(t *testing.T) {
	for _, c := range []codes.Code{codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.FailedPrecondition, codes.Internal, codes.Unavailable} {
		err := status.Error(c, "boom")
		if got := ClassifyMutationError(err, errHistorySentinel); got != MutationRejected {
			t.Fatalf("code %v: got %v, want MutationRejected", c, got)
		}
	}
}

func TestClassifyMutationErrorUnexpected(t *testing.T) {
	err := errors.New("some non-status error")
	if got := ClassifyMutationError(err, errHistorySentinel); got != MutationUnexpected {
		t.Fatalf("got %v, want MutationUnexpected", got)
	}
}

func TestClassifyMutationErrorNilSentinelNeverClassifiesDisconnected(t *testing.T) {
	err := errors.New("anything")
	if got := ClassifyMutationError(err, nil); got == MutationDisconnected {
		t.Fatalf("a nil sentinel must never match as disconnected, got %v", got)
	}
}

func TestIsVersionConflict(t *testing.T) {
	conflict := status.Errorf(codes.FailedPrecondition, "rpc: item %d: expected_version %d does not match the current version", 42, 3)
	if !IsVersionConflict(conflict) {
		t.Errorf("expected version-conflict message to be classified as a conflict")
	}

	otherFailedPrecondition := status.Error(codes.FailedPrecondition, "rpc: reverting would leave item.title empty")
	if IsVersionConflict(otherFailedPrecondition) {
		t.Errorf("a FailedPrecondition unrelated to expected_version must not be classified as a conflict")
	}

	notFound := status.Error(codes.NotFound, "rpc: item 1 not found")
	if IsVersionConflict(notFound) {
		t.Errorf("a non-FailedPrecondition error must not be classified as a conflict")
	}

	if IsVersionConflict(nil) {
		t.Errorf("nil must not be classified as a conflict")
	}

	if IsVersionConflict(errors.New("some non-status error")) {
		t.Errorf("a non-status error must not be classified as a conflict")
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
