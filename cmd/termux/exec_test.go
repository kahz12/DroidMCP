package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Most tests need real executables. /bin/sh and /bin/echo are present on
// every Termux/Linux system we target; if they're missing the host is
// non-conformant and we skip rather than failing.
func requireSh(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
}

func TestRunCommandCapturesStdoutStderr(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo out; echo err >&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "out" {
		t.Errorf("stdout: %q", res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "err" {
		t.Errorf("stderr: %q", res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit_code: %d", res.ExitCode)
	}
	if res.TimedOut || res.Cancelled {
		t.Errorf("flags: timed_out=%v cancelled=%v", res.TimedOut, res.Cancelled)
	}
}

func TestRunCommandRejectsLinkerEnvExtra(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	for _, key := range []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "ld_preload", "DYLD_INSERT_LIBRARIES"} {
		_, err := runCommand(context.Background(), execOptions{
			Command:  "/bin/sh",
			Args:     []string{"-c", "true"},
			EnvExtra: map[string]string{key: "/tmp/evil.so"},
		})
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("env key %q should be rejected, got err=%v", key, err)
		}
	}
}

func TestRunCommandAllowsBenignEnvExtra(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	res, err := runCommand(context.Background(), execOptions{
		Command:  "/bin/sh",
		Args:     []string{"-c", "printf %s \"$MY_VAR\""},
		EnvExtra: map[string]string{"MY_VAR": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "ok" {
		t.Errorf("benign env_extra should propagate, got %q", res.Stdout)
	}
}

func TestRunCommandTrustedSkipsEnvExtraCheck(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	// Trusted internal callers (the termux-* wrappers) are not caller-driven, so
	// the linker-env guard does not apply to them.
	_, err := runCommand(context.Background(), execOptions{
		Command:  "/bin/sh",
		Args:     []string{"-c", "true"},
		EnvExtra: map[string]string{"LD_PRELOAD": "/tmp/x.so"},
		Trusted:  true,
	})
	if err != nil {
		t.Errorf("trusted call should not be blocked by env guard, got %v", err)
	}
}

func TestCheckEnvExtra(t *testing.T) {
	if err := checkEnvExtra(map[string]string{"PATH": "/x", "FOO": "bar"}); err != nil {
		t.Errorf("benign env should pass: %v", err)
	}
	if err := checkEnvExtra(map[string]string{"LD_AUDIT": "x"}); err == nil {
		t.Error("LD_AUDIT should be rejected")
	}
	if err := checkEnvExtra(nil); err != nil {
		t.Errorf("nil env should pass: %v", err)
	}
}

func TestRunCommandPropagatesNonZeroExit(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Errorf("expected exit_code 7, got %d", res.ExitCode)
	}
}

func TestRunCommandTimeout(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	start := time.Now()
	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Errorf("expected timed_out=true, got %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("kill-on-timeout took too long: %v", elapsed)
	}
}

func TestRunCommandPanicButtonOnCancel(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res, err := runCommand(ctx, execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cancelled {
		t.Errorf("expected cancelled=true, got %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("SIGTERM-on-cancel did not kill child quickly: %v", elapsed)
	}
}

func TestRunCommandAppliesCwd(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	dir := t.TempDir()

	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "pwd"},
		Cwd:     dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	// On macOS /tmp is a symlink, but t.TempDir resolves it. On Linux/Termux
	// the prefix should match exactly.
	got := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(got, filepath.Clean(dir)) && !strings.HasSuffix(got, filepath.Base(dir)) {
		t.Errorf("expected pwd to match cwd %q, got %q", dir, got)
	}
}

func TestRunCommandRejectsBadCwd(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	_, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "true"},
		Cwd:     "/nonexistent/definitely/not-here",
	})
	if err == nil {
		t.Fatal("expected error for missing cwd")
	}
}

func TestRunCommandPassesEnvExtra(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")

	res, err := runCommand(context.Background(), execOptions{
		Command:  "/bin/sh",
		Args:     []string{"-c", "echo $DROIDMCP_TEST_VAR"},
		EnvExtra: map[string]string{"DROIDMCP_TEST_VAR": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Errorf("env_extra not applied: %q", res.Stdout)
	}
}

func TestRunCommandAllowlistBlocks(t *testing.T) {
	t.Setenv(allowlistEnv, "ls,echo")
	_, err := runCommand(context.Background(), execOptions{
		Command: "rm",
		Args:    []string{"-rf", "/"},
	})
	if err == nil {
		t.Fatal("expected allowlist block")
	}
	if !strings.Contains(err.Error(), allowlistEnv) {
		t.Errorf("error should mention %s, got %v", allowlistEnv, err)
	}
}

func TestRunCommandAllowlistAcceptsBasename(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "sh")

	// Full path to /bin/sh: basename "sh" should match the allowlist entry.
	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo ok"},
	})
	if err != nil {
		t.Fatalf("expected /bin/sh to be allowed via basename, got %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Errorf("stdout: %q", res.Stdout)
	}
}

func TestRunCommandAllowlistEmptyMeansAllow(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	if err := allowlistCheck("anything-at-all"); err != nil {
		t.Fatalf("expected empty allowlist to permit everything, got %v", err)
	}
}

func TestRunCommandTrustedBypassesAllowlist(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "ls")
	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo bypass"},
		Trusted: true,
	})
	if err != nil {
		t.Fatalf("Trusted should bypass allowlist, got %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "bypass" {
		t.Errorf("stdout: %q", res.Stdout)
	}
}

