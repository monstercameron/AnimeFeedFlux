//go:build js && wasm

package settings

import (
	"context"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// isDisconnected reports the shell's DISCONNECTED application state
// (TODOS.md D-FLOW), read via the same shell.SessionAtom every other page
// subscribes to (web/pages/generate/render.go's identical
// `state.UseAtomKey(shell.SessionAtom)` call is the precedent this
// mirrors) rather than threading a Disconnected prop through a wiring.go
// this package does not have this round. Each section component below
// calls this once, as its own unconditional/positional hook call within
// its own fiber — safe to call from multiple independent components since
// the atom is a shared global, not per-caller state.
func isDisconnected() bool {
	sess := state.UseAtomKey(shell.SessionAtom)
	return sess.Get() == appstate.Disconnected
}

// Section is one of the seven /settings sections (PLAN.md §12.5 named six;
// Appearance was added 2026-08-11 with the language selector). No nested navigation
// (D-FLOW: "three pages do not need wayfinding") — within /settings these
// are flat tabs, not a tree.
type Section int

const (
	SectionSecurity Section = iota
	SectionProvider
	SectionGeneration
	SectionPublishing
	SectionData
	// SectionAppearance sits between Data and About deliberately: the six
	// sections before it are all server state, About is a read-only report,
	// and Appearance is the only one that configures the client itself. Last
	// among the configurable sections is where a per-browser preference
	// belongs — ahead of About, which is not a setting at all.
	SectionAppearance
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
	case SectionAppearance:
		return "settings.nav.appearance"
	case SectionAbout:
		return "settings.nav.about"
	default:
		return "settings.nav.unknown"
	}
}

// tabID is this section's stable id for web/ui.Tabs' Tab.ID/ActiveID (and
// hence its roving-tabindex arrow-key model) — a separate string from
// titleKey() because a tab's identity and its display label are different
// concerns even though today they happen to correspond 1:1.
func (s Section) tabID() string {
	switch s {
	case SectionSecurity:
		return "settings-tab-security"
	case SectionProvider:
		return "settings-tab-provider"
	case SectionGeneration:
		return "settings-tab-generation"
	case SectionPublishing:
		return "settings-tab-publishing"
	case SectionData:
		return "settings-tab-data"
	case SectionAppearance:
		return "settings-tab-appearance"
	case SectionAbout:
		return "settings-tab-about"
	default:
		return "settings-tab-unknown"
	}
}

var allSections = []Section{
	SectionSecurity, SectionProvider, SectionGeneration,
	SectionPublishing, SectionData, SectionAppearance, SectionAbout,
}

// slug is the section's URL segment: /settings/provider, /settings/data.
//
// Deliberately its own short string rather than reusing tabID(): a tab id is
// a DOM id whose shape (settings-tab-provider) exists to be unique in a
// document, and putting it in the address bar would make every URL an
// implementation detail that could not be changed without breaking links.
func (s Section) slug() string {
	switch s {
	case SectionSecurity:
		return "security"
	case SectionProvider:
		return "provider"
	case SectionGeneration:
		return "generation"
	case SectionPublishing:
		return "publishing"
	case SectionData:
		return "data"
	case SectionAppearance:
		return "appearance"
	case SectionAbout:
		return "about"
	}
	return ""
}

// sectionFromSlug resolves a URL segment. An unknown slug falls back to the
// first section rather than erroring: a typo'd or stale bookmark should land
// somewhere useful, and /settings itself (no segment at all) takes this same
// path.
func sectionFromSlug(slug string) Section {
	for _, sec := range allSections {
		if sec.slug() == slug {
			return sec
		}
	}
	return SectionSecurity
}

// SectionPath is the address of one settings section. Exported so a runbook,
// a test, or another page can link at a panel by name instead of by
// reconstructing the string.
func SectionPath(s Section) string { return "/settings/" + s.slug() }

// settingsPanelID is the single shared tabpanel id every section mounts
// into — only one section is ever mounted at a time (Render swaps the
// panel's child, it does not keep all six mounted and hide the rest), so
// one id is enough for the aria-controls/aria-labelledby wiring
// web/ui.Tabs expects.
const settingsPanelID = "settings-panel"

