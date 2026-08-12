package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// TestEveryCallSiteKeyIsDefined is TODOS.md D6-22's actual deliverable, and
// it exists because every other test in this package was structurally unable
// to catch the bug it is named for.
//
// The failure it prevents: a key referenced by a page but absent from the
// catalogue renders as its own name, so the interface says
// "common.connectionUnreachable" to an operator instead of telling them the
// server is unreachable. That shipped. It was found by a person looking at a
// login screen, after two separate automated sweeps in the same session had
// declared the catalogue complete.
//
// Why the existing tests could not see it:
//
//   - TestEveryDeclaredKeyResolves and TestNoOrphanCatalogueEntries are both
//     driven from hand-maintained slices in catalog_test.go. A key nobody
//     adds to the slice is invisible to both, by construction.
//   - TestGenerateHistorySettingsResolve iterates the CATALOGUE's own keys,
//     which is the exact inverse of the question: it can prove every entry
//     resolves, never that every call site has an entry.
//   - internal/i18nlint.FindKeyRefs only collects string LITERALS passed as
//     a lookup call's first argument. Both keys that reached production this
//     way were declared as `const keyFoo = "foo"` and passed as identifiers,
//     so the literal never appears at a call site at all.
//
// This test reads the real source of web/pages and web/shell and collects
// keys three ways — literal call arguments, `key*` string constants, and the
// `...Key:` struct fields the shared web/ui primitives take — then asserts
// each one is defined in some namespace. Namespace-agnostic on purpose: a
// key is bound to its namespace at the call site by intl.NS(...), which is
// not statically recoverable here, and "defined nowhere" is the failure that
// actually reaches the screen.
func TestEveryCallSiteKeyIsDefined(t *testing.T) {
	// Host-only: this walks web/pages and web/shell on disk, and Go's js
	// syscall layer has no O_DIRECTORY, so a directory walk cannot run in a
	// browser build at all. Nothing is lost by skipping there — the source
	// tree it reads is identical whichever way the tests were compiled, and
	// the host run covers it every time. See scripts/coverage-wasm.sh for why
	// this package is now built under wasm as well.
	if runtime.GOOS == "js" {
		t.Skip("scans the source tree; directory reads are unsupported in a js/wasm build")
	}

	defined := map[string]bool{}
	for _, cat := range []gwci18n.NamespaceCatalog{
		authMessages, commonMessages, shellMessages,
		generateMessages, historyMessages, settingsMessages,
	} {
		for k := range cat {
			defined[k] = true
		}
	}

	refs, err := collectKeyRefs("..")
	if err != nil {
		t.Fatalf("scanning call sites: %v", err)
	}
	if len(refs) < 100 {
		// A scanner that silently stops finding anything would make this
		// test pass forever while checking nothing.
		t.Fatalf("only %d key references found — the scanner is broken, not the catalogue", len(refs))
	}

	for key, where := range refs {
		if !defined[key] {
			t.Errorf("key %q is referenced by %s but defined in no namespace catalogue — it will render as its own name", key, where)
		}
	}
}

// lookupFuncs are the call shapes whose FIRST argument is a catalogue key.
var lookupFuncs = map[string]bool{"T": true, "t": true, "tc": true, "Translate": true}

// keyFields are the struct fields the shared web/ui primitives accept a key
// in (Button.LabelKey, Confirm.TitleKey, ...). They take keys as literals at
// the composite-literal site rather than as call arguments, so the call-based
// scan alone misses them.
var keyFields = map[string]bool{
	"LabelKey": true, "TitleKey": true, "MessageKey": true, "PromptKey": true,
	"DisabledReasonKey": true, "EmptyKey": true, "ErrorKey": true, "HeadingKey": true,
}

// collectKeyRefs walks web/pages and web/shell under root and returns every
// catalogue key it can see, mapped to the file it was found in.
func collectKeyRefs(root string) (map[string]string, error) {
	out := map[string]string{}

	for _, dir := range []string{"pages", "shell"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			// The page packages are js/wasm-only, but parsing is
			// target-independent — this reads source, it does not build it,
			// which is what lets a host-run `go test ./web/i18n/` check
			// files that only compile under GOOS=js.
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // an unparseable file is not this test's business
			}
			rel := filepath.ToSlash(path)

			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if len(node.Args) == 0 || !lookupFuncs[calleeName(node)] {
						return true
					}
					if v, ok := stringLit(node.Args[0]); ok {
						out[v] = rel
					}
				case *ast.ValueSpec:
					// `const keyFoo = "foo"` / `keyFoo := "foo"`.
					for i, name := range node.Names {
						if !strings.HasPrefix(name.Name, "key") || i >= len(node.Values) {
							continue
						}
						if v, ok := stringLit(node.Values[i]); ok {
							out[v] = rel
						}
					}
				case *ast.KeyValueExpr:
					// `LabelKey: "settings.security.signOut.action"`.
					ident, ok := node.Key.(*ast.Ident)
					if !ok || !keyFields[ident.Name] {
						return true
					}
					if v, ok := stringLit(node.Value); ok {
						out[v] = rel
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}
