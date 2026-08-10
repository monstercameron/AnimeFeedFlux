package obs

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newTestMetrics builds a Metrics registry backed by a ManualReader, so
// tests can Collect what was actually recorded without any exporter.
func newTestMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := NewMetrics(mp)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m, reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rm
}

func collectedNames(rm metricdata.ResourceMetrics) map[string]bool {
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	return names
}

// TestMetricNamesMatchThePlanExactly is the table test over the §15.0a set:
// the ten names, in the ten constants, exactly — catches both a missing
// metric and an extra one that crept in.
func TestMetricNamesMatchThePlanExactly(t *testing.T) {
	want := []string{
		"aff_runs_total",
		"aff_run_duration_seconds",
		"aff_items_published_total",
		"aff_items_rejected_total",
		"aff_tokens_total",
		"aff_cost_usd_total",
		"aff_feed_staleness_seconds",
		"aff_http_requests_total",
		"aff_cache_hits_total",
		"aff_provider_errors_total",
	}
	if len(MetricNames) != len(want) {
		t.Fatalf("MetricNames has %d entries, want %d (§15.0a defines exactly this many)", len(MetricNames), len(want))
	}
	for i, name := range want {
		t.Run(name, func(t *testing.T) {
			if MetricNames[i] != name {
				t.Errorf("MetricNames[%d] = %q, want %q", i, MetricNames[i], name)
			}
		})
	}
}

// TestMetricsRegistersExactlyTheDeclaredSet actually registers every
// instrument through NewMetrics and records one measurement on each, then
// asserts the collected instrument names are exactly MetricNames — no more,
// no fewer. This is what would catch an instrument that was registered but
// never wired into a RecordXxx method, or vice versa.
func TestMetricsRegistersExactlyTheDeclaredSet(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	if err := m.RecordRun(ctx, "anime-trivia-daily", "cron", "success", 12.5); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if err := m.RecordItemsPublished(ctx, "anime-trivia-daily", 3); err != nil {
		t.Fatalf("RecordItemsPublished: %v", err)
	}
	if err := m.RecordItemRejected(ctx, "anime-trivia-daily", "duplicate"); err != nil {
		t.Fatalf("RecordItemRejected: %v", err)
	}
	if err := m.RecordTokens(ctx, "gpt-5", "in", 1000); err != nil {
		t.Fatalf("RecordTokens: %v", err)
	}
	if err := m.RecordCost(ctx, "anime-trivia-daily", "gpt-5", 0.03); err != nil {
		t.Fatalf("RecordCost: %v", err)
	}
	if err := m.RecordFeedStaleness(ctx, "anime-trivia-daily", 900); err != nil {
		t.Fatalf("RecordFeedStaleness: %v", err)
	}
	if err := m.RecordHTTPRequest(ctx, "/feeds/anime-trivia-daily.xml", "304"); err != nil {
		t.Fatalf("RecordHTTPRequest: %v", err)
	}
	if err := m.RecordCacheResult(ctx, "hit"); err != nil {
		t.Fatalf("RecordCacheResult: %v", err)
	}
	if err := m.RecordProviderError(ctx, "rate_limited"); err != nil {
		t.Fatalf("RecordProviderError: %v", err)
	}

	rm := collect(t, reader)
	got := collectedNames(rm)

	for _, name := range MetricNames {
		if !got[name] {
			t.Errorf("metric %s was never recorded/collected", name)
		}
	}
	if len(got) != len(MetricNames) {
		t.Errorf("collected %d distinct instruments, want exactly %d (%v)", len(got), len(MetricNames), got)
	}
}

func TestCardinalityGuardRejectsUnboundedValue(t *testing.T) {
	m, _ := newTestMetrics(t)
	ctx := context.Background()

	// A URL, the shape §15.0a explicitly forbids as a label.
	err := m.RecordItemRejected(ctx, "anime-trivia-daily", "https://example.com/some/very/specific/article-page")
	if err == nil {
		t.Fatal("expected the cardinality guard to reject a URL-shaped reason, got nil error")
	}
	if !errors.Is(err, ErrUnboundedLabel) {
		t.Errorf("error = %v, want it to wrap ErrUnboundedLabel", err)
	}
}

func TestCardinalityGuardRejectsItemKeyShapedValue(t *testing.T) {
	guard := &CardinalityGuard{Label: "feed_slug", MaxDistinct: 500}
	// A 32-hex-char content hash / item_key shape must never pass as a label.
	if err := guard.Check("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"); err == nil {
		t.Fatal("expected an opaque-token-shaped value to be rejected")
	}
}

func TestCardinalityGuardRejectsTitleWithWhitespace(t *testing.T) {
	guard := &CardinalityGuard{Label: "reason", MaxDistinct: 100}
	if err := guard.Check("Attack on Titan Season 4 Finale Discussion"); err == nil {
		t.Fatal("expected a title-shaped value (contains whitespace) to be rejected")
	}
}

func TestCardinalityGuardLearnsThenCapsMaxDistinct(t *testing.T) {
	guard := &CardinalityGuard{Label: "feed_slug", MaxDistinct: 2}
	if err := guard.Check("feed-a"); err != nil {
		t.Fatalf("1st distinct value: %v", err)
	}
	if err := guard.Check("feed-b"); err != nil {
		t.Fatalf("2nd distinct value: %v", err)
	}
	// Re-seeing an already-learned value must stay fine even at the cap.
	if err := guard.Check("feed-a"); err != nil {
		t.Fatalf("re-seeing a learned value should not fail: %v", err)
	}
	if err := guard.Check("feed-c"); err == nil {
		t.Fatal("3rd distinct value should exceed MaxDistinct=2 and fail loudly")
	} else if !errors.Is(err, ErrUnboundedLabel) {
		t.Errorf("error = %v, want it to wrap ErrUnboundedLabel", err)
	}
}

func TestCardinalityGuardClosedEnumerationRejectsOutsideValues(t *testing.T) {
	guard := NewCardinalityGuard("outcome", "success", "skipped", "rejected", "failed")
	if err := guard.Check("success"); err != nil {
		t.Errorf("an allowed value must pass: %v", err)
	}
	if err := guard.Check("kinda_worked"); err == nil {
		t.Fatal("a value outside the closed enumeration must be rejected, with no learning")
	}
}

func TestCardinalityGuardAcceptsOrdinaryBoundedValues(t *testing.T) {
	m, _ := newTestMetrics(t)
	ctx := context.Background()
	if err := m.RecordRun(ctx, "anime-trivia-daily", "cron", "success", 1.0); err != nil {
		t.Errorf("ordinary bounded labels must be accepted: %v", err)
	}
	if err := m.RecordHTTPRequest(ctx, "/healthz", "200"); err != nil {
		t.Errorf("ordinary bounded labels must be accepted: %v", err)
	}
}

func TestLooksUnboundedHeuristics(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"short slug", "anime-trivia-daily", false},
		{"http status code", "200", false},
		{"model name", "gpt-5", false},
		{"url", "https://example.com/x", true},
		{"has whitespace", "hello world", true},
		{"long string", strings.Repeat("a", 65), true},
		{"opaque hex token", "9f86d081884c7d659a2feaa0c55ad015", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksUnbounded(tc.in); got != tc.want {
				t.Errorf("looksUnbounded(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
