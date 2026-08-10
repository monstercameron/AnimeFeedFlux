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
	"github.com/monstercameron/AnimeFeedFlux/web/guard"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
)

// LoginPageProps wires /login to the shell's live control-plane client and
// session bookkeeping. Client is required. OnSuccess is called once the
// Login RPC succeeds, BEFORE this component navigates away — the shell
// uses it to transition its own appstate (ANON -> AUTH,
// web/appstate.EvLoginSuccess) and record the session; this package does
// not import web/appstate itself so that a session-bookkeeping change
// never requires touching this page.
type LoginPageProps struct {
	Client    LoginClient
	OnSuccess func(session *affv1.Session)
}

// LoginPage is /login's root component (PLAN.md §12.1, D1-01..06). It is
// deliberately plain: one form, two steps, no client-side niceties that
// could themselves become a reason the one page that "must never break"
// breaks.
func LoginPage(props LoginPageProps) ui.Node {
	intl := gwci18n.UseI18n()
	t := intl.NS(afi18n.NSAuth)
	tc := intl.NS(afi18n.NSCommon)
	nav := router.UseNavigate()
	announcer := ui.UseAnnouncer()
	focus := ui.UseFocusManager()

	form := ui.UseState(NewLoginForm())
	failed := ui.UseState(false)
	clock := ui.UseState(time.Now())

	passwordID := ui.UseId() + "-password"
	totpID := ui.UseId() + "-totp"
	errorID := ui.UseId() + "-error"

	// Re-render once a second so a live backoff countdown actually counts
	// down (D1-03: "honestly", not a number frozen at the moment it first
	// appeared).
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

	handlePassword := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetPassword(e.GetValue()))
	})
	handleTOTP := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetTOTPCode(e.GetValue()))
	})
	handleContinue := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		next, ok := form.Get().ContinueToTOTP()
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)
		announcer.Polite(t.T(afi18n.KeyLoginTOTPStepLabel))
		focus.FocusSelector("#" + totpID)
	})
	handleBack := ui.UseEvent(func() {
		next, ok := form.Get().Back()
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)
		focus.FocusSelector("#" + passwordID)
	})
	handleSubmit := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		submitNow := time.Now()
		next, ok := form.Get().BeginSubmit(submitNow)
		if !ok {
			return
		}
		failed.Set(false)
		form.Set(next)

		password, code := next.Password, next.TOTPCode
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := props.Client.Login(ctx, &affv1.AuthServiceLoginRequest{
				Password: password,
				TotpCode: code,
			})
			completedAt := time.Now()
			if err != nil || resp == nil || resp.Session == nil {
				form.Set(form.Get().SubmitFailed(completedAt))
				failed.Set(true)
				announcer.Assertive(tc.T(afi18n.KeyCommonGenericAuthError))
				focus.FocusSelector("#" + errorID)
				return
			}
			if props.OnSuccess != nil {
				props.OnSuccess(resp.Session)
			}
			form.Set(form.Get().SubmitSucceeded())
			// D1-05: replace history state on success so Back cannot land
			// on a stale login form.
			nav.Replace(guard.DefaultAuthed)
		}()
	})

	remaining := f.RemainingBackoff(now)
	blocked := remaining > 0

	var errorNode ui.Node
	if failed.Get() {
		errorNode = h.P(
			h.ID(errorID),
			h.Aria("live", "assertive"),
			h.ClassStr("af-form-error"),
			tc.T(afi18n.KeyCommonGenericAuthError),
		)
	} else {
		errorNode = h.P(h.ID(errorID), h.ClassStr("af-form-error"))
	}

	var backoffNode ui.Node
	if blocked {
		backoffNode = h.P(
			h.Role("status"),
			h.ClassStr("af-backoff-notice"),
			tc.T(afi18n.KeyCommonBackoffNotice, gwci18n.Arguments{"count": BackoffSecondsCeil(remaining)}),
		)
	} else {
		backoffNode = h.P(h.ClassStr("af-backoff-notice"))
	}

	var stepNode ui.Node
	switch f.Step {
	case LoginStepPassword:
		stepNode = h.Div(
			h.ClassStr("af-login-step"),
			h.P(h.ClassStr("af-step-label"), t.T(afi18n.KeyLoginPasswordStepLabel)),
			h.Label(h.For(passwordID), t.T(afi18n.KeyLoginPasswordLabel)),
			h.Input(
				h.ID(passwordID),
				h.Type("password"),
				h.AutoComplete("current-password"),
				h.Value(f.Password),
				h.OnInput(handlePassword),
				h.Aria("required", "true"),
			),
			h.Button(
				h.Type("submit"),
				h.Disabled(!f.CanContinue()),
				t.T(afi18n.KeyLoginContinue),
			),
		)
	case LoginStepTOTP:
		submitLabel := t.T(afi18n.KeyLoginSubmit)
		if f.Submitting {
			submitLabel = tc.T(afi18n.KeyCommonSubmitting)
		}
		stepNode = h.Div(
			h.ClassStr("af-login-step"),
			h.P(h.ClassStr("af-step-label"), t.T(afi18n.KeyLoginTOTPStepLabel)),
			h.Label(h.For(totpID), t.T(afi18n.KeyLoginTOTPLabel)),
			h.Input(
				h.ID(totpID),
				h.Type("text"),
				h.AutoComplete("one-time-code"),
				h.Aria("describedby", totpID+"-hint"),
				h.Value(f.TOTPCode),
				h.OnInput(handleTOTP),
				h.Disabled(f.Submitting),
			),
			h.P(h.ID(totpID+"-hint"), h.ClassStr("af-field-hint"), t.T(afi18n.KeyLoginTOTPHint)),
			h.Div(
				h.ClassStr("af-login-actions"),
				h.Button(h.Type("button"), h.OnClick(handleBack), h.Disabled(f.Submitting), tc.T(afi18n.KeyCommonBack)),
				h.Button(h.Type("submit"), h.Disabled(!f.CanSubmit(now)), submitLabel),
			),
		)
	}

	submitHandler := handleSubmit
	if f.Step == LoginStepPassword {
		submitHandler = handleContinue
	}

	return h.Main(
		h.ClassStr("af-auth-page"),
		announcer.Region(),
		h.H1(t.T(afi18n.KeyLoginTitle)),
		h.Form(h.OnSubmit(submitHandler), h.ClassStr("af-auth-form"),
			stepNode,
			errorNode,
			backoffNode,
		),
		h.Nav(
			h.ClassStr("af-auth-links"),
			h.A(h.Href("/recover"), t.T(afi18n.KeyLoginRecoverLink)),
		),
	)
}
