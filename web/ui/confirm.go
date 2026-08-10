package ui

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/css"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ConfirmMatches reports whether typed matches the required confirmation
// phrase. It trims leading/trailing whitespace from typed only (a trailing
// newline from a paste, or accidental leading space, should not fail an
// otherwise-correct confirmation) but is otherwise an EXACT, case-sensitive
// comparison — a typed-confirmation gate that folds case defeats its own
// purpose of proving the operator read and reproduced the exact phrase
// rather than something that merely case-insensitively resembles it. Pure
// function, host-testable (TODOS.md Tests §: "typed-confirmation matching").
//
// # D6-16's translation question
//
// TODOS.md D6-16 requires deciding explicitly whether the typed word is
// translated, and if so that the comparison translates too. This package
// takes RequiredPhrase as an already-resolved plain string (ConfirmProps
// below), not an i18n key — the caller decides whether it is the resolved
// (translated) text or a fixed non-translated token (e.g. always requiring
// the feed's own slug, which is data, not interface text, and is never
// translated). Either way ConfirmMatches only ever compares two already-
// resolved strings, so it can never itself be the place a translation
// mismatch hides.
func ConfirmMatches(typed, requiredPhrase string) bool {
	return strings.TrimSpace(typed) == requiredPhrase
}

// ConfirmProps configures Confirm — the typed-confirmation modal every
// irreversible action must go through (PLAN.md §12.6, TODOS.md D0-16): purge,
// revoke-all-sessions, regenerate-recovery-codes, and similar. RequiredPhrase
// is what the operator must type verbatim (see ConfirmMatches's doc comment
// on i18n); a good RequiredPhrase names the specific thing being destroyed
// (a feed's slug, not the word "delete") so the confirmation also functions
// as a last look at what is about to happen.
type ConfirmProps struct {
	T              T
	ID             string
	TitleKey       string
	MessageKey     string
	MessageArgs    []any
	RequiredPhrase string
	Typed          string
	OnTypedChange  func(string)
	OnConfirm      func()
	OnCancel       func()
	Open           bool
	Busy           bool
}

// Confirm renders the typed-confirmation modal. The confirm button stays
// disabled until ConfirmMatches(Typed, RequiredPhrase) is true — there is no
// separate "are you sure" click to race past.
func Confirm(p ConfirmProps) Node {
	matches := ConfirmMatches(p.Typed, p.RequiredPhrase)

	children := []Node{
		h.Div(css.Class([]css.Rule{
			css.TextColor(tokens.Color(tokens.RoleTextMuted)),
			css.FontSize(tokens.FontSize(tokens.TextSm)),
			bodyFont(),
		}), resolve(p.T, p.MessageKey, p.MessageArgs...)),
		h.Div(css.Class([]css.Rule{
			css.Display.Flex,
			css.FlexDir.Col,
			css.Gap(tokens.Space(1)),
		}),
			h.Span(css.Class([]css.Rule{
				css.FontSize(tokens.FontSize(tokens.TextXs)),
				css.TextColor(tokens.Color(tokens.RoleTextMuted)),
				bodyFont(),
			}), resolve(p.T, "confirm.typePhrase", p.RequiredPhrase)),
			h.Span(css.Class([]css.Rule{
				monoFont(),
				css.FontWeight.Semibold,
				css.TextColor(tokens.Color(tokens.RoleText)),
			}), p.RequiredPhrase),
		),
		Input(InputProps{
			T:        p.T,
			ID:       p.ID + "-input",
			LabelKey: "confirm.typeLabel",
			Value:    p.Typed,
			OnChange: p.OnTypedChange,
			Mono:     true,
		}),
		h.Div(css.Class([]css.Rule{
			css.Display.Flex,
			css.Justify.End,
			css.Gap(tokens.Space(2)),
		}),
			Button(ButtonProps{T: p.T, LabelKey: "action.cancel", Variant: ButtonSecondary, OnClick: p.OnCancel, Disabled: p.Busy}),
			Button(ButtonProps{
				T: p.T, LabelKey: "action.confirmDestroy", Variant: ButtonDanger,
				OnClick: p.OnConfirm, Disabled: !matches || p.Busy, Busy: p.Busy,
			}),
		),
	}

	return Modal(ModalProps{
		T:          p.T,
		ID:         p.ID,
		TitleKey:   p.TitleKey,
		Open:       p.Open,
		OnDismiss:  p.OnCancel,
		Children:   children,
		Persistent: p.Busy,
	})
}
