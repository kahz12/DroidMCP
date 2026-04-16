// Package core provides the base MCP server implementation.
// It abstracts the mark3labs/mcp-go server logic for DroidMCP services.
package core

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/server"
)

// authHeader is the request header every DroidMCP client must set when a
// server is configured with an API key.
const authHeader = "X-DroidMCP-Key"

// DroidServer wraps the MCP server to provide common transport initialization.
type DroidServer struct {
	MCPServer *server.MCPServer

	// APIKey is the secret required in the X-DroidMCP-Key header for every
	// inbound request. An empty value disables authentication (dev mode) and
	// is logged loudly at startup.
	APIKey string
}

// NewDroidServer initializes a new MCP server with the given identity.
func NewDroidServer(name, version string) *DroidServer {
	s := server.NewMCPServer(name, version)
	return &DroidServer{
		MCPServer: s,
	}
}

// ServeSSE starts the server using the SSE (Server-Sent Events) transport.
// The listener is always bound to 127.0.0.1 so the server is unreachable from
// external network interfaces. All routes (SSE and message endpoints) are
// wrapped in the API key middleware.
func (s *DroidServer) ServeSSE(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	baseURL := fmt.Sprintf("http://%s", addr)

	sseServer := server.NewSSEServer(s.MCPServer, server.WithBaseURL(baseURL))
	handler := authMiddleware(s.APIKey, sseServer)

	httpSrv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	if s.APIKey == "" {
		logger.Info("Starting MCP SSE Server (auth DISABLED — dev mode)",
			"addr", addr, "url", baseURL+"/sse")
	} else {
		logger.Info("Starting MCP SSE Server",
			"addr", addr, "url", baseURL+"/sse", "auth", "enabled")
	}
	return httpSrv.ListenAndServe()
}

// authMiddleware enforces that every inbound request carries a valid
// X-DroidMCP-Key header. When apiKey is empty, the middleware is a passthrough
// so local development does not require a secret.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	expected := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get(authHeader)
		if presented == "" {
			rejectAuth(w, r, "missing key")
			return
		}
		// Constant-time compare to avoid leaking key length/content via timing.
		if subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
			rejectAuth(w, r, "invalid key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rejectAuth(w http.ResponseWriter, r *http.Request, reason string) {
	logger.Log.Warn("auth rejected",
		"remote", r.RemoteAddr,
		"path", r.URL.Path,
		"reason", reason,
	)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
