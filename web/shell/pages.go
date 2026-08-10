//go:build js && wasm

package shell

import (
	"sort"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// keyPagePlaceholder is the shell.* key for the "not yet implemented" page
// placeholder (PLAN.md §12.6, D6-09). web/i18n's shell namespace is
// declared empty for this wave (see this package's report to the
// coordinator) — this constant is what needs adding there, with a Message
// whose Text interpolates {path}, e.g.:
//
//	shell.pagePlaceholder: {Text: "Page not yet implemented: {path}"}
//
// Interpolated via Runtime.T's Arguments, never fmt.Sprintf/h.Textf (§12.6:
// "interpolation through the library, never string concatenation" — a
// concatenated/Sprintf'd fragment assumes English word order).
// This used to be a stale local placeholder ("pagePlaceholder") that never
// matched anything in web/i18n's real catalogue (keys_shell.go's
// KeyShellNotImplemented, "notImplemented.placeholder"), so every
// unregistered route was silently hitting Bundle.OnMissing (a stderr log
// plus the raw "shell.pagePlaceholder" text) instead of the intended
// English sentence. Fixed while wiring the five routes, as the one other
// composition-root-adjacent bug found reading this file end to end.
const keyPagePlaceholder = afi18n.KeyShellNotImplemented

// PageRenderer builds one page's body. Later waves register the real
// /login, /generate, /history, /settings, /recover implementations via
// RegisterPage; any path without one falls back to a labeled placeholder
// so the shell never shows a blank box for a route it doesn't yet have
// content for (PLAN.md §12.6: loading/empty/error states are designed for
// every list — an admin tool that shows a blank box on failure is how a
// stale feed goes unnoticed for a week; the same "never blank" principle
// applies here to whole pages during incremental build-out).
type PageRenderer func() ui.Node

var pages = map[string]PageRenderer{}

// RegisterPage installs the real body for path. Safe to call any time
// before or after Mount — page bodies are looked up dynamically on each
// navigation, not captured at registration time.
func RegisterPage(path string, render PageRenderer) {
	pages[path] = render
}

// RegisteredPaths returns the sorted set of paths that currently have a
// real registered page body (i.e. would NOT fall through to the
// placeholder in renderPageBody). Exported so a test can assert against
// the fixed five-route table from outside this package (route_
// registration_test.go, package shell_test, blank-importing the four page
// packages so their own init()-time RegisterPage calls run) rather than
// trusting that every page package's init() actually ran just because it
// compiled — that gap ("built, tested, connected to nothing") is exactly
// what made all five routes unreachable before this change.
func RegisteredPaths() []string {
	out := make([]string, 0, len(pages))
	for p := range pages {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// renderPageBody is only ever called from inside renderShellWrapper's own
// render (see below), so gwci18n.UseI18n() here attaches to that same
// fiber — legal per the "hooks unconditional" rule because whether the
// found-page or placeholder branch runs is fixed for the lifetime of a
// given renderShellWrapper fiber (path is immutable per mount: a route
// change always tears down and remounts a new pageComponent instance via
// the router, never flips this branch under an existing fiber).
func renderPageBody(path string) ui.Node {
	if render, ok := pages[path]; ok {
		return render()
	}
	t := gwci18n.UseI18n().NS(afi18n.NSShell)
	return h.Div(
		h.ClassStr("af-placeholder"),
		h.Text(t.T(keyPagePlaceholder, gwci18n.Arguments{"path": path})),
	)
}

// shellWrapperProps carries the matched path into renderShellWrapper.
type shellWrapperProps struct {
	Path string
}

// renderShellWrapper wraps every route's page body with the banner and
// expiry modal, which must stay visible regardless of which of the five
// routes is current. GWC's nested-layout feature (router.Options{Layout:
// true} + router.UseOutlet()) is built for exactly this "one shell stays
// mounted, child routes swap" shape, but it models a parent/child PATH
// relationship — these five routes are siblings, not children of a common
// parent path, so wrapping each route's own component here is the direct
// fit rather than inventing an artificial parent route.
func renderShellWrapper(props shellWrapperProps) ui.Node {
	return h.Fragment(
		ui.CreateElement(renderBanner, nil),
		ui.CreateElement(renderExpiryModal, nil),
		renderPageBody(props.Path),
	)
}

// i18nBundle is the shell's one i18n.Bundle instance (web/i18n.NewBundle,
// D6-01/D6-02): built once at package init and handed to gwci18n.Provider
// by every route's render (see renderShellRoot) rather than constructed
// per-navigation, so OnMissing's stderr logging (D6-07) isn't duplicated
// bundle-construction noise on every route change.
var i18nBundle = afi18n.NewBundle()

// Bundle returns the shell's one gwci18n.Bundle instance — the same one
// gwci18n.Provider is mounted with in renderShellRoot — so the composition
// root (web/main.go) can build page-declared Translator/Formatters/Catalog
// adapters against the real catalogue instead of each page's own
// zero-configuration fallback. There is deliberately only ever one Bundle
// (see renderShellRoot's doc comment on why the Provider itself is
// mounted exactly once); this accessor hands out the same value, never a
// second one.
func Bundle() *gwci18n.Bundle {
	return i18nBundle
}

// renderShellRoot mounts the i18n Provider "above the router" (D0-21: "a
// provider inside a route cannot serve the shell's own reconnect banner or
// the guard's redirect notice"). This is as high in the tree as GWC v5's
// router API allows a wrapper to sit: router.Router.Mount renders directly
// into the target DOM node itself (confirmed by reading router.go's
// Mount/renderCurrentRoute) rather than being embedded as a child inside
// some outer ui.CreateElement tree this package could wrap once from
// outside — there is no "parent of the router" component slot to mount
// into. Every registered route funnels through this same pageComponent
// (including "*"), so wrapping here is the single, non-duplicated Provider
// mount point that still covers the banner and expiry modal (both live
// inside renderShellWrapper, this component's only child) on every route,
// exactly as D0-21 requires — never a second Provider mounted deeper.
func renderShellRoot(props shellWrapperProps) ui.Node {
	return gwci18n.Provider(gwci18n.ProviderProps{
		Bundle:        i18nBundle,
		CurrentLocale: afi18n.DefaultLocale,
		Child:         ui.CreateElement(renderShellWrapper, props),
	})
}

// pageComponent returns the router.Component for path — every registered
// route (including "*") renders through renderShellRoot (the i18n Provider)
// wrapping renderShellWrapper (the banner/expiry-modal/page-body shell).
func pageComponent(path string) router.Component {
	return func(router.Attrs) *router.Element {
		return ui.CreateElement(renderShellRoot, shellWrapperProps{Path: path})
	}
}
