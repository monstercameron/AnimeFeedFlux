// Tracing and metric-provider wiring for AnimeFeedFlux (PLAN.md §15.0a),
// built on the real go.opentelemetry.io/otel SDK.
//
// All exporter packages this file needs are direct dependencies and resolve
// cleanly: go.opentelemetry.io/otel, /sdk, /sdk/trace, /sdk/metric,
// /sdk/resource, /trace, /trace/noop, /metric, /metric/noop,
// /semconv/v1.26.0, /exporters/stdout/stdouttrace,
// /exporters/stdout/stdoutmetric,
// /exporters/otlp/otlptrace/otlptracegrpc, and
// /exporters/otlp/otlpmetric/otlpmetricgrpc. (Earlier in this task's history
// the three OTLP/stdoutmetric packages were not yet direct dependencies and
// this file used hand-rolled no-op fallbacks; that stopgap is gone now that
// they resolve — see git history if that era's rationale is ever needed
// again.)
package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Config configures tracing and metrics for the process. The zero value is
// the safe default: disabled, no exporter, no overhead beyond genuine no-op
// providers.
type Config struct {
	Enabled     bool
	Exporter    string // "otlp", "stdout", or "" (none)
	ServiceName string
	Version     string
	Commit      string
	SampleRatio float64 // publish-request sample ratio; runs always sample regardless
}

// spanKindAttrKey tags each span with which §15.0a traffic shape it belongs
// to, so the tail sampler (below) can tell a generation.run from an
// http.request without guessing from the span name.
const spanKindAttrKey = attribute.Key("aff.span_kind")

// SpanKind distinguishes the two traffic shapes §15.0a samples differently.
type SpanKind int

const (
	// KindRun covers generation.run and its children: rare, expensive (an
	// LLM call), and always sampled.
	KindRun SpanKind = iota
	// KindRequest covers http.request and its children: constant, mostly
	// 304s, and therefore ratio-sampled — except any trace that contains an
	// error, which is always kept (see Sampler).
	KindRequest
)

func (k SpanKind) attrValue() string {
	if k == KindRun {
		return "run"
	}
	return "request"
}

// Start begins a span on the process tracer (installed by Setup, or a real
// no-op TracerProvider before Setup runs / when tracing is disabled) and
// tags it with kind for the tail sampler.
func Start(ctx context.Context, name string, kind SpanKind) (context.Context, oteltrace.Span) {
	return getTracer().Start(ctx, name, oteltrace.WithAttributes(spanKindAttrKey.String(kind.attrValue())))
}

