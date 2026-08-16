//go:build js && wasm

package generatepage

import (
	"strconv"
	"syscall/js"

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

	// Force the DOM's actual selection to agree with p.Value once the
	// <select> exists (TODOS.md A7-22, confirmed live 2026-08-14).
	//
	// Root cause, traced into GoWebComponents/v5@v5.0.1's reconciler rather
	// than this file: the control renders as a plain <input> while the
	// model list is unavailable/loading, then swaps to a <select> carrying
	// 100+ <option>s across two <optgroup>s once ListModels resolves. That
	// swap reproducibly lands the browser's actual selection on an
	// unrelated option — observed settling on the FIRST option of the
	// LAST-inserted <optgroup>, i.e. an embedding model, not on the ""
	// placeholder this file's own SelectedIf(true) declares below. No
	// change/input DOM event fires when this happens (instrumented and
	// confirmed: this is the browser's native "no option marked selected on
	// this large a tree" resolution outracing GWC's per-option property
	// writes, not anything reading the wrong prop). Because no event fires,
	// Go-side state (p.Value, and therefore what actually gets saved) stays
	// correct even when the DOM visibly disagrees — verified by saving
	// under the bug and reading the row back fresh: the saved model was
	// always "".
	//
	// A single post-commit correction is NOT enough: instrumented and
	// confirmed the wrong auto-selection lands on a LATER DOM mutation than
	// this effect's own — the reconciler appends the two <optgroup>s (100+
	// <option>s) to the <select> across more than one commit, and the
	// browser's own default-selection algorithm mis-fires on one of the
	// later appends, after an effect running once at mount already found
	// the value correct. So this watches every subsequent child mutation
	// via MutationObserver and re-asserts p.Value each time, converging to
	// correct regardless of how many discrete appends the swap takes —
	// bypassing GWC's declarative diff (which does not reliably win this
	// race) with a direct DOM write, the same escape hatch
	// renderVariableChips already uses for a different case where props
	// alone are not enough.
	ready := !p.Unavailable && len(p.Models) > 0
	effectKey := strconv.FormatBool(ready) + "|" + p.Value
	ui.UseEffectOf(func() func() {
		if !ready {
			return nil
		}
		el := js.Global().Get("document").Call("getElementById", p.ID)
		if !el.Truthy() {
			return nil
		}
		value := p.Value
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