// Render is /settings' page body, registered with web/shell via
// shell.RegisterPage in deps.go's init(). It owns exactly one piece of
// state — which section tab is active — everything else lives in each
// section's own component (a real GWC component per section, mounted
// conditionally, so switching tabs unmounts one component and mounts
// another rather than changing which hooks a single persistent fiber
// calls — see doc.go / GWC discipline: hooks must stay unconditional and
// positional WITHIN one component's fiber).
func Render() ui.Node {
	// The active section is READ FROM THE URL, not held as state.
	//
	// It used to be ui.UseState(SectionSecurity), which meant the section
	// existed only inside this component: a reload dropped you back on
	// Security no matter what you were looking at, and there was no way to
	// link anyone at a panel. Now /settings/provider IS the provider panel,
	// and /settings (no segment) resolves to the first section.
	loc := router.UseRoute()
	nav := router.UseNavigate()
	active := sectionFromSlug(loc.Param("section"))

	if !wired {
		return renderNotWired()
	}

	tabs := make([]affui.Tab, 0, len(allSections))
	tabIDToSection := make(map[string]Section, len(allSections))
	for _, s := range allSections {
		id := s.tabID()
		tabIDToSection[id] = s
		tabs = append(tabs, affui.Tab{ID: id, LabelKey: s.titleKey(), PanelID: settingsPanelID})
	}

	tabsNode := ui.CreateElement(renderSectionTabs, sectionTabsProps{
		ActiveID: active.tabID(),
		Tabs:     tabs,
		OnChange: func(id string) {
			if s, ok := tabIDToSection[id]; ok {
				// Navigate rather than set state: the URL is the source of
				// truth, so changing tabs has to change the address or the
				// two immediately disagree. Push, not replace — Back through
				// the sections you visited is the behaviour a tabbed page in
				// the address bar implies.
				nav.Navigate(SectionPath(s))
			}
		},
	})

	return h.Div(
		h.ClassStr("af-settings"),
		h.H1(h.Text(t("settings.title"))),
		tabsNode,
		h.Div(h.ID(settingsPanelID), h.Role("tabpanel"), renderActiveSection(active)),
	)
}

// sectionTabsProps/renderSectionTabs isolates web/ui.Tabs' own hook
// (gwcui.UseCompositeNavigation, called internally by affui.Tabs) into its
// own fiber via ui.CreateElement — the same reason renderKebabUI and
// renderConfirmUI below do this rather than calling the affui primitive
// directly inline: a hook-calling function embedded directly in a parent
// whose own hook count/order can change across renders (Render's early
// `if !wired` return, here) is exactly the hazard doc.go's GWC-discipline
// note warns about, and giving it its own always-mounted-the-same-way
// component sidesteps that regardless of how Render's own control flow
// changes later.
type sectionTabsProps struct {
	ActiveID string
	Tabs     []affui.Tab
	OnChange func(string)
}

func renderSectionTabs(props sectionTabsProps) ui.Node {
	return affui.Tabs(affui.TabsProps{
		T:        t,
		ID:       "settings-tabs",
		LabelKey: "settings.nav.label",
		Tabs:     props.Tabs,
		ActiveID: props.ActiveID,
		OnChange: props.OnChange,
	})
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
	case SectionAppearance:
		return ui.CreateElement(renderAppearance)
	case SectionAbout:
		return ui.CreateElement(renderAbout)
	default:
		return h.Fragment()
	}
}

// --- Shared building blocks used by every section ---------------------

// toListState maps this package's own ScreenState (screenstate.go's
// pure, host-tested six-state precedence) onto web/ui's ListState enum —
// the two are the same six states by design (D-FLOW's matrix), just owned
// by two different packages for the reason doc.go/web/ui's own doc
// comment both give: this package cannot import web/ui's *type*
// definitions into its host-testable, no-build-tag files (screenstate.go
// has no `js && wasm` tag, so it cannot depend on anything that pulls in
// GWC), so the enum is duplicated and only reconciled here, in the
// browser-only rendering layer.
func toListState(s ScreenState) affui.ListState {
	switch s {
	case ScreenLoading:
		return affui.StateLoading
	case ScreenEmpty:
		return affui.StateEmpty
	case ScreenError:
		return affui.StateError
	case ScreenDisabledWithReason:
		return affui.StateDisabledWithReason
	case ScreenDisconnected:
		return affui.StateDisconnected
	default:
		return affui.StatePopulated
	}
}

// screenWrapper renders body when state is Populated, and web/ui.StatePanel's
// matching loading/empty/error/disabled/disconnected view otherwise
// (TODOS.md D-FLOW's six-state matrix, applied uniformly per PLAN.md
// §12.6: "loading, empty, and error states are designed for every list") —
// this is the ONE consumer of every section's ComputeScreenState result, so
// adopting the shared StatePanel primitive here means every section's
// non-populated views come from the same tested implementation instead of
// six hand-rolled `<div class="af-state...">`s.
//
// No call site currently sets ScreenInputs.DisabledReason to anything but a
// presence check (see screenstate.go's own doc comment: it decides which
// STATE applies, but the raw reason text was never threaded through to
// render.go before this change either) — DisabledReasonKey below is
// therefore a generic settings.common.state.disabledGeneric message, not a
// per-panel one, an honest continuation of the previous behavior rather
// than a regression this change introduces.
// screenWrapperRetry is screenWrapper with a recovery path.
//
// PLAN.md §12.6 is explicit that a failure must be renderable and
// recoverable, and web/ui.StatePanel has carried an OnRetry field the whole
// time — set by nobody, anywhere in the app. Every error view in Settings and
// on /generate offered a message and no way out except reloading the page.
func screenWrapperRetry(scr ScreenState, err error, onRetry func(), body ui.Node) ui.Node {
	errorKey := ""
	var errorArgs []any
	if err != nil {
		errorKey = "settings.common.state.errorDetail"
		errorArgs = []any{err.Error()}
	}
	return affui.StatePanel(affui.StatePanelProps{
		T:                 t,
		State:             toListState(scr),
		ErrorKey:          errorKey,
		ErrorArgs:         errorArgs,
		DisabledReasonKey: "settings.common.state.disabledGeneric",
		OnRetry:           onRetry,
		Populated:         func() []ui.Node { return []ui.Node{body} },
	})
}

