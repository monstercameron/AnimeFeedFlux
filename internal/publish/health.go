// Per-feed health reporting for the publish plane (PLAN.md §15, §15.0a;
// TODOS.md C4-08, C4-15). This file is deliberately NOT wired into
// server.go's handleHealthz here — server.go is owned by another change in
// flight — but it provides everything that wiring needs: the JSON body
// shape, the staleness computation (reusing internal/ops.Check so the
// webhook and /healthz can never disagree about what "stale" means), and
// the status-code decision the ticket asked for, argued below.
//
// # Status-code decision
//
// A stale feed does NOT flip /healthz to a non-2xx status. It stays 200
// (barring the process being unable to answer at all) and carries the
// detail in the body instead. Reasoning:
//
//   - PLAN.md's route table already casts /healthz as a LIVENESS check
//     ("GET /healthz liveness + per-feed staleness", §14.1/§15), not a
//     readiness check. Liveness answers "is this process alive and able to
//     respond correctly", and it can: the publish plane has no write path
//     and no dependency on the LLM provider (§2's two-plane split) — a
//     generator that stopped firing says nothing about whether cached feeds
//     are still being served correctly right now.
//   - A hard failure (503/500) on staleness would take the container out of
//     a compose/systemd healthcheck and restart-loop it. That "fix" changes
//     nothing about the actual problem — restarting the publish process
//     does not make a stuck LLM provider respond, does not un-pause a
//     schedule, and does not backfill a missed run — while it DOES take
//     down the one thing that was genuinely fine: serving the last-good
//     cached feed to every reader who has no idea tonight's item is late.
//     That trade is strictly worse than doing nothing.
//   - Conflating liveness and readiness here also breaks the *other* stated
//     goal: "an operator needs to see staleness by looking, not only by
//     being told." A 503 tells an uptime monitor "down" (an alert that then
//     duplicates the Slack webhook and adds no new information) while
//     hiding exactly the per-feed detail — which feed, how stale, how many
//     errors — that a human opening the page in a browser wants to read.
//     A 200 with a rich, honest body serves both the human and the machine
//     checker (which can grep the body for "stale":true without needing a
//     status-code semantic overloaded to mean two different things).
//
// So: 200 whenever the handler can build a response at all; "degraded" in
// the JSON `status` field, plus a `stale` flag and full detail per feed, is
// how staleness surfaces. HealthReport.HTTPStatus documents this as code,
// not just as a comment, so the eventual server.go wiring has one call to
// make and nothing to decide.
package publish

import (
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

// DefaultHealthGrace mirrors ops schedule.go's own SchedulerConfig default
// (2.0): /healthz and the nightly Slack webhook must apply the identical
// grace multiplier to the identical Check function, or "stale" would mean
// two different things depending on which surface you looked at.
const DefaultHealthGrace = 2.0

// FeedHealthInput is what a caller (the eventual server.go Deps wiring)
// supplies per feed. It embeds ops.FeedStatus — the same shape the nightly
// watchdog already reads — and adds ErrorCount, which ops.FeedStatus has no
// way to carry: recent run-failure counts live in internal/store's run
// history, a query this package intentionally has no access to (§2: the
// publish plane has no writer and no store dependency). Whoever wires this
// in is expected to pass a count of failed runs in some caller-defined
// recent window (e.g. since the last success, or a fixed lookback) — this
// file does not prescribe the window, only the shape.
type FeedHealthInput struct {
	ops.FeedStatus
	ErrorCount int
}

// FeedHealth is the per-feed slice of the /healthz body.
type FeedHealth struct {
	FeedSlug string `json:"feed_slug"`
	Enabled  bool   `json:"enabled"`

	// LastSuccessAt is RFC 3339 UTC, nil when NeverSucceeded is true.
	LastSuccessAt *string `json:"last_success_at,omitempty"`
	// AgeSeconds is time.Now().Sub(LastSuccessAt) at report-build time, nil
	// when NeverSucceeded is true — there is no age to report for a feed
	// that has never once succeeded, only absence.
	AgeSeconds *float64 `json:"age_seconds,omitempty"`
	// NeverSucceeded is true when the feed has no successful run on record
	// at all — the most extreme case of staleness, called out explicitly
	// (per ops.Check's own doc comment) rather than left to be inferred from
	// a missing AgeSeconds.
	NeverSucceeded bool `json:"never_succeeded,omitempty"`

	// ThresholdSeconds is Interval * grace, the same number ops.Check
	// compares Age against. Omitted for a disabled feed or one with no
	// meaningful Interval, matching ops.Check's own skip condition.
	ThresholdSeconds *float64 `json:"threshold_seconds,omitempty"`

	// ErrorCount is passed through from FeedHealthInput unchanged.
	ErrorCount int `json:"error_count"`

	// Stale mirrors exactly what ops.Check would flag for this feed at the
	// same (now, grace) — computed by calling ops.Check, not reimplemented,
	// so this can never silently drift from the watchdog's own definition.
	Stale bool `json:"stale"`
}

// HealthReport is the /healthz body this package proposes. Status is
// informational ("ok" or "degraded") and — see the package doc comment —
// deliberately does NOT drive the HTTP status code returned alongside it.
type HealthReport struct {
	Status         string       `json:"status"`
	StaleFeedCount int          `json:"stale_feed_count"`
	Feeds          []FeedHealth `json:"feeds,omitempty"`
}

// HTTPStatus is always 200: see the package doc comment for the full
// argument. It exists as a function, not a comment, so the code that wires
// HealthReport into an http.ResponseWriter has exactly one call to make and
// nothing left to decide about status codes at that call site.
func (r HealthReport) HTTPStatus() int { return 200 }

// BuildHealthReport computes per-feed staleness (via ops.Check, the same
// function the nightly Slack webhook uses) and assembles the /healthz body.
// grace <= 0 uses DefaultHealthGrace.
func BuildHealthReport(inputs []FeedHealthInput, now time.Time, grace float64) HealthReport {
	if grace <= 0 {
		grace = DefaultHealthGrace
	}

	statuses := make([]ops.FeedStatus, len(inputs))
	for i, in := range inputs {
		statuses[i] = in.FeedStatus
	}

	stale := make(map[string]struct{}, len(inputs))
	for _, s := range ops.Check(statuses, now, grace) {
		stale[s.FeedSlug] = struct{}{}
	}

	report := HealthReport{Status: "ok"}
	for _, in := range inputs {
		fh := FeedHealth{
			FeedSlug:   in.FeedSlug,
			Enabled:    in.Enabled,
			ErrorCount: in.ErrorCount,
		}

		if in.LastSuccessAt.IsZero() {
			fh.NeverSucceeded = true
		} else {
			ts := in.LastSuccessAt.UTC().Format(time.RFC3339)
			fh.LastSuccessAt = &ts
			age := now.Sub(in.LastSuccessAt).Seconds()
			fh.AgeSeconds = &age
		}

		if in.Interval > 0 {
			th := in.Interval.Seconds() * grace
			fh.ThresholdSeconds = &th
		}

		if _, ok := stale[in.FeedSlug]; ok {
			fh.Stale = true
			report.StaleFeedCount++
		}

		report.Feeds = append(report.Feeds, fh)
	}

	if report.StaleFeedCount > 0 {
		report.Status = "degraded"
	}
	return report
}
