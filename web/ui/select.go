package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// SelectOption is one <option>. LabelKey is resolved through T — SelectOption
// never carries a literal label (D0-22).
type SelectOption struct {
	Value    string
	LabelKey string
}

// SelectProps configures Select.
type SelectProps struct {
	T         T
	ID        string
	LabelKey  string
	HelpKey   string
	ErrorKey  string
	ErrorArgs []any
	Value     string
	Options   []SelectOption
	OnChange  func(value string)
	Disabled  bool
	Required  bool
}

// Select is the shared select primitive, styled to match Input and wired to
// the same help/error/aria-describedby convention.
func Select(p SelectProps) Node {
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

	rules := []css.Rule{
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Border(css.Px(1), borderColor),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		bodyFont(),
		transitionColors(),
	}
	if p.Disabled {
		rules = append(rules, css.OpacityNum(css.Num(0.6)), css.Raw("cursor", "not-allowed"))
	}
	rules = append(rules, focusVisible()...)

	opts := []html.PropOption{
		html.ID(fieldID),
		css.Class(rules),
		html.DisabledIf(p.Disabled),
		html.Required(p.Required),
		html.Aria("invalid", boolStr(hasError)),
		html.Aria("describedby", helpID),
	}
	// Registered every render regardless of p.Disabled — see button.go's
	// matching comment on why gating hook registration on a value that
	// varies across renders is unsafe under GWC's positional hook slots.
	if p.OnChange != nil {
		onChange := p.OnChange
		disabled := p.Disabled
		opts = append(opts, html.OnChange(func(e gwcui.ChangeEvent) {
			if !disabled {
				onChange(e.GetValue())
			}
		}))
	}

	optionNodes := make([]Node, 0, len(p.Options))
	for _, o := range p.Options {
		optionNodes = append(optionNodes, h.Option(
			html.Value(o.Value), html.SelectedIf(o.Value == p.Value),
			resolve(p.T, o.LabelKey),
		))
	}

	children := []Node{
		h.Label(html.For(fieldID), fieldLabelClass(), resolve(p.T, p.LabelKey)),
		h.Select(opts, optionNodes),
		helpOrError(p.T, helpID, p.HelpKey, p.ErrorKey, p.ErrorArgs),
	}
	return h.Div(fieldWrapClass(), children)
}
