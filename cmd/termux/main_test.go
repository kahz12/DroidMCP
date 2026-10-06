package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func writeFile(path, body string, mode os.FileMode) error {
	return os.WriteFile(path, []byte(body), mode)
}

func pathEnv() string { return os.Getenv("PATH") }

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestHandleRunCommandJSON(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo hi"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if strings.TrimSpace(got.Stdout) != "hi" {
		t.Errorf("stdout: %q", got.Stdout)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit_code: %d", got.ExitCode)
	}
}

func TestHandleRunCommandNonZeroIsErrorResult(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "exit 5"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected error result, got success: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.ExitCode != 5 {
		t.Errorf("exit_code: %d", got.ExitCode)
	}
}

func TestHandleRunCommandMissingCommand(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error for missing command")
	}
}

func TestHandleRunCommandEnvExtraAndCwd(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	dir := t.TempDir()
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo $X"},
		"env_extra": map[string]any{
			"X": "from-env",
		},
		"cwd": dir,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if strings.TrimSpace(got.Stdout) != "from-env" {
		t.Errorf("env_extra didn't propagate: %q", got.Stdout)
	}
}

func TestHandleRunCommandTimeoutCap(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command":         "/bin/sh",
		"args":            []any{"-c", "sleep 30"},
		"timeout_seconds": float64(1),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !got.TimedOut {
		t.Errorf("expected timed_out, got %+v", got)
	}
}

func TestHandleRunCommandAllowlistBlocks(t *testing.T) {
	t.Setenv(allowlistEnv, "ls,echo")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo nope"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected allowlist block to surface as error result")
	}
	if !strings.Contains(text, allowlistEnv) {
		t.Errorf("expected %s in error, got %q", allowlistEnv, text)
	}
}

