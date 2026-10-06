package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCopyRejectsSameFile(t *testing.T) {
	for _, alias := range []string{"same", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			root := withRoot(t)
			src := filepath.Join(root, "src")
			if err := os.WriteFile(src, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			dst := "src"
			if alias == "hardlink" {
				dst = "alias"
				if err := os.Link(src, filepath.Join(root, dst)); err != nil {
					t.Skipf("hard links unsupported in this environment: %v", err)
				}
			}
			_, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src", "destination": dst}))
			if !failed {
				t.Fatal("expected same-file rejection")
			}
			data, err := os.ReadFile(src)
			if err != nil || string(data) != "keep me" {
				t.Fatalf("source was changed: %q, %v", data, err)
			}
		})
	}
}

func TestCopyRejectsDescendantAndDestinationSymlink(t *testing.T) {
	root := withRoot(t)
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "file"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{"src", "src/nested"} {
		_, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src", "destination": dst}))
		if !failed {
			t.Fatalf("accepted recursive destination %s", dst)
		}
	}
	outside := t.TempDir()
	protected := filepath.Join(outside, "protected")
	if err := os.WriteFile(protected, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dst"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(protected, filepath.Join(root, "dst", "file")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	_, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src", "destination": "dst"}))
	if !failed {
		t.Fatal("accepted destination symlink")
	}
	data, err := os.ReadFile(protected)
	if err != nil || string(data) != "old" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
}

func writeTree(t *testing.T, files map[string]string) {
	t.Helper()
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A dangling in-root symlink must not let a write create a file outside root.
func TestWritesRejectDanglingSymlinkOutsideRoot(t *testing.T) {
	root := withRoot(t)
	outside := t.TempDir()
	symlinkOrSkip(t, filepath.Join(outside, "x.sh"), filepath.Join(root, "evil"))
	writeTree(t, map[string]string{filepath.Join(root, "payload.sh"): "boom"})

	for name, call := range map[string]func() (string, bool){
		"copy_file": func() (string, bool) {
			return resultText(t, mustCall(handleCopyFile, map[string]any{"source": "payload.sh", "destination": "evil"}))
		},
		"write_file": func() (string, bool) {
			return resultText(t, mustCall(handleWriteFile, map[string]any{"path": "evil", "content": "boom"}))
		},
	} {
		if _, failed := call(); !failed {
			t.Errorf("%s wrote through a dangling symlink", name)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "x.sh")); err == nil {
		t.Fatal("file was created outside root")
	}
}

func TestCopyDirRejectsDestinationAliasedIntoSource(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{filepath.Join(root, "src", "inner", "f.txt"): "x"})
	symlinkOrSkip(t, filepath.Join(root, "src", "inner"), filepath.Join(root, "alias"))

	_, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src", "destination": "alias/new"}))
	if !failed {
		t.Fatal("accepted a destination that resolves inside the source")
	}
	if _, err := os.Stat(filepath.Join(root, "src", "inner", "new")); err == nil {
		t.Fatal("copy started recursing into itself")
	}
}

func TestCopyDirRejectsAncestorDestination(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{
		filepath.Join(root, "a", "x"):      "ORIGINAL",
		filepath.Join(root, "a", "a", "x"): "NESTED",
	})
	for _, dst := range []string{".", "a"} {
		_, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "a", "destination": dst}))
		if !failed {
			t.Fatalf("accepted destination %q that contains the source", dst)
		}
	}
	for path, want := range map[string]string{
		filepath.Join(root, "a", "x"): "ORIGINAL",
		filepath.Join(root, "x"):      "",
	} {
		data, err := os.ReadFile(path)
		if want == "" {
			if err == nil {
				t.Fatalf("%s should not exist", path)
			}
			continue
		}
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
		}
	}
}

func TestCopyDirIntoSymlinkedDirectory(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{filepath.Join(root, "from", "a.txt"): "a"})
	if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, "real", filepath.Join(root, "inside"))

	if msg, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "from", "destination": "inside"})); failed {
		t.Fatalf("copy into an in-root symlinked directory failed: %s", msg)
	}
	if data, err := os.ReadFile(filepath.Join(root, "real", "a.txt")); err != nil || string(data) != "a" {
		t.Fatalf("real/a.txt = %q, %v", data, err)
	}
}

// A source link that copyDir skips must not abort the copy just because the
// destination already has something with that name.
func TestCopyDirSkipsSourceSymlinkBeforeCheckingDestination(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{
		filepath.Join(root, "src", "a.txt"): "a",
		filepath.Join(root, "src", "z.txt"): "z",
		filepath.Join(root, "other"):        "other",
	})
	symlinkOrSkip(t, "a.txt", filepath.Join(root, "src", "link"))
	if err := os.Mkdir(filepath.Join(root, "dst"), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(root, "other"), filepath.Join(root, "dst", "link"))

	if msg, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src", "destination": "dst"})); failed {
		t.Fatalf("copy aborted on a skipped symlink: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "dst", "z.txt")); err != nil {
		t.Fatalf("z.txt was not copied: %v", err)
	}
}

func TestCopyFIFODoesNotBlock(t *testing.T) {
	root := withRoot(t)
	if err := syscall.Mkfifo(filepath.Join(root, "p"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	writeTree(t, map[string]string{filepath.Join(root, "d", "ok.txt"): "ok"})
	if err := syscall.Mkfifo(filepath.Join(root, "d", "p"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	for name, args := range map[string]map[string]any{
		"file": {"source": "p", "destination": "q"},
		"dir":  {"source": "d", "destination": "d2"},
	} {
		done := make(chan bool, 1)
		go func() {
			_, failed := resultText(t, mustCall(handleCopyFile, args))
			done <- failed
		}()
		select {
		case failed := <-done:
			if !failed {
				t.Errorf("%s: copying a FIFO succeeded", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: copy_file blocked on a FIFO", name)
		}
	}
}

func TestCopyThroughSymlinkKeepsSourceMode(t *testing.T) {
	root := withRoot(t)
	secret := filepath.Join(root, "secret.txt")
	writeTree(t, map[string]string{secret: "s"})
	symlinkOrSkip(t, "secret.txt", filepath.Join(root, "link"))
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	if msg, failed := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "link", "destination": "copy.txt"})); failed {
		t.Fatalf("copy failed: %s", msg)
	}
	info, err := os.Stat(filepath.Join(root, "copy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("copy mode = %v, want 0600", got)
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
