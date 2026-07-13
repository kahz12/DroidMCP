package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleGetStorageExplicitPath(t *testing.T) {
	dir := t.TempDir()
	res, err := handleGetStorage(context.Background(), callRequest(map[string]any{
		"path": dir,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got storageResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if len(got.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(got.Mounts))
	}
	m := got.Mounts[0]
	if m.Path != dir {
		t.Errorf("path echo: got %q", m.Path)
	}
	if m.TotalBytes == 0 {
		t.Error("total_bytes should be > 0")
	}
	if m.AvailableBytes > m.TotalBytes {
		t.Errorf("available (%d) > total (%d)", m.AvailableBytes, m.TotalBytes)
	}
	if m.UsedPercent < 0 || m.UsedPercent > 100 {
		t.Errorf("used_percent out of range: %f", m.UsedPercent)
	}
}

func TestHandleGetStorageDefaultsUseHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PREFIX", "")
	res, err := handleGetStorage(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got storageResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	found := false
	for _, m := range got.Mounts {
		if m.Path == home {
			found = true
			if m.Error != "" {
				t.Errorf("unexpected entry error: %s", m.Error)
			}
		}
	}
	if !found {
		t.Fatalf("expected HOME (%s) in mounts, got %+v", home, got.Mounts)
	}
}

func TestHandleGetStorageMissingPath(t *testing.T) {
	res, err := handleGetStorage(context.Background(), callRequest(map[string]any{
		"path": "/nonexistent/droidmcp/path",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr || !strings.Contains(text, "statfs") {
		t.Fatalf("expected statfs error, got isErr=%v %q", isErr, text)
	}
}
