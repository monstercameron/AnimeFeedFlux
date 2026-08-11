package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// recordingTransport wraps another RoundTripper and captures the last
// request it saw, so tests can assert on headers the caller actually sent
// (StaticClient itself does not record requests).
type recordingTransport struct {
	inner   http.RoundTripper
	lastReq *http.Request
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.lastReq = req
	return r.inner.RoundTrip(req)
}

func newRecordingClient(responses map[string]testutil.StaticResponse) (*http.Client, *recordingTransport) {
	c := testutil.StaticClient(responses)
	rec := &recordingTransport{inner: c.Transport}
	c.Transport = rec
	return c, rec
}

func TestFetch_ParsesAndNormalizesCandidateURLs(t *testing.T) {
	rss := readFixture(t, "rss_sample.xml")
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Body: rss},
	})

	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}
	cands, res, err := f.FetchCandidates(context.Background(), "https://example.com/feed.xml", "Anime News Wire", "", "")
	if err != nil {
		t.Fatalf("FetchCandidates: %v", err)
	}
	if res.NotModified {
		t.Fatal("unexpected NotModified")
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}

	got := cands[0].URL
	want := "https://example.com/articles/new-season"
	if got != want {
		t.Errorf("candidate URL = %q, want tracking params stripped: %q", got, want)
	}
}

func TestFetch_NotModified(t *testing.T) {
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Status: http.StatusNotModified},
	})

	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}
	res, err := f.Fetch(context.Background(), "https://example.com/feed.xml", `"etag-1"`, "Thu, 04 Oct 2007 23:59:45 GMT")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.NotModified {
		t.Error("expected NotModified = true")
	}
	if len(res.Body) != 0 {
		t.Errorf("expected empty body on 304, got %d bytes", len(res.Body))
	}
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("StatusCode = %d, want 304", res.StatusCode)
	}
}

func TestFetch_SendsConditionalHeaders(t *testing.T) {
	rss := readFixture(t, "rss_sample.xml")
	client, rec := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Body: rss},
	})

	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}
	_, err := f.Fetch(context.Background(), "https://example.com/feed.xml", `"my-etag"`, "Thu, 04 Oct 2007 23:59:45 GMT")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rec.lastReq == nil {
		t.Fatal("no request recorded")
	}
	if got := rec.lastReq.Header.Get("If-None-Match"); got != `"my-etag"` {
		t.Errorf("If-None-Match = %q", got)
	}
	if got := rec.lastReq.Header.Get("If-Modified-Since"); got != "Thu, 04 Oct 2007 23:59:45 GMT" {
		t.Errorf("If-Modified-Since = %q", got)
	}
}

func TestFetch_NoConditionalHeadersWhenNotSupplied(t *testing.T) {
	rss := readFixture(t, "rss_sample.xml")
	client, rec := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Body: rss},
	})

	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}
	if _, err := f.Fetch(context.Background(), "https://example.com/feed.xml", "", ""); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := rec.lastReq.Header.Get("If-None-Match"); got != "" {
		t.Errorf("If-None-Match should be empty, got %q", got)
	}
	if got := rec.lastReq.Header.Get("If-Modified-Since"); got != "" {
		t.Errorf("If-Modified-Since should be empty, got %q", got)
	}
}

func TestFetch_BodyOverMaxBytesRejected(t *testing.T) {
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'x'
	}
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Body: big},
	})

	f := &Fetcher{Client: client, MaxBytes: 10}
	_, err := f.Fetch(context.Background(), "https://example.com/feed.xml", "", "")
	if err == nil {
		t.Fatal("expected error for body exceeding MaxBytes")
	}
}

// --- sources.fetch span (PLAN.md §15.0a, TODOS A6-17) ---
//
// Same pattern internal/generate/otel_test.go uses: real tracing through
// obs.Setup's stdout exporter, captured off a redirected os.Stdout, so this
// exercises the exact obs.Start/span.SetAttributes path production uses
// rather than a hand-wired TracerProvider.

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

func (s capturedSpan) hasAttr(key string) bool {
	for _, kv := range s.Attributes {
		if kv.Key == key {
			return true
		}
	}
	return false
}

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

