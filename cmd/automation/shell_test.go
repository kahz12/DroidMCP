package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireCommands(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not available: %v", name, err)
		}
	}
}

func mustAllowlist(t *testing.T, raw string) allowlist {
	t.Helper()
	a, err := parseAllowlist(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func run(t *testing.T, allow, script string, opts ...func(*scriptOptions)) scriptResult {
	t.Helper()
	o := scriptOptions{Script: script, Timeout: 10 * time.Second, MaxBytes: 64 << 10, Cwd: t.TempDir()}
	for _, f := range opts {
		f(&o)
	}
	return runScript(context.Background(), mustAllowlist(t, allow), o)
}

func TestParseAllowlist(t *testing.T) {
	a := mustAllowlist(t, " cat, sleep ,/usr/bin/env,, ")
	if !a.names["cat"] || !a.names["sleep"] || !a.paths["/usr/bin/env"] || len(a.names) != 2 || len(a.paths) != 1 {
		t.Fatalf("parsed %+v", a)
	}
	for _, raw := range []string{"", " , "} {
		if a := mustAllowlist(t, raw); !a.empty() {
			t.Errorf("%q should be empty", raw)
		}
	}
	if _, err := parseAllowlist("cat,bin/tool"); err == nil {
		t.Error("accepted a relative path")
	}
}

func TestScriptRunsAllowlistedCommands(t *testing.T) {
	requireCommands(t, "cat")
	res := run(t, "cat", `printf 'hola\n' | cat; x=$(printf mundo | cat); echo "$x"`)
	if res.ExitCode != 0 || res.Error != "" || res.Stdout != "hola\nmundo\n" {
		t.Fatalf("got %+v", res)
	}
}

// Every way a script can start a command goes through the allowlist, and a
// refused command stops the script.
func TestScriptRefusesCommandsOutsideAllowlist(t *testing.T) {
	requireCommands(t, "cat", "ls")
	for _, script := range []string{
		"ls",
		"echo before; ls; echo after",
		"x=$(ls)",
		"echo `ls`",
		"printf a | ls",
		"eval ls",
		"command ls",
		"exec ls",
		"c=ls; $c",
		"f() { ls; }; f",
		"cat <(ls)",
		"ls &",
		"/bin/cat </dev/null", // an absolute path must be listed as such
	} {
		res := run(t, "cat", script)
		if res.ExitCode != 126 || !strings.Contains(res.Error, "not in "+allowlistEnv) {
			t.Errorf("%q: got exit %d, error %q", script, res.ExitCode, res.Error)
		}
		if strings.Contains(res.Stdout, "after") {
			t.Errorf("%q: the script kept going after a refused command", script)
		}
	}
}

// An allowlisted name runs the binary from the server's PATH, whatever PATH
// the script sets.
func TestScriptResolvesNamesWithServerPath(t *testing.T) {
	requireCommands(t, "cat")
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	evil := t.TempDir()
	marker := filepath.Join(evil, "ran")
	if err := os.WriteFile(filepath.Join(evil, "cat"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{
		"PATH=" + evil + ":$PATH; cat </dev/null",
		"export PATH=" + evil + "; cat </dev/null",
		"PATH=" + evil + " cat </dev/null",
	} {
		if res := run(t, "cat", script); res.ExitCode != 0 {
			t.Errorf("%q: got %+v", script, res)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a script redirected an allowlisted name to its own binary")
	}
}

func TestScriptAbsolutePathMustMatchExactly(t *testing.T) {
	realCat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat not available")
	}
	realCat, _ = filepath.Abs(realCat)
	if res := run(t, realCat, realCat+" </dev/null"); res.ExitCode != 0 {
		t.Errorf("listed absolute path refused: %+v", res)
	}
	if res := run(t, realCat, "cat </dev/null"); res.ExitCode != 126 {
		t.Errorf("a bare name passed with only the absolute path listed: %+v", res)
	}
}

func TestScriptCannotChangeLinkerVariables(t *testing.T) {
	requireCommands(t, "env")
	t.Setenv("LD_DROIDMCP_TEST", "orig")

	res := run(t, "env", "env")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "LD_DROIDMCP_TEST=orig") {
		t.Fatalf("inherited linker variable not passed through unchanged: %+v", res)
	}
	for _, script := range []string{
		"LD_DROIDMCP_TEST=evil env",
		"export LD_DROIDMCP_TEST=evil; env",
		"export LD_DROIDMCP_NEW=/tmp/x.so; env",
		"export dyld_insert_libraries=/tmp/x.dylib; env",
	} {
		if res := run(t, "env", script); res.ExitCode != 126 || !strings.Contains(res.Error, "cannot set") {
			t.Errorf("%q: got exit %d, error %q", script, res.ExitCode, res.Error)
		}
	}
	if res := run(t, "env", "unset LD_DROIDMCP_TEST; env"); res.ExitCode != 0 || strings.Contains(res.Stdout, "LD_DROIDMCP_TEST") {
		t.Errorf("unsetting a linker variable should be allowed: %+v", res)
	}
}

func TestScriptCannotWriteFilesThroughRedirections(t *testing.T) {
	requireCommands(t, "cat")
	dir := t.TempDir()
	cwd := func(o *scriptOptions) { o.Cwd = dir }
	for _, script := range []string{
		"echo x > out",
		"echo x >> out",
		"echo x > " + filepath.Join(dir, "out"),
		"echo x | cat > out",
	} {
		res := run(t, "cat", script, cwd)
		if res.Error == "" || !strings.Contains(res.Error, "cannot redirect") {
			t.Errorf("%q: got %+v", script, res)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
		t.Fatal("a redirection created a file")
	}

	if res := run(t, "cat", "echo x > /dev/null; echo ok", cwd); res.ExitCode != 0 || res.Stdout != "ok\n" {
		t.Errorf("writing to /dev/null should work: %+v", res)
	}
	if err := os.WriteFile(filepath.Join(dir, "in"), []byte("datos"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := run(t, "cat", "cat < in", cwd); res.ExitCode != 0 || res.Stdout != "datos" {
		t.Errorf("reading through a redirection should work: %+v", res)
	}
}

func TestScriptTimeoutAndCancel(t *testing.T) {
	requireCommands(t, "sleep")
	short := func(o *scriptOptions) { o.Timeout = 200 * time.Millisecond }
	for _, script := range []string{"while :; do :; done", "sleep 30"} {
		start := time.Now()
		res := run(t, "sleep", script, short)
		if !res.TimedOut || res.ExitCode != -1 {
			t.Errorf("%q: got %+v, want a timeout", script, res)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%q: took %v to stop", script, elapsed)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	res := runScript(ctx, mustAllowlist(t, "sleep"), scriptOptions{Script: "sleep 30", Timeout: time.Minute, MaxBytes: 1024})
	if !res.Cancelled || res.TimedOut {
		t.Fatalf("got %+v, want a cancelled run", res)
	}
}

func TestScriptExitCodeOutputCapAndSyntax(t *testing.T) {
	if res := run(t, "cat", "echo err >&2; exit 3"); res.ExitCode != 3 || res.Stderr != "err\n" || res.Error != "" {
		t.Errorf("exit 3: got %+v", res)
	}
	small := func(o *scriptOptions) { o.MaxBytes = 1000 }
	if res := run(t, "cat", "printf '%05000d' 0", small); !res.Truncated || len(res.Stdout) != 1000 {
		t.Errorf("output cap: truncated=%v len=%d", res.Truncated, len(res.Stdout))
	}
	if res := run(t, "cat", "if then fi"); res.ExitCode != 2 || !strings.Contains(res.Error, "syntax") {
		t.Errorf("syntax error: got %+v", res)
	}
}

func TestCheckScript(t *testing.T) {
	requireCommands(t, "cat")
	allow := mustAllowlist(t, "cat")
	for _, script := range []string{
		"printf a | cat",
		"[ -f x ] && cat x",
		"f() { cat; }; f",
		"$CMD", // only known at run time; checked then
		"cd /tmp && echo $(pwd)",
		"cat x > /dev/null 2>&1",
		"echo oops >&2",
		"cat < input",
		`echo x > "$OUT"`, // target only known at run time; refused then
	} {
		file, err := parseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkScript(allow, file); err != nil {
			t.Errorf("%q rejected: %v", script, err)
		}
	}
	for _, script := range []string{
		"ls",
		"echo $(ls)",
		"cat x | grep y",
		"if true; then rm -rf x; fi",
		"f() { ls; }",
		"echo x > out",
		"cat in >> /tmp/log",
		"echo x &> out",
	} {
		file, err := parseScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkScript(allow, file); err == nil {
			t.Errorf("%q accepted", script)
		}
	}
}
