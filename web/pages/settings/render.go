//go:build js && wasm

package settings

import (
	"context"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// Section is one of the six PLAN.md §12.5 sections. No nested navigation
// (D-FLOW: "three pages do not need wayfinding") — within /settings these
// are flat tabs, not a tree.
type Section int

const (
	SectionSecurity Section = iota
	SectionProvider
	SectionGeneration
	SectionPublishing
	SectionData
	SectionAbout
)

func (s Section) titleKey() string {
	switch s {
	case SectionSecurity:
		return "settings.nav.security"
	case SectionProvider:
		return "settings.nav.provider"
	case SectionGeneration:
		return "settings.nav.generation"
	case SectionPublishing:
		return "settings.nav.publishing"
	case SectionData:
		return "settings.nav.data"
	case SectionAbout:
		return "settings.nav.about"
	default:
		return "settings.nav.unknown"
	}
}

var allSections = []Section{
	SectionSecurity, SectionProvider, SectionGeneration,
	SectionPublishing, SectionData, SectionAbout,
}

// Render is /settings' page body, registered with web/shell via
// shell.RegisterPage in deps.go's init(). It owns exactly one piece of
// state — which section tab is active — everything else lives in each
// section's own component (a real GWC component per section, mounted
// conditionally, so switching tabs unmounts one component and mounts
// another rather than changing which hooks a single persistent fiber
// calls — see doc.go / GWC discipline: hooks must stay unconditional and
// positional WITHIN one component's fiber).
func Render() ui.Node {
	active := ui.UseState(SectionSecurity)

	if !wired {
		return renderNotWired()
	}

	// Plain closures, not ui.UseEvent, inside this loop — see renderKebab's
	// doc comment for why a fiber hook per loop iteration is avoided here.
	tabs := make([]ui.Node, 0, len(allSections))
	for _, s := range allSections {
		s := s
		tabs = append(tabs, h.Button(
			h.Type("button"),
			h.ClassStr(h.ClassMap(map[string]bool{
				"af-settings-tab":         true,
				"af-settings-tab--active": active.Get() == s,
			})),
			h.OnClick(func() { active.Set(s) }),
			h.Text(t(s.titleKey())),
		))
	}

	return h.Div(
		h.ClassStr("af-settings"),
		h.H1(h.Text(t("settings.title"))),
		h.Nav(h.ClassStr("af-settings-nav"), tabs),
		h.Div(h.ClassStr("af-settings-panel"), renderActiveSection(active.Get())),
	)
}

func renderActiveSection(active Section) ui.Node {
	switch active {
	case SectionSecurity:
		return ui.CreateElement(renderSecurity)
	case SectionProvider:
		return ui.CreateElement(renderProvider)
	case SectionGeneration:
		return ui.CreateElement(renderGeneration)
	case SectionPublishing:
		return ui.CreateElement(renderPublishing)
	case SectionData:
		return ui.CreateElement(renderData)
	case SectionAbout:
		return ui.CreateElement(renderAbout)
	default:
		return h.Fragment()
	}
}

// --- Shared building blocks used by every section ---------------------

// screenWrapper renders body when state is Populated, and the matching
// loading/empty/error/disabled/disconnected placeholder otherwise
// (TODOS.md D-FLOW's six-state matrix, applied uniformly per PLAN.md
// §12.6: "loading, empty, and error states are designed for every list").
func screenWrapper(state ScreenState, err error, body ui.Node) ui.Node {
	switch state {
	case ScreenLoading:
		return h.Div(h.ClassStr("af-state af-state--loading"), h.Text(t("common.state.loading")))
	case ScreenEmpty:
		return h.Div(h.ClassStr("af-state af-state--empty"), h.Text(t("common.state.empty")))
	case ScreenError:
		msg := t("common.state.error")
		if err != nil {
			msg = t("common.state.errorDetail", err.Error())
		}
		return h.Div(h.ClassStr("af-state af-state--error"), h.Text(msg))
	case ScreenDisabledWithReason:
		return h.Div(h.ClassStr("af-state af-state--disabled"), h.Text(t("common.state.disabled")))
	case ScreenDisconnected:
		return h.Div(h.ClassStr("af-state af-state--disconnected"), h.Text(t("common.state.disconnected")))
	default:
		return body
	}
}

// kebabItem/kebabProps back the "destructive actions behind a ⋯, never a
// primary button" affordance (PLAN.md §12.6). renderKebab is a real GWC
// component (mounted via ui.CreateElement at each call site, never
// called as a plain helper) precisely so its own UseState call stays
// unconditional and positional within ITS OWN fiber regardless of how
// its parent chooses to mount/unmount it — see doc.go's GWC-discipline
// note. Item click handlers are plain closures (not wrapped in
// ui.UseEvent) inside the render loop below: html.OnClick/toHandler
// accept a plain func directly (confirmed against the pinned v5.0.1
// source, html/sugar.go's toHandler), and ui.UseEvent per loop iteration
// would itself be a hooks-in-a-loop hazard this file is specifically
// trying to avoid.
type kebabItem struct {
	label   string
	onClick func()
	danger  bool
}

type kebabProps struct {
	Items []kebabItem
}

func renderKebab(props kebabProps) ui.Node {
	open := ui.UseState(false)

	menuItems := make([]ui.Node, 0, len(props.Items))
	for _, it := range props.Items {
		it := it
		menuItems = append(menuItems, h.Li(
			h.Button(
				h.Type("button"),
				h.ClassStr(h.ClassMap(map[string]bool{
					"af-kebab-item":         true,
					"af-kebab-item--danger": it.danger,
				})),
				h.OnClick(func() {
					open.Set(false)
					it.onClick()
				}),
				h.Text(it.label),
			),
		))
	}

	return h.Div(
		h.ClassStr("af-kebab"),
		h.Button(
			h.Type("button"),
			h.ClassStr("af-kebab-trigger"),
			h.OnClick(func() { open.Set(!open.Get()) }),
			h.Text(t("common.actions.more")),
		),
		h.Show(open.Get(), h.Ul(h.ClassStr("af-kebab-menu"), menuItems)),
	)
}

func kebabMenu(items []kebabItem) ui.Node {
	return ui.CreateElement(renderKebab, kebabProps{Items: items})
}

// confirmModalProps/renderConfirmModal is the typed-confirmation gate for
// irreversible actions (PLAN.md §12.6). Always mounted by its caller
// (never conditionally included in the tree) with Visible toggling
// display via h.Show — mirrors web/shell/expiry.go's renderExpiryModal
// exactly, for the same reason: a component's own hooks (typed's
// UseState here) must run every time IT renders, so the mount/unmount
// decision belongs to the parent choosing whether to call CreateElement
// at all (never done here) rather than to a condition inside this
// component's own body.
type confirmModalProps struct {
	Visible   bool
	PromptKey string
	Word      string
	OnConfirm func()
	OnCancel  func()
}

func renderConfirmModal(props confirmModalProps) ui.Node {
	typed := ui.UseState("")
	matches := ConfirmationMatches(typed.Get(), props.Word)

	return h.Show(props.Visible, h.Div(
		h.ClassStr("af-modal af-modal--visible"),
		h.Div(
			h.ClassStr("af-modal-body"),
			h.P(h.Text(t(props.PromptKey))),
			h.P(h.Text(t("common.confirm.typeToConfirm", props.Word))),
			h.Input(
				h.Type("text"),
				h.Value(typed.Get()),
				h.OnInput(func(e ui.InputEvent) { typed.Set(e.GetValue()) }),
			),
			h.Div(
				h.ClassStr("af-modal-actions"),
				h.Button(h.Type("button"), h.OnClick(func() {
					typed.Set("")
					props.OnCancel()
				}), h.Text(t("common.actions.cancel"))),
				h.Button(
					h.Type("button"),
					h.DisabledIf(!matches),
					h.OnClick(func() {
						if !matches {
							return
						}
						typed.Set("")
						props.OnConfirm()
					}),
					h.Text(t("common.actions.confirm")),
				),
			),
		),
	))
}

func confirmModal(props confirmModalProps) ui.Node {
	return ui.CreateElement(renderConfirmModal, props)
}

// bgContext returns a fresh, unbounded context for a one-shot RPC fired
// from an event handler. Section components use this rather than a
// shared context because there is no per-render request context to hang
// one off in a WASM SPA the way an HTTP handler would have — mirrors the
// pattern web/shell/app.go uses for its own boot-time RPC (there it
// bounds with a timeout; mutation calls here rely on the RPC's own
// deadline behavior, consistent with the rest of this app's RPC calls
// which do not thread per-call timeouts through the UI layer either).
func bgContext() context.Context {
	return context.Background()
}
