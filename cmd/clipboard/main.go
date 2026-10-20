// Command clipboard provides an MCP server for Android clipboard management
// via Termux. Tools return JSON results: {ok, text, bytes_len, base64,
// truncated, ...}. Reads (get_clipboard) are non-destructive and do not
// touch history; writes (set_clipboard) push an entry into an in-process,
// bounded history accessible via clipboard_history. clear_clipboard wipes
// both the system clipboard and the in-process history.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig("clipboard")
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	server := core.NewDroidServer("mcp-clipboard", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("clipboard")
	registerTools(server)

	if err := server.Serve(cfg); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("get_clipboard",
		mcp.WithDescription("Read clipboard. Returns JSON {text, bytes_len, base64, is_utf8, truncated}. Binary content is recoverable from base64."),
	), handleGetClipboard)

	s.MCPServer.AddTool(mcp.NewTool("set_clipboard",
		mcp.WithDescription("Write to clipboard. Provide exactly one of `text` (UTF-8) or `text_base64` (decoded as binary)."),
		mcp.WithString("text", mcp.Description("UTF-8 text to write")),
		mcp.WithString("text_base64", mcp.Description("Base64-encoded bytes to write (use for non-UTF-8 / binary content)")),
	), handleSetClipboard)

	s.MCPServer.AddTool(mcp.NewTool("clear_clipboard",
		mcp.WithDescription("Clear the system clipboard and the in-process history. Returns JSON {ok, history_cleared}."),
	), handleClearClipboard)

	s.MCPServer.AddTool(mcp.NewTool("clipboard_history",
		mcp.WithDescription("Return the in-process history of clipboard writes (oldest first). Limits configurable via DROIDMCP_CLIPBOARD_HISTORY_ENTRIES / _BYTES."),
	), handleClipboardHistory)
}

// getClipboardResult is the JSON shape returned by get_clipboard. Text is a
// UTF-8-safe view (replacement char on invalid bytes); the original bytes
// are always available via Base64.
type getClipboardResult struct {
	Text      string `json:"text"`
	BytesLen  int    `json:"bytes_len"`
	Base64    string `json:"base64,omitempty"`
	IsUTF8    bool   `json:"is_utf8"`
	Truncated bool   `json:"truncated,omitempty"`
}

func handleGetClipboard(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(binClipboardGet); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runClipboardCmd(ctx, binClipboardGet, nil, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	out := getClipboardResult{
		BytesLen:  len(res.RawStdout),
		Truncated: res.Truncated,
		IsUTF8:    utf8.Valid(res.RawStdout),
		Base64:    base64.StdEncoding.EncodeToString(res.RawStdout),
	}
	if out.IsUTF8 {
		out.Text = string(res.RawStdout)
	} else {
		out.Text = res.Stdout
	}
	return jsonResult(out)
}

type setClipboardResult struct {
	OK       bool   `json:"ok"`
	BytesLen int    `json:"bytes_len"`
	Source   string `json:"source"`
}

func handleSetClipboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	hasText := stringKeyPresent(args, "text")
	hasB64 := stringKeyPresent(args, "text_base64")
	if hasText == hasB64 {
		return mcp.NewToolResultError("provide exactly one of `text` or `text_base64`"), nil
	}

	var (
		body   []byte
		source string
	)
	if hasText {
		s, _ := args["text"].(string)
		body = []byte(s)
		source = "text"
	} else {
		s, _ := args["text_base64"].(string)
		decoded, derr := base64.StdEncoding.DecodeString(s)
		if derr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("text_base64 invalid: %v", derr)), nil
		}
		body = decoded
		source = "base64"
	}

	if err := ensureBinaries(binClipboardSet); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, err := runClipboardCmd(ctx, binClipboardSet, nil, body)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		merged := struct {
			*clipboardResult
			Source string `json:"source"`
		}{res, source}
		marshalled, _ := json.Marshal(merged)
		return mcp.NewToolResultError(string(marshalled)), nil
	}
	history.Push(body, source)
	return jsonResult(setClipboardResult{
		OK:       true,
		BytesLen: len(body),
		Source:   source,
	})
}

type clearClipboardResult struct {
	OK             bool `json:"ok"`
	HistoryCleared bool `json:"history_cleared"`
}

func handleClearClipboard(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ensureBinaries(binClipboardSet); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// termux-clipboard-set with empty stdin clears the clipboard.
	res, err := runClipboardCmd(ctx, binClipboardSet, nil, []byte{})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.ExitCode != 0 || res.TimedOut || res.Cancelled {
		body, _ := json.Marshal(res)
		return mcp.NewToolResultError(string(body)), nil
	}
	history.Clear()
	return jsonResult(clearClipboardResult{OK: true, HistoryCleared: true})
}

type historyEntryWire struct {
	At        string `json:"at"`
	Source    string `json:"source"`
	BytesLen  int    `json:"bytes_len"`
	Text      string `json:"text,omitempty"`
	Base64    string `json:"base64"`
	IsUTF8    bool   `json:"is_utf8"`
	Truncated bool   `json:"truncated,omitempty"`
}

type historyResult struct {
	Count      int                `json:"count"`
	MaxEntries int                `json:"max_entries"`
	MaxBytes   int64              `json:"max_bytes"`
	Entries    []historyEntryWire `json:"entries"`
}

func handleClipboardHistory(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	snap := history.Snapshot()
	out := historyResult{
		Count:      len(snap),
		MaxEntries: history.maxEntries,
		MaxBytes:   history.maxBytes,
		Entries:    make([]historyEntryWire, 0, len(snap)),
	}
	for _, e := range snap {
		isText := utf8.Valid(e.Content)
		w := historyEntryWire{
			At:        e.At.Format(time.RFC3339),
			Source:    e.Source,
			BytesLen:  e.BytesLen,
			Truncated: e.Truncated,
			IsUTF8:    isText,
			Base64:    base64.StdEncoding.EncodeToString(e.Content),
		}
		if isText {
			w.Text = string(e.Content)
		}
		out.Entries = append(out.Entries, w)
	}
	return jsonResult(out)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// stringKeyPresent reports whether args contains a string-typed value at
// key. It does not require the string to be non-empty: callers can set the
// clipboard to an empty string explicitly.
func stringKeyPresent(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	_, ok = v.(string)
	return ok
}
