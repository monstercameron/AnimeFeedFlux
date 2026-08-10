//go:build js && wasm

// wiring.go is this package's wiring/registration entry point — the
// counterpart to web/pages/{generate,settings}/deps.go's Init +
// self-registering init(), which this package never had (doc.go says
// "whatever wires web/wsconn's *Conn to this page just needs to hand over
// [RunsClient/ItemsClient]", but nothing ever did, and nothing ever
// registered "/history" with web/shell either — this file closes both
// gaps without touching root.go/runs_ui.go/items_ui.go/forms_ui.go
// themselves, per this task's hard rule).
package history

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// Deps carries everything this page needs from outside itself, mirroring
// the shape generatepage.Deps/settings.Deps already use.
type Deps struct {
	Runs  RunsClient
	Items ItemsClient
	T     Catalog
}

// deps/wired are the package-level "set once at boot, read on every
// render" vars Init installs into — same shape as web/shell/app.go's own
// unexported `conn` and generatepage.deps/settings.deps.
var (
	deps  Deps
	wired bool
)

// Init installs d as this page's dependencies. Call once, from the
// composition root (web/main.go), before the operator can reach
// /history — safe to call again later since Render reads deps fresh on
// every render, there is no stale closure to invalidate.
func Init(d Deps) {
	if d.T == nil {
		d.T = fallbackCatalog{}
	}
	deps = d
	wired = d.Runs != nil && d.Items != nil
}

// fallbackCatalog mirrors generatepage/settings' own fallbackTranslator
// (D6-07: "a missing key renders the key itself... never an empty
// string") — used until Init supplies a real Catalog, and as the
// zero-configuration default so this package is never dead in the water
// before the composition root wires the real i18n bundle in.
type fallbackCatalog struct{}

func (fallbackCatalog) T(key string, _ map[string]any) string { return key }

// Render is /history's registered page body (PLAN.md §12.4, TODOS.md D3).
// It owns exactly the one piece of state root.go's own RootProps doc
// comment assigns to whoever mounts History: translating the shared
// D-FLOW session state into RootProps.Disconnected/DisabledReason, so
// History itself never has to know about web/shell's SessionAtom.
func Render() ui.Node {
	if !wired {
		return renderNotWired()
	}

	sess := state.UseAtomKey(shell.SessionAtom)
	disconnected := sess.Get() == appstate.Disconnected

	// root.go's own RootProps doc comment: "DisabledReason, non-empty,
	// means the KILLED application state ... applies here". History
	// itself is not one of the actions D-FLOW's appstate.State doc
	// restricts under KILLED ("everything except generate/sample
	// actions"), but this page's own declared contract ties
	// DisabledReason to KILLED specifically, so that is what gets
	// honored here rather than this file inventing a different meaning
	// for a field it doesn't own.
	disabledReason := ""
	if sess.Get() == appstate.Killed {
		disabledReason = deps.T.T("history.state.disabled", nil)
	}

	return ui.CreateElement(History, RootProps{
		Runs:           deps.Runs,
		Items:          deps.Items,
		T:              deps.T,
		Disconnected:   disconnected,
		DisabledReason: disabledReason,
	})
}

// renderNotWired is shown instead of a blank page or a nil-client panic
// when Init has not been called yet (or was called with an incomplete
// Deps) — mirrors generatepage.renderNotWired/settings.renderNotWired.
// Routed through fallbackCatalog rather than a literal English string:
// there is no "history.notWired" entry in the real catalogue (unlike
// generate.notWired/settings.notWired.message), so per D6-07 this
// deliberately renders the key itself rather than inventing wording here
// that would just be a second, competing catalogue.
func renderNotWired() ui.Node {
	return h.Div(
		h.ClassStr("history-page history-page--not-wired"),
		h.P(h.Text(fallbackCatalog{}.T("history.notWired", nil))),
	)
}

// init self-registers this page with the shell's router (web/shell/
// pages.go: "RegisterPage... Safe to call any time before or after
// Mount"), matching the pattern generatepage/settings already use — so
// blank-importing this package is sufficient for the route to exist. It
// does NOT call Init — until the composition root does, Render shows the
// not-wired state above rather than a blank page.
func init() {
	shell.RegisterPage("/history", Render)
}
