package main

import (
	"context"
	"fmt"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// cmdPromote implements `aff promote <sample-id>`. A sample can hold up to
// five candidates (PLAN.md §12.3: "sample size 1-5"), so
// ItemService.PromoteSample needs both ids; --candidate defaults to the
// first candidate id for the common case of a single-candidate sample, and
// must be given explicitly otherwise.
func (a *app) cmdPromote(args []string) int {
	fs := a.newFlagSet("aff promote")
	candidate := fs.String("candidate", "", "candidate id within the sample (required unless the sample has exactly one)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff promote: want exactly one sample-id argument")
		return exitUsage
	}
	sampleID := fs.Arg(0)

	// ListSamples (SampleService) returns only summaries — feed id, created
	// at, candidate COUNT — not the candidate ids themselves (see
	// SampleServiceListSamplesResponse.SampleSummary in
	// proto/aff/v1/sample.proto), so there is nothing to look up here: the
	// candidate id can only come from the `aff sample` output that produced
	// it, and this must be given explicitly rather than guessed.
	if *candidate == "" {
		fmt.Fprintln(a.Stderr, "aff promote: --candidate is required (re-run the original `aff sample` "+
			"to see candidate ids)")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff promote: %v\n", err)
		return exitFail
	}

	resp, err := cb.Item.PromoteSample(ctx, &affv1.ItemServicePromoteSampleRequest{
		SampleId: sampleID, CandidateId: *candidate,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff promote: %v\n", err)
		return exitFail
	}
	return a.printItem(resp.GetItem())
}
