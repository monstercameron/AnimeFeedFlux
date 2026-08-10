package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ToastKind selects a toast's color role. It does not select whether it is
// announced assertively — see Toast's doc comment.
type ToastKind int

const (
	ToastInfo ToastKind = iota
	ToastSuccess
	ToastWarning
	ToastDanger
)

// ToastItem is one live toast. MessageKey is an i18n key (D0-22).
type ToastItem struct {
	ID          string
	Kind        ToastKind
	MessageKey  string
	MessageArgs []any
}

// ToastProps configures Toast — the stacked notification list (e.g. "Sample
// promoted", "Run failed", "Recipe saved").
type ToastProps struct {
	T         T
	ID        string
	Items     []ToastItem
	OnDismiss func(id string)
}

// Toast renders the toast stack as a polite/assertive live region pair
// (gwcui.Announcer): Danger and Warning toasts are announced assertively (an
// operator paying attention to something else should still hear "the kill
// switch just fired" or "this run failed"), everything else politely, so
// routine confirmations do not interrupt a screen reader mid-sentence.
func Toast(p ToastProps) Node {
	announcer := gwcui.UseAnnouncer()
	gwcui.UseEffect(func() func() {
		if len(p.Items) == 0 {
			return nil
		}
		last := p.Items[len(p.Items)-1]
		msg := resolve(p.T, last.MessageKey, last.MessageArgs...)
		if last.Kind == ToastDanger || last.Kind == ToastWarning {
			announcer.Assertive(msg)
		} else {
			announcer.Polite(msg)
		}
		return nil
	}, len(p.Items))

	nodes := make([]Node, 0, len(p.Items)+1)
	nodes = append(nodes, announcer.Region())
	for _, item := range p.Items {
		nodes = append(nodes, toastCard(p.T, item, p.OnDismiss))
	}

	return h.Div(
		html.ID(p.ID),
		css.Class([]css.Rule{
			css.Raw("position", "fixed"),
			css.Raw("bottom", string(tokens.Space(4))),
			css.Raw("right", string(tokens.Space(4))),
			css.Display.Flex,
			css.FlexDir.ColRev,
			css.Gap(tokens.Space(2)),
			css.ZIndex(60),
		}),
		nodes,
	)
}

func toastCard(t T, item ToastItem, onDismiss func(string)) Node {
	accent := toastColor(item.Kind)
	id := item.ID
	dismiss := onDismiss

	closeOpt := []Node{}
	if dismiss != nil {
		closeOpt = append(closeOpt, h.Button(
			html.Type("button"),
			html.Aria("label", resolve(t, "action.dismiss")),
			css.Class(append([]css.Rule{
				css.Bg(css.Transparent),
				css.Raw("border", "none"),
				css.TextColor(tokens.Color(tokens.RoleTextMuted)),
				css.Cursor.Pointer,
			}, focusVisible()...)),
			html.OnClick(func() { dismiss(id) }),
			"×",
		))
	}

	body := []Node{
		h.Span(css.Class([]css.Rule{
			css.TextColor(tokens.Color(tokens.RoleText)),
			css.FontSize(tokens.FontSize(tokens.TextSm)),
			bodyFont(),
		}), resolve(t, item.MessageKey, item.MessageArgs...)),
	}
	body = append(body, closeOpt...)

	return html.WithKey(h.Div(
		html.Role("status"),
		css.Class([]css.Rule{
			css.Display.Flex,
			css.Items.Center,
			css.Justify.Between,
			css.Gap(tokens.Space(3)),
			css.Padding(tokens.Space(3)),
			css.MinWidth(css.Px(240)),
			css.MaxWidth(css.Px(360)),
			css.Rounded(tokens.Radius(tokens.RadiusMd)),
			css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
			css.Raw("border-left", "3px solid "+string(accent)),
			css.Raw("box-shadow", tokens.Shadow(tokens.ShadowMd)),
		}),
		body,
	), id)
}

func toastColor(k ToastKind) css.Color {
	switch k {
	case ToastSuccess:
		return tokens.Color(tokens.RoleSuccess)
	case ToastWarning:
		return tokens.Color(tokens.RoleWarning)
	case ToastDanger:
		return tokens.Color(tokens.RoleDanger)
	default:
		return tokens.Color(tokens.RoleAccent)
	}
}
