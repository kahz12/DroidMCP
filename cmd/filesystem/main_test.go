package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// callRequest builds an mcp.CallToolRequest with the given arguments map. The
// Get/Require helpers on CallToolRequest expect Arguments to be map[string]any.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// resultText returns the concatenated text of all text-content blocks in the
// result. It also reports whether the call ended in an error so tests can
// assert on both the text and the IsError flag in one step.
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

// withRoot points the global cfg at a freshly created temp directory and
// returns the path. Cleanup is registered via t.Cleanup.
func withRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcp-fs-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg = &config.Config{Root: dir}
	return dir
}

func TestSecurePath(t *testing.T) {
	tmpDir := withRoot(t)

	tests := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"valid path", "test.txt", false},
		{"nested path", "subdir/test.txt", false},
		{"escape attempt", "../outside.txt", true},
		{"absolute escape", "/etc/passwd", true},
		{"dot dot slash", "subdir/../../outside.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := securePath(tt.rel)
			if (err != nil) != tt.wantErr {
				t.Errorf("securePath(%s) error = %v, wantErr %v", tt.rel, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				absRoot, _ := filepath.Abs(tmpDir)
				if !strings.HasPrefix(got, absRoot) {
					t.Errorf("securePath(%s) = %s, does not have prefix %s", tt.rel, got, absRoot)
				}
			}
		})
	}
}

func TestSecurePathSymlinkEscape(t *testing.T) {
	root := withRoot(t)

	// A directory outside root, holding a file we must not be able to reach.
	outside, err := os.MkdirTemp("", "mcp-fs-outside")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A symlink inside root pointing outside root must not be traversable,
	// whether the leaf already exists or is about to be created.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	for _, rel := range []string{"escape", "escape/secret.txt", "escape/newfile.txt"} {
		if _, err := securePath(rel); err == nil {
			t.Errorf("securePath(%q) should be denied: symlink escapes root", rel)
		}
	}

	// A symlink that stays inside root is still allowed.
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inside")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	if _, err := securePath("inside/ok.txt"); err != nil {
		t.Errorf("securePath(inside/ok.txt) should be allowed (symlink stays in root): %v", err)
	}
}

func TestReadFileOffsetLength(t *testing.T) {
	root := withRoot(t)
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("abcdefghij"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"full file", map[string]any{"path": "data.txt"}, "abcdefghij"},
		{"offset only", map[string]any{"path": "data.txt", "offset": 4}, "efghij"},
		{"length only", map[string]any{"path": "data.txt", "length": 3}, "abc"},
		{"offset and length", map[string]any{"path": "data.txt", "offset": 2, "length": 4}, "cdef"},
		{"length past EOF", map[string]any{"path": "data.txt", "offset": 8, "length": 100}, "ij"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isErr := resultText(t, mustCall(handleReadFile, tt.args))
			if isErr {
				t.Fatalf("handleReadFile returned error: %s", got)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("negative offset rejected", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleReadFile, map[string]any{"path": "data.txt", "offset": -1}))
		if !isErr {
			t.Fatalf("expected error, got success: %s", got)
		}
	})
}

