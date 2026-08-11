package obs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestSetupDisabledInstallsNoopProviderAndNoopShutdown(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown func is nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() = %v, want nil", err)
	}

	// The installed provider must be the real OTel no-op implementation, not
	// merely "some provider" — a span started against it must be non-nil and
	// safe to call End() on, exercising exactly the code path that would
	// break if instrumentation were skipped behind an `if enabled` check.
	ctx, span := Start(context.Background(), "generation.run", KindRun)
	if span == nil {
		t.Fatal("Start returned a nil span with tracing disabled")
	}
	span.End()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("unexpected deadline on returned context")
	}

	tp := GetTracerProvider()
	if _, ok := tp.(tracenoop.TracerProvider); !ok {
		t.Errorf("GetTracerProvider() = %T, want tracenoop.TracerProvider", tp)
	}
}

func TestSetupStdoutExporterEmitsASpan(t *testing.T) {
	// Setup writes to the real os.Stdout (that is the point of the "stdout"
	// exporter: inspectable with no backend). Redirect the process's stdout
	// file descriptor to a pipe for the duration of the test rather than
	// bypassing Setup, so this exercises the exact path Setup wires up.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	shutdown, err := Setup(context.Background(), Config{
		Enabled:     true,
		Exporter:    "stdout",
		ServiceName: "animefeedflux-test",
		SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	ctx, span := Start(context.Background(), "generation.run", KindRun)
	span.SetAttributes()
	span.End()
	_ = ctx

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_ = w.Close()
	os.Stdout = origStdout

	out, err := io.ReadAll(bufio.NewReader(r))
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("stdout exporter produced no output for a KindRun span")
	}
	if !strings.Contains(string(out), "generation.run") {
		t.Errorf("stdout output does not mention the span name:\n%s", out)
	}
}

func TestSamplerAlwaysSamplesRuns(t *testing.T) {
	s := &Sampler{Ratio: 0, rnd: func() float64 { return 0.999 }} // ratio 0 would normally always drop
	if !s.ShouldSample(KindRun, false) {
		t.Error("a run span must always be sampled regardless of ratio")
	}
}

func TestSamplerAlwaysSamplesErrorTraces(t *testing.T) {
	s := &Sampler{Ratio: 0, rnd: func() float64 { return 0.999 }} // ratio 0 would normally always drop
	if !s.ShouldSample(KindRequest, true) {
		t.Error("a request span carrying an error must always be sampled regardless of ratio")
	}
}

func TestSamplerRatioSamplesOrdinaryRequests(t *testing.T) {
	below := &Sampler{Ratio: 0.5, rnd: func() float64 { return 0.1 }}
	if !below.ShouldSample(KindRequest, false) {
		t.Error("rnd()=0.1 < ratio=0.5 should sample")
	}
	above := &Sampler{Ratio: 0.5, rnd: func() float64 { return 0.9 }}
	if above.ShouldSample(KindRequest, false) {
		t.Error("rnd()=0.9 >= ratio=0.5 should not sample")
	}
}

func TestSamplerRatioBoundaries(t *testing.T) {
	zero := &Sampler{Ratio: 0, rnd: func() float64 { return 0 }}
	if zero.ShouldSample(KindRequest, false) {
		t.Error("ratio<=0 must never sample an ordinary request")
	}
	full := &Sampler{Ratio: 1, rnd: func() float64 { return 0.999999 }}
	if !full.ShouldSample(KindRequest, false) {
		t.Error("ratio>=1 must always sample")
	}
}

