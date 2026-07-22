package main

import (
	"context"
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

// capture points a fake binary's argv and stdin at files the test can read
// back. The returned funcs read the recorded argv (one element per line) and
// the recorded stdin.
func capture(t *testing.T, dir string) (argv func() string, stdin func() string) {
	t.Helper()
	argsFile := filepath.Join(dir, "argv")
	stdinFile := filepath.Join(dir, "stdin")
	t.Setenv("SMS_ARGS_FILE", argsFile)
	t.Setenv("SMS_STDIN_FILE", stdinFile)
	read := func(p string) func() string {
		return func() string {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("recording not found (%s): %v", p, err)
			}
			return string(b)
		}
	}
	return read(argsFile), read(stdinFile)
}

// recordArgv records argv and stdin, then succeeds. termux-sms-send prints
// nothing on success, so no stdout is emitted.
const recordArgv = `printf '%s\n' "$@" > "$SMS_ARGS_FILE"; cat > "$SMS_STDIN_FILE"`

func TestEnsureBinariesMissingMessage(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	err := ensureBinaries(binSMSSend)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !strings.Contains(err.Error(), "termux-api") || !strings.Contains(err.Error(), binSMSSend) {
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

func TestRunCmdStdin(t *testing.T) {
	requireSh(t)
	res, err := runCmd(context.Background(), "/bin/sh", []string{"-c", "cat"}, "hello from stdin", defaultExecTimeout)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Stdout != "hello from stdin" {
		t.Errorf("stdin should be piped to the child: got %q", res.Stdout)
	}
}

func TestRunCmdTimeout(t *testing.T) {
	requireSh(t)
	start := time.Now()
	res, err := runCmd(context.Background(), "/bin/sh", []string{"-c", "sleep 30"}, "", 1*time.Second)
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
