package main

import (
	"context"

	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolSpec couples an MCP tool definition with its handler so each tool is a
// single self-contained unit and registration becomes data-driven.
type ToolSpec struct {
	Tool    mcp.Tool
	Handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// register adds every spec to the server. Adding a tool no longer means
// touching this loop: append a ToolSpec to the relevant tools_*.go file.
func register(s *core.DroidServer, specs []ToolSpec) {
	for _, sp := range specs {
		s.MCPServer.AddTool(sp.Tool, sp.Handler)
	}
}
