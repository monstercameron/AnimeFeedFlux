//go:build js

package history

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// TypedConfirmProps drives a typed-confirmation dialog (PLAN.md §12.6,
// TODOS.md D0-16: "irreversible ones require typed confirmation"). It is
// hand-rolled on top of ui.AccessibleOverlay because no shared web/ui
// primitive exists yet (see doc.go) — this is not this page's preferred
// long-term home for a confirmation dialog, just what is available today
// without importing the directory another agent owns.
//
// Used for RunService.Delete specifically: unlike Items' soft
// delete+Restore, a deleted run has no undo (proto/aff/v1/run.proto's own
// comment: "deleting a historical row is not a 'recipe'... " — there is no
// expected_version and no restore RPC), so it is the one delete action on
// this page that actually needs the irreversible-action gate rather than
// just living behind a kebab.
type TypedConfirmProps struct {
	T          Catalog
	Open       bool
	TitleKey   string
	PromptKey  string // e.g. "history.confirm.type_delete" — must tell the operator what to type
	MatchWord  string // the literal the operator must type, e.g. "DELETE"
	OnConfirm  func()
	OnCancel   func()
	LabelledBy string
}

// TypedConfirm renders nothing when Open is false; the caller controls
// mount/unmount, which resets the typed-input state for free each time it
// reopens (a fresh component instance).
func TypedConfirm(props TypedConfirmProps) ui.Node {
	if !props.Open {
		return ui.Fragment()
	}

	typed := ui.UseState("")
	matches := typed.Get() == props.MatchWord

	return ui.AccessibleOverlay(ui.AccessibleOverlayProps{
		Open:                true,
		Modal:               true,
		TrapFocus:           true,
		RestoreFocus:        true,
		CloseOnEscape:       true,
		CloseOnOutsideClick: false,
		LockScroll:          true,
		Backdrop:            true,
		LabelledBy:          props.LabelledBy,
		OnDismiss:           props.OnCancel,
		Child: h.Div(
			h.ClassStr("history-confirm-dialog"),
			h.H2(h.ID(props.LabelledBy), props.T.T(props.TitleKey, nil)),
			h.P(props.T.T(props.PromptKey, map[string]any{"word": props.MatchWord})),
			h.Input(
				h.Value(typed.Get()),
				h.OnInput(func(ev ui.InputEvent) { typed.Set(ev.GetValue()) }),
			),
			h.Div(h.ClassStr("history-confirm-actions"),
				h.Button(h.Type("button"), h.Disabled(!matches), h.OnClick(func() {
					props.OnConfirm()
				}), props.T.T("history.confirm.confirm", nil)),
				h.Button(h.Type("button"), h.OnClick(props.OnCancel), props.T.T("history.cancel", nil)),
			),
		),
	})
}
