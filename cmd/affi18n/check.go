package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/i18nlint"
)

// runCheck implements `affi18n check --catalogue=<path> [dir]` (TODOS.md
// D6-22 and D6-23): every key referenced by code must exist in the
// catalogue, and every catalogue key must be referenced by code.
//
// The catalogue file is a flat JSON object of key -> translated string
// (`{"nav.dashboard": "Dashboard", ...}`). This is a placeholder format:
// web/'s actual catalogue format is being decided concurrently by other
// agents: swap the decode in loadCatalogue for whatever that format turns
// out to be — CheckCatalogue itself only needs a map[string]string and
// doesn't care where it came from.
func runCheck(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cataloguePath := fs.String("catalogue", "", "path to the i18n catalogue (flat JSON key -> string)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cataloguePath == "" {
		fmt.Fprintln(stderr, "affi18n check: --catalogue=<path> is required")
		return 2
	}

	dir := "web"
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	cat, err := loadCatalogue(*cataloguePath)
	if err != nil {
		fmt.Fprintf(stderr, "affi18n check: %v\n", err)
		return 1
	}

	refs, err := i18nlint.FindKeyRefs(os.DirFS("."), dir)
	if err != nil {
		fmt.Fprintf(stderr, "affi18n check: %v\n", err)
		return 1
	}

	missing, unused := i18nlint.CheckCatalogue(cat, refs)

	if len(missing) == 0 && len(unused) == 0 {
		fmt.Fprintf(stdout, "affi18n check: catalogue and %s agree (%d refs, %d keys)\n", dir, len(refs), len(cat))
		return 0
	}

	for _, key := range missing {
		fmt.Fprintf(stdout, "%s: missing: referenced key %q has no catalogue entry\n", *cataloguePath, key)
	}
	for _, key := range unused {
		fmt.Fprintf(stdout, "%s: unused: catalogue key %q is never referenced\n", *cataloguePath, key)
	}
	fmt.Fprintf(stderr, "affi18n check: %d missing, %d unused\n", len(missing), len(unused))
	return 1
}

func loadCatalogue(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading catalogue %s: %w", path, err)
	}
	var cat map[string]string
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parsing catalogue %s as flat JSON key -> string: %w", path, err)
	}
	return cat, nil
}
