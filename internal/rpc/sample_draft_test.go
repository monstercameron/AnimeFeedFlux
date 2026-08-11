package rpc

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// smpApplyDraft is what makes "preview exactly what is on screen" true: the
// draft overrides the saved recipe for one call. Two things about it are
// load-bearing and were untested — an empty field must NOT erase the saved
// value, and a draft must not be able to smuggle past the validation the
// save path applies.

func TestApplyDraftOverridesOnlyWhatWasTyped(t *testing.T) {
	base := func() *generate.Spec {
		return &generate.Spec{
			SystemPrompt:       "saved system",
			UserPromptTemplate: "saved user {{.Today}}",
			Model:              "gpt-4o",
			Effort:             "smart",
		}
	}

	t.Run("a nil draft changes nothing", func(t *testing.T) {
		spec := base()
		if err := smpApplyDraft(spec, nil); err != nil {
			t.Fatalf("smpApplyDraft: %v", err)
		}
		if *spec != *base() {
			t.Errorf("a nil draft modified the spec: %+v", spec)
		}
	})

	t.Run("an empty field keeps the saved value", func(t *testing.T) {
		// This is the difference between "I did not touch this field" and "I
		// cleared this field". Treating empty as an override would blank the
		// system prompt of every feed previewed from a form that only sent
		// the changed box.
		spec := base()
		if err := smpApplyDraft(spec, &affv1.SampleDraft{UserPrompt: "draft user"}); err != nil {
			t.Fatalf("smpApplyDraft: %v", err)
		}
		if spec.SystemPrompt != "saved system" {
			t.Errorf("system prompt = %q, want the saved one", spec.SystemPrompt)
		}
		if spec.UserPromptTemplate != "draft user" {
			t.Errorf("user prompt = %q, want the draft", spec.UserPromptTemplate)
		}
		if spec.Model != "gpt-4o" || spec.Effort != "smart" {
			t.Errorf("untouched fields moved: %+v", spec)
		}
	})

	t.Run("every field can be overridden", func(t *testing.T) {
		spec := base()
		err := smpApplyDraft(spec, &affv1.SampleDraft{
			SystemPrompt: "draft system",
			UserPrompt:   "draft user {{.Season}}",
			Model:        "gpt-4.1",
			Effort:       "quick",
		})
		if err != nil {
			t.Fatalf("smpApplyDraft: %v", err)
		}
		want := generate.Spec{
			SystemPrompt:       "draft system",
			UserPromptTemplate: "draft user {{.Season}}",
			Model:              "gpt-4.1",
			Effort:             "quick",
		}
		if *spec != want {
			t.Errorf("spec = %+v, want %+v", spec, want)
		}
	})
}

