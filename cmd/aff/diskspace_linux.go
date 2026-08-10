//go:build linux

package main

import (
	"golang.org/x/sys/unix"
)

// diskFreeBytes reports free and total space on the filesystem holding path,
// used by `aff doctor`'s disk-space check. Bavail (available to an
// unprivileged process), not Bfree, is the number that matters here — Bfree
// includes space reserved for root, which this process is not.
func diskFreeBytes(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, nil
}