func TestHandleInstallPkgUsesDoubleDash(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	// The real `pkg` is not available here, so a shim earlier on PATH stands
	// in for it and echoes the args it receives.
	dir := t.TempDir()
	fake := dir + "/pkg"
	if err := writeFile(fake, "#!/bin/sh\necho \"$@\"\n", 0o755); err != nil {
		t.Fatalf("shim: %v", err)
	}
	t.Setenv("PATH", dir+":"+pathEnv())
	res, err := handleInstallPkg(context.Background(), callRequest(map[string]any{
		"package": "-evil-flag",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if !strings.Contains(got.Stdout, "install -y -- -evil-flag") {
		t.Errorf("expected `--` separator before package, got %q", got.Stdout)
	}
}

func TestHandleListPkgsTrusted(t *testing.T) {
	t.Setenv(allowlistEnv, "ls") // "pkg" is not allowlisted
	dir := t.TempDir()
	fake := dir + "/pkg"
	if err := writeFile(fake, "#!/bin/sh\necho list-installed-output\n", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+pathEnv())
	res, err := handleListPkgs(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("trusted handler should bypass allowlist, got error: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !strings.Contains(got.Stdout, "list-installed-output") {
		t.Errorf("unexpected stdout: %q", got.Stdout)
	}
}

func TestHandleReadEnvSingle(t *testing.T) {
	t.Setenv("DROIDMCP_TEST_RE", "yes")
	res, err := handleReadEnv(context.Background(), callRequest(map[string]any{
		"name": "DROIDMCP_TEST_RE",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got envResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Name != "DROIDMCP_TEST_RE" || got.Value != "yes" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestHandleReadEnvAll(t *testing.T) {
	t.Setenv("DROIDMCP_TEST_RE_DUMP", "found")
	res, err := handleReadEnv(context.Background(), callRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got envResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.Vars["DROIDMCP_TEST_RE_DUMP"] != "found" {
		t.Errorf("expected env dump to include the test var, got %d entries", len(got.Vars))
	}
}

func TestStringMapArgDropsNonStrings(t *testing.T) {
	got := stringMapArg(callRequest(map[string]any{
		"env_extra": map[string]any{"A": "1", "B": 2, "C": "3"},
	}), "env_extra")
	if got["A"] != "1" || got["C"] != "3" {
		t.Fatalf("unexpected: %+v", got)
	}
	if _, ok := got["B"]; ok {
		t.Errorf("non-string value should be dropped")
	}
}

func TestTimeoutFromReqClamps(t *testing.T) {
	d := timeoutFromReq(callRequest(map[string]any{
		"timeout_seconds": float64(99999),
	}))
	if d != maxExecTimeout {
		t.Errorf("expected clamp to %v, got %v", maxExecTimeout, d)
	}
	d = timeoutFromReq(callRequest(map[string]any{}))
	if d != 0 {
		t.Errorf("expected 0 (default), got %v", d)
	}
}

// termuxShims puts stand-ins for the named Termux:API commands first on PATH.
// Each prints one "arg:" line per argument and then its stdin as a "stdin:"
// line, so a test sees exactly what the handler passed and through which
// channel. exitCode lets a test simulate a failing command.
func termuxShims(t *testing.T, exitCode string, names ...string) {
	t.Helper()
	requireSh(t)
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\n" +
		"printf 'stdin:%s\\n' \"$(cat)\"\n" +
		"exit " + exitCode + "\n"
	for _, name := range names {
		if err := writeFile(filepath.Join(dir, name), script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+pathEnv())
}

type termuxHandler = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

func TestTermuxAPIHandlersArgv(t *testing.T) {
	// The Termux:API handlers are trusted: they must run even when the
	// run_command allowlist does not list their binaries.
	t.Setenv(allowlistEnv, "ls")
	termuxShims(t, "0", "termux-battery-status", "termux-location", "termux-notification",
		"termux-toast", "termux-sms-send", "termux-tts-speak")

	cases := []struct {
		name    string
		handler termuxHandler
		args    map[string]any
		want    []string
	}{
		{"battery", handleBatteryStatus, nil, []string{"stdin:"}},
		{"location defaults", handleLocation, nil, []string{"stdin:"}},
		{"location", handleLocation, map[string]any{"provider": "gps", "request": "last"},
			[]string{"arg:-p", "arg:gps", "arg:-r", "arg:last", "stdin:"}},
		{"notification without fields", handleNotification, nil, []string{"stdin:"}},
		{"notification", handleNotification, map[string]any{"title": "T", "content": "C", "id": "7"},
			[]string{"arg:--title", "arg:T", "arg:--content", "arg:C", "arg:--id", "arg:7", "stdin:"}},
		// Toast and TTS text travels on stdin, so text that looks like a flag
		// stays text.
		{"toast", handleToast, map[string]any{"text": "-s hello"}, []string{"stdin:-s hello"}},
		{"tts defaults", handleTTSSpeak, map[string]any{"text": "hola"}, []string{"stdin:hola"}},
		{"tts", handleTTSSpeak, map[string]any{"text": "-l xx", "language": "es-ES", "rate": 1.5, "pitch": 1.0},
			[]string{"arg:-l", "arg:es-ES", "arg:-r", "arg:1.5", "arg:-p", "arg:1", "stdin:-l xx"}},
		// The message body follows `--`, so a body that looks like a flag is
		// still the body.
		{"sms", handleSMSSend, map[string]any{"number": "+34600000000", "text": "--help me"},
			[]string{"arg:-n", "arg:+34600000000", "arg:--", "arg:--help me", "stdin:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.handler(context.Background(), callRequest(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			text, isErr := resultText(t, res)
			if isErr {
				t.Fatalf("unexpected error result: %s", text)
			}
			var got execResult
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, text)
			}
			lines := strings.Split(strings.TrimSuffix(got.Stdout, "\n"), "\n")
			if strings.Join(lines, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("command saw:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestTermuxAPIHandlersRequireArgs(t *testing.T) {
	termuxShims(t, "0", "termux-toast", "termux-sms-send", "termux-tts-speak")
	for name, tc := range map[string]struct {
		handler termuxHandler
		args    map[string]any
	}{
		"toast without text": {handleToast, nil},
		"sms without number": {handleSMSSend, map[string]any{"text": "hi"}},
		"sms without text":   {handleSMSSend, map[string]any{"number": "123"}},
		"tts without text":   {handleTTSSpeak, map[string]any{"language": "es-ES"}},
	} {
		res, err := tc.handler(context.Background(), callRequest(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if text, isErr := resultText(t, res); !isErr || strings.Contains(text, "stdin:") {
			t.Errorf("%s: got %q (error=%v), want an argument error before running the command", name, text, isErr)
		}
	}
}

// A Termux:API command that fails (e.g. a missing permission) must surface as
// an error result that keeps the exit code.
func TestTermuxAPIHandlerFailureIsErrorResult(t *testing.T) {
	termuxShims(t, "3", "termux-sms-send")
	res, err := handleSMSSend(context.Background(), callRequest(map[string]any{"number": "123", "text": "hi"}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected an error result, got: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil || got.ExitCode != 3 {
		t.Fatalf("exit code not reported: %+v %v", got, err)
	}
}
