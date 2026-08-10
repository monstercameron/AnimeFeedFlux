// Metrics for AnimeFeedFlux (PLAN.md §15.0a): the exact ten-instrument set,
// no more, built on the real go.opentelemetry.io/otel/metric API.
//
// NewMetrics accepts any otelmetric.MeterProvider rather than reaching for a
// global: production code passes obs.GetMeterProvider() (otel.go's Setup
// installs a real one, exported via stdoutmetric or otlpmetricgrpc per
// AFF_OTEL_EXPORTER — see otel.go's setupMetrics), and tests pass a
// sdkmetric.MeterProvider backed by a ManualReader so they can Collect what
// was recorded with no exporter or network involved at all.
package obs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// The §15.0a metric set, and no more. Adding a metric here means adding it
// to PLAN.md §15.0a first — this list is the enforcement point, not the
// documentation.
const (
	MetricRunsTotal            = "aff_runs_total"
	MetricRunDurationSeconds   = "aff_run_duration_seconds"
	MetricItemsPublishedTotal  = "aff_items_published_total"
	MetricItemsRejectedTotal   = "aff_items_rejected_total"
	MetricTokensTotal          = "aff_tokens_total"
	MetricCostUSDTotal         = "aff_cost_usd_total"
	MetricFeedStalenessSeconds = "aff_feed_staleness_seconds"
	MetricHTTPRequestsTotal    = "aff_http_requests_total"
	MetricCacheHitsTotal       = "aff_cache_hits_total"
	MetricProviderErrorsTotal  = "aff_provider_errors_total"
)

// MetricNames enumerates the fixed set above, in the order §15.0a lists
// them, for table-driven verification that the constants above match the
// plan exactly.
var MetricNames = []string{
	MetricRunsTotal,
	MetricRunDurationSeconds,
	MetricItemsPublishedTotal,
	MetricItemsRejectedTotal,
	MetricTokensTotal,
	MetricCostUSDTotal,
	MetricFeedStalenessSeconds,
	MetricHTTPRequestsTotal,
	MetricCacheHitsTotal,
	MetricProviderErrorsTotal,
}

// ---- cardinality guard ------------------------------------------------

// ErrUnboundedLabel is returned by CardinalityGuard.Check when a label
// value is not something §15.0a permits as a label: not in a closed
// allow-list, over the learned distinct-value cap for an open-but-small
// set, or shaped like the things that are never allowed regardless of
// context (a URL, free text, an opaque token) — item_key, a URL, a title,
// or model output must never become a label, because one unbounded label is
// how a metrics backend falls over.
var ErrUnboundedLabel = errors.New("obs: metric label value is not from a bounded set")

// CardinalityGuard validates a label value before it is allowed to become a
// metric attribute. It covers both shapes of "bounded" that §15.0a's label
// set actually has:
//   - a closed enumeration (outcome, reason-taxonomy, result, direction) —
//     pass Allowed values up front and leave MaxDistinct at 0.
//   - an open-but-small set (feed_slug: "tens"; model: "a few"; route: the
//     registered handlers) — leave Allowed empty (or partially seeded) and
//     set MaxDistinct; the guard learns values as it sees them and starts
//     failing once more than MaxDistinct distinct values have been observed
//     for this label, which is exactly the signature of a label that is
//     actually unbounded (e.g. someone passed an item_key by mistake).
//
// Either way, a value shaped like a URL, containing whitespace (a title),
// or that is a long high-entropy token (an item_key, a hash, model output)
// is rejected outright regardless of mode — that heuristic is a backstop,
// not the primary control; the primary control is call sites never doing
// this in the first place, enforced by review and by RecordXxx's method
// signatures only accepting the labels §15.0a defines.
type CardinalityGuard struct {
	Label string
	// MaxDistinct caps how many distinct values this guard will learn
	// before it starts rejecting new ones. 0 means "closed enumeration
	// only": nothing outside Allowed is ever accepted.
	MaxDistinct int

	mu      sync.Mutex
	allowed map[string]struct{}
	seen    map[string]struct{}
}

// NewCardinalityGuard builds a guard for label, optionally pre-seeded with
// a closed set of allowed values.
func NewCardinalityGuard(label string, allowed ...string) *CardinalityGuard {
	g := &CardinalityGuard{Label: label}
	if len(allowed) > 0 {
		g.allowed = make(map[string]struct{}, len(allowed))
		for _, v := range allowed {
			g.allowed[v] = struct{}{}
		}
	}
	return g
}

