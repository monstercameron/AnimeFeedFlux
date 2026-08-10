package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ModalProps configures Modal. TitleKey is required — it becomes the
// dialog's accessible name via aria-labelledby.
type ModalProps struct {
	T         T
	ID        string
	TitleKey  string
	Open      bool
	OnDismiss func()
	Children  []Node
	// Persistent disables Escape/outside-click dismissal — for a modal
	// mid-mutation (an in-flight typed confirmation) where an accidental
	// dismiss should not be free.
	Persistent bool
}

// Modal is the shared modal primitive: role="dialog", trapped and restored
// focus, Esc/backdrop dismissal (unless Persistent), scroll lock, and a
// visible close control — built on gwcui.AccessibleOverlay rather than a
// hand-rolled fixed-position div, so the focus-trap/aria wiring the a11y
// floor requires is the library's tested implementation, not this
// package's.
func Modal(p ModalProps) Node {
	titleID := p.ID + "-title"
	onDismiss := p.OnDismiss

	closeBtn := h.Button(
		html.Type("button"),
		html.Aria("label", resolve(p.T, "action.close")),
		css.Class(append([]css.Rule{
			css.Raw("position", "absolute"),
			css.Raw("top", string(tokens.Space(3))),
			css.Raw("right", string(tokens.Space(3))),
			css.W(css.Px(28)),
			css.H(css.Px(28)),
			css.Display.InlineFlex,
			css.Items.Center,
			css.Justify.Center,
			css.Rounded(tokens.Radius(tokens.RadiusMd)),
			css.Bg(css.Transparent),
			css.Raw("border", "none"),
			css.TextColor(tokens.Color(tokens.RoleTextMuted)),
			css.Cursor.Pointer,
		}, focusVisible()...)),
		html.OnClick(func() {
			if onDismiss != nil {
				onDismiss()
			}
		}),
		"×",
	)

	body := []Node{
		closeBtn,
		h.Tag("h2", html.ID(titleID), css.Class([]css.Rule{
			css.FontSize(tokens.FontSize(tokens.TextLg)),
			css.FontWeight.Semibold,
			css.TextColor(tokens.Color(tokens.RoleText)),
			bodyFont(),
			css.Margin(css.Zero),
		}), resolve(p.T, p.TitleKey)),
	}
	body = append(body, p.Children...)

	return gwcui.AccessibleOverlay(gwcui.AccessibleOverlayProps{
		Open:                p.Open,
		SurfaceID:           p.ID,
		Kind:                gwcui.OverlayKindDialog,
		Role:                "dialog",
		Modal:               true,
		LabelledBy:          titleID,
		TrapFocus:           true,
		RestoreFocus:        true,
		CloseOnEscape:       !p.Persistent,
		CloseOnOutsideClick: !p.Persistent,
		LockScroll:          true,
		BackgroundInert:     true,
		Backdrop:            true,
		BaseZIndex:          50,
		BackdropStyle: map[string]string{
			"background": string(tokens.Color(tokens.RoleScrim)),
		},
		SurfaceClass: string(css.New(
			css.Raw("position", "relative"),
			css.Display.Flex,
			css.FlexDir.Col,
			css.Gap(tokens.Space(4)),
			css.Padding(tokens.Space(6)),
			css.MaxWidth(css.Px(560)),
			css.W(css.Length("100%")),
			css.Rounded(tokens.Radius(tokens.RadiusLg)),
			css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
			css.Raw("box-shadow", tokens.Shadow(tokens.ShadowLg)),
		)),
		Children: body,
		OnDismiss: func() {
			if onDismiss != nil {
				onDismiss()
			}
		},
	})
}
