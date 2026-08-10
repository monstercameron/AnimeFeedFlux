package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/i18nlint"
)

// runLint implements `affi18n lint [dir]` (TODOS.md D6-20): fails the build
// on a user-visible string literal in web/, output shaped as
// "path:line:col: rule: text" so it's clickable.
func runLint(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := "web"
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	findings, err := i18nlint.FindLiterals(os.DirFS("."), dir)
	if err != nil {
		fmt.Fprintf(stderr, "affi18n lint: %v\n", err)
		return 1
	}

	if len(findings) == 0 {
		fmt.Fprintf(stdout, "affi18n lint: 0 literals in %s\n", dir)
		return 0
	}

	for _, f := range findings {
		fmt.Fprintln(stdout, f.String())
	}
	fmt.Fprintf(stderr, "affi18n lint: %d finding(s) in %s\n", len(findings), dir)
	return 1
}
