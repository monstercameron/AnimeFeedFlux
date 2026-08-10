package ops

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostToSlackNoOpOnEmptyURL(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	if err := postToSlack(t.Context(), "", "hello"); err != nil {
		t.Fatalf("postToSlack with empty URL: %v", err)
	}
	if called {
		t.Error("postToSlack must not call the webhook when webhookURL is empty")
	}
}

func TestPostToSlackFailureDoesNotPanicAndReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("postToSlack panicked: %v", r)
		}
	}()

	if err := postToSlack(t.Context(), "http://127.0.0.1:1/webhook", "x"); err == nil {
		t.Error("postToSlack against an unreachable address: want error, got nil")
	}
	if err := postToSlack(t.Context(), "://not-a-url", "x"); err == nil {
		t.Error("postToSlack with a malformed URL: want error, got nil")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := postToSlack(t.Context(), srv.URL, "x"); err == nil {
		t.Error("postToSlack against a 500 response: want error, got nil")
	}
}

func TestNotifyBackupAlertPostsOperatorActionableFacts(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	last := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	alert := BackupAlert{
		Reason:        "nightly backup run failed",
		Err:           errors.New("disk full"),
		LastSuccessAt: last,
		Age:           9 * 24 * time.Hour,
		Dir:           "/var/lib/animefeedflux/backups",
	}
	if err := NotifyBackupAlert(t.Context(), srv.URL, alert); err != nil {
		t.Fatalf("NotifyBackupAlert: %v", err)
	}

	for _, want := range []string{
		"nightly backup run failed",      // what failed
		"disk full",                      // the underlying error
		"2026-08-01",                     // last known-good timestamp
		"/var/lib/animefeedflux/backups", // where backups live
	} {
		if !strings.Contains(body, want) {
			t.Errorf("alert body missing %q; got: %s", want, body)
		}
	}
}

func TestNotifyBackupAlertNeverSuccessSaysNever(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NotifyBackupAlert(t.Context(), srv.URL, BackupAlert{Reason: "no successful backup ever"}); err != nil {
		t.Fatalf("NotifyBackupAlert: %v", err)
	}
	if !strings.Contains(body, "never") {
		t.Errorf("alert body for a never-succeeded backup should say so; got: %s", body)
	}
}

// TestStalenessAndBackupAlertsShareTransport asserts (not assumes) that
// Notify (watchdog.go) and NotifyBackupAlert (alert.go) go through the same
// underlying transport: hitting the same broken webhook, both must fail with
// postToSlack's exact wrapped error text, which only happens if both are
// routed through it rather than each carrying its own HTTP client code.
func TestStalenessAndBackupAlertsShareTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	staleErr := Notify(t.Context(), srv.URL, []Stale{{FeedSlug: "x"}})
	backupErr := NotifyBackupAlert(t.Context(), srv.URL, BackupAlert{Reason: "x"})

	if staleErr == nil || backupErr == nil {
		t.Fatalf("expected both to fail against a 500 response: stale=%v backup=%v", staleErr, backupErr)
	}
	if staleErr.Error() != backupErr.Error() {
		t.Errorf("Notify and NotifyBackupAlert produced different errors for the same failure, "+
			"suggesting they are not sharing a transport: stale=%q backup=%q", staleErr.Error(), backupErr.Error())
	}
}
