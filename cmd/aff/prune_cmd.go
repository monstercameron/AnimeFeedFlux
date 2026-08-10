// aff prune — the manual trigger for the nightly retention job (PLAN.md
// §15, internal/ops/prune.go). `--dry-run` (the default) is the ONLY way
// this command runs unless the operator explicitly types
// `--dry-run=false`; there is no other flag that deletes anything, which is
// the actual safety property this file exists to guarantee — see
// TestPruneNeverDeletesWithoutExplicitDryRunFalse in prune_cmd_test.go.
//
// This races the live scheduler for real: internal/ops/schedule.go runs
// this exact same Prune once a night, in-process, against the live writer.
// An operator running `aff prune --dry-run=false` at 2am while that
// scheduler is mid-run is not a hypothetical — WAL's busy_timeout is what
// makes the two writers coexist rather than one erroring outright, and nothing
// here tries to detect or avoid the overlap beyond that.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func (a *app) cmdPrune(args []string) int {
	fs := a.newFlagSet("aff prune")
	dryRun := fs.Bool("dry-run", true, "report what would be deleted without deleting anything "+
		"(default; pass --dry-run=false to actually delete)")
	yes := fs.Bool("yes", false, "skip the confirmation prompt when actually deleting")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (default: $AFF_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff prune: takes no positional arguments")
		return exitUsage
	}

	dbPath, err := a.requireDBPath("aff prune")
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	ctx := context.Background()
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	opts := ops.PruneOptions{Now: now, RunRetention: ops.DefaultRunRetention, EmbeddingWindow: ops.DefaultEmbeddingWindow}

	// The dry-run count is computed unconditionally, even on the delete
	// path — it is both the report for --dry-run and the number shown in the
	// delete confirmation prompt, so an operator never confirms a deletion
	// without having seen the counts first.
	roDB, err := ops.OpenReadOnly(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff prune: %v\n", err)
		return exitFail
	}
	preview, err := ops.PruneDryRun(ctx, roDB, opts)
	_ = roDB.Close()
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff prune: %v\n", err)
		return exitFail
	}

	if *dryRun {
		a.printPruneResult("DRY RUN — nothing deleted", preview)
		return exitOK
	}

	fmt.Fprintf(a.Stderr, "This will permanently delete %d expired sample(s), %d over-window embedding(s), "+
		"and %d old run(s). Items are never deleted by this command.\n",
		preview.SamplesDeleted, preview.EmbeddingsDeleted, preview.RunsDeleted)
	ok, err := a.confirm(*yes, "Continue?")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff prune: %v\n", err)
		return exitFail
	}
	if !ok {
		fmt.Fprintln(a.Stderr, "aff prune: aborted, nothing was deleted")
		return exitFail
	}

	rwDB, err := ops.OpenReadWrite(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff prune: %v\n", err)
		return exitFail
	}
	defer func() { _ = rwDB.Close() }()

	result, err := ops.Prune(ctx, rwDB, opts)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff prune: %v\n", err)
		return exitFail
	}
	a.printPruneResult("deleted", result)
	return exitOK
}

func (a *app) printPruneResult(heading string, result ops.PruneResult) {
	if a.JSON {
		_ = a.printJSON(map[string]any{
			"heading":            heading,
			"samples_deleted":    result.SamplesDeleted,
			"embeddings_deleted": result.EmbeddingsDeleted,
			"runs_deleted":       result.RunsDeleted,
			"items_deleted":      0,
		})
		return
	}
	fmt.Fprintln(a.Stdout, heading)
	fmt.Fprintf(a.Stdout, "samples:     %d (expired)\n", result.SamplesDeleted)
	fmt.Fprintf(a.Stdout, "embeddings:  %d (beyond the novelty window)\n", result.EmbeddingsDeleted)
	fmt.Fprintf(a.Stdout, "runs:        %d (old, non-failure)\n", result.RunsDeleted)
	fmt.Fprintf(a.Stdout, "items:       0 (never pruned — items are the archive)\n")
}
