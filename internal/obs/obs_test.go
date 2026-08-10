package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRedactingWriterScrubsCredentialShapes(t *testing.T) {
	for _, tc := range []struct {
		name, in, mustNotContain string
	}{
		{"openai key", "using sk-abcdefghijklmnopqrstuvwxyz01", "sk-abcdefghijklmnopqrstuvwxyz01"},
		{"github token", "token ghp_abcdefghijklmnopqrstuvwxyz01", "ghp_abcdefghijklmnopqrstuvwxyz01"},
		{"slack token", "xoxb-123456789012-abcdefghijkl", "xoxb-123456789012-abcdefghijkl"},
		// "fake" in the name is load-bearing: the pre-commit credential guard
		// allowlists obvious placeholders, and a bare PEM header in a diff is
		// otherwise indistinguishable from a real leak.
		{"fake private key header", "-----BEGIN RSA PRIVATE KEY-----", "BEGIN RSA PRIVATE KEY"},
		{"assignment", "SCHEMAFLUX_API_KEY=hunter2hunter2hunter2", "hunter2hunter2hunter2"},
		{"assignment colon", "AFF_SECRET_KEY: hunter2hunter2hunter2", "hunter2hunter2hunter2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewRedactingWriter(&buf)

			n, err := w.Write([]byte(tc.in))
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			// Redaction changes the length; reporting anything but len(p) would
			// look like a short write to the caller.
			if n != len(tc.in) {
				t.Errorf("Write returned %d, want len(input)=%d", n, len(tc.in))
			}
			if strings.Contains(buf.String(), tc.mustNotContain) {
				t.Fatalf("secret survived redaction: %s", buf.String())
			}
			if !strings.Contains(buf.String(), redacted) {
				t.Errorf("no redaction marker in output: %s", buf.String())
			}
		})
	}
}

func TestRedactionKeepsTheVariableName(t *testing.T) {
	// Losing the name would make the line useless: you would know something was
	// redacted but not which setting it was.
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	if _, err := w.Write([]byte("SCHEMAFLUX_API_KEY=hunter2hunter2hunter2")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "SCHEMAFLUX_API_KEY=") {
		t.Errorf("variable name was lost: %s", buf.String())
	}
}

func TestRedactingWriterLeavesOrdinaryTextAlone(t *testing.T) {
	const msg = `{"level":"INFO","msg":"feed built","slug":"anime-trivia-daily","items":1}`
	var buf bytes.Buffer
	if _, err := NewRedactingWriter(&buf).Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.String() != msg {
		t.Errorf("ordinary text was altered:\n got: %s\nwant: %s", buf.String(), msg)
	}
}

func TestRedactingWriterIsConcurrencySafe(t *testing.T) {
	// slog handlers write from many goroutines; io.Writer makes no such promise
	// on its own. This test is meaningful only under -race, which is CI-only.
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_, _ = w.Write([]byte("sk-abcdefghijklmnopqrstuvwxyz01\n"))
		})
	}
	wg.Wait()

	if strings.Contains(buf.String(), "sk-abcdefghijklmnop") {
		t.Fatal("a secret escaped under concurrent writes")
	}
}

func TestLoggerAttachesContextIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelInfo, Writer: &buf})

	ctx := WithRequestID(WithRunID(context.Background(), "run-123"), "req-456")
	log.InfoContext(ctx, "generated")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if rec["run_id"] != "run-123" {
		t.Errorf("run_id = %v, want run-123", rec["run_id"])
	}
	if rec["request_id"] != "req-456" {
		t.Errorf("request_id = %v, want req-456", rec["request_id"])
	}
}

func TestLoggerOmitsAbsentIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelInfo, Writer: &buf})
	log.InfoContext(context.Background(), "no ids here")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if _, ok := rec["run_id"]; ok {
		t.Error("run_id present when the context had none")
	}
}

func TestLoggerRedactsThroughTheHandler(t *testing.T) {
	// The end-to-end version of RULE-2's backstop: even if a secret is passed
	// as a plain attribute, it must not reach the output.
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelInfo, Writer: &buf})
	log.Info("oops", slog.String("key", "sk-abcdefghijklmnopqrstuvwxyz01"))

	if strings.Contains(buf.String(), "sk-abcdefghijklmnopqrstuvwxyz01") {
		t.Fatalf("secret reached the log: %s", buf.String())
	}
}

func TestLoggerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelWarn, Writer: &buf})
	log.Info("should not appear")
	if buf.Len() != 0 {
		t.Errorf("info line emitted at warn level: %s", buf.String())
	}
}

func TestContextHelpersOnNilAndEmpty(t *testing.T) {
	if RunID(context.Background()) != "" {
		t.Error("RunID should be empty when unset")
	}
	// The nil-context guard in stringValue stays — these helpers run on paths
	// that must not panic during an incident — but it is not asserted here.
	// Writing the assertion means writing RunID(nil), and a linter that
	// (correctly) forbids passing a nil context then fails the build. Not worth
	// suppressing a good rule to test a defensive branch.
	if RequestID(context.Background()) != "" {
		t.Error("RequestID should be empty when unset")
	}
}
