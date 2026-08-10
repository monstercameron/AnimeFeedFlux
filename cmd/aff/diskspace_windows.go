//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// diskFreeBytes reports free and total space on the volume holding path,
// used by `aff doctor`'s disk-space check. golang.org/x/sys/windows is not a
// new dependency — see term_windows.go's identical note; it is already
// present transitively.
func diskFreeBytes(path string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return freeAvail, totalBytes, nil
}
