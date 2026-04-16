package core

import (
	"fmt"

	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/server"
)

type DroidServer struct {
	MCPServer *server.MCPServer
}

func NewDroidServer(name, version string) *DroidServer {
	s := server.NewMCPServer(name, version)
	return &DroidServer{
		MCPServer: s,
	}
}

func (s *DroidServer) ServeSSE(port int) error {
	baseURL := fmt.Sprintf("http://localhost:%d", port)
	sseServer := server.NewSSEServer(s.MCPServer, server.WithBaseURL(baseURL))

	logger.Info("Starting MCP SSE Server", "port", port)
	return sseServer.Start(fmt.Sprintf(":%d", port))
}