// TraceContext returns the trace_id and span_id of the span active in ctx,
// or "" for both if there is none.
//
// This is the enrichment hook §15.0a asks for so log records can carry
// trace_id/span_id: obs.go's contextHandler.Handle would call this exactly
// where it already calls RunID(ctx)/RequestID(ctx) and add the two
// attributes. Wiring that call is a deliberate follow-up — obs.go is
// off-limits for this change — but the function it needs already exists
// here with the right shape (ctx in, two strings out, "" when absent).
func TraceContext(ctx context.Context) (traceID, spanID string) {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// ---- Sampler -----------------------------------------------------------

// Sampler decides whether a finished span is kept, per §15.0a: runs always
// sample (rare, expensive); requests ratio-sample (constant, mostly 304s);
// and any span carrying an error is always kept regardless of kind or
// ratio, because the rare, interesting case is exactly what a random ratio
// would usually throw away.
//
// This has to be a TAIL decision (made at span end), not the OTel SDK's
// normal head decision (made at span start): a head sampler cannot know yet
// whether the span will error. So the SDK-level sdktrace.Sampler installed
// on the TracerProvider (see headSampler below) always records everything,
// and this Sampler is consulted from a custom SpanProcessor's OnEnd to
// decide what actually reaches the exporter. That trades a small amount of
// always-on in-process recording for the ability to honor "always keep
// errors" at all — the trade §15.0a explicitly wants ("a feed reader
// polling every fifteen minutes must not generate more telemetry than the
// content it fetches" is about export volume, not recording).
type Sampler struct {
	Ratio float64
	// rnd is overridden in tests for determinism; defaults to math/rand/v2.
	rnd func() float64
}

// NewSampler builds a Sampler for the given publish-request ratio.
func NewSampler(ratio float64) *Sampler {
	return &Sampler{Ratio: ratio, rnd: rand.Float64}
}

// ShouldSample implements the rule described on Sampler.
func (s *Sampler) ShouldSample(kind SpanKind, hasError bool) bool {
	if kind == KindRun || hasError {
		return true
	}
	if s.Ratio <= 0 {
		return false
	}
	if s.Ratio >= 1 {
		return true
	}
	r := s.rnd
	if r == nil {
		r = rand.Float64
	}
	return r() < s.Ratio
}

// headSampler is installed on the TracerProvider itself. It always records
// (RecordAndSample) so every span has real attributes/status to inspect;
// the actual sampling decision is applied later by tailSampleProcessor.
// Recording unconditionally is what makes instrumentation "written
// unconditionally" true at the SDK level too, not just at call sites.
type headSampler struct{}

func (headSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	return sdktrace.SamplingResult{
		Decision:   sdktrace.RecordAndSample,
		Tracestate: oteltrace.SpanContextFromContext(p.ParentContext).TraceState(),
	}
}

func (headSampler) Description() string { return "aff-head-sampler(always-record)" }

// tailSampleProcessor wraps a real SpanProcessor and only forwards OnEnd to
// it when Sampler.ShouldSample keeps the span, per §15.0a's rule. OnStart,
// Shutdown, and ForceFlush pass straight through.
type tailSampleProcessor struct {
	inner   sdktrace.SpanProcessor
	sampler *Sampler
}

func (p *tailSampleProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.inner.OnStart(parent, s)
}

func (p *tailSampleProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	kind := KindRequest
	for _, a := range s.Attributes() {
		if a.Key == spanKindAttrKey && a.Value.AsString() == "run" {
			kind = KindRun
			break
		}
	}
	hasError := s.Status().Code == codes.Error
	if !p.sampler.ShouldSample(kind, hasError) {
		return
	}
	p.inner.OnEnd(s)
}

func (p *tailSampleProcessor) Shutdown(ctx context.Context) error   { return p.inner.Shutdown(ctx) }
func (p *tailSampleProcessor) ForceFlush(ctx context.Context) error { return p.inner.ForceFlush(ctx) }

// ---- exporters -------------------------------------------------------------

// ErrExporterUnavailable is surfaced (via the OTel error handler, never
// returned from Setup) for an AFF_OTEL_EXPORTER value this package does not
// recognize — Setup falls back to a no-op provider rather than failing
// startup over it, because telemetry is not the product.
//
// It is deliberately NOT used for "OTLP endpoint is unreachable": the OTLP
// gRPC exporters connect lazily (grpc.NewClient does not dial at
// construction), so Setup succeeds even against a dead endpoint, and actual
// export failures surface later, per-attempt, through
// otel.SetErrorHandler(defaultErrHandler) — the same log-and-continue path,
// just triggered by the SDK itself rather than by this file.
var ErrExporterUnavailable = errors.New("obs: unknown AFF_OTEL_EXPORTER value, falling back to a no-op provider")

// ---- global tracer -----------------------------------------------------

var (
	tracerMu sync.RWMutex
	// tracerProvider defaults to the real OTel no-op implementation — a
	// genuine no-op provider, not a nil check at each call site, per
	// §15.0a's explicit requirement. Start()/RecordError()/End() all run
	// unconditionally against it and do real (cheap) work that goes nowhere.
	tracerProvider oteltrace.TracerProvider = tracenoop.NewTracerProvider()

	meterMu sync.RWMutex
	// meterProvider mirrors tracerProvider's default-off story for metrics:
	// a genuine no-op MeterProvider until Setup installs a real one.
	meterProvider otelmetric.MeterProvider = metricnoop.NewMeterProvider()
)

func getTracer() oteltrace.Tracer {
	tracerMu.RLock()
	defer tracerMu.RUnlock()
	return tracerProvider.Tracer("animefeedflux")
}

// GetTracerProvider returns the process TracerProvider installed by the
// most recent Setup call (or the no-op default if Setup has not run).
func GetTracerProvider() oteltrace.TracerProvider {
	tracerMu.RLock()
	defer tracerMu.RUnlock()
	return tracerProvider
}

func setTracerProvider(tp oteltrace.TracerProvider) {
	tracerMu.Lock()
	tracerProvider = tp
	tracerMu.Unlock()
}

// GetMeterProvider returns the process MeterProvider installed by the most
// recent Setup call (or the no-op default if Setup has not run). Pass this
// to NewMetrics (metrics.go) to register the §15.0a instrument set —
// NewMetrics also accepts any other otelmetric.MeterProvider directly
// (e.g. one backed by a ManualReader), which is what tests do.
func GetMeterProvider() otelmetric.MeterProvider {
	meterMu.RLock()
	defer meterMu.RUnlock()
	return meterProvider
}

func setMeterProvider(mp otelmetric.MeterProvider) {
	meterMu.Lock()
	meterProvider = mp
	meterMu.Unlock()
}

// SetTracerProviderForTest installs tp as the process TracerProvider and
// returns a restore func that puts back whatever was installed before —
// call it (typically via t.Cleanup) before the calling test returns. This is
// the seam a consumer package's test needs to assert on span attributes
// without standing up obs.Setup's real stdout exporter and parsing process
// stdout — e.g. install an sdktrace.NewTracerProvider backed by a recording
// SpanProcessor, call the code under test (which reaches obs.Start the
// normal way), then inspect the recorder directly.
//
// This does NOT take a testing.TB parameter, deliberately: an earlier
// version did, on the theory that requiring a live *testing.T/B would make
// misuse from production code impossible. It doesn't — "testing" would still
// have to be imported by this file to spell that parameter type, and
// importing "testing" into a non-test file pulls the whole test harness
// (including its own flag.CommandLine registrations) into every binary that
// links this package, which is a worse defect than the one being guarded
// against. An export_test.go helper avoids that import but doesn't solve the
// actual problem either: _test.go files are invisible outside the package
// they live in, so a consumer package's own test file (e.g.
// internal/generate/otel_test.go, the test this exists for) could not call
// it at all. So the guard here is the name and this comment, not the type
// system: ForTest means for tests, never call it from a production code
// path. Setup remains the only supported way to install a provider for a
// running process; this mutates the exact same package-level var Setup
// does. Do not call it from two tests running in parallel against the same
// package-level state without coordinating — it is process-wide state, same
// as Setup's.
func SetTracerProviderForTest(tp oteltrace.TracerProvider) (restore func()) {
	prev := GetTracerProvider()
	setTracerProvider(tp)
	return func() { setTracerProvider(prev) }
}

// SetMeterProviderForTest is SetTracerProviderForTest's metrics counterpart,
// and the same "ForTest is the whole guard, not testing.TB" reasoning
// applies. Most metrics tests don't need this at all — NewMetrics
// (metrics.go) already accepts any otelmetric.MeterProvider directly, so a
// test can pass a sdkmetric.MeterProvider backed by a ManualReader straight
// to NewMetrics with no global involved. This exists only for the narrower
// case: code under test that calls obs.GetMeterProvider() itself (rather
// than receiving a *Metrics via injection) and therefore needs the
// package-level provider swapped out from under it.
func SetMeterProviderForTest(mp otelmetric.MeterProvider) (restore func()) {
	prev := GetMeterProvider()
	setMeterProvider(mp)
	return func() { setMeterProvider(prev) }
}

// defaultErrHandler logs and continues — OTel's "a failing exporter must
// never block or crash the app" requirement, applied via
// otel.SetErrorHandler so it covers export errors the SDK itself surfaces
// (e.g. an OTLP export attempt against an unreachable endpoint), not just
// the ones this package generates directly.
var defaultErrHandler = func(err error) {
	if err == nil {
		return
	}
	slog.Default().Warn("obs: telemetry error", "error", err)
}

func noopShutdown(context.Context) error { return nil }

// buildResource assembles the resource attributes shared by traces and
// metrics, so a trace and a metric from the same process always identify
// the same service/version/commit.
func buildResource(cfg Config) *sdkresource.Resource {
	return sdkresource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
		attribute.String("service.commit", cfg.Commit),
	)
}