// Check validates value. It is safe for concurrent use, since instrument
// call sites run from many goroutines (per-run workers, HTTP handlers).
func (g *CardinalityGuard) Check(value string) error {
	if looksUnbounded(value) {
		return fmt.Errorf("%w: label=%s value=%q looks like a URL/title/opaque token, never a label", ErrUnboundedLabel, g.Label, value)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.allowed[value]; ok {
		return nil
	}
	if g.MaxDistinct <= 0 {
		if len(g.allowed) == 0 {
			// No allow-list and no learning cap configured: refuse to guard
			// nothing — an unconfigured guard that always passes would be
			// worse than no guard, because it looks like protection.
			return fmt.Errorf("%w: label=%s has neither an allow-list nor MaxDistinct configured", ErrUnboundedLabel, g.Label)
		}
		return fmt.Errorf("%w: label=%s value=%q not in allow-list", ErrUnboundedLabel, g.Label, value)
	}
	if g.seen == nil {
		g.seen = make(map[string]struct{})
	}
	if _, ok := g.seen[value]; ok {
		return nil
	}
	if len(g.seen) >= g.MaxDistinct {
		return fmt.Errorf("%w: label=%s exceeded MaxDistinct=%d distinct observed values (last: %q)", ErrUnboundedLabel, g.Label, g.MaxDistinct, value)
	}
	g.seen[value] = struct{}{}
	return nil
}

// looksUnbounded is a heuristic backstop: it catches the shapes an
// unbounded value actually has (a URL, a title with spaces, a long
// high-entropy token such as an item_key or a content hash) even if a
// caller bypassed the allow-list logic entirely. It is intentionally cheap
// and intentionally imperfect — it is not the control, the guard's
// allow-list/MaxDistinct logic above is.
func looksUnbounded(v string) bool {
	if len(v) == 0 {
		return false
	}
	if len(v) > 64 {
		return true
	}
	if strings.ContainsAny(v, " \t\n\r") {
		return true
	}
	if strings.Contains(v, "://") {
		return true
	}
	return looksLikeOpaqueToken(v)
}

// looksLikeOpaqueToken flags long, mostly-hex strings: content hashes,
// item keys, UUIDs — exactly the shapes §15.0a calls out as "log only,
// never a metric label."
func looksLikeOpaqueToken(v string) bool {
	if len(v) < 16 {
		return false
	}
	hexish := 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			hexish++
		case r >= 'a' && r <= 'f':
			hexish++
		case r >= 'A' && r <= 'F':
			hexish++
		case r == '-':
			// UUID separators don't count against the ratio but don't
			// disqualify it either.
		default:
			return false
		}
	}
	return float64(hexish)/float64(len(v)) > 0.85
}

// ---- Metrics registry --------------------------------------------------

// Metrics holds the ten §15.0a instruments, each built from the given
// MeterProvider under the "animefeedflux" meter name, plus the cardinality
// guards for every label those instruments accept.
type Metrics struct {
	runsTotal            otelmetric.Int64Counter
	runDurationSeconds   otelmetric.Float64Histogram
	itemsPublishedTotal  otelmetric.Int64Counter
	itemsRejectedTotal   otelmetric.Int64Counter
	tokensTotal          otelmetric.Int64Counter
	costUSDTotal         otelmetric.Float64Counter
	feedStalenessSeconds otelmetric.Float64Histogram
	httpRequestsTotal    otelmetric.Int64Counter
	cacheHitsTotal       otelmetric.Int64Counter
	providerErrorsTotal  otelmetric.Int64Counter

	feedSlug  *CardinalityGuard // aff_runs_total, aff_run_duration_seconds, aff_items_published_total, aff_items_rejected_total, aff_feed_staleness_seconds, aff_cost_usd_total
	trigger   *CardinalityGuard // aff_runs_total
	outcome   *CardinalityGuard // aff_runs_total
	reason    *CardinalityGuard // aff_items_rejected_total
	model     *CardinalityGuard // aff_tokens_total, aff_cost_usd_total
	direction *CardinalityGuard // aff_tokens_total
	route     *CardinalityGuard // aff_http_requests_total
	status    *CardinalityGuard // aff_http_requests_total
	result    *CardinalityGuard // aff_cache_hits_total
	errKind   *CardinalityGuard // aff_provider_errors_total
}

