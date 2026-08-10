package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// usage is the top-level command tree (PLAN.md §11 CLI paragraph). Kept as
// one literal, not generated from the dispatch table, so `aff help` reads
// like documentation rather than a dump of Go identifiers.
const usage = `aff is a gRPC client for the AnimeFeedFlux control plane.

It talks to the same RPCs the admin UI uses — there is no privileged back
door — except "admin init" and "admin reset", which are local-only and touch
the SQLite file directly (see below).

Usage:
  aff <command> [subcommand] [flags] [args]

Commands:
  login                              authenticate and store a local session
  feed list|get|create|update|enable|disable|delete
  recipe export|import
  sample <slug> [--size N]           dry-run candidates, novelty, cost estimate
  promote <sample-id>
  run <slug>                         trigger a manual run and stream progress
  runs [--feed ID] [--status S]
  item list|get|create|update|delete|restore|correct
  system stats|kill-switch|backup|version
  admin init|reset                   LOCAL-ONLY, requires filesystem DB access

Global flags (accepted by every command):
  --server ADDR        admin gRPC address (default: $AFF_ADMIN_ADDR)
  --session-file PATH  local session file (default: OS config dir)
  --json               machine-readable JSON output where applicable
`

// run is the top-level dispatcher. It never touches the network itself —
// each subcommand dials lazily via a.client() — so unknown-command and
// bad-flag paths return before anything is opened.
func (a *app) run(args []string) int {
	a.applyEnvDefaults()

	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usage)
		return exitUsage
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(a.Stdout, usage)
		return exitOK
	case "login":
		return a.cmdLogin(rest)
	case "feed":
		return a.cmdFeed(rest)
	case "recipe":
		return a.cmdRecipe(rest)
	case "sample":
		return a.cmdSample(rest)
	case "promote":
		return a.cmdPromote(rest)
	case "run":
		return a.cmdRun(rest)
	case "runs":
		return a.cmdRuns(rest)
	case "item":
		return a.cmdItem(rest)
	case "system":
		return a.cmdSystem(rest)
	case "admin":
		return a.cmdAdmin(rest)
	default:
		fmt.Fprintf(a.Stderr, "aff: unknown command %q\n\n", cmd)
		fmt.Fprint(a.Stderr, usage)
		return exitUsage
	}
}

// applyEnvDefaults fills fields a real invocation reads from the
// environment, but only if a caller (or a test) hasn't already set them —
// tests construct an *app directly and must not have those values
// clobbered.
func (a *app) applyEnvDefaults() {
	if a.Server == "" {
		a.Server = os.Getenv("AFF_ADMIN_ADDR")
	}
	if a.DBPath == "" {
		a.DBPath = os.Getenv("AFF_DB_PATH")
	}
	if a.SessionFile == "" {
		a.SessionFile = defaultSessionFile()
	}
}

// defaultSessionFile is a file (not a directory) in the OS user config dir,
// mode 0600 once written (session.go) — never world- or group-readable,
// since it is the bearer credential for the admin account (PLAN.md §4's
// "sessions table contains no usable bearer credential" is about the
// *database*; the raw token handed to this CLI is exactly that kind of
// credential, and the filesystem permission is what protects it here).
func defaultSessionFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "aff", "session.json")
}

// missingSubcommand renders a consistent "aff <group>: missing subcommand"
// usage error for the command-group dispatchers (feed, item, recipe,
// system, admin).
func (a *app) missingSubcommand(group string, choices ...string) int {
	fmt.Fprintf(a.Stderr, "aff %s: missing subcommand, want one of: %v\n", group, choices)
	return exitUsage
}

func (a *app) unknownSubcommand(group, got string, choices ...string) int {
	fmt.Fprintf(a.Stderr, "aff %s: unknown subcommand %q, want one of: %v\n", group, got, choices)
	return exitUsage
}
