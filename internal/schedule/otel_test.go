// Tests for TODOS A7-21: a budget/kill-switch refusal from Gate increments
// aff_runs_total{outcome="skipped"} — never an error counter — and does so
// without ever calling Executor.Execute, since generate.Run (where the
// success/failed terminal states are recorded, internal/generate/runner.go)
// is never reached for a refused feed.
package schedule

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

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

func runsTotalPoints(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.DataPoint[int64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != obs.MetricRunsTotal {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				return sum.DataPoints
			}
		}
	}
	return nil
}

func TestRunner_GateDenial_RecordsSkippedRunMetric_NotAnError(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	job := stubJob{id: 1, slug: "denied", interval: time.Millisecond}
	exec := newRecordingExecutor(clock)
	gate := denyGate{denied: map[int64]bool{1: true}}
	metrics, reader := newTestMetrics(t)

	r := New(clock, []Job{job}, exec, gate,
		WithMaxConcurrent(1), WithJitterWindow(time.Millisecond), WithMetrics(metrics))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// A single Advance can race the scheduler goroutine's first
	// clock.After call (before it registers a waiter, so that one Advance
	// finds nothing to fire) — repeated small advances give it many chances
	// to land, the same pattern nudgeUntilCalls uses elsewhere in this
	// package's tests.
	for i := 0; i < 20; i++ {
		clock.Advance(3 * time.Millisecond)
		exec.waitForChange(5 * time.Millisecond)
	}

	if got := exec.totalCalls(); got != 0 {
		t.Fatalf("gate-denied feed must not call Execute, got %d calls", got)
	}

	var (
		sawSkipped bool
		sawOther   bool
	)
	for _, dp := range runsTotalPoints(t, reader) {
		outcome, _ := dp.Attributes.Value("outcome")
		slug, _ := dp.Attributes.Value("feed_slug")
		if slug.AsString() != "denied" {
			continue
		}
		switch outcome.AsString() {
		case "skipped":
			sawSkipped = true
		default:
			sawOther = true
		}
	}
	if !sawSkipped {
		t.Error("aff_runs_total{feed_slug=\"denied\",outcome=\"skipped\"} not recorded for a gate refusal")
	}
	if sawOther {
		t.Error("gate refusal recorded under an outcome other than \"skipped\" — a refusal must never count as an error/failed run")
	}

	cancel()
	<-done
}

// TestRunner_NoMetrics_DoesNotPanic guards Deps.Metrics'/Runner.metrics' nil
// contract: WithMetrics is never called (the AFF_OTEL_ENABLED=0 default), and
// a gate refusal must still work exactly as before.
func TestRunner_NoMetrics_DoesNotPanic(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	job := stubJob{id: 1, slug: "denied", interval: time.Millisecond}
	exec := newRecordingExecutor(clock)
	gate := denyGate{denied: map[int64]bool{1: true}}

	r := New(clock, []Job{job}, exec, gate, WithMaxConcurrent(1), WithJitterWindow(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	for i := 0; i < 20; i++ {
		clock.Advance(3 * time.Millisecond)
		exec.waitForChange(5 * time.Millisecond)
	}

	cancel()
	<-done
}
