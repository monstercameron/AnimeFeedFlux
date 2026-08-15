//go:build js && wasm

package auth

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/router"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// SetupPageProps wires /setup to the live control-plane client. Client is
// required; there is no OnSuccess seam because a successful Setup changes no
// session state at all (the RPC never mints one — the operator proves the
// enrollment worked by signing in through /login afterward), so there is
// nothing for the shell to be told.
type SetupPageProps struct {
	Client SetupClient
}

// SetupPage is /setup's root component: first-run admin account creation,
// the web counterpart of `aff admin init` (PLAN.md §4). Deliberately as
// plain as /login: one form, then the shown-once enrollment screen. It
// works exactly once per instance; on a claimed instance every submit gets
// the server's one generic refusal, rendered as setup.unavailable with the
// way to /login.
func SetupPage(props SetupPageProps) ui.Node {
	intl := gwci18n.UseI18n()
	t := intl.NS(afi18n.NSAuth)
	tc := intl.NS(afi18n.NSCommon)
	nav := router.UseNavigate()
	announcer := ui.UseAnnouncer()
	focus := ui.UseFocusManager()

	form := ui.UseState(NewSetupForm())
	// errKey mirrors login.go's errKey (a key, not a bool). Unlike login it
	// can name keys from TWO namespaces: setup.* causes (auth namespace —
	// already-claimed, server-rejected password) and the shared transport/
	// generic causes (common namespace). setupErrorText below resolves by
	// prefix so the two never need separate state.
	errKey := ui.UseState("")

	passwordID := ui.UseId() + "-password"
	confirmID := ui.UseId() + "-confirm"
	errorID := ui.UseId() + "-error"
	savedConfirmID := ui.UseId() + "-savedconfirm"

	f := form.Get()

	setupErrorText := func(key string) string {
		if len(key) > len("setup.") && key[:len("setup.")] == "setup." {
			return t.T(key)
		}
		return tc.T(key)
	}

	handlePassword := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetPassword(e.GetValue()))
	})
	handleConfirm := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetConfirmPassword(e.GetValue()))
	})
	handleToggleSaved := ui.UseEvent(func(e ui.InputEvent) {
		form.Set(form.Get().SetSaved(e.IsChecked()))
	})
	handleGoToLogin := ui.UseEvent(func() {
		if !form.Get().CanLeave() {
			return
		}
		// Replace, not Push — Back must not land on the shown-once screen
		// after the operator has left it (mirrors login.go's D1-05
		// reasoning), and the values are gone from the server side anyway.
		nav.Replace("/login")
	})

	handleSubmit := ui.UseEvent(func(e ui.FormEvent) {
		e.PreventDefault()
		next, ok := form.Get().BeginSubmit()
		if !ok {
			return
		}
		errKey.Set("")
		form.Set(next)
		password := next.Password
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := props.Client.Setup(ctx, &affv1.AuthServiceSetupRequest{Password: password})
			if err != nil || resp == nil {
				form.Set(form.Get().SubmitFailed())
				// Cause classification, narrowest first. The two setup.*
				// causes are the only places this page says more than
				// login would: both messages restate what the server
				// itself already disclosed (its one generic refusal; its
				// password-policy feedback), never a distinction the page
				// inferred on its own.
				key := afi18n.KeyCommonGenericAuthError
				switch {
				case shell.IsDisconnected(err):
					key = AuthErrorKey(true, afi18n.KeyCommonGenericAuthError)
				case status.Code(err) == codes.FailedPrecondition:
					key = afi18n.KeySetupUnavailable
				case status.Code(err) == codes.InvalidArgument:
					key = afi18n.KeySetupPasswordRejected
				}
				errKey.Set(key)
				announcer.Assertive(setupErrorText(key))
				focus.FocusSelector("#" + errorID)
				return
			}
			done, _ := form.Get().SubmitSucceeded(resp.ProvisioningUri, resp.RecoveryCodes)
			form.Set(done)
			announcer.Polite(t.T(afi18n.KeySetupDoneHeading))
		}()
	})

	// Sighted error paragraph: same no-live-region shape as login.go's
	// errorNode (the announcer speaks it; focus moves to it), and always
	// mounted so the focus target exists.
	var errorNode ui.Node
	if errKey.Get() != "" {
		errorNode = h.P(h.ID(errorID), h.ClassStr("af-form-error"), setupErrorText(errKey.Get()))
	} else {
		errorNode = h.P(h.ID(errorID), h.ClassStr("af-form-error"))
	}

	var body ui.Node
	switch f.Step {
	case SetupStepPassword:
		// Client-side validation messages, shown only once both fields have
		// content — typing the first character of a 15-char minimum must
		// not immediately scold.
		var validationNode ui.Node = h.P(h.ClassStr("af-field-hint"))
		switch {
		case f.Password != "" && !f.PasswordLengthOK():
			validationNode = h.P(h.ClassStr("af-form-error"),
				t.T(afi18n.KeySetupPasswordTooShort, gwci18n.Arguments{"min": minRecoveryPasswordLen}))
			if len(f.Password) > maxRecoveryPasswordLen {
				validationNode = h.P(h.ClassStr("af-form-error"),
					t.T(afi18n.KeySetupPasswordTooLong, gwci18n.Arguments{"max": maxRecoveryPasswordLen}))
			}
		case f.ConfirmPassword != "" && !f.PasswordsMatch():
			validationNode = h.P(h.ClassStr("af-form-error"), t.T(afi18n.KeySetupPasswordMismatch))
		}

		submitLabel := t.T(afi18n.KeySetupSubmit)
		if f.Submitting {
			submitLabel = tc.T(afi18n.KeyCommonSubmitting)
		}
		body = h.Form(h.OnSubmit(handleSubmit), h.ClassStr("af-auth-form"),
			h.Div(
				h.ClassStr("af-login-step"),
				h.Label(h.For(passwordID), t.T(afi18n.KeySetupPasswordLabel)),
				h.Input(
					h.ID(passwordID),
					h.Type("password"),
					h.AutoComplete("new-password"),
					h.Value(f.Password),
					h.OnInput(handlePassword),
					h.Aria("required", "true"),
					h.Aria("describedby", passwordID+"-hint"),
					h.Disabled(f.Submitting),
				),
				h.P(h.ID(passwordID+"-hint"), h.ClassStr("af-field-hint"), t.T(afi18n.KeySetupPasswordHint)),
				h.Label(h.For(confirmID), t.T(afi18n.KeySetupConfirmPasswordLabel)),
				h.Input(
					h.ID(confirmID),
					h.Type("password"),
					h.AutoComplete("new-password"),
					h.Value(f.ConfirmPassword),
					h.OnInput(handleConfirm),
					h.Aria("required", "true"),
					h.Disabled(f.Submitting),
				),
				validationNode,
				h.Button(h.Type("submit"), h.Disabled(!f.CanSubmit()), submitLabel),
			),
			errorNode,
		)
	case SetupStepDone:
		// The provisioning URI and recovery codes are the "shown once,
		// stored hashed" values (PLAN.md §4): plain selectable text, and the
		// exit stays disabled until the checkbox confirms both were saved —
		// same stray-click protection as recover.go's re-enroll screen.
		codeItems := make([]any, 0, len(f.RecoveryCodes)+1)
		codeItems = append(codeItems, h.ClassStr("af-recovery-codes"))
		for _, c := range f.RecoveryCodes {
			codeItems = append(codeItems, h.Li(h.ClassStr("af-code"), h.Text(c)))
		}
		body = h.Div(
			h.ClassStr("af-recover-done"),
			h.H2(t.T(afi18n.KeySetupDoneHeading)),
			h.P(h.ClassStr("af-code"), t.T(afi18n.KeySetupProvisioned, gwci18n.Arguments{"uri": f.ProvisioningURI})),
			h.H3(t.T(afi18n.KeySetupCodesHeading)),
			h.P(t.T(afi18n.KeySetupCodesIntro)),
			h.Ul(codeItems...),
			h.Div(
				h.ClassStr("af-recover-saved-confirm"),
				h.Input(
					h.ID(savedConfirmID),
					h.Type("checkbox"),
					h.Checked(f.Saved),
					h.OnChange(handleToggleSaved),
				),
				h.Label(h.For(savedConfirmID), t.T(afi18n.KeySetupSavedConfirm)),
			),
			h.Button(h.Type("button"), h.OnClick(handleGoToLogin), h.Disabled(!f.CanLeave()), t.T(afi18n.KeySetupGoToLogin)),
		)
	}

	return h.Main(
		h.ClassStr("af-auth-page"),
		announcer.Region(),
		renderAuthBrand(),
		h.H1(t.T(afi18n.KeySetupTitle)),
		h.P(t.T(afi18n.KeySetupIntro)),
		body,
	)
}
