// Command clipboard provides an MCP server for Android clipboard management via Termux.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	server := core.NewDroidServer("mcp-clipboard", "1.0.0")
	server.APIKey = config.ResolveAPIKey("clipboard")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// get_clipboard: Reads current clipboard content.
	getClipboardTool := mcp.NewTool("get_clipboard",
		mcp.WithDescription("Read current clipboard content"),
	)
	s.MCPServer.AddTool(getClipboardTool, handleGetClipboard)

	// set_clipboard: Writes text to clipboard.
	setClipboardTool := mcp.NewTool("set_clipboard",
		mcp.WithDescription("Write text to clipboard"),
		mcp.WithString("text", mcp.Required(), mcp.Description("The text to write to the clipboard")),
	)
	s.MCPServer.AddTool(setClipboardTool, handleSetClipboard)

	// clipboard_history: Retrieve clipboard history (if available).
	clipboardHistoryTool := mcp.NewTool("clipboard_history",
		mcp.WithDescription("Retrieve clipboard history (Note: Not natively supported by Termux API)"),
	)
	s.MCPServer.AddTool(clipboardHistoryTool, handleClipboardHistory)
}

func handleGetClipboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := exec.CommandContext(ctx, "termux-clipboard-get")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error reading clipboard: %v\nOutput: %s", err, string(output))), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func handleSetClipboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	cmd := exec.CommandContext(ctx, "termux-clipboard-set")
	cmd.Stdin = strings.NewReader(text)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error setting clipboard: %v\nOutput: %s", err, string(output))), nil
	}

	return mcp.NewToolResultText("Successfully set clipboard content"), nil
}

func handleClipboardHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Termux API does not currently provide a clipboard history mechanism.
	return mcp.NewToolResultText("Clipboard history is not natively supported by Termux API."), nil
}