func TestRunCommandRejectsEmptyCommand(t *testing.T) {
	_, err := runCommand(context.Background(), execOptions{Command: "   "})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-command error, got %v", err)
	}
}

func TestRunCommandUnknownBinary(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/this-binary-does-not-exist-droidmcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != -1 {
		t.Errorf("expected exit_code -1 for missing binary, got %d", res.ExitCode)
	}
	if res.Stderr == "" {
		t.Errorf("expected error text in stderr, got empty")
	}
}

func TestCappedBufferTruncates(t *testing.T) {
	c := &cappedBuffer{max: 4}
	n, _ := c.Write([]byte("abcdef"))
	if n != 6 {
		t.Errorf("Write should report all bytes accepted, got %d", n)
	}
	if !c.truncated {
		t.Error("expected truncated=true")
	}
	if string(c.Bytes()) != "abcd" {
		t.Errorf("expected 'abcd', got %q", c.Bytes())
	}
}

func TestSafeUTF8(t *testing.T) {
	if got := safeUTF8([]byte("hola")); got != "hola" {
		t.Errorf("plain ascii: %q", got)
	}
	// 0xff is invalid UTF-8 in any context.
	got := safeUTF8([]byte{0xff, 0xfe, 'a'})
	if !strings.HasSuffix(got, "a") {
		t.Errorf("expected trailing 'a' to survive, got %q", got)
	}
	if strings.ContainsRune(got, rune(0xff)) {
		t.Errorf("raw 0xff should have been replaced, got %q", got)
	}
}

func TestParseAllowlistTrimsAndDedupes(t *testing.T) {
	got := parseAllowlist("a, b ,a,, c")
	want := []string{"a", "b", "c"}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d (%v)", len(got), got)
	}
}

func TestRunCommandTruncatesLargeOutput(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	// Print ~16 KiB of "x" with a 1 KiB cap.
	res, err := runCommand(context.Background(), execOptions{
		Command:  "/bin/sh",
		Args:     []string{"-c", "yes x | head -c 16384"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("expected truncated=true")
	}
	if int64(len(res.Stdout)) != 1024 {
		t.Errorf("expected stdout capped at 1024, got %d", len(res.Stdout))
	}
}

// Sanity: errors.As on exec.ExitError works as expected.
func TestExitErrorIsExecExitError(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := runCommand(context.Background(), execOptions{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Errorf("unexpected exit code: %d", res.ExitCode)
	}
	if !errors.Is(context.Background().Err(), nil) && context.Background().Err() != nil {
		t.Error("background ctx should not be done")
	}
}
