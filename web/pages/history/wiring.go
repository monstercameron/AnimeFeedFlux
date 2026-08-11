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
	Feeds FeedsClient
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

	// D0-08: this page was previously never registering a dirty check at
	// all, so a session expiry while mid-edit on the Items tab's editor
	// (forms_ui.go's title/summary/body/... ui.UseState fields) silently
	// dropped to ANON, discarding the in-progress edit with no warning.
	// The precise per-field predicate generate/render.go's DraftDirty uses
	// (comparing a live draft against its last-loaded snapshot) is not
	// reachable from this file without editing forms_ui.go/items_ui.go/
	// root.go, all out of this change's allowed paths — their edit-form
	// state lives entirely in those components' own local hooks, which a
	// sibling file has no way to read. Registered here is the conservative
	// substitute available at this boundary: while /history is mounted and
	// wired, treat it as potentially dirty, so every session expiry holds
	// and shows the confirmation modal rather than ever silently
	// discarding an open edit. The tradeoff is an occasional unnecessary
	// prompt (e.g. expiry while only browsing the read-only Runs tab) in
	// exchange for D0-08's actual requirement — never lose work silently —
	// which this satisfies exactly, just at page granularity instead of
	// field granularity. Registered every render, matching
	// generate/render.go's own DraftDirty registration, so it always
	// reflects whichever page most recently rendered (see
	// web/shell/session.go's dirtyCheck doc comment).
	shell.RegisterDirtyCheck(func() bool {
		return wired
	})

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
		Feeds:          deps.Feeds,
		T:              deps.T,
		Ready:          sess.Get() == appstate.Auth || sess.Get() == appstate.Killed,
		Disconnected:   disconnected,
		DisabledReason: disabledReason,
	})
}

// renderNotWired is shown instead of a blank page or a nil-client panic
// when Init has not been called yet (or was called with an incomplete
// Deps) — mirrors generatepage.renderNotWired/settings.renderNotWired.
// Routed through fallbackCatalog rather than a literal English string, so
// the wording lives in the catalogue with every other string.
//
// This comment used to say the opposite — that no "history.notWired" entry
// existed and that rendering the raw key was therefore correct per D6-07.
// D6-07 describes what a MISSING key degrades to, not a design to aim for,
// and generate.notWired/settings.notWired.message both carried real copy for
// the identical state. The entry was added 2026-08-10 (web/i18n/
// keys_history.go) after web/i18n's new call-site test flagged this as the
// last key in the app referenced by a page and defined nowhere.
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
