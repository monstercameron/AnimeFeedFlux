package auth

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/guard"
)

func TestNewRecoverFormStartsOnCodeStep(t *testing.T) {
	f := NewRecoverForm()
	if f.Step != RecoverStepEnterCode {
		t.Fatalf("Step = %v, want RecoverStepEnterCode", f.Step)
	}
	if f.RemainingCodes != -1 {
		t.Fatalf("RemainingCodes = %d, want -1 (unknown before a code is accepted, D1-09)", f.RemainingCodes)
	}
}

func TestCodeAcceptedRecordsRemainingCount(t *testing.T) {
	f := NewRecoverForm().SetCode("ABCDE-FGHJK-MNPQR-STVWX")
	now := time.Now()
	f, _ = f.BeginSubmitCode(now)
	f, ok := f.CodeAccepted(7)
	if !ok {
		t.Fatal("CodeAccepted() ok = false")
	}
	if f.Step != RecoverStepChooseAction {
		t.Fatalf("Step = %v, want RecoverStepChooseAction", f.Step)
	}
	if f.RemainingCodes != 7 {
		t.Fatalf("RemainingCodes = %d, want 7", f.RemainingCodes)
	}
	if f.LowOnCodes() {
		t.Fatal("LowOnCodes() = true at 7 remaining")
	}
}

func TestLowOnCodesThreshold(t *testing.T) {
	for _, tc := range []struct {
		remaining int
		want      bool
	}{{-1, false}, {0, true}, {1, true}, {2, true}, {3, false}, {10, false}} {
		f := RecoverForm{RemainingCodes: tc.remaining}
		if got := f.LowOnCodes(); got != tc.want {
			t.Errorf("RemainingCodes=%d: LowOnCodes() = %v, want %v", tc.remaining, got, tc.want)
		}
	}
}

func TestCodeFailedIsGenericAndGrowsBackoff(t *testing.T) {
	now := time.Now()
	f := NewRecoverForm().SetCode("wrong")
	f, _ = f.BeginSubmitCode(now)
	f = f.CodeFailed(now)
	if f.Submitting {
		t.Fatal("Submitting = true after CodeFailed")
	}
	if f.ConsecutiveFails != 1 {
		t.Fatalf("ConsecutiveFails = %d, want 1", f.ConsecutiveFails)
	}
	if f.Step != RecoverStepEnterCode {
		t.Fatalf("Step = %v, want unchanged RecoverStepEnterCode", f.Step)
	}
}

// TestElevatedChoiceIsMutuallyExclusive is doc.go assumption #3's pure-state
// proof: after a code is accepted, choosing one action moves the form away
// from RecoverStepChooseAction entirely — there is no state that lets both
// ChoosePasswordReset and ChooseReenrollTOTP be taken in the same session.
func TestElevatedChoiceIsMutuallyExclusive(t *testing.T) {
	base, _ := NewRecoverForm().CodeAccepted(5)

	viaReset, ok := base.ChoosePasswordReset()
	if !ok || viaReset.Step != RecoverStepResetPassword {
		t.Fatalf("ChoosePasswordReset(): ok=%v step=%v", ok, viaReset.Step)
	}
	if _, ok := viaReset.ChooseReenrollTOTP(); ok {
		t.Fatal("ChooseReenrollTOTP() succeeded from RecoverStepResetPassword — the choice must be exclusive")
	}

	viaReenroll, ok := base.ChooseReenrollTOTP()
	if !ok || viaReenroll.Step != RecoverStepReenrollTOTP {
		t.Fatalf("ChooseReenrollTOTP(): ok=%v step=%v", ok, viaReenroll.Step)
	}
	if _, ok := viaReenroll.ChoosePasswordReset(); ok {
		t.Fatal("ChoosePasswordReset() succeeded from RecoverStepReenrollTOTP — the choice must be exclusive")
	}
}

