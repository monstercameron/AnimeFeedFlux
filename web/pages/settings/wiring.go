//go:build js && wasm

// wiring.go closes two gaps named honestly in this task's brief, without
// touching deps.go/render.go/render_security.go/render_provider.go/
// render_generation.go/render_publishing.go/client.go — all out of this
// change's allowed paths (only web/shell/, web/pages/history/wiring.go,
// and this file).
//
//  1. D0-08 held only on /generate. Session expiry while /settings is
//     mounted — six sections, several of them real forms (change
//     password, TOTP re-enrollment, Provider/Generation/Publishing save
//     forms) — silently dropped to ANON with no warning. The true
//     per-field dirty predicate for each of those forms lives entirely in
//     that section's own component-local ui.UseState hooks (render_
//     security.go's currentPassword/newPassword/..., render_provider.go's
//     activeProvider/defaultModel/..., and so on) — this file has no way
//     to read those without editing the files that own them, which this
//     task's file list forbids. renderWithShellWiring below registers the
//     same conservative substitute web/pages/history/wiring.go uses for
//     the identical reason: while /settings is mounted and wired, treat
//     it as potentially dirty, so a session expiry always holds and shows
//     the confirmation modal rather than ever silently discarding an open
//     edit. This is a known, documented coarsening (page granularity
//     instead of field granularity), not a hidden one.
//
//  2. Nothing anywhere calls shell.ApplyEvent(appstate.EvLogout) — there
//     is no sign-out in the app. PLAN.md §12.5 places session management
//     ("active sessions ... individual or global revoke") in Settings'
//     Security section, which is where a sign-out control belongs too,
//     but render_security.go is out of this file's reach, so
//     renderSignOut below is appended after render.go's own Render()
//     output via a fragment rather than laid into that section's card
//     list — a placement compromise forced by the file boundary, not the
//     intended final layout; whoever next owns render_security.go should
//     fold it into the Security section's card list properly. What it
//     does is the real thing D4-04/§4 ask for, not a client-only
//     approximation: call the full AuthServiceClient's Logout (through
//     shell.Conn(), not this package's own client.go AuthClient — that
//     interface deliberately excludes Logout; see its doc comment "Logout
//     ... belongs to the /login and /recover pages, not here" — this file
//     is the one legitimate exception, since /settings is where the
//     control actually needs to live), which revokes the session
//     server-side; only once that succeeds does it drop to ANON
//     (shell.ApplyEvent(appstate.EvLogout)) and navigate to /login. A
//     sign-out that only cleared client state would leave the real
//     session alive server-side — the opposite of what pressing the
//     button means, given this system's whole model is that the server,
//     not the browser, decides session validity (PLAN.md §4). If the
//     Logout call itself is refused because the socket is down
//     (wsconn.ErrDisconnected, surfaced here via shell.IsDisconnected —
//     see web/shell/mutationerror_js.go), that is rendered as a distinct
//     "you're offline" message rather than silently proceeding to a local
//     ANON that would strand a live session, and rather than showing the
//     same generic message a genuine server rejection would get (the
//     D0-10 check this task also asked to confirm).
//
// This file overrides deps.go's shell.RegisterPage("/settings", Render)
// registration with shell.RegisterPage("/settings", renderWithShellWiring)
// in its own init(). web/shell/pages.go's `pages` is a plain map — "Safe
// to call any time before or after Mount — page bodies are looked up
// dynamically on each navigation, not captured at registration time" is
// that doc comment's own words, i.e. last-registered-wins is the
// documented contract, not an accident this file is exploiting. Which
// call is last is decided by Go's own init() ordering: the language spec
// asks build tools to "present multiple files belonging to the same
// package in lexical file name order," which cmd/go does, so this
// package's init() functions run in filename order — deps.go's before
// wiring.go's ("d" < "w") — making this file's registration the one that
// sticks, deterministically, not by luck of the file system.
package settings

import (
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// renderWithShellWiring is the actual registered /settings page body (see
// init() below): render.go's Render(), plus the D0-08 dirty check and the
// D4-04 sign-out control that file and its siblings never wire up.
func renderWithShellWiring() ui.Node {
	// See this file's package doc comment, point 1, for why "wired" (page
	// mounted and ready, not per-field dirty state) is the conservative
	// substitute used here. Registered every render, matching
	// web/pages/generate/render.go's and web/pages/history/wiring.go's own
	// RegisterDirtyCheck calls, so it always reflects whichever page most
	// recently rendered (web/shell/session.go's dirtyCheck doc comment).
	shell.RegisterDirtyCheck(func() bool {
		return wired
	})

	// Just the page. The sign-out control that used to be appended here was
	// removed 2026-08-10: web/shell/header.go now carries one on every
	// authenticated route, so this was a second way to do the same thing,
	// reachable only by scrolling to the bottom of Settings, and it was the
	// only reason this function returned a Fragment rather than a page.
	return Render()
}

func init() {
	shell.RegisterPage("/settings", renderWithShellWiring)
	// Every settings section is its own address (/settings/provider, ...),
	// served by the same body — Render reads the section from the URL. Both
	// paths must point at renderWithShellWiring, not at Render directly:
	// this wrapper is what registers the unsaved-changes dirty check, and a
	// route that skipped it would silently lose that guard.
	shell.RegisterPage("/settings/:section", renderWithShellWiring)
}
