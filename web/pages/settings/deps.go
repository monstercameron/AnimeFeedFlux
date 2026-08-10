//go:build js && wasm

package settings

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// Deps is the wiring seam doc.go describes: everything this page needs
// from the outside world, injected rather than reached for directly,
// since this package cannot import web/wsconn (see doc.go's "wiring seam
// this package could NOT close" section).
type Deps struct {
	Auth       AuthClient
	System     SystemClient
	Feed       FeedClient
	I18n       Translator
	Formatters Formatters
}

// deps is the package-level variable Init installs into — the same
// "js-only package var set once at boot, read everywhere" shape
// web/shell/app.go uses for its own `conn`.
var deps Deps

// wired reports whether Init has supplied real clients. Until it has,
// Render shows renderNotWired instead of attempting calls against nil
// interfaces.
var wired bool

// Init installs deps and marks the page ready to render its real content.
// Call once, from wherever web/main.go (or a future wave) constructs the
// control-plane connection and the real i18n catalogue. Safe to call
// before or after the router mounts this page, same guarantee
// shell.RegisterPage documents for page bodies generally.
func Init(d Deps) {
	if d.I18n == nil {
		d.I18n = DefaultTranslator
	}
	if d.Formatters == nil {
		d.Formatters = DefaultFormatters
	}
	deps = d
	wired = d.Auth != nil && d.System != nil && d.Feed != nil
}

func init() {
	shell.RegisterPage("/settings", Render)
}

// t is a package-local shorthand so section files read `t("settings.x")`
// instead of `deps.I18n.T("settings.x")` at every call site.
func t(key string, args ...any) string {
	tr := deps.I18n
	if tr == nil {
		tr = DefaultTranslator
	}
	return tr.T(key, args...)
}

// fmts is the same shorthand for Formatters.
func fmts() Formatters {
	if deps.Formatters == nil {
		return DefaultFormatters
	}
	return deps.Formatters
}

// renderNotWired is what Render shows while wired is false (doc.go's
// documented gap): the six-state matrix's own error treatment, not a
// blank page or a panic.
func renderNotWired() ui.Node {
	return h.Div(
		h.ClassStr("af-settings af-settings--error"),
		h.H2(h.Text(t("settings.title"))),
		h.P(h.Text(t("settings.notWired.message"))),
	)
}
