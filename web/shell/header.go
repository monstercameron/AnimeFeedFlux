//go:build js && wasm

package shell

import (
	"context"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// header.go builds the app chrome docs/design-direction.md calls for and
// this app never had: a top bar carrying the brand lockup, the three authed
// destinations, and a sign-out control (this task's job #2). Before this
// file, renderShellWrapper (pages.go) went straight from the DISCONNECTED
// banner into the raw page body — there was no header, no navigation, no
// identity anywhere in the shell, on any route.
//
// # Keys this file needs that web/i18n does not have yet
//
// Every user-visible string below is a NEW shell.* key this package does
// not own (web/i18n is off this task's allowed-path list, same restriction
// pages.go/banner.go/expiry.go already document for their own keys). These
// render through Runtime.T like every other shell string, so a missing
// catalogue entry degrades to Bundle.OnMissing's fallback text rather than
// silently going untranslated forever — but the real English copy still
// needs adding to web/i18n/keys_shell.go's shellMessages map by whoever
// owns that file. The full list, reported in this task's summary:
//
//	header.brand.label       "AnimeFeedFlux Admin"
//	header.brand.homeLabel   "AnimeFeedFlux Admin — go to Generate"
//	header.nav.label         "Primary navigation"
//	header.nav.generate      "Generate"
//	header.nav.history       "History"
//	header.nav.settings      "Settings"
//	header.signOut           "Sign out"
//	header.signOut.busy      "Signing out…"
//	header.signOut.error     "Sign out failed — try again."
const (
	keyHeaderBrandLabel     = "header.brand.label"
	keyHeaderBrandHomeLabel = "header.brand.homeLabel"
	keyHeaderNavLabel       = "header.nav.label"
	keyHeaderNavGenerate    = "header.nav.generate"
	keyHeaderNavHistory     = "header.nav.history"
	keyHeaderNavSettings    = "header.nav.settings"
	keyHeaderSignOut        = "header.signOut"
	keyHeaderSignOutBusy    = "header.signOut.busy"
	keyHeaderSignOutError   = "header.signOut.error"
)

// The four header.theme.* keys, and the renderThemeControl that rendered
// them, were removed 2026-08-11 when the theme moved into Settings →
// Appearance beside the new language selector (settings.appearance.*).
//
// Left as a note rather than silently deleted, because the comment that
// stood here argued the opposite and the argument was a good one: the theme
// control was in the header specifically so that an operator working at
// night could reach it BEFORE signing in, since /settings is behind the
// session. That cost is real and is now paid — /login and /recover render in
// whatever theme and language were last stored, with no way to change either
// from those screens. The trade accepted in exchange is one preference with
// one home: theme and language are the same kind of setting, and splitting
// them across two surfaces (one in the header, one in Settings) is the shape
// this repo already rejected when it deleted the duplicate sign-out button.
// If the pre-auth case turns out to matter more than the symmetry, the
// answer is to render the Appearance controls on the auth routes too, not to
// put a second theme switch back in the header.

// headerNavItem is one of the three authed destinations (PLAN.md §12: "no
// nested navigation, no hamburger, no breadcrumb" — three links need no
// more wayfinding than three links). The array length (3) is load-bearing:
// renderHeader below calls ui.UseEvent exactly len(headerNavItems) times,
// every render, unconditionally — see that function's doc comment.
type headerNavItem struct {
	path string
	key  string
}

var headerNavItems = [3]headerNavItem{
	{path: "/generate", key: keyHeaderNavGenerate},
	{path: "/history", key: keyHeaderNavHistory},
	{path: "/settings", key: keyHeaderNavSettings},
}

// headerSignOutPhase mirrors web/pages/settings/wiring.go's renderSignOut of
// the same name (that file's own doc comment explains why the only
// PRE-EXISTING sign-out control lives inside Settings' Security section
// rather than the shell chrome: whoever wrote it could not reach the shell
// package at all). This header adds a second, always-visible sign-out entry
// point — the one this task's brief asks for — duplicating the small state
// machine rather than importing web/pages/settings (out of this package's
// allowed paths, and the wrong direction of dependency besides: shell must
// not import a page package).
type headerSignOutPhase int

const (
	headerSignOutIdle headerSignOutPhase = iota
	headerSignOutInFlight
)

// isModifiedClick reports whether a click event should be left to the
// browser's default anchor handling (new tab, new window, "open in
// background", ...) rather than intercepted for client-side routing —
// anything but a plain unmodified left click.
func isModifiedClick(e ui.MouseEvent) bool {
	return e.GetButton() != 0 || e.GetCtrlKey() || e.GetMetaKey() || e.GetShiftKey()
}

// renderHeader is the shell's top bar, mounted once per route (as the first
// child of renderShellWrapper, ahead of the DISCONNECTED banner and the
// page body) so identity and navigation stay visible across every
// navigation the way the banner and expiry modal already do.
//
// # Hooks are called unconditionally, every render — see this package's own
// warning on session.go/expiry.go about GWC's positional hook slots
//
// ui.UseEvent allocates one hook slot per call site the FIRST time it runs
// for a given fiber, then re-points that same slot at the latest closure on
// every later render (its own doc comment: "created once per hook slot").
// That only works if the same call sites run, in the same order, on every
// render of this fiber — branching a UseEvent call on session state (e.g.
// "only wrap the sign-out handler when interactive") would change the hook
// count between an AUTH render and the next DISCONNECTED render and corrupt
// every hook slot after it. So every UseEvent below — the brand link, all
// three nav items (the fixed-length headerNavItems array, looped
// unconditionally), and sign-out — runs on every render regardless of
// which state the component is in; only the RETURNED NODES branch on state,
// after all hooks have already run. Never move one of these calls behind an
// `if`.
//
// # What each D-FLOW state shows, and why
//
//   - ANON: the mark alone. There is nowhere authed to navigate to, and no
//     session to sign out of (D-FLOW: ANON -> only /login, /recover).
//   - ELEVATED: the mark alone too. guard.Decide refuses ELEVATED on
//     /generate, /history, and /settings unconditionally (D1-11) — a header
//     that offered those three links here would be lying about what
//     clicking them does. There is no ordinary session to sign out of
//     either (ELEVATED is a 10-minute recovery window, not AUTH).
//   - AUTH / KILLED: the full lockup, all three links live, sign-out live.
//     KILLED only restricts what /generate's own actions let you do
//     (appstate.go: "everything except generate/sample actions") — it does
//     not restrict which of the three routes you can reach, so the header
//     treats it exactly like AUTH.
//   - DISCONNECTED: the lockup (still linked home — appstate.CanNavigate
//     allows moving between the three authed routes while disconnected,
//     DISCONNECTED restricts what a page lets you DO, not which of the
//     three you can look at) and the three destinations stay VISIBLE, but
//     render inert: plain text, not links, `aria-disabled="true"`,
//     unfocusable, no hover/active styling. This is this task's brief
//     verbatim ("without the nav appearing to work") — the reconnect
//     banner already told the truth about connectivity; the header must
//     not contradict it by offering controls that look like they work.
//     Sign-out is a real `<button disabled>` for the same reason, and for a
//     second, harder one: the Logout RPC would be refused outright by
//     wsconn's guarded client (ErrDisconnected) even if clicked, so
//     disabling it is not just honest chrome, it is the true state of the
//     control.
func renderHeader() ui.Node {
	sess := state.UseAtomKey(SessionAtom)
	loc := router.UseRoute()
	t := gwci18n.UseI18n().NS(afi18n.NSShell)
	nav := router.UseNavigate() // no hook state of its own (verified against v5.0.1 source); safe regardless, called unconditionally like every hook below

	brandClick := ui.UseEvent(func(e ui.MouseEvent) {
		if isModifiedClick(e) {
			return
		}
		e.PreventDefault()
		nav.Navigate("/generate")
	})

	var navClicks [len(headerNavItems)]ui.Handler
	for i, item := range headerNavItems {
		path := item.path
		navClicks[i] = ui.UseEvent(func(e ui.MouseEvent) {
			if isModifiedClick(e) {
				return
			}
			e.PreventDefault()
			nav.Navigate(path)
		})
	}

	phase := ui.UseState(headerSignOutIdle)
	signOutErr := ui.UseState(error(nil))
	handleSignOut := ui.UseEvent(func() {
		if phase.Get() == headerSignOutInFlight {
			return
		}
		conn := Conn()
		if conn == nil {
			// Mount's own DISCONNECTED fallback (app.go) means this can
			// happen even before any RPC is attempted — the initial dial
			// failed outright. Same ErrDisconnected sentinel
			// settings/wiring.go's renderSignOut uses for the identical
			// case.
			signOutErr.Set(ErrDisconnected)
			return
		}
		phase.Set(headerSignOutInFlight)
		signOutErr.Set(nil)
		go func() {
			_, err := conn.Auth.Logout(context.Background(), &affv1.AuthServiceLogoutRequest{})
			phase.Set(headerSignOutIdle)
			if err != nil {
				signOutErr.Set(err)
				return
			}
			ApplyEvent(appstate.EvLogout)
			// Replace, not push — mirrors settings/wiring.go's
			// renderSignOut: Back must not be able to land on an authed
			// route's history entry for a session that no longer exists.
			router.NavigateReplace("/login")
		}()
	})

	current := sess.Get()
	showNav := appstate.IsAuthedIsh(current)
	interactive := current == appstate.Auth || current == appstate.Killed
	linkableBrand := interactive || current == appstate.Disconnected

	return h.Header(
		h.ClassStr("af-header"),
		renderHeaderBrand(t, linkableBrand, !showNav, brandClick),
		h.Show(showNav, renderHeaderNav(t, loc.Path, interactive, navClicks)),
		h.Show(showNav, renderHeaderSignOut(t, interactive, phase.Get(), signOutErr.Get(), handleSignOut)),
	)
}

// markSrc is the brand crest, served by StaticHandler from the same serve
// directory as the bundle (web/build.sh stages it; internal/publish's
// staticContentTypes gives .png a real Content-Type so the browser renders
// it rather than offering it as a download).
//
// # Why this is an <img> and no longer inline SVG
//
// Until 2026-08-10 this file carried a hand-drawn SVG of the previous mark
// as a Go string constant, rendered through h.RawHTMLUnsafe. The mark it
// drew no longer exists: the brand is now a raster render (gradients, soft
// interior shading), and there is no faithful vector of it — a traced
// approximation would be a different mark wearing the same name. An <img>
// also removes three problems the inline copy had: the markup was
// duplicated by hand into web/pages/auth/brand.go with a comment admitting
// the two "must be kept in sync by hand"; it shipped inside app.wasm, so
// changing the logo meant recompiling; and it needed RawHTMLUnsafe, the
// one escape hatch in this app that bypasses sanitizing.
//
// # The wordmark stays HTML text, and that is now load-bearing
//
// The source lockup sets "AnimeFeedFlux" in dark navy inside its own bright
// glow, so it cannot be extracted to a transparent asset (loosen the key and
// the mark ships inside a blue cloud twice its size; tighten it and the text
// dissolves first) and, being dark navy, it would be invisible on this app's
// dark theme even if it could. renderHeaderBrand therefore keeps rendering
// the wordmark as ordinary HTML, which inherits tokens.FontSans, stays crisp
// at every size, and follows `color` into dark mode. The mark itself keeps
// its own colours, per docs/design-direction.md: "a brand mark that changes
// hue with the theme stops being a brand mark."
const markSrc = "/favicon-512.png"

// markOnly drops the rule and wordmark, leaving the crest by itself. It is
// set in the states this file's own doc comment has always described as "the
// mark alone" (ANON, ELEVATED) and which, until now, still rendered the full
// lockup — visibly wrong on /login, where the header's lockup and the auth
// card's lockup said the same thing twice, 200px apart. The card's is the
// one that belongs there: that screen's entire job is to state the identity.
//
// renderHeaderBrand is the mark+wordmark lockup, a plain rendering helper
// (no hooks of its own — see renderHeader's doc comment; onClick is already
// a stable ui.Handler built there). linkable=false (ANON, ELEVATED) renders
// a plain, unlinked div; linkable=true (AUTH, KILLED, DISCONNECTED) links
// home to /generate, matching the "logo goes home" convention.
//
// The wordmark is plain HTML (see afLogoMarkSVG's doc comment for why it
// cannot live inside the SVG), colored by inheriting .af-header__brand's
// `color` — real `currentColor` via CSS inheritance, same effect the
// original SVG's `fill="currentColor"` was after, just via a different
// mechanism, so it still inverts correctly in dark mode. Only the mark
// itself (the red cel, .af-header__mark) keeps its own fixed color,
// matching web/static/logo.svg's own doc comment: "a brand mark that
// changes hue with the theme stops being a brand mark."
func renderHeaderBrand(t gwci18n.Namespace, linkable, markOnly bool, onClick ui.Handler) ui.Node {
	lockup := h.Fragment(
		h.Img(
			h.ClassStr("af-header__mark"),
			h.Src(markSrc),
			h.Alt(""), // decorative: the wrapping link/div carries the accessible name
			h.Aria("hidden", "true"),
		),
		h.Show(!markOnly, h.Span(h.ClassStr("af-header__rule"), h.Aria("hidden", "true"))),
		h.Show(!markOnly, h.Span(
			h.ClassStr("af-header__wordmark"),
			h.Aria("hidden", "true"), // decorative alongside the accessible name on the wrapping link/div
			// The product name is a proper noun and is identical in every
			// locale — translating it would rename the product, not
			// localise it. This is the deliberate brand exemption PLAN.md
			// §12 flagged as an open question ("a defect to fix, or an
			// exemption D6-19 should have named but didn't"), settled here
			// as an exemption and declared through the linter's own
			// reason-carrying suppression rather than left as a standing
			// finding. It joins §12.6's two existing out-of-catalogue
			// cases: model-authored feed content, and the generic
			// login-failure string.
			h.Span(h.ClassStr("af-header__wordmark-strong"), h.Text("Anime")), //nolint:i18n -- brand name, not translatable copy
			h.Span(h.ClassStr("af-header__wordmark-light"), h.Text("Feed")),   //nolint:i18n -- brand name, not translatable copy
			h.Span(h.ClassStr("af-header__wordmark-strong"), h.Text("Flux")),  //nolint:i18n -- brand name, not translatable copy
		)),
	)
	if !linkable {
		return h.Div(h.ClassStr("af-header__brand"), h.Aria("label", t.T(keyHeaderBrandLabel)), lockup)
	}
	return h.A(
		h.ClassStr("af-header__brand"),
		h.Href("/generate"),
		h.Aria("label", t.T(keyHeaderBrandHomeLabel)),
		h.OnClick(onClick),
		lockup,
	)
}

// renderHeaderNav renders the three destinations — a plain rendering helper
// (no hooks; clicks are the stable ui.Handler values renderHeader already
// built, one per headerNavItems entry, in the same order). interactive=false
// (the DISCONNECTED case) renders plain, unfocusable, aria-disabled text
// instead of real links: see renderHeader's doc comment for why this must
// not merely look muted but actually not function as navigation while the
// control plane is down.
func renderHeaderNav(t gwci18n.Namespace, currentPath string, interactive bool, clicks [len(headerNavItems)]ui.Handler) ui.Node {
	// h.Ul's variadic ...any parameter is the function's ONLY parameter, so
	// a leading fixed arg cannot be combined with a trailing `items...`
	// spread (Go spec: "no other arguments may be present" alongside a
	// spread) — the class option has to go INTO the slice instead.
	items := make([]any, 0, 1+len(headerNavItems))
	items = append(items, h.ClassStr("af-header__nav-list"))
	for i, item := range headerNavItems {
		items = append(items, renderHeaderNavItem(t, item, navItemActive(currentPath, item.path), interactive, clicks[i]))
	}
	return h.Nav(
		h.ClassStr("af-header__nav"),
		h.Aria("label", t.T(keyHeaderNavLabel)),
		h.Ul(items...),
	)
}

func renderHeaderNavItem(t gwci18n.Namespace, item headerNavItem, active, interactive bool, onClick ui.Handler) ui.Node {
	label := t.T(item.key)
	if !interactive {
		return h.Li(
			h.ClassStr("af-header__nav-item"),
			h.Span(
				h.ClassStr(h.ClassMap(map[string]bool{
					"af-header__nav-link":        true,
					"af-header__nav-link--inert": true,
					"af-header__nav-link--here":  active,
				})),
				h.Aria("disabled", "true"),
				h.Text(label),
			),
		)
	}

	linkProps := []any{
		h.ClassStr(h.ClassMap(map[string]bool{
			"af-header__nav-link":         true,
			"af-header__nav-link--active": active,
		})),
		h.Href(item.path),
		h.OnClick(onClick),
	}
	if active {
		linkProps = append(linkProps, h.Aria("current", "page"))
	}
	linkProps = append(linkProps, h.Text(label))

	return h.Li(h.ClassStr("af-header__nav-item"), h.A(linkProps...))
}

// renderHeaderSignOut is the header's always-visible sign-out control — a
// plain rendering helper (no hooks; onClick is renderHeader's stable
// ui.Handler). interactive=false disables the button outright (see
// renderHeader's doc comment: while DISCONNECTED, the Logout RPC would be
// refused anyway).
func renderHeaderSignOut(t gwci18n.Namespace, interactive bool, phase headerSignOutPhase, signOutErr error, onClick ui.Handler) ui.Node {
	busy := phase == headerSignOutInFlight
	disabled := !interactive || busy
	label := t.T(keyHeaderSignOut)
	if busy {
		label = t.T(keyHeaderSignOutBusy)
	}
	return h.Div(
		h.ClassStr("af-header__signout"),
		h.Button(
			h.Type("button"),
			h.ClassStr("af-header__signout-btn"),
			h.Disabled(disabled),
			h.Aria("busy", boolAttr(busy)),
			h.OnClick(onClick),
			h.Text(label),
		),
		h.Show(signOutErr != nil, h.P(
			h.ClassStr("af-header__signout-error"),
			h.Role("alert"),
			h.Aria("live", "assertive"),
			h.Text(t.T(keyHeaderSignOutError)),
		)),
	)
}

func boolAttr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
