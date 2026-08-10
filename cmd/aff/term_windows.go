//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows' stdlib `syscall` package exposes GetConsoleMode but not
// SetConsoleMode (verified against $GOROOT/src/syscall on this build), so
// the mode can be read but never restored through the standard library
// alone. golang.org/x/sys/windows is not a new dependency — it is already
// present in go.sum via existing indirect requirements (grpc's transitive
// graph) — so this uses that instead of hand-rolling a DLL call.

// isTerminal reports whether f is attached to a real console. A successful
// GetConsoleMode call is itself the test: it only succeeds on a console
// handle, which is exactly the case where toggling echo makes sense (a
// piped/redirected stdin, used by tests and scripted `admin init`, fails
// this and falls back to a plain read).
func isTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

// setEcho clears (enable=false) or restores (enable=true) ENABLE_ECHO_INPUT
// on f's console mode and returns a restore func that puts the original mode
// back — always call it, even on a later error, or the terminal is left
// silently echo-less for the rest of the session.
func setEcho(f *os.File, enable bool) (restore func(), err error) {
	handle := windows.Handle(f.Fd())
	var oldMode uint32
	if err := windows.GetConsoleMode(handle, &oldMode); err != nil {
		return func() {}, err
	}
	newMode := oldMode
	if enable {
		newMode |= windows.ENABLE_ECHO_INPUT
	} else {
		newMode &^= windows.ENABLE_ECHO_INPUT
	}
	if err := windows.SetConsoleMode(handle, newMode); err != nil {
		return func() {}, err
	}
	return func() { _ = windows.SetConsoleMode(handle, oldMode) }, nil
}
