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
		// Decorative glyph; the button's accessible name is the translated
		// aria-label above (resolve(p.T, "action.close")) — see kebab.go's
		// matching comment.
		h.Span(html.Aria("hidden", "true"), "×"), //nolint:i18n -- decorative glyph, aria-hidden; accessible name is the translated aria-label above
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

	// D5-01: two independent narrow-width problems, two independent fixes.
	// (1) Content taller than the viewport (a long form on a short/landscape
	// phone) must scroll INSIDE the surface, not push the surface itself
	// off-screen or force the page to scroll behind the backdrop — hence the
	// unconditional max-height/overflow-y here, not gated to narrow widths,
	// since a short viewport is not exclusively a narrow one. (2) Below
	// NarrowMaxWidth the fixed Space(6) padding and the 560px cap both cost
	// too much of a ~320-375px screen, so both shrink; MaxWidth switches to a
	// viewport-relative calc() so the surface keeps a safe margin even if
	// its positioning wrapper (owned by gwcui, not this package) applies
	// none of its own.
	surfaceRules := []css.Rule{
		css.Raw("position", "relative"),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
		css.Padding(tokens.Space(6)),
		css.MaxWidth(css.Px(560)),
		css.W(css.Length("100%")),
		css.Raw("max-height", "calc(100vh - "+string(tokens.Space(8))+")"),
		css.OverflowY.Auto,
		css.Rounded(tokens.Radius(tokens.RadiusLg)),
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.Raw("box-shadow", tokens.Shadow(tokens.ShadowLg)),
	}
	surfaceRules = append(surfaceRules, narrowMedia(
		css.Padding(tokens.Space(4)),
		css.MaxWidth(css.Length("calc(100vw - 32px)")),
	)...)

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
		SurfaceClass: string(css.New(surfaceRules...)),
		Children:     body,
		OnDismiss: func() {
			if onDismiss != nil {
				onDismiss()
			}
		},
	})
}
