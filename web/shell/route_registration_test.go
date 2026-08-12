//go:build js && wasm

// route_registration_test.go is a package shell_test (external, black-box)
// test deliberately, not `package shell`: it blank-imports the four page
// packages so their own init()-time shell.RegisterPage calls run, and
// three of those four packages themselves import web/shell (for
// shell.RegisterPage/shell.SessionAtom/shell.ApplyEvent) — an internal
// `package shell` test file importing them back would be a real import
// cycle at test-build time, whereas shell_test -> {shell, auth, generate,
// history, settings} -> shell is not (shell_test is a distinct package
// from shell).
//
// This is the regression test the task brief asked for: "a test that
// enumerates the five expected routes and asserts each resolves to a real
// component rather than the placeholder." Before web/main.go's
// composition root existed, this test would have failed for /login,
// /recover, and /history (never registered at all — RegisteredPaths
// would have returned only /generate and /settings, the two pages whose
// deps.go/init() at least self-registered even though nothing ever called
// their Init). Only compiled/exercised via
// `GOOS=js GOARCH=wasm go test -c ./web/shell/...` (see
// web/wsconn/conn_js_test.go's own doc comment for why this repo's
// toolchain does not wire up a JS host to actually RUN a wasm test
// binary) — `go test ./web/...` on the host skips this file entirely via
// the build tag, same as every other js-only file in this tree.
package shell_test

import (
	"testing"

	_ "github.com/monstercameron/AnimeFeedFlux/web/pages/auth"
	_ "github.com/monstercameron/AnimeFeedFlux/web/pages/generate"
	_ "github.com/monstercameron/AnimeFeedFlux/web/pages/history"
	_ "github.com/monstercameron/AnimeFeedFlux/web/pages/settings"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// TestAllFiveRoutesRegistered asserts the fixed five-route table (PLAN.md
// §12 / web/shell/app.go's own routeTable) each have a REAL registered
// page body — i.e. would not fall through to renderPageBody's "Page not
// yet implemented" placeholder — purely from each page package's own
// init(), with no shell.Mount/Init(Deps) call involved. It intentionally
// does NOT call any page's Init: this test's whole point is to catch "the
// route exists but renders the placeholder because nothing ever
// registered it," not "the route renders correctly with live data" (that
// needs a browser, a dialed connection, and the composition root's
// wirePages — out of reach for a host-compiled test either way).
func TestAllFiveRoutesRegistered(t *testing.T) {
	// Seven, not five, since /history/:tab and /settings/:section were added —
	// both are real registered page bodies, not decoration, and a reload on
	// /settings/provider depends on this one existing.
	//
	// This assertion was stale and could not have caught anything: it is a
	// js-only test, and until scripts/coverage-wasm.sh existed nothing ever
	// COMPILED it, let alone ran it. The list it checked had been wrong since
	// the addressable-tabs work landed. Sorted, because RegisteredPaths sorts.
	want := []string{
		"/generate",
		"/history",
		"/history/:tab",
		"/login",
		"/recover",
		"/settings",
		"/settings/:section",
	}

	got := shell.RegisteredPaths()

	if len(got) != len(want) {
		t.Fatalf("RegisteredPaths() = %v (len %d), want %v (len %d) — a page package's init() is missing its shell.RegisterPage call", got, len(got), want, len(want))
	}
	for i, path := range want {
		if got[i] != path {
			t.Fatalf("RegisteredPaths()[%d] = %q, want %q (full list: got=%v want=%v)", i, got[i], path, got, want)
		}
	}
}
