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
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
	"github.com/monstercameron/AnimeFeedFlux/web/wsconn"
)

// sessionTicketHeader MUST match internal/rpc/auth.go's SessionTicketHeader
// constant. Duplicated rather than imported for the same reason
// web/wsconn/ticket.go duplicates internal/bridge/server.go's
// ticketQueryParam: internal/rpc is not part of a GOOS=js/wasm build (it
// pulls in the SQLite driver, the store package, and the rest of the server
// dependency graph), so there is no Go import that could share this
// constant across the client/server boundary.
const sessionTicketHeader = "x-aff-login-ticket"

// keyBackoffCleared is the completion-side counterpart to the existing
// common.backoffNotice key: announced once (via ShouldAnnounceBackoffCleared)
// when the estimated backoff window elapses, so a screen-reader user who
// heard "try again in 8s" also hears when that changed rather than having
// to keep re-focusing the submit button to find out. Same missing-catalogue
// situation as backoff_display.go's keyConnectionUnreachable — not yet
// declared in web/i18n.
//
// It lives here rather than beside that constant because its only callers
// (login.go, recover.go) are `js && wasm`, and an untagged declaration is
// dead code in the host build that `staticcheck ./...` — which the
// pre-commit hook runs with the host toolchain — reports as U1000.
const keyBackoffCleared = "backoffCleared"

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
	loginClient = ticketRedeemingLoginClient{inner: login}
	recoverClient = ticketRedeemingRecoverClient{inner: recoverC}
}

// ticketRedeemingLoginClient wraps a real LoginClient so a successful Login
// call also completes the WebSocket-only login flow §3/PLAN.md's "one
// bridge, no HTTP side door" now requires: internal/bridge's ServeHTTP
// allows the ANONYMOUS upgrade this page's socket dials with (no session
// cookie exists yet the first time an operator visits /login), and
// internal/rpc's interceptor confines that anonymous connection to exactly
// Login and RecoverWithCode — so Login succeeding is the ONLY moment this
// page ever has a ticket to redeem. login.go itself (off limits to this
// change) calls Client.Login with no CallOptions of its own; this wrapper
// is what attaches grpc.Header(&header) so the ticket
// AuthServer.issueSessionCredential set (internal/rpc/auth.go's
// SessionTicketHeader) is actually observable here, without login.go having
// to know any of this exists.
type ticketRedeemingLoginClient struct {
	inner LoginClient
}

func (c ticketRedeemingLoginClient) Login(ctx context.Context, in *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
	var header metadata.MD
	resp, err := c.inner.Login(ctx, in, append(opts, grpc.Header(&header))...)
	if err != nil {
		return resp, err
	}
	redeemTicketIfPresent(ctx, header)
	return resp, err
}

// ticketRedeemingRecoverClient is ticketRedeemingLoginClient's sibling for
// RecoverWithCode — PLAN.md §4/§12.2 lists it alongside Login as the other
// call an anonymous, cookie-less connection may reach
// (interceptor.go's noSessionMethods), and AuthServer.RecoverWithCode mints
// a ticket the exact same way Login does (both call
// issueSessionCredential). ChangePassword/ReenrollTOTP are NOT wrapped here:
// by the time either is reachable, RecoverWithCode has already redeemed a
// ticket and the operator is on an authenticated (elevated) connection with
// a real cookie, so there is nothing left for those two calls to redeem.
type ticketRedeemingRecoverClient struct {
	inner RecoverClient
}

func (c ticketRedeemingRecoverClient) RecoverWithCode(ctx context.Context, in *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	var header metadata.MD
	resp, err := c.inner.RecoverWithCode(ctx, in, append(opts, grpc.Header(&header))...)
	if err != nil {
		return resp, err
	}
	redeemTicketIfPresent(ctx, header)
	return resp, err
}

func (c ticketRedeemingRecoverClient) ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
	return c.inner.ChangePassword(ctx, in, opts...)
}

func (c ticketRedeemingRecoverClient) ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
	return c.inner.ReenrollTOTP(ctx, in, opts...)
}

// redeemTicketIfPresent runs wsconn.CompleteLogin synchronously (blocking
// the caller's Login/RecoverWithCode return) whenever header carries a
// ticket. Synchronous, not fire-and-forget, is deliberate: a successful
// redemption ends in a full page reload (wsconn.CompleteLogin ->
// ReloadPage), and letting the RPC return first would let login.go/recover.go
// (both off limits to this change) run their own OnSuccess navigation
// against a connection that is about to be torn down by that reload — an
// observable flash, not a correctness bug (the reload wins either way), but
// pointless to allow when awaiting first avoids it. A missing header (no
// SessionTicketHeader — e.g. AFF's ticket store was never wired, or this
// call reached the server over some transport other than the bridge, which
// should not happen for a browser client but is handled the same
// degraded-not-broken way internal/rpc/auth.go's issueSessionCredential
// documents) is silently a no-op: Login/RecoverWithCode still succeeded, the
// caller's existing OnSuccess flow still runs normally, and the operator
// simply stays on whatever (anonymous) connection they had — see
// web/wsconn/ticket.go's CompleteLogin doc comment for why that is the
// correct degraded behavior rather than something to error out on here. A
// redemption FAILURE (ticket rejected, network hiccup) is logged and
// likewise swallowed for the same reason: Login itself already succeeded,
// and the existing D0-10 guard machinery (web/wsconn's ErrDisconnected)
// already makes every subsequent RPC on the still-anonymous connection fail
// honestly rather than silently.
func redeemTicketIfPresent(ctx context.Context, header metadata.MD) {
	values := header.Get(sessionTicketHeader)
	if len(values) == 0 || values[0] == "" {
		return
	}
	if err := wsconn.CompleteLogin(ctx, values[0]); err != nil {
		slog.Warn("auth: redeeming login ticket failed; connection remains anonymous", "error", err)
	}
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
