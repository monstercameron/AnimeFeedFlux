//go:build js && wasm

package settings

import (
	"strconv"
	"syscall/js"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderProvider is the Provider section (D4-05..07): active provider,
// default model, and the editable price table. Key material is rendered
// as a boolean (api_key_present) — never a masked string, since a
// masked string means the value crossed the wire (this task's explicit
// instruction, restating PLAN.md §12.5/§4).
func renderProvider() ui.Node {
	disconnected := isDisconnected()

	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	saving := ui.UseState(false)
	saved := ui.UseState(false)

	activeProvider := ui.UseState("")
	defaultModel := ui.UseState("")
	embeddingModel := ui.UseState("")
	apiKeyPresent := ui.UseState(false)
	// The built-in provider's key controls: typed-but-unsaved key, whether
	// one is stored server-side, and a queued clear. The env var is only
	// the dev fallback — production keys are configured here.
	defaultApiKey := ui.UseState("")
	defaultKeyStored := ui.UseState(false)
	clearDefaultKey := ui.UseState(false)
	priceTable := ui.UseState([]*affv1.PriceEntry(nil))
	priceErr := ui.UseState("")
	// The provider's own model list, fetched separately from the settings
	// themselves: it is a live call to a third party, so a slow or failing
	// provider must not delay or fail the settings load. Its absence is a
	// normal state the fields handle (modelselect.go's free-text fallback).
	effort := ui.UseState(defaultEffort)
	profiles := ui.UseState([]*affv1.ProviderProfile(nil))
	activeProfile := ui.UseState("")
	costBuckets := ui.UseState([]*affv1.CostBucket(nil))
	costTotal := ui.UseState(0.0)
	costDays := ui.UseState(int32(30))
	hoveredDay := ui.UseState(-1)
	models := ui.UseState([]*affv1.ProviderModel(nil))
	modelsUnavailable := ui.UseState(false)
	modelsReason := ui.UseState("")

	// reloadTick drives the error view's Retry control — see §12.6.
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
			p := resp.GetSettings().GetProvider()
			activeProvider.Set(p.GetActiveProvider())
			defaultModel.Set(p.GetDefaultModel())
			embeddingModel.Set(p.GetEmbeddingModel())
			apiKeyPresent.Set(p.GetApiKeyPresent())
			defaultKeyStored.Set(p.GetDefaultKeyStored())
			defaultApiKey.Set("")
			clearDefaultKey.Set(false)
			priceTable.Set(p.GetPriceTable())
			if e := p.GetEffort(); e != "" {
				effort.Set(e)
			}
			profiles.Set(p.GetProfiles())
			activeProfile.Set(p.GetActiveProfile())
		}()
		return nil
	}, reloadTick.Get())

	ui.UseEffectOf(func() func() {
		go func() {
			resp, err := deps.System.CostHistory(bgContext(), &affv1.SystemServiceCostHistoryRequest{Days: costDays.Get()})
			if err != nil {
				// The chart is a read-only panel beside the settings it
				// annotates; a failure here empties it rather than failing
				// the whole screen, so a provider or query problem cannot
				// stop someone changing a model.
				costBuckets.Set(nil)
				costTotal.Set(0)
				return
			}
			costBuckets.Set(resp.GetBuckets())
			costTotal.Set(resp.GetTotalUsd())
		}()
		return nil
	}, costDays.Get())

	ui.UseEffectOf(func() func() {
		go func() {
			resp, err := deps.System.ListModels(bgContext(), &affv1.SystemServiceListModelsRequest{})
			if err != nil {
				// A transport failure is the same situation as a provider
				// failure from the operator's side: no list, type the id.
				modelsUnavailable.Set(true)
				modelsReason.Set(t("settings.provider.model.unreachable"))
				return
			}
			modelsUnavailable.Set(resp.GetUnavailable())
			modelsReason.Set(resp.GetUnavailableReason())
			models.Set(resp.GetModels())
		}()
		return nil
	}, reloadTick.Get())

	doSave := func() {
		if disconnected {
			return
		}
		// Refuse a table that would silently produce wrong money: a rate with
		// no model matches nothing, two rows for one model make which-wins
		// arbitrary, and a negative rate turns spend into credit.
		if problems := ValidatePriceTable(priceTable.Get()); len(problems) > 0 {
			priceErr.Set(priceProblemText(t, problems[0]))
			return
		}
		priceErr.Set("")
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
						// ApiKeyPresent/DefaultKeyStored are server-computed
						// and never sent back; DefaultApiKey rides write-only
						// and is cleared again the moment the save lands.
						PriceTable:      priceTable.Get(),
						Effort:          effort.Get(),
						ActiveProfile:   activeProfile.Get(),
						Profiles:        profiles.Get(),
						DefaultApiKey:   defaultApiKey.Get(),
						ClearDefaultKey: clearDefaultKey.Get(),
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
			defaultKeyStored.Set(p.GetDefaultKeyStored())
			defaultApiKey.Set("")
			clearDefaultKey.Set(false)
			// Re-adopt the saved profiles so each card's stored-key state
			// reflects what the server now holds — a just-stored key must
			// flip to "stored" rather than staying in the input.
			profiles.Set(p.GetProfiles())
			activeProfile.Set(p.GetActiveProfile())
			saved.Set(true)
		}()
	}

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), Disconnected: disconnected, ItemCount: 1})

	// The price table (D4-07) is its own list within the panel and gets
	// its own six-state read rather than inheriting the panel's
	// (Loading/Err always agree with the panel, since both come from the
	// same GetSettings call, but ItemCount does not — a settings object
	// with zero price entries must not render as a header with an empty
	// body, which is indistinguishable from the table having failed to
	// load; §12.3: "an admin tool that shows a blank box ... is how a
	// stale feed goes unnoticed for a week").
	priceState := ComputeScreenState(ScreenInputs{
		Loading:      loading.Get(),
		Err:          errState.Get(),
		Disconnected: disconnected,
		ItemCount:    len(priceTable.Get()),
	})

	// NOTE (adoption gap — see the ticket report): affui.Input is a full
	// labeled field group (label + input + help/error line), designed for a
	// form, not a dense table cell — using it here would print "Input
	// $/1M tokens" a second time next to every row's price, once per row,
	// which is worse than the column header it duplicates and roughly
	// doubles the row height. web/ui has no bare "table-cell input"
	// variant, so these two numeric cells stay a plain <input> with an
	// aria-label carrying the same accessible name Input would have given
	// it, rather than forcing the primitive where it does not fit.
	priceRows := make([]map[string]affui.Node, 0, len(priceTable.Get()))
	priceRowKeys := make([]string, 0, len(priceTable.Get()))
	for i, entry := range priceTable.Get() {
		i, entry := i, entry
		priceRows = append(priceRows, map[string]affui.Node{
			// A select over the provider's own model list, not free text: a
			// rate must be LINKED to a model the provider can run — a
			// mistyped id prices nothing and every run of that model then
			// reports $0.0000. Falls back to a text input when the list is
			// unavailable (renderPriceModelSelect).
			"model": renderPriceModelSelect(
				"settings-price-model-"+strconv.Itoa(i),
				entry.GetModel(), t("settings.provider.priceTable.col.model"),
				models.Get(), modelsUnavailable.Get(),
				func(v string) { updatePriceModel(priceTable, i, v) }),
			// Displayed and edited in $ per 1M tokens — the app's standard
			// unit — converted to/from the stored per-1K value at this
			// boundary only (see perMillionStr).
			"in": h.Input(h.Type("number"), h.Step("0.0001"), h.Value(perMillionStr(entry.GetUsdPer_1KTokensIn())),
				h.Aria("label", t("settings.provider.priceTable.col.in")),
				h.OnInput(func(e ui.InputEvent) { updatePriceIn(priceTable, i, e.GetValue()) })),
			"out": h.Input(h.Type("number"), h.Step("0.0001"), h.Value(perMillionStr(entry.GetUsdPer_1KTokensOut())),
				h.Aria("label", t("settings.provider.priceTable.col.out")),
				h.OnInput(func(e ui.InputEvent) { updatePriceOut(priceTable, i, e.GetValue()) })),
			"rm": h.Button(h.Type("button"), h.ClassStr("af-provider-card__remove"),
				h.Aria("label", t("settings.provider.priceTable.removeRow", entry.GetModel())),
				h.OnClick(ui.UseEvent(func() { removePriceRow(priceTable, i) })),
				h.Text(t("settings.provider.priceTable.removeLabel"))),
		})
		// Keyed by position, not by model name: the name is now editable, and
		// a key that changes on every keystroke remounts the row and drops
		// focus after the first character.
		priceRowKeys = append(priceRowKeys, "price-row-"+strconv.Itoa(i))
	}

	// ONE value guard for every price-row model select (never a hook per
	// row — see useSelectValueGuard's hook-discipline note): re-asserts each
	// row's model against the browser's default-selection mis-fire during
	// the input→select swap, watching the whole table with one observer.
	priceGuardReady := !modelsUnavailable.Get() && len(models.Get()) > 0
	priceGuardKey := strconv.FormatBool(priceGuardReady)
	for _, e := range priceTable.Get() {
		priceGuardKey += "|" + e.GetModel()
	}
	rowModels := make([]string, len(priceTable.Get()))
	for i, e := range priceTable.Get() {
		rowModels[i] = e.GetModel()
	}
	ui.UseEffectOf(func() func() {
		if !priceGuardReady {
			return nil
		}
		doc := js.Global().Get("document")
		correct := func() {
			for i, want := range rowModels {
				el := doc.Call("getElementById", "settings-price-model-"+strconv.Itoa(i))
				if el.Truthy() && el.Get("value").String() != want {
					el.Set("value", want)
				}
			}
		}
		correct()
		table := doc.Call("getElementById", "settings-price-table")
		if !table.Truthy() {
			return nil
		}
		var cb js.Func
		cb = js.FuncOf(func(this js.Value, args []js.Value) any {
			correct()
			return nil
		})
		observer := js.Global().Get("MutationObserver").New(cb)
		observer.Call("observe", table, map[string]any{"childList": true, "subtree": true})
		return func() {
			observer.Call("disconnect")
			cb.Release()
		}
	}, priceGuardKey)

	// Wrapped in .af-table-wrap so the table's scroll container spans the
	// card's full grid width — unwrapped it sat in one ~20rem column, which
	// crushed the model inputs to a two-character sliver.
	priceTableBody := h.Div(h.ClassStr("af-table-wrap"), screenWrapper(priceState, errState.Get(), affui.Table(affui.TableProps{
		T: t, ID: "settings-price-table", CaptionKey: "settings.provider.priceTable.caption",
		Columns: []affui.TableColumn{
			{ID: "model", LabelKey: "settings.provider.priceTable.col.model", Mono: true},
			{ID: "in", LabelKey: "settings.provider.priceTable.col.in"},
			{ID: "out", LabelKey: "settings.provider.priceTable.col.out"},
			{ID: "rm", LabelKey: "settings.provider.priceTable.col.remove"},
		},
		Rows: priceRows, RowKeys: priceRowKeys,
	})))

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.provider.title"))),

		// --- Providers: every configured endpoint as a card, the active one
		// marked, switching one click, keys stored (encrypted) or read from
		// the environment — plus the wire-protocol menu.
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.provider.providers.title"))),
			h.P(h.Text(t("settings.provider.providers.help"))),
			renderProviderCards(
				profiles.Get(), activeProfile.Get(),
				builtinKeyState{
					Stored:       defaultKeyStored.Get(),
					EnvPresent:   apiKeyPresent.Get(),
					Pending:      defaultApiKey.Get(),
					PendingClear: clearDefaultKey.Get(),
					OnKey: func(v string) {
						defaultApiKey.Set(v)
						if v != "" {
							clearDefaultKey.Set(false)
						}
					},
					OnClear: func(v bool) {
						clearDefaultKey.Set(v)
						if v {
							defaultApiKey.Set("")
						}
					},
				},
				disconnected,
				func(next []*affv1.ProviderProfile) { profiles.Set(next) },
				func(name string) { activeProfile.Set(name) },
			),
			renderBackendSelect(activeProvider.Get(), disconnected,
				func(v string) { activeProvider.Set(v) }),
		),

		// --- Model and effort: what runs, and how hard it works.
		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.provider.model.title"))),
			renderModelSelect(modelSelectProps{
				ID: "settings-provider-model", LabelKey: "settings.provider.defaultModel",
				Value: defaultModel.Get(), OnChange: func(v string) { defaultModel.Set(v) },
				Disabled: disconnected,
				Models:   models.Get(), Unavailable: modelsUnavailable.Get(), Reason: modelsReason.Get(),
			}, chatModels),
			renderModelSelect(modelSelectProps{
				ID: "settings-provider-embed", LabelKey: "settings.provider.embeddingModel",
				Value: embeddingModel.Get(), OnChange: func(v string) { embeddingModel.Set(v) },
				Disabled: disconnected,
				Models:   models.Get(), Unavailable: modelsUnavailable.Get(), Reason: modelsReason.Get(),
			}, embeddingModels),
			renderEffortSelect(effort.Get(), disconnected, func(v string) { effort.Set(v) }),
		),

		// --- Rates: what those models cost, per PLAN.md §12.5's "editable
		// price table, since published prices change and a stale table
		// silently makes every cost number wrong".
		h.Section(
			h.ClassStr("af-settings-card"),
			h.Div(h.ClassStr("af-settings-card-header"),
				h.H3(h.Text(t("settings.provider.priceTable.title"))),
				h.Show(priceErr.Get() != "", h.P(h.Role("alert"), h.ClassStr("af-error"), h.Text(priceErr.Get()))),
				affui.Button(affui.ButtonProps{
					T: t, LabelKey: "settings.provider.priceTable.add", Variant: affui.ButtonSecondary,
					Disabled: disconnected,
					OnClick: func() {
						priceTable.Set(append(append([]*affv1.PriceEntry(nil), priceTable.Get()...),
							&affv1.PriceEntry{}))
					},
				}),
			),
			h.P(h.Text(t("settings.provider.priceTable.help"))),
			priceTableBody,
		),

		// --- Cost history. Read-only, and last: it explains the settings
		// above it rather than being one of them.
		h.Section(
			h.ClassStr("af-settings-card"),
			h.Div(h.ClassStr("af-settings-card-header"),
				h.H3(h.Text(t("settings.provider.cost.title"))),
				renderWindowSelect(costDays.Get(), func(d int32) { costDays.Set(d); hoveredDay.Set(-1) }),
			),
			h.Div(h.ClassStr("af-cost__hero"),
				h.Div(h.ClassStr("af-cost__total"), h.Text(fmts().Currency(costTotal.Get()))),
				h.Div(h.ClassStr("af-cost__caption"), h.Text(costCaption(costBuckets.Get(), hoveredDay.Get(), costDays.Get()))),
			),
			h.Div(h.ClassStr("af-cost__plot"),
				renderCostChart(costBuckets.Get(), hoveredDay.Get(), func(i int) { hoveredDay.Set(i) }),
			),
		),

		h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason")))),
		h.Show(errState.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.provider.saveError")))),
		h.Show(saved.Get(), h.P(h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"), h.Text(t("settings.provider.saved")))),
		affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.provider.save", Variant: affui.ButtonPrimary,
			Disabled: disconnected, Busy: saving.Get(), OnClick: doSave,
		}),
	)

	return screenWrapperRetry(state, errState.Get(), func() { reloadTick.Update(func(n int) int { return n + 1 }) }, body)
}
