//go:build js && wasm

package settings

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderProvider is the Provider section (D4-05..07): active provider,
// default model, and the editable price table. Key material is rendered
// as a boolean (api_key_present) — never a masked string, since a
// masked string means the value crossed the wire (this task's explicit
// instruction, restating PLAN.md §12.5/§4).
func renderProvider() ui.Node {
	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	saving := ui.UseState(false)
	saved := ui.UseState(false)

	activeProvider := ui.UseState("")
	defaultModel := ui.UseState("")
	embeddingModel := ui.UseState("")
	apiKeyPresent := ui.UseState(false)
	priceTable := ui.UseState([]*affv1.PriceEntry(nil))

	ui.UseEffect(func() func() {
		go func() {
			loading.Set(true)
			resp, err := deps.System.GetSettings(bgContext(), &affv1.SystemServiceGetSettingsRequest{})
			loading.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			p := resp.GetSettings().GetProvider()
			activeProvider.Set(p.GetActiveProvider())
			defaultModel.Set(p.GetDefaultModel())
			embeddingModel.Set(p.GetEmbeddingModel())
			apiKeyPresent.Set(p.GetApiKeyPresent())
			priceTable.Set(p.GetPriceTable())
		}()
		return nil
	}, "provider-mount")

	doSave := func() {
		saving.Set(true)
		saved.Set(false)
		errState.Set(nil)
		go func() {
			resp, err := deps.System.UpdateSettings(bgContext(), &affv1.SystemServiceUpdateSettingsRequest{
				Settings: &affv1.Settings{
					Provider: &affv1.Settings_Provider{
						ActiveProvider: activeProvider.Get(),
						DefaultModel:   defaultModel.Get(),
						EmbeddingModel: embeddingModel.Get(),
						// ApiKeyPresent is server-computed and never sent
						// back — PLAN.md §12.5: "never editable here".
						PriceTable: priceTable.Get(),
					},
				},
			})
			saving.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			p := resp.GetSettings().GetProvider()
			apiKeyPresent.Set(p.GetApiKeyPresent())
			saved.Set(true)
		}()
	}

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), ItemCount: 1})

	priceRows := make([]ui.Node, 0, len(priceTable.Get()))
	for i, entry := range priceTable.Get() {
		i, entry := i, entry
		priceRows = append(priceRows, h.Tr(
			h.Td(h.Text(entry.GetModel())),
			h.Td(h.Input(h.Type("number"), h.Step("0.0001"), h.Value(floatToStr(entry.GetUsdPer_1KTokensIn())),
				h.OnInput(func(e ui.InputEvent) { updatePriceIn(priceTable, i, e.GetValue()) }))),
			h.Td(h.Input(h.Type("number"), h.Step("0.0001"), h.Value(floatToStr(entry.GetUsdPer_1KTokensOut())),
				h.OnInput(func(e ui.InputEvent) { updatePriceOut(priceTable, i, e.GetValue()) }))),
		))
	}

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.provider.title"))),
		h.Label(h.Text(t("settings.provider.activeProvider")),
			h.Input(h.Value(activeProvider.Get()), h.OnInput(func(e ui.InputEvent) { activeProvider.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.provider.defaultModel")),
			h.Input(h.Value(defaultModel.Get()), h.OnInput(func(e ui.InputEvent) { defaultModel.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.provider.embeddingModel")),
			h.Input(h.Value(embeddingModel.Get()), h.OnInput(func(e ui.InputEvent) { embeddingModel.Set(e.GetValue()) }))),
		h.P(h.Text(t("settings.provider.apiKeyPresent", boolYesNo(apiKeyPresent.Get())))),
		h.H3(h.Text(t("settings.provider.priceTable.title"))),
		h.Table(
			h.Thead(h.Tr(
				h.Th(h.Text(t("settings.provider.priceTable.col.model"))),
				h.Th(h.Text(t("settings.provider.priceTable.col.in"))),
				h.Th(h.Text(t("settings.provider.priceTable.col.out"))),
			)),
			h.Tbody(priceRows),
		),
		h.Show(errState.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.provider.saveError")))),
		h.Show(saved.Get(), h.P(h.ClassStr("af-success"), h.Text(t("settings.provider.saved")))),
		h.Button(h.Type("button"), h.DisabledIf(saving.Get()), h.OnClick(doSave), h.Text(t("settings.provider.save"))),
	)

	return screenWrapper(state, errState.Get(), body)
}
