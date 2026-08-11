package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ToggleProps configures Toggle — a labeled on/off switch (e.g. a feed's
// enable toggle, PLAN.md §12.3). LabelKey is a11y-required: Toggle renders a
// real <button role="switch"> with aria-checked, not a bare unlabeled input.
type ToggleProps struct {
	T        T
	ID       string
	LabelKey string
	Checked  bool
	OnChange func(checked bool)
	Disabled bool
	// DisabledReasonKey, when set alongside Disabled, is exposed as
	// aria-describedby text so a disabled toggle explains itself inline —
	// the same "never a dead control with no explanation" rule as the
	// dedicated DisabledWithReason state component (§12.3), applied to a
	// single control rather than a whole panel.
	DisabledReasonKey string
}

// Toggle is the shared on/off switch primitive.
func Toggle(p ToggleProps) Node {
	id := p.ID
	if id == "" {
		id = gwcui.UseId()
	}
	reasonID := id + "-reason"

	trackBg := tokens.Color(tokens.RoleBorder)
	if p.Checked {
		trackBg = tokens.Color(tokens.RoleAccent)
	}

	// 44x24 rather than 36x20. The track IS the hit target — there is no
	// padded wrapper around it — and 20px tall is under the floor where a
	// pointer target stops being comfortable. This one carries the
	// generation kill switch, so a mis-click is not a cosmetic problem.
	trackRules := []css.Rule{
		css.Display.InlineFlex,
		css.Items.Center,
		css.Raw("position", "relative"),
		css.W(css.Px(44)),
		css.H(css.Px(24)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(trackBg),
		css.Raw("border", "none"),
		css.Cursor.Pointer,
		transitionColors(),
	}
	if p.Disabled {
		trackRules = append(trackRules, css.OpacityNum(css.Num(0.5)), css.Raw("cursor", "not-allowed"))
	}
	trackRules = append(trackRules, focusVisible()...)

	knobOffset := "3px"
	if p.Checked {
		// Track width minus knob width minus the 3px inset on the far side.
		knobOffset = "23px"
	}
	knobRules := []css.Rule{
		css.Raw("position", "absolute"),
		css.Raw("top", "3px"),
		css.Raw("left", knobOffset),
		css.W(css.Px(18)),
		css.H(css.Px(18)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Raw("transition", "left "+string(tokens.Duration(tokens.DurationFast))+" "+string(tokens.Easing(tokens.EasingStd))),
	}

	opts := []html.PropOption{
		html.ID(id),
		html.Type("button"),
		html.Role("switch"),
		css.Class(trackRules),
		html.Aria("checked", boolStr(p.Checked)),
		html.DisabledIf(p.Disabled),
	}
	if p.DisabledReasonKey != "" {
		opts = append(opts, html.Aria("describedby", reasonID))
	}
	// Registered every render regardless of p.Disabled — see button.go's
	// matching comment on why gating hook registration on a value that
	// varies across renders is unsafe under GWC's positional hook slots.
	if p.OnChange != nil {
		onChange := p.OnChange
		checked := p.Checked
		disabled := p.Disabled
		opts = append(opts, html.OnClick(func() {
			if !disabled {
				onChange(!checked)
			}
		}))
	}

	switchNode := h.Button(opts, h.Span(css.Class(knobRules)))

	labelNode := h.Label(html.For(id), fieldLabelClass(), resolve(p.T, p.LabelKey))

	row := h.Div(
		css.Class([]css.Rule{css.Display.Flex, css.Items.Center, css.Gap(tokens.Space(3))}),
		switchNode, labelNode,
	)
	if p.DisabledReasonKey == "" {
		return row
	}
	return h.Div(
		css.Class([]css.Rule{css.Display.Flex, css.FlexDir.Col, css.Gap(tokens.Space(1))}),
		row,
		h.Span(
			html.ID(reasonID),
			css.Class([]css.Rule{
				css.FontSize(tokens.FontSize(tokens.TextXs)),
				css.TextColor(tokens.Color(tokens.RoleTextMuted)),
				bodyFont(),
			}),
			resolve(p.T, p.DisabledReasonKey),
		),
	)
}
