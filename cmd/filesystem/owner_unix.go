//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// ownerOf extracts the file owner UID/GID from the underlying syscall.Stat_t.
// On systems where Sys() does not return *syscall.Stat_t (rare on unix) it
// reports ok=false so the caller can omit the fields.
func ownerOf(info fs.FileInfo) (uid uint32, gid uint32, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}