func TestTailSampleProcessorDropsOrdinaryRequestsAtLowRatio(t *testing.T) {
	rec := &recordingExporter{}
	proc := &tailSampleProcessor{
		inner:   sdktrace.NewSimpleSpanProcessor(rec),
		sampler: &Sampler{Ratio: 0, rnd: func() float64 { return 0.999 }},
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(headSampler{}),
		sdktrace.WithSpanProcessor(proc),
	)
	ctx, span := tp.Tracer("t").Start(context.Background(), "http.request",
		oteltrace.WithAttributes(spanKindAttrKey.String(KindRequest.attrValue())))
	_ = ctx
	span.End()

	if len(rec.spans) != 0 {
		t.Errorf("ratio=0 KindRequest span should have been dropped, got %d exported", len(rec.spans))
	}
}

func TestTailSampleProcessorKeepsRunsAndErrors(t *testing.T) {
	rec := &recordingExporter{}
	proc := &tailSampleProcessor{
		inner:   sdktrace.NewSimpleSpanProcessor(rec),
		sampler: &Sampler{Ratio: 0, rnd: func() float64 { return 0.999 }},
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(headSampler{}),
		sdktrace.WithSpanProcessor(proc),
	)

	_, runSpan := tp.Tracer("t").Start(context.Background(), "generation.run",
		oteltrace.WithAttributes(spanKindAttrKey.String(KindRun.attrValue())))
	runSpan.End()

	_, errSpan := tp.Tracer("t").Start(context.Background(), "http.request",
		oteltrace.WithAttributes(spanKindAttrKey.String(KindRequest.attrValue())))
	errSpan.RecordError(errors.New("boom"))
	errSpan.SetStatus(codes.Error, "boom")
	errSpan.End()

	if len(rec.spans) != 2 {
		t.Fatalf("want 2 exported spans (run + error), got %d", len(rec.spans))
	}
}

type recordingExporter struct {
	spans []sdktrace.ReadOnlySpan
}

func (r *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.spans = append(r.spans, spans...)
	return nil
}
func (r *recordingExporter) Shutdown(context.Context) error { return nil }

func TestTraceContextEmptyWithNoActiveSpan(t *testing.T) {
	traceID, spanID := TraceContext(context.Background())
	if traceID != "" || spanID != "" {
		t.Errorf("TraceContext with no active span = (%q, %q), want (\"\", \"\")", traceID, spanID)
	}
}

func TestTraceContextReflectsActiveSpan(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "stdout", SampleRatio: 1})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	ctx, span := Start(context.Background(), "generation.run", KindRun)
	defer span.End()

	traceID, spanID := TraceContext(ctx)
	if traceID == "" || spanID == "" {
		t.Errorf("TraceContext(ctx) = (%q, %q), want non-empty ids for an active span", traceID, spanID)
	}
}

func TestShutdownGivesItselfADeadlineWhenCallerHasNone(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "stdout"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- shutdown(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung with no caller deadline; expected an internal bound")
	}
}

// TestOtlpTraceExporterConstructsAgainstAnUnreachableEndpoint is the test
// the coordinator asked for: otlptracegrpc.New dials lazily (grpc.NewClient
// does not connect at construction time), so Setup must succeed even when
// OTEL_EXPORTER_OTLP_ENDPOINT points at nothing — the exporter is not
// resolvable in the "no such host" sense, it is simply not reachable, and
// that distinction must not surface as a Setup error. Any real send failure
// belongs to the error handler, exercised separately below.
func TestOtlpTraceExporterConstructsAgainstAnUnreachableEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1") // port 1: nothing listens there
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "otlp"})
	if err != nil {
		t.Fatalf("Setup with an unreachable otlp endpoint must not fail construction: %v", err)
	}

	ctx, span := Start(context.Background(), "generation.run", KindRun)
	_ = ctx
	span.End() // queued on the BatchSpanProcessor; the export attempt (if any) happens async

	done := make(chan error, 1)
	go func() { done <- shutdown(context.Background()) }()
	select {
	case <-done:
		// Shutdown itself may return a non-nil error here (the batch flush
		// failing against the dead endpoint) or nil (nothing flushed yet) —
		// either is acceptable. What must not happen is a hang or a panic,
		// which the select's timeout branch below would catch.
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung against an unreachable otlp endpoint; expected the internal bound to apply")
	}
}