func TestReadFileMaxBytes(t *testing.T) {
	root := withRoot(t)
	orig := maxReadBytes
	maxReadBytes = 8
	t.Cleanup(func() { maxReadBytes = orig })

	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unbounded read of a file over the cap is rejected.
	got, isErr := resultText(t, mustCall(handleReadFile, map[string]any{"path": "big.txt"}))
	if !isErr {
		t.Fatalf("expected error for file over cap, got: %s", got)
	}

	// A bounded read within the cap still works.
	got, isErr = resultText(t, mustCall(handleReadFile, map[string]any{"path": "big.txt", "length": 4}))
	if isErr {
		t.Fatalf("bounded read within cap should succeed: %s", got)
	}
	if got != "0123" {
		t.Errorf("got %q, want %q", got, "0123")
	}

	// Requesting more than the cap in one call is rejected up front.
	if _, isErr := resultText(t, mustCall(handleReadFile, map[string]any{"path": "big.txt", "length": 9})); !isErr {
		t.Fatalf("expected error when requested length exceeds cap")
	}

	// A file exactly at the cap reads fine.
	if err := os.WriteFile(filepath.Join(root, "exact.txt"), []byte("01234567"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, isErr = resultText(t, mustCall(handleReadFile, map[string]any{"path": "exact.txt"}))
	if isErr {
		t.Fatalf("file exactly at cap should read: %s", got)
	}
	if got != "01234567" {
		t.Errorf("got %q, want %q", got, "01234567")
	}
}

func TestReadFileLines(t *testing.T) {
	root := withRoot(t)
	body := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"single line", map[string]any{"path": "lines.txt", "start": 2, "end": 2}, "line2\n"},
		{"range", map[string]any{"path": "lines.txt", "start": 2, "end": 4}, "line2\nline3\nline4\n"},
		{"to end (no end)", map[string]any{"path": "lines.txt", "start": 4}, "line4\nline5\n"},
		{"start past EOF", map[string]any{"path": "lines.txt", "start": 99}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isErr := resultText(t, mustCall(handleReadFileLines, tt.args))
			if isErr {
				t.Fatalf("handleReadFileLines returned error: %s", got)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	errCases := []struct {
		name string
		args map[string]any
	}{
		{"start zero", map[string]any{"path": "lines.txt", "start": 0}},
		{"end before start", map[string]any{"path": "lines.txt", "start": 3, "end": 2}},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			_, isErr := resultText(t, mustCall(handleReadFileLines, tt.args))
			if !isErr {
				t.Fatalf("expected error for args %v", tt.args)
			}
		})
	}
}

func TestStat(t *testing.T) {
	root := withRoot(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, isErr := resultText(t, mustCall(handleStat, map[string]any{"path": "f.txt"}))
	if isErr {
		t.Fatalf("handleStat returned error: %s", got)
	}
	var entry fileEntry
	if err := json.Unmarshal([]byte(got), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if entry.Name != "f.txt" {
		t.Errorf("Name = %q, want f.txt", entry.Name)
	}
	if entry.Type != "file" {
		t.Errorf("Type = %q, want file", entry.Type)
	}
	if entry.Size != 5 {
		t.Errorf("Size = %d, want 5", entry.Size)
	}
	if entry.ModeOct != "0600" {
		t.Errorf("ModeOct = %q, want 0600", entry.ModeOct)
	}
	if entry.Modified == "" {
		t.Errorf("Modified is empty")
	}
}

func TestListDirectoryJSON(t *testing.T) {
	root := withRoot(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, isErr := resultText(t, mustCall(handleListDirectory, map[string]any{"path": "."}))
	if isErr {
		t.Fatalf("handleListDirectory returned error: %s", got)
	}
	var entries []fileEntry
	if err := json.Unmarshal([]byte(got), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	byName := map[string]fileEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if byName["a.txt"].Type != "file" || byName["a.txt"].Size != 2 {
		t.Errorf("a.txt entry wrong: %+v", byName["a.txt"])
	}
	if byName["sub"].Type != "dir" {
		t.Errorf("sub entry wrong: %+v", byName["sub"])
	}
}

func TestSearchFilesGlobAndRegex(t *testing.T) {
	root := withRoot(t)
	for _, name := range []string{"alpha.go", "beta.go", "gamma.txt", "delta.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("glob pattern", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{"pattern": "*.go"}))
		if isErr {
			t.Fatalf("error: %s", got)
		}
		lines := strings.Split(strings.TrimSpace(got), "\n")
		if len(lines) != 2 {
			t.Errorf("got %d matches, want 2: %v", len(lines), lines)
		}
	})

	t.Run("regex", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{"regex": `^(alpha|gamma)\.`}))
		if isErr {
			t.Fatalf("error: %s", got)
		}
		lines := strings.Split(strings.TrimSpace(got), "\n")
		if len(lines) != 2 {
			t.Errorf("got %d matches, want 2: %v", len(lines), lines)
		}
	})

	t.Run("max_results caps output", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{"pattern": "*", "max_results": 2}))
		if isErr {
			t.Fatalf("error: %s", got)
		}
		lines := strings.Split(strings.TrimSpace(got), "\n")
		if len(lines) != 2 {
			t.Errorf("got %d matches, want 2: %v", len(lines), lines)
		}
	})

	t.Run("pattern and regex mutually exclusive", func(t *testing.T) {
		_, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{"pattern": "*.go", "regex": "x"}))
		if !isErr {
			t.Fatalf("expected error when both pattern and regex are set")
		}
	})

	t.Run("neither pattern nor regex", func(t *testing.T) {
		_, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{}))
		if !isErr {
			t.Fatalf("expected error when neither pattern nor regex is set")
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		_, isErr := resultText(t, mustCall(handleSearchFiles, map[string]any{"regex": "([unclosed"}))
		if !isErr {
			t.Fatalf("expected error on invalid regex")
		}
	})
}

