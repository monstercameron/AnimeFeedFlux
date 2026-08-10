//go:build !windows && !linux

package main

import "os"

// isTerminal is conservatively false on platforms this CLI has no tested
// no-echo path for (BSD, Darwin). readPassword then falls back to a plain
// line read rather than silently echoing a password it claimed to hide.
func isTerminal(f *os.File) bool { return false }

func setEcho(f *os.File, enable bool) (restore func(), err error) {
	return func() {}, nil
}
