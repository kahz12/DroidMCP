//go:build !linux

package main

// statStorage is a stub for non-Linux builds; DroidMCP targets Android
// (Linux), so elsewhere the tool reports the platform as unsupported.
func statStorage(path string) storageEntry {
	return storageEntry{Path: path, Error: "get_storage is only supported on Linux/Android"}
}
