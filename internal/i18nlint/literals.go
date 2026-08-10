package i18nlint

import (
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Finding is one flagged (or bare-nolint-errored) string literal.
type Finding struct {
	Path string // slash-separated path relative to the fs.FS root, e.g. "web/shell/banner.go"
	Line int
	Col  int
	Text string // decoded literal value (for text-* / attribute-text rules); comment text for bare-nolint
	Rule string // "text-call" | "text-child" | "attribute-text" | "bare-nolint"
}

// String renders a Finding as "path:line:col: rule: text", clickable in
// terminals and CI logs alike.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s: %q", f.Path, f.Line, f.Col, f.Rule, f.Text)
}

// TextFuncNames are GWC's dedicated text-node constructors. Any direct
// string-literal argument to one of these is prose by construction.
var TextFuncNames = map[string]bool{
	"Text":   true,
	"Textf":  true,
	"TextIf": true,
}

// TagChildFuncNames are HTML tag builders that commonly carry text
// children. A bare string argument passed directly to one of these becomes
// a text node via GWC's splitArgs -> html.Children -> appendNormalizedChild
// chain (see doc.go). Void/structural elements that don't carry prose
// (Br, Hr, Img, Input, Meta, Script, Head, Html, Body, Iframe, Video,
// Audio, Source, Track, Canvas, Svg and its children, Col, Colgroup,
// HiddenInput, Select, Textarea, Form, Fieldset) are deliberately excluded
// — either they never take a bare string child in practice, or their
// stray string children are more often IDs/values than prose, and a miss
// there is preferred over noise.
var TagChildFuncNames = map[string]bool{
	"Div": true, "Span": true, "P": true,
	"H1": true, "H2": true, "H3": true, "H4": true, "H5": true, "H6": true,
	"A": true, "Button": true, "Label": true, "Li": true,
	"Td": true, "Th": true, "Option": true, "Legend": true,
	"Summary": true, "Figcaption": true, "Caption": true,
	"Strong": true, "Em": true, "Small": true, "B": true, "I": true, "U": true,
	"Mark": true, "Abbr": true, "Kbd": true, "Blockquote": true,
	"Article": true, "Aside": true, "Header": true, "Footer": true,
	"Nav": true, "Section": true, "Main": true, "Dialog": true,
	"Details": true, "Tr": true, "Ul": true, "Ol": true,
	"Table": true, "Thead": true, "Tbody": true, "Tfoot": true,
}

// ProseAttrFuncNames set tooltip/placeholder/alt content: read by a hovering
// admin or a screen reader just as much as a text node is.
var ProseAttrFuncNames = map[string]bool{
	"Title":       true,
	"Placeholder": true,
	"Alt":         true,
}

// AttributeFuncNames are identifier/attribute/CSS builder functions whose
// string arguments are never prose: class tokens, element/attribute
// identifiers, route-shaped values, and raw CSS property names/values.
// "Tag" is handled separately (only its position-0 argument, the element
// name, is excluded — later positional args are ordinary children/props).
var AttributeFuncNames = map[string]bool{
	"Class": true, "ClassStr": true, "ClassNames": true, "ClassIf": true, "When": true,
	"ID": true, "For": true, "Name": true, "Href": true, "Src": true,
	"Role": true, "Lang": true, "Dir": true, "Target": true, "Rel": true,
	"Accept": true, "AutoComplete": true, "Min": true, "Max": true, "Step": true,
	"Pattern": true, "Loading": true, "Type": true, "Value": true,
	// css package builders (github.com/monstercameron/GoWebComponents/v5/css):
	"Property": true, "Prop": true, "Length": true, "Color": true, "Number": true,
	"Duration": true, "Easing": true, "DynamicVar": true, "DynamicLength": true,
	"DynamicName": true,
}

