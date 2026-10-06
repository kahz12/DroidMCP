package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportPreservesDestinationOnRowError(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	dest := filepath.Join(root, "out.csv")
	if err := os.WriteFile(dest, []byte("previous export"), 0600); err != nil {
		t.Fatal(err)
	}
	failingSQL := "SELECT 1 AS n UNION ALL SELECT abs(-9223372036854775808)"
	_, failed := resultText(t, mustCall(t, handleExportCSV, map[string]any{
		"db": "app.db", "destination": "out.csv", "sql": failingSQL,
	}))
	if !failed {
		t.Fatal("expected integer overflow during iteration")
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "previous export" {
		t.Fatalf("destination changed: %q %v", data, err)
	}

	// A failed export to a new destination must not leave a partial file under
	// either name.
	if _, failed := resultText(t, mustCall(t, handleExportCSV, map[string]any{
		"db": "app.db", "destination": "fresh.csv", "sql": failingSQL,
	})); !failed {
		t.Fatal("expected integer overflow during iteration")
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.csv")); !os.IsNotExist(err) {
		t.Fatalf("partial export published: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".droidmcp-export-*")); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
}

func TestExportNewDestinationIsPrivate(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	okText(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "users.csv", "sql": "SELECT * FROM users"})
	info, err := os.Stat(filepath.Join(root, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new export mode = %v, want 0600", got)
	}
}

func TestExportRejectsDirectoryDestination(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	if err := os.Mkdir(filepath.Join(root, "exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, failed := resultText(t, mustCall(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "exports", "sql": "SELECT 1"})); !failed {
		t.Fatal("accepted a directory as the destination")
	}
	if info, err := os.Stat(filepath.Join(root, "exports")); err != nil || !info.IsDir() {
		t.Fatalf("directory destination was replaced: %v %v", info, err)
	}
}

func TestExportRejectsDatabaseAliases(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := withRoot(t)
			seed(t, "app.db")
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if err := link(filepath.Join(root, "app.db"), filepath.Join(root, "alias")); err != nil {
				t.Skipf("%s unsupported in this environment: %v", kind, err)
			}
			_, failed := resultText(t, mustCall(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "alias", "sql": "SELECT 1"}))
			if !failed {
				t.Fatal("accepted database alias")
			}
			okText(t, handleQuery, map[string]any{"db": "app.db", "sql": "SELECT * FROM users"})
		})
	}
}

func TestExportKeepsDestinationModeAndSymlink(t *testing.T) {
	root := withRoot(t)
	seed(t, "app.db")
	real := filepath.Join(root, "exports", "2026.csv")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("exports", "2026.csv"), filepath.Join(root, "latest.csv")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	okText(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "latest.csv", "sql": "SELECT * FROM users"})

	if info, err := os.Lstat(filepath.Join(root, "latest.csv")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("latest.csv is no longer a symlink: %v %v", info, err)
	}
	data, err := os.ReadFile(real)
	if err != nil || string(data) == "old" {
		t.Fatalf("symlink target was not updated: %q %v", data, err)
	}
	if info, err := os.Stat(real); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, %v; want 0644 kept", info.Mode().Perm(), err)
	}
}

func TestExportRefusesReadOnlyDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	root := withRoot(t)
	seed(t, "app.db")
	dest := filepath.Join(root, "report.csv")
	if err := os.WriteFile(dest, []byte("old"), 0o444); err != nil {
		t.Fatal(err)
	}
	_, failed := resultText(t, mustCall(t, handleExportCSV, map[string]any{"db": "app.db", "destination": "report.csv", "sql": "SELECT * FROM users"}))
	if !failed {
		t.Fatal("replaced a read-only destination")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "old" {
		t.Fatalf("destination changed: %q %v", data, err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".droidmcp-export-*")); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
}

func TestOpenDBRejectsDanglingSymlinkOutsideRoot(t *testing.T) {
	root := withRoot(t)
	outside := filepath.Join(t.TempDir(), "evil.db")
	if err := os.Symlink(outside, filepath.Join(root, "evil.db")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	_, failed := resultText(t, mustCall(t, handleOpenDB, map[string]any{"db": "evil.db"}))
	if !failed {
		t.Fatal("open_db followed a dangling symlink out of root")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("database created outside root")
	}
}

// main must refuse an explicit DROIDMCP_ROOT=/. That check lives in main, which
// exits, so the test re-runs its own binary as a child process that calls main.
func TestMainRefusesFilesystemRoot(t *testing.T) {
	if os.Getenv("DROIDMCP_TEST_RUN_MAIN") == "1" {
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainRefusesFilesystemRoot$")
	// No API key in the environment: without the root check, main would exit
	// for that reason instead (rejected below) rather than start serving.
	cmd.Env = []string{"DROIDMCP_TEST_RUN_MAIN=1", "DROIDMCP_ROOT=/"}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("main with DROIDMCP_ROOT=/: %v, want exit status 1\n%s", err, out)
	}
	if !strings.Contains(string(out), "is the filesystem root") {
		t.Fatalf("main exited for another reason:\n%s", out)
	}
}
