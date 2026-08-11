package auth

import (
	"strings"
	"time"
)

// RecoverStep is which stage of /recover is showing. PLAN.md §12.2 names
// two PATHS (a recovery code, or break-glass over SSH); this models the
// interactive one (break-glass is documented on the page, never driven
// through it — see recover.go). ChooseAction/ResetPassword/ReenrollTOTP
// exist because of doc.go assumption #3: the elevated session the code
// opens can complete exactly one of "set new password" / "re-enroll
// TOTP", not both in sequence, so the UI must make that an explicit
// choice rather than silently chaining two calls where the second would
// fail.
type RecoverStep int

const (
	RecoverStepEnterCode RecoverStep = iota
	RecoverStepChooseAction
	RecoverStepResetPassword
	RecoverStepReenrollTOTP
	RecoverStepDone
)

// minRecoveryPasswordLen/maxRecoveryPasswordLen mirror
// internal/auth/password.go's minPasswordLen/maxPasswordLen (15/128,
// PLAN.md §4's NIST SP 800-63B policy) so the client can reject an
// obviously-too-short-or-long password before spending a round trip —
// the server's auth.IsWeak remains the actual authority (D6-15: this is
// the "mirroring server text" case that most easily drifts, so the
// numbers are named constants here specifically to make a future change
// on the server side visible as a diff here too, not a silent mismatch).
const (
	minRecoveryPasswordLen = 15
	maxRecoveryPasswordLen = 128
)

// RecoverForm is the pure client-side state for /recover, covering the
// recovery-code path (§12.2 item 1). Break-glass (§12.2 item 2) has no
// state here — recover.go renders it as static, always-visible page copy
// (D1-10), never a form.
type RecoverForm struct {
	Step             RecoverStep
	Code             string
	Submitting       bool
	ConsecutiveFails int
	BackoffUntil     time.Time

	// RemainingCodes is -1 until a code has been accepted (D1-09: "show
	// remaining recovery-code count after use" — there is nothing to show
	// before that).
	RemainingCodes int

	NewPassword     string
	ConfirmPassword string
}

// NewRecoverForm returns the initial state: the code-entry step.
func NewRecoverForm() RecoverForm {
	return RecoverForm{Step: RecoverStepEnterCode, RemainingCodes: -1}
}

// SetCode updates the recovery-code field. No-op once submitting or past
// the code step.
func (f RecoverForm) SetCode(v string) RecoverForm {
	if f.Submitting || f.Step != RecoverStepEnterCode {
		return f
	}
	f.Code = v
	return f
}

// Blocked/RemainingBackoff mirror LoginForm's — see backoff_display.go.
// The server's backoffTracker is shared across the login and recover
// endpoints by IP (internal/rpc/auth.go uses one `s.backoff` for both), so
// this form's own local counter is, if anything, an UNDER-estimate
// relative to the real server state when both forms have been tried from
// the same browser — documented here rather than papered over, same as
// doc.go assumption #2.
func (f RecoverForm) Blocked(now time.Time) bool {
	return RemainingBackoff(now, f.BackoffUntil) > 0
}

func (f RecoverForm) RemainingBackoff(now time.Time) time.Duration {
	return RemainingBackoff(now, f.BackoffUntil)
}

// CanSubmitCode reports whether the code-entry step's submit is allowed.
func (f RecoverForm) CanSubmitCode(now time.Time) bool {
	return f.Step == RecoverStepEnterCode &&
		!f.Submitting &&
		strings.TrimSpace(f.Code) != "" &&
		!f.Blocked(now)
}

// BeginSubmitCode marks the code submit as in-flight.
func (f RecoverForm) BeginSubmitCode(now time.Time) (RecoverForm, bool) {
	if !f.CanSubmitCode(now) {
		return f, false
	}
	f.Submitting = true
	return f, true
}

// CodeAccepted transitions into the elevated session's action choice
// (D1-08), recording the remaining-code count the RPC response carried
// (D1-09).
func (f RecoverForm) CodeAccepted(remaining int) (RecoverForm, bool) {
	if f.Step != RecoverStepEnterCode {
		return f, false
	}
	f.Step = RecoverStepChooseAction
	f.Submitting = false
	f.ConsecutiveFails = 0
	f.BackoffUntil = time.Time{}
	f.RemainingCodes = remaining
	return f, true
}

// CodeFailed records a rejected/already-used/unrecognized code as exactly
// the same generic outcome as any other failure (D1-02/D6-18 apply to
// /recover too, per PLAN.md §12.2: "Same generic-error and backoff rules
// as login").
func (f RecoverForm) CodeFailed(now time.Time) RecoverForm {
	f.Submitting = false
	f.ConsecutiveFails++
	f.BackoffUntil = now.Add(EstimateBackoff(f.ConsecutiveFails))
	return f
}

// LowOnCodes reports whether the dashboard-nag threshold (§12.2: "at <=2
// remaining, the dashboard nags") applies. /recover itself is not "the
// dashboard", but showing the same warning right after the count is first
// known is the earliest possible moment it's actionable.
func (f RecoverForm) LowOnCodes() bool {
	return f.RemainingCodes >= 0 && f.RemainingCodes <= 2
}

// ChoosePasswordReset moves from the post-code choice into the
// password-reset step. See doc.go assumption #3 for why this and
// ChooseReenrollTOTP are mutually exclusive rather than sequential.
func (f RecoverForm) ChoosePasswordReset() (RecoverForm, bool) {
	if f.Step != RecoverStepChooseAction {
		return f, false
	}
	f.Step = RecoverStepResetPassword
	return f, true
}

