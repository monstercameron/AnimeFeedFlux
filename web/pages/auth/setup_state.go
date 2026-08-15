package auth

import "strings"

// SetupStep is which stage of /setup is showing. Two steps only: enter the
// new password (with confirmation), then the one-time enrollment screen —
// provisioning URI plus recovery codes — which is terminal: neither value
// is ever retrievable again (PLAN.md §4 "shown once, stored hashed"), so
// there is no step after Done, only the gated exit to /login.
type SetupStep int

const (
	SetupStepPassword SetupStep = iota
	SetupStepDone
)

// SetupForm is the pure client-side state for /setup — the same
// no-DOM/no-network shape as LoginForm and RecoverForm, driven by setup.go.
// No backoff machinery: unlike login/recover there is no enrolled secret to
// guess at, and the server's own one-time gate (AuthService.Setup returning
// one generic refusal once the admin row exists) is the only rate control
// this flow needs.
type SetupForm struct {
	Step            SetupStep
	Password        string
	ConfirmPassword string
	Submitting      bool

	// ProvisioningURI and RecoveryCodes are what the successful Setup RPC
	// returned, held only long enough to show once on the Done step.
	ProvisioningURI string
	RecoveryCodes   []string
	// Saved is the "I've saved these" confirmation that gates leaving the
	// Done step — same reasoning as recover.go's saved-confirm checkbox:
	// navigating away without scanning the QR and storing the codes means
	// the only ways back in are a recovery code (unsaved) or break-glass.
	Saved bool
}

// NewSetupForm returns the initial state: the password step, everything
// empty.
func NewSetupForm() SetupForm {
	return SetupForm{Step: SetupStepPassword}
}

// SetPassword/SetConfirmPassword update the password fields. No-op once
// submitting or past the password step, same guard as every other auth form
// here.
func (f SetupForm) SetPassword(v string) SetupForm {
	if f.Submitting || f.Step != SetupStepPassword {
		return f
	}
	f.Password = v
	return f
}

func (f SetupForm) SetConfirmPassword(v string) SetupForm {
	if f.Submitting || f.Step != SetupStepPassword {
		return f
	}
	f.ConfirmPassword = v
	return f
}

// PasswordsMatch reports whether Password and ConfirmPassword agree.
func (f SetupForm) PasswordsMatch() bool {
	return f.Password == f.ConfirmPassword
}

// PasswordLengthOK mirrors the server's auth.IsWeak length bounds through
// the same client-side constants recover_state.go declares
// (minRecoveryPasswordLen/maxRecoveryPasswordLen) — convenience only; the
// server's auth.IsWeak, including the compromised-password list this
// deliberately does not replicate client-side, remains authoritative.
func (f SetupForm) PasswordLengthOK() bool {
	n := len(strings.TrimSpace(f.Password))
	return n >= minRecoveryPasswordLen && n <= maxRecoveryPasswordLen
}

// CanSubmit reports whether the password step's submit is allowed.
func (f SetupForm) CanSubmit() bool {
	return f.Step == SetupStepPassword &&
		!f.Submitting &&
		f.PasswordsMatch() &&
		f.PasswordLengthOK()
}

// BeginSubmit marks the form as in-flight. ok is false (state unchanged) if
// CanSubmit was false — same cannot-skip-the-guard encoding as LoginForm.
func (f SetupForm) BeginSubmit() (SetupForm, bool) {
	if !f.CanSubmit() {
		return f, false
	}
	f.Submitting = true
	return f, true
}

// SubmitFailed clears Submitting and stays on the password step so the
// operator can retry (the component owns WHICH error is shown; this pure
// state does not distinguish causes).
func (f SetupForm) SubmitFailed() SetupForm {
	f.Submitting = false
	return f
}

// SubmitSucceeded moves to the terminal Done step carrying the two
// shown-once values, and clears the password fields — they have done their
// job and nothing on the Done step needs them.
func (f SetupForm) SubmitSucceeded(provisioningURI string, recoveryCodes []string) (SetupForm, bool) {
	if f.Step != SetupStepPassword {
		return f, false
	}
	f.Step = SetupStepDone
	f.Submitting = false
	f.Password = ""
	f.ConfirmPassword = ""
	f.ProvisioningURI = provisioningURI
	f.RecoveryCodes = recoveryCodes
	return f, true
}

// SetSaved records the Done step's confirmation checkbox.
func (f SetupForm) SetSaved(v bool) SetupForm {
	if f.Step != SetupStepDone {
		return f
	}
	f.Saved = v
	return f
}

// CanLeave reports whether the Done step's "go to sign in" exit is enabled:
// only once the operator has confirmed the shown-once values are saved.
func (f SetupForm) CanLeave() bool {
	return f.Step == SetupStepDone && f.Saved
}
