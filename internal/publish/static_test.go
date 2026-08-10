package publish

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeStaticFixture stages a minimal build.sh output (app.wasm, its .gz
// sibling, wasm_exec.js, index.html) into a fresh temp dir and returns the
// handler built from it, so each test gets its own isolated snapshot.
func writeStaticFixture(t *testing.T) *StaticHandler {
	t.Helper()

	dir := t.TempDir()

	wasmBody := []byte("\x00asm-fake-wasm-body-for-testing-only")
	writeFile(t, dir, "app.wasm", wasmBody)

	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	if _, err := gw.Write(wasmBody); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	writeFile(t, dir, "app.wasm.gz", gz.Bytes())

	writeFile(t, dir, "wasm_exec.js", []byte("// glue"))
	writeFile(t, dir, "index.html", []byte("<!doctype html><title>admin</title>"))

	h, err := NewStaticHandler(dir)
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}
	return h
}

func writeFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestStaticHandler_GzipClientGetsCompressedWasm(t *testing.T) {
	h := writeStaticFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("Content-Type = %q, want application/wasm", ct)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", ce)
	}

	// Body must actually be gzip-compressed bytes, not the plain body
	// mislabeled.
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("response body is not valid gzip: %v", err)
	}
	defer gr.Close()
	plain, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("decompress response body: %v", err)
	}
	if string(plain) != "\x00asm-fake-wasm-body-for-testing-only" {
		t.Errorf("decompressed body mismatch: %q", plain)
	}
}

func TestStaticHandler_NonGzipClientGetsPlainWasm(t *testing.T) {
	h := writeStaticFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	// No Accept-Encoding header at all.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("Content-Type = %q, want application/wasm", ct)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want empty", ce)
	}
	if got := rec.Body.String(); got != "\x00asm-fake-wasm-body-for-testing-only" {
		t.Errorf("body = %q, want plain uncompressed body", got)
	}
}

func TestStaticHandler_ETagsDifferBetweenRepresentations(t *testing.T) {
	h := writeStaticFixture(t)

	plainRec := httptest.NewRecorder()
	h.ServeHTTP(plainRec, httptest.NewRequest(http.MethodGet, "/app.wasm", nil))

	gzipReq := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	gzipReq.Header.Set("Accept-Encoding", "gzip")
	gzipRec := httptest.NewRecorder()
	h.ServeHTTP(gzipRec, gzipReq)

	plainETag := plainRec.Header().Get("ETag")
	gzipETag := gzipRec.Header().Get("ETag")
	if plainETag == "" || gzipETag == "" {
		t.Fatalf("missing ETag: plain=%q gzip=%q", plainETag, gzipETag)
	}
	if plainETag == gzipETag {
		t.Errorf("ETag must differ between plain and gzip representations, both = %q", plainETag)
	}
}

func TestStaticHandler_VaryAcceptEncodingAlwaysPresent(t *testing.T) {
	h := writeStaticFixture(t)

	for _, path := range []string{"/app.wasm", "/index.html", "/wasm_exec.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
			t.Errorf("%s: Vary = %q, want Accept-Encoding", path, v)
		}
	}
}

func TestStaticHandler_ConditionalGetReturns304(t *testing.T) {
	h := writeStaticFixture(t)

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/app.wasm", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response had no ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response must have no body, got %d bytes", rec.Body.Len())
	}
}

func TestStaticHandler_ConditionalGetHonorsEncodingSpecificETag(t *testing.T) {
	h := writeStaticFixture(t)

	// Fetch the gzip ETag, then present it with a request that does NOT
	// accept gzip: the representation actually served (plain) has a
	// different ETag, so this must NOT be a 304.
	gzipReq := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	gzipReq.Header.Set("Accept-Encoding", "gzip")
	gzipRec := httptest.NewRecorder()
	h.ServeHTTP(gzipRec, gzipReq)
	gzipETag := gzipRec.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	req.Header.Set("If-None-Match", gzipETag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (plain representation has a different ETag)", rec.Code)
	}
}

func TestStaticHandler_MissingAssetIs404NotPanic(t *testing.T) {
	h := writeStaticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/does-not-exist.bin", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestStaticHandler_IndexServedAtRoot(t *testing.T) {
	h := writeStaticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

func TestStaticHandler_MethodNotAllowed(t *testing.T) {
	h := writeStaticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/app.wasm", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
}

func TestNewStaticHandler_MissingDirIsError(t *testing.T) {
	_, err := NewStaticHandler(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing serve directory, got nil")
	}
}
