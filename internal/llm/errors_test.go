package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/schemaflux"
)

func TestClassify_Nil(t *testing.T) {
	if got := Classify(nil); got != nil {
		t.Fatalf("Classify(nil) = %v, want nil", got)
	}
}

func TestClassify(t *testing.T) {
	table := []struct {
		name      string
		err       error
		wantKind  Kind
		wantScope Scope
	}{
		{"deadline exceeded", context.DeadlineExceeded, Transient, ScopeNone},
		{"wrapped deadline exceeded", fmt.Errorf("call: %w", context.DeadlineExceeded), Transient, ScopeNone},
		{"canceled", context.Canceled, Fatal, ScopeNone},
		{"rate limited sentinel", fmt.Errorf("wrap: %w", schemaflux.ErrRateLimited), Transient, ScopeNone},
		{"provider unavailable sentinel", schemaflux.ErrProviderUnavailable, Transient, ScopeNone},
		{"timeout sentinel", schemaflux.ErrTimeout, Transient, ScopeNone},
		{"schema violation sentinel", schemaflux.ErrSchemaViolation, Invalid, ScopeNone},
		{"malformed output sentinel", schemaflux.ErrMalformedOutput, Invalid, ScopeNone},
		{"output truncated sentinel", schemaflux.ErrOutputTruncated, Invalid, ScopeNone},
		{"policy violation sentinel (refusal)", schemaflux.ErrPolicyViolation, Invalid, ScopeNone},
		{"authentication sentinel", schemaflux.ErrAuthentication, Fatal, ScopeAccount},
		{"permission sentinel", schemaflux.ErrPermission, Fatal, ScopeAccount},
		{"budget exceeded sentinel", schemaflux.ErrBudgetExceededKind, Fatal, ScopeAccount},
		{"configuration sentinel (model not found)", schemaflux.ErrConfiguration, Fatal, ScopeRecipe},
		{"context too large sentinel", schemaflux.ErrContextTooLarge, Fatal, ScopeRecipe},
		{"invalid request sentinel", schemaflux.ErrInvalidRequest, Fatal, ScopeRecipe},
		{"shutdown sentinel", schemaflux.ErrShutdown, Fatal, ScopeNone},
		{"unclassified error", errors.New("something odd"), Invalid, ScopeNone},
	}

	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got == nil {
				t.Fatalf("Classify(%v) = nil", tc.err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tc.wantKind)
			}
			if got.Scope != tc.wantScope {
				t.Errorf("Scope = %v, want %v", got.Scope, tc.wantScope)
			}
			if !errors.Is(got, tc.err) {
				t.Errorf("errors.Is(Classify(err), err) = false, want true so callers can still errors.Is/As through")
			}
		})
	}
}

func TestClassify_TransientSurfacesRetryAfter(t *testing.T) {
	rateLimited := &schemaflux.RateLimitError{
		APIError:   &schemaflux.APIError{Provider: "openai", StatusCode: 429},
		RetryAfter: 42 * time.Second,
	}

	got := Classify(rateLimited)
	if got.Kind != Transient {
		t.Fatalf("Kind = %v, want Transient", got.Kind)
	}
	if got.RetryAfter != 42*time.Second {
		t.Fatalf("RetryAfter = %v, want 42s", got.RetryAfter)
	}
	if !got.Retryable() {
		t.Fatal("Retryable() = false, want true for a Transient error")
	}
}

func TestClassify_StatusCodes(t *testing.T) {
	table := []struct {
		status    int
		wantKind  Kind
		wantScope Scope
	}{
		{401, Fatal, ScopeAccount},
		{403, Fatal, ScopeAccount},
		{404, Fatal, ScopeRecipe},
		{413, Fatal, ScopeRecipe},
		{429, Transient, ScopeNone},
		{500, Transient, ScopeNone},
		{503, Transient, ScopeNone},
		{400, Invalid, ScopeNone},
		{422, Invalid, ScopeNone},
	}

	for _, tc := range table {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			apiErr := &schemaflux.APIError{Provider: "openai", StatusCode: tc.status}
			got := Classify(apiErr)
			if got.Kind != tc.wantKind {
				t.Errorf("status %d: Kind = %v, want %v", tc.status, got.Kind, tc.wantKind)
			}
			if got.Scope != tc.wantScope {
				t.Errorf("status %d: Scope = %v, want %v", tc.status, got.Scope, tc.wantScope)
			}
		})
	}
}

func TestError_Retryable(t *testing.T) {
	if (&Error{Kind: Transient}).Retryable() != true {
		t.Error("Transient should be retryable")
	}
	if (&Error{Kind: Invalid}).Retryable() != false {
		t.Error("Invalid should not be retryable")
	}
	if (&Error{Kind: Fatal}).Retryable() != false {
		t.Error("Fatal should not be retryable")
	}
	var nilErr *Error
	if nilErr.Retryable() != false {
		t.Error("nil *Error should not be retryable")
	}
}

// TestIsAccountScoped verifies the seam a caller (internal/generate, and
// beyond it the composition root) uses to distinguish an account-wide kill-
// switch condition from every other failure shape: only Fatal+ScopeAccount
// trips it, it survives fmt.Errorf wrapping, and it never panics on plain
// errors or nil.
func TestIsAccountScoped(t *testing.T) {
	table := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"account-scoped auth sentinel", Classify(schemaflux.ErrAuthentication), true},
		{"account-scoped, wrapped", fmt.Errorf("generate: run failed: %w", Classify(schemaflux.ErrAuthentication)), true},
		{"recipe-scoped configuration sentinel", Classify(schemaflux.ErrConfiguration), false},
		{"fatal but ScopeNone (canceled)", Classify(context.Canceled), false},
		{"transient, not fatal", Classify(schemaflux.ErrRateLimited), false},
		{"plain unwrapped error", errors.New("boom"), false},
		{"nil *Error typed as error", (*Error)(nil), false},
	}
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAccountScoped(tc.err); got != tc.want {
				t.Errorf("IsAccountScoped(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestError_UnwrapAndIs(t *testing.T) {
	cause := errors.New("underlying")
	wrapped := &Error{Kind: Invalid, Err: cause}

	if !errors.Is(wrapped, cause) {
		t.Fatal("errors.Is should reach the wrapped cause through Unwrap")
	}
	if got := wrapped.Unwrap(); got != cause {
		t.Fatalf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestError_ErrorStringIncludesScopeAndRetryAfter(t *testing.T) {
	err := &Error{Kind: Fatal, Scope: ScopeAccount, Err: errors.New("bad key")}
	msg := err.Error()
	if msg == "" {
		t.Fatal("Error() returned empty string")
	}
	if want := "fatal/account"; !contains(msg, want) {
		t.Errorf("Error() = %q, want it to contain %q", msg, want)
	}

	retry := &Error{Kind: Transient, RetryAfter: 5 * time.Second, Err: errors.New("429")}
	if msg := retry.Error(); !contains(msg, "retry after 5s") {
		t.Errorf("Error() = %q, want it to mention the retry wait", msg)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
