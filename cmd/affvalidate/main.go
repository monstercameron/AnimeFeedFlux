// Command affvalidate runs the offline feed checks in internal/feedvalidate
// against rendered feed files, standing in for the hosted W3C / RSS Advisory
// Board validator that `make validate` names in PLAN.md §5.6 (see the
// internal/feedvalidate package doc for why that substitution is necessary
// and what it does not cover).
//
// Usage:
//
//	affvalidate <file>...
//
// Format is sniffed from each file's extension: .xml/.rss -> RSS 2.0,
// .atom -> Atom 1.0, .json -> JSON Feed 1.1.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedvalidate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run takes io.Writer rather than *os.File so tests can capture output
// without redirecting real file descriptors.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: affvalidate <file>...")
		return 2
	}

	// §5.6: CI asserts zero errors *and* zero warnings, because a warning
	// here is not cosmetic — it means a real reader (Slack's RSS app, a
	// picky aggregator) will visibly mishandle the feed even though the
	// document technically parses. So a single Warning finding fails the
	// run exactly like an Error does; the Level on each Finding is for a
	// human reading the output, not for deciding the exit code.
	sawFinding := false

	for _, path := range args {
		doc, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			sawFinding = true
			continue
		}

		findings, err := validateByExtension(path, doc)
		if errors.Is(err, errSkip) {
			// Deliberately not a feed (an HTML golden sitting beside the feed
			// ones). Distinct from an unrecognised file, which is a real error:
			// conflating them would either fail the build on a permalink page
			// or silently ignore a feed nobody is validating.
			fmt.Fprintf(stdout, "%s: skipped (not a feed)\n", path)
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			sawFinding = true
			continue
		}

		if len(findings) == 0 {
			fmt.Fprintf(stdout, "%s: ok\n", path)
			continue
		}

		for _, f := range findings {
			sawFinding = true
			fmt.Fprintf(stdout, "%s: [%s] %s: %s\n", path, f.Level, f.Rule, f.Message)
		}
	}

	if sawFinding {
		return 1
	}
	return 0
}

// errSkip marks a file that is deliberately not validated rather than one that
// failed validation. Conflating the two would either fail the build on an HTML
// golden or hide a genuinely unrecognised file.
var errSkip = errors.New("not a feed document")

func validateByExtension(path string, doc []byte) ([]feedvalidate.Finding, error) {
	// Golden files are named <format>_<case>.golden and all share one
	// extension, so `make validate` cannot sniff them by suffix. Dispatch on
	// the name prefix instead. Doing this here rather than renaming the goldens
	// keeps the golden filenames readable, which is what makes a failing diff
	// legible.
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".golden") {
		switch {
		case strings.HasPrefix(base, "rss_"):
			return feedvalidate.RSS(doc), nil
		case strings.HasPrefix(base, "atom_"):
			return feedvalidate.Atom(doc), nil
		case strings.HasPrefix(base, "jsonfeed_"):
			return feedvalidate.JSONFeed(doc), nil
		case strings.HasPrefix(base, "permalink_"):
			// The item permalink page — the one thing Slack's unfurl actually
			// fetches (§5.5 "Link unfurling"). Validated against the OG/
			// unfurl-tag contract rather than skipped: skipping it left
			// `make validate` blind to the entire unfurl surface.
			return feedvalidate.Permalink(doc), nil
		case strings.HasPrefix(base, "index_"):
			// The feed index HTML page, not a feed and not a per-item
			// permalink — §5.5's OG/unfurl contract is a per-item
			// requirement, so this has nothing to check against yet. Skipped
			// rather than treated as an error: it legitimately lives
			// alongside the feed goldens.
			return nil, errSkip
		}
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".xml", ".rss":
		return feedvalidate.RSS(doc), nil
	case ".atom":
		return feedvalidate.Atom(doc), nil
	case ".json":
		return feedvalidate.JSONFeed(doc), nil
	case ".html", ".htm":
		return feedvalidate.Permalink(doc), nil
	default:
		return nil, fmt.Errorf("unrecognized feed %q (expected .xml/.rss, .atom, .json, or a <format>_*.golden)", filepath.Base(path))
	}
}
