//go:build !js || !wasm

package ui

// measureKebabAnchor has nothing to measure off the browser. Returning an
// unmeasured rect keeps the menu hidden, which is the honest answer for a
// host build that has no viewport at all.
func measureKebabAnchor(string, int) kebabRect { return kebabRect{} }
