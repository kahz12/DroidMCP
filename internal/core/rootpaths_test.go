package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestStripRoot(t *testing.T) {
	forms := []string{"/r/ws"}
	cases := map[string]string{
		"rename /r/ws/a.txt /r/ws/b/c.txt: no such file or directory": "rename a.txt b/c.txt: no such file or directory",
		"stat /r/ws: permission denied":                               "stat .: permission denied",
		"cannot open /r/ws":                                           "cannot open .",
		`ffmpeg: "/r/ws/in/clip.mp4": Invalid data`:                   `ffmpeg: "in/clip.mp4": Invalid data`,
		"a sibling /r/ws-old/a and /r/ws2/b is kept":                  "a sibling /r/ws-old/a and /r/ws2/b is kept",
		"/r/wsx stays":    "/r/wsx stays",
		"no paths at all": "no paths at all",
	}
	for in, want := range cases {
		if got := stripRoot(in, forms); got != want {
			t.Errorf("stripRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootFormsAddsResolvedSymlinkAndSkipsSlash(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	forms := rootForms(link)
	if len(forms) != 2 || !contains(forms, link) || !contains(forms, realResolved) {
		t.Errorf("rootForms(%q) = %v, want the link and %q", link, forms, realResolved)
	}
	if got := rootForms("/"); len(got) != 0 {
		t.Errorf(`rootForms("/") = %v, want none`, got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRootRelativeErrorsEndToEnd checks the middleware as a client sees it: an
// error naming the root through a symlink and through its real path comes out
// relative, while a successful result is not touched.
func TestRootRelativeErrorsEndToEnd(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)

	ds := newEchoServer("")
	ds.Root = link
	ds.MCPServer.AddTool(mcp.NewTool("fail"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("rename " + filepath.Join(link, "a.txt") + " " +
			filepath.Join(realResolved, "b", "c.txt") + ": no such file or directory"), nil
	})
	ds.MCPServer.AddTool(mcp.NewTool("succeed"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("wrote " + filepath.Join(link, "a.txt")), nil
	})
	ds.applyRoot()

	replies := stdioSession(t, ds,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fail","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"succeed","arguments":{}}}`)
	text := func(id float64) string {
		b, _ := json.Marshal(replies[id]["result"])
		return string(b)
	}
	if got := text(2); !strings.Contains(got, "rename a.txt b/c.txt: no such file or directory") ||
		strings.Contains(got, link) || strings.Contains(got, realResolved) {
		t.Errorf("error result still names the root: %s", got)
	}
	if got := text(3); !strings.Contains(got, filepath.Join(link, "a.txt")) {
		t.Errorf("a successful result must be left alone: %s", got)
	}
}

func TestApplyRootWithoutRootInstallsNothing(t *testing.T) {
	ds := newEchoServer("")
	ds.MCPServer.AddTool(mcp.NewTool("fail"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("open /data/x: denied"), nil
	})
	ds.applyRoot()
	replies := stdioSession(t, ds,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fail","arguments":{}}}`)
	b, _ := json.Marshal(replies[2]["result"])
	if !strings.Contains(string(b), "open /data/x: denied") {
		t.Errorf("without a Root errors must be unchanged: %s", b)
	}
}
