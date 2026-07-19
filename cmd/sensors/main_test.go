package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
}

// callRequest builds an mcp.CallToolRequest with the given arguments map.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
}

// resultText concatenates all text-content blocks and reports the IsError flag.
func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// mustCall invokes a handler and returns its result, failing on a Go-level error.
func mustCall(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	return res
}

// fakeBin writes an executable shell script named `name` into dir and prepends
// dir to PATH (once per test via t.Setenv in fakeBinDir).
func fakeBin(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeBinDir creates a temp dir, puts it first on PATH, and returns it. Any
// fake binary written there shadows the real command for the test's duration.
func fakeBinDir(t *testing.T) string {
	t.Helper()
	requireSh(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestEnsureBinariesMissingMessage(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	err := ensureBinaries(binBattery)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !strings.Contains(err.Error(), "termux-api") || !strings.Contains(err.Error(), binBattery) {
		t.Errorf("error should hint at termux-api and name the binary, got %q", err.Error())
	}
}

func TestReqTimeout(t *testing.T) {
	if d := reqTimeout(callRequest(nil), defaultExecTimeout); d != defaultExecTimeout {
		t.Errorf("default: got %v", d)
	}
	if d := reqTimeout(callRequest(map[string]any{"timeout_seconds": 5.0}), defaultExecTimeout); d != 5*time.Second {
		t.Errorf("explicit: got %v", d)
	}
	if d := reqTimeout(callRequest(map[string]any{"timeout_seconds": 9999.0}), defaultExecTimeout); d != maxExecTimeout {
		t.Errorf("clamp: got %v", d)
	}
	if d := reqTimeout(callRequest(map[string]any{"timeout_seconds": -3.0}), locationExecTimeout); d != locationExecTimeout {
		t.Errorf("negative falls back to default: got %v", d)
	}
}

func TestParseSettingsInt(t *testing.T) {
	if v, err := parseSettingsInt("128\n"); err != nil || v != 128 {
		t.Errorf("got %d, %v", v, err)
	}
	for _, bad := range []string{"", "null", "abc"} {
		if _, err := parseSettingsInt(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestJSONPassthrough(t *testing.T) {
	got, isErr := resultText(t, must(jsonPassthrough(`{"a":1}`+"\n")))
	if isErr || got != `{"a":1}` {
		t.Errorf("valid JSON should pass through trimmed, got %q err=%v", got, isErr)
	}
	got, isErr = resultText(t, must(jsonPassthrough("not json")))
	if isErr || got != `{"raw":"not json"}` {
		t.Errorf("non-JSON should be wrapped, got %q", got)
	}
}

func must(res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	if err != nil {
		panic(err)
	}
	return res
}

func TestGetBattery(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binBattery, `echo '{"health":"GOOD","percentage":85,"status":"CHARGING"}'`)

	got, isErr := resultText(t, mustCall(t, handleGetBattery, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out["percentage"] != 85.0 {
		t.Errorf("percentage=%v, want 85", out["percentage"])
	}
}

func TestGetBatteryNonZeroExit(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binBattery, "echo 'api unreachable' 1>&2\nexit 3")

	got, isErr := resultText(t, mustCall(t, handleGetBattery, nil))
	if !isErr {
		t.Fatalf("expected error result, got %s", got)
	}
	if !strings.Contains(got, "api unreachable") || !strings.Contains(got, `"exit_code":3`) {
		t.Errorf("error should carry stderr and exit code: %s", got)
	}
}

func TestGetLocationArgsAndValidation(t *testing.T) {
	dir := fakeBinDir(t)
	// Echo the arguments back so the test can assert flag wiring.
	fakeBin(t, dir, binLocation, `echo "{\"args\":\"$*\"}"`)

	got, isErr := resultText(t, mustCall(t, handleGetLocation, map[string]any{
		"provider": "gps", "request": "last",
	}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	if !strings.Contains(got, "-p gps -r last") {
		t.Errorf("flags not wired: %s", got)
	}

	// Defaults.
	got, _ = resultText(t, mustCall(t, handleGetLocation, nil))
	if !strings.Contains(got, "-p network -r once") {
		t.Errorf("defaults not applied: %s", got)
	}

	// Invalid values are rejected before any exec.
	if _, isErr := resultText(t, mustCall(t, handleGetLocation, map[string]any{"provider": "wifi"})); !isErr {
		t.Errorf("bad provider should error")
	}
	if _, isErr := resultText(t, mustCall(t, handleGetLocation, map[string]any{"request": "updates"})); !isErr {
		t.Errorf("streaming request kind should be rejected")
	}
}

func TestGetVolumePassthrough(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binVolume, `echo '[{"stream":"music","volume":5,"max_volume":15}]'`)

	got, isErr := resultText(t, mustCall(t, handleGetVolume, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var streams []map[string]any
	if err := json.Unmarshal([]byte(got), &streams); err != nil || len(streams) != 1 {
		t.Fatalf("expected one-stream JSON array, got %s (err %v)", got, err)
	}
}

func TestGetBrightness(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSettings, `case "$3" in
screen_brightness) echo 142 ;;
screen_brightness_mode) echo 1 ;;
esac`)

	got, isErr := resultText(t, mustCall(t, handleGetBrightness, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out brightnessResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.Brightness != 142 || !out.Auto {
		t.Errorf("got %+v, want brightness=142 auto=true", out)
	}
}

func TestGetBrightnessUnavailable(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSettings, "echo null")

	got, isErr := resultText(t, mustCall(t, handleGetBrightness, nil))
	if !isErr {
		t.Fatalf("expected error result, got %s", got)
	}
	if !strings.Contains(got, "cannot read screen brightness") {
		t.Errorf("error should explain the limitation: %s", got)
	}
}

func TestListSensors(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binBattery, "echo '{}'")
	fakeBin(t, dir, binSensor, `echo '{"sensors":["accelerometer","gyroscope"]}'`)
	// Force lookPath to only see the fakes we created.
	prev := lookPath
	lookPath = func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return "", errors.New("not found")
		}
		return p, nil
	}
	t.Cleanup(func() { lookPath = prev })

	got, isErr := resultText(t, mustCall(t, handleListSensors, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out struct {
		Tools    map[string]backendStatus `json:"tools"`
		Hardware map[string][]string      `json:"hardware"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Tools["get_battery"].Available {
		t.Errorf("get_battery should be available: %+v", out.Tools)
	}
	if out.Tools["get_location"].Available {
		t.Errorf("get_location should be unavailable: %+v", out.Tools)
	}
	if len(out.Hardware["sensors"]) != 2 {
		t.Errorf("hardware inventory missing: %s", got)
	}
}

func TestListSensorsNoTermuxSensor(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	got, isErr := resultText(t, mustCall(t, handleListSensors, nil))
	if isErr {
		t.Fatalf("availability map should still succeed: %s", got)
	}
	if !strings.Contains(got, "hardware_error") || !strings.Contains(got, "termux-api") {
		t.Errorf("expected hardware_error hint: %s", got)
	}
}

func TestRunSensorCmdTimeout(t *testing.T) {
	requireSh(t)
	start := time.Now()
	res, err := runSensorCmd(context.Background(), "/bin/sh", []string{"-c", "sleep 30"}, 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("expected TimedOut, got %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout not enforced: %s", elapsed)
	}
}

func TestSafeUTF8AndCappedBuffer(t *testing.T) {
	if s := safeUTF8([]byte{0xff, 'a'}); !strings.Contains(s, "a") || !strings.Contains(s, "�") {
		t.Errorf("invalid bytes should be replaced: %q", s)
	}
	cb := &cappedBuffer{max: 4}
	if _, err := cb.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if string(cb.Bytes()) != "1234" || !cb.truncated {
		t.Errorf("cap not enforced: %q truncated=%v", cb.Bytes(), cb.truncated)
	}
}
