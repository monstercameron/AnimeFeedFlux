//go:build js && wasm

package ui

import "syscall/js"

// measureKebabAnchor reads the trigger's position and works out where the
// menu should sit: below it by default, above it when there is no room, and
// right-aligned to the trigger but never off either edge.
func measureKebabAnchor(triggerID string, itemCount int) kebabRect {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return kebabRect{}
	}
	el := doc.Call("getElementById", triggerID)
	if !el.Truthy() {
		return kebabRect{}
	}
	rect := el.Call("getBoundingClientRect")
	if !rect.Truthy() {
		return kebabRect{}
	}
	top := rect.Get("bottom").Float() + kebabGap
	right := rect.Get("right").Float()

	win := js.Global()
	viewH := win.Get("innerHeight").Float()
	viewW := win.Get("innerWidth").Float()
	menuH := float64(itemCount)*kebabItemHeight + kebabMenuPadY

	// Flip above the trigger when it would otherwise run off the bottom.
	if top+menuH > viewH-kebabMargin {
		if above := rect.Get("top").Float() - kebabGap - menuH; above > kebabMargin {
			top = above
		} else {
			// Neither side fits: pin it inside the viewport rather than
			// letting it hang off.
			top = viewH - menuH - kebabMargin
			if top < kebabMargin {
				top = kebabMargin
			}
		}
	}

	left := right - kebabMenuWidth
	if left < kebabMargin {
		left = kebabMargin
	}
	if max := viewW - kebabMenuWidth - kebabMargin; left > max {
		left = max
	}
	return kebabRect{top: top, left: left, measured: true}
}
