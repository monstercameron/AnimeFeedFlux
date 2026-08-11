package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// Common-namespace key constants (D6-03: named for meaning, not wording).
// These are the ones shared across more than one D1 page rather than
// belonging to a single surface — see the doc comment on
// KeyCommonGenericAuthError for why the login and recovery generic-error
// text specifically live here instead of under auth.*.
const (
	// KeyCommonGenericAuthError is THE ONE error string PLAN.md §12.1/§12.2
	// use for every authentication failure — wrong password, unknown
	// account, bad TOTP, backoff active, unrecognized/already-used
	// recovery code. D1-02 says "no oracle" for /login; D6-18 extends
	// that into i18n terms ("stays generic in every locale"). This key
	// lives in `common`, not `auth`, and is used by BOTH web/pages/auth/
	// login.go and recover.go, on purpose: PLAN.md's phrasing ("Same
	// generic-error and backoff rules as login", §12.2) reads as the two
	// surfaces sharing one failure vocabulary, not each minting its own
	// look-alike string that a translator could accidentally diverge
	// (D6-18's actual failure mode). One key, one translation, used from
	// two call sites, is what keeps "generic" true across the whole app
	// rather than just within one page.
	KeyCommonGenericAuthError = "genericAuthError"

	// KeyCommonBackoffNotice is the honest "try again in N seconds"
	// surfaced by D1-03/§12.1. Plural-keyed on {count} (whole seconds).
	// See web/pages/auth's backoff_display.go for why this is a
	// CLIENT-SIDE estimate, never a value the server discloses.
	KeyCommonBackoffNotice = "backoffNotice"

	// KeyCommonBack is a generic "go back one step" label, shared by
	// login's TOTP->password step-back and recovery's later steps.
	KeyCommonBack = "back"

	// KeyCommonSubmitting is a generic in-flight submit-button label
	// (D1-04's "disable submit while in flight" needs the button to say
	// something other than its resting label while it is disabled for
	// that reason specifically, as opposed to disabled-with-reason's
	// other causes).
	KeyCommonSubmitting = "submitting"

	// KeyCommonConnectionUnreachable is what a failed sign-in renders when
	// the RPC never reached the server at all, as opposed to reaching it and
	// being rejected. §12.1's "one generic error string" rule exists to stop
	// WHICH CREDENTIAL was wrong from becoming an oracle; it was never about
	// hiding connectivity, and "the server is unreachable, nothing was sent"
	// leaks nothing about any account.
	//
	// Declared here 2026-08-10 because it was NOT: web/pages/auth/
	// backoff_display.go has carried `const keyConnectionUnreachable =
	// "connectionUnreachable"` with a doc comment openly stating the
	// catalogue entry was "out of this task's allowed paths", so the login
	// page rendered the literal string "common.connectionUnreachable" to the
	// operator on every unreachable-server sign-in. Reported from a real
	// browser, not found by any test — and not found by this session's own
	// first sweep either, which only checked NAMESPACE-PREFIXED key literals
	// and so could not see a bare one that gets its namespace from
	// intl.NS(NSCommon) at the call site.
	KeyCommonConnectionUnreachable = "connectionUnreachable"

	// KeyCommonBackoffCleared is the completion-side counterpart to
	// KeyCommonBackoffNotice: announced once when the estimated backoff
	// window elapses, so a screen-reader user who heard "try again in 8s"
	// also hears when that stopped being true. Same missing-catalogue
	// history as KeyCommonConnectionUnreachable above.
	KeyCommonBackoffCleared = "backoffCleared"
)

