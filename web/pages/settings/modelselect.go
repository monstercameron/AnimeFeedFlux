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

// useSelectValueGuard re-asserts a <select>'s value against the browser's
// own default-selection algorithm mis-firing during GWC's input→select swap
// — the exact race web/pages/generate/modelpicker.go documents and fixes
// (TODOS A7-22): the control renders as a text input until the model list
// arrives, then swaps to a <select> whose 100+ options land across more
// than one reconciler commit, and the browser's native selection settles on
// an unrelated option with no event fired. Saved state stays correct; the
// DOM lies. Same MutationObserver correction, applied here because the
// settings page's model selects (and the price table's) have the identical
// shape and were never given the fix.
//
// HOOK DISCIPLINE: this contains ui.UseEffectOf, so callers must invoke it
// a FIXED number of times per render — top-level controls only, never once
// per table row (the price table gets one shared guard over all its rows in
// render_provider.go for exactly this reason).
func useSelectValueGuard(id, value string, ready bool) {
	effectKey := strconv.FormatBool(ready) + "|" + id + "|" + value
	ui.UseEffectOf(func() func() {
		if !ready {
			return nil
		}
		el := js.Global().Get("document").Call("getElementById", id)
		if !el.Truthy() {
			return nil
		}
		correct := func() {
			if el.Get("value").String() != value {
				el.Set("value", value)
			}
		}
		correct()
		var cb js.Func
		cb = js.FuncOf(func(this js.Value, args []js.Value) any {
			correct()
			return nil
		})
		observer := js.Global().Get("MutationObserver").New(cb)
		observer.Call("observe", el, map[string]any{"childList": true, "subtree": true})
		return func() {
			observer.Call("disconnect")
			cb.Release()
		}
	}, effectKey)
}

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
	// Unconditional, before any branch — hook rules (see useSelectValueGuard).
	useSelectValueGuard(p.ID, p.Value, !p.Unavailable && len(p.Models) > 0)
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

// renderPriceModelSelect is the price table's model cell: a rate is only
// meaningful when it names a model the provider can actually run, so the
// cell offers the same provider-reported list the two model fields above it
// use rather than a typing test (a mistyped id here silently prices
// nothing — every run of that model reports $0.0000). A bare <select> with
// an aria-label, not a labeled field group, because it lives in a table
// cell under a column header that already names it.
//
// Same two escapes as renderModelSelect, for the same reasons: when the
// list is unavailable this degrades to the text input the cell used to be,
// and a saved value the provider no longer lists stays present and
// selected — opening the screen must never re-point a rate by itself.
// NO hooks in here — this renders once per table row, and a variable number
// of hook calls per render violates GWC's positional-hook rule. The value
// guard the swap race needs (useSelectValueGuard's doc) is applied for ALL
// rows at once by render_provider.go's single table-level effect, keyed on
// the id passed here.
func renderPriceModelSelect(id, value, ariaLabel string, models []*affv1.ProviderModel, unavailable bool, onChange func(string)) ui.Node {
	if unavailable || len(models) == 0 {
		return h.Input(h.ID(id), h.Type("text"), h.Value(value),
			h.Aria("label", ariaLabel),
			h.Attr("placeholder", t("settings.provider.model.choose")),
			h.OnInput(ui.UseEvent(func(e ui.InputEvent) { onChange(e.GetValue()) })))
	}

	chat := make([]*affv1.ProviderModel, 0, len(models))
	other := make([]*affv1.ProviderModel, 0, len(models))
	known := false
	for _, m := range models {
		if m.GetId() == value {
			known = true
		}
		if m.GetChat() {
			chat = append(chat, m)
		} else {
			other = append(other, m)
		}
	}

	opts := make([]any, 0, len(models)+6)
	opts = append(opts,
		h.ID(id),
		h.Aria("label", ariaLabel),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { onChange(e.GetValue()) })),
	)
	if value == "" {
		opts = append(opts, h.Option(h.Value(""), h.SelectedIf(true), h.Text(t("settings.provider.model.choose"))))
	}
	if !known && value != "" {
		opts = append(opts, h.Option(
			h.Value(value), h.SelectedIf(true),
			h.Text(t("settings.provider.model.notListed", value)),
		))
	}
	opts = append(opts, modelGroup(t("settings.provider.model.group.recommended"), chat, value))
	if len(other) > 0 {
		opts = append(opts, modelGroup(t("settings.provider.model.group.other"), other, value))
	}
	return h.Select(opts...)
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
