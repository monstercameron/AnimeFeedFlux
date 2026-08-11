package obs

import (
	"context"
	"log/slog"
	"regexp"
	"time"
)

// This file is the canonical logging vocabulary (PLAN.md §15.0). Today's logs
// use bare string keys, so nothing can be grouped on reliably and a typo'd
// field name is invisible — `feed`, `feed_slug`, and `slug` in three packages
// are three fields, and no query finds all of them. Everything that reaches a
// log record must be one of the constants below.
//
// # Level policy (§15.0)
//
// Levels have meanings, so that alerting on them is possible:
//
//   - ERROR means a human must look now.
//   - WARN means something failed but self-healed — recurrence matters, but
//     this instance does not need a human yet. A retried transient provider
//     error is WARN, not ERROR: a level that cries wolf trains the reader to
//     ignore it, and that is how an alert channel becomes noise and then gets
//     muted.
//   - INFO is reserved for the two canonical wide events below
//     (EventRunFinished, EventHTTPRequest). Nothing else should log at INFO —
//     chatty INFO is what makes people stop reading logs.
//   - DEBUG is development detail and may be as chatty as needed; progress
//     commentary belongs here, not at INFO.
//
// One canonical line per unit of work, not a running commentary: a generation
// run emits a single run.finished record carrying every field that applies to
// it, and a feed request emits one http.request. The interesting question
// during an incident is almost always "what happened to this run", which one
// wide event answers and forty narrow ones do not.

// Canonical field names. A field not in this list must not appear on a log
// record — see IsCanonicalField, which tests enforce this against.
const (
	FieldRunID     = "run_id"     // anything inside a generation run; attached from context, never by hand
	FieldRequestID = "request_id" // anything inside an HTTP request; attached from context, never by hand
	FieldTraceID   = "trace_id"   // the join between logs and traces (§15.0a), once tracing is on
	FieldSpanID    = "span_id"    // as above

	FieldFeedSlug = "feed_slug" // anything feed-scoped; bounded cardinality, safe as a metric label too
	FieldItemKey  = "item_key"  // item-scoped work; log only — NEVER a metric label (§15.0a cardinality rule)

	FieldModel     = "model"      // provider calls
	FieldTokensIn  = "tokens_in"  // provider calls
	FieldTokensOut = "tokens_out" // provider calls
	FieldCostUSD   = "cost_usd"   // provider calls

	FieldDurationMs = "duration_ms" // anything timed; a NUMBER, never a formatted string (A0-L03)
	FieldOutcome    = "outcome"     // terminal events; constrained to Outcome's four values (A0-L04)
	FieldReason     = "reason"      // any non-success outcome; a short stable token, never a sentence (A0-L05)

	FieldTrigger = "trigger" // generation.run: what started the run
	FieldRoute   = "route"   // http.request
	FieldStatus  = "status"  // http.request: HTTP status code
	FieldCache   = "cache"   // http.request: hit|miss|304
)

// canonicalFields is the whitelist IsCanonicalField checks against. Keep this
// in lockstep with the constants above — it exists so a test can enumerate
// every field name a log record carries and fail on anything not listed here,
// which is the actual enforcement mechanism (A0-L12), not just documentation.
var canonicalFields = map[string]struct{}{
	FieldRunID:      {},
	FieldRequestID:  {},
	FieldTraceID:    {},
	FieldSpanID:     {},
	FieldFeedSlug:   {},
	FieldItemKey:    {},
	FieldModel:      {},
	FieldTokensIn:   {},
	FieldTokensOut:  {},
	FieldCostUSD:    {},
	FieldDurationMs: {},
	FieldOutcome:    {},
	FieldReason:     {},
	FieldTrigger:    {},
	FieldRoute:      {},
	FieldStatus:     {},
	FieldCache:      {},
}

// structuralFields are slog's own record keys, not business fields. A test
// enumerating a record's keys against the canonical set must allow these too.
var structuralFields = map[string]struct{}{
	"time":  {},
	"level": {},
	"msg":   {},
}

// IsCanonicalField reports whether name is a field this package defines, or
// one of slog's own structural keys (time, level, msg). Anything else on a
// log record is a typo or a bare string key that PLAN.md §15.0 forbids.
func IsCanonicalField(name string) bool {
	if _, ok := canonicalFields[name]; ok {
		return true
	}
	_, ok := structuralFields[name]
	return ok
}

// Outcome is a terminal event's result, constrained to exactly four values
// (A0-L04). It is a defined type rather than a bare string so a call site
// that types OutcomeSuccess et al. cannot accidentally produce a fifth value;
// ValidOutcome is the runtime backstop for anything that arrives as a plain
// string (e.g. deserialized, or constructed dynamically).
type Outcome string

const (
	OutcomeSuccess  Outcome = "success"
	OutcomeSkipped  Outcome = "skipped"
	OutcomeRejected Outcome = "rejected"
	OutcomeFailed   Outcome = "failed"
)

// ValidOutcome reports whether o is one of the four constrained values.
func ValidOutcome(o Outcome) bool {
	switch o {
	case OutcomeSuccess, OutcomeSkipped, OutcomeRejected, OutcomeFailed:
		return true
	}
	return false
}

// reasonTokenPattern is what a "short stable token" (§15.0) looks like:
// lowercase, snake_case, bounded length. Prose does not match this — and
// prose is exactly the shape model output, upstream RSS content, and other
// hostile input (RULE-3) take. Rejecting anything that doesn't match is the
// enforcement point, not just a style preference: an unbounded reason value
// is a cardinality problem on any backend that groups on it, and a verbatim
// model string in a reason field is a privacy problem on top of that.
var reasonTokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