// web/ui's shared primitives (D6-14, PLAN.md §12.6) render only these
// common.* keys — action.*, state.*, confirm.*, kebab.* — never a
// namespace-specific one, because a Button/Modal/StatePanel instance is
// reused across auth/generate/history/settings and cannot know which
// namespace its caller is in. See adapter.go's NewLabelResolver: it is the
// one place these keys are looked up, always against NSCommon.
//
// Every key here that takes an argument names its placeholder {arg1},
// {arg2}, ... (1-indexed, in call order) — see NewLabelResolver's doc
// comment for why: web/ui.T's signature is positional (...any), so there
// is no argument NAME to key a {placeholder} on the way there is for a
// direct gwci18n.Arguments call. Any other namespace that also resolves
// through a positional (...any) Translator (TODOS.md D6-11/12/13's
// generate/history/settings packages, none of which import this package —
// see doc.go in each) should follow the same arg1/arg2/... convention for
// its own catalogue entries so one interpolation rule holds across the
// whole app, not a different guess per namespace.
const (
	// KeyActionSave is Button's LabelKey for a form's primary "persist
	// this" action (settings sections, the generate editor).
	KeyActionSave = "action.save"
	// KeyActionCancel backs out of a form or dialog without saving.
	KeyActionCancel = "action.cancel"
	// KeyActionRetry is StatePanel's error-view retry button (D0-14).
	KeyActionRetry = "action.retry"
	// KeyActionClose is Modal's icon-only close button's accessible name.
	KeyActionClose = "action.close"
	// KeyActionDismiss is Toast's icon-only dismiss button's accessible
	// name.
	KeyActionDismiss = "action.dismiss"
	// KeyActionConfirmDestroy is Confirm's danger-variant submit button —
	// deliberately generic across every irreversible action (purge,
	// revoke-all, regenerate-recovery-codes, ...) rather than one label
	// per action, matching D0-16's "the destructive action has already
	// been isolated behind its own dialog" reasoning: the button's job is
	// to name the ACT (confirm-and-destroy), the dialog's title/message
	// keys (caller-supplied) name the specific thing being destroyed.
	KeyActionConfirmDestroy = "action.confirmDestroy"

	// KeyStateLoading/.../Disconnected are StatePanel's five non-populated
	// views (D0-13/D0-14/D0-23's six-state matrix; Populated has no label
	// of its own — it renders the caller's rows). Every list/panel in
	// Phase D shares these instead of hand-rolling per-surface wording, so
	// "loading" reads identically everywhere.
	KeyStateLoading      = "state.loading"
	KeyStateEmpty        = "state.empty"
	KeyStateError        = "state.error"
	KeyStateDisabled     = "state.disabled"
	KeyStateDisconnected = "state.disconnected"

	// KeyConfirmTypePhrase is Confirm's "type X to confirm" instruction,
	// interpolated with {arg1} = the already-resolved RequiredPhrase the
	// caller passed to ui.ConfirmProps (web/ui/confirm.go, which this
	// package does not edit — see this file's package doc comment on
	// D6-16 below). KeyConfirmTypeLabel is the typed-input field's own
	// label.
	KeyConfirmTypePhrase = "confirm.typePhrase"
	KeyConfirmTypeLabel  = "confirm.typeLabel"

	// KeyKebabActionsFor is a Kebab trigger's accessible name, interpolated
	// with {arg1} = the row's own title/identifier (e.g. a feed's slug) —
	// a bare "⋯" has no accessible name on its own (web/ui/kebab.go's doc
	// comment).
	KeyKebabActionsFor = "kebab.actionsFor"
)

var commonMessages = gwci18n.NamespaceCatalog{
	KeyCommonGenericAuthError: {Text: "That didn't work. Check your details and try again."},
	KeyCommonBackoffNotice: {
		PluralArg: "count",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Too many attempts. Try again in {count} second.",
			gwci18n.PluralOther: "Too many attempts. Try again in {count} seconds.",
		},
	},
	KeyCommonBack:       {Text: "Back"},
	KeyCommonSubmitting: {Text: "Working…"},
	// States what happened and what it means, and is explicit that nothing
	// was sent — an operator who has just deployed needs to know whether to
	// re-check the password or the server.
	KeyCommonConnectionUnreachable: {Text: "Couldn't reach the server — nothing was sent. Check the connection and try again."},
	KeyCommonBackoffCleared:        {Text: "You can try again now."},

	KeyActionSave:           {Text: "Save"},
	KeyActionCancel:         {Text: "Cancel"},
	KeyActionRetry:          {Text: "Retry"},
	KeyActionClose:          {Text: "Close"},
	KeyActionDismiss:        {Text: "Dismiss"},
	KeyActionConfirmDestroy: {Text: "Confirm"},

	KeyStateLoading:      {Text: "Loading…"},
	KeyStateEmpty:        {Text: "Nothing here yet."},
	KeyStateError:        {Text: "Something went wrong."},
	KeyStateDisabled:     {Text: "Disabled"},
	KeyStateDisconnected: {Text: "Reconnecting…"},

	KeyConfirmTypePhrase: {Text: "Type {arg1} to confirm."},
	KeyConfirmTypeLabel:  {Text: "Confirmation phrase"},

	KeyKebabActionsFor: {Text: "Actions for {arg1}"},
}
