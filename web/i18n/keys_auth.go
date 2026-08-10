package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// Auth-namespace key constants for /login (PLAN.md §12.1, TODOS D1-01..06,
// D6-10). Named for what the string MEANS in the flow (D6-03), not for its
// current English wording, so rewording is a Text edit here, never a
// rename at every call site.
const (
	KeyLoginTitle             = "login.title"
	KeyLoginPasswordStepLabel = "login.passwordStepLabel"
	KeyLoginPasswordLabel     = "login.passwordLabel"
	KeyLoginContinue          = "login.continue"
	KeyLoginTOTPStepLabel     = "login.totpStepLabel"
	KeyLoginTOTPLabel         = "login.totpLabel"
	KeyLoginTOTPHint          = "login.totpHint"
	KeyLoginSubmit            = "login.submit"
	KeyLoginRecoverLink       = "login.recoverLink"
)

var loginMessages = gwci18n.NamespaceCatalog{
	KeyLoginTitle:             {Text: "Sign in"},
	KeyLoginPasswordStepLabel: {Text: "Step 1 of 2 — Password"},
	KeyLoginPasswordLabel:     {Text: "Password"},
	KeyLoginContinue:          {Text: "Continue"},
	KeyLoginTOTPStepLabel:     {Text: "Step 2 of 2 — Authentication code"},
	KeyLoginTOTPLabel:         {Text: "6-digit code"},
	KeyLoginTOTPHint:          {Text: "Enter the current code from your authenticator app."},
	KeyLoginSubmit:            {Text: "Sign in"},
	KeyLoginRecoverLink:       {Text: "Can't sign in? Recover your account"},
}

// Auth-namespace key constants for /recover (PLAN.md §12.2, TODOS
// D1-07..11, D6-10). See web/pages/auth/recover_state.go's package doc for
// why the elevated flow offers "reset password" and "re-enroll TOTP" as
// mutually exclusive choices rather than a two-step sequence the plan text
// reads as doing both: internal/rpc/auth.go's ChangePassword and
// ReenrollTOTP BOTH end the elevated session on the elevated branch
// (endElevatedSession), so whichever runs first while ELEVATED consumes
// the session — the second call would already be unauthenticated. That is
// a server behavior this UI reflects honestly rather than papers over.
const (
	KeyRecoverTitle                 = "recover.title"
	KeyRecoverIntro                 = "recover.intro"
	KeyRecoverCodeStepLabel         = "recover.codeStepLabel"
	KeyRecoverCodeLabel             = "recover.codeLabel"
	KeyRecoverCodeHint              = "recover.codeHint"
	KeyRecoverCodeSubmit            = "recover.codeSubmit"
	KeyRecoverRemainingCodes        = "recover.remainingCodes"
	KeyRecoverLowCodesWarning       = "recover.lowCodesWarning"
	KeyRecoverChooseActionHeading   = "recover.chooseActionHeading"
	KeyRecoverChooseActionIntro     = "recover.chooseActionIntro"
	KeyRecoverChoosePasswordReset   = "recover.choosePasswordReset"
	KeyRecoverChooseReenrollTOTP    = "recover.chooseReenrollTOTP"
	KeyRecoverNewPasswordLabel      = "recover.newPasswordLabel"
	KeyRecoverConfirmPasswordLabel  = "recover.confirmPasswordLabel"
	KeyRecoverPasswordMismatch      = "recover.passwordMismatch"
	KeyRecoverPasswordTooShort      = "recover.passwordTooShort"
	KeyRecoverPasswordTooLong       = "recover.passwordTooLong"
	KeyRecoverResetSubmit           = "recover.resetSubmit"
	KeyRecoverReenrollHeading       = "recover.reenrollHeading"
	KeyRecoverReenrollIntro         = "recover.reenrollIntro"
	KeyRecoverReenrollSubmit        = "recover.reenrollSubmit"
	KeyRecoverReenrollProvisioned   = "recover.reenrollProvisioned"
	KeyRecoverDoneHeading           = "recover.doneHeading"
	KeyRecoverDoneBody              = "recover.doneBody"
	KeyRecoverDoneGoToLogin         = "recover.doneGoToLogin"
	KeyRecoverBreakGlassHeading     = "recover.breakGlassHeading"
	KeyRecoverBreakGlassBody        = "recover.breakGlassBody"
	KeyRecoverBreakGlassCommandNote = "recover.breakGlassCommandNote"
)

