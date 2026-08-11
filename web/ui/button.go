package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ButtonVariant selects a button's visual weight, not its destructiveness —
// destructive actions never use Primary (PLAN.md §12.6/D0-15: they live
// behind a Kebab instead of being a primary button in the first place).
type ButtonVariant int

const (
	// ButtonSecondary is the default: bordered, surface-colored. Use for
	// most actions.
	ButtonSecondary ButtonVariant = iota
	// ButtonPrimary is the one accent-filled call to action per view (e.g.
	// "Save recipe", "Sample this query").
	ButtonPrimary
	// ButtonGhost is borderless/low-emphasis, for a toolbar's secondary
	// actions or a dismiss control.
	ButtonGhost
	// ButtonDanger is filled with the danger role. It is NOT how destructive
	// actions are normally exposed (that's Kebab, D0-15) — it exists for the
	// confirm button INSIDE a typed-confirmation modal (D0-16), where the
	// destructive action has already been isolated behind its own dialog and
	// naming it plainly is the point.
	ButtonDanger
)

// ButtonProps configures Button. LabelKey is resolved through T — Button
// never takes a literal label (D0-22).
type ButtonProps struct {
	T         T
	LabelKey  string
	LabelArgs []any
	Variant   ButtonVariant
	OnClick   func()
	Disabled  bool
	Busy      bool // in flight — disables and swaps to a busy affordance without changing layout
	Type      string
	ID        string
	FullWidth bool
}

// Button is the shared button primitive: bordered/filled variants, disabled
// and busy states, visible focus, and an i18n-keyed label.
func Button(p ButtonProps) Node {
	bg, fg, border := buttonColors(p.Variant)
	isDisabled := p.Disabled || p.Busy

	rules := []css.Rule{
		css.Display.InlineFlex,
		css.Items.Center,
		css.Justify.Center,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Border(css.Px(1), border),
		css.Bg(bg),
		css.TextColor(fg),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.FontWeight.Medium,
		bodyFont(),
		transitionColors(),
		css.Cursor.Pointer,
	}
	if p.FullWidth {
		rules = append(rules, css.W(css.Length("100%")))
	}
	if isDisabled {
		rules = append(rules, css.OpacityNum(css.Num(0.55)), css.Raw("cursor", "not-allowed"))
	} else {
		rules = append(rules, css.Hover(css.OpacityNum(css.Num(0.9)))...)
	}
	rules = append(rules, focusVisible()...)

	typ := p.Type
	if typ == "" {
		typ = "button"
	}

	opts := []html.PropOption{
		html.Type(typ),
		css.Class(rules),
		html.DisabledIf(isDisabled),
		html.Aria("busy", boolStr(p.Busy)),
	}
	if p.ID != "" {
		opts = append(opts, html.ID(p.ID))
	}
	// The handler is registered every render regardless of isDisabled — GWC's
	// html.OnClick ultimately calls ui.UseEvent, which claims a POSITIONAL
	// hook slot on the enclosing fiber (internal/runtime/hooks.go's
	// GoUseFunc). Skipping registration only when Disabled/Busy is true would
	// shift every hook slot claimed after this Button call within the same
	// render whenever Disabled/Busy flips across renders — the classic
	// "hooks must be called unconditionally" hazard, not a cosmetic issue.
	// The native `disabled` attribute above already stops the browser from
	// ever dispatching the click, so gating registration bought nothing.
	if p.OnClick != nil {
		onClick := p.OnClick
		opts = append(opts, html.OnClick(func() {
			if !isDisabled {
				onClick()
			}
		}))
	}

	label := resolve(p.T, p.LabelKey, p.LabelArgs...)
	return h.Button(opts, label)
}

func buttonColors(v ButtonVariant) (bg, fg, border css.Color) {
	switch v {
	case ButtonPrimary:
		return tokens.Color(tokens.RoleAccent), tokens.Color(tokens.RoleAccentFg), tokens.Color(tokens.RoleAccent)
	case ButtonDanger:
		return tokens.Color(tokens.RoleDanger), tokens.Color(tokens.RoleDangerFg), tokens.Color(tokens.RoleDanger)
	case ButtonGhost:
		return css.Transparent, tokens.Color(tokens.RoleText), css.Transparent
	default: // ButtonSecondary
		return tokens.Color(tokens.RoleSurface), tokens.Color(tokens.RoleText), tokens.Color(tokens.RoleBorder)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
