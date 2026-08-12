//go:build js && wasm

package generatepage

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// modelpicker.go is the one model control this page has.
//
// There were two: the strip picked a model from the provider's own list
// (SystemService.ListModels), while the recipe form under "Model parameters" —
// the field that actually gets SAVED onto the feed — was a free-text box. So
// the transient, per-preview choice was safe from typos and the permanent,
// scheduled-run one was not. That is exactly backwards: PLAN.md §8's error
// taxonomy classifies "model not found" as a recipe-scoped Fatal that disables
// the feed, and a typo is indistinguishable from a deprecation until a
// scheduled run fails at 4am.
//
// Both now render through renderModelPicker, so they cannot drift apart again.

// modelPickerProps configures one model control.
type modelPickerProps struct {
	ID    string
	Class string
	// LabelKey names the control for assistive tech when it has no visible
	// <label> of its own (the strip); the recipe form supplies one.
	LabelKey string
	Value    string
	OnChange func(string)
	Disabled bool

	// Models is the provider's list; Unavailable/Reason come from the RPC,
	// which reports "cannot reach the provider" rather than failing.
	Models      []*affv1.ProviderModel
	Unavailable bool
	Reason      string
}

// renderModelPicker renders a menu of the models this deployment's key can
// actually call, or the free-text box it used to be when that list cannot be
// fetched.
//
// The fallback is not a nicety: ListModels reports Unavailable for a missing
// key, an unreachable provider or a rate limit, and a recipe that cannot be
// edited while a third party is down is worse than one that asks for the id.
// The same reasoning covers a model the provider does not list but this
// deployment legitimately uses — it is pinned into the menu as its own option
// rather than silently retargeted to something else.
func renderModelPicker(p modelPickerProps) ui.Node {
	t := deps.I18n

	if p.Unavailable || len(p.Models) == 0 {
		reason := p.Reason
		if reason == "" {
			reason = t.T(p.LabelKey)
		}
		return h.Input(h.ID(p.ID), h.Type("text"),
			h.ClassStr(p.Class),
			h.Aria("label", t.T(p.LabelKey)),
			h.Attr("placeholder", t.T("generate.workbench.model")),
			h.Attr("title", reason),
			h.DisabledIf(p.Disabled),
			h.Value(p.Value),
			h.OnInput(p.OnChange))
	}

	// Chat models first and in their own group, because that is what this app
	// generates with. Nothing is REMOVED: the chat/embedding split is a
	// heuristic over the model id (internal/llm's ClassifyModel), and a family
	// it guesses wrong about must still be reachable.
	chat := make([]*affv1.ProviderModel, 0, len(p.Models))
	other := make([]*affv1.ProviderModel, 0, len(p.Models))
	listed := false
	for _, m := range p.Models {
		if m.GetId() == p.Value {
			listed = true
		}
		if m.GetChat() {
			chat = append(chat, m)
			continue
		}
		other = append(other, m)
	}

	opts := make([]any, 0, len(p.Models)+6)
	opts = append(opts,
		h.ID(p.ID),
		h.ClassStr(p.Class),
		h.Aria("label", t.T(p.LabelKey)),
		h.DisabledIf(p.Disabled),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { p.OnChange(e.GetValue()) })),
	)
	// One of these two placeholder options is always present, which also
	// keeps the option indices stable: dropping a leading option shifts every
	// index by one, and the browser restores a select's value BY INDEX across
	// a re-render.
	if p.Value == "" {
		opts = append(opts, h.Option(h.Value(""), h.SelectedIf(true),
			h.Text(t.T("generate.workbench.modelDefault"))))
	} else if !listed {
		opts = append(opts, h.Option(h.Value(p.Value), h.SelectedIf(true),
			h.Text(t.T("generate.workbench.modelUnlisted", p.Value))))
	}
	appendGroup := func(label string, models []*affv1.ProviderModel) {
		if len(models) == 0 {
			return
		}
		group := make([]any, 0, len(models)+1)
		group = append(group, h.Attr("label", label))
		for _, m := range models {
			id := m.GetId()
			group = append(group, h.Option(h.Value(id), h.SelectedIf(id == p.Value), h.Text(id)))
		}
		opts = append(opts, h.Optgroup(group...))
	}
	appendGroup(t.T("generate.workbench.modelGroupChat"), chat)
	appendGroup(t.T("generate.workbench.modelGroupOther"), other)
	return h.Select(opts...)
}
