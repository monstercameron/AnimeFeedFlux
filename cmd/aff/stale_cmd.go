// aff stale — runs the staleness watchdog (internal/ops/watchdog.go)
// against the live database directly, for the operator who wants the answer
// right now rather than waiting for the nightly job or trusting whatever
// /healthz currently reports. Read-only: opens the database with mode=ro, so
// it is always safe to run alongside the live service.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func (a *app) cmdStale(args []string) int {
	fs := a.newFlagSet("aff stale")
	grace := fs.Float64("grace", ops.DefaultStaleGrace, "how many schedule intervals late before a feed counts as stale")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (default: $AFF_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff stale: takes no positional arguments")
		return exitUsage
	}

	dbPath, err := a.requireDBPath("aff stale")
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	ctx := context.Background()
	db, err := ops.OpenReadOnly(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff stale: %v\n", err)
		return exitFail
	}
	defer func() { _ = db.Close() }()

	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}

	feeds, err := ops.LiveFeedStatuses(ctx, db, now)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff stale: %v\n", err)
		return exitFail
	}
	stale := ops.Check(feeds, now, *grace)

	if a.JSON {
		out := make([]map[string]any, len(stale))
		for i, s := range stale {
			out[i] = map[string]any{
				"feed_slug":         s.FeedSlug,
				"last_success_at":   formatOrEmpty(s.LastSuccessAt),
				"age_seconds":       s.Age.Seconds(),
				"threshold_seconds": s.Threshold.Seconds(),
			}
		}
		_ = a.printJSON(map[string]any{"stale": out, "checked": len(feeds)})
	} else if len(stale) == 0 {
		fmt.Fprintf(a.Stdout, "no stale feeds (%d checked)\n", len(feeds))
	} else {
		for _, s := range stale {
			if s.LastSuccessAt.IsZero() {
				fmt.Fprintf(a.Stdout, "%s: never succeeded (threshold %s)\n", s.FeedSlug, s.Threshold.Round(time.Second))
				continue
			}
			fmt.Fprintf(a.Stdout, "%s: stale, last success %s ago (threshold %s)\n",
				s.FeedSlug, s.Age.Round(time.Second), s.Threshold.Round(time.Second))
		}
	}

	if len(stale) > 0 {
		return exitFail
	}
	return exitOK
}

func formatOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
