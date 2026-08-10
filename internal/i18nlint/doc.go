// Package i18nlint checks the i18n convention described in PLAN.md §12.6 and
// tracked as TODOS.md D6-20..24: every string an admin can read in web/ goes
// through the i18n catalogue rather than living as a hardcoded literal.
//
// The package is deliberately dependency-free (go/ast, go/parser, go/token
// only) and works against any fs.FS, so it is testable against fixture
// trees under testdata/ without a live web/ directory and without a binary.
//
// # The user-visible rule (FindLiterals)
//
// FindLiterals is a syntactic, not a type-checked, analysis: it never loads
// the full program (no go/types, no golang.org/x/tools/go/packages — those
// are exactly the "new deps" this package is barred from taking). That
// means every rule below is a heuristic over the AST shape, matched by
// function name rather than by resolved type. The trade a heuristic forces
// is between false positives (flagging things that aren't prose — CSS
// class names, element tags, route paths, struct tags) and false negatives
// (missing prose that doesn't flow through a recognized shape, e.g. string
// concatenation across a helper function, or a literal assigned to a local
// variable three lines before it's rendered). This package chooses false
// negatives: a linter with a high false-positive rate gets disabled within
// a week, and an under-flagged convention that people still trust beats an
// over-flagged one nobody reads the output of.
//
// A string literal is flagged ONLY when it appears in one of these three
// positive shapes, all read directly off the GoWebComponents v5 API this
// repo is built on (github.com/monstercameron/GoWebComponents/v5/html and
// .../html/shorthand):
//
//  1. A direct argument to Text, Textf, or TextIf (any package — these are
//     GWC's dedicated text-node constructors; h.Text("Save") renders that
//     literal string to the page). Rule: "text-call".
//  2. A bare (unwrapped) argument to one of the HTML tag-builder functions
//     that commonly carries prose children (Div, Span, P, H1..H6, A,
//     Button, Label, Li, Td, Th, Option, Legend, Summary, Figcaption,
//     Caption, and similar — see TagChildFuncNames). GWC's splitArgs sends
//     any bare string argument through html.Children, whose `case string`
//     wraps it in Text(...) — so a bare string here becomes a text node by
//     construction, not by convention. Rule: "text-child".
//  3. An argument to Title, Placeholder, or Alt — these set tooltip,
//     placeholder-hint, and alt-text content respectively, which a screen
//     reader or a hovering admin reads just as much as a text node. Rule:
//     "attribute-text".
//
// Literal string concatenation (`h.Text("Hello, " + name)`) is traced
// through a chain of `+` and parens back to its enclosing call, so the
// literal operand is still attributed to that call's rule — but only
// through that one shape; concatenation hidden behind a helper function is
// a documented miss.
//
// # What is deliberately never flagged
//
// These are excluded before the positive rules are even consulted, because
// they are exactly the categories that make a naive string-literal linter
// unusable:
//
//   - The empty string literal ("") in any position — there is nothing to
//     translate in a zero-length spacer/placeholder value.
//   - Struct tags (`json:"id"`) — not prose, and not even reachable as a
//     normal expression literal.
//   - Import paths.
//   - `_test.go` files, wholesale — test fixtures and assertions are not
//     interface text.
//   - Any file under a `testdata/` directory.
//   - String literals used as map keys in a `map[string]bool` composite
//     literal — this is exactly the shape `h.ClassMap(map[string]bool{...})`
//     uses, where the keys are CSS class-name tokens, not prose.
//   - Arguments to a curated set of attribute/identifier/CSS builder
//     functions (Class, ClassStr, ClassNames, ClassIf, When, ID, For, Name,
//     Href, Src, Role, Lang, Dir, Target, Rel, Accept, AutoComplete, Min,
//     Max, Step, Pattern, Loading, Type, Value, and the css package's
//     Property/Prop/Length/Color/Number/Duration/Easing/DynamicVar/
//     DynamicLength family) — see AttributeFuncNames.
//   - The tag-name argument (position 0 only) to shorthand.Tag — an element
//     name like "div", not prose.
//   - Arguments to log/error/panic-shaped calls (Errorf, errors.New,
//     Printf, Println, Print, Fatal*, Panic*, Warn*, Info*, Debug*, Wrap*)
//     — operator/log surface per TODOS.md D6-19, not interface text.
//   - Arguments to a curated set of i18n lookup function names (T, Tf,
//     MustT, Translate, TranslateF, Localize, MustLocalize, Msg, Key) — the
//     literal IS the catalogue key, not the text to show. This list is a
//     placeholder: web/'s actual i18n API is being built concurrently by
//     other agents and its lookup function name is not yet fixed. Extend
//     I18nLookupFuncNames (or the package name heuristic beside it) once
//     that API lands — this is the one rule most likely to need a same-day
//     edit after this package ships.
//
// Anything outside those three positive shapes is not flagged: a literal
// assigned to a variable, returned bare from a function, stored in a
// non-map[string]bool composite literal field, or built through a helper
// function this package doesn't know about. That is the stated false-
// negative surface, and it is intentional.
//
// # Escape hatch
//
// A `//nolint:i18n` comment suppresses nothing by itself — TODOS.md D6-20
// requires the escape hatch to carry a reason, or the bare form becomes an
// error in its own right (rule "bare-nolint"), reported independently of
// whatever it's attached to. `//nolint:i18n -- reason text` (or `:`, `//`,
// or plain-space separated reason text) is treated as having a reason and
// suppresses any literal on the same line or the line directly below the
// comment. Only single-line `//` comments are recognized; a `/* ... */`
// block comment is not matched, which is a documented limitation.
package i18nlint
