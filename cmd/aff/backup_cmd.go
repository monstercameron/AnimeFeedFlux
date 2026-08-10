// aff backup / aff restore / aff verify — the local-only, direct-filesystem
// half of PLAN.md §15's backup story (the other half, `aff system backup`,
// is the RPC-based on-demand download in system_cmd.go and stays separate).
// Every command here operates on the SQLite file directly, the same way
// admin_cmd.go's `aff admin init`/`reset` do, and every one of them is safe
// to run against a database the live service is simultaneously writing to
// (internal/ops/cli.go's package doc comment explains how).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

// cmdBackup implements `aff backup [--out DIR] [--keep N]`. It races nothing
// on the live database: ops.Backup opens its source connection read-only and
// asks SQLite itself for a consistent snapshot via VACUUM INTO, so it never
// blocks — and is never blocked by — the service's own writer.
func (a *app) cmdBackup(args []string) int {
	fs := a.newFlagSet("aff backup")
	out := fs.String("out", "", "directory to write the snapshot into (default: $AFF_BACKUP_DIR)")
	keep := fs.Int("keep", ops.DefaultBackupKeep, "how many snapshots to retain in --out; older ones are pruned")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (default: $AFF_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff backup: takes no positional arguments")
		return exitUsage
	}

	dbPath, err := a.requireDBPath("aff backup")
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	outDir := *out
	if outDir == "" {
		outDir = a.backupDir()
	}
	if outDir == "" {
		fmt.Fprintln(a.Stderr, "aff backup: --out is required (or set AFF_BACKUP_DIR)")
		return exitUsage
	}

	ctx := context.Background()
	info, err := ops.CLIBackup(ctx, dbPath, outDir, *keep)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff backup: %v\n", err)
		return exitFail
	}

	if a.JSON {
		_ = a.printJSON(map[string]any{"path": info.Path, "bytes": info.Bytes})
	} else {
		fmt.Fprintf(a.Stdout, "wrote:      %s\n", info.Path)
		fmt.Fprintf(a.Stdout, "size:       %d bytes (%s)\n", info.Bytes, humanBytes(info.Bytes))
		fmt.Fprintf(a.Stdout, "integrity:  ok\n")
		fmt.Fprintf(a.Stdout, "retained:   %d most recent snapshot(s) in %s\n", *keep, outDir)
	}
	return exitOK
}

// backupDir reads AFF_BACKUP_DIR directly, the same env var
// internal/config.Load exposes to the server (PLAN.md §16), so an operator
// who already has the container's environment sourced gets the same default
// this CLI would.
func (a *app) backupDir() string {
	return os.Getenv("AFF_BACKUP_DIR")
}

// cmdRestore implements `aff restore --from PATH --to PATH`. Destructive —
// it overwrites --to — so it confirms unless --yes, and the confirmation
// prompt names the risk plainly: if the live service currently has --to
// open, this restore is happening underneath it and the service should be
// stopped first. ops.Restore itself verifies the backup before touching
// anything and re-verifies the destination afterward.
func (a *app) cmdRestore(args []string) int {
	fs := a.newFlagSet("aff restore")
	from := fs.String("from", "", "backup file to restore (required)")
	to := fs.String("to", "", "destination database path to overwrite (required)")
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for scripting)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff restore: takes no positional arguments, use --from and --to")
		return exitUsage
	}
	if *from == "" || *to == "" {
		fmt.Fprintln(a.Stderr, "aff restore: --from and --to are both required")
		return exitUsage
	}

	ctx := context.Background()

	if err := ops.Verify(ctx, *from); err != nil {
		fmt.Fprintf(a.Stderr, "aff restore: refusing — %s failed its integrity check: %v\n", *from, err)
		return exitFail
	}

	fmt.Fprintf(a.Stderr, "This will OVERWRITE %s with the contents of %s.\n", *to, *from)
	fmt.Fprintln(a.Stderr, "If a running service has this database open, stop it first — restoring "+
		"underneath a live writer leaves that process holding a stale connection to a file that just "+
		"changed out from under it.")
	ok, err := a.confirm(*yes, "Continue?")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff restore: %v\n", err)
		return exitFail
	}
	if !ok {
		fmt.Fprintln(a.Stderr, "aff restore: aborted, nothing was changed")
		return exitFail
	}

	if err := ops.Restore(ctx, *from, *to); err != nil {
		fmt.Fprintf(a.Stderr, "aff restore: %v\n", err)
		return exitFail
	}

	if a.JSON {
		_ = a.printJSON(map[string]any{"restored_to": *to, "from": *from, "verified": true})
	} else {
		fmt.Fprintf(a.Stdout, "restored:  %s\n", *to)
		fmt.Fprintf(a.Stdout, "from:      %s\n", *from)
		fmt.Fprintf(a.Stdout, "verified:  ok\n")
	}
	return exitOK
}

// cmdVerify implements `aff verify PATH`: the integrity check alone, with no
// side effects — safe to run against any file, live database included,
// since it opens read-only.
func (a *app) cmdVerify(args []string) int {
	fs := a.newFlagSet("aff verify")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Stderr, "aff verify: want exactly one path argument")
		return exitUsage
	}
	path := fs.Arg(0)

	ctx := context.Background()
	err := ops.Verify(ctx, path)
	if a.JSON {
		_ = a.printJSON(map[string]any{"path": path, "ok": err == nil, "error": errString(err)})
	} else if err != nil {
		fmt.Fprintf(a.Stdout, "%s: FAILED — %v\n", path, err)
	} else {
		fmt.Fprintf(a.Stdout, "%s: ok\n", path)
	}
	if err != nil {
		return exitFail
	}
	return exitOK
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
