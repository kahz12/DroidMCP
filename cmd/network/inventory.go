// Persistent device inventory backing list_devices and get_device_info.
// scan_network records every host it finds here so the two read-only tools
// can answer "what's on this network?" across restarts without re-scanning.
// The store is a small JSON file keyed by IP, guarded by a mutex and written
// atomically (temp + rename) so a crash mid-write cannot corrupt it.
package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kahz12/droidmcp/internal/logger"
)

// now is overridable so tests can assert on first_seen/last_seen.
var now = time.Now

// deviceRecord is a host remembered across scans. Ports and MAC are
// best-effort (MAC comes from ARP, only for hosts on the local subnet).
type deviceRecord struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac,omitempty"`
	OpenPorts []string  `json:"open_ports,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	TimesSeen int       `json:"times_seen"`
}

// inventory is a thread-safe, disk-backed set of known devices keyed by IP.
type inventory struct {
	path string
	mu   sync.Mutex
	byIP map[string]*deviceRecord
}

// inventoryPath resolves where the device store lives: DROIDMCP_NETWORK_DB if
// set, otherwise ~/.droidmcp/network-devices.json (temp dir if HOME is
// unknown).
func inventoryPath() string {
	if p := strings.TrimSpace(os.Getenv("DROIDMCP_NETWORK_DB")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".droidmcp", "network-devices.json")
}

// newInventory creates a store bound to path and loads any existing records.
// A missing or unreadable file yields an empty (but usable) inventory.
func newInventory(path string) *inventory {
	inv := &inventory{path: path, byIP: make(map[string]*deviceRecord)}
	inv.load()
	return inv
}

// load reads the JSON file into byIP. Best-effort: a missing file is normal
// (first run) and a corrupt file is logged and skipped rather than fatal.
func (inv *inventory) load() {
	data, err := os.ReadFile(inv.path)
	if err != nil {
		return // missing file on first run is expected
	}
	var records []*deviceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		logger.Warn("device inventory is corrupt; starting empty", "path", inv.path, "error", err.Error())
		return
	}
	for _, r := range records {
		if r != nil && r.IP != "" {
			inv.byIP[r.IP] = r
		}
	}
}

// persist writes the current records to disk atomically. Callers hold inv.mu.
func (inv *inventory) persist() error {
	records := inv.sortedLocked()
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(inv.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := inv.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, inv.path)
}

// record upserts every scanned host: bumps last_seen and times_seen, refreshes
// MAC/ports, and sets first_seen for hosts never seen before. Persisting is
// best-effort — a read-only filesystem must not fail the scan that called us.
func (inv *inventory) record(hosts []scannedHost) {
	if len(hosts) == 0 {
		return
	}
	ts := now()
	inv.mu.Lock()
	for _, h := range hosts {
		if h.IP == "" {
			continue
		}
		rec, ok := inv.byIP[h.IP]
		if !ok {
			rec = &deviceRecord{IP: h.IP, FirstSeen: ts}
			inv.byIP[h.IP] = rec
		}
		rec.LastSeen = ts
		rec.TimesSeen++
		if h.MAC != "" {
			rec.MAC = h.MAC
		}
		if len(h.OpenPorts) > 0 {
			rec.OpenPorts = h.OpenPorts
		}
	}
	err := inv.persist()
	inv.mu.Unlock()
	if err != nil {
		logger.Warn("could not persist device inventory", "path", inv.path, "error", err.Error())
	}
}

// get returns the record whose IP or MAC matches id. MAC matching is
// case-insensitive; IP matching is exact.
func (inv *inventory) get(id string) (deviceRecord, bool) {
	id = strings.TrimSpace(id)
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if rec, ok := inv.byIP[id]; ok {
		return *rec, true
	}
	idLower := strings.ToLower(id)
	for _, rec := range inv.byIP {
		if rec.MAC != "" && strings.ToLower(rec.MAC) == idLower {
			return *rec, true
		}
	}
	return deviceRecord{}, false
}

// list returns a copy of every known record, sorted by IP.
func (inv *inventory) list() []deviceRecord {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	sorted := inv.sortedLocked()
	out := make([]deviceRecord, len(sorted))
	for i, r := range sorted {
		out[i] = *r
	}
	return out
}

// sortedLocked returns the records ordered by numeric IPv4 value (falling back
// to string order for anything that isn't a v4 literal). Callers hold inv.mu.
func (inv *inventory) sortedLocked() []*deviceRecord {
	out := make([]*deviceRecord, 0, len(inv.byIP))
	for _, r := range inv.byIP {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := ipv4ToUint32(net.ParseIP(out[i].IP)), ipv4ToUint32(net.ParseIP(out[j].IP))
		if ai != aj {
			return ai < aj
		}
		return out[i].IP < out[j].IP
	})
	return out
}
