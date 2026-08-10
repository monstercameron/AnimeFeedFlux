//go:build !windows && !linux

package main

import "errors"

// diskFreeBytes has no implementation on platforms this CLI has no tested
// syscall path for (BSD, Darwin) — matching term_fallback.go's precedent of
// degrading honestly rather than guessing. `aff doctor` treats this error as
// "disk space could not be checked" and skips that one check rather than
// failing the whole report on it.
func diskFreeBytes(path string) (free, total uint64, err error) {
	return 0, 0, errors.New("aff: disk space check is not implemented on this platform")
}
