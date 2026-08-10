// Staleness detection (PLAN.md §15). A feed that silently stops generating
// is a worse failure than one that errors loudly: an error shows up in the
// run history and the retry accounting immediately, while a generator that
// just stops firing produces nothing to look at — the feed goes quiet and,
// absent something that watches for the ABSENCE of work, nobody notices for
// a week. Check exists to alert on that absence, not merely on presence of
// errors, which is the entire reason this file is separate from the run
// error path.
package ops

import (
	"context"
	"fmt"
	"time"
)

// FeedStatus is the minimal view Check needs of one feed: whether it should
// be running at all, how often it is scheduled to fire, and when it last
// finished a run successfully.
type FeedStatus struct {
	FeedSlug string
	Enabled  bool
	// Interval is the feed's nominal schedule period (e.g. 24h for a daily
	// feed). A feed with no meaningful interval (Interval <= 0) is skipped —
	// there is nothing to compare staleness against.
	Interval time.Duration
	// LastSuccessAt is the start time of the most recent run that reached
	// 'success' (or 'completed_unconfirmed' — it did produce items). Zero
	// means the feed has never once succeeded.
	LastSuccessAt time.Time
}

// Stale describes one feed Check flagged.
type Stale struct {
	FeedSlug      string
	LastSuccessAt time.Time // zero if the feed has never succeeded
	Age           time.Duration
	Threshold     time.Duration
}

// Check compares each enabled feed's last successful run against its
// schedule interval times grace, and returns the ones that have gone quiet
// longer than that allows. grace exists because "exactly one interval late"
// is normal jitter/retry behavior, not staleness — a grace of 2.0 means a
// daily feed is not flagged until roughly two days without a success.
//
// A feed that has NEVER succeeded (LastSuccessAt is zero) is always flagged
// once enabled and past its first interval: "never ran" is the most extreme
// case of "went quiet", and treating it as fresh because there is no
// timestamp to subtract from would hide exactly the failure this function
// exists to catch.
func Check(feeds []FeedStatus, now time.Time, grace float64) []Stale {
	if grace <= 0 {
		grace = 1
	}

	var out []Stale
	for _, f := range feeds {
		if !f.Enabled || f.Interval <= 0 {
			continue
		}
		threshold := time.Duration(float64(f.Interval) * grace)

		if f.LastSuccessAt.IsZero() {
			out = append(out, Stale{
				FeedSlug:  f.FeedSlug,
				Threshold: threshold,
			})
			continue
		}

		age := now.Sub(f.LastSuccessAt)
		if age > threshold {
			out = append(out, Stale{
				FeedSlug:      f.FeedSlug,
				LastSuccessAt: f.LastSuccessAt,
				Age:           age,
				Threshold:     threshold,
			})
		}
	}
	return out
}

// Notify posts a staleness report to a Slack incoming webhook, via the
// shared postToSlack (alert.go) — the same transport the backup alerts in
// alert.go use. A failure to notify (network error, bad URL, non-2xx
// response) is returned as an error for the caller to log — it must never
// itself crash the nightly job, since losing the backup or the prune pass
// over a broken webhook would be a far worse outcome than a missed Slack
// message.
func Notify(ctx context.Context, webhookURL string, stale []Stale) error {
	if len(stale) == 0 {
		return nil
	}
	return postToSlack(ctx, webhookURL, formatStaleReport(stale))
}

func formatStaleReport(stale []Stale) string {
	msg := fmt.Sprintf(":warning: %d feed(s) have gone stale:\n", len(stale))
	for _, s := range stale {
		if s.LastSuccessAt.IsZero() {
			msg += fmt.Sprintf("• `%s` has never completed a run\n", s.FeedSlug)
			continue
		}
		msg += fmt.Sprintf("• `%s` last succeeded %s ago (threshold %s)\n",
			s.FeedSlug, s.Age.Round(time.Second), s.Threshold.Round(time.Second))
	}
	return msg
}
