package generate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// AllRejectReasons is hand-maintained, and a hand-maintained list of things
// declared elsewhere in the same package is a list that goes stale. This
// reads the package's own source for `Reason… = "…"` declarations and
// compares, so adding a reason constant and forgetting the list fails here
// rather than silently shipping an unlabelled identifier to the run panel
// (TODOS.md A8-30).
func TestAllRejectReasonsMatchesTheDeclaredConstants(t *testing.T) {
	declared := map[string]string{} // token -> constant name
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				constName := vs.Names[0].Name
				if len(constName) < 7 || constName[:6] != "Reason" {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				reason, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				declared[reason] = constName
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no Reason… constants at all — the scan is broken, not the list")
	}

	listed := map[string]bool{}
	for _, r := range AllRejectReasons() {
		if listed[r] {
			t.Errorf("AllRejectReasons lists %q twice", r)
		}
		listed[r] = true
		if _, ok := declared[r]; !ok {
			t.Errorf("AllRejectReasons lists %q, which no Reason… constant declares", r)
		}
	}
	for tok, name := range declared {
		if !listed[tok] {
			t.Errorf("%s = %q is declared but missing from AllRejectReasons", name, tok)
		}
	}
}
