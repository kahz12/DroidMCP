//go:build linux

package main

import (
	"fmt"
	"syscall"
)

// statStorage fills a storageEntry from statfs(2). Available reflects the
// space usable by an unprivileged caller (f_bavail), while Used is computed
// against the true free count (f_bfree) so reserved blocks are not counted
// as used space twice.
func statStorage(path string) storageEntry {
	entry := storageEntry{Path: path}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		entry.Error = fmt.Sprintf("statfs %s: %v", path, err)
		return entry
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	entry.TotalBytes = total
	entry.UsedBytes = total - st.Bfree*bsize
	entry.AvailableBytes = st.Bavail * bsize
	if total > 0 {
		entry.UsedPercent = float64(entry.UsedBytes) / float64(total) * 100
	}
	return entry
}
