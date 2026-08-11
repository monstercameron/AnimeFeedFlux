// Tests for PLAN.md §15.0a's publish-plane tracing (TODOS A9-20, A9-21,
// A9-23): exactly one http.request root span per request carrying
// route/status/cache; a render.feed child span only on a feed-document cache
// miss, never opened (and immediately closed) on a hit; and the tail
// sampler's "errors always sampled" rule holding for this plane's spans
// specifically, not just in the abstract (internal/obs/otel_test.go already
// covers Sampler.ShouldSample in isolation).
//
// Span assertions go through obs.Setup's real "stdout" exporter, the same
// pattern internal/generate/otel_test.go and internal/sources/fetch_test.go
// use: internal/obs (a package this change may only extend through its
// existing public surface, not rewire) exposes obs.Setup as the only
// supported way to install a TracerProvider, and capturing os.Stdout around
// Setup(Enabled:true, Exporter:"stdout") exercises the exact obs.Start path
// production goes through.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

// capturedSpan is the minimal shape this file needs out of stdouttrace's
// JSON-per-span output (a tracetest.SpanStub, field-for-field: Name,
// Attributes as []attribute.KeyValue, Status as {Code, Description}).
type capturedSpan struct {
	Name       string
	Attributes []struct {
		Key   string
		Value struct {
			Type  string
			Value json.RawMessage
		}
	}
	Status struct {
		Code        string
		Description string
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

func (s capturedSpan) attrInt(key string) (int64, bool) {
	for _, kv := range s.Attributes {
		if kv.Key != key {
			continue
		}
		var v int64
		if err := json.Unmarshal(kv.Value.Value, &v); err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// runWithStdoutTracing installs a real TracerProvider exporting to a
// captured os.Stdout for the duration of fn, then decodes every span JSON
// record written. cfg lets each test pick its own SampleRatio: 1.0 makes a
// non-error KindRequest span deterministic to assert on; 0 (with an induced
// error) exercises the "errors always sampled" override specifically, since
// a ratio of exactly 0 would otherwise drop every request span.
func runWithStdoutTracing(t *testing.T, ratio float64, fn func()) []capturedSpan {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	// Drain the pipe CONCURRENTLY, starting now.
	//
	// This used to copy from r only after shutdown() returned, which
	// deadlocked: an OS pipe has a fixed kernel buffer (~64 KiB), so once the
	// exporters had written that much with nobody reading, the next write
	// blocked forever — and shutdown() blocks waiting for those exporters, so
	// the drain that would have unblocked them was never reached. It survived
	// unnoticed because it is output-volume dependent: the two-request tests
	// stay under the buffer, and only TestHTTPRequestSpan_RecordedOnceAcross-
	// EveryRoute (eight requests, plus the OTel stdout METRIC exporter's
	// periodic dump of every counter) went over it. That one test hung for
	// the full 10-minute package timeout, taking `go test ./...` red with it.
	var buf bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&buf, r)
	}()

	shutdown, err := obs.Setup(context.Background(), obs.Config{
		Enabled:     true,
		Exporter:    "stdout",
		ServiceName: "animefeedflux-test",
		SampleRatio: ratio,
	})
	if err != nil {
		os.Stdout = origStdout
		t.Fatalf("obs.Setup: %v", err)
	}

	fn()

	_ = shutdown(context.Background())
	if resetShutdown, rerr := obs.Setup(context.Background(), obs.Config{Enabled: false}); rerr == nil {
		_ = resetShutdown(context.Background())
	}

	os.Stdout = origStdout
	// Closing the writer ends the io.Copy above; wait for it before reading
	// buf, or this races the drain goroutine.
	_ = w.Close()
	<-drained
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

// newTracedTestServer is newTestServer, minus the deleted-item fixture this
// file's tests don't need, built with a fixed clock so span timing
// assertions (none of these tests make any, but the fixture matches the
// rest of the package) stay deterministic.
func newTracedTestServer(t *testing.T) (http.Handler, *fakeBackend) {
	t.Helper()
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	return NewServer(b.deps(now)), b
}

// TestHTTPRequestSpan_OneRootSpanWithRouteStatusCache is A9-20: every
// request opens exactly one http.request span, carrying the same bounded
// route pattern, status, and cache axis as the log-side wide event
// (server_test.go's TestHTTPRequestWideEventCarriesRequiredFields covers the
// log side; this is the trace side of the same requirement).
func TestHTTPRequestSpan_OneRootSpanWithRouteStatusCache(t *testing.T) {
	h, _ := newTracedTestServer(t)

	spans := runWithStdoutTracing(t, 1.0, func() {
		rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	reqSpans := spansNamed(spans, "http.request")
	if len(reqSpans) != 1 {
		t.Fatalf("got %d http.request spans, want exactly 1 (all spans: %+v)", len(reqSpans), spans)
	}
	span := reqSpans[0]

	if route, ok := span.attrString(obs.FieldRoute); !ok || route != "/feeds/{slug}.xml" {
		t.Errorf("route attribute = %q, ok=%v, want %q", route, ok, "/feeds/{slug}.xml")
	}
	if status, ok := span.attrInt(obs.FieldStatus); !ok || status != http.StatusOK {
		t.Errorf("status attribute = %d, ok=%v, want %d", status, ok, http.StatusOK)
	}
	if cache, ok := span.attrString(obs.FieldCache); !ok || cache != "miss" {
		t.Errorf("cache attribute = %q, ok=%v, want %q", cache, ok, "miss")
	}
	if span.Status.Code == "Error" {
		t.Errorf("a 200 response must not mark the span an error, got Status=%+v", span.Status)
	}
}

// TestHTTPRequestSpan_RecordedOnceAcrossEveryRoute is the trace-side
// counterpart of server_test.go's TestHTTPRequestRecordedExactlyOnce: every
// route this plane serves opens exactly one http.request span, including
// the early-return 404/405 branches.
func TestHTTPRequestSpan_RecordedOnceAcrossEveryRoute(t *testing.T) {
	h, _ := newTracedTestServer(t)

	spans := runWithStdoutTracing(t, 1.0, func() {
		do(h, http.MethodGet, "/", nil)
		do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
		do(h, http.MethodGet, "/feeds/does-not-exist.xml", nil)
		do(h, http.MethodPost, "/feeds/trivia-daily.xml", nil)
		do(h, http.MethodGet, "/items/01J000000000000000000ALIVE", nil)
		do(h, http.MethodGet, "/healthz", nil)
		do(h, http.MethodGet, "/robots.txt", nil)
		do(h, http.MethodGet, "/favicon.ico", nil)
	})

	reqSpans := spansNamed(spans, "http.request")
	if len(reqSpans) != 8 {
		t.Fatalf("got %d http.request spans for 8 requests, want exactly 8: %+v", len(reqSpans), spans)
	}
}

// TestRenderFeedSpan_OpensOnlyOnCacheMiss is A9-21: the first request for a
// feed document (a cache miss) gets a render.feed child span carrying
// format/items/bytes; a second request for the same document (a cache hit)
// must NOT open one at all — not open-and-immediately-close, genuinely
// absent — because a hit has to stay as cheap as A9-04 already requires.
func TestRenderFeedSpan_OpensOnlyOnCacheMiss(t *testing.T) {
	h, _ := newTracedTestServer(t)

	var missCode, hitCode int
	spans := runWithStdoutTracing(t, 1.0, func() {
		missCode = do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil).Code
		hitCode = do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil).Code
	})
	if missCode != http.StatusOK || hitCode != http.StatusOK {
		t.Fatalf("miss status = %d, hit status = %d, want 200/200", missCode, hitCode)
	}

	renderSpans := spansNamed(spans, "render.feed")
	if len(renderSpans) != 1 {
		t.Fatalf("got %d render.feed spans across one miss + one hit, want exactly 1: %+v", len(renderSpans), spans)
	}
	span := renderSpans[0]

	if format, ok := span.attrString("format"); !ok || format != "xml" {
		t.Errorf("format attribute = %q, ok=%v, want %q", format, ok, "xml")
	}
	if items, ok := span.attrInt("items"); !ok || items != 1 {
		t.Errorf("items attribute = %d, ok=%v, want 1", items, ok)
	}
	if bytesAttr, ok := span.attrInt("bytes"); !ok || bytesAttr <= 0 {
		t.Errorf("bytes attribute = %d, ok=%v, want > 0", bytesAttr, ok)
	}

	reqSpans := spansNamed(spans, "http.request")
	if len(reqSpans) != 2 {
		t.Fatalf("got %d http.request spans for miss+hit, want exactly 2", len(reqSpans))
	}
	if cache, _ := reqSpans[0].attrString(obs.FieldCache); cache != "miss" {
		t.Errorf("first request's http.request cache attribute = %q, want %q", cache, "miss")
	}
	if cache, _ := reqSpans[1].attrString(obs.FieldCache); cache != "hit" {
		t.Errorf("second request's http.request cache attribute = %q, want %q", cache, "hit")
	}
}

// TestHTTPRequestSpan_ErrorAlwaysSampledRegardlessOfRatio is A9-23's
// specific claim for this plane: a 5xx response marks its http.request span
// an error via SetStatus(codes.Error, ...), which is exactly the signal
// internal/obs's tailSampleProcessor checks to keep a span regardless of the
// sample ratio. It drives obs.Config.SampleRatio to the lowest value the
// config actually honors — obs.Setup floors anything <= 0 to the documented
// 0.05 default (otel.go), so 0 and 0.05 select the same ratio; a request at
// that ratio has only a 5% chance of being kept by chance alone, so running
// this against a real (non-injected) RNG and still seeing the span makes the
// always-sample-errors override, not luck, the only credible explanation.
// internal/obs/otel_test.go's TestSamplerAlwaysSamplesErrorTraces already
// covers the Sampler type in isolation with a deterministic rnd; this test
// is the end-to-end proof the override actually reaches this plane's spans.
func TestHTTPRequestSpan_ErrorAlwaysSampledRegardlessOfRatio(t *testing.T) {
	b := newFakeBackend()
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	deps := b.deps(now)
	deps.GetFeed = func(context.Context, string) (model.Feed, bool, error) {
		return model.Feed{}, false, errors.New("backend unavailable")
	}
	h := NewServer(deps)

	spans := runWithStdoutTracing(t, 0, func() {
		rec := do(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	reqSpans := spansNamed(spans, "http.request")
	if len(reqSpans) != 1 {
		t.Fatalf("got %d http.request spans for the errored request at the floor ratio, want exactly 1 (the always-sample-errors override should have kept it): %+v", len(reqSpans), spans)
	}
	span := reqSpans[0]
	if span.Status.Code != "Error" {
		t.Errorf("http.request span Status = %+v, want Code=Error for a 500 response", span.Status)
	}
	if status, ok := span.attrInt(obs.FieldStatus); !ok || status != http.StatusInternalServerError {
		t.Errorf("status attribute = %d, ok=%v, want %d", status, ok, http.StatusInternalServerError)
	}

	// The render.feed child span (opened before GetFeed's error is known)
	// must ALSO have been kept — it is a child of the same trace, and
	// §15.0a's rule is "any trace that contains an error", not just the
	// root span that happened to record it.
	renderSpans := spansNamed(spans, "render.feed")
	if len(renderSpans) != 1 {
		t.Fatalf("got %d render.feed spans for the errored request at ratio=0, want exactly 1", len(renderSpans))
	}
	if renderSpans[0].Status.Code != "Error" {
		t.Errorf("render.feed span Status = %+v, want Code=Error", renderSpans[0].Status)
	}
}
