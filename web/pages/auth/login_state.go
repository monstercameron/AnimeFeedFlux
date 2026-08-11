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

// LoginFocusTarget is which control login.go should move focus to after a
// step transition. A symbolic enum rather than a concrete element ID
// because ui.UseId()-generated IDs only exist inside a live render —
// login.go maps this onto "#"+passwordID/"#"+totpID/"#"+errorID, and this
// package stays host-testable without a DOM.
type LoginFocusTarget int

const (
	// LoginFocusNone means "leave focus where it is" — used when a
	// transition did not happen (an ok=false guard already rejected it).
	LoginFocusNone LoginFocusTarget = iota
	LoginFocusPassword
	LoginFocusTOTP
	LoginFocusError
)

// LoginFocusTargetForStep is the focus destination for arriving at step
// (via ContinueToTOTP or Back): the step's own primary input, so a
// keyboard or screen-reader user is never stranded on a control that no
// longer exists once the previous step's markup unmounts. This only fires
// on the discrete Continue/Back click — never on every keystroke while
// typing — so it cannot interrupt mid-entry the way a focus move driven by
// input events would.
func LoginFocusTargetForStep(step LoginStep) LoginFocusTarget {
	switch step {
	case LoginStepPassword:
		return LoginFocusPassword
	case LoginStepTOTP:
		return LoginFocusTOTP
	default:
		return LoginFocusNone
	}
}

// newLoginFormWithDevPrefill is NewLoginForm plus, in a `-tags devui` build
// only, a working password and a currently-valid TOTP code.
//
// It advances straight to the TOTP step when both are present: stopping on a
// pre-filled password step would still need two clicks, and one-click is the
// entire point. In every other build devLoginPrefill returns nothing and this
// is byte-for-byte NewLoginForm() — the credential is absent from the binary,
// not merely unused.
// Called only from login.go, which is js/wasm-tagged, so staticcheck's
// host-target analysis cannot see the caller — and because its unused
// analysis is transitive, flagging this also flags devLoginPrefill below it.
// Deleting either breaks the wasm build while leaving the host build green.
//
//lint:ignore U1000 called from the js-tagged login.go
func newLoginFormWithDevPrefill(now time.Time) LoginForm {
	f := NewLoginForm()
	password, code, ok := devLoginPrefill(now)
	if !ok {
		return f
	}
	f = f.SetPassword(password)
	if next, advanced := f.ContinueToTOTP(); advanced {
		f = next
	}
	if code != "" {
		f = f.SetTOTPCode(code)
	}
	return f
}
