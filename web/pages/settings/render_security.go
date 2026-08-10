//go:build js && wasm

package settings

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderSecurity is the Security section (D4-01..04): change password,
// re-enroll TOTP, regenerate recovery codes, active sessions.
func renderSecurity() ui.Node {
	// --- sessions list state ---
	sessions := ui.UseState([]SessionRow(nil))
	sessionsLoading := ui.UseState(true)
	sessionsErr := ui.UseState(error(nil))
	revokeAllVisible := ui.UseState(false)

	ui.UseEffect(func() func() {
		loadSessions(sessions, sessionsLoading, sessionsErr)
		return nil
	}, "security-sessions-mount")

	// --- change password state ---
	currentPassword := ui.UseState("")
	newPassword := ui.UseState("")
	totpCode := ui.UseState("")
	pwSubmitting := ui.UseState(false)
	pwErr := ui.UseState(error(nil))
	pwSuccess := ui.UseState(false)

	// --- TOTP re-enrollment state ---
	reenrollPassword := ui.UseState("")
	provisioningURI := ui.UseState("")
	reenrollErr := ui.UseState(error(nil))
	reenrollSubmitting := ui.UseState(false)

	// --- recovery codes state ---
	regenVisible := ui.UseState(false)
	newCodes := ui.UseState([]string(nil))
	remainingHint := ui.UseState(-1) // -1 = unknown until regenerated once this session
	regenErr := ui.UseState(error(nil))

	doChangePassword := func() {
		if pwSubmitting.Get() {
			return
		}
		if err := ValidatePasswordLength(newPassword.Get()); err != nil {
			pwErr.Set(err)
			return
		}
		pwSubmitting.Set(true)
		pwErr.Set(nil)
		go func() {
			_, err := deps.Auth.ChangePassword(bgContext(), &affv1.AuthServiceChangePasswordRequest{
				CurrentPassword: currentPassword.Get(),
				TotpCode:        totpCode.Get(),
				NewPassword:     newPassword.Get(),
			})
			pwSubmitting.Set(false)
			if err != nil {
				pwErr.Set(err)
				return
			}
			currentPassword.Set("")
			newPassword.Set("")
			totpCode.Set("")
			pwSuccess.Set(true)
			// SEC-46: this call already revoked every OTHER session
			// server-side. Refresh the list so it reflects that rather
			// than showing stale rows.
			loadSessions(sessions, sessionsLoading, sessionsErr)
		}()
	}

	doReenrollTOTP := func() {
		if reenrollSubmitting.Get() {
			return
		}
		reenrollSubmitting.Set(true)
		reenrollErr.Set(nil)
		go func() {
			resp, err := deps.Auth.ReenrollTOTP(bgContext(), &affv1.AuthServiceReenrollTOTPRequest{
				CurrentPassword: reenrollPassword.Get(),
			})
			reenrollSubmitting.Set(false)
			if err != nil {
				reenrollErr.Set(err)
				return
			}
			reenrollPassword.Set("")
			provisioningURI.Set(resp.GetProvisioningUri())
		}()
	}

	doRegenerateCodes := func() {
		regenErr.Set(nil)
		go func() {
			resp, err := deps.Auth.RegenerateRecoveryCodes(bgContext(), &affv1.AuthServiceRegenerateRecoveryCodesRequest{
				CurrentPassword: currentPassword.Get(),
				TotpCode:        totpCode.Get(),
			})
			if err != nil {
				regenErr.Set(err)
				return
			}
			codes := resp.GetRecoveryCodes()
			newCodes.Set(codes)
			remainingHint.Set(len(codes))
		}()
	}

	doRevokeOne := func(sessionID string) {
		go func() {
			if _, err := deps.Auth.RevokeSession(bgContext(), &affv1.AuthServiceRevokeSessionRequest{SessionId: sessionID}); err == nil {
				loadSessions(sessions, sessionsLoading, sessionsErr)
			}
		}()
	}

	doRevokeAll := func() {
		go func() {
			if _, err := deps.Auth.RevokeAllSessions(bgContext(), &affv1.AuthServiceRevokeAllSessionsRequest{}); err == nil {
				loadSessions(sessions, sessionsLoading, sessionsErr)
			}
		}()
	}

	rows := SortSessionsForDisplay(sessions.Get())
	sessionState := ComputeScreenState(ScreenInputs{
		Loading:   sessionsLoading.Get(),
		Err:       sessionsErr.Get(),
		ItemCount: len(rows),
	})

	sessionRows := make([]ui.Node, 0, len(rows))
	for _, row := range rows {
		row := row
		sessionRows = append(sessionRows, h.Tr(
			h.Td(h.Text(row.UserAgent)),
			h.Td(h.Text(row.IP)),
			h.Td(h.Text(fmts().RelativeTime(row.LastSeenAt, time.Now()))),
			h.Td(h.Text(t("settings.security.sessions.current", boolYesNo(row.IsCurrent)))),
			h.Td(h.Show(!row.IsCurrent && !row.Revoked(), h.Button(
				h.Type("button"),
				h.OnClick(func() { doRevokeOne(row.ID) }),
				h.Text(t("settings.security.sessions.revoke")),
			))),
		))
	}

	return h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.security.title"))),

		// Change password
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.security.changePassword.title"))),
			h.P(h.ClassStr("af-warning"), h.Text(t(RevokeAllWarningKey(ActionChangePassword)))),
			h.P(h.Text(t("settings.security.passwordPolicy.hint", PasswordGuidanceArgs()["min"], PasswordGuidanceArgs()["max"]))),
			h.Form(
				h.OnSubmit(func(e ui.FormEvent) { e.PreventDefault(); doChangePassword() }),
				h.Label(h.Text(t("settings.security.changePassword.current")),
					h.Input(h.Type("password"), h.Value(currentPassword.Get()),
						h.OnInput(func(e ui.InputEvent) { currentPassword.Set(e.GetValue()) }))),
				h.Label(h.Text(t("settings.security.changePassword.totp")),
					h.Input(h.Type("text"), h.Value(totpCode.Get()),
						h.OnInput(func(e ui.InputEvent) { totpCode.Set(e.GetValue()) }))),
				h.Label(h.Text(t("settings.security.changePassword.new")),
					h.Input(h.Type("password"), h.Value(newPassword.Get()),
						h.OnInput(func(e ui.InputEvent) { newPassword.Set(e.GetValue()) }))),
				h.Show(pwErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.security.changePassword.error")))),
				h.Show(pwSuccess.Get(), h.P(h.ClassStr("af-success"), h.Text(t("settings.security.changePassword.success")))),
				h.Button(h.Type("submit"), h.DisabledIf(pwSubmitting.Get()), h.Text(t("settings.security.changePassword.submit"))),
			),
		),

		// TOTP re-enrollment
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.security.reenrollTotp.title"))),
			h.Form(
				h.OnSubmit(func(e ui.FormEvent) { e.PreventDefault(); doReenrollTOTP() }),
				h.Label(h.Text(t("settings.security.reenrollTotp.currentPassword")),
					h.Input(h.Type("password"), h.Value(reenrollPassword.Get()),
						h.OnInput(func(e ui.InputEvent) { reenrollPassword.Set(e.GetValue()) }))),
				h.Show(reenrollErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.security.reenrollTotp.error")))),
				h.Button(h.Type("submit"), h.DisabledIf(reenrollSubmitting.Get()), h.Text(t("settings.security.reenrollTotp.submit"))),
			),
			h.Show(provisioningURI.Get() != "", h.Div(
				h.P(h.Text(t("settings.security.reenrollTotp.shownOnce"))),
				h.Code(h.Text(provisioningURI.Get())),
			)),
		),

		// Recovery codes
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.security.recoveryCodes.title"))),
			h.Show(remainingHint.Get() >= 0, h.P(h.Text(t("settings.security.recoveryCodes.remaining", remainingHint.Get())))),
			h.Show(RecoveryCodesLow(remainingHint.Get()) && remainingHint.Get() >= 0, h.P(h.ClassStr("af-warning"), h.Text(t("settings.security.recoveryCodes.lowNag")))),
			kebabMenu([]kebabItem{{
				label:   t("settings.security.recoveryCodes.regenerate.action"),
				danger:  true,
				onClick: func() { regenVisible.Set(true) },
			}}),
			confirmModal(confirmModalProps{
				Visible:   regenVisible.Get(),
				PromptKey: "settings.security.recoveryCodes.regenerate.prompt",
				Word:      t(ConfirmationWordKey(ActionRegenerateRecoveryCodes)),
				OnConfirm: func() { regenVisible.Set(false); doRegenerateCodes() },
				OnCancel:  func() { regenVisible.Set(false) },
			}),
			h.Show(regenErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.security.recoveryCodes.error")))),
			h.Show(len(newCodes.Get()) > 0, renderRecoveryCodeList(newCodes.Get())),
		),

		// Active sessions
		h.Section(
			h.ClassStr("af-settings-card"),
			h.Div(
				h.ClassStr("af-settings-card-header"),
				h.H3(h.Text(t("settings.security.sessions.title"))),
				h.Show(RevocableSessionCount(rows) > 1, kebabMenu([]kebabItem{{
					label:   t("settings.security.sessions.revokeAll.action"),
					danger:  true,
					onClick: func() { revokeAllVisible.Set(true) },
				}})),
			),
			h.P(h.ClassStr("af-warning"), h.Text(t(RevokeAllWarningKey(ActionRevokeAllSessions)))),
			confirmModal(confirmModalProps{
				Visible:   revokeAllVisible.Get(),
				PromptKey: "settings.security.sessions.revokeAll.prompt",
				Word:      t(ConfirmationWordKey(ActionRevokeAllSessions)),
				OnConfirm: func() { revokeAllVisible.Set(false); doRevokeAll() },
				OnCancel:  func() { revokeAllVisible.Set(false) },
			}),
			screenWrapper(sessionState, sessionsErr.Get(), h.Table(
				h.Thead(h.Tr(
					h.Th(h.Text(t("settings.security.sessions.col.device"))),
					h.Th(h.Text(t("settings.security.sessions.col.ip"))),
					h.Th(h.Text(t("settings.security.sessions.col.lastSeen"))),
					h.Th(h.Text(t("settings.security.sessions.col.current"))),
					h.Th(h.Text(t("settings.security.sessions.col.actions"))),
				)),
				h.Tbody(sessionRows),
			)),
		),
	)
}

