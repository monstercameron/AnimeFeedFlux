//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// providercontrols.go holds the Provider section's non-model controls:
// effort, the OpenAI-compatible profile list, and the cost window.

// defaultEffort mirrors internal/rpc's defaultProviderEffort. Duplicated
// rather than imported because this package must not import server code —
// the value is part of the wire contract, and the RPC rejects anything else
// on save, so a drift here surfaces immediately as a refused save rather
// than as silently different behaviour.
const defaultEffort = "smart"

// effortTiers are SchemaFlux's Speed tiers (PLAN.md §8.1), in decreasing
// order of work done. They are the library's own names, not an invented
// scale — see internal/llm's Request.Effort for why.
var effortTiers = []struct{ value, labelKey string }{
	{"smart", "settings.provider.effort.smart"},
	{"fast", "settings.provider.effort.fast"},
	{"quick", "settings.provider.effort.quick"},
}

// renderEffortSelect is the how-hard-should-it-think control.
//
// Presented with its consequence in the help text, because effort is the
// one provider setting that trades money and latency against output quality
// and nothing else on the page says so.
func renderEffortSelect(value string, disabled bool, onChange func(string)) ui.Node {
	if value == "" {
		value = defaultEffort
	}
	opts := make([]any, 0, len(effortTiers)+3)
	opts = append(opts,
		h.ID("settings-provider-effort"),
		h.Disabled(disabled),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { onChange(e.GetValue()) })),
	)
	for _, tier := range effortTiers {
		opts = append(opts, h.Option(
			h.Value(tier.value),
			h.SelectedIf(tier.value == value),
			h.Text(t(tier.labelKey)),
		))
	}
	return h.Div(
		h.ClassStr("af-model-field"),
		h.Label(h.For("settings-provider-effort"), h.Text(t("settings.provider.effort.label"))),
		h.Select(opts...),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.effort.help"))),
	)
}

// providerBackends are the wire protocols this build's SchemaFlux registers
// (mirrors internal/rpc's validProviderBackends the same way defaultEffort
// mirrors defaultProviderEffort: the RPC rejects anything else on save, so
// drift surfaces as a refused save, never as silent divergence). The empty
// value IS openai — the server normalizes both spellings — so the menu
// carries one option for it rather than an "openai" and a "default" that
// mean the same thing.
var providerBackends = []struct{ value, labelKey string }{
	{"", "settings.provider.backend.openai"},
	{"anthropic", "settings.provider.backend.anthropic"},
	{"openrouter", "settings.provider.backend.openrouter"},
	{"cerebras", "settings.provider.backend.cerebras"},
	{"deepseek", "settings.provider.backend.deepseek"},
	{"qwen", "settings.provider.backend.qwen"},
	{"zai", "settings.provider.backend.zai"},
}

// renderBackendSelect replaces the free-text "active provider" field: the
// server accepts exactly seven values, and a field whose every valid answer
// is known is a menu, not a typing test with a 4am failure mode.
func renderBackendSelect(value string, disabled bool, onChange func(string)) ui.Node {
	if value == "openai" {
		value = ""
	}
	opts := make([]any, 0, len(providerBackends)+3)
	opts = append(opts,
		h.ID("settings-provider-backend"),
		h.Disabled(disabled),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { onChange(e.GetValue()) })),
	)
	for _, b := range providerBackends {
		opts = append(opts, h.Option(
			h.Value(b.value),
			h.SelectedIf(b.value == value),
			h.Text(t(b.labelKey)),
		))
	}
	return h.Div(
		h.ClassStr("af-model-field"),
		h.Label(h.For("settings-provider-backend"), h.Text(t("settings.provider.backend.label"))),
		h.Select(opts...),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.backend.help"))),
	)
}