var recoverMessages = gwci18n.NamespaceCatalog{
	KeyRecoverTitle: {Text: "Recover your account"},
	KeyRecoverIntro: {Text: "There are exactly two ways back in: a recovery code entered below, or break-glass access on the server itself. There is no \"email me a reset link\" — this system has no email infrastructure."},

	KeyRecoverCodeStepLabel: {Text: "Recovery code"},
	KeyRecoverCodeLabel:     {Text: "Recovery code"},
	KeyRecoverCodeHint:      {Text: "One of the single-use codes shown when two-factor authentication was enrolled. Each code works once."},
	KeyRecoverCodeSubmit:    {Text: "Continue"},

	KeyRecoverRemainingCodes: {
		PluralArg: "count",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "{count} recovery code left.",
			gwci18n.PluralOther: "{count} recovery codes left.",
		},
	},
	KeyRecoverLowCodesWarning: {Text: "Running low on recovery codes — regenerate a fresh set from Settings once you're signed back in."},

	KeyRecoverChooseActionHeading: {Text: "Choose one action"},
	KeyRecoverChooseActionIntro:   {Text: "This recovery session is valid for 10 minutes and ends the moment you complete either action below — choose the one you need. To do the other one too, use a second recovery code afterward, or use break-glass access, which does both at once."},
	KeyRecoverChoosePasswordReset: {Text: "Set a new password"},
	KeyRecoverChooseReenrollTOTP:  {Text: "Re-enroll two-factor authentication"},

	KeyRecoverNewPasswordLabel:     {Text: "New password"},
	KeyRecoverConfirmPasswordLabel: {Text: "Confirm new password"},
	KeyRecoverPasswordMismatch:     {Text: "Passwords don't match."},
	KeyRecoverPasswordTooShort:     {Text: "Password must be at least {min} characters."},
	KeyRecoverPasswordTooLong:      {Text: "Password must be at most {max} characters."},
	KeyRecoverResetSubmit:          {Text: "Set new password"},

	KeyRecoverReenrollHeading:     {Text: "Re-enroll two-factor authentication"},
	KeyRecoverReenrollIntro:       {Text: "This replaces your authenticator secret. Scan the new code into your authenticator app before you sign out — the old one stops working immediately."},
	KeyRecoverReenrollSubmit:      {Text: "Re-enroll"},
	KeyRecoverReenrollProvisioned: {Text: "Scan this into your authenticator app now — it will not be shown again: {uri}"},

	KeyRecoverDoneHeading:   {Text: "Done"},
	KeyRecoverDoneBody:      {Text: "This recovery session has ended and every other session was signed out. Sign in again with your new credential."},
	KeyRecoverDoneGoToLogin: {Text: "Go to sign in"},

	KeyRecoverBreakGlassHeading:     {Text: "Locked out entirely?"},
	KeyRecoverBreakGlassBody:        {Text: "If you have no recovery codes either, break-glass access resets your password, TOTP, and recovery codes all at once — but it only works over SSH, directly on the server, with access to the database file. There is no remote or web path for it."},
	KeyRecoverBreakGlassCommandNote: {Text: "Run: aff admin reset"},
}

// authMessages merges login.* and recover.* into the single "auth"
// namespace catalog.go registers (D6-04: one namespace per surface, not
// one per page — /login and /recover are both part of the same "auth"
// surface in PLAN.md §12).
var authMessages = mergeNamespaces(loginMessages, recoverMessages)

func mergeNamespaces(parts ...gwci18n.NamespaceCatalog) gwci18n.NamespaceCatalog {
	out := make(gwci18n.NamespaceCatalog)
	for _, part := range parts {
		for k, v := range part {
			out[k] = v
		}
	}
	return out
}