// fallbackReason is what SanitizeReason returns for input that is not a valid
// token. It is itself a valid token, so it never breaks the invariant it
// enforces, and it is distinctive enough in a log query to make the caller
// that produced it findable and fixable.
const fallbackReason = "unsanitized_reason"

// SanitizeReason returns s unchanged if it is a valid stable token, or
// fallbackReason otherwise. This is the boundary that keeps model output,
// upstream titles, and other unbounded text out of the reason field
// (RULE-3 + the §15.0a cardinality rule): call it at the point a reason is
// attached to any record. RunFinished and HTTPRequest call it so a caller
// cannot bypass it by using those helpers; call sites that build their own
// records must call it directly.
func SanitizeReason(s string) string {
	if reasonTokenPattern.MatchString(s) {
		return s
	}
	return fallbackReason
}

// DurationMsAttr converts d to the canonical duration_ms attribute: an
// integer number of milliseconds, never a formatted string (A0-L03). A string
// duration cannot be aggregated by a metrics or log backend, and it looks
// fine right up until someone tries to chart it.
func DurationMsAttr(d time.Duration) slog.Attr {
	return slog.Int64(FieldDurationMs, d.Milliseconds())
}

// Canonical event names. Exactly one of each is emitted per unit of work.
const (
	EventRunFinished = "run.finished"
	EventHTTPRequest = "http.request"
)

// RunFinishedFields carries every field a run.finished record may need. Zero
// values are omitted only in the sense that model/token/cost fields are
// meaningless for a run that never called a provider; callers that have no
// value for a field simply leave it at the zero value, and it is still logged
// — RunFinished emits every field that applies, per §15.0's "carrying every
// field above."
type RunFinishedFields struct {
	FeedSlug  string
	Trigger   string
	Outcome   Outcome
	Reason    string // required (and sanitized) when Outcome != OutcomeSuccess
	Duration  time.Duration
	Model     string
	TokensIn  int
	TokensOut int
	CostUSD   float64
}

// RunFinished emits the single canonical run.finished wide event (A0-L08).
// This is the ONE line a completed generation run produces — see A0-L11: a
// completed run emits exactly one of these, not a running commentary.
//
// Outcome is validated and Reason is sanitized so a caller cannot smuggle an
// unconstrained outcome or a verbatim model string through this helper: it is
// the enforcement point for A0-L04/L05/L13, not just a convenience wrapper.
func RunFinished(ctx context.Context, logger *slog.Logger, f RunFinishedFields) {
	outcome := f.Outcome
	if !ValidOutcome(outcome) {
		outcome = OutcomeFailed
	}
	reason := f.Reason
	if reason != "" || outcome != OutcomeSuccess {
		reason = SanitizeReason(reason)
	}

	logger.LogAttrs(ctx, slog.LevelInfo, EventRunFinished,
		slog.String(FieldFeedSlug, f.FeedSlug),
		slog.String(FieldTrigger, f.Trigger),
		slog.String(FieldOutcome, string(outcome)),
		slog.String(FieldReason, reason),
		DurationMsAttr(f.Duration),
		slog.String(FieldModel, f.Model),
		slog.Int(FieldTokensIn, f.TokensIn),
		slog.Int(FieldTokensOut, f.TokensOut),
		slog.Float64(FieldCostUSD, f.CostUSD),
	)
}

// HTTPRequestFields carries every field an http.request record needs.
type HTTPRequestFields struct {
	Route    string
	Status   int
	Cache    string // hit|miss|304
	Duration time.Duration
}

// HTTPRequest emits the single canonical http.request wide event (A0-L09).
//
// The rightful caller (verified 2026-08-10, not yet wired): a middleware or
// wrapper around the http.Handler internal/publish/server.go's NewServer
// returns. That handler is a bare *http.ServeMux with zero logging or
// telemetry today — every route (handleRoot, handleFeed, handleItem,
// handleHealthz, handleRobots, handleFavicon) writes a response and returns
// with no call into this package at all. This is the one piece of the
// publish plane that must emit http.request exactly once per request,
// carrying the route pattern (not the raw path — §15.0a's cardinality rule
// makes "/feeds/{slug}.{ext}" the label-safe shape, not the literal URL),
// the status code actually written, and cache in {hit, miss, 304} (a hit
// never reaches SQLite; a miss additionally opens the render.feed child
// span TraceContext/Start already support). The wrapper needs
// ResponseWriter status capture (net/http gives none by default) — that is
// the missing piece, not anything in this package.
//
// This is NOT speculative: TODOS.md A9-20 through A9-24 mark this done
// against exactly this shape (span http.request with route/status/cache,
// aff_http_requests_total, aff_cache_hits_total, ratio sampling, one event
// per request), and PLAN.md §15.0a's traffic-shape table lists http.request
// as a real root span alongside generation.run. The checked-off TODOS items
// describe intent this package fulfills (this helper, RecordHTTPRequest/
// RecordCacheResult in metrics.go, KindRequest sampling in otel.go) — but
// nothing in internal/publish calls any of it yet, so the checked boxes are
// premature against the actual tree. internal/publish is outside this
// change's edit scope (only internal/obs), so the fix is wiring a caller
// there, not deleting this helper.
func HTTPRequest(ctx context.Context, logger *slog.Logger, f HTTPRequestFields) {
	logger.LogAttrs(ctx, slog.LevelInfo, EventHTTPRequest,
		slog.String(FieldRoute, f.Route),
		slog.Int(FieldStatus, f.Status),
		slog.String(FieldCache, f.Cache),
		DurationMsAttr(f.Duration),
	)
}
