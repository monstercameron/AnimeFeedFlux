package main

import (
	"context"
	"fmt"
	"os"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func (a *app) cmdSystem(args []string) int {
	choices := []string{"stats", "kill-switch", "backup", "version"}
	if len(args) == 0 {
		return a.missingSubcommand("system", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "stats":
		return a.cmdSystemStats(rest)
	case "kill-switch":
		return a.cmdSystemKillSwitch(rest)
	case "backup":
		return a.cmdSystemBackup(rest)
	case "version":
		return a.cmdSystemVersion(rest)
	default:
		return a.unknownSubcommand("system", sub, choices...)
	}
}

func (a *app) cmdSystemStats(args []string) int {
	fs := a.newFlagSet("aff system stats")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system stats: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system stats: %v\n", err)
		return exitFail
	}
	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "feeds:              %d (%d enabled)\n", resp.GetFeedCount(), resp.GetEnabledFeedCount())
	fmt.Fprintf(a.Stdout, "items:               %d\n", resp.GetItemCount())
	fmt.Fprintf(a.Stdout, "db size:             %d bytes\n", resp.GetDbSizeBytes())
	fmt.Fprintf(a.Stdout, "spend today:         $%.4f (estimate)\n", resp.GetTodaySpendUsd())
	fmt.Fprintf(a.Stdout, "remaining today:     $%.4f (estimate)\n", resp.GetTodayRemainingBudgetUsd())
	fmt.Fprintf(a.Stdout, "generation enabled:  %t\n", resp.GetGenerationEnabled())
	return exitOK
}

// cmdSystemKillSwitch implements `aff system kill-switch [on|off]`. With no
// argument it reports the current state (via Stats, which already surfaces
// it); with an argument it flips SystemService.SetGenerationEnabled — the
// global kill switch (PLAN.md §13): existing feeds keep serving, nothing new
// generates.
func (a *app) cmdSystemKillSwitch(args []string) int {
	fs := a.newFlagSet("aff system kill-switch")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(a.Stderr, "aff system kill-switch: want at most one argument (on or off)")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
		return exitFail
	}

	if fs.NArg() == 0 {
		resp, err := cb.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
		if err != nil {
			fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
			return exitFail
		}
		if a.JSON {
			_ = a.printJSON(map[string]any{"generation_enabled": resp.GetGenerationEnabled()})
		} else if resp.GetGenerationEnabled() {
			fmt.Fprintln(a.Stdout, "generation is ENABLED")
		} else {
			fmt.Fprintln(a.Stdout, "generation is DISABLED (kill switch is on)")
		}
		return exitOK
	}

	var enabled bool
	switch args := fs.Arg(0); args {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		fmt.Fprintf(a.Stderr, "aff system kill-switch: want \"on\" or \"off\", got %q\n", args)
		return exitUsage
	}

	resp, err := cb.System.SetGenerationEnabled(ctx, &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: enabled})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"generation_enabled": resp.GetEnabled()})
	} else if resp.GetEnabled() {
		fmt.Fprintln(a.Stdout, "generation is now ENABLED")
	} else {
		fmt.Fprintln(a.Stdout, "generation is now DISABLED (kill switch is on)")
	}
	return exitOK
}

func (a *app) cmdSystemBackup(args []string) int {
	fs := a.newFlagSet("aff system backup")
	out := fs.String("out", "", "write the backup to this file (required — the DB file is binary, not printable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *out == "" {
		fmt.Fprintln(a.Stderr, "aff system backup: --out is required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Backup(ctx, &affv1.SystemServiceBackupRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: %v\n", err)
		return exitFail
	}
	if err := os.WriteFile(*out, resp.GetDbFile(), 0o600); err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: writing %s: %v\n", *out, err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"filename": resp.GetFilename(), "bytes": len(resp.GetDbFile()), "wrote": *out})
	} else {
		fmt.Fprintf(a.Stdout, "Wrote %d bytes to %s (server filename: %s).\n", len(resp.GetDbFile()), *out, resp.GetFilename())
	}
	return exitOK
}

func (a *app) cmdSystemVersion(args []string) int {
	fs := a.newFlagSet("aff system version")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system version: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Version(ctx, &affv1.SystemServiceVersionRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system version: %v\n", err)
		return exitFail
	}
	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "version: %s\nbuild:   %s\n", resp.GetVersion(), resp.GetBuild())
	return exitOK
}
