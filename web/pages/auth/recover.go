//go:build js && wasm

package auth

import (
	"context"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// RecoverPageProps wires /recover to the shell's live control-plane
// client. OnElevated fires the moment RecoverWithCode succeeds — the
// shell uses it to transition ANON -> ELEVATED
// (web/appstate.EvRecoveryCodeAccepted) so the route guard starts
// enforcing D1-11 immediately, not only after this component re-renders.
// OnComplete fires once the chosen elevated action (password reset OR
// TOTP re-enrollment — see doc.go assumption #3) succeeds and the
// elevated session has ended server-side; the shell uses it to
// transition ELEVATED -> ANON (web/appstate.EvRecoveryComplete).
type RecoverPageProps struct {
	Client     RecoverClient
	OnElevated func(session *affv1.Session)
	OnComplete func()
}

// RecoverPage is /recover's root component (PLAN.md §12.2, D1-07..11).
func RecoverPage(props RecoverPageProps) ui.Node {
	intl := gwci18n.UseI18n()
	t := intl.NS(afi18n.NSAuth)
	tc := intl.NS(afi18n.NSCommon)
	nav := router.UseNavigate()
	announcer := ui.UseAnnouncer()
	focus := ui.UseFocusManager()

	form := ui.UseState(NewRecoverForm())
	failed := ui.UseState(false)
	provisioningURI := ui.UseState("")
	clock := ui.UseState(time.Now())

	codeID := ui.UseId() + "-code"
	newPasswordID := ui.UseId() + "-newpw"
	confirmPasswordID := ui.UseId() + "-confirmpw"
	errorID := ui.UseId() + "-error"

	ui.UseMount(func() func() {
		ticker := time.NewTicker(time.Second)
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-ticker.C:
					clock.Set(time.Now())
				case <-stop:
					return
				}
			}
		}()
		return func() {
			ticker.Stop()
			close(stop)
		}
	})

	now := clock.Get()
	f := form.Get()

	handleCode := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetCode(e.GetValue()))
	})
	handleNewPassword := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetNewPassword(e.GetValue()))
	})
	handleConfirmPassword := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetConfirmPassword(e.GetValue()))
	})

	handleCodeSubmit := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		submitNow := time.Now()
		next, ok := form.Get().BeginSubmitCode(submitNow)
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)

		code := next.Code
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := props.Client.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: code})
			completedAt := time.Now()
			if err != nil || resp == nil || resp.Session == nil {
				form.Set(form.Get().CodeFailed(completedAt))
				failed.Set(true)
				announcer.Assertive(tc.T(afi18n.KeyCommonGenericAuthError))
				focus.FocusSelector("#" + errorID)
				return
			}
			if props.OnElevated != nil {
				props.OnElevated(resp.Session)
			}
			nextForm, ok := form.Get().CodeAccepted(int(resp.RemainingRecoveryCodes))
			if ok {
				form.Set(nextForm)
			}
			announcer.Polite(t.T(afi18n.KeyRecoverChooseActionHeading))
		}()
	})

	handleChoosePasswordReset := ui.UseEvent(func() {
		next, ok := form.Get().ChoosePasswordReset()
		if !ok {
			return
		}
		form.Set(next)
		focus.FocusSelector("#" + newPasswordID)
	})
	handleChooseReenrollTOTP := ui.UseEvent(func() {
		next, ok := form.Get().ChooseReenrollTOTP()
		if !ok {
			return
		}
		form.Set(next)
	})

	handleResetSubmit := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		next, ok := form.Get().BeginSubmitReset()
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)

		newPassword := next.NewPassword
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// Elevated session: CurrentPassword/TotpCode are unused server
			// side (identity already re-proven by the recovery code —
			// AuthServiceChangePasswordRequest's doc, mirrored in
			// recover_state.go's CanSubmitReenroll comment for the
			// sibling call).
			_, err := props.Client.ChangePassword(ctx, &affv1.AuthServiceChangePasswordRequest{
				NewPassword: newPassword,
			})
			if err != nil {
				form.Set(form.Get().ResetFailed())
				failed.Set(true)
				announcer.Assertive(tc.T(afi18n.KeyCommonGenericAuthError))
				focus.FocusSelector("#" + errorID)
				return
			}
			nextForm, ok := form.Get().ResetSucceeded()
			if ok {
				form.Set(nextForm)
			}
			if props.OnComplete != nil {
				props.OnComplete()
			}
		}()
	})

	handleReenrollSubmit := ui.UseEvent(func() {
		next, ok := form.Get().BeginSubmitReenroll()
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := props.Client.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{})
			if err != nil || resp == nil {
				form.Set(form.Get().ReenrollFailed())
				failed.Set(true)
				announcer.Assertive(tc.T(afi18n.KeyCommonGenericAuthError))
				focus.FocusSelector("#" + errorID)
				return
			}
			provisioningURI.Set(resp.ProvisioningUri)
			nextForm, ok := form.Get().ReenrollSucceeded()
			if ok {
				form.Set(nextForm)
			}
			if props.OnComplete != nil {
				props.OnComplete()
			}
		}()
	})

	handleGoToLogin := ui.UseEvent(func() {
		nav.Replace("/login")
	})

	remaining := f.RemainingBackoff(now)
	blocked := remaining > 0

	var errorNode ui.Node
	if failed.Get() {
		errorNode = h.P(h.ID(errorID), h.Aria("live", "assertive"), h.ClassStr("af-form-error"), tc.T(afi18n.KeyCommonGenericAuthError))
	} else {
		errorNode = h.P(h.ID(errorID), h.ClassStr("af-form-error"))
	}

	var backoffNode ui.Node
	if blocked {
		backoffNode = h.P(h.Role("status"), h.ClassStr("af-backoff-notice"),
			tc.T(afi18n.KeyCommonBackoffNotice, gwci18n.Arguments{"count": BackoffSecondsCeil(remaining)}))
	} else {
		backoffNode = h.P(h.ClassStr("af-backoff-notice"))
	}

	breakGlass := h.Section(
		h.ClassStr("af-recover-breakglass"),
		h.H2(t.T(afi18n.KeyRecoverBreakGlassHeading)),
		h.P(t.T(afi18n.KeyRecoverBreakGlassBody)),
		h.P(h.ClassStr("af-code"), t.T(afi18n.KeyRecoverBreakGlassCommandNote)),
	)

	var body ui.Node
	switch f.Step {
	case RecoverStepEnterCode:
		body = h.Form(
			h.OnSubmit(handleCodeSubmit),
			h.ClassStr("af-auth-form"),
			h.Label(h.For(codeID), t.T(afi18n.KeyRecoverCodeLabel)),
			h.Input(
				h.ID(codeID),
				h.Type("text"),
				h.AutoComplete("off"),
				h.Aria("describedby", codeID+"-hint"),
				h.Value(f.Code),
				h.OnInput(handleCode),
				h.Disabled(f.Submitting),
			),
			h.P(h.ID(codeID+"-hint"), h.ClassStr("af-field-hint"), t.T(afi18n.KeyRecoverCodeHint)),
			h.Button(h.Type("submit"), h.Disabled(!f.CanSubmitCode(now)), t.T(afi18n.KeyRecoverCodeSubmit)),
			errorNode,
			backoffNode,
		)
	case RecoverStepChooseAction:
		var lowCodesNode ui.Node = h.P()
		if f.LowOnCodes() {
			lowCodesNode = h.P(h.Role("status"), h.ClassStr("af-low-codes-warning"), t.T(afi18n.KeyRecoverLowCodesWarning))
		}
		body = h.Div(
			h.ClassStr("af-recover-choice"),
			h.P(h.Role("status"), t.T(afi18n.KeyRecoverRemainingCodes, gwci18n.Arguments{"count": f.RemainingCodes})),
			lowCodesNode,
			h.H2(t.T(afi18n.KeyRecoverChooseActionHeading)),
			h.P(t.T(afi18n.KeyRecoverChooseActionIntro)),
			h.Div(
				h.ClassStr("af-recover-actions"),
				h.Button(h.Type("button"), h.OnClick(handleChoosePasswordReset), t.T(afi18n.KeyRecoverChoosePasswordReset)),
				h.Button(h.Type("button"), h.OnClick(handleChooseReenrollTOTP), t.T(afi18n.KeyRecoverChooseReenrollTOTP)),
			),
		)
	case RecoverStepResetPassword:
		submitLabel := t.T(afi18n.KeyRecoverResetSubmit)
		if f.Submitting {
			submitLabel = tc.T(afi18n.KeyCommonSubmitting)
		}
		var mismatchNode ui.Node = h.P()
		if f.NewPassword != "" && f.ConfirmPassword != "" && !f.PasswordsMatch() {
			mismatchNode = h.P(h.Role("alert"), h.ClassStr("af-field-error"), t.T(afi18n.KeyRecoverPasswordMismatch))
		}
		body = h.Form(
			h.OnSubmit(handleResetSubmit),
			h.ClassStr("af-auth-form"),
			h.Label(h.For(newPasswordID), t.T(afi18n.KeyRecoverNewPasswordLabel)),
			h.Input(h.ID(newPasswordID), h.Type("password"), h.AutoComplete("new-password"),
				h.Value(f.NewPassword), h.OnInput(handleNewPassword), h.Disabled(f.Submitting)),
			h.Label(h.For(confirmPasswordID), t.T(afi18n.KeyRecoverConfirmPasswordLabel)),
			h.Input(h.ID(confirmPasswordID), h.Type("password"), h.AutoComplete("new-password"),
				h.Value(f.ConfirmPassword), h.OnInput(handleConfirmPassword), h.Disabled(f.Submitting)),
			mismatchNode,
			h.Button(h.Type("submit"), h.Disabled(!f.CanSubmitReset()), submitLabel),
			errorNode,
		)
	case RecoverStepReenrollTOTP:
		submitLabel := t.T(afi18n.KeyRecoverReenrollSubmit)
		if f.Submitting {
			submitLabel = tc.T(afi18n.KeyCommonSubmitting)
		}
		body = h.Div(
			h.ClassStr("af-recover-reenroll"),
			h.H2(t.T(afi18n.KeyRecoverReenrollHeading)),
			h.P(t.T(afi18n.KeyRecoverReenrollIntro)),
			h.Button(h.Type("button"), h.OnClick(handleReenrollSubmit), h.Disabled(f.Submitting), submitLabel),
			errorNode,
		)
	case RecoverStepDone:
		var qrNode ui.Node = h.P()
		if provisioningURI.Get() != "" {
			qrNode = h.P(h.ClassStr("af-code"), t.T(afi18n.KeyRecoverReenrollProvisioned, gwci18n.Arguments{"uri": provisioningURI.Get()}))
		}
		body = h.Div(
			h.ClassStr("af-recover-done"),
			h.H2(t.T(afi18n.KeyRecoverDoneHeading)),
			h.P(t.T(afi18n.KeyRecoverDoneBody)),
			qrNode,
			h.Button(h.Type("button"), h.OnClick(handleGoToLogin), t.T(afi18n.KeyRecoverDoneGoToLogin)),
		)
	}

	return h.Main(
		h.ClassStr("af-auth-page"),
		announcer.Region(),
		h.H1(t.T(afi18n.KeyRecoverTitle)),
		h.P(t.T(afi18n.KeyRecoverIntro)),
		body,
		breakGlass,
	)
}
