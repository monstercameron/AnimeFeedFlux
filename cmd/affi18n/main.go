// Command affi18n is the gate for the i18n convention described in
// PLAN.md §12.6 and tracked as TODOS.md D6-20..24: every string an admin
// can read in web/ goes through the i18n catalogue rather than living as a
// hardcoded literal. See internal/i18nlint's package doc for the exact
// rule affi18n enforces and what it deliberately does not flag.
//
// Usage:
//
//	affi18n lint [dir]
//	affi18n check --catalogue=<path> [dir]
//	affi18n ratchet --baseline=<path> [dir]
//	affi18n pseudo [string...]
//
// dir defaults to "web". Every subcommand exits non-zero on failure and
// prints findings as "path:line:col: rule: text" so they're clickable in a
// terminal or a CI log.
package main

import (
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "lint":
		return runLint(rest, stdout, stderr)
	case "check":
		return runCheck(rest, stdout, stderr)
	case "ratchet":
		return runRatchet(rest, stdout, stderr)
	case "pseudo":
		return runPseudo(rest, stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "affi18n: unknown command %q\n", cmd)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w *os.File) {
	fmt.Fprint(w, `usage:
  affi18n lint [dir]
  affi18n check --catalogue=<path> [dir]
  affi18n ratchet --baseline=<path> [dir]
  affi18n pseudo [string...]

dir defaults to "web".
`)
}
