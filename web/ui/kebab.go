package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// KebabItem is one action in a Kebab menu. LabelKey is an i18n key (D0-22).
// Danger marks it as destructive (visually, in the danger role, and moved to
// the end of the menu by OrderKebabItems) — PLAN.md §12.6/D0-15: destructive
// actions live behind this kebab, never a primary button.
type KebabItem struct {
	ID       string
	LabelKey string
	OnSelect func()
	Danger   bool
	Disabled bool
}

// OrderKebabItems returns items with all non-destructive actions first (in
// their original relative order) followed by all destructive ones (also in
// their original relative order) — a stable partition, not a sort, so two
// callers who list their safe actions in a considered order never have that
// order silently reshuffled. Kept as a pure function (no DOM) so the
// ordering rule is host-testable on its own (TODOS.md Tests §).
func OrderKebabItems(items []KebabItem) []KebabItem {
	out := make([]KebabItem, 0, len(items))
	for _, it := range items {
		if !it.Danger {
			out = append(out, it)
		}
	}
	for _, it := range items {
		if it.Danger {
			out = append(out, it)
		}
	}
	return out
}

// KebabProps configures Kebab. LabelKey is the accessible name for the
// trigger button (e.g. "kebab.actionsFor" with the row's title interpolated)
// — a bare "⋯" with no accessible name is unusable with a screen reader.
type KebabProps struct {
	T            T
	ID           string
	LabelKey     string
	LabelArgs    []any
	Items        []KebabItem
	Open         bool
	OnOpenChange func(open bool)
}

// Kebab is the shared "⋯" overflow-menu primitive: destructive items are
// visually separated to the end (OrderKebabItems), the trigger has a real
// accessible name, and the menu is a trapped-focus, Esc-closable
// AccessibleOverlay rather than a hand-rolled absolutely-positioned div.
func Kebab(p KebabProps) Node {
	triggerID := p.ID + "-trigger"
	menuID := p.ID + "-menu"
	ordered := OrderKebabItems(p.Items)

	triggerRules := []css.Rule{
		css.Display.InlineFlex,
		css.Items.Center,
		css.Justify.Center,
		css.W(css.Px(32)),
		css.H(css.Px(32)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Bg(css.Transparent),
		css.Raw("border", "none"),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.Cursor.Pointer,
		transitionColors(),
	}
	triggerRules = append(triggerRules, css.Hover(css.Bg(tokens.Color(tokens.RoleSurfaceRaised)))...)
	triggerRules = append(triggerRules, focusVisible()...)

	trigger := h.Button(
		html.ID(triggerID),
		html.Type("button"),
		html.Aria("haspopup", "menu"),
		html.Aria("expanded", boolStr(p.Open)),
		html.Aria("controls", menuID),
		html.Aria("label", resolve(p.T, p.LabelKey, p.LabelArgs...)),
		css.Class(triggerRules),
		html.OnClick(func() {
			if p.OnOpenChange != nil {
				p.OnOpenChange(!p.Open)
			}
		}),
		"⋯",
	)

	itemNodes := make([]Node, 0, len(ordered))
	for _, it := range ordered {
		color := tokens.Color(tokens.RoleText)
		if it.Danger {
			color = tokens.Color(tokens.RoleDanger)
		}
		rules := []css.Rule{
			css.Display.Flex,
			css.W(css.Length("100%")),
			css.Raw("text-align", "left"),
			css.Padding(tokens.Space(2)),
			css.PaddingX(tokens.Space(3)),
			css.Bg(css.Transparent),
			css.Raw("border", "none"),
			css.TextColor(color),
			css.FontSize(tokens.FontSize(tokens.TextSm)),
			bodyFont(),
			css.Cursor.Pointer,
			transitionColors(),
		}
		if it.Disabled {
			rules = append(rules, css.OpacityNum(css.Num(0.5)), css.Raw("cursor", "not-allowed"))
		} else {
			rules = append(rules, css.Hover(css.Bg(tokens.Color(tokens.RoleSurface)))...)
		}
		rules = append(rules, focusVisible()...)

		opts := []html.PropOption{
			html.ID(it.ID),
			html.Type("button"),
			html.Role("menuitem"),
			css.Class(rules),
			html.DisabledIf(it.Disabled),
		}
		onSelect := it.OnSelect
		openChange := p.OnOpenChange
		if onSelect != nil && !it.Disabled {
			opts = append(opts, html.OnClick(func() {
				onSelect()
				if openChange != nil {
					openChange(false)
				}
			}))
		}
		itemNodes = append(itemNodes, h.Button(opts, resolve(p.T, it.LabelKey)))
	}

	onDismiss := p.OnOpenChange
	menu := gwcui.AccessibleOverlay(gwcui.AccessibleOverlayProps{
		Open:                p.Open,
		SurfaceID:           menuID,
		Kind:                gwcui.OverlayKindMenu,
		Role:                "menu",
		AnchorSelector:      "#" + triggerID,
		Positioning:         "anchored",
		TrapFocus:           true,
		RestoreFocus:        true,
		CloseOnEscape:       true,
		CloseOnOutsideClick: true,
		BaseZIndex:          40,
		SurfaceClass: string(css.New(
			css.Display.Flex,
			css.FlexDir.Col,
			css.Padding(tokens.Space(1)),
			css.MinWidth(css.Px(180)),
			css.Rounded(tokens.Radius(tokens.RadiusMd)),
			css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
			css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
			css.Raw("box-shadow", tokens.Shadow(tokens.ShadowMd)),
		)),
		Children: itemNodes,
		OnDismiss: func() {
			if onDismiss != nil {
				onDismiss(false)
			}
		},
	})

	return h.Div(css.Class([]css.Rule{css.Raw("position", "relative"), css.Display.InlineBlock}), trigger, menu)
}
