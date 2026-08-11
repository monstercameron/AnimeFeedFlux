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

	// KeyShellHeaderNav* back the primary navigation in web/shell/header.go.
	//
	// header.go already declares exactly these key strings and calls through
	// the runtime with them — but the catalogue had no entries, so the
	// fallback rendered the raw key and the header literally read
	// "header.nav.generate". Found 2026-08-10 by screenshotting the app
	// rather than by any test: a missing key is not a compile error and not
	// a lookup error, it is a string that renders. DF-14 exists for exactly
	// this ("no raw key and no blank label appears"), and the long raw keys
	// were also what forced /settings into horizontal scroll at 380px.
	//
	// Keep these strings identical to the values documented in header.go's
	// doc comment; that comment is where the wording was decided.
	KeyShellHeaderNavLabel    = "header.nav.label"
	KeyShellHeaderNavGenerate = "header.nav.generate"
	KeyShellHeaderNavHistory  = "header.nav.history"
	KeyShellHeaderNavSettings = "header.nav.settings"

	// KeyShellHeaderSignOut is the header's sign-out control. The action
	// keeps this one name everywhere it appears so the vocabulary stays
	// learnable — the button says "Sign out", not "Log out" next to an
	// "expiry.login" modal that says "Log in".
	KeyShellHeaderSignOut = "header.signOut"

	// The remaining four keys header.go references. Same defect as the
	// KeyShellHeaderNav* block above and found the same way (a screenshot,
	// not a test): header.go has always called through the runtime with
	// these, and the catalogue has never had them, so today a screen reader
	// on the brand link announces the literal string "header.brand.homeLabel"
	// and a failed sign-out renders "header.signOut.error" in the danger
	// colour. PLAN.md §12 records both as known defects; this closes them.
	//
	// The two brand keys are ACCESSIBLE NAMES, not visible text — the
	// wordmark beside them is aria-hidden precisely so the name is said
	// once. homeLabel says where the link goes, because "AnimeFeedFlux
	// Admin" alone does not tell a screen-reader user that activating it
	// navigates.
	KeyShellHeaderBrandLabel     = "header.brand.label"
	KeyShellHeaderBrandHomeLabel = "header.brand.homeLabel"
	KeyShellHeaderSignOutBusy    = "header.signOut.busy"
	KeyShellHeaderSignOutError   = "header.signOut.error"

	// KeyShellHeaderTheme* back the appearance control (theme.go). The
	// label names what the control is FOR ("Appearance") rather than what
	// it currently shows, and each option is stated as the thing you get,
	// not as a verb — a control that reads "Dark" in dark mode and "Light"
	// in light mode is ambiguous about whether it reports or acts.
	KeyShellHeaderThemeLabel  = "header.theme.label"
	KeyShellHeaderThemeSystem = "header.theme.system"
	KeyShellHeaderThemeLight  = "header.theme.light"
	KeyShellHeaderThemeDark   = "header.theme.dark"
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
	// "sign in", not "log in": the login page's own control says "Sign in",
	// and an action that changes name between the modal and the page it
	// sends you to makes the interface harder to learn for no gain.
	KeyShellExpiryBody:  {Text: "Your session expired. Your unsaved changes are kept until you sign in again."},
	KeyShellExpiryLogin: {Text: "Sign in"},

	KeyShellGuardRedirectNotice: {Text: "You were redirected because you need to sign in first."},

	KeyShellNotImplemented: {Text: "Page not yet implemented: {path}"},

	KeyShellHeaderNavLabel:    {Text: "Primary navigation"},
	KeyShellHeaderNavGenerate: {Text: "Generate"},
	KeyShellHeaderNavHistory:  {Text: "History"},
	KeyShellHeaderNavSettings: {Text: "Settings"},
	KeyShellHeaderSignOut:     {Text: "Sign out"},

	KeyShellHeaderBrandLabel:     {Text: "AnimeFeedFlux Admin"},
	KeyShellHeaderBrandHomeLabel: {Text: "AnimeFeedFlux Admin — go to Generate"},
	// Present tense, same verb as the button: "Sign out" -> "Signing out…".
	KeyShellHeaderSignOutBusy: {Text: "Signing out…"},
	// States what happened and what to do, in the interface's voice, with
	// no apology and nothing vague — the register the rest of this
	// catalogue's errors use.
	KeyShellHeaderSignOutError: {Text: "Sign out failed. Try again."},

	KeyShellHeaderThemeLabel:  {Text: "Appearance"},
	KeyShellHeaderThemeSystem: {Text: "Match system"},
	KeyShellHeaderThemeLight:  {Text: "Light"},
	KeyShellHeaderThemeDark:   {Text: "Dark"},
}
