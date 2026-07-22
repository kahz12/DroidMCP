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

func must(res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	if err != nil {
		panic(err)
	}
	return res
}

// fakeBin writes an executable shell script named `name` into dir.
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

// captureArgs points a fake binary's argv at a file the test can read back.
// The returned func reads the recorded argv (one element per line).
func captureArgs(t *testing.T, dir string) func() string {
	t.Helper()
	argsFile := filepath.Join(dir, "argv")
	t.Setenv("NOTIF_ARGS_FILE", argsFile)
	return func() string {
		b, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("argv not recorded: %v", err)
		}
		return string(b)
	}
}

const recordArgv = `printf '%s\n' "$@" > "$NOTIF_ARGS_FILE"`

func TestEnsureBinariesMissingMessage(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	err := ensureBinaries(binNotify)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !strings.Contains(err.Error(), "termux-api") || !strings.Contains(err.Error(), binNotify) {
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
	if d := reqTimeout(callRequest(map[string]any{"timeout_seconds": -3.0}), defaultExecTimeout); d != defaultExecTimeout {
		t.Errorf("negative falls back to default: got %v", d)
	}
}

func TestParseSettingsInt(t *testing.T) {
	if v, err := parseSettingsInt("1\n"); err != nil || v != 1 {
		t.Errorf("got %d, %v", v, err)
	}
	for _, bad := range []string{"", "null", "abc"} {
		if _, err := parseSettingsInt(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestJSONPassthrough(t *testing.T) {
	got, isErr := resultText(t, must(jsonPassthrough(`[{"id":1}]`+"\n")))
	if isErr || got != `[{"id":1}]` {
		t.Errorf("valid JSON should pass through trimmed, got %q err=%v", got, isErr)
	}
	got, isErr = resultText(t, must(jsonPassthrough("not json")))
	if isErr || got != `{"raw":"not json"}` {
		t.Errorf("non-JSON should be wrapped, got %q", got)
	}
}

func TestSendNotificationArgsWiring(t *testing.T) {
	dir := fakeBinDir(t)
	readArgs := captureArgs(t, dir)
	fakeBin(t, dir, binNotify, recordArgv)

	got, isErr := resultText(t, mustCall(t, handleSendNotification, map[string]any{
		"content": "hello world", "title": "hi", "id": "42", "priority": "high",
	}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out sendResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Sent || out.ID != "42" {
		t.Errorf("got %+v, want sent=true id=42", out)
	}
	argv := readArgs()
	for _, want := range []string{"--content", "hello world", "--title", "hi", "--id", "42", "--priority", "high"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv missing %q:\n%s", want, argv)
		}
	}
}

func TestSendNotificationContentRequired(t *testing.T) {
	// No content: rejected before any exec, so no fake binary is needed.
	if _, isErr := resultText(t, mustCall(t, handleSendNotification, nil)); !isErr {
		t.Error("missing content should error")
	}
	if _, isErr := resultText(t, mustCall(t, handleSendNotification, map[string]any{"content": "  "})); !isErr {
		t.Error("blank content should error")
	}
}

func TestSendNotificationInvalidPriority(t *testing.T) {
	// Priority is validated before ensureBinaries, so no fake binary is needed.
	got, isErr := resultText(t, mustCall(t, handleSendNotification, map[string]any{
		"content": "x", "priority": "urgent",
	}))
	if !isErr {
		t.Fatalf("bad priority should error, got %s", got)
	}
	if !strings.Contains(got, "priority") {
		t.Errorf("error should name the offending field: %s", got)
	}
}

func TestSendNotificationNonZeroExit(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binNotify, "echo 'permission denied' 1>&2\nexit 1")

	got, isErr := resultText(t, mustCall(t, handleSendNotification, map[string]any{"content": "x"}))
	if !isErr {
		t.Fatalf("expected error result, got %s", got)
	}
	if !strings.Contains(got, "permission denied") || !strings.Contains(got, `"exit_code":1`) {
		t.Errorf("error should carry stderr and exit code: %s", got)
	}
}

func TestListNotificationsPassthrough(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binNotifyList, `echo '[{"id":7,"title":"build done"}]'`)

	got, isErr := resultText(t, mustCall(t, handleListNotifications, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil || len(out) != 1 {
		t.Fatalf("expected one-element JSON array, got %s (err %v)", got, err)
	}
}

func TestDismissNotification(t *testing.T) {
	dir := fakeBinDir(t)
	readArgs := captureArgs(t, dir)
	fakeBin(t, dir, binNotifyRemove, recordArgv)

	got, isErr := resultText(t, mustCall(t, handleDismissNotification, map[string]any{"id": "42"}))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out dismissResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.Dismissed || out.ID != "42" {
		t.Errorf("got %+v, want dismissed=true id=42", out)
	}
	if argv := readArgs(); !strings.Contains(argv, "42") {
		t.Errorf("id not passed to command: %s", argv)
	}
}

func TestDismissNotificationIDRequired(t *testing.T) {
	// Empty id is rejected before any exec.
	if _, isErr := resultText(t, mustCall(t, handleDismissNotification, nil)); !isErr {
		t.Error("missing id should error")
	}
	if _, isErr := resultText(t, mustCall(t, handleDismissNotification, map[string]any{"id": "  "})); !isErr {
		t.Error("blank id should error")
	}
}

func TestGetDNDStatus(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSettings, "echo 1")

	got, isErr := resultText(t, mustCall(t, handleGetDNDStatus, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out dndResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if !out.DNDEnabled || out.Mode != "priority_only" || out.ZenMode != 1 {
		t.Errorf("got %+v, want enabled/priority_only/1", out)
	}
}

func TestGetDNDStatusOff(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSettings, "echo 0")

	got, isErr := resultText(t, mustCall(t, handleGetDNDStatus, nil))
	if isErr {
		t.Fatalf("unexpected error: %s", got)
	}
	var out dndResult
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if out.DNDEnabled || out.Mode != "off" {
		t.Errorf("got %+v, want disabled/off", out)
	}
}

func TestGetDNDStatusUnavailable(t *testing.T) {
	dir := fakeBinDir(t)
	fakeBin(t, dir, binSettings, "echo null")

	got, isErr := resultText(t, mustCall(t, handleGetDNDStatus, nil))
	if !isErr {
		t.Fatalf("expected error result, got %s", got)
	}
	if !strings.Contains(got, "Do Not Disturb") {
		t.Errorf("error should explain the limitation: %s", got)
	}
}

func TestRunCmdTimeout(t *testing.T) {
	requireSh(t)
	start := time.Now()
	res, err := runCmd(context.Background(), "/bin/sh", []string{"-c", "sleep 30"}, 1*time.Second)
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
