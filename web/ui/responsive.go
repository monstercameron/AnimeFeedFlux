package ui

import "github.com/monstercameron/GoWebComponents/v5/css"

// Breakpoint is a coarse viewport-width tier. AnimeFeedFlux's admin surfaces
// need exactly one responsive switch point today (D5-01, PLAN.md §12.6): a
// primitive either has the room a table/tablist/modal/menu/toast was
// designed for, or it doesn't. This is kept as a named tier rather than a
// bare bool so a second step (e.g. a tablet-width tier) has somewhere to go
// later without renaming every call site.
type Breakpoint int

const (
	// BreakpointRegular is the default tier: no narrow-width overrides apply.
	BreakpointRegular Breakpoint = iota
	// BreakpointNarrow is phone/small-tablet-portrait width and below.
	BreakpointNarrow
)

// NarrowMaxWidth is the widest viewport (in CSS px) still classified
// BreakpointNarrow. 640 is the single number every primitive's narrow-width
// CSS (via narrowMedia, below) and SelectBreakpoint must agree on — by
// keeping exactly one call site (narrowMedia) that turns it into a media
// query, a future change to the threshold cannot desync CSS from logic.
const NarrowMaxWidth = 640

// SelectBreakpoint is the pure, host-testable half of the responsive switch:
// given a viewport width in CSS px, which tier applies. It has no DOM
// dependency — nothing in web/ui currently needs to branch Go-side on the
// live viewport width (every primitive's narrow-width behaviour below is
// expressed as CSS, which the browser evaluates itself), but the threshold
// decision is exercised here so it is unit-tested independent of a browser,
// and so a future caller that DOES need the Go-side answer (e.g. choosing
// between two different Node trees rather than two CSS rule sets) has a
// single correct function to call instead of re-deriving the threshold.
func SelectBreakpoint(viewportWidthPx int) Breakpoint {
	if viewportWidthPx <= NarrowMaxWidth {
		return BreakpointNarrow
	}
	return BreakpointRegular
}

// narrowMedia scopes rules to BreakpointNarrow viewports. Every primitive's
// narrow-width override goes through this so NarrowMaxWidth has exactly one
// place that becomes an actual @media query.
func narrowMedia(rules ...css.Rule) []css.Rule {
	return css.Media(css.MaxW(NarrowMaxWidth), rules...)
}
