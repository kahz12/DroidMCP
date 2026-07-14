package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withStore points the package-level `devices` at a fresh temp-backed
// inventory for the duration of a test and restores the previous value after.
func withStore(t *testing.T) *inventory {
	t.Helper()
	path := filepath.Join(t.TempDir(), "devices.json")
	prev := devices
	inv := newInventory(path)
	devices = inv
	t.Cleanup(func() { devices = prev })
	return inv
}

func TestInventoryRecordUpsertsAndCountsSeen(t *testing.T) {
	inv := newInventory(filepath.Join(t.TempDir(), "devices.json"))

	inv.record([]scannedHost{
		{IP: "192.168.1.10", MAC: "aa:bb:cc:dd:ee:ff", OpenPorts: []string{"22"}},
		{IP: "192.168.1.20"},
	})
	inv.record([]scannedHost{
		{IP: "192.168.1.10", OpenPorts: []string{"22", "80"}},
	})

	rec, ok := inv.get("192.168.1.10")
	if !ok {
		t.Fatal("expected .10 to be known")
	}
	if rec.TimesSeen != 2 {
		t.Errorf("times_seen: got %d, want 2", rec.TimesSeen)
	}
	if rec.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC should persist across a scan that lacked it: %q", rec.MAC)
	}
	if len(rec.OpenPorts) != 2 {
		t.Errorf("open_ports should refresh to latest: %v", rec.OpenPorts)
	}
	if rec.FirstSeen.After(rec.LastSeen) {
		t.Errorf("first_seen (%v) after last_seen (%v)", rec.FirstSeen, rec.LastSeen)
	}
	if _, ok := inv.get("192.168.1.20"); !ok {
		t.Error("expected .20 to be known")
	}
}

func TestInventoryFirstSeenStableAcrossScans(t *testing.T) {
	inv := newInventory(filepath.Join(t.TempDir(), "devices.json"))

	base := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	prevNow := now
	now = func() time.Time { return base }
	inv.record([]scannedHost{{IP: "10.0.0.1"}})
	now = func() time.Time { return base.Add(time.Hour) }
	inv.record([]scannedHost{{IP: "10.0.0.1"}})
	now = prevNow

	rec, _ := inv.get("10.0.0.1")
	if !rec.FirstSeen.Equal(base) {
		t.Errorf("first_seen moved: got %v, want %v", rec.FirstSeen, base)
	}
	if !rec.LastSeen.Equal(base.Add(time.Hour)) {
		t.Errorf("last_seen not updated: got %v", rec.LastSeen)
	}
}

func TestInventoryGetByMACCaseInsensitive(t *testing.T) {
	inv := newInventory(filepath.Join(t.TempDir(), "devices.json"))
	inv.record([]scannedHost{{IP: "192.168.1.5", MAC: "AA:BB:CC:11:22:33"}})

	rec, ok := inv.get("aa:bb:cc:11:22:33")
	if !ok {
		t.Fatal("expected MAC lookup to match case-insensitively")
	}
	if rec.IP != "192.168.1.5" {
		t.Errorf("wrong record: %+v", rec)
	}
}

func TestInventoryPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "devices.json") // parent dir must be created
	inv := newInventory(path)
	inv.record([]scannedHost{
		{IP: "192.168.1.2", OpenPorts: []string{"443"}},
		{IP: "192.168.1.1", MAC: "de:ad:be:ef:00:01"},
	})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("inventory file not written: %v", err)
	}

	// A fresh inventory over the same path must see the persisted records,
	// sorted by IP.
	reloaded := newInventory(path)
	list := reloaded.list()
	if len(list) != 2 {
		t.Fatalf("reloaded %d devices, want 2", len(list))
	}
	if list[0].IP != "192.168.1.1" || list[1].IP != "192.168.1.2" {
		t.Errorf("not sorted by IP: %+v", list)
	}
}

func TestInventoryCorruptFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv := newInventory(path)
	if got := len(inv.list()); got != 0 {
		t.Errorf("expected empty inventory from corrupt file, got %d", got)
	}
	// And it must still be writable afterwards.
	inv.record([]scannedHost{{IP: "10.1.1.1"}})
	if _, ok := inv.get("10.1.1.1"); !ok {
		t.Error("inventory unusable after recovering from corrupt file")
	}
}

func TestHandleListDevicesEmpty(t *testing.T) {
	withStore(t)
	res, err := handleListDevices(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got listDevicesResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Count != 0 || len(got.Devices) != 0 {
		t.Errorf("expected empty list, got %+v", got)
	}
}

func TestHandleGetDeviceInfoFound(t *testing.T) {
	inv := withStore(t)
	inv.record([]scannedHost{{IP: "192.168.1.50", MAC: "aa:bb:cc:dd:ee:01", OpenPorts: []string{"80"}}})

	res, err := handleGetDeviceInfo(context.Background(), callRequest(map[string]any{
		"device": "192.168.1.50",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got deviceRecord
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.IP != "192.168.1.50" || got.MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("wrong record: %+v", got)
	}
}

func TestHandleGetDeviceInfoNotFound(t *testing.T) {
	withStore(t)
	res, _ := handleGetDeviceInfo(context.Background(), callRequest(map[string]any{
		"device": "192.168.1.250",
	}))
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected not-found error, got %s", text)
	}
}

func TestHandleGetDeviceInfoMissingArg(t *testing.T) {
	withStore(t)
	res, _ := handleGetDeviceInfo(context.Background(), callRequest(nil))
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error for missing device arg")
	}
}

func TestHandleScanNetworkRecordsToInventory(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	inv := withStore(t)

	res, err := handleScanNetwork(context.Background(), callRequest(map[string]any{
		"subnet":          "127.0.0.0/30",
		"timeout_seconds": float64(3),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, isErr := resultText(t, res); isErr {
		t.Fatal("scan should succeed on private CIDR")
	}
	// Whether or not a host answered, the store must be consistent: every
	// device list_devices returns must be retrievable by get.
	for _, d := range inv.list() {
		if _, ok := inv.get(d.IP); !ok {
			t.Errorf("device %s listed but not gettable", d.IP)
		}
	}
}
