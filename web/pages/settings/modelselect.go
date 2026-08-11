//go:build js && wasm

package settings

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// modelselect.go turns the Provider section's two model fields from text
// boxes into menus hydrated from the provider itself.
//
// A typed model id is a per-feed outage waiting to happen: PLAN.md §8's
// error taxonomy classifies "model not found" as a recipe-scoped Fatal that
// disables that feed, and a typo looks exactly like a deprecation until a
// scheduled run fails at 4am. The list comes from
// SystemService.ListModels (internal/rpc/models.go), which asks OpenAI
// server-side — the API key never reaches the browser (§4).
//
// # The free-text fallback is not a nicety
//
// ListModels never fails the request; it reports Unavailable instead (no key
// configured, provider unreachable, rate-limited). When it does, this
// renders the ORIGINAL text input, because a settings screen that cannot be
// configured while a third party is down is worse than one that asks the
// operator to type the id — which is what they did before this existed.
// The same fallback covers a model the provider does not list but the
// deployment legitimately uses.

// modelFilter selects which half of the provider's list a field offers.
type modelFilter int

const (
	// chatModels is the default-model field: text generation.
	chatModels modelFilter = iota
	// embeddingModels is the embedding-model field (§9.5's novelty gate).
	embeddingModels
)

// modelSelectProps configures renderModelSelect.
type modelSelectProps struct {
	ID       string
	LabelKey string
	Value    string
	OnChange func(string)
	Disabled bool

	// Models is the full provider list; Filter picks the relevant half.
	Models []*affv1.ProviderModel
	// Unavailable and Reason come straight from the RPC.
	Unavailable bool
	Reason      string
}

// renderModelSelect renders either a menu of real models or, when the list
// could not be fetched, the text input this field used to be.
func renderModelSelect(p modelSelectProps, filter modelFilter) ui.Node {
	if p.Unavailable || len(p.Models) == 0 {
		return h.Div(
			h.ClassStr("af-model-field"),
			affui.Input(affui.InputProps{
				T: t, ID: p.ID, LabelKey: p.LabelKey,
				Value: p.Value, OnChange: p.OnChange, Disabled: p.Disabled,
			}),
			h.Show(p.Reason != "", h.P(h.ClassStr("af-warning"), h.Text(p.Reason))),
		)
	}

	matching := make([]*affv1.ProviderModel, 0, len(p.Models))
	other := make([]*affv1.ProviderModel, 0, len(p.Models))
	for _, m := range p.Models {
		if (filter == chatModels && m.GetChat()) || (filter == embeddingModels && m.GetEmbedding()) {
			matching = append(matching, m)
			continue
		}
		other = append(other, m)
	}

	// The currently-saved value always appears and is always selectable,
	// even if the provider did not list it. Otherwise opening this screen
	// would silently re-point a working feed at whatever the browser picked
	// as the first option — a settings page must never change a setting just
	// by being looked at.
	known := false
	for _, m := range p.Models {
		if m.GetId() == p.Value {
			known = true
			break
		}
	}

	opts := make([]any, 0, len(p.Models)+4)
	opts = append(opts,
		h.ID(p.ID),
		h.Disabled(p.Disabled),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { p.OnChange(e.GetValue()) })),
	)
	if p.Value == "" {
		opts = append(opts, h.Option(h.Value(""), h.SelectedIf(true), h.Text(t("settings.provider.model.choose"))))
	}
	if !known && p.Value != "" {
		opts = append(opts, h.Option(
			h.Value(p.Value), h.SelectedIf(true),
			h.Text(t("settings.provider.model.notListed", p.Value)),
		))
	}
	opts = append(opts, modelGroup(t("settings.provider.model.group.recommended"), matching, p.Value))
	if len(other) > 0 {
		opts = append(opts, modelGroup(t("settings.provider.model.group.other"), other, p.Value))
	}

	return h.Div(
		h.ClassStr("af-model-field"),
		h.Label(h.For(p.ID), h.Text(t(p.LabelKey))),
		h.Select(opts...),
	)
}

// modelGroup renders one <optgroup>. The provider's own "owned by" value is
// shown after the id because a long list is otherwise a wall of
// near-identical strings.
func modelGroup(label string, models []*affv1.ProviderModel, selected string) ui.Node {
	if len(models) == 0 {
		return h.Fragment()
	}
	args := make([]any, 0, len(models)+1)
	args = append(args, h.Attr("label", label))
	for _, m := range models {
		id := m.GetId()
		text := id
		if owner := m.GetOwnedBy(); owner != "" && owner != "openai" {
			text = id + " — " + owner
		}
		args = append(args, h.Option(h.Value(id), h.SelectedIf(id == selected), h.Text(text)))
	}
	return h.Optgroup(args...)
}
