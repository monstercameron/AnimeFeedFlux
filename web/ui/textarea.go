package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// TextareaProps configures Textarea. LabelKey/HelpKey/ErrorKey are all i18n
// keys (D0-22) — never a literal. Shaped to match InputProps so a caller
// switching a field from single-line to multi-line (e.g. a feed description
// or a prompt template) changes only the constructor, not the surrounding
// wiring.
//
// # Why this exists
//
// Before this package had any production importer, the generate/history/
// settings pages that needed multi-line text (description, system/user
// prompt templates, TOML import/export, item summary/body) all fell back to
// a bare github.com/monstercameron/GoWebComponents/v5/html/shorthand
// Textarea — with no label wired to the field, no error text, and no
// aria-describedby/aria-invalid/focus-ring treatment, unlike every
// single-line field going through Input. That inconsistency is a real a11y
// gap this primitive closes, not a preference: Input and Select already
// solved label/help/error/focus wiring once; Textarea reuses that solution
// instead of leaving multi-line fields as the one input kind without it.
type TextareaProps struct {
	T              T
	ID             string
	LabelKey       string
	LabelArgs      []any
	PlaceholderKey string
	HelpKey        string // shown under the field when there is no error
	ErrorKey       string // shown instead of HelpKey when non-empty; also sets aria-invalid
	ErrorArgs      []any
	Value          string
	OnChange       func(value string)
	Disabled       bool
	Required       bool
	Mono           bool // use the data/mono font stack (TOML, prompt templates with braces/vars)
	Rows           int  // defaults to 4 when zero
}

// Textarea is the shared multi-line-text primitive: same label/help/error/
// aria-describedby convention as Input and Select (helpOrError, in input.go).
func Textarea(p TextareaProps) Node {
	fieldID := p.ID
	if fieldID == "" {
		fieldID = gwcui.UseId()
	}
	helpID := fieldID + "-help"
	hasError := p.ErrorKey != ""

	borderColor := tokens.Color(tokens.RoleBorder)
	if hasError {
		borderColor = tokens.Color(tokens.RoleDanger)
	}

	rows := p.Rows
	if rows == 0 {
		rows = 4
	}

	fieldRules := []css.Rule{
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Border(css.Px(1), borderColor),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		transitionColors(),
	}
	if p.Mono {
		fieldRules = append(fieldRules, monoFont())
	} else {
		fieldRules = append(fieldRules, bodyFont())
	}
	if p.Disabled {
		fieldRules = append(fieldRules, css.OpacityNum(css.Num(0.6)), css.Raw("cursor", "not-allowed"))
	}
	fieldRules = append(fieldRules, focusVisible()...)

	opts := []html.PropOption{
		html.ID(fieldID),
		html.Rows(rows),
		html.Value(p.Value),
		css.Class(fieldRules),
		html.DisabledIf(p.Disabled),
		html.Required(p.Required),
		html.Aria("invalid", boolStr(hasError)),
		html.Aria("describedby", helpID),
	}
	if p.PlaceholderKey != "" {
		opts = append(opts, html.Placeholder(resolve(p.T, p.PlaceholderKey)))
	}
	// Registered every render regardless of p.Disabled — see button.go's
	// matching comment on why gating hook registration on a value that
	// varies across renders is unsafe under GWC's positional hook slots.
	if p.OnChange != nil {
		onChange := p.OnChange
		disabled := p.Disabled
		opts = append(opts, html.OnInput(func(e gwcui.InputEvent) {
			if !disabled {
				onChange(e.GetValue())
			}
		}))
	}

	children := []Node{
		h.Label(html.For(fieldID), fieldLabelClass(), resolve(p.T, p.LabelKey, p.LabelArgs...)),
		h.Tag("textarea", opts),
		helpOrError(p.T, helpID, p.HelpKey, p.ErrorKey, p.ErrorArgs),
	}
	return h.Div(fieldWrapClass(), children)
}