func TestFetch_EmitsSourcesFetchSpan_OnSuccess(t *testing.T) {
	rss := readFixture(t, "rss_sample.xml")
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml?token=secret123": {Body: rss},
	})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	spans := runWithStdoutTracing(t, func() {
		if _, err := f.Fetch(context.Background(), "https://example.com/feed.xml?token=secret123", "", ""); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1 (all spans: %+v)", len(fetchSpans), spans)
	}
	span := fetchSpans[0]

	if host, ok := span.attrString("host"); !ok || host != "example.com" {
		t.Errorf("host attribute = %q, ok=%v, want %q", host, ok, "example.com")
	}
	if outcome, ok := span.attrString(obs.FieldOutcome); !ok || outcome != string(obs.OutcomeSuccess) {
		t.Errorf("outcome attribute = %q, ok=%v, want %q", outcome, ok, obs.OutcomeSuccess)
	}
	if status, ok := span.attrInt(obs.FieldStatus); !ok || status != http.StatusOK {
		t.Errorf("status attribute = %d, ok=%v, want %d", status, ok, http.StatusOK)
	}

	// The full URL -- including its query string, which is the whole point:
	// a token or tracking param must never leak into a span attribute --
	// must never appear as any attribute value.
	for _, kv := range span.Attributes {
		var v string
		if err := json.Unmarshal(kv.Value.Value, &v); err != nil {
			continue
		}
		if strings.Contains(v, "secret123") || strings.Contains(v, "feed.xml") {
			t.Errorf("span attribute %s carries the full URL/query secret: %q", kv.Key, v)
		}
	}
}

func TestFetch_EmitsSourcesFetchSpan_OnFailure(t *testing.T) {
	// No fixture registered for this URL: staticTransport.RoundTrip fails
	// loudly (its own doc comment), which stands in for a real transport
	// error (connection reset, DNS failure, ...) for this test's purpose --
	// asserting the span records outcome=failed, not exercising a specific
	// failure cause.
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	spans := runWithStdoutTracing(t, func() {
		if _, err := f.Fetch(context.Background(), "https://example.com/feed.xml", "", ""); err == nil {
			t.Fatal("Fetch: expected an error, got nil")
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1 (all spans: %+v)", len(fetchSpans), spans)
	}
	span := fetchSpans[0]

	if host, ok := span.attrString("host"); !ok || host != "example.com" {
		t.Errorf("host attribute = %q, ok=%v, want %q", host, ok, "example.com")
	}
	if outcome, ok := span.attrString(obs.FieldOutcome); !ok || outcome != string(obs.OutcomeFailed) {
		t.Errorf("outcome attribute = %q, ok=%v, want %q", outcome, ok, obs.OutcomeFailed)
	}
}

func TestFetch_EmitsSourcesFetchSpan_On304(t *testing.T) {
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Status: http.StatusNotModified},
	})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	spans := runWithStdoutTracing(t, func() {
		if _, err := f.Fetch(context.Background(), "https://example.com/feed.xml", "", ""); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1", len(fetchSpans))
	}
	span := fetchSpans[0]
	if outcome, _ := span.attrString(obs.FieldOutcome); outcome != string(obs.OutcomeSuccess) {
		t.Errorf("outcome for a 304 = %q, want %q (a 304 is a successful fetch)", outcome, obs.OutcomeSuccess)
	}
	if status, ok := span.attrInt(obs.FieldStatus); !ok || status != http.StatusNotModified {
		t.Errorf("status attribute = %d, ok=%v, want %d", status, ok, http.StatusNotModified)
	}
}

// --- FetchCandidates' sources.fetch span carries item count (A6-17) ---
//
// A6-17 was PARTIAL because Fetch's span closed before Parse ever ran, so
// item count could never be attached to it. FetchCandidates now owns the
// span itself and keeps it open through Parse -- these tests are the check
// for that ticket's stated requirement: url (via host), status, whether it
// 304'd, and item count, all on the SAME span.