func TestApplyDraftRejectsAnEffortTheSavePathWouldReject(t *testing.T) {
	// The three Speed tiers are all SchemaFlux exposes; a draft that could
	// name a fourth would be a way around the settings validation.
	for _, effort := range []string{"smart", "fast", "quick"} {
		spec := &generate.Spec{UserPromptTemplate: "x"}
		if err := smpApplyDraft(spec, &affv1.SampleDraft{Effort: effort}); err != nil {
			t.Errorf("effort %q was rejected: %v", effort, err)
		}
	}
	spec := &generate.Spec{UserPromptTemplate: "x"}
	err := smpApplyDraft(spec, &affv1.SampleDraft{Effort: "turbo"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("an unknown effort returned %v, want InvalidArgument", err)
	}
	if spec.Effort == "turbo" {
		t.Error("the rejected effort was applied anyway")
	}
}

func TestApplyDraftCatchesATemplateThatOnlyFailsAtExecute(t *testing.T) {
	// text/template's Parse accepts {{.Titles}} — a field that does not
	// exist — and only fails when executed. Without this check the typo
	// surfaces as a validation error AFTER a paid provider call.
	cases := map[string]*affv1.SampleDraft{
		"system prompt":   {SystemPrompt: "{{.NotAField}}", UserPrompt: "ok"},
		"user prompt":     {UserPrompt: "{{.NotAField}}"},
		"unclosed action": {UserPrompt: "{{.Today"},
	}
	for name, draft := range cases {
		t.Run(name, func(t *testing.T) {
			spec := &generate.Spec{UserPromptTemplate: "saved", SystemPrompt: "saved"}
			err := smpApplyDraft(spec, draft)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
			// The message has to say WHICH prompt: an operator with two
			// text areas open cannot act on "invalid template".
			if msg := status.Convert(err).Message(); msg == "" {
				t.Error("the rejection carries no message")
			}
		})
	}
}

// --- error classification ---------------------------------------------------
//
// Two mappings from the same taxonomy, one for the unary call's gRPC status
// and one for the stream's terminal ErrorKind. They must agree, because the
// same failure reaching an operator through two paths that disagree about
// whether it is retryable is worse than either answer alone.

func TestSampleErrorClassificationAgreesAcrossBothPaths(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode codes.Code
		wantKind affv1.ErrorKind
	}{
		{
			name:     "transient is retryable",
			err:      &llm.Error{Kind: llm.Transient, Err: errors.New("429")},
			wantCode: codes.Unavailable,
			wantKind: affv1.ErrorKind_ERROR_KIND_TRANSIENT,
		},
		{
			name:     "invalid is the caller's input",
			err:      &llm.Error{Kind: llm.Invalid, Err: errors.New("schema violation")},
			wantCode: codes.InvalidArgument,
			wantKind: affv1.ErrorKind_ERROR_KIND_INVALID,
		},
		{
			name:     "fatal is a precondition, not a retry",
			err:      &llm.Error{Kind: llm.Fatal, Scope: llm.ScopeAccount, Err: errors.New("bad key")},
			wantCode: codes.FailedPrecondition,
			wantKind: affv1.ErrorKind_ERROR_KIND_FATAL,
		},
		{
			name:     "malformed output is invalid, not internal",
			err:      generate.ErrMalformedOutput,
			wantCode: codes.InvalidArgument,
			wantKind: affv1.ErrorKind_ERROR_KIND_INVALID,
		},
		{
			name:     "anything else is ours",
			err:      errors.New("something broke in here"),
			wantCode: codes.Internal,
			wantKind: affv1.ErrorKind_ERROR_KIND_UNSPECIFIED,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := status.Code(smpClassifySampleError(tc.err)); got != tc.wantCode {
				t.Errorf("smpClassifySampleError = %v, want %v", got, tc.wantCode)
			}
			if got := smpErrorKind(tc.err); got != tc.wantKind {
				t.Errorf("smpErrorKind = %v, want %v", got, tc.wantKind)
			}
			// And the reverse mapping the stream falls back to must land on
			// the same kind, or the two paths disagree.
			if got := smpGRPCCodeToErrorKind(tc.wantCode); got != tc.wantKind {
				t.Errorf("smpGRPCCodeToErrorKind(%v) = %v, want %v", tc.wantCode, got, tc.wantKind)
			}
		})
	}
}

func TestGRPCCodeToErrorKindTreatsNotFoundAsFatal(t *testing.T) {
	// A feed that no longer exists will not start existing on a retry.
	if got := smpGRPCCodeToErrorKind(codes.NotFound); got != affv1.ErrorKind_ERROR_KIND_FATAL {
		t.Errorf("NotFound mapped to %v, want FATAL", got)
	}
	if got := smpGRPCCodeToErrorKind(codes.DeadlineExceeded); got != affv1.ErrorKind_ERROR_KIND_UNSPECIFIED {
		t.Errorf("an unmapped code produced %v, want UNSPECIFIED", got)
	}
}

func TestClassifySampleErrorKeepsTheKindOutOfAnUnclassifiedError(t *testing.T) {
	// An *llm.Error with an unclassified kind is not a transient failure and
	// must not be reported as one — the caller would retry into a wall.
	err := smpClassifySampleError(&llm.Error{Kind: llm.KindUnclassified, Err: errors.New("who knows")})
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("an unclassified llm.Error mapped to %v, want Internal", got)
	}
}
