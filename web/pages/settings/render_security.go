//go:build js && wasm

package settings

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderSecurity is the Security section (D4-01..04): change password,
// re-enroll TOTP, regenerate recovery codes, active sessions.
func renderSecurity() ui.Node {
	// D0-10/D-FLOW: mutations must be refused (never fail silently) while
	// the shell is DISCONNECTED — this section has five (change password,
	// re-enroll TOTP, regenerate recovery codes, revoke one, revoke all).
	disconnected := isDisconnected()

	// --- sessions list state ---
	sessions := ui.UseState([]SessionRow(nil))
	sessionsLoading := ui.UseState(true)
	sessionsErr := ui.UseState(error(nil))
	revokeAllVisible := ui.UseState(false)
	revokeAllSubmitting := ui.UseState(false)

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
	regenSubmitting := ui.UseState(false)
	newCodes := ui.UseState([]string(nil))
	// -1 means "not yet known". It is filled from AuthService.Session on
	// mount and updated after a regenerate, so the panel states the count
	// the moment it loads rather than only after the operator has already
	// used the button — which was the previous behaviour and left the card
	// showing nothing but its own kebab.
	remainingHint := ui.UseState(-1)
	regenErr := ui.UseState(error(nil))

	ui.UseEffect(func() func() {
		go func() {
			resp, err := deps.Auth.Session(bgContext(), &affv1.AuthServiceSessionRequest{})
			if err != nil {
				// Leave it unknown rather than claiming zero: "0 codes left"
				// is an alarming, actionable statement (break-glass is the
				// only way back in) and must never be produced by a failed
				// read.
				return
			}
			remainingHint.Set(int(resp.GetRemainingRecoveryCodes()))
		}()
		return nil
	}, "security-recovery-count")

	doChangePassword := func() {
		if pwSubmitting.Get() || disconnected {
			return
		}
		if err := ValidatePasswordLength(newPassword.Get()); err != nil {
			pwErr.Set(err)
			return
		}
		pwSubmitting.Set(true)
		pwErr.Set(nil)
		// Cleared on every ATTEMPT, not only on success. It was only ever
		// set, never reset, so a failed attempt after a successful one
		// rendered "Password changed" and the failure banner side by side —
		// and the reassuring one is the one people read.
		pwSuccess.Set(false)
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
		if reenrollSubmitting.Get() || disconnected {
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
		if regenSubmitting.Get() || disconnected {
			return
		}
		regenSubmitting.Set(true)
		regenErr.Set(nil)
		go func() {
			resp, err := deps.Auth.RegenerateRecoveryCodes(bgContext(), &affv1.AuthServiceRegenerateRecoveryCodesRequest{
				CurrentPassword: currentPassword.Get(),
				TotpCode:        totpCode.Get(),
			})
			regenSubmitting.Set(false)
			if err != nil {
				regenErr.Set(err)
				return
			}
			codes := resp.GetRecoveryCodes()
			newCodes.Set(codes)
			remainingHint.Set(len(codes))
		}()
	}

	// revokingID is which row's revoke is in flight, "" when none. Every
	// other mutation on this page has an in-flight guard; this one did not,
	// so a double-click fired two revokes and the button gave no sign the
	// first had been heard. It also swallowed its error — a failed revoke
	// looked exactly like a successful one, on a security screen.
	revokingID := ui.UseState("")
	revokeErr := ui.UseState(error(nil))
	doRevokeOne := func(sessionID string) {
		if disconnected || revokingID.Get() != "" {
			return
		}
		revokingID.Set(sessionID)
		revokeErr.Set(nil)
		go func() {
			_, err := deps.Auth.RevokeSession(bgContext(), &affv1.AuthServiceRevokeSessionRequest{SessionId: sessionID})
			revokingID.Set("")
			if err != nil {
				revokeErr.Set(err)
				return
			}
			loadSessions(sessions, sessionsLoading, sessionsErr)
		}()
	}

	doRevokeAll := func() {
		if revokeAllSubmitting.Get() || disconnected {
			return
		}
		revokeAllSubmitting.Set(true)
		go func() {
			_, err := deps.Auth.RevokeAllSessions(bgContext(), &affv1.AuthServiceRevokeAllSessionsRequest{})
			revokeAllSubmitting.Set(false)
			if err == nil {
				loadSessions(sessions, sessionsLoading, sessionsErr)
			}
		}()
	}

	rows, hiddenRevoked := TrimRevoked(SortSessionsForDisplay(sessions.Get()))
	sessionState := ComputeScreenState(ScreenInputs{
		Loading:      sessionsLoading.Get(),
		Err:          sessionsErr.Get(),
		Disconnected: disconnected,
		ItemCount:    len(rows),
	})

	sessionRows := make([]map[string]affui.Node, 0, len(rows))
	sessionRowKeys := make([]string, 0, len(rows))
	for _, row := range rows {
		row := row
		revokeCell := h.Fragment()
		if !row.IsCurrent && !row.Revoked() {
			revokeCell = affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.security.sessions.revoke", Variant: affui.ButtonSecondary,
				Disabled: disconnected || revokingID.Get() != "",
				Busy:     revokingID.Get() == row.ID,
				OnClick:  func() { doRevokeOne(row.ID) },
			})
		}
		// A revoked row previously rendered identically to a live one — "No"
		// in Current, nothing in Actions — so the only difference between
		// "still logged in, cannot revoke from here" and "already ended" was
		// invisible. The Actions cell now says which it is.
		if row.Revoked() {
			revokeCell = h.Span(h.ClassStr("af-session-revoked"),
				h.Text(t("settings.security.sessions.revoked")))
		}
		sessionRows = append(sessionRows, map[string]affui.Node{
			"device":   h.Text(row.UserAgent),
			"ip":       h.Text(row.IP),
			"lastSeen": h.Text(fmts().RelativeTime(row.LastSeenAt, time.Now())),
			"current":  h.Text(t("settings.security.sessions.current", boolYesNo(row.IsCurrent))),
			"actions":  revokeCell,
		})
		sessionRowKeys = append(sessionRowKeys, row.ID)
	}

	disconnectedNote := func() ui.Node {
		return h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason"))))
	}

	revokeAllItem := disconnectedKebabItem(affui.KebabItem{
		ID: "settings-sessions-revoke-all", LabelKey: "settings.security.sessions.revokeAll.action",
		Danger: true, OnSelect: func() { revokeAllVisible.Set(true) },
	}, disconnected)
	regenItem := disconnectedKebabItem(affui.KebabItem{
		ID: "settings-recovery-regenerate", LabelKey: "settings.security.recoveryCodes.regenerate.action",
		Danger: true, OnSelect: func() { regenVisible.Set(true) },
	}, disconnected)

	return h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.security.title"))),

		// Change password
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.security.changePassword.title"))),
			h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t(RevokeAllWarningKey(ActionChangePassword)))),
			h.P(h.Text(t("settings.security.passwordPolicy.hint", PasswordGuidanceArgs()["min"], PasswordGuidanceArgs()["max"]))),
			disconnectedNote(),
			h.Form(
				h.OnSubmit(func(e ui.FormEvent) { e.PreventDefault(); doChangePassword() }),
				affui.Input(affui.InputProps{
					T: t, ID: "settings-pw-current", LabelKey: "settings.security.changePassword.current",
					Type: "password", Value: currentPassword.Get(), Disabled: pwSubmitting.Get(),
					AutoComplete: "current-password", OnChange: func(v string) { currentPassword.Set(v) },
				}),
				affui.Input(affui.InputProps{
					T: t, ID: "settings-pw-totp", LabelKey: "settings.security.changePassword.totp",
					Value: totpCode.Get(), Disabled: pwSubmitting.Get(), Mono: true,
					OnChange: func(v string) { totpCode.Set(v) },
				}),
				affui.Input(affui.InputProps{
					T: t, ID: "settings-pw-new", LabelKey: "settings.security.changePassword.new",
					Type: "password", Value: newPassword.Get(), Disabled: pwSubmitting.Get(),
					AutoComplete: "new-password", OnChange: func(v string) { newPassword.Set(v) },
				}),
				h.Show(pwErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.security.changePassword.error")))),
				h.Show(pwSuccess.Get(), h.P(h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"), h.Text(t("settings.security.changePassword.success")))),
				affui.Button(affui.ButtonProps{
					T: t, LabelKey: "settings.security.changePassword.submit", Variant: affui.ButtonPrimary,
					Type: "submit", Disabled: disconnected, Busy: pwSubmitting.Get(),
				}),
			),
		),

		// TOTP re-enrollment
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.security.reenrollTotp.title"))),
			disconnectedNote(),
			h.Form(
				h.OnSubmit(func(e ui.FormEvent) { e.PreventDefault(); doReenrollTOTP() }),
				affui.Input(affui.InputProps{
					T: t, ID: "settings-reenroll-pw", LabelKey: "settings.security.reenrollTotp.currentPassword",
					Type: "password", Value: reenrollPassword.Get(), Disabled: reenrollSubmitting.Get(),
					AutoComplete: "current-password", OnChange: func(v string) { reenrollPassword.Set(v) },
				}),
				h.Show(reenrollErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.security.reenrollTotp.error")))),
				affui.Button(affui.ButtonProps{
					T: t, LabelKey: "settings.security.reenrollTotp.submit", Variant: affui.ButtonPrimary,
					Type: "submit", Disabled: disconnected, Busy: reenrollSubmitting.Get(),
				}),
			),
			// h.If, not h.Show: the enrolment URI is a secret shown once, and
			// h.Show would leave it constructed, cloned and mounted (merely
			// hidden) in the DOM for the whole session (A8-45). Safe here
			// because it is the last child of this form — h.If yields nil and
			// GWC drops nil children, which shifts every later sibling.
			h.If(provisioningURI.Get() != "", h.Div(
				h.P(h.Text(t("settings.security.reenrollTotp.shownOnce"))),
				h.Code(h.Text(provisioningURI.Get())),
			)),
		),

		// Recovery codes
		h.Section(
			h.ClassStr("af-settings-card"),
			// Heading and kebab on one row, matching Active sessions below.
			// They were inconsistent: this card put its ⋯ AFTER the body
			// text, so the one destructive action here floated under the
			// content while the identical control one card down sat beside
			// its heading. §12.6 puts destructive actions behind a kebab; it
			// should be the same kebab, in the same place, both times.
			h.Div(h.ClassStr("af-settings-card-header"),
				h.H3(h.Text(t("settings.security.recoveryCodes.title"))),
				kebabUI(kebabUIProps{
					ID: "settings-recovery-kebab", LabelKey: "kebab.actionsFor",
					LabelArgs: []any{t("settings.security.recoveryCodes.title")},
					Items:     []affui.KebabItem{regenItem},
				}),
			),
			h.Show(remainingHint.Get() >= 0, h.P(h.Text(t("settings.security.recoveryCodes.remaining", remainingHint.Get())))),
			h.Show(RecoveryCodesLow(remainingHint.Get()) && remainingHint.Get() >= 0, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.security.recoveryCodes.lowNag")))),
			disconnectedNote(),
			confirmUI(confirmUIProps{
				ID: "settings-recovery-confirm", TitleKey: "settings.security.recoveryCodes.regenerate.confirmTitle",
				MessageKey:     "settings.security.recoveryCodes.regenerate.prompt",
				RequiredPhrase: t(ConfirmationWordKey(ActionRegenerateRecoveryCodes)),
				Open:           regenVisible.Get(), Busy: regenSubmitting.Get(),
				OnConfirm: func() { regenVisible.Set(false); doRegenerateCodes() },
				OnCancel:  func() { regenVisible.Set(false) },
			}),
			h.Show(regenErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.security.recoveryCodes.error")))),
			// Same reasoning as the enrolment URI above: ten fresh recovery
			// codes, shown once, built and mounted on every render of this
			// page under h.Show. Last child of the section, so h.If's nil
			// shifts nothing (A8-45).
			h.If(len(newCodes.Get()) > 0, renderRecoveryCodeList(newCodes.Get())),
		),

		// Active sessions
		h.Section(
			h.ClassStr("af-settings-card"),
			h.Div(
				h.ClassStr("af-settings-card-header"),
				h.H3(h.Text(t("settings.security.sessions.title"))),
				h.Show(RevocableSessionCount(rows) > 1, kebabUI(kebabUIProps{
					ID: "settings-sessions-kebab", LabelKey: "kebab.actionsFor",
					LabelArgs: []any{t("settings.security.sessions.title")},
					Items:     []affui.KebabItem{revokeAllItem},
				})),
			),
			h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t(RevokeAllWarningKey(ActionRevokeAllSessions)))),
			disconnectedNote(),
			confirmUI(confirmUIProps{
				ID: "settings-sessions-confirm", TitleKey: "settings.security.sessions.revokeAll.confirmTitle",
				MessageKey:     "settings.security.sessions.revokeAll.prompt",
				RequiredPhrase: t(ConfirmationWordKey(ActionRevokeAllSessions)),
				Open:           revokeAllVisible.Get(), Busy: revokeAllSubmitting.Get(),
				OnConfirm: func() { revokeAllVisible.Set(false); doRevokeAll() },
				OnCancel:  func() { revokeAllVisible.Set(false) },
			}),
			// VirtualTable, not Table: this list gains a row on every sign-in
			// and never loses one (revoked sessions stay, they just show no
			// revoke action), so it is the one list on this page whose length
			// is set by usage rather than by design — ~90 rows after a single
			// afternoon on a dev box. Table would build, reconcile and lay out
			// all of them for the ten a person can see.
			h.Show(revokeErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"),
				h.ClassStr("af-error"), h.Text(t("settings.security.sessions.revokeError")))),
			screenWrapperRetry(sessionState, sessionsErr.Get(),
				func() { loadSessions(sessions, sessionsLoading, sessionsErr) },
				affui.VirtualTable(affui.VirtualTableProps{
					T: t, ID: "settings-sessions-table", CaptionKey: "settings.security.sessions.caption",
					// Weighted, because these five columns hold very
					// different amounts of text and equal fifths served none
					// of them: a full user-agent string got the same width as
					// a "Yes"/"No" and was cut mid-word, while Current and
					// Last seen sat mostly empty. Device gets the room it
					// needs; the rest get what their content actually is.
					Columns: []affui.TableColumn{
						{ID: "device", LabelKey: "settings.security.sessions.col.device", Weight: 4},
						{ID: "ip", LabelKey: "settings.security.sessions.col.ip", Mono: true, Weight: 2},
						{ID: "lastSeen", LabelKey: "settings.security.sessions.col.lastSeen", Weight: 2},
						{ID: "current", LabelKey: "settings.security.sessions.col.current", Weight: 1.2},
						{ID: "actions", LabelKey: "settings.security.sessions.col.actions", Weight: 1.6},
					},
					Rows: sessionRows, RowKeys: sessionRowKeys,
					// The revoke button is taller than a line of text, so the row
					// height is stated rather than defaulted — VirtualList sizes
					// its spacers from this number, and a row that renders taller
					// than it claims makes the scrollbar drift.
					RowHeight: 44,
				})),
			// Say what was left out. A list that truncates silently is how
			// someone concludes a session is gone when it is only off-list.
			h.Show(hiddenRevoked > 0, h.P(h.ClassStr("af-field-help"),
				h.Text(t("settings.security.sessions.hiddenRevoked", hiddenRevoked)))),
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
		return t("settings.common.yes")
	}
	return t("settings.common.no")
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
				// protoTime, not .AsTime() directly.
				//
				// timestamppb's AsTime() on a NIL message returns
				// time.Unix(0,0) — 1970-01-01 — and time.Time.IsZero() is
				// only true for the year-1 zero value, so an unset
				// revoked_at read as "revoked in 1970". Every session
				// therefore reported itself revoked, including the caller's
				// own live one: the Actions column was empty on every row
				// and individual session revoke (§12.5, D4-04) was
				// unreachable from the UI entirely. It looked like correct
				// behaviour — "these are all revoked, so no revoke button" —
				// which is why it survived until a row that was obviously
				// live (current, last seen 0 seconds ago) still said so.
				RevokedAt: protoTime(s.GetRevokedAt()),
				IsCurrent: s.GetIsCurrent(),
			})
		}
		sessions.Set(out)
	}()
}