// Setup builds the process TracerProvider and MeterProvider per cfg and
// returns a combined shutdown func.
//
// Enabled=false installs the real oteltrace/noop and otelmetric/noop
// providers rather than leaving callers to check a flag themselves — see
// the package doc comment for why that distinction matters. shutdown is
// then a true no-op: nothing was ever opened, so there is nothing to flush.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(defaultErrHandler))

	if !cfg.Enabled {
		setTracerProvider(tracenoop.NewTracerProvider())
		setMeterProvider(metricnoop.NewMeterProvider())
		return noopShutdown, nil
	}

	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 0.05 // matches AFF_TRACE_SAMPLE_RATIO's documented default
	}

	res := buildResource(cfg)

	tp, traceShutdown, err := setupTraces(ctx, cfg, res, ratio)
	if err != nil {
		return nil, err
	}
	setTracerProvider(tp)

	mp, metricShutdown, err := setupMetrics(ctx, cfg, res)
	if err != nil {
		return nil, err
	}
	setMeterProvider(mp)

	shutdownFn := func(shutdownCtx context.Context) error {
		// The caller's context may have no deadline (e.g. a bare
		// context.Background() passed from a signal handler); shutdown must
		// not hang forever waiting on a network exporter, so it gets its own
		// bound when the caller didn't set one.
		if _, ok := shutdownCtx.Deadline(); !ok {
			var cancel context.CancelFunc
			shutdownCtx, cancel = context.WithTimeout(shutdownCtx, 5*time.Second)
			defer cancel()
		}
		done := make(chan error, 1)
		go func() {
			done <- errors.Join(traceShutdown(shutdownCtx), metricShutdown(shutdownCtx))
		}()
		select {
		case shutdownErr := <-done:
			return shutdownErr
		case <-shutdownCtx.Done():
			return shutdownCtx.Err()
		}
	}
	return shutdownFn, nil
}

