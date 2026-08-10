package main

import (
	"context"
	"fmt"
	"os"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func (a *app) cmdRecipe(args []string) int {
	choices := []string{"export", "import"}
	if len(args) == 0 {
		return a.missingSubcommand("recipe", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "export":
		return a.cmdRecipeExport(rest)
	case "import":
		return a.cmdRecipeImport(rest)
	default:
		return a.unknownSubcommand("recipe", sub, choices...)
	}
}

// cmdRecipeExport implements `aff recipe export`: FeedService.ExportTOML,
// for versioning and disaster recovery (PLAN.md §7 — "SQLite is the source
// of truth for recipes", TOML is the offline copy).
func (a *app) cmdRecipeExport(args []string) int {
	fs := a.newFlagSet("aff recipe export")
	out := fs.String("out", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff recipe export: want exactly one feed id argument")
		return exitUsage
	}
	id, err := parseInt64Arg(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe export: %v\n", err)
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe export: %v\n", err)
		return exitFail
	}
	resp, err := cb.Feed.ExportTOML(ctx, &affv1.FeedServiceExportTOMLRequest{FeedId: id})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe export: %v\n", err)
		return exitFail
	}

	if a.JSON {
		_ = a.printJSON(map[string]any{"feed_id": id, "toml": resp.GetToml()})
		return exitOK
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(resp.GetToml()), 0o644); err != nil {
			fmt.Fprintf(a.Stderr, "aff recipe export: writing %s: %v\n", *out, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprint(a.Stdout, resp.GetToml())
	return exitOK
}

// cmdRecipeImport implements `aff recipe import`: FeedService.ImportTOML.
// With --feed-id it replaces that feed's spec (subject to
// --expected-version); without it, imports as a new feed.
func (a *app) cmdRecipeImport(args []string) int {
	fs := a.newFlagSet("aff recipe import")
	feedID := fs.Int64("feed-id", 0, "replace this feed's spec instead of creating a new feed")
	expectedVersion := fs.Int64("expected-version", 0, "required when --feed-id is set")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff recipe import: want exactly one TOML file path argument")
		return exitUsage
	}
	if *feedID != 0 && *expectedVersion == 0 {
		fmt.Fprintln(a.Stderr, "aff recipe import: --expected-version is required with --feed-id")
		return exitUsage
	}

	body, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe import: reading %s: %v\n", fs.Arg(0), err)
		return exitFail
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe import: %v\n", err)
		return exitFail
	}
	resp, err := cb.Feed.ImportTOML(ctx, &affv1.FeedServiceImportTOMLRequest{
		Toml: string(body), FeedId: *feedID, ExpectedVersion: *expectedVersion,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recipe import: %v\n", err)
		return exitFail
	}
	return a.printFeed(resp.GetFeed())
}
