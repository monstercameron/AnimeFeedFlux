package auth

import (
	"strings"
	"testing"
)

const setupTestPassword = "correct horse battery staple long enough"

func TestSetupFormHappyPath(t *testing.T) {
	f := NewSetupForm()
	if f.Step != SetupStepPassword {
		t.Fatalf("initial step = %v, want SetupStepPassword", f.Step)
	}

	f = f.SetPassword(setupTestPassword).SetConfirmPassword(setupTestPassword)
	if !f.CanSubmit() {
		t.Fatal("matching, policy-length password should be submittable")
	}

	f, ok := f.BeginSubmit()
	if !ok || !f.Submitting {
		t.Fatal("BeginSubmit should mark the form in-flight")
	}

	codes := []string{"code-one", "code-two"}
	f, ok = f.SubmitSucceeded("otpauth://totp/x?secret=ABC", codes)
	if !ok || f.Step != SetupStepDone {
		t.Fatal("SubmitSucceeded should reach the Done step")
	}
	if f.Password != "" || f.ConfirmPassword != "" {
		t.Fatal("password fields must be cleared on success")
	}
	if f.ProvisioningURI == "" || len(f.RecoveryCodes) != 2 {
		t.Fatal("Done step must carry the shown-once values")
	}

	if f.CanLeave() {
		t.Fatal("exit must be gated on the saved confirmation")
	}
	f = f.SetSaved(true)
	if !f.CanLeave() {
		t.Fatal("confirmed-saved Done step should allow leaving")
	}
}

func TestSetupFormValidationGates(t *testing.T) {
	f := NewSetupForm()

	f = f.SetPassword("short").SetConfirmPassword("short")
	if f.PasswordLengthOK() || f.CanSubmit() {
		t.Fatal("a password under the 15-char floor must not be submittable")
	}

	long := strings.Repeat("x", maxRecoveryPasswordLen+1)
	f = NewSetupForm().SetPassword(long).SetConfirmPassword(long)
	if f.PasswordLengthOK() || f.CanSubmit() {
		t.Fatal("a password over the 128-char ceiling must not be submittable")
	}

	f = NewSetupForm().SetPassword(setupTestPassword).SetConfirmPassword(setupTestPassword + "!")
	if f.PasswordsMatch() || f.CanSubmit() {
		t.Fatal("mismatched confirmation must not be submittable")
	}
}

func TestSetupFormGuardsAgainstOutOfOrderTransitions(t *testing.T) {
	// Editing mid-flight is a no-op, same rule as LoginForm.
	f := NewSetupForm().SetPassword(setupTestPassword).SetConfirmPassword(setupTestPassword)
	f, _ = f.BeginSubmit()
	if got := f.SetPassword("changed mid-flight"); got.Password != setupTestPassword {
		t.Fatal("SetPassword must be a no-op while submitting")
	}

	// A second BeginSubmit while in-flight is refused.
	if _, ok := f.BeginSubmit(); ok {
		t.Fatal("BeginSubmit while submitting must be refused")
	}

	// SubmitFailed returns to an editable password step.
	f = f.SubmitFailed()
	if f.Submitting || f.Step != SetupStepPassword {
		t.Fatal("SubmitFailed should stay on an editable password step")
	}

	// SubmitSucceeded from Done is refused, and Saved is meaningless off
	// the Done step.
	done, _ := func() (SetupForm, bool) {
		g, _ := f.BeginSubmit()
		return g.SubmitSucceeded("uri", []string{"c"})
	}()
	if _, ok := done.SubmitSucceeded("uri2", nil); ok {
		t.Fatal("SubmitSucceeded must be refused off the password step")
	}
	if g := NewSetupForm().SetSaved(true); g.Saved || g.CanLeave() {
		t.Fatal("SetSaved must be a no-op off the Done step")
	}
}
