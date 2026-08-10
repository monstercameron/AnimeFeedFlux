//go:build js && wasm

// wiring.go is this package's wiring/registration entry point — the
// counterpart to web/pages/{generate,settings}/deps.go's Init +
// self-registering init(), which this package never had (PLAN.md §12.1/
// §12.2 built /login and /recover as pure components taking
// LoginPageProps/RecoverPageProps, but nothing ever constructed those
// props with a real client or registered either page with web/shell —
// this file closes that gap without touching login.go/recover.go
// themselves, per this task's hard rule).
package auth

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// loginClient/recoverClient are the package-level "set once at boot, read
// on every render" vars Init installs into — the same shape web/shell/
// app.go's own unexported `conn` uses, and what generate/settings' Init
// does for their own Deps.
var (
	loginClient   LoginClient
	recoverClient RecoverClient
)

// Init installs the live control-plane clients renderLogin/renderRecover
// hand to LoginPage/RecoverPage. Call once, from the composition root
// (web/main.go), before the operator can reach /login or /recover — safe
// to call again later since both render functions read these vars fresh
// on every render, same guarantee generatepage.Init/settings.Init
// document for their own Deps.
//
// login and recover are typically the SAME value (web/wsconn.Conn.Auth
// satisfies both LoginClient and RecoverClient structurally — see
// client.go), passed as two parameters rather than one so a caller could
// still supply distinct fakes in a future test without this package
// inventing its own narrower "AuthClient" union type.
func Init(login LoginClient, recoverC RecoverClient) {
	loginClient = login
	recoverClient = recoverC
}

// renderLogin is /login's registered page body: LoginPage plus the one
// piece of session bookkeeping login.go's own doc comment says belongs to
// whoever mounts it — translating a successful Login RPC into the shared
// D-FLOW transition (ANON -> AUTH) via shell.ApplyEvent, which login.go
// calls BEFORE navigating away (see LoginPageProps.OnSuccess's doc
// comment), so the route guard already sees AUTH by the time nav.Replace
// lands on /generate.
func renderLogin() ui.Node {
	if loginClient == nil {
		return ui.CreateElement(renderAuthNotWired, nil)
	}
	return LoginPage(LoginPageProps{
		Client: loginClient,
		OnSuccess: func(*affv1.Session) {
			shell.ApplyEvent(appstate.EvLoginSuccess)
		},
	})
}

// renderRecover is /recover's registered page body: RecoverPage plus the
// two D-FLOW transitions recover.go's own doc comment assigns to whoever
// mounts it — OnElevated (ANON -> ELEVATED, the moment a recovery code is
// accepted, so the guard starts enforcing D1-11 immediately) and
// OnComplete (ELEVATED -> ANON, once the chosen elevated action finishes
// and the server has already ended the elevated session — see doc.go
// assumption #3: recovery "forces a full re-login").
func renderRecover() ui.Node {
	if recoverClient == nil {
		return ui.CreateElement(renderAuthNotWired, nil)
	}
	return RecoverPage(RecoverPageProps{
		Client: recoverClient,
		OnElevated: func(*affv1.Session) {
			shell.ApplyEvent(appstate.EvRecoveryCodeAccepted)
		},
		OnComplete: func() {
			shell.ApplyEvent(appstate.EvRecoveryComplete)
		},
	})
}

// renderAuthNotWired covers the one case Init genuinely cannot avoid: the
// control plane's initial dial failed outright (web/shell.Mount's
// DISCONNECTED fallback), so the composition root never had a real
// AuthServiceClient to hand this package (see web/main.go's wirePages —
// it deliberately does not call auth.Init with a nil conn). Per this
// task's brief ("do not fabricate a Deps value to make it compile"), this
// renders an honest degraded state instead of risking a nil-interface
// panic three frames deep in a form submit handler.
//
// A real GWC component (mounted via ui.CreateElement above, never called
// as a plain func) because gwci18n.UseI18n() may only run inside an
// active render. There is no dedicated auth.* "not wired" catalogue key
// (unlike generate.notWired/settings.notWired.message — this package has
// no Translator/Deps seam of its own, per doc.go), so this reuses
// common.state.disconnected, which is both true (the control plane really
// is unreachable) and already registered in the catalogue.
func renderAuthNotWired() ui.Node {
	t := gwci18n.UseI18n().NS(afi18n.NSCommon)
	return h.Div(
		h.ClassStr("af-auth-page af-auth-page--not-wired"),
		h.P(h.Text(t.T(afi18n.KeyStateDisconnected))),
	)
}

// init self-registers both of this package's pages with the shell's
// router (web/shell/pages.go: "RegisterPage... Safe to call any time
// before or after Mount"), matching the pattern generatepage/settings
// already use — so blank-importing this package is sufficient for both
// routes to exist. It does NOT call Init; until the composition root does,
// renderLogin/renderRecover show the not-wired state above rather than a
// blank page or a nil-client panic.
func init() {
	shell.RegisterPage("/login", renderLogin)
	shell.RegisterPage("/recover", renderRecover)
}
