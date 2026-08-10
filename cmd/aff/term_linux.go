//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal reports whether f is a real tty. IoctlGetTermios only succeeds
// against a terminal device, so a piped/redirected stdin (tests, scripted
// `admin init`) falls through to a plain read instead.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

// setEcho clears (enable=false) or restores (enable=true) the ECHO bit in
// the terminal's local mode flags. Unlike Windows, Unix termios has no
// separate "echo" toggle API — it is one bit in a larger flags word that
// must be read, modified, and written back whole, which is why this saves
// and restores the entire termios struct rather than a single field.
func setEcho(f *os.File, enable bool) (restore func(), err error) {
	fd := int(f.Fd())
	orig, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return func() {}, err
	}
	t := *orig
	if enable {
		t.Lflag |= unix.ECHO
	} else {
		t.Lflag &^= unix.ECHO
	}
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &t); err != nil {
		return func() {}, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, orig) }, nil
}