// LogFuncNames are log/error/panic-shaped calls: operator surface per
// TODOS.md D6-19, never interface text.
var LogFuncNames = map[string]bool{
	"Errorf": true, "New": true, "Wrap": true, "Wrapf": true,
	"Printf": true, "Print": true, "Println": true,
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"panic": true, "Panic": true, "Panicf": true, "Panicln": true,
	"Warn": true, "Warnf": true, "Warnln": true,
	"Info": true, "Infof": true, "Infoln": true,
	"Debug": true, "Debugf": true, "Debugln": true,
	"Error": true,
}

// I18nLookupFuncNames are catalogue-lookup call names: the literal argument
// IS the key, not the string to display. This is a placeholder set — see
// doc.go — extend it once web/'s i18n API is finalized.
var I18nLookupFuncNames = map[string]bool{
	"T": true, "Tf": true, "MustT": true,
	"Translate": true, "TranslateF": true,
	"Localize": true, "MustLocalize": true,
	"Msg": true, "Key": true,
}

var nolintRe = regexp.MustCompile(`nolint:([A-Za-z0-9_,]+)(.*)`)

// FindLiterals parses every non-test Go file under root in fsys and reports
// user-visible string literals per the rule documented in doc.go, plus any
// bare (reason-less) //nolint:i18n escape hatch found anywhere in scope.
func FindLiterals(fsys fs.FS, root string) ([]Finding, error) {
	var findings []Finding

	walkErr := walkGoFiles(fsys, root, func(p string, file *ast.File, fset *token.FileSet) error {
		fileFindings, err := findInFile(fset, file, p)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Col < findings[j].Col
	})
	return findings, nil
}

func findInFile(fset *token.FileSet, file *ast.File, relPath string) ([]Finding, error) {
	var findings []Finding

	// Bare-nolint check: independent of any literal, scanned across every
	// single-line comment in the file.
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "/*") {
				continue // block comments not recognized; see doc.go
			}
			m := nolintRe.FindStringSubmatch(c.Text)
			if m == nil {
				continue
			}
			names := strings.Split(m[1], ",")
			isI18n := false
			for _, n := range names {
				if strings.TrimSpace(n) == "i18n" {
					isI18n = true
					break
				}
			}
			if !isI18n {
				continue
			}
			reason := trimNolintReason(m[2])
			if reason == "" {
				pos := fset.Position(c.Pos())
				findings = append(findings, Finding{
					Path: relPath, Line: pos.Line, Col: pos.Column,
					Text: c.Text, Rule: "bare-nolint",
				})
			}
		}
	}

	// Struct-tag positions, so the generic literal walk can exclude them.
	tagLits := map[*ast.BasicLit]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok && field.Tag != nil {
			tagLits[field.Tag] = true
		}
		return true
	})

	// suppressed-by-nolint-with-reason lines
	suppressedLines := map[int]bool{}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "/*") {
				continue
			}
			m := nolintRe.FindStringSubmatch(c.Text)
			if m == nil {
				continue
			}
			names := strings.Split(m[1], ",")
			isI18n := false
			for _, n := range names {
				if strings.TrimSpace(n) == "i18n" {
					isI18n = true
					break
				}
			}
			if !isI18n || trimNolintReason(m[2]) == "" {
				continue
			}
			line := fset.Position(c.Pos()).Line
			suppressedLines[line] = true   // same line as the comment
			suppressedLines[line+1] = true // line directly below a standalone comment
		}
	}

	// Import-spec paths, excluded up front.
	importLits := map[*ast.BasicLit]bool{}
	for _, imp := range file.Imports {
		importLits[imp.Path] = true
	}

	// Manual stack maintained through ast.Inspect's enter/exit (nil-on-exit)
	// protocol, so classify() can see the full ancestor chain of each
	// literal without a second traversal.
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}

		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if !tagLits[lit] && !importLits[lit] {
				if f, flagged := classify(lit, stack); flagged {
					pos := fset.Position(lit.Pos())
					if !suppressedLines[pos.Line] {
						f.Path = relPath
						f.Line = pos.Line
						f.Col = pos.Column
						findings = append(findings, f)
					}
				}
			}
		}

		stack = append(stack, n)
		return true
	})

	return findings, nil
}

