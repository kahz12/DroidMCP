// Storage reporting for get_storage. The statfs syscall lives in
// storage_linux.go / storage_other.go so the package still builds on
// platforms without syscall.Statfs (the tool then returns an error entry).
package main

import (
	"context"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// storageEntry is the per-mount JSON shape for get_storage.
type storageEntry struct {
	Path           string  `json:"path"`
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	Error          string  `json:"error,omitempty"`
}

type storageResult struct {
	Mounts []storageEntry `json:"mounts"`
}

// defaultStoragePaths returns the locations worth reporting on a stock
// Termux install: home, the package prefix, and shared storage. Paths that
// do not exist are filtered out by the handler.
func defaultStoragePaths() []string {
	return []string{
		os.Getenv("HOME"),
		os.Getenv("PREFIX"),
		"/storage/emulated/0",
		"/sdcard",
	}
}

func handleGetStorage(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if p := strings.TrimSpace(req.GetString("path", "")); p != "" {
		entry := statStorage(p)
		if entry.Error != "" {
			return mcp.NewToolResultError(entry.Error), nil
		}
		return jsonResult(storageResult{Mounts: []storageEntry{entry}})
	}

	var mounts []storageEntry
	seen := map[string]bool{}
	for _, p := range defaultStoragePaths() {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			continue
		}
		mounts = append(mounts, statStorage(p))
	}
	if len(mounts) == 0 {
		return mcp.NewToolResultError("no storage paths available; pass an explicit path"), nil
	}
	return jsonResult(storageResult{Mounts: mounts})
}
