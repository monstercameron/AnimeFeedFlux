//go:build js && wasm

// Command web is the AnimeFeedFlux admin WASM entrypoint (TODOS.md D0-01):
// mounts the shell (routing, auth guard, DISCONNECTED banner) into the
// page's #app element and blocks forever via utils.WaitForever, matching
// the documented GWC entrypoint pattern (see web/shell's package doc
// comment for why this is built on GoWebComponents v5, not the earlier
// hand-rolled syscall/js version).
package main

import (
	"context"

	"github.com/monstercameron/GoWebComponents/v5/utils"

	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

func main() {
	// Page-specific bodies get registered here by later waves, e.g.:
	//   shell.RegisterPage("/login", loginpage.Render)
	//   shell.RegisterPage("/generate", generatepage.Render)
	// Any route left unregistered renders a labeled placeholder instead
	// of a blank box (see web/shell/pages.go's renderPageBody).

	shell.Mount(context.Background(), "#app")

	// Block forever: returning from main tears down the Go runtime and
	// the whole app goes dead, same as every wasm_exec.js program.
	utils.WaitForever()
}
