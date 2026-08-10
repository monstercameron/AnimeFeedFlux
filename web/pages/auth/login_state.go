package auth

import (
	"strings"
	"time"
)

// LoginStep is which of /login's two steps is currently showing
// (PLAN.md §12.1: "password step then TOTP step, one page").
type LoginStep int

const (
	LoginStepPassword LoginStep = iota
	LoginStepTOTP
)

// LoginForm is the pure client-side state for /login. It has no knowledge
// of the DOM, the network, or a browser event loop — login.go (the
// //go:build js && wasm component) drives it from event handlers. Values
// are copied, never mutated in place, so every transition below is
// impossible to apply out of order without going through a guarded
// method (mirrors web/appstate.Transition's ok-bool style).
type LoginForm struct {
	Step             LoginStep
	Password         string
	TOTPCode         string
	Submitting       bool
	ConsecutiveFails int
	BackoffUntil     time.Time
}

// NewLoginForm returns the initial state: step one, everything empty.
func NewLoginForm() LoginForm {
	return LoginForm{Step: LoginStepPassword}
}

// SetPassword updates the password field. No-op (returns f unchanged) once
// submitting or past the password step — D1-04's "no double-submit"
// extends naturally to "no editing a field mid-flight either".
func (f LoginForm) SetPassword(v string) LoginForm {
	if f.Submitting || f.Step != LoginStepPassword {
		return f
	}
	f.Password = v
	return f
}

// SetTOTPCode updates the TOTP field, same guard as SetPassword.
func (f LoginForm) SetTOTPCode(v string) LoginForm {
	if f.Submitting || f.Step != LoginStepTOTP {
		return f
	}
	f.TOTPCode = v
	return f
}

// CanContinue reports whether the password step may advance to the TOTP
// step: a non-blank password, not mid-submit. This is a client-side
// convenience gate only — PLAN.md's real password check happens once,
// bundled with the TOTP code, in the single Login RPC (doc.go assumption
// #1) — so CanContinue never itself contacts the server or reveals
// whether the password is correct.
func (f LoginForm) CanContinue() bool {
	return f.Step == LoginStepPassword && !f.Submitting && strings.TrimSpace(f.Password) != ""
}

// ContinueToTOTP advances password -> TOTP. ok is false (state unchanged)
// if CanContinue() was false.
func (f LoginForm) ContinueToTOTP() (LoginForm, bool) {
	if !f.CanContinue() {
		return f, false
	}
	f.Step = LoginStepTOTP
	return f, true
}

// Back returns from the TOTP step to the password step (D1's UX escape
// hatch when the admin realizes they mistyped the password — there is no
// server round trip to undo, since none happened yet). The TOTP code is
// cleared; a code the admin already typed for a wrong password attempt is
// not meaningfully reusable once they change the password field, and
// clearing avoids it looking like it survived unchanged. No-op while
// submitting.
func (f LoginForm) Back() (LoginForm, bool) {
	if f.Submitting || f.Step != LoginStepTOTP {
		return f, false
	}
	f.Step = LoginStepPassword
	f.TOTPCode = ""
	return f, true
}

// Blocked reports whether now is still inside this form's client-side
// backoff estimate window (see backoff_display.go).
func (f LoginForm) Blocked(now time.Time) bool {
	return RemainingBackoff(now, f.BackoffUntil) > 0
}

// RemainingBackoff is how much of the estimated backoff window is left at
// now.
func (f LoginForm) RemainingBackoff(now time.Time) time.Duration {
	return RemainingBackoff(now, f.BackoffUntil)
}

// CanSubmit reports whether the TOTP step's submit is currently allowed:
// on the TOTP step, a non-blank code, not already submitting, and not
// inside the backoff estimate window (D1-04: "no double-submit burning a
// TOTP window").
func (f LoginForm) CanSubmit(now time.Time) bool {
	return f.Step == LoginStepTOTP &&
		!f.Submitting &&
		strings.TrimSpace(f.TOTPCode) != "" &&
		!f.Blocked(now)
}

// BeginSubmit marks the form as in-flight. ok is false (state unchanged)
// if CanSubmit(now) was false — callers must check CanSubmit before
// calling the RPC, this just encodes that invariant so it cannot be
// skipped by accident.
func (f LoginForm) BeginSubmit(now time.Time) (LoginForm, bool) {
	if !f.CanSubmit(now) {
		return f, false
	}
	f.Submitting = true
	return f, true
}

// SubmitFailed records a failed Login RPC: clears Submitting, grows the
// client-side backoff estimate (never told apart from ANY other failure
// cause per D1-02 — this package does not and cannot know whether the
// password or the TOTP code was wrong, or whether the server's own
// backoff was already active; see doc.go assumption #2).
func (f LoginForm) SubmitFailed(now time.Time) LoginForm {
	f.Submitting = false
	f.ConsecutiveFails++
	f.BackoffUntil = now.Add(EstimateBackoff(f.ConsecutiveFails))
	return f
}

// SubmitSucceeded returns a fresh form. The caller (login.go) is
// responsible for everything success actually does off to the side —
// notifying the shell's session state and replacing history/navigating
// (D1-05) — none of which this pure package can express.
func (f LoginForm) SubmitSucceeded() LoginForm {
	return NewLoginForm()
}
