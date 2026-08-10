package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// Node is this package's export of the GWC render-tree node type, so callers
// composing these primitives do not also need to import
// github.com/monstercameron/GoWebComponents/v5/ui directly just to spell the
// return type.
type Node = gwcui.Node

// focusVisible is the visible-focus treatment shared by every interactive
// primitive in this package (buttons, inputs, tabs, kebab items, …). A11y is
// not polish here: every primitive gets this, not just the ones someone
// remembered to test with a keyboard.
func focusVisible() []css.Rule {
	return css.FocusVisible(
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	)
}

// bodyFont / monoFont select the two font stacks the token layer declares —
// see web/tokens's design brief for why UI chrome and data figures use
// different stacks.
func bodyFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontSans)) }
func monoFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontMono)) }

// transitionColors is the standard hover/active color transition, gated by
// nothing extra — color/background/border transitions are not the kind of
// motion prefers-reduced-motion asks to suppress (no movement, no vestibular
// trigger), unlike the animated affordances in state.go, which do gate.
func transitionColors() css.Rule {
	return css.Transition(css.PropColors, tokens.Duration(tokens.DurationFast), tokens.Easing(tokens.EasingStd))
}
