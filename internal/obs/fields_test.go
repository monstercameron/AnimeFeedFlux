package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// countEvents returns how many top-level JSON records in buf have msg == name.
func countEvents(t *testing.T, buf *bytes.Buffer, name string) int {
	t.Helper()
	n := 0
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
		}
		if rec["msg"] == name {
			n++
		}
	}
	return n
}

// A0-L11: a completed run emits exactly one run.finished record carrying
// every required field.
func TestRunFinishedEmitsExactlyOneRecordWithEveryField(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelDebug, Writer: &buf})

	RunFinished(context.Background(), log, RunFinishedFields{
		FeedSlug:  "anime-trivia-daily",
		Trigger:   "schedule",
		Outcome:   OutcomeSuccess,
		Duration:  1500 * time.Millisecond,
		Model:     "gpt-5",
		TokensIn:  120,
		TokensOut: 340,
		CostUSD:   0.0042,
	})

	if got := countEvents(t, &buf, EventRunFinished); got != 1 {
		t.Fatalf("run.finished emitted %d times, want exactly 1", got)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}

	required := []string{
		FieldFeedSlug, FieldTrigger, FieldOutcome, FieldReason,
		FieldDurationMs, FieldModel, FieldTokensIn, FieldTokensOut, FieldCostUSD,
	}
	for _, field := range required {
		if _, ok := rec[field]; !ok {
			t.Errorf("run.finished missing required field %q: %s", field, buf.String())
		}
	}

	// A0-L03: duration_ms must be a JSON number, never a formatted string.
	if _, ok := rec[FieldDurationMs].(float64); !ok {
		t.Errorf("duration_ms is not a number: %T %v", rec[FieldDurationMs], rec[FieldDurationMs])
	}
	if got := rec[FieldDurationMs]; got != float64(1500) {
		t.Errorf("duration_ms = %v, want 1500", got)
	}
}

// A0-L04: outcome is constrained to success|skipped|rejected|failed. An
// invalid value passed through the helper must not reach the log verbatim.
func TestRunFinishedRejectsInvalidOutcome(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelDebug, Writer: &buf})

	RunFinished(context.Background(), log, RunFinishedFields{
		FeedSlug: "x",
		Outcome:  Outcome("something-made-up"),
	})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	switch rec[FieldOutcome] {
	case string(OutcomeSuccess), string(OutcomeSkipped), string(OutcomeRejected), string(OutcomeFailed):
		// fine
	default:
		t.Errorf("outcome = %v, want one of the four constrained values", rec[FieldOutcome])
	}
}

// A0-L05 + A0-L13: reason is a short stable token, and model output must
// never reach a log field verbatim — a model-generated title is exactly the
// kind of unbounded, sentence-shaped input this guards against.
func TestRunFinishedSanitizesReason(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelDebug, Writer: &buf})

	modelOutput := "Breaking: Why This Anime Season Is The Best One Yet — an in-depth take!"
	RunFinished(context.Background(), log, RunFinishedFields{
		FeedSlug: "x",
		Outcome:  OutcomeRejected,
		Reason:   modelOutput,
	})

	if strings.Contains(buf.String(), modelOutput) {
		t.Fatalf("model output reached the log verbatim: %s", buf.String())
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	reason, _ := rec[FieldReason].(string)
	if !reasonTokenPattern.MatchString(reason) {
		t.Errorf("reason %q is not a stable token", reason)
	}
}

func TestSanitizeReasonAcceptsValidTokens(t *testing.T) {
	for _, valid := range []string{"low_novelty", "duplicate", "provider.timeout", "a"} {
		if got := SanitizeReason(valid); got != valid {
			t.Errorf("SanitizeReason(%q) = %q, want unchanged", valid, got)
		}
	}
}

func TestSanitizeReasonRejectsProseAndOversizedInput(t *testing.T) {
	for _, invalid := range []string{
		"This is a whole sentence, not a token.",
		"Has Spaces",
		"UPPERCASE_TOKEN",
		strings.Repeat("a", 100),
		"",
	} {
		if got := SanitizeReason(invalid); got != fallbackReason {
			t.Errorf("SanitizeReason(%q) = %q, want fallback %q", invalid, got, fallbackReason)
		}
	}
}

// A0-L09: http.request emits the canonical wide event with duration as a
// number.
func TestHTTPRequestEmitsCanonicalEvent(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelDebug, Writer: &buf})

	HTTPRequest(context.Background(), log, HTTPRequestFields{
		Route:    "/feeds/anime-trivia-daily.xml",
		Status:   304,
		Cache:    "hit",
		Duration: 12 * time.Millisecond,
	})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if rec["msg"] != EventHTTPRequest {
		t.Errorf("msg = %v, want %s", rec["msg"], EventHTTPRequest)
	}
	if _, ok := rec[FieldDurationMs].(float64); !ok {
		t.Errorf("duration_ms is not a number: %T", rec[FieldDurationMs])
	}
	if rec[FieldStatus] != float64(304) {
		t.Errorf("status = %v, want 304", rec[FieldStatus])
	}
}

// A0-L12: no log record is emitted with a field name outside the canonical
// set. Enumerate every key on both canonical events against the whitelist.
func TestNoFieldOutsideCanonicalSet(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Options{Level: slog.LevelDebug, Writer: &buf})
	ctx := WithRequestID(WithRunID(context.Background(), "run-1"), "req-1")

	RunFinished(ctx, log, RunFinishedFields{
		FeedSlug: "x", Trigger: "manual", Outcome: OutcomeSuccess, Duration: time.Second,
	})
	HTTPRequest(ctx, log, HTTPRequestFields{Route: "/x", Status: 200, Cache: "miss", Duration: time.Millisecond})

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		for key := range rec {
			if !IsCanonicalField(key) {
				t.Errorf("field %q is outside the canonical set: %s", key, buf.String())
			}
		}
	}
}

func TestIsCanonicalFieldRejectsBareStringKeys(t *testing.T) {
	for _, bad := range []string{"slug", "feed", "err", "duration", "id", ""} {
		if IsCanonicalField(bad) {
			t.Errorf("IsCanonicalField(%q) = true, want false", bad)
		}
	}
}

func TestValidOutcome(t *testing.T) {
	for _, ok := range []Outcome{OutcomeSuccess, OutcomeSkipped, OutcomeRejected, OutcomeFailed} {
		if !ValidOutcome(ok) {
			t.Errorf("ValidOutcome(%q) = false, want true", ok)
		}
	}
	if ValidOutcome(Outcome("partial")) {
		t.Error("ValidOutcome(\"partial\") = true, want false")
	}
}

func TestDurationMsAttrIsNumeric(t *testing.T) {
	attr := DurationMsAttr(2500 * time.Millisecond)
	if attr.Key != FieldDurationMs {
		t.Errorf("key = %s, want %s", attr.Key, FieldDurationMs)
	}
	if attr.Value.Kind() != slog.KindInt64 {
		t.Errorf("kind = %v, want Int64", attr.Value.Kind())
	}
	if attr.Value.Int64() != 2500 {
		t.Errorf("value = %d, want 2500", attr.Value.Int64())
	}
}
