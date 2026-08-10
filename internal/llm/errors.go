package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/monstercameron/schemaflux"
)

// Kind is what a caller should do about a failed call. "The API failed" is
// not actionable at 4am; PLAN.md §8 asks for exactly this taxonomy.
type Kind int

const (
	// KindUnclassified means Classify was never run, or ran on a nil error.
	// Not returned by Classify itself.
	KindUnclassified Kind = iota

	// Transient: 429, 5xx, timeout, connection reset.
	//
	// SchemaFlux does the retrying. This classification exists so the CALLER
	// can tell a transient failure apart from a permanent one when deciding
	// whether to log WARN or ERROR (§15.0) and whether to count the run as
	// failed — not so that anything here retries.
	Transient

	// Invalid: schema violation, refusal, truncated output. The generator
	// makes one repair attempt with the validation error fed back (§9.3);
	// that is generation logic, not transport retry, which is why it lives
	// there and not here.
	Invalid

	// Fatal: fail immediately and surface on /healthz and the dashboard
	// rather than retrying into a wall. Scope says how far the failure
	// reaches.
	Fatal
)

func (k Kind) String() string {
	switch k {
	case Transient:
		return "transient"
	case Invalid:
		return "invalid"
	case Fatal:
		return "fatal"
	default:
		return "unclassified"
	}
}

// Scope narrows a Fatal error to what it disables. PLAN.md §8: an
// account-wide cause (bad key, quota exhausted) trips the global kill switch
// because every feed shares the credential; a recipe-scoped cause (model not
// found, context window smaller than the prompt) disables only that feed. A
// mistyped model id must never take every feed offline.
type Scope int

const (
	// ScopeNone applies to every non-Fatal Kind, and to a Fatal error that is
	// neither account-wide nor recipe-scoped (context canceled, shutdown) --
	// fail this run, but auto-disable nothing.
	ScopeNone Scope = iota

	// ScopeAccount: the credential is bad or exhausted. Every feed shares it.
	ScopeAccount

	// ScopeRecipe: this feed's own configuration is the problem.
	ScopeRecipe
)

func (s Scope) String() string {
	switch s {
	case ScopeAccount:
		return "account"
	case ScopeRecipe:
		return "recipe"
	default:
		return "none"
	}
}

// Error is the classified failure a Provider returns. Every Provider
// implementation in this package (SchemaFluxProvider and Fake) returns one of
// these on failure rather than a bare error, so a caller never has to run
// Classify itself to find out what happened.
type Error struct {
	Kind  Kind
	Scope Scope

	// RetryAfter is the wait the provider asked for. Zero if it did not say.
	// Only meaningful when Kind is Transient.
	RetryAfter time.Duration

	// Err is the underlying error, unwrapped by Unwrap.
	Err error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := "llm: " + e.Kind.String()
	if e.Kind == Fatal && e.Scope != ScopeNone {
		msg += "/" + e.Scope.String()
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap exposes the underlying error so errors.Is/errors.As reach through.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable reports whether repeating the identical call could succeed.
func (e *Error) Retryable() bool {
	return e != nil && e.Kind == Transient
}

// Classify maps a SchemaFlux (or transport) failure onto Kind/Scope.
//
// It never calls SchemaFlux's own internal/llm.Classify -- that package is
// unexported and unreachable from here -- and instead layers three things
// SchemaFlux does export: RetryAfterFrom/StatusCodeFrom (llm.APIError and
// llm.RateLimitError, reachable through any amount of wrapping), the
// ErrorKind sentinels (schemaflux.ErrRateLimited and friends, matched via
// errors.Is against a *types.OperationError when an operation wrapped one),
// and net.Error for a transport failure that never reached the provider at
// all (a connection reset, for instance, which is neither an APIError nor an
// OperationError). Nothing here is a guess: every branch is backed by a type
// or sentinel SchemaFlux documents as public API.
func Classify(err error) *Error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: Transient, Err: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: Fatal, Scope: ScopeNone, Err: err}
	}

	if wait, ok := schemaflux.RetryAfterFrom(err); ok {
		return &Error{Kind: Transient, RetryAfter: wait, Err: err}
	}

	if status, ok := schemaflux.StatusCodeFrom(err); ok {
		return classifyStatus(status, err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return &Error{Kind: Transient, Err: err}
	}

	switch {
	case errors.Is(err, schemaflux.ErrRateLimited),
		errors.Is(err, schemaflux.ErrProviderUnavailable),
		errors.Is(err, schemaflux.ErrTimeout),
		errors.Is(err, schemaflux.ErrCircuitOpen):
		return &Error{Kind: Transient, Err: err}

	case errors.Is(err, schemaflux.ErrSchemaViolation),
		errors.Is(err, schemaflux.ErrMalformedOutput),
		errors.Is(err, schemaflux.ErrOutputTruncated),
		errors.Is(err, schemaflux.ErrPolicyViolation),
		errors.Is(err, schemaflux.ErrInvariantViolation),
		errors.Is(err, schemaflux.ErrEvidenceViolation),
		errors.Is(err, schemaflux.ErrBatchProtocolViolation):
		return &Error{Kind: Invalid, Err: err}

	case errors.Is(err, schemaflux.ErrAuthentication),
		errors.Is(err, schemaflux.ErrPermission),
		errors.Is(err, schemaflux.ErrBudgetExceededKind):
		return &Error{Kind: Fatal, Scope: ScopeAccount, Err: err}

	case errors.Is(err, schemaflux.ErrConfiguration),
		errors.Is(err, schemaflux.ErrContextTooLarge),
		errors.Is(err, schemaflux.ErrInvalidRequest),
		errors.Is(err, schemaflux.ErrUnsupportedCapability):
		return &Error{Kind: Fatal, Scope: ScopeRecipe, Err: err}

	case errors.Is(err, schemaflux.ErrCanceled),
		errors.Is(err, schemaflux.ErrShutdown):
		return &Error{Kind: Fatal, Scope: ScopeNone, Err: err}
	}

	// Nothing classified it. The safe default is Invalid: one repair attempt
	// and then fail the run, rather than retrying forever (Transient) or
	// tripping a kill switch (Fatal) over a failure nobody recognised.
	return &Error{Kind: Invalid, Err: err}
}

// classifyStatus maps an HTTP status SchemaFlux recovered from a wrapped
// llm.APIError/llm.RateLimitError. 429 is handled by RetryAfterFrom before
// this is ever reached when the provider sent a Retry-After; this covers a
// 429 that did not.
func classifyStatus(status int, err error) *Error {
	switch {
	case status == 401 || status == 403:
		return &Error{Kind: Fatal, Scope: ScopeAccount, Err: err}
	case status == 429 || status == 503:
		return &Error{Kind: Transient, Err: err}
	case status == 413:
		// Request too large: the closest HTTP signal to §8's "context window
		// smaller than the prompt" case.
		return &Error{Kind: Fatal, Scope: ScopeRecipe, Err: err}
	case status == 404:
		// Model not found (or an unrecognised endpoint) is recipe
		// configuration, not an account problem.
		return &Error{Kind: Fatal, Scope: ScopeRecipe, Err: err}
	case status >= 500:
		return &Error{Kind: Transient, Err: err}
	case status == 400 || status == 422:
		return &Error{Kind: Invalid, Err: err}
	default:
		return &Error{Kind: Invalid, Err: err}
	}
}