// NewMetrics registers the §15.0a instruments against mp.
func NewMetrics(mp otelmetric.MeterProvider) (*Metrics, error) {
	meter := mp.Meter("animefeedflux")

	runsTotal, err := meter.Int64Counter(MetricRunsTotal,
		otelmetric.WithDescription("generation runs, by feed/trigger/outcome"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricRunsTotal, err)
	}
	runDurationSeconds, err := meter.Float64Histogram(MetricRunDurationSeconds,
		otelmetric.WithDescription("generation run wall time"), otelmetric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricRunDurationSeconds, err)
	}
	itemsPublishedTotal, err := meter.Int64Counter(MetricItemsPublishedTotal,
		otelmetric.WithDescription("items published, by feed"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricItemsPublishedTotal, err)
	}
	itemsRejectedTotal, err := meter.Int64Counter(MetricItemsRejectedTotal,
		otelmetric.WithDescription("items rejected, by feed/reason"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricItemsRejectedTotal, err)
	}
	tokensTotal, err := meter.Int64Counter(MetricTokensTotal,
		otelmetric.WithDescription("LLM tokens, by model/direction"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricTokensTotal, err)
	}
	costUSDTotal, err := meter.Float64Counter(MetricCostUSDTotal,
		otelmetric.WithDescription("LLM spend, by feed/model"), otelmetric.WithUnit("{USD}"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricCostUSDTotal, err)
	}
	feedStalenessSeconds, err := meter.Float64Histogram(MetricFeedStalenessSeconds,
		otelmetric.WithDescription("age of each feed's last successful run"), otelmetric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricFeedStalenessSeconds, err)
	}
	httpRequestsTotal, err := meter.Int64Counter(MetricHTTPRequestsTotal,
		otelmetric.WithDescription("publish-plane HTTP requests, by route/status"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricHTTPRequestsTotal, err)
	}
	cacheHitsTotal, err := meter.Int64Counter(MetricCacheHitsTotal,
		otelmetric.WithDescription("render-cache hit/miss/304 outcomes"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricCacheHitsTotal, err)
	}
	providerErrorsTotal, err := meter.Int64Counter(MetricProviderErrorsTotal,
		otelmetric.WithDescription("provider errors, by §8 taxonomy kind"))
	if err != nil {
		return nil, fmt.Errorf("obs: registering %s: %w", MetricProviderErrorsTotal, err)
	}

	return &Metrics{
		runsTotal:            runsTotal,
		runDurationSeconds:   runDurationSeconds,
		itemsPublishedTotal:  itemsPublishedTotal,
		itemsRejectedTotal:   itemsRejectedTotal,
		tokensTotal:          tokensTotal,
		costUSDTotal:         costUSDTotal,
		feedStalenessSeconds: feedStalenessSeconds,
		httpRequestsTotal:    httpRequestsTotal,
		cacheHitsTotal:       cacheHitsTotal,
		providerErrorsTotal:  providerErrorsTotal,

		// feed_slug and model are open-but-small ("tens" / "a few" per
		// §15.0a): they learn values rather than requiring a fixed
		// allow-list, but still fail loudly past a generous cap instead of
		// growing without bound.
		feedSlug: &CardinalityGuard{Label: "feed_slug", MaxDistinct: 500},
		model:    &CardinalityGuard{Label: "model", MaxDistinct: 50},
		route:    &CardinalityGuard{Label: "route", MaxDistinct: 200},
		reason:   &CardinalityGuard{Label: "reason", MaxDistinct: 100},
		errKind:  &CardinalityGuard{Label: "kind", MaxDistinct: 50},
		status:   &CardinalityGuard{Label: "status", MaxDistinct: 30},

		// outcome, direction, result are true closed enumerations —
		// §15.0's canonical field table fixes outcome's values exactly.
		trigger:   NewCardinalityGuard("trigger", "cron", "manual", "api"),
		outcome:   NewCardinalityGuard("outcome", "success", "skipped", "rejected", "failed"),
		direction: NewCardinalityGuard("direction", "in", "out"),
		result:    NewCardinalityGuard("result", "hit", "miss"),
	}, nil
}

