package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// Shell-namespace key constants (TODOS.md D6-09, PLAN.md §12.6): the
// DISCONNECTED banner (including its reconnect countdown), the
// session-expiry modal, the auth guard's redirect notice, and the
// not-yet-implemented route placeholder. web/shell (this wave does not
// touch it — see the top-level task's hard rule) currently renders all of
// these as literal strings (banner.go's "Disconnected — reconnecting…",
// expiry.go's "Your session expired...", pages.go's "Page not yet
// implemented: %s"); these keys exist so whoever wires shell to
// gwci18n.UseI18n() next has somewhere to point without inventing wording
// or a namespace shape.
//
// shell has no ui.T-style consumer (unlike web/ui's common.* keys): every
// caller here is expected to hold a gwci18n.Runtime (from UseI18n(), per
// catalog.go's package doc) and call Runtime.T(NSShell, key, Arguments{...})
// or Runtime.NS(NSShell).T(key, Arguments{...}) directly, so these keys use
// NAMED placeholders ({seconds}, {path}) rather than common.go's arg1/arg2
// convention, which exists specifically to work around web/ui.T's
// positional-only signature. Shell has no such constraint.
const (
	// KeyShellBannerDisconnected is the DISCONNECTED banner's steady-state
	// text (no known reconnect ETA yet).
	KeyShellBannerDisconnected = "banner.disconnected"

	// KeyShellBannerReconnectingIn is the banner's countdown variant, once
	// a retry delay is known — plural-keyed on {seconds} (D6-06: "through
	// the library's plural rules, not `if n == 1`"). Whoever wires this
	// computes the remaining whole seconds and passes it as the "seconds"
	// argument; do not pre-render "1 second" vs "N seconds" by hand.
	KeyShellBannerReconnectingIn = "banner.reconnectingIn"

	// KeyShellExpiryTitle/.../Login back the session-expiry modal
	// (session.go's D0-08 hold: "don't silently lose unsaved work").
	KeyShellExpiryTitle = "expiry.title"
	KeyShellExpiryBody  = "expiry.body"
	KeyShellExpiryLogin = "expiry.login"

	// KeyShellGuardRedirectNotice explains an involuntary redirect from
	// the route guard (guardadapter.go) — e.g. an ANON admin deep-linking
	// into /history lands on /login with this text, rather than silently
	// landing on /login with no explanation for why they're there.
	KeyShellGuardRedirectNotice = "guard.redirectNotice"

	// KeyShellNotImplemented is pages.go's per-route placeholder,
	// interpolated with {path} = the route that has no registered body
	// yet. TODOS.md D6-09 requires this go through
	// Runtime.T(NSShell, key, Arguments{"path": path}) rather than
	// fmt.Sprintf/h.Textf, so a locale swap changes this text too instead
	// of leaving one hardcoded English sentence behind everywhere else.
	KeyShellNotImplemented = "notImplemented.placeholder"
)

var shellMessages = gwci18n.NamespaceCatalog{
	KeyShellBannerDisconnected: {Text: "Disconnected — reconnecting…"},
	KeyShellBannerReconnectingIn: {
		PluralArg: "seconds",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Disconnected — reconnecting in {seconds} second…",
			gwci18n.PluralOther: "Disconnected — reconnecting in {seconds} seconds…",
		},
	},

	KeyShellExpiryTitle: {Text: "Session expired"},
	KeyShellExpiryBody:  {Text: "Your session expired. Your unsaved changes are kept until you log in again."},
	KeyShellExpiryLogin: {Text: "Log in"},

	KeyShellGuardRedirectNotice: {Text: "You were redirected because you need to sign in first."},

	KeyShellNotImplemented: {Text: "Page not yet implemented: {path}"},
}
