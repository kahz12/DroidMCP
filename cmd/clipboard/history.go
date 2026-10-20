package main

import (
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	defaultHistoryEntries = 32
	defaultHistoryBytes   = int64(64 << 10) // 64 KiB total
	maxStoredEntryBytes   = int64(16 << 10) // 16 KiB per entry; bigger entries are truncated
	historyEntriesEnv     = "DROIDMCP_CLIPBOARD_HISTORY_ENTRIES"
	historyBytesEnv       = "DROIDMCP_CLIPBOARD_HISTORY_BYTES"
)

// historyEntry is one in-memory snapshot of a clipboard write. Content is
// possibly truncated; BytesLen records the original size. The clipboard
// contents stay in memory only — never written to disk.
type historyEntry struct {
	At        time.Time
	Source    string // "text" | "base64"
	Content   []byte
	BytesLen  int
	Truncated bool
}

// clipboardHistory is a bounded ring of recent writes. Eviction is FIFO: the
// oldest entry is dropped when either the entry-count or total-byte cap is
// exceeded. Reads (get_clipboard) do not push to history; only writes do.
type clipboardHistory struct {
	mu         sync.Mutex
	entries    []historyEntry
	maxEntries int
	maxBytes   int64
	curBytes   int64
}

var history = newHistoryFromEnv()

func newHistoryFromEnv() *clipboardHistory {
	return &clipboardHistory{
		maxEntries: intFromEnv(historyEntriesEnv, defaultHistoryEntries, 1, 1024),
		maxBytes:   int64FromEnv(historyBytesEnv, defaultHistoryBytes, 1024, 16<<20),
	}
}

func intFromEnv(name string, def, min, max int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func int64FromEnv(name string, def, min, max int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func (h *clipboardHistory) Push(content []byte, source string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	original := len(content)
	stored := content
	truncated := false
	limit := int(maxStoredEntryBytes)
	if len(stored) > limit {
		stored = append([]byte(nil), stored[:limit]...)
		truncated = true
	} else {
		stored = append([]byte(nil), stored...)
	}
	h.entries = append(h.entries, historyEntry{
		At:        time.Now().UTC(),
		Source:    source,
		Content:   stored,
		BytesLen:  original,
		Truncated: truncated,
	})
	h.curBytes += int64(len(stored))
	h.evict()
}

func (h *clipboardHistory) evict() {
	for len(h.entries) > 0 && (len(h.entries) > h.maxEntries || h.curBytes > h.maxBytes) {
		ev := h.entries[0]
		h.entries = h.entries[1:]
		h.curBytes -= int64(len(ev.Content))
		if h.curBytes < 0 {
			h.curBytes = 0
		}
	}
}

func (h *clipboardHistory) Snapshot() []historyEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]historyEntry, len(h.entries))
	copy(out, h.entries)
	return out
}

func (h *clipboardHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = nil
	h.curBytes = 0
}
