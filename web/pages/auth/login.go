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
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
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

	// In a `-tags devui` build this arrives pre-filled with a working
	// password and a freshly computed TOTP code, so a dev can click straight
	// through. In every other build devLoginPrefill returns nothing and this
	// is exactly NewLoginForm() — see devfill_off.go.
	form := ui.UseState(newLoginFormWithDevPrefill(time.Now()))
	// errKey holds the i18n key currently rendered by errorNode, "" for no
	// error. It carries a KEY rather than a bool so a failed submit can
	// distinguish "reached the server, credentials rejected" (D1-02's one
	// generic string) from "never reached the server at all"
	// (backoff_display.go's AuthErrorKey/keyConnectionUnreachable) without
	// ever distinguishing WHICH credential was wrong — connectivity is not
	// an oracle risk the way account/password/TOTP causes are.
	errKey := ui.UseState("")
	// backoffActive tracks whether THIS form most recently announced a
	// backoff window starting, so the countdown-cleared announcement
	// (ShouldAnnounceBackoffCleared) never fires for a backoff that was
	// never announced as started in the first place (e.g. initial mount).
	backoffActive := ui.UseState(false)
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
	// loginFocusSelector maps LoginFocusTargetForStep's symbolic result onto
	// this render's actual (ui.UseId()-generated) element IDs.
	loginFocusSelector := func(target LoginFocusTarget) string {
		switch target {
		case LoginFocusPassword:
			return "#" + passwordID
		case LoginFocusTOTP:
			return "#" + totpID
		case LoginFocusError:
			return "#" + errorID
		default:
			return ""
		}
	}

	handleContinue := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		next, ok := form.Get().ContinueToTOTP()
		if !ok {
			return
		}
		errKey.Set("")
		form.Set(next)
		announcer.Polite(t.T(afi18n.KeyLoginTOTPStepLabel))
		// Focus moves to the TOTP step's own input ONLY on this explicit
		// Continue click — never while the admin is still typing the
		// password — so a keyboard/screen-reader user is never stranded on
		// a control that just unmounted, and is never interrupted
		// mid-entry either.
		focus.FocusSelector(loginFocusSelector(LoginFocusTargetForStep(next.Step)))
	})
	handleBack := ui.UseEvent(func() {
		next, ok := form.Get().Back()
		if !ok {
			return
		}
		errKey.Set("")
		form.Set(next)
		focus.FocusSelector(loginFocusSelector(LoginFocusTargetForStep(next.Step)))
	})
	handleSubmit := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		submitNow := time.Now()
		next, ok := form.Get().BeginSubmit(submitNow)
		if !ok {
			return
		}
		errKey.Set("")
		form.Set(next)

		// In a `-tags devui` build, refresh an untouched prefilled TOTP code
		// against the clock at SUBMIT time.
		//
		// devfill computes the code once, at mount. A TOTP step is 30
		// seconds, so a dev-build login page left open for longer than that
		// — reading the screen, switching windows, or just retrying after a
		// failure — submits an expired code and is refused with the generic
		// "That didn't work", which reads as wrong credentials rather than
		// as a stale prefill. devfill_on.go's own doc comment names this
		// exact failure ("a prefilled constant would be stale before the
		// page finished loading... it looks like a working one-click login
		// that rejects you") and then reintroduces it one layer up by
		// computing at mount instead of at submit.
		//
		// Only an UNEDITED prefilled value is replaced, so anything typed by
		// hand is submitted exactly as typed. In every other build this is
		// the identity function and the credential is not in the binary at
		// all (devfill_off.go).
		password, code := devRefreshTOTP(next.Password, next.TOTPCode, submitNow)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := props.Client.Login(ctx, &affv1.AuthServiceLoginRequest{
				Password: password,
				TotpCode: code,
			})
			completedAt := time.Now()
			if err != nil || resp == nil || resp.Session == nil {
				failedForm := form.Get().SubmitFailed(completedAt)
				form.Set(failedForm)
				// D0-10's classification: a refusal because the socket was
				// never up is not the same thing as a credential rejection
				// (D1-02), and must not be hidden behind the same silent
				// "nothing happened" the wrong-password case gets — see
				// backoff_display.go's AuthErrorKey doc comment.
				key := AuthErrorKey(shell.IsDisconnected(err), afi18n.KeyCommonGenericAuthError)
				errKey.Set(key)
				announcer.Assertive(tc.T(key))
				remaining := failedForm.RemainingBackoff(completedAt)
				if ShouldAnnounceBackoffStarted(remaining) {
					announcer.Polite(tc.T(afi18n.KeyCommonBackoffNotice, gwci18n.Arguments{"count": BackoffSecondsCeil(remaining)}))
					backoffActive.Set(true)
				}
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

	// The countdown announcement is deliberately NOT wired through a live
	// region that re-renders every second (that would speak once per tick,
	// which is unusable with a screen reader) — this effect only re-runs
	// when `blocked` actually changes value (ui.UseEffectOf's dependency
	// comparison), i.e. exactly at completion. The start announcement
	// happens once, imperatively, in handleSubmit's failure branch above,
	// where the actual wait estimate is first known.
	ui.UseEffectOf(func() func() {
		if ShouldAnnounceBackoffCleared(blocked, backoffActive.Get()) {
			announcer.Polite(tc.T(keyBackoffCleared))
			backoffActive.Set(false)
		}
		return nil
	}, blocked)

	var errorNode ui.Node
	if errKey.Get() != "" {
		errorNode = h.P(
			h.ID(errorID),
			h.Aria("live", "assertive"),
			h.ClassStr("af-form-error"),
			tc.T(errKey.Get()),
		)
	} else {
		errorNode = h.P(h.ID(errorID), h.ClassStr("af-form-error"))
	}

	// backoffNode itself carries NO role="status"/aria-live — it re-renders
	// its visible text every second as `now` ticks (an honest, sighted
	// countdown, D1-03), and a live region that updates once a second is
	// exactly the unusable case this task called out. The one-time
	// start/completion announcements are handled separately above and in
	// handleSubmit's failure branch, through the announcer's own dedicated
	// region (announcer.Region(), rendered once below), not this node.
	var backoffNode ui.Node
	if blocked {
		backoffNode = h.P(
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
				// inputmode="numeric" brings up a numeric keypad on
				// mobile without narrowing accepted input the way
				// type="number" would (which also mangles a leading
				// zero and offers spinner controls that make no sense
				// for a code). No maxlength: a pasted code must land
				// intact, never silently truncated. autocorrect/
				// autocapitalize/spellcheck off: none of the platforms
				// that honor these should be "correcting" a 6-digit
				// code.
				h.Attr("inputmode", "numeric"),
				h.Attr("autocorrect", "off"),
				h.Attr("autocapitalize", "off"),
				h.Attr("spellcheck", "false"),
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
		renderAuthBrand(),
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
