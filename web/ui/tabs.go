package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// Tab is one tab. LabelKey is an i18n key (D0-22). PanelID must match the id
// on the panel element the tab controls (aria-controls/aria-labelledby).
type Tab struct {
	ID       string
	LabelKey string
	PanelID  string
}

// TabsProps configures Tabs. It renders ONLY the tablist — callers render
// each tab's panel themselves (each with role="tabpanel",
// aria-labelledby=tab.ID, id=tab.PanelID, hidden when not ActiveID), which
// keeps Tabs usable for panels of any shape (PLAN.md §12.4's Runs/Items
// history tabs, in particular).
type TabsProps struct {
	T        T
	ID       string
	LabelKey string // accessible name for the tablist as a whole
	Tabs     []Tab
	ActiveID string
	OnChange func(id string)
}

// Tabs renders a WAI-ARIA tablist with roving-tabindex arrow-key navigation
// (via gwcui.UseCompositeNavigation) and correct tab/tabpanel role wiring.
func Tabs(p TabsProps) Node {
	items := make([]gwcui.CompositeItem, 0, len(p.Tabs))
	activeIndex := 0
	for i, t := range p.Tabs {
		items = append(items, gwcui.CompositeItem{ID: t.ID, Text: resolve(p.T, t.LabelKey)})
		if t.ID == p.ActiveID {
			activeIndex = i
		}
	}
	nav := gwcui.UseCompositeNavigation(items, gwcui.CompositeNavigationOptions{
		Orientation:  "horizontal",
		Loop:         true,
		InitialIndex: activeIndex,
	})

	tabNodes := make([]Node, 0, len(p.Tabs))
	for i, t := range p.Tabs {
		isActive := t.ID == p.ActiveID
		tabIndex := -1
		if i == nav.ActiveIndex() {
			tabIndex = 0
		}
		rules := []css.Rule{
			css.Display.InlineFlex,
			css.Items.Center,
			css.Padding(tokens.Space(2)),
			css.PaddingX(tokens.Space(4)),
			css.Raw("border", "none"),
			css.Raw("border-bottom", "2px solid transparent"),
			css.Bg(css.Transparent),
			css.TextColor(tokens.Color(tokens.RoleTextMuted)),
			css.FontSize(tokens.FontSize(tokens.TextBase)),
			css.FontWeight.Medium,
			bodyFont(),
			css.Cursor.Pointer,
			transitionColors(),
		}
		if isActive {
			rules = append(rules,
				css.TextColor(tokens.Color(tokens.RoleText)),
				css.Raw("border-bottom", "2px solid "+string(tokens.Color(tokens.RoleAccent))),
			)
		}
		rules = append(rules, focusVisible()...)

		id := t.ID
		index := i
		onClick := p.OnChange
		tabNodes = append(tabNodes, h.Button(
			html.ID(id),
			html.Type("button"),
			html.Role("tab"),
			html.TabIndex(tabIndex),
			html.Aria("selected", boolStr(isActive)),
			html.Aria("controls", t.PanelID),
			css.Class(rules),
			html.OnClick(func() {
				nav.SetActive(index)
				if onClick != nil {
					onClick(id)
				}
			}),
			resolve(p.T, t.LabelKey),
		))
	}

	return h.Div(
		html.ID(p.ID),
		html.Role("tablist"),
		html.Aria("label", resolve(p.T, p.LabelKey)),
		html.OnKeyDown(func(e gwcui.KeyboardEvent) { nav.OnKeyDown(e) }),
		css.Class([]css.Rule{
			css.Display.Flex,
			css.Gap(tokens.Space(1)),
			css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		}),
		tabNodes,
	)
}
