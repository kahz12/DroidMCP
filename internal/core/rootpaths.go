package core

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// applyRoot installs rootRelativeErrors when the server has a Root. Like the
// rate limiter it is tool middleware, so it covers every transport.
func (s *DroidServer) applyRoot() {
	if s.Root != "" {
		s.MCPServer.Use(rootRelativeErrors(s.Root))
	}
}

// rootRelativeErrors rewrites DROIDMCP_ROOT out of every error result, so a
// failing tool names paths the way the caller gave them, relative to the root,
// and does not disclose where the root lives on the device. OS errors carry
// absolute paths ("rename /data/data/com.termux/files/home/ws/a.txt …"), and
// so does ffmpeg's stderr. Successful results are left alone: the servers
// already report relative paths there.
func rootRelativeErrors(root string) server.ToolHandlerMiddleware {
	forms := rootForms(root)
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := next(ctx, req)
			if res == nil || !res.IsError {
				return res, err
			}
			for i, c := range res.Content {
				switch tc := c.(type) {
				case mcp.TextContent:
					tc.Text = stripRoot(tc.Text, forms)
					res.Content[i] = tc
				case *mcp.TextContent:
					tc.Text = stripRoot(tc.Text, forms)
				}
			}
			return res, err
		}
	}
}

// rootForms returns the spellings an error may use for root: as configured
// (made absolute) and with symlinks resolved. On Android /data/data/<pkg> and
// /data/user/0/<pkg> name the same directory, and OS errors may show either.
// The longest comes first so a form that contains another is replaced whole.
func rootForms(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	forms := []string{filepath.Clean(abs)}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real != forms[0] {
		forms = append(forms, real)
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	out := forms[:0]
	for _, f := range forms {
		// A root of "/" is refused at startup; never rewrite every path.
		if f != string(filepath.Separator) {
			out = append(out, f)
		}
	}
	return out
}

// stripRoot turns "<root>/a/b" into "a/b" and a bare "<root>" into ".". An
// occurrence followed by anything else that can continue a path name
// ("<root>-old", "<root>2") is a different directory and is kept.
func stripRoot(s string, forms []string) string {
	for _, root := range forms {
		var b strings.Builder
		for {
			i := strings.Index(s, root)
			if i < 0 {
				b.WriteString(s)
				break
			}
			b.WriteString(s[:i])
			rest := s[i+len(root):]
			switch {
			case strings.HasPrefix(rest, string(filepath.Separator)):
				rest = rest[1:]
			case rest == "" || !continuesName(rest[0]):
				b.WriteString(".")
			default:
				b.WriteString(root)
			}
			s = rest
		}
		s = b.String()
	}
	return s
}

// continuesName reports whether c can be part of a path component, so that
// "<root>" followed by it names some other path.
func continuesName(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c == '~' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
