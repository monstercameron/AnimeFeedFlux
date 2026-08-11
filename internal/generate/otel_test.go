// Tests for PLAN.md §15.0a's instrumentation of the generation pipeline
// (TODOS A4-31, A4-32, A4-33, A7-19, A7-20): exactly one generation.run root
// span per Run call, with feed_slug/trigger/outcome; aff_runs_total and
// aff_run_duration_seconds on every terminal state, not only success; tokens
// and cost recorded from RunRecord's own (estimated) usage; and no unbounded
// attribute value reaching a span or a metric.
//
// Span assertions go through obs.Setup's real "stdout" exporter rather than
// a hand-wired TracerProvider: internal/obs (owned by another agent, PLAN.md
// task brief) exposes no test hook to inject a custom provider into its
// package-level tracer, and Setup's public Config is the only supported way
// to install one. Capturing os.Stdout around Setup(Enabled:true,
// Exporter:"stdout") exercises the exact path production uses.
package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

// capturedSpan is the minimal shape this file needs out of stdouttrace's
// JSON-per-span output (attribute.Value.MarshalJSON emits {Type,Value}).
type capturedSpan struct {
	Name       string
	Attributes []struct {
		Key   string
		Value struct {
			Type  string
			Value json.RawMessage
		}
	}
}

func (s capturedSpan) attrString(key string) (string, bool) {
	for _, kv := range s.Attributes {
		if kv.Key != key {
			continue
		}
		var v string
		if err := json.Unmarshal(kv.Value.Value, &v); err != nil {
			return "", false
		}
		return v, true
	}
	return "", false
}

// runWithStdoutTracing enables real tracing (obs.Setup, stdout exporter),
// runs fn with tracing active, tears it back down, and returns every span
// stdouttrace wrote in the meantime.
func runWithStdoutTracing(t *testing.T, fn func()) []capturedSpan {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	shutdown, err := obs.Setup(context.Background(), obs.Config{
		Enabled:     true,
		Exporter:    "stdout",
		ServiceName: "animefeedflux-test",
	})
	if err != nil {
		os.Stdout = origStdout
		t.Fatalf("obs.Setup: %v", err)
	}

	fn()

	_ = shutdown(context.Background())
	// Reset back to the disabled default so later tests in this package
	// never inherit a live TracerProvider (obs's tracer/meter providers are
	// package-level state, set once per process by whichever Setup call ran
	// last).
	if resetShutdown, rerr := obs.Setup(context.Background(), obs.Config{Enabled: false}); rerr == nil {
		_ = resetShutdown(context.Background())
	}

	os.Stdout = origStdout
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	var spans []capturedSpan
	dec := json.NewDecoder(&buf)
	for {
		var s capturedSpan
		if err := dec.Decode(&s); err != nil {
			break
		}
		spans = append(spans, s)
	}
	return spans
}

func spansNamed(spans []capturedSpan, name string) []capturedSpan {
	var out []capturedSpan
	for _, s := range spans {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

func TestRun_RootSpan_OneSpanWithRequiredAttributes(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	var result RunResult
	var runErr error
	spans := runWithStdoutTracing(t, func() {
		provider := llm.NewFake()
		provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
			validGeneratedItem("A perfectly reasonable trivia title one"),
		}})
		store := &stubStore{}
		nov := &stubNovelty{}
		deps := newDeps(store, nov, nil, provider, now)
		spec := testSpec()
		spec.Trigger = "cron"

		result, runErr = Run(context.Background(), deps, testFeedGenerative(), spec)
	})

	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if result.Run.Status != StatusCompleted {
		t.Fatalf("Status = %q, want completed", result.Run.Status)
	}

	roots := spansNamed(spans, "generation.run")
	if len(roots) != 1 {
		t.Fatalf("got %d generation.run spans, want exactly 1 (all spans: %+v)", len(roots), spans)
	}

	root := roots[0]
	if v, _ := root.attrString("feed_slug"); v != "trivia-daily" {
		t.Errorf("feed_slug = %q, want trivia-daily", v)
	}
	if v, _ := root.attrString("trigger"); v != "cron" {
		t.Errorf("trigger = %q, want cron", v)
	}
	if v, _ := root.attrString("outcome"); v != "success" {
		t.Errorf("outcome = %q, want success", v)
	}
}

// TestValidateSpan_ReasonsAreBoundedTokens_NeverModelOutput exercises a
// batch with a mix of valid and rejected items and asserts the "validate"
// span's "reasons" attribute contains only known, stable reason tokens — the
// model's actual (junk) title text must never leak into a span attribute.
func TestValidateSpan_ReasonsAreBoundedTokens_NeverModelOutput(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	junkTitle := "zzzz" // fails titleMinLen -> ReasonTitleTooShort; picked to share no substring with any reason token

	spans := runWithStdoutTracing(t, func() {
		provider := llm.NewFake()
		provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
			validGeneratedItem("A perfectly reasonable trivia title one"),
			{Title: junkTitle, SummaryText: "irrelevant"},
		}})
		store := &stubStore{}
		nov := &stubNovelty{}
		deps := newDeps(store, nov, nil, provider, now)

		if _, err := Run(context.Background(), deps, testFeedGenerative(), testSpec()); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	})

	validateSpans := spansNamed(spans, "validate")
	if len(validateSpans) == 0 {
		t.Fatalf("no validate span recorded (all spans: %+v)", spans)
	}
	var sawRejected bool
	for _, s := range validateSpans {
		reasons, ok := s.attrString("reasons")
		if !ok || reasons == "" {
			continue
		}
		sawRejected = true
		if strings.Contains(reasons, junkTitle) {
			t.Fatalf("validate span reasons attribute leaked model input: %q", reasons)
		}
		for _, token := range strings.Split(reasons, ",") {
			if strings.ContainsAny(token, " \t\n") || len(token) > 64 {
				t.Errorf("reason token %q does not look like a stable short token", token)
			}
		}
	}
	if !sawRejected {
		t.Fatal("no validate span reported a rejection; test fixture is not exercising the rejection path")
	}
}

