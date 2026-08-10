package sources

import (
	"context"
	"net/http"
	"testing"

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