func screenWrapper(scr ScreenState, err error, body ui.Node) ui.Node {
	errorKey := ""
	var errorArgs []any
	if err != nil {
		errorKey = "settings.common.state.errorDetail"
		errorArgs = []any{err.Error()}
	}
	return affui.StatePanel(affui.StatePanelProps{
		T:                 t,
		State:             toListState(scr),
		ErrorKey:          errorKey,
		ErrorArgs:         errorArgs,
		DisabledReasonKey: "settings.common.state.disabledGeneric",
		Populated:         func() []ui.Node { return []ui.Node{body} },
	})
}

// renderKebabUI/kebabUI is the web/ui.Kebab adoption of this file's former
// hand-rolled renderKebab/kebabMenu (PLAN.md §12.6/D0-15: destructive
// actions live behind a ⋯, never a primary button). Isolated into its own
// component via ui.CreateElement for the same hook-safety reason
// sectionTabsProps/renderSectionTabs above documents: affui.Kebab's
// trigger/menu wiring is stateless from THIS file's point of view, but the
// Open/OnOpenChange state it needs still has to live in some fiber, and
// giving it its own means the call site (a section's render function) does
// not itself need an extra ui.UseState just to drive one kebab.
type kebabUIProps struct {
	ID        string
	LabelKey  string
	LabelArgs []any
	Items     []affui.KebabItem
}

func renderKebabUI(props kebabUIProps) ui.Node {
	open := ui.UseState(false)
	return affui.Kebab(affui.KebabProps{
		T:            t,
		ID:           props.ID,
		LabelKey:     props.LabelKey,
		LabelArgs:    props.LabelArgs,
		Items:        props.Items,
		Open:         open.Get(),
		OnOpenChange: func(o bool) { open.Set(o) },
	})
}

func kebabUI(props kebabUIProps) ui.Node {
	return ui.CreateElement(renderKebabUI, props)
}

// disconnectedKebabItem marks a destructive KebabItem as disabled (with the
// panel's own disconnectedNote() carrying the reason text alongside it,
// same as every section already renders) while DISCONNECTED — D0-10:
// refuse mutations while disconnected, never fail silently, never a
// keyboard-unreachable dead control (KebabItem.Disabled still renders the
// item, just non-interactive and visually dimmed — see web/ui/kebab.go).
func disconnectedKebabItem(item affui.KebabItem, disconnected bool) affui.KebabItem {
	item.Disabled = disconnected
	return item
}

// confirmUIProps/renderConfirmUI/confirmUI is the web/ui.Confirm adoption
// of this file's former hand-rolled confirmModalProps/renderConfirmModal —
// PLAN.md §12.6's typed-confirmation gate for irreversible actions
// (revoke-all-sessions, regenerate-recovery-codes, TOML import). Always
// mounted by its caller (never conditionally included in the tree) with
// Open toggling AccessibleOverlay's own visibility — mirrors this file's
// previous convention exactly, and web/shell/expiry.go's renderExpiryModal,
// for the same reason: a component's own hooks (the typed-input UseState
// here) must run every time IT renders, so the mount/unmount decision
// belongs to the parent choosing whether to call CreateElement at all
// (never done here) rather than to a condition inside this component's own
// body.
type confirmUIProps struct {
	ID             string
	TitleKey       string
	MessageKey     string
	MessageArgs    []any
	RequiredPhrase string
	Open           bool
	Busy           bool
	OnConfirm      func()
	OnCancel       func()
}

func renderConfirmUI(props confirmUIProps) ui.Node {
	typed := ui.UseState("")
	onConfirm := props.OnConfirm
	onCancel := props.OnCancel
	return affui.Confirm(affui.ConfirmProps{
		T:              t,
		ID:             props.ID,
		TitleKey:       props.TitleKey,
		MessageKey:     props.MessageKey,
		MessageArgs:    props.MessageArgs,
		RequiredPhrase: props.RequiredPhrase,
		Typed:          typed.Get(),
		OnTypedChange:  func(v string) { typed.Set(v) },
		OnConfirm: func() {
			typed.Set("")
			if onConfirm != nil {
				onConfirm()
			}
		},
		OnCancel: func() {
			typed.Set("")
			if onCancel != nil {
				onCancel()
			}
		},
		Open: props.Open,
		Busy: props.Busy,
	})
}

func confirmUI(props confirmUIProps) ui.Node {
	return ui.CreateElement(renderConfirmUI, props)
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
