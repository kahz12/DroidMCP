// Package core provides the base MCP server implementation.
// It abstracts the mark3labs/mcp-go server logic for DroidMCP services.
package core

import (
	"fmt"

	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/server"
)

// DroidServer wraps the MCP server to provide common transport initialization.
type DroidServer struct {
	MCPServer *server.MCPServer
}

// NewDroidServer initializes a new MCP server with the given identity.
func NewDroidServer(name, version string) *DroidServer {
	s := server.NewMCPServer(name, version)
	return &DroidServer{
		MCPServer: s,
	}
}

// ServeSSE starts the server using the SSE (Server-Sent Events) transport.
// This is the preferred transport for DroidMCP as it works well over HTTP.
func (s *DroidServer) ServeSSE(port int) error {
	// baseURL is used by the MCP protocol to inform clients where to send POST messages.
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	sseServer := server.NewSSEServer(s.MCPServer, server.WithBaseURL(baseURL))

	logger.Info("Starting MCP SSE Server", "port", port, "url", baseURL+"/sse")
	return sseServer.Start(fmt.Sprintf(":%d", port))
}