func TestFetchCandidates_EmitsSourcesFetchSpan_WithItemCount(t *testing.T) {
	rss := readFixture(t, "rss_sample.xml")
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Body: rss},
	})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	var cands []Candidate
	spans := runWithStdoutTracing(t, func() {
		var err error
		cands, _, err = f.FetchCandidates(context.Background(), "https://example.com/feed.xml", "Anime News Wire", "", "")
		if err != nil {
			t.Fatalf("FetchCandidates: %v", err)
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1 (all spans: %+v)", len(fetchSpans), spans)
	}
	span := fetchSpans[0]

	if outcome, ok := span.attrString(obs.FieldOutcome); !ok || outcome != string(obs.OutcomeSuccess) {
		t.Errorf("outcome attribute = %q, ok=%v, want %q", outcome, ok, obs.OutcomeSuccess)
	}
	if status, ok := span.attrInt(obs.FieldStatus); !ok || status != http.StatusOK {
		t.Errorf("status attribute = %d, ok=%v, want %d", status, ok, http.StatusOK)
	}
	items, ok := span.attrInt("items")
	if !ok {
		t.Fatal("items attribute missing from the sources.fetch span")
	}
	if int(items) != len(cands) {
		t.Errorf("items attribute = %d, want %d (len(cands))", items, len(cands))
	}
	if items == 0 {
		t.Fatal("items attribute = 0, want > 0 for a feed with candidates (test would pass vacuously)")
	}
}

func TestFetchCandidates_EmitsSourcesFetchSpan_ZeroItemsOn304(t *testing.T) {
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{
		"https://example.com/feed.xml": {Status: http.StatusNotModified},
	})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	spans := runWithStdoutTracing(t, func() {
		if _, _, err := f.FetchCandidates(context.Background(), "https://example.com/feed.xml", "Anime News Wire", "", ""); err != nil {
			t.Fatalf("FetchCandidates: %v", err)
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1", len(fetchSpans))
	}
	span := fetchSpans[0]

	if outcome, _ := span.attrString(obs.FieldOutcome); outcome != string(obs.OutcomeSuccess) {
		t.Errorf("outcome for a 304 = %q, want %q", outcome, obs.OutcomeSuccess)
	}
	items, ok := span.attrInt("items")
	if !ok {
		t.Fatal("items attribute missing on a 304 -- a conditional GET that never revalidates must still be visible")
	}
	if items != 0 {
		t.Errorf("items attribute on a 304 = %d, want 0 (a 304 fetched nothing new)", items)
	}
}

func TestFetchCandidates_EmitsSourcesFetchSpan_OnFailure(t *testing.T) {
	client, _ := newRecordingClient(map[string]testutil.StaticResponse{})
	f := &Fetcher{Client: client, MaxBytes: MaxParseBytes}

	spans := runWithStdoutTracing(t, func() {
		if _, _, err := f.FetchCandidates(context.Background(), "https://example.com/feed.xml", "Anime News Wire", "", ""); err == nil {
			t.Fatal("FetchCandidates: expected an error, got nil")
		}
	})

	fetchSpans := spansNamed(spans, "sources.fetch")
	if len(fetchSpans) != 1 {
		t.Fatalf("got %d sources.fetch spans, want exactly 1", len(fetchSpans))
	}
	span := fetchSpans[0]
	if outcome, _ := span.attrString(obs.FieldOutcome); outcome != string(obs.OutcomeFailed) {
		t.Errorf("outcome attribute = %q, ok, want %q", outcome, obs.OutcomeFailed)
	}
	if span.hasAttr("items") {
		t.Error("items attribute present on a failed fetch that never parsed anything; want it absent")
	}
}

func TestFetchHost_BoundedAndNeverTheFullURL(t *testing.T) {
	table := []struct {
		in   string
		want string
	}{
		{"https://example.com/feed.xml?token=secret", "example.com"},
		{"https://sub.example.com:8443/a/b/c", "sub.example.com:8443"},
		{"not a url at all", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range table {
		if got := fetchHost(tc.in); got != tc.want {
			t.Errorf("fetchHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDedupe_CollapsesTrackingParamVariants(t *testing.T) {
	cands := []Candidate{
		{Title: "A", URL: "https://example.com/articles/x?utm_source=a"},
		{Title: "A dup", URL: "https://example.com/articles/x?utm_source=b&fbclid=123"},
		{Title: "B", URL: "https://example.com/articles/y"},
	}
	out := Dedupe(cands)
	if len(out) != 2 {
		t.Fatalf("got %d candidates after Dedupe, want 2", len(out))
	}
	if out[0].Title != "A" {
		t.Errorf("Dedupe should keep the first occurrence, got %q", out[0].Title)
	}
	if out[1].Title != "B" {
		t.Errorf("unexpected second entry: %q", out[1].Title)
	}
}
