package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/monstercameron/AnimeFeedFlux/internal/i18nlint"
)

// runRatchet implements `affi18n ratchet --baseline=<path> [dir]`
// (TODOS.md D6-21): the flagged-literal count starts at zero and may never
// rise. See internal/i18nlint.Ratchet's doc comment for how this baseline
// differs from scripts/coverage-ratchet.sh's.
func runRatchet(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("ratchet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baselinePath := fs.String("baseline", "", "path to the literal-count baseline file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *baselinePath == "" {
		fmt.Fprintln(stderr, "affi18n ratchet: --baseline=<path> is required")
		return 2
	}

	dir := "web"
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	findings, err := i18nlint.FindLiterals(os.DirFS("."), dir)
	if err != nil {
		fmt.Fprintf(stderr, "affi18n ratchet: %v\n", err)
		return 1
	}
	count := len(findings)

	baseline, baselineErr := readBaseline(*baselinePath)

	if err := i18nlint.Ratchet(count, *baselinePath); err != nil {
		fmt.Fprintf(stderr, "affi18n ratchet: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "affi18n ratchet: count=%d baseline=%s ok\n", count, *baselinePath)
	if baselineErr == nil && count < baseline {
		fmt.Fprintf(stdout,
			"affi18n ratchet: count improved to %d (baseline is %d) — to raise the floor, "+
				"edit %s to %d by hand in its own commit; this tool will not do it for you\n",
			count, baseline, *baselinePath, count)
	}
	return 0
}

// readBaseline is best-effort, used only to print the improvement notice
// above; Ratchet itself is the authority on pass/fail and does its own
// (stricter) validation of the file.
func readBaseline(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