// --- metrics: aff_runs_total / aff_run_duration_seconds / tokens / cost ---
//
// These go through Deps.Metrics directly against an in-memory ManualReader,
// independent of obs's global provider — the same pattern
// internal/obs/metrics_test.go uses for the instruments themselves.

func newTestMetrics(t *testing.T) (*obs.Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := obs.NewMetrics(mp)
	if err != nil {
		t.Fatalf("obs.NewMetrics: %v", err)
	}
	return m, reader
}

func collectIntSum(t *testing.T, reader *sdkmetric.ManualReader, metric string) []metricdata.DataPoint[int64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				return sum.DataPoints
			}
		}
	}
	return nil
}

func collectFloatSum(t *testing.T, reader *sdkmetric.ManualReader, metric string) []metricdata.DataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[float64]); ok {
				return sum.DataPoints
			}
		}
	}
	return nil
}

func collectHistCount(t *testing.T, reader *sdkmetric.ManualReader, metric string) uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var total uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			if hist, ok := m.Data.(metricdata.Histogram[float64]); ok {
				for _, dp := range hist.DataPoints {
					total += dp.Count
				}
			}
		}
	}
	return total
}

func dpOutcome(dp metricdata.DataPoint[int64]) string {
	v, _ := dp.Attributes.Value("outcome")
	return v.AsString()
}

func TestRun_EveryTerminalState_EmitsRunsCounterAndDuration(t *testing.T) {
	tests := []struct {
		name        string
		wantOutcome string
		setup       func(nov *stubNovelty, provider *llm.Fake)
	}{
		{
			name:        "completed",
			wantOutcome: "success",
			setup: func(nov *stubNovelty, provider *llm.Fake) {
				provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
					validGeneratedItem("A perfectly reasonable trivia title one"),
				}})
			},
		},
		{
			name:        "failed",
			wantOutcome: "failed",
			setup: func(nov *stubNovelty, provider *llm.Fake) {
				provider.QueueGenerateError(errors.New("boom"))
				provider.QueueGenerateError(errors.New("boom again")) // repair attempt
			},
		},
		{
			name:        "skipped",
			wantOutcome: "skipped",
			setup: func(nov *stubNovelty, provider *llm.Fake) {
				nov.dupAll = true
				for i := 0; i < 3; i++ {
					provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
						validGeneratedItem("A perfectly reasonable trivia title"),
					}})
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics, reader := newTestMetrics(t)

			now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
			provider := llm.NewFake()
			store := &stubStore{}
			nov := &stubNovelty{}
			tt.setup(nov, provider)

			spec := testSpec()
			spec.Trigger = "cron"
			deps := newDeps(store, nov, nil, provider, now)
			deps.Metrics = metrics

			result, _ := Run(context.Background(), deps, testFeedGenerative(), spec)
			if result.Run.Status == "" {
				t.Fatal("Run() produced no RunRecord")
			}

			var found bool
			for _, dp := range collectIntSum(t, reader, obs.MetricRunsTotal) {
				if dpOutcome(dp) == tt.wantOutcome && dp.Value == 1 {
					found = true
				}
			}
			if !found {
				t.Errorf("aff_runs_total{outcome=%q} not recorded", tt.wantOutcome)
			}

			if collectHistCount(t, reader, obs.MetricRunDurationSeconds) == 0 {
				t.Errorf("aff_run_duration_seconds not observed for outcome %q", tt.wantOutcome)
			}
		})
	}
}

// TestRun_TokensAndCost_UseRecordedRunUsage confirms aff_tokens_total and
// aff_cost_usd_total (A4-33) come from the SAME estimate RunRecord persists
// (TokensIn/TokensOut/EstCostUSD), not a second, independent computation.
func TestRun_TokensAndCost_UseRecordedRunUsage(t *testing.T) {
	metrics, reader := newTestMetrics(t)

	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable trivia title one"),
	}})
	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)
	deps.Metrics = metrics
	spec := testSpec()
	spec.Trigger = "cron"
	spec.PriceInputPerMToken = 1
	spec.PriceOutputPerMToken = 2

	result, err := Run(context.Background(), deps, testFeedGenerative(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.EstCostUSD <= 0 {
		t.Fatalf("RunRecord.EstCostUSD = %v, want > 0 so this test is meaningful", result.Run.EstCostUSD)
	}

	var total float64
	for _, dp := range collectFloatSum(t, reader, obs.MetricCostUSDTotal) {
		total += dp.Value
	}
	if total != result.Run.EstCostUSD {
		t.Errorf("aff_cost_usd_total = %v, want exactly RunRecord.EstCostUSD = %v", total, result.Run.EstCostUSD)
	}

	var gotIn, gotOut int64
	for _, dp := range collectIntSum(t, reader, obs.MetricTokensTotal) {
		v, _ := dp.Attributes.Value("direction")
		switch v.AsString() {
		case "in":
			gotIn += dp.Value
		case "out":
			gotOut += dp.Value
		}
	}
	if int(gotIn) != result.Run.TokensIn {
		t.Errorf("aff_tokens_total{direction=in} = %d, want %d", gotIn, result.Run.TokensIn)
	}
	if int(gotOut) != result.Run.TokensOut {
		t.Errorf("aff_tokens_total{direction=out} = %d, want %d", gotOut, result.Run.TokensOut)
	}
}