// setupTraces builds the TracerProvider for the Enabled=true path.
//
// The exporter choice also picks the processor: "stdout" uses
// SimpleSpanProcessor (synchronous export on OnEnd) because it makes "a
// span is emitted" deterministic in a short-lived test/dev process and this
// app's volume never needs batching's throughput; "otlp" uses
// BatchSpanProcessor because it is a real network call and §15.0a's whole
// point for that mode is "straight to a hosted backend" without a local
// collector doing the batching for it.
func setupTraces(ctx context.Context, cfg Config, res *sdkresource.Resource, ratio float64) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	var (
		traceExporter sdktrace.SpanExporter
		processor     sdktrace.SpanProcessor
	)

	switch cfg.Exporter {
	case "stdout":
		exp, err := stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
		if err != nil {
			return nil, nil, fmt.Errorf("obs: building stdout trace exporter: %w", err)
		}
		traceExporter = exp
		processor = sdktrace.NewSimpleSpanProcessor(traceExporter)
	case "otlp":
		// otlptracegrpc.New dials lazily (no WithBlock/grpc.NewClient does
		// not connect at construction), so this succeeds even when
		// OTEL_EXPORTER_OTLP_ENDPOINT points nowhere; a real failure to
		// reach the backend surfaces later, per export attempt, through
		// otel.SetErrorHandler. No options are passed beyond the resource
		// attributes carried by the provider: the standard
		// OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_HEADERS (and
		// their _TRACES_ variants) env vars are read internally by the
		// exporter per the OTel spec, which is what §16 documents as
		// "honoured as-is."
		exp, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("obs: constructing otlp trace exporter: %w", err)
		}
		traceExporter = exp
		processor = sdktrace.NewBatchSpanProcessor(traceExporter)
	case "", "none":
		return sdktrace.NewTracerProvider( // no exporter configured: record-and-drop
			sdktrace.WithSampler(headSampler{}),
			sdktrace.WithResource(res),
		), noopShutdown, nil
	default:
		defaultErrHandler(fmt.Errorf("%w: %q", ErrExporterUnavailable, cfg.Exporter))
		return sdktrace.NewTracerProvider(
			sdktrace.WithSampler(headSampler{}),
			sdktrace.WithResource(res),
		), noopShutdown, nil
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(headSampler{}),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(&tailSampleProcessor{inner: processor, sampler: NewSampler(ratio)}),
	)
	return tp, tp.Shutdown, nil
}

// setupMetrics builds the MeterProvider for the Enabled=true path. Unlike
// traces, metrics have no per-signal tail-sampling requirement in
// §15.0a — the ten instruments are cheap, low-cardinality counters and
// histograms, so they are always exported at whatever interval the reader
// uses, with no equivalent of tailSampleProcessor.
func setupMetrics(ctx context.Context, cfg Config, res *sdkresource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	var reader sdkmetric.Reader

	switch cfg.Exporter {
	case "stdout":
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stdout))
		if err != nil {
			return nil, nil, fmt.Errorf("obs: building stdout metric exporter: %w", err)
		}
		reader = sdkmetric.NewPeriodicReader(exp)
	case "otlp":
		// Same lazy-dial reasoning as the trace exporter: construction
		// succeeds against an unreachable endpoint; export failures are
		// reported per-attempt via otel.SetErrorHandler.
		exp, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("obs: constructing otlp metric exporter: %w", err)
		}
		reader = sdkmetric.NewPeriodicReader(exp)
	case "", "none":
		return sdkmetric.NewMeterProvider(sdkmetric.WithResource(res)), noopShutdown, nil
	default:
		// Setup already reported ErrExporterUnavailable once via
		// setupTraces for the same cfg.Exporter; no need to log it twice.
		return sdkmetric.NewMeterProvider(sdkmetric.WithResource(res)), noopShutdown, nil
	}

	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))
	return mp, mp.Shutdown, nil
}
