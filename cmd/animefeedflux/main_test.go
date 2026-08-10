package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestHealthzReturnsOKWithBuildInfo(t *testing.T) {
	// The container healthcheck reads only the status code, but a human
	// debugging a deploy reads the body — and "which build is this?" is the
	// first question they have.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	publishMux(testLogger()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — health must never be cached", cc)
	}

	var h health
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rec.Body.String())
	}
	if h.Status != "ok" {
		t.Errorf("status = %q, want ok", h.Status)
	}
	if h.Version == "" || h.Commit == "" {
		t.Errorf("build info missing from body: %+v", h)
	}
}

func TestPublishPlaneRejectsOtherMethods(t *testing.T) {
	// §6: the publish plane is GET/HEAD only. Go's method-scoped patterns give
	// this for free, but "for free" is exactly the kind of guarantee that
	// disappears in a refactor, so it is asserted.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(m, func(t *testing.T) {
			rec := httptest.NewRecorder()
			publishMux(testLogger()).ServeHTTP(rec, httptest.NewRequest(m, "/healthz", nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("%s /healthz returned 200; the publish plane must not accept it", m)
			}
		})
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	publishMux(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/nope.xml", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (feeds arrive in A9)", rec.Code)
	}
}