// search_files walks the whole tree, so it must stop once the request is
// cancelled instead of finishing the walk.
func TestSearchFilesStopsWhenCancelled(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{filepath.Join(root, "a.txt"): "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := handleSearchFiles(ctx, callRequest(map[string]any{"pattern": "*.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, failed := resultText(t, res); !failed || !strings.Contains(got, "context canceled") {
		t.Fatalf("cancelled search = %q (error=%v), want a cancellation error", got, failed)
	}
}

func TestDeleteFileRecursive(t *testing.T) {
	root := withRoot(t)
	dir := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("non-recursive on non-empty dir hints recursive", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleDeleteFile, map[string]any{"path": "tree"}))
		if !isErr {
			t.Fatalf("expected error, got success: %s", got)
		}
		if !strings.Contains(got, "recursive=true") {
			t.Errorf("error message should hint at recursive=true; got: %s", got)
		}
	})

	t.Run("recursive succeeds", func(t *testing.T) {
		got, isErr := resultText(t, mustCall(handleDeleteFile, map[string]any{"path": "tree", "recursive": true}))
		if isErr {
			t.Fatalf("recursive delete failed: %s", got)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("tree should be gone, stat err = %v", err)
		}
	})
}

func TestCopyFile(t *testing.T) {
	root := withRoot(t)
	if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, isErr := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "src.txt", "destination": "dst/copy.txt"}))
	if isErr {
		t.Fatalf("copy failed: %s", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "dst", "copy.txt"))
	if err != nil {
		t.Fatalf("destination missing: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("destination contents = %q, want %q", data, "payload")
	}
}

func TestCopyDir(t *testing.T) {
	root := withRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "from", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "from", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "from", "nested", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, isErr := resultText(t, mustCall(handleCopyFile, map[string]any{"source": "from", "destination": "to"}))
	if isErr {
		t.Fatalf("copy dir failed: %s", got)
	}
	for _, p := range []string{"to/a.txt", "to/nested/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
}

func TestMoveFile(t *testing.T) {
	root := withRoot(t)
	writeTree(t, map[string]string{
		filepath.Join(root, "a.txt"):        "payload",
		filepath.Join(root, "dir", "b.txt"): "B",
		filepath.Join(root, "keep.txt"):     "keep",
		filepath.Join(root, "old.txt"):      "old",
	})
	move := func(src, dst string) (string, bool) {
		return resultText(t, mustCall(handleMoveFile, map[string]any{"source": src, "destination": dst}))
	}
	content := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(data)
	}

	t.Run("renames a file", func(t *testing.T) {
		if msg, failed := move("a.txt", "renamed.txt"); failed {
			t.Fatalf("move failed: %s", msg)
		}
		if got := content("renamed.txt"); got != "payload" {
			t.Fatalf("renamed.txt = %q", got)
		}
		if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
			t.Fatalf("source still exists: %v", err)
		}
	})

	t.Run("moves a directory", func(t *testing.T) {
		if msg, failed := move("dir", "dir2"); failed {
			t.Fatalf("move failed: %s", msg)
		}
		if got := content("dir2/b.txt"); got != "B" {
			t.Fatalf("dir2/b.txt = %q", got)
		}
	})

	// os.Rename semantics, as documented for the tool: an existing file at the
	// destination is replaced.
	t.Run("replaces an existing destination file", func(t *testing.T) {
		if msg, failed := move("old.txt", "renamed.txt"); failed {
			t.Fatalf("move failed: %s", msg)
		}
		if got := content("renamed.txt"); got != "old" {
			t.Fatalf("renamed.txt = %q, want the moved content", got)
		}
	})

	t.Run("rejects a directory moved into itself", func(t *testing.T) {
		if _, failed := move("dir2", "dir2/inner"); !failed {
			t.Fatal("moved a directory into its own subtree")
		}
		if got := content("dir2/b.txt"); got != "B" {
			t.Fatalf("dir2/b.txt = %q after a rejected move", got)
		}
	})

	t.Run("rejects paths outside root", func(t *testing.T) {
		outside := t.TempDir()
		symlinkOrSkip(t, filepath.Join(outside, "stolen.txt"), filepath.Join(root, "evil"))
		for _, args := range [][2]string{
			{"keep.txt", "../stolen.txt"},
			{"keep.txt", "/tmp/stolen.txt"},
			{"../keep.txt", "x"},
			{"keep.txt", "evil"},
		} {
			if _, failed := move(args[0], args[1]); !failed {
				t.Errorf("move %q -> %q succeeded, want an escape error", args[0], args[1])
			}
		}
		if got := content("keep.txt"); got != "keep" {
			t.Fatalf("keep.txt = %q after rejected moves", got)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Fatalf("files appeared outside root: %v", entries)
		}
	})
}

// mustCall invokes a handler with the given args and returns its result.
// Handler errors are propagated as test failures since none of these handlers
// are designed to return a non-nil error (they encode failures in the result).
func mustCall(h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		panic(err)
	}
	return res
}