func renderRecoveryCodeList(codes []string) ui.Node {
	items := make([]ui.Node, 0, len(codes))
	for _, c := range codes {
		items = append(items, h.Li(h.Code(h.Text(c))))
	}
	return h.Div(
		h.P(h.ClassStr("af-warning"), h.Text(t("settings.security.recoveryCodes.shownOnce"))),
		h.Ul(items),
	)
}

func boolYesNo(b bool) string {
	if b {
		return t("common.yes")
	}
	return t("common.no")
}

func loadSessions(sessions ui.State[[]SessionRow], loading ui.State[bool], errState ui.State[error]) {
	loading.Set(true)
	errState.Set(nil)
	go func() {
		resp, err := deps.Auth.ListSessions(bgContext(), &affv1.AuthServiceListSessionsRequest{PageSize: 100})
		loading.Set(false)
		if err != nil {
			errState.Set(err)
			return
		}
		out := make([]SessionRow, 0, len(resp.GetSessions()))
		for _, s := range resp.GetSessions() {
			out = append(out, SessionRow{
				ID:         s.GetId(),
				CreatedAt:  s.GetCreatedAt().AsTime(),
				LastSeenAt: s.GetLastSeenAt().AsTime(),
				ExpiresAt:  s.GetExpiresAt().AsTime(),
				IP:         s.GetIp(),
				UserAgent:  s.GetUserAgent(),
				RevokedAt:  s.GetRevokedAt().AsTime(),
				IsCurrent:  s.GetIsCurrent(),
			})
		}
		sessions.Set(out)
	}()
}