// TestOtlpMetricExporterConstructsAgainstAnUnreachableEndpoint is the
// metrics-provider counterpart of the trace test above.
func TestOtlpMetricExporterConstructsAgainstAnUnreachableEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "otlp"})
	if err != nil {
		t.Fatalf("Setup with an unreachable otlp endpoint must not fail construction: %v", err)
	}
	m, err := NewMetrics(GetMeterProvider())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if err := m.RecordProviderError(context.Background(), "rate_limited"); err != nil {
		t.Fatalf("RecordProviderError: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- shutdown(context.Background()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung against an unreachable otlp endpoint; expected the internal bound to apply")
	}
}

// TestUnknownExporterFallsBackToNoopWithoutCrashing covers the one
// remaining "unavailable" case left in this file: an AFF_OTEL_EXPORTER
// value that is neither "stdout" nor "otlp".
func TestUnknownExporterFallsBackToNoopWithoutCrashing(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "carrier-pigeon"})
	if err != nil {
		t.Fatalf("Setup with an unknown exporter must not fail startup: %v", err)
	}
	ctx, span := Start(context.Background(), "generation.run", KindRun)
	_ = ctx
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetupStdoutBuildsAWorkingMeterProvider(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	shutdown, err := Setup(context.Background(), Config{
		Enabled:     true,
		Exporter:    "stdout",
		ServiceName: "animefeedflux-test",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	mp := GetMeterProvider()
	m, err := NewMetrics(mp)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if err := m.RecordCacheResult(context.Background(), "hit"); err != nil {
		t.Fatalf("RecordCacheResult: %v", err)
	}

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_ = w.Close()
	os.Stdout = origStdout

	out, err := io.ReadAll(bufio.NewReader(r))
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("stdout metric exporter produced no output; expected the recorded aff_cache_hits_total point flushed on shutdown")
	}
	if !strings.Contains(string(out), "aff_cache_hits_total") {
		t.Errorf("stdout output does not mention aff_cache_hits_total:\n%s", out)
	}
}

func TestSetupDisabledInstallsNoopMeterProvider(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	mp := GetMeterProvider()
	if _, ok := mp.(metricnoop.MeterProvider); !ok {
		t.Errorf("GetMeterProvider() = %T, want metricnoop.MeterProvider", mp)
	}
}

func TestStdoutRecordEmitsValidJSONLine(t *testing.T) {
	// Belt-and-suspenders on top of TestSetupStdoutExporterEmitsASpan: parse
	// each line as JSON so a malformed encoder change fails loudly here
	// instead of merely producing a non-empty buffer.
	rec := &recordingExporter{}
	proc := &tailSampleProcessor{
		inner:   sdktrace.NewSimpleSpanProcessor(rec),
		sampler: NewSampler(1),
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(headSampler{}),
		sdktrace.WithSpanProcessor(proc),
	)
	_, span := tp.Tracer("t").Start(context.Background(), "generation.run",
		oteltrace.WithAttributes(spanKindAttrKey.String(KindRun.attrValue())))
	span.End()

	if len(rec.spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(rec.spans))
	}
	if rec.spans[0].Name() != "generation.run" {
		t.Errorf("span name = %q", rec.spans[0].Name())
	}
	// Confirm the recorded span really does round-trip as attributes a real
	// stdout exporter would serialize without error.
	attrs := map[string]string{}
	for _, a := range rec.spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		t.Fatalf("attributes did not marshal: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("empty attribute encoding")
	}
}

// ---- SetTracerProviderForTest / SetMeterProviderForTest ------------------
//
// These cover the test-injection hook a consumer package (internal/generate)
// needs so its own tests can install a recording TracerProvider and assert
// on span attributes directly, instead of standing up obs.Setup's real
// "stdout" exporter and parsing process stdout (see internal/generate's
// otel_test.go doc comment for the brittle-test shape this replaces).

