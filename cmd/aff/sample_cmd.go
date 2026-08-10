package main

import (
	"context"
	"fmt"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// cmdSample implements `aff sample <slug> [--size N]`: the unary dry run
// (SampleService.Sample), rendering candidates, the novelty verdict, link
// verdicts, and the cost estimate — labelled as an estimate, per PLAN.md
// §8.1's "SchemaFlux's Generating[T] reports no usage/cost today ... labeled
// an estimate wherever shown".
func (a *app) cmdSample(args []string) int {
	fs := a.newFlagSet("aff sample")
	size := fs.Int("size", 1, "number of candidates to generate (1-5)")
	temperature := fs.Float64("temperature", 0, "override the recipe's temperature (0 = use the recipe's)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff sample: want exactly one feed slug argument")
		return exitUsage
	}
	slug := fs.Arg(0)

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff sample: %v\n", err)
		return exitFail
	}

	feedResp, err := cb.Feed.Get(ctx, &affv1.FeedServiceGetRequest{Slug: slug})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff sample: resolving feed %q: %v\n", slug, err)
		return exitFail
	}

	resp, err := cb.Sample.Sample(ctx, &affv1.SampleServiceSampleRequest{
		FeedId:              feedResp.GetFeed().GetId(),
		SampleSize:          int32(*size),
		TemperatureOverride: *temperature,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff sample: %v\n", err)
		return exitFail
	}

	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}

	fmt.Fprintf(a.Stdout, "sample %s (%d candidate(s)), remaining budget today: $%.4f (estimate)\n\n",
		resp.GetSampleId(), len(resp.GetCandidates()), resp.GetRemainingDailyBudgetUsd())

	for i, c := range resp.GetCandidates() {
		fmt.Fprintf(a.Stdout, "--- candidate %d (%s) ---\n", i+1, c.GetCandidateId())
		fmt.Fprintf(a.Stdout, "title:   %s\n", c.GetTitle())
		fmt.Fprintf(a.Stdout, "summary: %s\n", c.GetSummaryText())
		if c.GetLink() != "" {
			fmt.Fprintf(a.Stdout, "link:    %s\n", c.GetLink())
		}
		nv := c.GetNovelty()
		if nv != nil {
			if nv.GetIsNovel() {
				fmt.Fprintln(a.Stdout, "novelty: NOVEL")
			} else {
				fmt.Fprintf(a.Stdout, "novelty: REJECTED (similarity %.3f to item %d %q)\n",
					nv.GetSimilarity(), nv.GetNearestItemId(), nv.GetNearestItemTitle())
			}
		}
		for _, lv := range c.GetLinkVerdicts() {
			status := "OK"
			if !lv.GetOk() {
				status = "FAILED byte-equality check"
			}
			fmt.Fprintf(a.Stdout, "link check: %s %s\n", status, lv.GetUrl())
		}
		fmt.Fprintf(a.Stdout, "cost (estimate): $%.4f (%d in / %d out tokens)\n\n",
			c.GetEstimatedCostUsd(), c.GetTokensIn(), c.GetTokensOut())
	}
	return exitOK
}
