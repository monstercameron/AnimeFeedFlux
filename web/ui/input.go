package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// InputProps configures Input. LabelKey/HelpKey/ErrorKey are all i18n keys
// (D0-22) — never a literal.
type InputProps struct {
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
	Type           string // "text" (default), "password", "number", …
	Disabled       bool
	Required       bool
	Mono           bool // use the data/mono font stack (cron expressions, GUIDs, numbers)
	AutoComplete   string
}

// Input is the shared text-input primitive: label, help/error text, and
// error-driven aria-invalid/aria-describedby wiring so a validation message
// is actually announced, not just colored red.
func Input(p InputProps) Node {
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

	inputRules := []css.Rule{
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
		inputRules = append(inputRules, monoFont())
	} else {
		inputRules = append(inputRules, bodyFont())
	}
	if p.Disabled {
		inputRules = append(inputRules, css.OpacityNum(css.Num(0.6)), css.Raw("cursor", "not-allowed"))
	}
	inputRules = append(inputRules, focusVisible()...)

	inputType := p.Type
	if inputType == "" {
		inputType = "text"
	}

	opts := []html.PropOption{
		html.ID(fieldID),
		html.Type(inputType),
		html.Value(p.Value),
		css.Class(inputRules),
		html.DisabledIf(p.Disabled),
		html.Required(p.Required),
		html.Aria("invalid", boolStr(hasError)),
		html.Aria("describedby", helpID),
	}
	if p.PlaceholderKey != "" {
		opts = append(opts, html.Placeholder(resolve(p.T, p.PlaceholderKey)))
	}
	if p.AutoComplete != "" {
		opts = append(opts, html.AutoComplete(p.AutoComplete))
	}
	// Registered every render regardless of p.Disabled — see button.go's
	// matching comment: html.OnInput claims a positional hook slot on the
	// enclosing fiber, and gating it on a value that varies across renders
	// (Disabled) would shift every hook slot after it in the same render.
	// The native `disabled` attribute above already stops the browser from
	// ever dispatching input events, so the guard belongs in the callback.
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
		h.Input(opts),
		helpOrError(p.T, helpID, p.HelpKey, p.ErrorKey, p.ErrorArgs),
	}
	return h.Div(fieldWrapClass(), children)
}

func fieldWrapClass() html.PropOption {
	return css.Class([]css.Rule{
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	})
}

func fieldLabelClass() html.PropOption {
	return css.Class([]css.Rule{
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		bodyFont(),
	})
}

func helpOrError(t T, helpID, helpKey, errorKey string, errorArgs []any) Node {
	if errorKey == "" && helpKey == "" {
		// Still render the (empty) node so aria-describedby always resolves
		// to a real element rather than a dangling id.
		return h.Span(html.ID(helpID))
	}
	color := tokens.Color(tokens.RoleTextMuted)
	text := resolve(t, helpKey)
	if errorKey != "" {
		color = tokens.Color(tokens.RoleDanger)
		text = resolve(t, errorKey, errorArgs...)
	}
	return h.Span(
		html.ID(helpID),
		css.Class([]css.Rule{
			css.FontSize(tokens.FontSize(tokens.TextXs)),
			css.TextColor(color),
			bodyFont(),
		}),
		text,
	)
}