// renderProviderCards is the provider list: one card per configured
// provider, plus the built-in OpenAI default as a fixed first card. Each
// card shows where its calls go (base URL), where its credential comes from
// (a stored encrypted key, an environment variable, or nowhere yet), and
// whether it is the one generation currently uses — with switching a
// one-click "Use this provider" on the card itself rather than a separate
// dropdown the operator has to correlate with the list below it.
//
// Keys typed here are WRITE-ONLY: they ride the save as
// ProviderProfile.api_key, are stored encrypted with AFF_SECRET_KEY
// server-side, and every response carries the field empty (the 2026-08-15
// revision of §4's environment-only rule — see the proto and DEVLOG).
// builtinKeyState is the built-in (profile-less) provider's key controls,
// threaded from render_provider.go's own state: the production path is a
// key stored from this page; the SCHEMAFLUX_API_KEY env var is only a
// dev/bootstrap fallback (operator directive 2026-08-15).
type builtinKeyState struct {
	// Stored: an encrypted key is stored server-side for the built-in
	// provider.
	Stored bool
	// EnvPresent: the dev-fallback env var is set on the server.
	EnvPresent bool
	// Pending is the key typed this session, not yet saved.
	Pending string
	// PendingClear: the stored key is queued for removal on save.
	PendingClear bool
	OnKey        func(string)
	OnClear      func(bool)
}

