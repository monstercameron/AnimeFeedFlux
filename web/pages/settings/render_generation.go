//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderGeneration is the Generation section (D4-08): kill switch, global
// ceiling, default budgets, staleness threshold.
func renderGeneration() ui.Node {
	disconnected := isDisconnected()

	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	saving := ui.UseState(false)
	saved := ui.UseState(false)

	enabled := ui.UseState(true)
	globalTokenCeiling := ui.UseState(int64(0))
	globalSpendCeiling := ui.UseState(0.0)
	defaultTokenBudget := ui.UseState(int64(0))
	defaultRunBudget := ui.UseState(int32(0))
	defaultFeedWindow := ui.UseState(int32(0))
	stalenessMinutes := ui.UseState(int32(0))

	// reloadTick drives the error view's Retry control: the load effect is
	// keyed on it rather than on a constant, so bumping it re-runs the load.
	// §12.6 — an error with no way out is a dead end.
	reloadTick := ui.UseState(0)
	ui.UseEffectOf(func() func() {
		go func() {
			loading.Set(true)
			resp, err := deps.System.GetSettings(bgContext(), &affv1.SystemServiceGetSettingsRequest{})
			loading.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			g := resp.GetSettings().GetGeneration()
			enabled.Set(g.GetEnabled())
			globalTokenCeiling.Set(g.GetGlobalDailyTokenCeiling())
			globalSpendCeiling.Set(g.GetGlobalDailySpendCeilingUsd())
			defaultTokenBudget.Set(g.GetDefaultDailyTokenBudget())
			defaultRunBudget.Set(g.GetDefaultDailyRunBudget())
			defaultFeedWindow.Set(g.GetDefaultFeedWindow())
			stalenessMinutes.Set(g.GetStalenessThresholdMinutes())
		}()
		return nil
	}, reloadTick.Get())

	// The kill switch has its own dedicated RPC (SetGenerationEnabled) so
	// flipping it does not require (and does not wait on) the rest of the
	// form's Save round trip — PLAN.md §13: existing feeds keep serving,
	// nothing generates, and that needs to take effect immediately.
	toggleKillSwitchTo := func(next bool) {
		if disconnected {
			return
		}
		enabled.Set(next)
		go func() {
			if _, err := deps.System.SetGenerationEnabled(bgContext(), &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: next}); err != nil {
				enabled.Set(!next) // revert on failure
				errState.Set(err)
			}
		}()
	}

	doSave := func() {
		if disconnected {
			return
		}
		saving.Set(true)
		saved.Set(false)
		errState.Set(nil)
		go func() {
			_, err := deps.System.UpdateSettings(bgContext(), &affv1.SystemServiceUpdateSettingsRequest{
				Settings: &affv1.Settings{
					Generation: &affv1.Settings_Generation{
						Enabled:                    enabled.Get(),
						GlobalDailyTokenCeiling:    globalTokenCeiling.Get(),
						GlobalDailySpendCeilingUsd: globalSpendCeiling.Get(),
						DefaultDailyTokenBudget:    defaultTokenBudget.Get(),
						DefaultDailyRunBudget:      defaultRunBudget.Get(),
						DefaultFeedWindow:          defaultFeedWindow.Get(),
						StalenessThresholdMinutes:  stalenessMinutes.Get(),
					},
				},
			})
			saving.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			saved.Set(true)
		}()
	}

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), Disconnected: disconnected, ItemCount: 1})

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.generation.title"))),
		affui.Toggle(affui.ToggleProps{
			T: t, ID: "settings-kill-switch", LabelKey: "settings.generation.killSwitch",
			Checked: enabled.Get(), Disabled: disconnected,
			DisabledReasonKey: "settings.common.disconnectedReason",
			OnChange:          toggleKillSwitchTo,
		}),
		h.Show(!enabled.Get(), h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.generation.killSwitch.reason")))),
		h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason")))),

		// Three groups, not one stack of six identical rows. Global ceilings
		// and per-feed defaults are different KINDS of number — one is the
		// backstop for the whole deployment, the other is what a newly
		// created feed starts with — and rendered as one undifferentiated
		// column of bare zeros there was nothing to tell them apart.
		h.Div(h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.generation.group.ceilings"))),
			h.P(h.ClassStr("af-field-help"), h.Text(t("settings.generation.group.ceilings.help"))),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-token-ceiling", LabelKey: "settings.generation.globalTokenCeiling",
				Type: "number", Mono: true, Value: strconv.FormatInt(globalTokenCeiling.Get(), 10),
				OnChange: func(v string) { globalTokenCeiling.Set(parseInt64(v)) },
			}),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-spend-ceiling", LabelKey: "settings.generation.globalSpendCeiling",
				Type: "number", Mono: true, Value: floatToStr(globalSpendCeiling.Get()),
				OnChange: func(v string) { globalSpendCeiling.Set(parseFloat(v)) },
			}),
		),
		h.Div(h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.generation.group.feedDefaults"))),
			h.P(h.ClassStr("af-field-help"), h.Text(t("settings.generation.group.feedDefaults.help"))),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-token-budget", LabelKey: "settings.generation.defaultTokenBudget",
				Type: "number", Mono: true, Value: strconv.FormatInt(defaultTokenBudget.Get(), 10),
				OnChange: func(v string) { defaultTokenBudget.Set(parseInt64(v)) },
			}),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-run-budget", LabelKey: "settings.generation.defaultRunBudget",
				Type: "number", Mono: true, Value: strconv.Itoa(int(defaultRunBudget.Get())),
				OnChange: func(v string) { defaultRunBudget.Set(int32(parseInt64(v))) },
			}),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-feed-window", LabelKey: "settings.generation.defaultFeedWindow",
				Type: "number", Mono: true, Value: strconv.Itoa(int(defaultFeedWindow.Get())),
				OnChange: func(v string) { defaultFeedWindow.Set(int32(parseInt64(v))) },
			}),
		),
		h.Div(h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.generation.group.staleness"))),
			affui.Input(affui.InputProps{
				T: t, ID: "settings-gen-staleness", LabelKey: "settings.generation.stalenessThreshold",
				Type: "number", Mono: true, Value: strconv.Itoa(int(stalenessMinutes.Get())),
				OnChange: func(v string) { stalenessMinutes.Set(int32(parseInt64(v))) },
			}),
		),

		h.Show(errState.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.generation.saveError")))),
		h.Show(saved.Get(), h.P(h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"), h.Text(t("settings.generation.saved")))),
		affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.generation.save", Variant: affui.ButtonPrimary,
			Disabled: disconnected, Busy: saving.Get(), OnClick: doSave,
		}),
	)

	return screenWrapperRetry(state, errState.Get(), func() { reloadTick.Update(func(n int) int { return n + 1 }) }, body)
}

// parseInt64 reads a settings field. A negative value is clamped to zero
// rather than stored: every one of these fields is a count, a ceiling or a
// budget, and internal/budget reads a negative the same way it reads zero —
// as "no limit" — so a stray minus sign silently removed a cap.
func parseInt64(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// parseFloat clamps for the same reason as parseInt64.
func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
