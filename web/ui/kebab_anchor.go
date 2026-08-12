package ui

import (
	"strconv"
	"time"
)

// kebab_anchor.go holds the menu's position type. The measurement itself is
// split by build tag: it needs syscall/js, and this package is compiled for
// the host too (its tests run there).

// kebabRect is a measured menu position, in viewport coordinates.
type kebabRect struct {
	top, left float64
	measured  bool
}

// style renders the position for the overlay surface.
//
// Every key is present in every state, always. Dropping a key does not clear
// the property: the DOM diff has nothing to compare against, so the old value
// stays on the element. The first version of this omitted `visibility` once a
// measurement existed, and the `hidden` it had set moments earlier stuck —
// the menu was correctly positioned, on top, and invisible.
//
// It starts hidden so the menu is not painted in the wrong place for the one
// frame between opening and measuring.
func (r kebabRect) style() map[string]string {
	if !r.measured {
		return map[string]string{
			"position":   "fixed",
			"visibility": "hidden",
			"top":        "0",
			"left":       "0",
		}
	}
	return map[string]string{
		"position":   "fixed",
		"visibility": "visible",
		"top":        strconv.FormatFloat(r.top, 'f', 0, 64) + "px",
		"left":       strconv.FormatFloat(r.left, 'f', 0, 64) + "px",
	}
}

// kebabMenuWidth/kebabItemHeight are the estimates used to keep the menu on
// screen. They only have to be close: they decide whether the menu opens
// upward or downward and how far left it is nudged, and both have a clamp
// behind them.
//
// Each carries its own ignore because every one of them is referenced only
// from measureKebabAnchor in the `js && wasm` file, which staticcheck's
// host-target analysis cannot see — the same situation as
// web/pages/generate/logic.go's float/int formatters, and the same
// annotation. staticcheck binds //lint:ignore to a single const spec, not to
// the block, so one comment above `const (` would silence only the first.
// Deleting any of them breaks the wasm build while leaving the host build
// green, which is the worst way to find out.
const (
	//lint:ignore U1000 used from kebab_anchor_js.go
	kebabMenuWidth = 190.0
	//lint:ignore U1000 used from kebab_anchor_js.go
	kebabItemHeight = 36.0
	//lint:ignore U1000 used from kebab_anchor_js.go
	kebabMenuPadY = 16.0
	//lint:ignore U1000 used from kebab_anchor_js.go
	kebabGap = 4.0
	//lint:ignore U1000 used from kebab_anchor_js.go
	kebabMargin = 8.0
)

// kebabDismissGuard is how long after an outside-click dismissal the trigger
// ignores its own click. Long enough to cover the gap between pointerdown and
// click (including a slow, deliberate press), short enough that it can never
// swallow a second, intentional click.
const kebabDismissGuard = 300 * time.Millisecond

// kebabNoDismiss is the "never dismissed" sentinel.
var kebabNoDismiss = time.Time{}

func kebabNow() time.Time { return time.Now() }

// kebabSinceDismiss reports how long ago the menu was dismissed, or -1 when
// it never has been.
func kebabSinceDismiss(at time.Time) time.Duration {
	if at.IsZero() {
		return -1
	}
	return time.Since(at)
}