func renderProviderCards(
	profiles []*affv1.ProviderProfile, active string, builtin builtinKeyState, disabled bool,
	onProfiles func([]*affv1.ProviderProfile), onActive func(string),
) ui.Node {
	replace := func(i int, mut func(*affv1.ProviderProfile)) {
		next := make([]*affv1.ProviderProfile, len(profiles))
		for j, p := range profiles {
			cp := *p
			next[j] = &cp
		}
		mut(next[i])
		onProfiles(next)
	}

	activeControl := func(name string) ui.Node {
		if name == active {
			return h.Span(h.ClassStr("af-provider-card__active"), h.Text(t("settings.provider.active.badge")))
		}
		return affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.provider.active.use", Variant: affui.ButtonSecondary,
			Disabled: disabled,
			OnClick:  func() { onActive(name) },
		})
	}

	cards := make([]any, 0, len(profiles)+3)
	cards = append(cards, h.ClassStr("af-providers"))

	// The built-in default: not removable, not renamable. Always first, so a
	// misconfigured custom provider always has a known-good neighbour to
	// switch back to. Its key is configured HERE like any other provider's;
	// the env var only covers a dev box that booted with one.
	builtinKeyText := t("settings.provider.key.none")
	builtinKeyClass := "af-provider-card__key-state af-warning"
	switch {
	case builtin.PendingClear:
		builtinKeyText = t("settings.provider.key.willClear")
	case builtin.Pending != "":
		builtinKeyText = t("settings.provider.key.willStore")
		builtinKeyClass = "af-provider-card__key-state af-success"
	case builtin.Stored:
		builtinKeyText = t("settings.provider.key.stored")
		builtinKeyClass = "af-provider-card__key-state af-success"
	case builtin.EnvPresent:
		builtinKeyText = t("settings.provider.builtin.keyFromEnv")
		builtinKeyClass = "af-provider-card__key-state af-success"
	}
	builtinKeyPlaceholder := t("settings.provider.key.placeholder")
	if builtin.Stored {
		builtinKeyPlaceholder = t("settings.provider.key.placeholderStored")
	}
	var builtinKeyActions ui.Node = h.Span()
	switch {
	case builtin.PendingClear:
		builtinKeyActions = affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.provider.key.undoClear", Variant: affui.ButtonSecondary,
			Disabled: disabled,
			OnClick:  func() { builtin.OnClear(false) },
		})
	case builtin.Stored:
		builtinKeyActions = affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.provider.key.clear", Variant: affui.ButtonSecondary,
			Disabled: disabled,
			OnClick:  func() { builtin.OnClear(true) },
		})
	}
	cards = append(cards, h.Div(
		h.ClassStr(providerCardClass(active == "")),
		h.Div(h.ClassStr("af-provider-card__header"),
			h.H4(h.Text(t("settings.provider.profile.builtin"))),
			activeControl(""),
		),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.builtin.help"))),
		h.Div(h.ClassStr("af-provider-card__field"),
			h.Label(h.For("settings-provider-builtin-key"), h.Text(t("settings.provider.key.label"))),
			h.P(h.ClassStr(builtinKeyClass), h.Text(builtinKeyText)),
			h.Div(h.ClassStr("af-provider-card__key-row"),
				h.Input(
					h.ID("settings-provider-builtin-key"), h.Type("password"),
					h.Value(builtin.Pending), h.Disabled(disabled),
					h.AutoComplete("off"),
					h.Attr("placeholder", builtinKeyPlaceholder),
					h.OnInput(ui.UseEvent(func(e ui.InputEvent) { builtin.OnKey(e.GetValue()) })),
				),
				builtinKeyActions,
			),
			h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.key.help"))),
		),
	))

	for i, p := range profiles {
		i, p := i, p
		nameID := "settings-provider-name-" + strconv.Itoa(i)
		urlID := "settings-provider-url-" + strconv.Itoa(i)
		keyID := "settings-provider-key-" + strconv.Itoa(i)
		envID := "settings-provider-env-" + strconv.Itoa(i)

		keyPlaceholder := t("settings.provider.key.placeholder")
		if p.GetHasStoredKey() {
			keyPlaceholder = t("settings.provider.key.placeholderStored")
		}

		var keyActions ui.Node = h.Span()
		switch {
		case p.GetClearStoredKey():
			keyActions = affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.provider.key.undoClear", Variant: affui.ButtonSecondary,
				Disabled: disabled,
				OnClick: func() {
					replace(i, func(pp *affv1.ProviderProfile) { pp.ClearStoredKey = false })
				},
			})
		case p.GetHasStoredKey():
			keyActions = affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.provider.key.clear", Variant: affui.ButtonSecondary,
				Disabled: disabled,
				OnClick: func() {
					replace(i, func(pp *affv1.ProviderProfile) {
						pp.ClearStoredKey = true
						pp.ApiKey = ""
					})
				},
			})
		}

		cards = append(cards, h.Div(
			h.ClassStr(providerCardClass(active == p.GetName() && p.GetName() != "")),
			h.Div(h.ClassStr("af-provider-card__header"),
				h.Div(h.ClassStr("af-provider-card__name"),
					h.Label(h.For(nameID), h.Text(t("settings.provider.profile.name"))),
					h.Input(
						h.ID(nameID), h.Type("text"), h.Value(p.GetName()), h.Disabled(disabled),
						h.Attr("placeholder", t("settings.provider.profile.namePlaceholder")),
						h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
							v := e.GetValue()
							replace(i, func(pp *affv1.ProviderProfile) { pp.Name = v })
						})),
					),
				),
				activeControl(p.GetName()),
			),
			h.Div(h.ClassStr("af-provider-card__field"),
				h.Label(h.For(urlID), h.Text(t("settings.provider.profile.baseUrl"))),
				h.Input(
					h.ID(urlID), h.Type("url"), h.Value(p.GetBaseUrl()), h.Disabled(disabled),
					h.Attr("placeholder", "https://host/v1"),
					h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
						v := e.GetValue()
						replace(i, func(pp *affv1.ProviderProfile) { pp.BaseUrl = v })
					})),
				),
				h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.profile.baseUrlHelp"))),
			),
			h.Div(h.ClassStr("af-provider-card__field"),
				h.Label(h.For(keyID), h.Text(t("settings.provider.key.label"))),
				h.P(h.ClassStr(providerKeyStateClass(p)), h.Text(providerKeyStateText(p))),
				h.Div(h.ClassStr("af-provider-card__key-row"),
					h.Input(
						h.ID(keyID), h.Type("password"), h.Value(p.GetApiKey()), h.Disabled(disabled),
						h.AutoComplete("off"),
						h.Attr("placeholder", keyPlaceholder),
						h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
							v := e.GetValue()
							replace(i, func(pp *affv1.ProviderProfile) {
								pp.ApiKey = v
								if v != "" {
									pp.ClearStoredKey = false
								}
							})
						})),
					),
					keyActions,
				),
				h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.key.help"))),
			),
			h.Div(h.ClassStr("af-provider-card__field"),
				h.Label(h.For(envID), h.Text(t("settings.provider.envVar.label"))),
				h.Input(
					h.ID(envID), h.Type("text"), h.Value(p.GetApiKeyEnv()), h.Disabled(disabled),
					h.Attr("placeholder", "PROVIDER_API_KEY"),
					h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
						v := e.GetValue()
						replace(i, func(pp *affv1.ProviderProfile) { pp.ApiKeyEnv = v })
					})),
				),
				h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.envVar.help"))),
			),
			h.Div(h.ClassStr("af-provider-card__footer"),
				h.Button(
					h.Type("button"), h.ClassStr("af-provider-card__remove"), h.Disabled(disabled),
					h.Aria("label", t("settings.provider.profile.remove", p.GetName())),
					h.OnClick(ui.UseEvent(func() {
						next := make([]*affv1.ProviderProfile, 0, len(profiles)-1)
						next = append(next, profiles[:i]...)
						next = append(next, profiles[i+1:]...)
						onProfiles(next)
						if active == p.GetName() {
							onActive("")
						}
					})),
					h.Text(t("settings.provider.profile.removeLabel")),
				),
			),
		))
	}

	cards = append(cards, h.Div(
		h.ClassStr("af-provider-card af-provider-card--add"),
		affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.provider.profile.add", Variant: affui.ButtonSecondary,
			Disabled: disabled,
			OnClick: func() {
				onProfiles(append(append([]*affv1.ProviderProfile(nil), profiles...), &affv1.ProviderProfile{}))
			},
		}),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.provider.profile.addHelp"))),
	))

	return h.Div(cards...)
}

