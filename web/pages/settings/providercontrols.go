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

// renderProfileSelect chooses which configured endpoint is in use. The
// built-in default is always the first option and is never removable — an
// operator must always have a way back to a known-good provider after
// misconfiguring a custom one.
func renderProfileSelect(profiles []*affv1.ProviderProfile, active string, disabled bool, onChange func(string)) ui.Node {
	opts := make([]any, 0, len(profiles)+4)
	opts = append(opts,
		h.ID("settings-provider-profile"),
		h.Disabled(disabled),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { onChange(e.GetValue()) })),
		h.Option(h.Value(""), h.SelectedIf(active == ""), h.Text(t("settings.provider.profile.builtin"))),
	)
	for _, p := range profiles {
		name := p.GetName()
		label := name
		// A profile whose key variable is not actually set on the server is
		// flagged in the menu itself, not only in the row below: choosing it
		// is the moment that matters, and finding out afterwards means a
		// failed run.
		if p.GetApiKeyEnv() != "" && !p.GetKeyPresent() {
			label = t("settings.provider.profile.noKeyOption", name, p.GetApiKeyEnv())
		}
		opts = append(opts, h.Option(h.Value(name), h.SelectedIf(name == active), h.Text(label)))
	}
	return h.Div(
		h.ClassStr("af-model-field"),
		h.Label(h.For("settings-provider-profile"), h.Text(t("settings.provider.profile.label"))),
		h.Select(opts...),
	)
}

// renderProfileEditor is the list of manually configured OpenAI-compatible
// providers: anything speaking the same wire protocol at a different base
// URL (a local Ollama or llama.cpp shim, OpenRouter, an Azure deployment, a
// self-hosted gateway).
//
// **No field here takes a key.** PLAN.md §4 keeps key material in the
// environment only — never in the database, never in a backup, never sent to
// a browser — so a profile names the environment VARIABLE holding its key.
// The server reports whether that variable is set; the value never leaves
// it. That is why the third column is "key variable" and not "key".
func renderProfileEditor(profiles []*affv1.ProviderProfile, disabled bool, onChange func([]*affv1.ProviderProfile)) ui.Node {
	replace := func(i int, mut func(*affv1.ProviderProfile)) {
		next := make([]*affv1.ProviderProfile, len(profiles))
		for j, p := range profiles {
			cp := *p
			next[j] = &cp
		}
		mut(next[i])
		onChange(next)
	}

	rows := make([]any, 0, len(profiles)+2)
	rows = append(rows, h.ClassStr("af-profiles"))
	for i, p := range profiles {
		i, p := i, p
		rows = append(rows, h.Div(
			h.ClassStr("af-profiles__row"),
			h.Input(
				h.Type("text"), h.Value(p.GetName()), h.Disabled(disabled),
				h.Aria("label", t("settings.provider.profile.name")),
				h.Attr("placeholder", t("settings.provider.profile.name")),
				h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
					v := e.GetValue()
					replace(i, func(pp *affv1.ProviderProfile) { pp.Name = v })
				})),
			),
			h.Input(
				h.Type("url"), h.Value(p.GetBaseUrl()), h.Disabled(disabled),
				h.Aria("label", t("settings.provider.profile.baseUrl")),
				h.Attr("placeholder", "https://host/v1"),
				h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
					v := e.GetValue()
					replace(i, func(pp *affv1.ProviderProfile) { pp.BaseUrl = v })
				})),
			),
			h.Input(
				h.Type("text"), h.Value(p.GetApiKeyEnv()), h.Disabled(disabled),
				h.Aria("label", t("settings.provider.profile.keyEnv")),
				h.Attr("placeholder", "PROVIDER_API_KEY"),
				h.OnInput(ui.UseEvent(func(e ui.InputEvent) {
					v := e.GetValue()
					replace(i, func(pp *affv1.ProviderProfile) { pp.ApiKeyEnv = v })
				})),
			),
			h.Span(
				h.ClassStr(profileKeyClass(p)),
				h.Text(profileKeyText(p)),
			),
			h.Button(
				h.Type("button"), h.ClassStr("af-profiles__remove"), h.Disabled(disabled),
				h.Aria("label", t("settings.provider.profile.remove", p.GetName())),
				h.OnClick(ui.UseEvent(func() {
					next := make([]*affv1.ProviderProfile, 0, len(profiles)-1)
					next = append(next, profiles[:i]...)
					next = append(next, profiles[i+1:]...)
					onChange(next)
				})),
				h.Text("×"), //nolint:i18n -- glyph, the accessible name is on aria-label
			),
		))
	}

	rows = append(rows, affui.Button(affui.ButtonProps{
		T: t, LabelKey: "settings.provider.profile.add", Variant: affui.ButtonSecondary,
		Disabled: disabled,
		OnClick: func() {
			onChange(append(append([]*affv1.ProviderProfile(nil), profiles...), &affv1.ProviderProfile{}))
		},
	}))
	return h.Div(rows...)
}

func profileKeyText(p *affv1.ProviderProfile) string {
	switch {
	case p.GetApiKeyEnv() == "":
		return t("settings.provider.profile.noKeyEnv")
	case p.GetKeyPresent():
		return t("settings.provider.profile.keySet")
	default:
		return t("settings.provider.profile.keyMissing")
	}
}

func profileKeyClass(p *affv1.ProviderProfile) string {
	if p.GetApiKeyEnv() != "" && p.GetKeyPresent() {
		return "af-profiles__key af-success"
	}
	return "af-profiles__key af-warning"
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
