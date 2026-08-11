package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestLogGenerateFailure_TransientIsWarnNotError is TODOS A0-L07's check: a
// retried transient provider error logs WARN, not ERROR. SchemaFlux already
// retried internally before Generate's error ever reaches this function (see
// logGenerateFailure's doc comment in llm.go), so what this asserts is the
// level distinction at the one place internal/llm can still make it: Kind is
// the only signal available, and Transient must map to WARN.
func TestLogGenerateFailure_TransientIsWarnNotError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logGenerateFailure(context.Background(), logger, "gpt-test", &Error{Kind: Transient})

	rec := decodeLogLine(t, buf.Bytes())
	if rec["level"] != "WARN" {
		t.Errorf("level = %v, want WARN for a Transient error", rec["level"])
	}
}

// TestLogGenerateFailure_FatalIsError is the other half of A0-L07: "only the
// final give-up should be ERROR". Fatal (and Invalid, tested below) never
// self-heal on the next attempt, so they must not be logged at the level
// reserved for "self-healed, recurrence matters" -- they need a human.
func TestLogGenerateFailure_FatalIsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logGenerateFailure(context.Background(), logger, "gpt-test", &Error{Kind: Fatal, Scope: ScopeAccount})

	rec := decodeLogLine(t, buf.Bytes())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR for a Fatal error", rec["level"])
	}
}

// TestLogGenerateFailure_InvalidIsError: a schema violation / malformed
// output is not expected to resolve itself on the next scheduled attempt
// either (the model will likely repeat the same mistake against the same
// prompt), so it gets ERROR like Fatal, not WARN like Transient.
func TestLogGenerateFailure_InvalidIsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logGenerateFailure(context.Background(), logger, "gpt-test", &Error{Kind: Invalid})

	rec := decodeLogLine(t, buf.Bytes())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR for an Invalid error", rec["level"])
	}
}

// TestLogGenerateFailure_NeverLogsErrorMessage is RULE-3 (never log model
// output) applied to this specific call site: a schema-violation or
// malformed-output error's message can and does echo the model's raw text
// back (errors.go's Classify wraps SchemaFlux's own error verbatim), so only
// the closed-set Kind may ever reach the log line -- never err.Err's message.
func TestLogGenerateFailure_NeverLogsErrorMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	secret := "the model said: HERE IS SOME LEAKED RAW MODEL OUTPUT 12345"
	logGenerateFailure(context.Background(), logger, "gpt-test", &Error{
		Kind: Invalid,
		Err:  &stubErr{msg: secret},
	})

	if strings.Contains(buf.String(), secret) {
		t.Fatalf("log line leaked the wrapped error's message (RULE-3 violation): %s", buf.String())
	}
}

// TestLogGenerateFailure_NilErrorAndNilLoggerAreNoOps guards the two zero
// values a caller could hand this function -- neither should panic.
func TestLogGenerateFailure_NilErrorAndNilLoggerAreNoOps(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logGenerateFailure(context.Background(), logger, "gpt-test", nil)
	if buf.Len() != 0 {
		t.Errorf("logGenerateFailure(nil error) wrote a log line: %s", buf.String())
	}

	logGenerateFailure(context.Background(), nil, "gpt-test", &Error{Kind: Fatal})
}

type stubErr struct{ msg string }

func (e *stubErr) Error() string { return e.msg }

func decodeLogLine(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, b)
	}
	return rec
}