func TestResetPasswordValidation(t *testing.T) {
	f, _ := NewRecoverForm().CodeAccepted(5)
	f, _ = f.ChoosePasswordReset()

	f = f.SetNewPassword("short")
	f = f.SetConfirmPassword("short")
	if f.PasswordLengthOK() {
		t.Fatal("PasswordLengthOK() = true for a 5-char password (min is 15)")
	}
	if f.CanSubmitReset() {
		t.Fatal("CanSubmitReset() = true with a too-short password")
	}

	long := "correct horse battery staple extra"
	f = f.SetNewPassword(long)
	f = f.SetConfirmPassword("different but also long enough")
	if f.PasswordsMatch() {
		t.Fatal("PasswordsMatch() = true for different passwords")
	}
	if f.CanSubmitReset() {
		t.Fatal("CanSubmitReset() = true with mismatched passwords")
	}

	f = f.SetConfirmPassword(long)
	if !f.CanSubmitReset() {
		t.Fatal("CanSubmitReset() = false with a valid, matching, long-enough password")
	}
}

func TestResetSucceededReachesDoneAndClearsPasswords(t *testing.T) {
	f, _ := NewRecoverForm().CodeAccepted(5)
	f, _ = f.ChoosePasswordReset()
	long := "correct horse battery staple extra"
	f = f.SetNewPassword(long).SetConfirmPassword(long)
	f, _ = f.BeginSubmitReset()
	f, ok := f.ResetSucceeded()
	if !ok {
		t.Fatal("ResetSucceeded() ok = false")
	}
	if f.Step != RecoverStepDone {
		t.Fatalf("Step = %v, want RecoverStepDone", f.Step)
	}
	if f.NewPassword != "" || f.ConfirmPassword != "" {
		t.Fatal("password fields not cleared after ResetSucceeded")
	}
}

func TestReenrollDoesNotRequireCurrentPassword(t *testing.T) {
	f, _ := NewRecoverForm().CodeAccepted(5)
	f, _ = f.ChooseReenrollTOTP()
	if !f.CanSubmitReenroll() {
		t.Fatal("CanSubmitReenroll() = false with no password field involved (elevated path needs none)")
	}
	f, _ = f.BeginSubmitReenroll()
	f, ok := f.ReenrollSucceeded()
	if !ok {
		t.Fatal("ReenrollSucceeded() ok = false")
	}
	if f.Step != RecoverStepDone {
		t.Fatalf("Step = %v, want RecoverStepDone", f.Step)
	}
}

// TestElevatedCannotReachAuthedRoutes is D1-11 ("ELEVATED cannot navigate
// to /generate, /history, or /settings") as a pure function. It
// deliberately does NOT reimplement the rule — that would let this
// package's copy silently drift from the one the router guard actually
// enforces — and instead exercises web/guard.Decide (owned by the shell
// wave, imported read-only here) directly, documenting that recover.go's
// own rendering must not offer navigation to those three routes while
// ELEVATED, on top of (not instead of) the guard's enforcement.
func TestElevatedCannotReachAuthedRoutes(t *testing.T) {
	for _, path := range []string{"/generate", "/history", "/settings"} {
		got := guard.Decide(appstate.Elevated, guard.RouteInfo{Path: path, RequiresAuth: true})
		if got.Allow {
			t.Errorf("guard.Decide(Elevated, %s) = Allow, want redirected away", path)
		}
		if got.RedirectTo != "/recover" {
			t.Errorf("guard.Decide(Elevated, %s) redirects to %q, want /recover", path, got.RedirectTo)
		}
	}
	if got := guard.Decide(appstate.Elevated, guard.RouteInfo{Path: "/recover"}); !got.Allow {
		t.Error("guard.Decide(Elevated, /recover) = not Allow, want Allow — ELEVATED's own page must stay reachable")
	}
	if got := guard.Decide(appstate.Elevated, guard.RouteInfo{Path: "/login"}); got.Allow {
		t.Error("guard.Decide(Elevated, /login) = Allow, want redirected — re-entering ordinary login mid-recovery abandons the elevated session's one job")
	}
}

func TestRecoverFocusTargetForStep(t *testing.T) {
	cases := []struct {
		step RecoverStep
		want RecoverFocusTarget
	}{
		{RecoverStepEnterCode, RecoverFocusCode},
		{RecoverStepChooseAction, RecoverFocusNone},
		{RecoverStepResetPassword, RecoverFocusNewPassword},
		{RecoverStepReenrollTOTP, RecoverFocusReenrollHeading},
		{RecoverStepDone, RecoverFocusNone},
	}
	for _, c := range cases {
		if got := RecoverFocusTargetForStep(c.step); got != c.want {
			t.Errorf("RecoverFocusTargetForStep(%v) = %v, want %v", c.step, got, c.want)
		}
	}
}