func TestSetTracerProviderForTestInstallsAndRestores(t *testing.T) {
	original := GetTracerProvider()

	rec := &recordingExporter{}
	fake := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(rec),
	)
	restore := SetTracerProviderForTest(fake)

	if GetTracerProvider() != oteltrace.TracerProvider(fake) {
		t.Fatal("SetTracerProviderForTest did not install the given provider")
	}

	// Exercise it through the same Start() call site production code uses —
	// this is the point of the hook: no obs.Setup, no exporter, no stdout.
	_, span := Start(context.Background(), "generation.run", KindRun)
	span.End()
	if len(rec.spans) != 1 {
		t.Fatalf("got %d spans recorded through the injected provider, want 1", len(rec.spans))
	}
	if rec.spans[0].Name() != "generation.run" {
		t.Errorf("recorded span name = %q", rec.spans[0].Name())
	}

	restore()
	if GetTracerProvider() != original {
		t.Error("restore() did not put back the provider that was installed before")
	}
}

func TestSetMeterProviderForTestInstallsAndRestores(t *testing.T) {
	original := GetMeterProvider()

	reader := sdkmetric.NewManualReader()
	fake := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	restore := SetMeterProviderForTest(fake)

	if GetMeterProvider() != otelmetric.MeterProvider(fake) {
		t.Fatal("SetMeterProviderForTest did not install the given provider")
	}

	m, err := NewMetrics(GetMeterProvider())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if err := m.RecordCacheResult(context.Background(), "hit"); err != nil {
		t.Fatalf("RecordCacheResult: %v", err)
	}
	rm := collect(t, reader)
	if !collectedNames(rm)[MetricCacheHitsTotal] {
		t.Error("aff_cache_hits_total was not recorded through the injected provider")
	}

	restore()
	if GetMeterProvider() != original {
		t.Error("restore() did not put back the provider that was installed before")
	}
}

// ---- hanging exporter must not stall Setup's shutdown --------------------
//
// The coordinator's brief is explicit that this must be a HANG, not a
// refused connection: a refused connection (nothing listening, e.g. port 1)
// fails fast at the TCP layer and never exercises the shutdown bound at all.
// A listener that accepts the TCP connection but never reads or writes
// anything at the application layer reproduces what actually happens in
// production against a half-dead network path — the gRPC export call blocks
// waiting on bytes that never arrive, and only Setup's own internal timeout
// (otel.go's 5s default when the caller's context has no deadline) can save
// it.

// hangingListener accepts TCP connections and then does nothing with them —
// no HTTP/2 preface response, no close — so anything that dials it and
// waits for a reply hangs at the application layer rather than failing at
// the TCP layer the way an unreachable/refused address would.
func newHangingListener(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed by t.Cleanup
			}
			// Deliberately never read/write/close conn: the connection stays
			// open and silent, which is what makes the client hang instead
			// of seeing a reset or EOF.
			_ = conn
		}
	}()
	return ln.Addr()
}

func TestOtlpExporterHangingEndpointDoesNotStallShutdown(t *testing.T) {
	addr := newHangingListener(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+addr.String())
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	shutdown, err := Setup(context.Background(), Config{Enabled: true, Exporter: "otlp", SampleRatio: 1})
	if err != nil {
		t.Fatalf("Setup against a hanging endpoint must not fail construction: %v", err)
	}

	ctx, span := Start(context.Background(), "generation.run", KindRun)
	_ = ctx
	span.End()

	// Give the batch processor a moment to actually attempt a send against
	// the hanging listener, so shutdown's flush genuinely has something to
	// wait on rather than shutting down before any send starts.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- shutdown(context.Background()) }()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 6*time.Second {
			t.Errorf("shutdown took %v against a hanging exporter; want it bounded near the internal 5s default", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung indefinitely against a hanging OTLP endpoint — telemetry must never be able to stall a run")
	}
}
