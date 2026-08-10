package i18nlint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
)

// walkGoFiles parses every non-test *.go file under root in fsys (skipping
// any testdata/ directory, the same exclusion FindLiterals documents) and
// invokes fn once per file. Shared by FindLiterals and FindKeyRefs so the
// "what counts as in-scope source" rule lives in exactly one place.
func walkGoFiles(fsys fs.FS, root string, fn func(path string, file *ast.File, fset *token.FileSet) error) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("i18nlint: parse %s: %w", p, err)
		}

		return fn(p, file, fset)
	})
}

// FindKeyRefs scans root in fsys for calls to a name in I18nLookupFuncNames
// and collects each call's literal first argument as a referenced
// catalogue key, for feeding into CheckCatalogue. Non-literal first
// arguments (a computed key) are not collected — CheckCatalogue can only
// judge what it can see statically, and a dynamically-built key is a
// documented miss here for the same reason dynamic text is a miss in
// FindLiterals. Duplicate refs (the same key used from multiple call
// sites) are preserved; CheckCatalogue deduplicates.
func FindKeyRefs(fsys fs.FS, root string) ([]string, error) {
	var refs []string
	err := walkGoFiles(fsys, root, func(_ string, file *ast.File, _ *token.FileSet) error {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if !I18nLookupFuncNames[callName(call)] {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, err := unquote(lit.Value); err == nil {
				refs = append(refs, value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}
