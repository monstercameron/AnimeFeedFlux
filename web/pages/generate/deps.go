//go:build js && wasm

package generatepage

import (
	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// Deps carries everything this page needs from outside itself: the typed
// control-plane clients and the i18n/formatting lookups. See doc.go for
// why this indirection exists instead of a direct web/wsconn.Conn field —
// in short, Conn does not expose the five clients this page needs, and
// wiring that is out of this change's scope (web/wsconn is owned by
// another concurrent agent).
type Deps struct {
	Feed   affv1.FeedServiceClient
	Sample affv1.SampleServiceClient
	Run    affv1.RunServiceClient
	System affv1.SystemServiceClient
	Item   affv1.ItemServiceClient

	// I18n/Formatters default to DefaultTranslator/DefaultFormatters
	// (i18n.go) when left nil, so a caller that only has the RPC clients
	// ready still gets a working (if unlocalized) page rather than a nil
	// dereference.
	I18n       Translator
	Formatters Formatters
}

// deps is the package-level "set once at boot, read everywhere" var, the
// same shape web/shell/app.go uses for its own unexported `conn` — see
// that file's doc comment. Zero value (all nils) is a valid, meaningful
// state: it is exactly "not wired yet", which Render below treats as its
// own case rather than a bug.
var deps Deps

// Init installs d as this page's dependencies. Call it once, after the
// control-plane connection is established and its clients are
// constructed (e.g. affv1.NewFeedServiceClient(cc) for whatever *grpc.
// ClientConn the wiring wave exposes), before the operator navigates to
// /generate. Safe to call again later (e.g. on reconnect, if a wave
// decides clients need reconstructing against a fresh *grpc.ClientConn) —
// GWC re-renders read deps.* fresh on every render, there is no stale
// closure to invalidate.
func Init(d Deps) {
	if d.I18n == nil {
		d.I18n = DefaultTranslator
	}
	if d.Formatters == nil {
		d.Formatters = DefaultFormatters
	}
	deps = d
}

// wired reports whether Init has been called with at least the clients
// Render's panes need. Checked at the top of Render rather than letting a
// nil client panic three frames deep in a goroutine.
func wired() bool {
	return deps.Feed != nil && deps.Sample != nil
}

// init self-registers this page with the shell's router (web/shell/
// pages.go: "RegisterPage... Safe to call any time before or after
// Mount"), so blank-importing this package is sufficient for the route to
// exist. It does NOT call Init — that still needs to happen from wherever
// the control-plane clients are constructed (see doc.go's "wiring seam"
// section); until it does, Render shows the not-wired state rather than a
// blank page (PLAN.md §12.6: "an admin tool that shows a blank box on
// failure is how a stale feed goes unnoticed for a week" — the same
// principle applies to a whole page during incremental build-out, per
// web/shell/pages.go's own doc comment).
func init() {
	shell.RegisterPage("/generate", Render)
}