func providerCardClass(active bool) string {
	if active {
		return "af-provider-card af-provider-card--active"
	}
	return "af-provider-card"
}

// providerKeyStateText names where this provider's credential comes from
// right now — the stored encrypted key, the named env var, or nowhere —
// plus the one pending state (a clear queued behind Save).
func providerKeyStateText(p *affv1.ProviderProfile) string {
	switch {
	case p.GetClearStoredKey():
		return t("settings.provider.key.willClear")
	case p.GetApiKey() != "":
		return t("settings.provider.key.willStore")
	case p.GetHasStoredKey():
		return t("settings.provider.key.stored")
	case p.GetApiKeyEnv() != "" && p.GetKeyPresent():
		return t("settings.provider.key.fromEnv", p.GetApiKeyEnv())
	case p.GetApiKeyEnv() != "":
		return t("settings.provider.profile.keyMissing")
	default:
		return t("settings.provider.key.none")
	}
}

func providerKeyStateClass(p *affv1.ProviderProfile) string {
	if p.GetClearStoredKey() || (!p.GetKeyPresent() && p.GetApiKey() == "") {
		return "af-provider-card__key-state af-warning"
	}
	return "af-provider-card__key-state af-success"
}

// costWindows are the spans the chart offers. A week reads day-to-day, a
// month is the billing rhythm and the default, a quarter shows a trend.
var costWindows = []int32{7, 30, 90}

func renderWindowSelect(active int32, onChange func(int32)) ui.Node {
	opts := make([]any, 0, len(costWindows)+3)
	opts = append(opts,
		h.ID("settings-provider-cost-window"),
		h.Aria("label", t("settings.provider.cost.window")),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
			if d, err := strconv.Atoi(e.GetValue()); err == nil {
				onChange(int32(d))
			}
		})),
	)
	for _, d := range costWindows {
		v := strconv.Itoa(int(d))
		opts = append(opts, h.Option(h.Value(v), h.SelectedIf(d == active),
			h.Text(t("settings.provider.cost.windowDays", v))))
	}
	return h.Select(opts...)
}

// costCaption is the line under the hero number. It reports the hovered day
// when there is one and the window otherwise, so the big number always has
// something saying what it covers — a currency figure with no period
// attached is not an answer to any question.
func costCaption(buckets []*affv1.CostBucket, hovered int, days int32) string {
	if hovered >= 0 && hovered < len(buckets) {
		return bucketLabel(buckets[hovered])
	}
	return t("settings.provider.cost.windowCaption", strconv.Itoa(int(days)))
}
