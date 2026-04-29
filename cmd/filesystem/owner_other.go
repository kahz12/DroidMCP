//go:build !unix

package main

import "io/fs"

// ownerOf is a no-op on non-unix platforms (Windows/Plan9) where uid/gid don't
// map cleanly. The JSON output simply omits those fields.
func ownerOf(_ fs.FileInfo) (uint32, uint32, bool) {
	return 0, 0, false
}
