package main

import (
	"errors"
	"fmt"
	"strings"
)

// requireDBPath is the shared gate for every command in this file's family
// (backup, restore, verify, prune, stale, doctor): all of them operate
// directly on the SQLite file, like `aff admin init`/`reset` already do
// (admin_cmd.go's openLocalStore), and must say so plainly rather than
// failing three calls deep with a bare "no such file" once dbPath turns out
// to be empty.
func (a *app) requireDBPath(cmdName string) (string, error) {
	if a.DBPath == "" {
		return "", errors.New(cmdName + ": requires --db or AFF_DB_PATH pointing at the SQLite file " +
			"on this machine; this command is LOCAL-ONLY and never talks to a running server over gRPC")
	}
	return a.DBPath, nil
}

// confirm prompts on Stderr (never Stdout — see readPassword's identical
// reasoning: --json output piped elsewhere must never have a prompt mixed
// into it) and returns whether the operator typed "y" or "yes"
// (case-insensitive). yesFlag short-circuits it entirely for scripting
// (every destructive command here accepts --yes), which is also what makes
// these commands usable from a cron job or a CI step without an interactive
// terminal at all.
func (a *app) confirm(yesFlag bool, prompt string) (bool, error) {
	if yesFlag {
		return true, nil
	}
	line, err := a.readLine(prompt + " [y/N] ")
	if err != nil {
		return false, fmt.Errorf("aff: reading confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// humanBytes renders a byte count the way an operator reads it at 2am: one
// unit, not six decimal places of one that doesn't matter.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