// trimNolintReason strips the leading list-of-linters remainder and common
// separators (" -- ", " // ", ": ", plain space) to see whether real reason
// text follows.
func trimNolintReason(rest string) string {
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "--")
	rest = strings.TrimPrefix(rest, "//")
	rest = strings.TrimPrefix(rest, ":")
	return strings.TrimSpace(rest)
}

// classify walks up the ancestor stack (nearest parent last) from a string
// literal, skipping through parens and `+` concatenation, to find the
// nearest enclosing shape this package has an opinion about. It returns the
// Finding to report (Path/Line/Col unset — filled by the caller) and
// whether the literal should be flagged at all.
func classify(lit *ast.BasicLit, stack []ast.Node) (Finding, bool) {
	value, err := unquote(lit.Value)
	if err != nil {
		value = strings.Trim(lit.Value, "`\"")
	}
	if value == "" {
		return Finding{}, false // nothing to translate — a spacer/placeholder child, not prose
	}

	// Walk from the literal's immediate parent outward, but only through
	// nodes that don't change "what this value is for": parens and `+`
	// concatenation. The first node that IS a decision point wins.
	child := ast.Node(lit)
	for i := len(stack) - 1; i >= 0; i-- {
		parent := stack[i]
		switch p := parent.(type) {
		case *ast.ParenExpr:
			child = p
			continue
		case *ast.BinaryExpr:
			if p.Op == token.ADD && (p.X == child || p.Y == child) {
				child = p
				continue
			}
			return Finding{}, false
		case *ast.KeyValueExpr:
			if p.Key == child {
				// map[string]bool{"class-name": true} — only excluded (not
				// flagged) when the enclosing composite literal is that
				// shape; checked one level further up.
				if isStringBoolMapKey(stack, i) {
					return Finding{}, false
				}
			}
			return Finding{}, false
		case *ast.CallExpr:
			return classifyCall(p, child, value)
		default:
			return Finding{}, false
		}
	}
	return Finding{}, false
}

// isStringBoolMapKey reports whether stack[idx] (an *ast.KeyValueExpr) sits
// directly inside a `map[string]bool{...}` composite literal.
func isStringBoolMapKey(stack []ast.Node, idx int) bool {
	if idx == 0 {
		return false
	}
	lit, ok := stack[idx-1].(*ast.CompositeLit)
	if !ok {
		return false
	}
	mt, ok := lit.Type.(*ast.MapType)
	if !ok {
		return false
	}
	keyIdent, ok := mt.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		return false
	}
	valIdent, ok := mt.Value.(*ast.Ident)
	return ok && valIdent.Name == "bool"
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

// argIndex returns the position of arg within call.Args, or -1.
func argIndex(call *ast.CallExpr, arg ast.Node) int {
	for i, a := range call.Args {
		if a == arg {
			return i
		}
	}
	return -1
}

func classifyCall(call *ast.CallExpr, arg ast.Node, value string) (Finding, bool) {
	name := callName(call)
	idx := argIndex(call, arg)
	if idx < 0 {
		return Finding{}, false // literal is the callee itself, or otherwise not a plain arg
	}

	if name == "Tag" && idx == 0 {
		return Finding{}, false // element name, not prose
	}
	if I18nLookupFuncNames[name] {
		return Finding{}, false // the literal is the catalogue key
	}
	if LogFuncNames[name] {
		return Finding{}, false // operator/log surface, D6-19
	}
	if AttributeFuncNames[name] {
		return Finding{}, false // class/id/route/CSS token, not prose
	}
	if ProseAttrFuncNames[name] {
		return Finding{Text: value, Rule: "attribute-text"}, true
	}
	if TextFuncNames[name] {
		return Finding{Text: value, Rule: "text-call"}, true
	}
	if TagChildFuncNames[name] {
		return Finding{Text: value, Rule: "text-child"}, true
	}
	return Finding{}, false
}

// unquote decodes a Go string-literal token (double-quoted or raw
// backtick) to its value. Shared with FindKeyRefs in walk.go.
func unquote(litValue string) (string, error) {
	return strconv.Unquote(litValue)
}
