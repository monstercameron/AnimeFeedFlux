package auth

import (
	"testing"
	"time"
)

func TestNewLoginFormStartsOnPasswordStep(t *testing.T) {
	f := NewLoginForm()
	if f.Step != LoginStepPassword {
		t.Fatalf("Step = %v, want LoginStepPassword", f.Step)
	}
	if f.CanContinue() {
		t.Fatal("CanContinue() = true on an empty password")
	}
}

func TestSetPasswordThenContinue(t *testing.T) {
	f := NewLoginForm()
	f = f.SetPassword("hunter2 correct battery staple")
	if !f.CanContinue() {
		t.Fatal("CanContinue() = false with a non-blank password")
	}
	next, ok := f.ContinueToTOTP()
	if !ok {
		t.Fatal("ContinueToTOTP() ok = false, want true")
	}
	if next.Step != LoginStepTOTP {
		t.Fatalf("Step = %v, want LoginStepTOTP", next.Step)
	}
	if next.Password != f.Password {
		t.Fatal("password was not carried across the step change (needed for the single bundled Login RPC — doc.go assumption #1)")
	}
}

func TestContinueToTOTPRejectsBlankPassword(t *testing.T) {
	f := NewLoginForm()
	next, ok := f.ContinueToTOTP()
	if ok {
		t.Fatal("ContinueToTOTP() ok = true with a blank password")
	}
	if next != f {
		t.Fatal("state changed despite ok=false")
	}
}

func TestSetPasswordNoOpAfterAdvancing(t *testing.T) {
	f := NewLoginForm().SetPassword("first")
	f, _ = f.ContinueToTOTP()
	f2 := f.SetPassword("second")
	if f2.Password != "first" {
		t.Fatalf("SetPassword mutated the password field on the TOTP step: got %q", f2.Password)
	}
}

func TestBackClearsTOTPCodeKeepsPassword(t *testing.T) {
	f := NewLoginForm().SetPassword("pw")
	f, _ = f.ContinueToTOTP()
	f = f.SetTOTPCode("123456")
	f, ok := f.Back()
	if !ok {
		t.Fatal("Back() ok = false")
	}
	if f.Step != LoginStepPassword {
		t.Fatalf("Step = %v, want LoginStepPassword", f.Step)
	}
	if f.TOTPCode != "" {
		t.Fatalf("TOTPCode = %q, want cleared", f.TOTPCode)
	}
	if f.Password != "pw" {
		t.Fatalf("Password = %q, want preserved as %q", f.Password, "pw")
	}
}

func TestBackNoOpFromPasswordStep(t *testing.T) {
	f := NewLoginForm()
	next, ok := f.Back()
	if ok {
		t.Fatal("Back() ok = true from the password step")
	}
	if next != f {
		t.Fatal("state changed despite ok=false")
	}
}

func TestCanSubmitRequiresTOTPStepAndCode(t *testing.T) {
	now := time.Now()
	f := NewLoginForm().SetPassword("pw")
	if f.CanSubmit(now) {
		t.Fatal("CanSubmit() = true on the password step")
	}
	f, _ = f.ContinueToTOTP()
	if f.CanSubmit(now) {
		t.Fatal("CanSubmit() = true with a blank TOTP code")
	}
	f = f.SetTOTPCode("123456")
	if !f.CanSubmit(now) {
		t.Fatal("CanSubmit() = false with a non-blank code, not submitting, not blocked")
	}
}

// TestNoDoubleSubmitBurningATOTPWindow is D1-04's namesake requirement.
func TestNoDoubleSubmitBurningATOTPWindow(t *testing.T) {
	now := time.Now()
	f := NewLoginForm().SetPassword("pw")
	f, _ = f.ContinueToTOTP()
	f = f.SetTOTPCode("123456")

	f, ok := f.BeginSubmit(now)
	if !ok {
		t.Fatal("BeginSubmit() ok = false on a submittable form")
	}
	if !f.Submitting {
		t.Fatal("Submitting = false after BeginSubmit")
	}
	if f.CanSubmit(now) {
		t.Fatal("CanSubmit() = true while already Submitting — a second submit would burn a second TOTP code for nothing")
	}
	if _, ok := f.BeginSubmit(now); ok {
		t.Fatal("BeginSubmit() ok = true while already Submitting")
	}
}

func TestSubmitFailedGrowsBackoffAndClearsSubmitting(t *testing.T) {
	now := time.Now()
	f := NewLoginForm().SetPassword("pw")
	f, _ = f.ContinueToTOTP()
	f = f.SetTOTPCode("123456")
	f, _ = f.BeginSubmit(now)

	f = f.SubmitFailed(now)
	if f.Submitting {
		t.Fatal("Submitting = true after SubmitFailed")
	}
	if f.ConsecutiveFails != 1 {
		t.Fatalf("ConsecutiveFails = %d, want 1", f.ConsecutiveFails)
	}
	// One failure is within grace (backoffGrace=2), so no block yet.
	if f.Blocked(now) {
		t.Fatal("Blocked() = true after only one failure (within grace)")
	}

	// Two more failures cross the grace period.
	f, _ = f.BeginSubmit(now)
	f = f.SubmitFailed(now)
	f, _ = f.BeginSubmit(now)
	f = f.SubmitFailed(now)
	if !f.Blocked(now) {
		t.Fatal("Blocked() = false after 3 consecutive failures, want true (past grace=2)")
	}
	if f.CanSubmit(now) {
		t.Fatal("CanSubmit() = true while inside the backoff window")
	}
	future := now.Add(f.RemainingBackoff(now) + time.Millisecond)
	if f.Blocked(future) {
		t.Fatal("Blocked() = true after the estimated window elapsed")
	}
}

func TestLoginFocusTargetForStep(t *testing.T) {
	cases := []struct {
		step LoginStep
		want LoginFocusTarget
	}{
		{LoginStepPassword, LoginFocusPassword},
		{LoginStepTOTP, LoginFocusTOTP},
	}
	for _, c := range cases {
		if got := LoginFocusTargetForStep(c.step); got != c.want {
			t.Errorf("LoginFocusTargetForStep(%v) = %v, want %v", c.step, got, c.want)
		}
	}
}

func TestSubmitSucceededResetsEverything(t *testing.T) {
	now := time.Now()
	f := NewLoginForm().SetPassword("pw")
	f, _ = f.ContinueToTOTP()
	f = f.SetTOTPCode("123456")
	f, _ = f.BeginSubmit(now)
	f = f.SubmitFailed(now)

	f = f.SubmitSucceeded()
	if f != NewLoginForm() {
		t.Fatalf("SubmitSucceeded() = %+v, want a fresh NewLoginForm()", f)
	}
}
