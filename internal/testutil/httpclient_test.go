package testutil

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticClientReturnsMappedResponse(t *testing.T) {
	const url = "https://upstream.example.com/feed.xml"
	client := StaticClient(map[string]StaticResponse{
		url: {
			Status: http.StatusCreated,
			Header: http.Header{"X-Test": []string{"yes"}},
			Body:   []byte("<rss></rss>"),
		},
	})

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if got := resp.Header.Get("X-Test"); got != "yes" {
		t.Fatalf("Header X-Test = %q, want %q", got, "yes")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "<rss></rss>" {
		t.Fatalf("Body = %q, want %q", body, "<rss></rss>")
	}
}

func TestStaticClientDefaultsStatusOK(t *testing.T) {
	const url = "https://upstream.example.com/no-status.xml"
	client := StaticClient(map[string]StaticResponse{
		url: {Body: []byte("ok")},
	})

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestStaticClientUnmappedURLFailsLoudly is RULE-1's whole point: a request
// for a URL nobody registered must error naming that URL, not silently
// succeed with an unexpected status.
func TestStaticClientUnmappedURLFailsLoudly(t *testing.T) {
	client := StaticClient(map[string]StaticResponse{
		"https://upstream.example.com/known.xml": {Body: []byte("x")},
	})

	_, err := client.Get("https://upstream.example.com/unknown.xml")
	if err == nil {
		t.Fatal("expected error for unmapped URL, got nil")
	}
	if !strings.Contains(err.Error(), "unknown.xml") {
		t.Fatalf("error %q does not name the unmapped URL", err.Error())
	}
}

// TestFileClientServesHeaderBlockFixture covers the "Status:"+headers form
// of the documented fixture format.
func TestFileClientServesHeaderBlockFixture(t *testing.T) {
	dir := t.TempDir()
	const url = "https://upstream.example.com/with-headers.xml"

	fixture := "Status: 404\r\nContent-Type: application/xml\r\n\r\n<error/>"
	writeFixture(t, dir, url, fixture)

	client := FileClient(dir)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/xml" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/xml")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<error/>" {
		t.Fatalf("Body = %q, want %q", body, "<error/>")
	}
}

// TestFileClientServesPlainBodyFixture covers the common case: a fixture
// file with no header block at all defaults to 200 and serves the whole
// file as the body, so a captured upstream payload can be dropped in
// verbatim.
func TestFileClientServesPlainBodyFixture(t *testing.T) {
	dir := t.TempDir()
	const url = "https://upstream.example.com/plain.xml"

	writeFixture(t, dir, url, "<rss><channel/></rss>")

	client := FileClient(dir)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<rss><channel/></rss>" {
		t.Fatalf("Body = %q, want %q", body, "<rss><channel/></rss>")
	}
}

// TestFileClientUnmappedURLFailsLoudly mirrors
// TestStaticClientUnmappedURLFailsLoudly for the file-backed transport.
func TestFileClientUnmappedURLFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	client := FileClient(dir)

	_, err := client.Get("https://upstream.example.com/missing.xml")
	if err == nil {
		t.Fatal("expected error for unmapped URL, got nil")
	}
	if !strings.Contains(err.Error(), "missing.xml") {
		t.Fatalf("error %q does not name the unmapped URL", err.Error())
	}
}

// TestFixtureFilenameStableAndDistinct pins down the documented mapping
// rule: same URL always maps to the same filename, and different URLs
// (almost certainly) map to different filenames.
func TestFixtureFilenameStableAndDistinct(t *testing.T) {
	a := fixtureFilename("https://upstream.example.com/a.xml")
	b := fixtureFilename("https://upstream.example.com/a.xml")
	c := fixtureFilename("https://upstream.example.com/b.xml")

	if a != b {
		t.Fatalf("fixtureFilename not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("fixtureFilename collided for distinct URLs: %q", a)
	}
	if !strings.HasSuffix(a, ".http") {
		t.Fatalf("fixtureFilename %q missing .http suffix", a)
	}
}

// writeFixture writes contents to the fixture file FileClient would look up
// for url inside dir, using t.TempDir so the test leaves nothing behind in
// the repo.
func writeFixture(t *testing.T, dir, url, contents string) {
	t.Helper()
	path := filepath.Join(dir, fixtureFilename(url))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", path, err)
	}
}
