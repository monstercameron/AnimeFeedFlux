// The single operator-alerting path for package ops (PLAN.md §15, TODOS.md
// C4-05). Before this file existed the only notifier was welded to the
// staleness watchdog (watchdog.go's Notify), which is how a failing nightly
// backup ended up invisible: Scheduler.runOnce only slog.Error'd it, and
// nobody reads container logs on a box that otherwise looks healthy. Every
// alert in this package — feed staleness and backup failure/absence alike —
// now goes through postToSlack, so there is exactly one place that knows how
// to reach an operator and exactly one place a broken webhook can go wrong.
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// slackPayload is the minimal Slack incoming-webhook shape — a single "text"
// field renders in any channel the webhook is bound to, with no dependency
// on Slack's richer block-kit format.
type slackPayload struct {
	Text string `json:"text"`
}

// postToSlack posts text to a Slack incoming webhook. An empty webhookURL is
// a no-op, not an error, so every caller here can be unconditionally wired
// without an extra "is alerting configured" check at each call site.
//
// A failure to notify (network error, bad URL, non-2xx response) is returned
// as an error for the caller to log — it must never itself crash the work it
// is reporting on. An alerter whose failure takes down the nightly backup or
// prune pass would make the outage it exists to report on worse, not better.
func postToSlack(ctx context.Context, webhookURL, text string) error {
	if webhookURL == "" {
		return nil
	}

	body, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		return fmt.Errorf("ops: encoding alert: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ops: building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ops: posting to slack webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ops: slack webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// BackupAlert carries what an operator needs at 2am about a backup problem.
// "Backup failed" alone does not say whether they've lost a day or a month —
// LastSuccessAt (and Age, computed by the caller against its own clock so
// this stays testable without the wall clock) answers that; Dir says where
// to go look without a second round trip through documentation.
type BackupAlert struct {
	// Reason is a short, stable statement of what's wrong: e.g. "nightly
	// backup run failed", "no successful backup within the schedule's grace
	// window", "off-box backup copy is not configured".
	Reason string
	// Err is the underlying error, if any — nil for a pure absence alert,
	// where nothing errored but nothing has succeeded recently either.
	Err error
	// LastSuccessAt is when the most recent verified backup was taken; zero
	// means none has ever succeeded.
	LastSuccessAt time.Time
	// Age is time elapsed since LastSuccessAt, as measured by the caller's
	// clock. Ignored (and omitted from the message) when LastSuccessAt is
	// zero.
	Age time.Duration
	// Dir is where backups (or off-box copies) live, so the alert answers
	// "where do I go look" without the operator hunting for the path.
	Dir string
}

// NotifyBackupAlert posts a backup-alert report to a Slack incoming webhook,
// sharing postToSlack with Notify (watchdog.go) — the same transport, same
// failure handling, one place to fix if the webhook contract ever changes.
func NotifyBackupAlert(ctx context.Context, webhookURL string, a BackupAlert) error {
	return postToSlack(ctx, webhookURL, formatBackupAlert(a))
}

func formatBackupAlert(a BackupAlert) string {
	msg := fmt.Sprintf(":rotating_light: Backup problem: %s\n", a.Reason)
	if a.Err != nil {
		msg += fmt.Sprintf("• error: %s\n", a.Err)
	}
	if a.LastSuccessAt.IsZero() {
		msg += "• last successful backup: never\n"
	} else {
		msg += fmt.Sprintf("• last successful backup: %s (%s ago)\n",
			a.LastSuccessAt.UTC().Format(time.RFC3339), a.Age.Round(time.Second))
	}
	if a.Dir != "" {
		msg += fmt.Sprintf("• backups live at: %s\n", a.Dir)
	}
	return msg
}

// OpsAlert is the generic shape for a nightly-job failure that isn't a
// backup problem (BackupAlert already covers that one, with its own
// richer fields) but still crosses the line from "log it" to "page it" —
// see schedule.go's runOnce for the two cases that currently produce one:
// the nightly prune failing outright, and the staleness watchdog itself
// being unable to read feed statuses. Deliberately minimal (a subject and
// an error) rather than growing BackupAlert's shape onto unrelated jobs.
type OpsAlert struct {
	// Subject is a short, stable statement of what's wrong, e.g. "nightly
	// prune failed" or "staleness watchdog could not read feed statuses".
	Subject string
	Err     error
}

// NotifyOpsAlert posts subject/err to the same Slack webhook and through the
// same postToSlack transport as NotifyBackupAlert and Notify — one shared
// alerter for every operator-facing failure in this package, per alert.go's
// package doc comment.
func NotifyOpsAlert(ctx context.Context, webhookURL string, a OpsAlert) error {
	return postToSlack(ctx, webhookURL, formatOpsAlert(a))
}

func formatOpsAlert(a OpsAlert) string {
	msg := fmt.Sprintf(":rotating_light: %s\n", a.Subject)
	if a.Err != nil {
		msg += fmt.Sprintf("• error: %s\n", a.Err)
	}
	return msg
}