// ChooseReenrollTOTP moves from the post-code choice into the
// TOTP-re-enrollment step.
func (f RecoverForm) ChooseReenrollTOTP() (RecoverForm, bool) {
	if f.Step != RecoverStepChooseAction {
		return f, false
	}
	f.Step = RecoverStepReenrollTOTP
	return f, true
}

// SetNewPassword/SetConfirmPassword update the reset-password fields.
func (f RecoverForm) SetNewPassword(v string) RecoverForm {
	if f.Submitting || f.Step != RecoverStepResetPassword {
		return f
	}
	f.NewPassword = v
	return f
}

func (f RecoverForm) SetConfirmPassword(v string) RecoverForm {
	if f.Submitting || f.Step != RecoverStepResetPassword {
		return f
	}
	f.ConfirmPassword = v
	return f
}

// PasswordsMatch reports whether NewPassword and ConfirmPassword agree.
func (f RecoverForm) PasswordsMatch() bool {
	return f.NewPassword == f.ConfirmPassword
}

// PasswordLengthOK mirrors the server's auth.IsWeak length bounds
// (minRecoveryPasswordLen/maxRecoveryPasswordLen above) — a client-side
// convenience check only; the server remains authoritative, including for
// the compromised-password-list rule this deliberately does NOT
// replicate client-side (that list is a server-side resource, not
// something to ship to a WASM bundle).
func (f RecoverForm) PasswordLengthOK() bool {
	n := len(strings.TrimSpace(f.NewPassword))
	return n >= minRecoveryPasswordLen && n <= maxRecoveryPasswordLen
}

// CanSubmitReset reports whether the password-reset step's submit is
// allowed.
func (f RecoverForm) CanSubmitReset() bool {
	return f.Step == RecoverStepResetPassword &&
		!f.Submitting &&
		f.PasswordsMatch() &&
		f.PasswordLengthOK()
}

// BeginSubmitReset marks the reset submit as in-flight.
func (f RecoverForm) BeginSubmitReset() (RecoverForm, bool) {
	if !f.CanSubmitReset() {
		return f, false
	}
	f.Submitting = true
	return f, true
}

// ResetFailed records a failed ChangePassword call (generic — see
// CodeFailed's doc comment; the same rule applies here) and stays on the
// reset step so the admin can retry within the still-live elevated
// session.
func (f RecoverForm) ResetFailed() RecoverForm {
	f.Submitting = false
	return f
}

// ResetSucceeded moves to the terminal Done step. Per doc.go assumption
// #3, a successful ChangePassword while elevated ends the elevated
// session server-side — there is nothing left this form can do afterward
// but tell the admin to log in again (§12.2: "forces a full re-login").
func (f RecoverForm) ResetSucceeded() (RecoverForm, bool) {
	if f.Step != RecoverStepResetPassword {
		return f, false
	}
	f.Step = RecoverStepDone
	f.Submitting = false
	f.NewPassword = ""
	f.ConfirmPassword = ""
	return f, true
}

// CanSubmitReenroll reports whether the TOTP-re-enrollment step's submit
// is allowed. Unlike a live-session re-enroll, the elevated path needs no
// current-password field (proto doc on ReenrollTOTPRequest.CurrentPassword:
// "identity was already re-proven by the recovery code itself") — so the
// only gate is not already submitting.
func (f RecoverForm) CanSubmitReenroll() bool {
	return f.Step == RecoverStepReenrollTOTP && !f.Submitting
}

// BeginSubmitReenroll marks the re-enroll submit as in-flight.
func (f RecoverForm) BeginSubmitReenroll() (RecoverForm, bool) {
	if !f.CanSubmitReenroll() {
		return f, false
	}
	f.Submitting = true
	return f, true
}

// ReenrollFailed mirrors ResetFailed for the re-enroll step.
func (f RecoverForm) ReenrollFailed() RecoverForm {
	f.Submitting = false
	return f
}

// ReenrollSucceeded moves to the terminal Done step, same reasoning as
// ResetSucceeded (doc.go assumption #3: ReenrollTOTP also ends the
// elevated session on success).
func (f RecoverForm) ReenrollSucceeded() (RecoverForm, bool) {
	if f.Step != RecoverStepReenrollTOTP {
		return f, false
	}
	f.Step = RecoverStepDone
	f.Submitting = false
	return f, true
}

// RecoverFocusTarget is which control recover.go should move focus to
// after a step transition — the RecoverForm analogue of login_state.go's
// LoginFocusTarget; see that type's doc comment for why this is a
// symbolic enum rather than a concrete element ID.
type RecoverFocusTarget int

const (
	// RecoverFocusNone means "leave focus where it is."
	RecoverFocusNone RecoverFocusTarget = iota
	RecoverFocusCode
	RecoverFocusNewPassword
	RecoverFocusReenrollHeading
	RecoverFocusError
)

// RecoverFocusTargetForStep is the focus destination for arriving at step.
// RecoverStepChooseAction has no single destination of its own here
// (recover.go focuses nothing on that step: the choice buttons are the
// first two interactive elements after the just-submitted code field
// unmounts, so the browser's own document order already puts them next in
// tab order without a forced move that would fight the still-focused
// submit button mid-click). RecoverStepDone likewise has none — the done
// screen's own confirmation gate (recover.go) drives its own focus.
func RecoverFocusTargetForStep(step RecoverStep) RecoverFocusTarget {
	switch step {
	case RecoverStepEnterCode:
		return RecoverFocusCode
	case RecoverStepResetPassword:
		return RecoverFocusNewPassword
	case RecoverStepReenrollTOTP:
		return RecoverFocusReenrollHeading
	default:
		return RecoverFocusNone
	}
}
