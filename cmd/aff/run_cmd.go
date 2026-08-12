package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// cmdRun implements `aff run <slug>`: FeedService.RunNow followed by
// RunService.Watch, streaming progress to the terminal until the run
// reaches a terminal RunStatus (PLAN.md §12.4/§11: "Watch ... Terminates
// after the run reaches a terminal RunStatus"). It is a normal run through
// the same budget/kill-switch gates as a scheduled one (PLAN.md §10) — the
// CLI does not skip them.
func (a *app) cmdRun(args []string) int {
	fs := a.newFlagSet("aff run")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff run: want exactly one feed slug argument")
		return exitUsage
	}
	slug := fs.Arg(0)

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff run: %v\n", err)
		return exitFail
	}

	feedResp, err := cb.Feed.Get(ctx, &affv1.FeedServiceGetRequest{Slug: slug})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff run: resolving feed %q: %v\n", slug, err)
		return exitFail
	}

	runNowResp, err := cb.Feed.RunNow(ctx, &affv1.FeedServiceRunNowRequest{FeedId: feedResp.GetFeed().GetId()})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff run: %v\n", err)
		return exitFail
	}
	runID := runNowResp.GetRunId()

	stream, err := cb.Run.Watch(ctx, &affv1.RunServiceWatchRequest{RunId: runID})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff run: watching run %d: %v\n", runID, err)
		return exitFail
	}

	var last *affv1.Run
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fmt.Fprintf(a.Stderr, "aff run: stream: %v\n", err)
			return exitFail
		}
		if a.JSON {
			if err := a.printProtoJSON(ev); err != nil {
				fmt.Fprintln(a.Stderr, err)
				return exitFail
			}
		} else {
			for _, line := range ev.GetLogLines() {
				fmt.Fprintln(a.Stdout, line)
			}
		}
		last = ev.GetRun()
		if runIsTerminal(last.GetStatus()) {
			break
		}
	}

	if last == nil {
		fmt.Fprintln(a.Stderr, "aff run: stream closed with no run status")
		return exitFail
	}
	if !a.JSON {
		fmt.Fprintf(a.Stdout, "run %d finished: %s (added %d, rejected %d)\n",
			last.GetId(), last.GetStatus(), last.GetItemsAdded(), last.GetItemsRejected())
	}
	if last.GetStatus() == affv1.RunStatus_RUN_STATUS_FAILED {
		return exitFail
	}
	return exitOK
}

func runIsTerminal(s affv1.RunStatus) bool {
	switch s {
	case affv1.RunStatus_RUN_STATUS_SUCCEEDED, affv1.RunStatus_RUN_STATUS_FAILED, affv1.RunStatus_RUN_STATUS_SKIPPED:
		return true
	default:
		return false
	}
}

// cmdRuns implements `aff runs [--feed] [--status]`: RunService.History.
func (a *app) cmdRuns(args []string) int {
	fs := a.newFlagSet("aff runs")
	feedID := fs.Int64("feed", 0, "filter by feed id")
	status := fs.String("status", "", "running, succeeded, failed, or skipped")
	pageSize := fs.Int("page-size", 0, "page size (0 = server default)")
	pageToken := fs.String("page-token", "", "opaque pagination cursor")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	// `aff runs daily` reads as "the runs of the daily feed", and it is not
	// that — the feed filter is --feed with an id. Without this check the
	// argument was silently ignored and the command printed every run for
	// every feed, which looks exactly like a feed that has been running a lot.
	// Silently wrong output is worse than a usage error, and every other
	// command in this CLI already rejects strays; this one did not.
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff runs: takes no positional arguments; filter with --feed <id>")
		return exitUsage
	}
	statusVal, err := parseRunStatus(*status)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff runs: %v\n", err)
		return exitFail
	}
	resp, err := cb.Run.History(ctx, &affv1.RunServiceHistoryRequest{
		PageSize:  int32(*pageSize),
		PageToken: *pageToken,
		FeedId:    *feedID,
		Status:    statusVal,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff runs: %v\n", err)
		return exitFail
	}

	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}

	tw := newTabWriter(a.Stdout)
	fmt.Fprintln(tw, "ID\tFEED\tTRIGGER\tSTATUS\tADDED\tREJECTED\tCOST(est)")
	for _, r := range resp.GetRuns() {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%d\t%d\t$%.4f\n",
			r.GetId(), r.GetFeedId(), r.GetTrigger(), r.GetStatus(),
			r.GetItemsAdded(), r.GetItemsRejected(), r.GetEstCostUsd())
	}
	_ = tw.Flush()
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(a.Stdout, "next-page-token: %s\n", resp.GetNextPageToken())
	}
	return exitOK
}
