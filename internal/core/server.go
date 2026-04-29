// Package core provides the base MCP server implementation.
// It abstracts the mark3labs/mcp-go server logic for DroidMCP services.
package core

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/server"
)

// HTTP timeouts. WriteTimeout is intentionally left at 0 because SSE streams
// are long-lived and we do not want to interrupt them. ReadHeaderTimeout and
// IdleTimeout still protect the /message endpoint against slowloris-style
// abuse.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 10 * time.Second
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
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	if s.APIKey == "" {
		logger.Info("Starting MCP SSE Server (auth DISABLED — dev mode)",
			"addr", addr, "url", baseURL+"/sse")
	} else {
		logger.Info("Starting MCP SSE Server",
			"addr", addr, "url", baseURL+"/sse", "auth", "enabled")
	}

	// Wait for SIGINT/SIGTERM in a separate goroutine and trigger a graceful
	// shutdown so in-flight handlers can finish (within shutdownGrace).
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalCtx.Done():
		logger.Info("Shutdown signal received, draining connections", "grace", shutdownGrace)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		return nil
	}
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