// RecordRun increments aff_runs_total{feed_slug,trigger,outcome} and, if
// duration >= 0, observes aff_run_duration_seconds{feed_slug}.
func (m *Metrics) RecordRun(ctx context.Context, feedSlug, trigger, outcome string, durationSeconds float64) error {
	if err := m.feedSlug.Check(feedSlug); err != nil {
		return err
	}
	if err := m.trigger.Check(trigger); err != nil {
		return err
	}
	if err := m.outcome.Check(outcome); err != nil {
		return err
	}
	m.runsTotal.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("feed_slug", feedSlug),
		attribute.String("trigger", trigger),
		attribute.String("outcome", outcome),
	))
	if durationSeconds >= 0 {
		m.runDurationSeconds.Record(ctx, durationSeconds, otelmetric.WithAttributes(
			attribute.String("feed_slug", feedSlug),
		))
	}
	return nil
}

// RecordItemsPublished increments aff_items_published_total{feed_slug} by n.
func (m *Metrics) RecordItemsPublished(ctx context.Context, feedSlug string, n int64) error {
	if err := m.feedSlug.Check(feedSlug); err != nil {
		return err
	}
	m.itemsPublishedTotal.Add(ctx, n, otelmetric.WithAttributes(attribute.String("feed_slug", feedSlug)))
	return nil
}

// RecordItemRejected increments aff_items_rejected_total{feed_slug,reason}.
// reason must be a short stable token (§15.0's canonical field table), never
// the item's title or key.
func (m *Metrics) RecordItemRejected(ctx context.Context, feedSlug, reason string) error {
	if err := m.feedSlug.Check(feedSlug); err != nil {
		return err
	}
	if err := m.reason.Check(reason); err != nil {
		return err
	}
	m.itemsRejectedTotal.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("feed_slug", feedSlug),
		attribute.String("reason", reason),
	))
	return nil
}

// RecordTokens increments aff_tokens_total{model,direction} by n. direction
// is "in" or "out".
func (m *Metrics) RecordTokens(ctx context.Context, model, direction string, n int64) error {
	if err := m.model.Check(model); err != nil {
		return err
	}
	if err := m.direction.Check(direction); err != nil {
		return err
	}
	m.tokensTotal.Add(ctx, n, otelmetric.WithAttributes(
		attribute.String("model", model),
		attribute.String("direction", direction),
	))
	return nil
}

// RecordCost increments aff_cost_usd_total{feed_slug,model} by usd.
func (m *Metrics) RecordCost(ctx context.Context, feedSlug, model string, usd float64) error {
	if err := m.feedSlug.Check(feedSlug); err != nil {
		return err
	}
	if err := m.model.Check(model); err != nil {
		return err
	}
	m.costUSDTotal.Add(ctx, usd, otelmetric.WithAttributes(
		attribute.String("feed_slug", feedSlug),
		attribute.String("model", model),
	))
	return nil
}

// RecordFeedStaleness observes aff_feed_staleness_seconds{feed_slug}.
func (m *Metrics) RecordFeedStaleness(ctx context.Context, feedSlug string, ageSeconds float64) error {
	if err := m.feedSlug.Check(feedSlug); err != nil {
		return err
	}
	m.feedStalenessSeconds.Record(ctx, ageSeconds, otelmetric.WithAttributes(attribute.String("feed_slug", feedSlug)))
	return nil
}

// RecordHTTPRequest increments aff_http_requests_total{route,status}. status
// is the HTTP status code as a string (e.g. "200", "304").
func (m *Metrics) RecordHTTPRequest(ctx context.Context, route, status string) error {
	if err := m.route.Check(route); err != nil {
		return err
	}
	if err := m.status.Check(status); err != nil {
		return err
	}
	m.httpRequestsTotal.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("route", route),
		attribute.String("status", status),
	))
	return nil
}

// RecordCacheResult increments aff_cache_hits_total{result}. result is
// "hit" or "miss".
func (m *Metrics) RecordCacheResult(ctx context.Context, result string) error {
	if err := m.result.Check(result); err != nil {
		return err
	}
	m.cacheHitsTotal.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", result)))
	return nil
}

// RecordProviderError increments aff_provider_errors_total{kind}, kind being
// the §8 provider-error taxonomy token.
func (m *Metrics) RecordProviderError(ctx context.Context, kind string) error {
	if err := m.errKind.Check(kind); err != nil {
		return err
	}
	m.providerErrorsTotal.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("kind", kind)))
	return nil
}
