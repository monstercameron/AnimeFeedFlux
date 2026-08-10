//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderGeneration is the Generation section (D4-08): kill switch, global
// ceiling, default budgets, staleness threshold.
func renderGeneration() ui.Node {
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

	ui.UseEffect(func() func() {
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
	}, "generation-mount")

	// The kill switch has its own dedicated RPC (SetGenerationEnabled) so
	// flipping it does not require (and does not wait on) the rest of the
	// form's Save round trip — PLAN.md §13: existing feeds keep serving,
	// nothing generates, and that needs to take effect immediately.
	toggleKillSwitch := func() {
		next := !enabled.Get()
		enabled.Set(next)
		go func() {
			if _, err := deps.System.SetGenerationEnabled(bgContext(), &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: next}); err != nil {
				enabled.Set(!next) // revert on failure
				errState.Set(err)
			}
		}()
	}

	doSave := func() {
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

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), ItemCount: 1})

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.generation.title"))),
		h.Label(
			h.Input(h.Type("checkbox"), h.Checked(enabled.Get()), h.OnChange(func(e ui.ChangeEvent) { toggleKillSwitch() })),
			h.Text(t("settings.generation.killSwitch")),
		),
		h.Show(!enabled.Get(), h.P(h.ClassStr("af-warning"), h.Text(t("settings.generation.killSwitch.reason")))),

		h.Label(h.Text(t("settings.generation.globalTokenCeiling")),
			h.Input(h.Type("number"), h.Value(strconv.FormatInt(globalTokenCeiling.Get(), 10)),
				h.OnInput(func(e ui.InputEvent) { globalTokenCeiling.Set(parseInt64(e.GetValue())) }))),
		h.Label(h.Text(t("settings.generation.globalSpendCeiling")),
			h.Input(h.Type("number"), h.Step("0.01"), h.Value(floatToStr(globalSpendCeiling.Get())),
				h.OnInput(func(e ui.InputEvent) { globalSpendCeiling.Set(parseFloat(e.GetValue())) }))),
		h.Label(h.Text(t("settings.generation.defaultTokenBudget")),
			h.Input(h.Type("number"), h.Value(strconv.FormatInt(defaultTokenBudget.Get(), 10)),
				h.OnInput(func(e ui.InputEvent) { defaultTokenBudget.Set(parseInt64(e.GetValue())) }))),
		h.Label(h.Text(t("settings.generation.defaultRunBudget")),
			h.Input(h.Type("number"), h.Value(strconv.Itoa(int(defaultRunBudget.Get()))),
				h.OnInput(func(e ui.InputEvent) { defaultRunBudget.Set(int32(parseInt64(e.GetValue()))) }))),
		h.Label(h.Text(t("settings.generation.defaultFeedWindow")),
			h.Input(h.Type("number"), h.Value(strconv.Itoa(int(defaultFeedWindow.Get()))),
				h.OnInput(func(e ui.InputEvent) { defaultFeedWindow.Set(int32(parseInt64(e.GetValue()))) }))),
		h.Label(h.Text(t("settings.generation.stalenessThreshold")),
			h.Input(h.Type("number"), h.Value(strconv.Itoa(int(stalenessMinutes.Get()))),
				h.OnInput(func(e ui.InputEvent) { stalenessMinutes.Set(int32(parseInt64(e.GetValue()))) }))),

		h.Show(errState.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.generation.saveError")))),
		h.Show(saved.Get(), h.P(h.ClassStr("af-success"), h.Text(t("settings.generation.saved")))),
		h.Button(h.Type("button"), h.DisabledIf(saving.Get()), h.OnClick(doSave), h.Text(t("settings.generation.save"))),
	)

	return screenWrapper(state, errState.Get(), body)
}

func parseInt64(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
