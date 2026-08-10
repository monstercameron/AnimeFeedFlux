// aff doctor — the command that matters most at 2am: one screen answering
// "is this system OK", covering PLAN.md §15's full operational surface in
// one pass (database opens, integrity, migrations, WAL size, feed
// staleness, provider key presence, disk space). Every check runs even if
// an earlier one failed — see internal/ops.Doctor's doc comment — because a
// 2am operator needs every fact at once, not a debugging session that stops
// at the first red line.
package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func (a *app) cmdDoctor(args []string) int {
	fs := a.newFlagSet("aff doctor")
	providerKeyEnv := fs.String("provider-key-env", "SCHEMAFLUX_API_KEY", "environment variable checked for presence (never printed)")
	walMaxBytes := fs.Int64("wal-max-bytes", ops.DefaultWALMaxBytes, "flag the -wal sidecar as unhealthy above this size")
	diskMinFreeBytes := fs.Uint64("disk-min-free-bytes", ops.DefaultDiskMinFreeBytes, "flag disk space as unhealthy below this many free bytes")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (default: $AFF_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff doctor: takes no positional arguments")
		return exitUsage
	}

	dbPath, err := a.requireDBPath("aff doctor")
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	cfg := ops.NewDoctorConfig(dbPath)
	if a.Now != nil {
		cfg.Now = a.Now()
	}
	cfg.ProviderKeyEnv = *providerKeyEnv
	cfg.WALMaxBytes = *walMaxBytes

	// Disk space is probed here (platform syscall — diskspace_*.go), not in
	// internal/ops, which stays portable and takes the numbers as input. A
	// probe failure (unsupported platform) disables the check rather than
	// failing the whole report on a fact this binary could not determine.
	if free, _, derr := diskFreeBytes(filepath.Dir(dbPath)); derr != nil {
		cfg.DiskMinFreeBytes = 0
	} else {
		cfg.DiskFreeBytes = free
		cfg.DiskMinFreeBytes = *diskMinFreeBytes
	}

	report := ops.Doctor(context.Background(), cfg)

	if a.JSON {
		checks := make([]map[string]any, len(report.Checks))
		for i, c := range report.Checks {
			checks[i] = map[string]any{"name": c.Name, "ok": c.OK, "detail": c.Detail}
		}
		_ = a.printJSON(map[string]any{"healthy": report.Healthy(), "checks": checks})
	} else {
		for _, c := range report.Checks {
			status := "ok  "
			if !c.OK {
				status = "FAIL"
			}
			line := fmt.Sprintf("[%s] %s", status, c.Name)
			if c.Detail != "" {
				line += ": " + c.Detail
			}
			fmt.Fprintln(a.Stdout, line)
		}
	}

	if !report.Healthy() {
		return exitFail
	}
	return exitOK
}
