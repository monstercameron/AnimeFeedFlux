package settings

// Destructive actions in this page (revoke all sessions, regenerate
// recovery codes, vacuum, TOML import) sit behind a kebab menu, never a
// primary button, and irreversible ones require typed confirmation
// (PLAN.md §12.6). This file is the pure matching/decision logic behind
// that modal — see TODOS.md's "Tests" instruction: "typed-confirmation
// matching" is explicitly named as something to host-test.

// ConfirmationMatches reports whether typed exactly equals want, with no
// trimming and no case-folding. An exact match is the point: a modal that
// accepts "delete " or "DELETE" when it asked for "delete" is not
// actually forcing the admin to read and reproduce the word, just to
// mash approximately the right keys — the friction is the safety
// property (PLAN.md §12.6: "irreversible ones require typed
// confirmation").
func ConfirmationMatches(typed, want string) bool {
	return typed == want
}

// DestructiveAction identifies each kebab-menu action this page defines
// that needs a confirmation decision, so the reversible/irreversible and
// typed-word choices below live in one place instead of being redecided
// per call site.
type DestructiveAction int

const (
	ActionRevokeOneSession DestructiveAction = iota
	ActionRevokeAllSessions
	ActionRegenerateRecoveryCodes
	ActionReenrollTOTP
	ActionChangePassword
	ActionVacuum
	ActionImportTOML
)

// RequiresTypedConfirmation reports whether action is irreversible enough
// to need the typed-word modal (PLAN.md §12.6) rather than a plain
// confirm/cancel. Revoking a single OTHER session is reversible in the
// sense that the admin can simply log back in on that device — it is
// destructive but not irreversible the way regenerating recovery codes
// (invalidates the old set permanently) or a TOML import (overwrites a
// feed's recipe) is. Vacuum is not data-destructive, but it takes an
// exclusive lock and blocks the live database for a real, size-dependent
// duration (internal/rpc/system.go's Vacuum, TODOS.md D4-10) — that
// "irreversible-feeling" cost while it runs earns it the same typed gate
// as the others, not a plain confirm/cancel an admin could fat-finger.
func RequiresTypedConfirmation(action DestructiveAction) bool {
	switch action {
	case ActionRevokeAllSessions, ActionRegenerateRecoveryCodes, ActionImportTOML, ActionVacuum:
		return true
	default:
		return false
	}
}

// ConfirmationWord returns the word or phrase the typed-confirmation
// modal asks for, for actions where RequiresTypedConfirmation is true.
// English-literal here would violate D6-16 ("decide explicitly whether
// the typed word is translated; if it is, the comparison must translate
// too"); this package resolves that by keeping the compared word an
// i18n-driven value the caller supplies to ConfirmationMatches rather
// than hardcoding one here — ConfirmationWordKey returns the i18n key for
// it instead, so a single locale change moves both the displayed word and
// the comparison target together.
func ConfirmationWordKey(action DestructiveAction) string {
	switch action {
	case ActionRevokeAllSessions:
		return "settings.security.sessions.revokeAll.confirmWord"
	case ActionRegenerateRecoveryCodes:
		return "settings.security.recoveryCodes.regenerate.confirmWord"
	case ActionImportTOML:
		return "settings.data.importToml.confirmWord"
	case ActionVacuum:
		return "settings.data.vacuum.confirmWord"
	default:
		return ""
	}
}

// RevokeAllWarning is the SEC-46-shaped rule generalized: any action that
// will end other sessions must say so BEFORE it happens, not report it
// afterwards. ChangePassword revokes every OTHER session (SEC-46);
// RevokeAllSessions revokes every session including the caller's own.
// Both need a pre-action warning; the wording differs (one logs the admin
// out too, the other does not), which is why this returns which i18n key
// to show rather than a bool.
func RevokeAllWarningKey(action DestructiveAction) string {
	switch action {
	case ActionChangePassword:
		return "settings.security.changePassword.revokesOtherSessionsWarning"
	case ActionRevokeAllSessions:
		return "settings.security.sessions.revokeAll.warning"
	default:
		return ""
	}
}
