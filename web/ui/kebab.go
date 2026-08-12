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

	// lastDismiss is when the overlay last closed itself. See the trigger's
	// click handler for what it is for.
	lastDismiss := gwcui.UseRef(kebabNoDismiss)

	// Where the menu goes. See kebabAnchor for why this is measured rather
	// than expressed in CSS.
	anchor := gwcui.UseState(kebabRect{})
	gwcui.UseEffect(func() func() {
		if !p.Open {
			return nil
		}
		anchor.Set(measureKebabAnchor(triggerID, len(ordered)))
		return nil
	}, p.Open, triggerID)

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
			if p.OnOpenChange == nil {
				return
			}
			// Ignore a click that lands right after a dismissal, because that
			// dismissal was almost certainly caused by this very click.
			//
			// gwcui.Overlay closes on `pointerdown`, captured at the document,
			// and it treats anything outside the SURFACE as outside — which
			// includes this trigger. So clicking an open menu ran: pointerdown
			// closes it, then click toggles it, and the menu came back. What
			// the operator saw was the menu flashing shut and reopening, and
			// then refusing to close at all.
			//
			// The trigger cannot simply stop the event: the library's listener
			// runs in the capture phase at the document, before the target's
			// own handlers, so there is nothing left to stop by the time this
			// runs. Suppressing the toggle for a moment after a dismissal is
			// what is actually available, and it gives the right answer in
			// both directions — a click on a closed menu opens it, a click on
			// an open one leaves it closed.
			if since := kebabSinceDismiss(lastDismiss.Get()); since >= 0 && since < kebabDismissGuard {
				return
			}
			p.OnOpenChange(!p.Open)
		}),
		// The glyph is decorative, not prose: the trigger's accessible name
		// is the translated aria-label set above (resolve(p.T, p.LabelKey,
		// ...)), so a screen reader never reaches this text node at all.
		// aria-hidden makes that explicit rather than relying on aria-label
		// silently overriding the accessible-name computation.
		h.Span(html.Aria("hidden", "true"), "⋯"), //nolint:i18n -- decorative glyph, aria-hidden; accessible name is the translated aria-label above
	)

	// Built only when the menu is open (TODOS.md A8-44). Closed, this loop
	// produced a ~12-rule []css.Rule, two more slices from the Hover and
	// focus-visible appends, a css.Class, a catalogue lookup and a closure
	// per item — on /history/items, 25 rows × ~5 items ≈ 125 menu buttons
	// rebuilt on every selection toggle, search keystroke and filter change,
	// and thrown away because nothing was open.
	//
	// Hook-safe, and checked rather than assumed: the loop contains no hooks.
	// html.OnClick and css.Class are plain prop setters in v5.0.1 (sugar.go's
	// optionFunc, css.go's Class), not hook registrations, and the three real
	// hooks — UseRef, UseState, UseEffect — sit above this and stay
	// unconditional, so no positional slot moves when Open flips.
	var itemNodes []Node
	if p.Open {
		itemNodes = buildKebabItems(p, ordered)
	}
	onDismiss := p.OnOpenChange
	// D5-01: MinWidth(180px) is comfortable up to phone width, but the same
	// 180px against a 320px viewport leaves only ~140px of margin either
	// side of an anchor near the screen edge — MaxWidth caps the menu at a
	// viewport-relative size below NarrowMaxWidth so it can never be wider
	// than the screen it is anchored to. Whether the anchored positioning
	// itself flips to stay on-screen when it would otherwise overflow is
	// decided by gwcui.AccessibleOverlay's positioning implementation, not
	// this package, and is one of the things this ticket could not verify
	// without a browser (see the report for the full list).
	menuRules := []css.Rule{
		css.Display.Flex,
		css.FlexDir.Col,
		css.Padding(tokens.Space(1)),
		css.MinWidth(css.Px(180)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.Raw("box-shadow", tokens.Shadow(tokens.ShadowMd)),
	}
	menuRules = append(menuRules, narrowMedia(
		css.MinWidth(css.Length("calc(100vw - 32px)")),
		css.MaxWidth(css.Length("calc(100vw - 32px)")),
	)...)
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
		SurfaceClass:        string(css.New(menuRules...)),
		// Fixed, at coordinates measured from the trigger.
		//
		// gwcui.Overlay positions nothing: it sets z-index and a
		// `data-overlay-positioning` attribute that is documentation, not
		// behaviour (the library's own anchored-overlay example passes
		// `fixed left-… top-…` in through SurfaceClass). So this menu rendered
		// `position: static` and pushed the page down by its own height every
		// time it opened.
		//
		// `absolute` inside the relative wrapper below would have been the
		// smaller change, but a kebab lives in a table row, and this app's
		// tables are their own horizontal-scroll containers — an absolutely
		// positioned menu is clipped by the first scrolling ancestor. Fixed
		// coordinates are immune to that, at the cost of having to measure.
		SurfaceStyle: anchor.Get().style(),
		Children:     itemNodes,
		OnDismiss: func() {
			lastDismiss.Set(kebabNow())
			if onDismiss != nil {
				onDismiss(false)
			}
		},
	})

	return h.Div(css.Class([]css.Rule{css.Raw("position", "relative"), css.Display.InlineBlock}), trigger, menu)
}

// buildKebabItems renders the menu's buttons. Called only when the menu is
// open (A8-44): closed, it produced a ~12-rule []css.Rule, two more slices
// from the Hover and focus-visible appends, a css.Class, a catalogue lookup
// and a closure per item — on /history/items, 25 rows × ~5 items ≈ 125 menu
// buttons rebuilt on every selection toggle, search keystroke and filter
// change, and thrown away because nothing was open.
//
// It is a function rather than an inline `if` block so that the hook question
// is answerable by looking at it: there are no hooks in here, and there can
// be none, because a hook inside a conditionally-called function is exactly
// the positional-slot bug this guard must not introduce. html.OnClick and
// css.Class are plain prop setters in v5.0.1 (sugar.go's optionFunc, css.go's
// Class), not hook registrations.
func buildKebabItems(p KebabProps, ordered []KebabItem) []Node {
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
		disabled := it.Disabled
		// The click handler is attached regardless of it.Disabled, and the
		// native `disabled` attribute above is what stops the browser
		// dispatching. That used to be a hook-slot argument (see
		// web/ui/button.go); here it is simply the simpler shape, since
		// html.OnClick registers no hook.
		if onSelect != nil {
			opts = append(opts, html.OnClick(func() {
				if disabled {
					return
				}
				onSelect()
				if openChange != nil {
					openChange(false)
				}
			}))
		}
		itemNodes = append(itemNodes, h.Button(opts, resolve(p.T, it.LabelKey)))
	}
	return itemNodes
}
