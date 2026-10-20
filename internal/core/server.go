// Package core provides the base MCP server implementation.
// It abstracts the mark3labs/mcp-go server logic for DroidMCP services.
package core

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/dnsconf"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/server"
)

// HTTP timeouts. WriteTimeout is intentionally left at 0 because SSE streams
// (legacy /sse and the Streamable HTTP GET stream) are long-lived and we do not
// want to interrupt them. ReadHeaderTimeout and IdleTimeout still protect the
// POST endpoints against slowloris-style abuse.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 10 * time.Second
)

// authHeader is the request header every DroidMCP client must set when a
// server is configured with an API key.
const authHeader = "X-DroidMCP-Key"

// minKeyLength is the API key length below which startup warns. The docs
// suggest `openssl rand -base64 32`, which yields 44 characters.
const minKeyLength = 16

// Env keys for optional TLS support. Both must be set to a readable cert/key
// pair; otherwise the server falls back to plain HTTP on 127.0.0.1.
const (
	envTLSCert = "DROIDMCP_TLS_CERT"
	envTLSKey  = "DROIDMCP_TLS_KEY"
)

// envAllowedHosts optionally extends the always-allowed loopback Host names
// with operator-supplied hostnames (comma-separated, no port), for
// reverse-proxy or port-forward front-ends. See hostGuard.
const envAllowedHosts = "DROIDMCP_ALLOWED_HOSTS"

// streamablePath is where the Streamable HTTP transport is mounted. The legacy
// SSE transport keeps its own /sse and /message routes on the same listener.
const streamablePath = "/mcp"

// DroidServer wraps the MCP server to provide common transport initialization.
type DroidServer struct {
	MCPServer *server.MCPServer

	// Name and Version identify the server in /healthz responses and log
	// lines.
	Name    string
	Version string

	// APIKey is the secret required in the X-DroidMCP-Key header for every
	// inbound request. An empty value disables authentication (dev mode) and
	// is logged loudly at startup.
	APIKey string

	// DefaultRateLimits are the server's built-in limits by tool name (or
	// config.AllTools), applied unless DROIDMCP_<SERVER>_RATE_LIMITS overrides
	// or switches them off. Most servers set none.
	DefaultRateLimits map[string]config.RateLimit
}

// dnsServers points DNS at Termux's nameservers on Android (see dnsconf). It is
// set when the package initializes, before any server's main runs, so even the
// lookups a server makes before NewDroidServer (mcp-github validates its token
// first) work.
var dnsServers = dnsconf.Install()

// NewDroidServer initializes a new MCP server with the given identity.
func NewDroidServer(name, version string) *DroidServer {
	if dnsServers != nil {
		logger.Info("DNS: no /etc/resolv.conf, using Termux's nameservers", "servers", strings.Join(dnsServers, ","))
	}
	s := server.NewMCPServer(name, version)
	return &DroidServer{
		MCPServer: s,
		Name:      name,
		Version:   version,
	}
}

// Serve installs the rate limits and removes the tools the operator disabled,
// then runs the server on the transport cfg selects (DROIDMCP_TRANSPORT):
// stdio, or the loopback HTTP listener on cfg.Port. It blocks until the client
// goes away (stdio) or a shutdown signal arrives.
func (s *DroidServer) Serve(cfg *config.Config) error {
	if err := s.applyRateLimits(cfg); err != nil {
		return err
	}
	if err := s.disableTools(cfg); err != nil {
		return err
	}
	if cfg.Stdio() {
		return s.serveStdio()
	}
	return s.serveHTTP(cfg.Port)
}

// disableTools unregisters the tools listed in DROIDMCP_<SERVER>_DISABLED_TOOLS
// so clients can neither list nor call them. An unknown name is an error rather
// than a no-op: a typo would otherwise leave enabled the very tool the operator
// meant to switch off.
func (s *DroidServer) disableTools(cfg *config.Config) error {
	if len(cfg.DisabledTools) == 0 {
		return nil
	}
	registered := s.MCPServer.ListTools()
	var unknown []string
	for _, name := range cfg.DisabledTools {
		if _, ok := registered[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s names unknown tool(s) %s; %s has: %s",
			config.DisabledToolsVar(cfg.Server), strings.Join(unknown, ", "), s.Name, strings.Join(s.toolNames(), ", "))
	}
	s.MCPServer.DeleteTools(cfg.DisabledTools...)
	logger.Info("Tools disabled", "tools", strings.Join(cfg.DisabledTools, ","))
	return nil
}

// serveStdio speaks MCP over stdin/stdout until stdin closes or SIGINT/SIGTERM
// arrives. Logs already go to stderr, so stdout carries only protocol frames.
func (s *DroidServer) serveStdio() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("Starting MCP stdio server", "server", s.Name, "version", s.Version)
	return s.listenStdio(ctx, os.Stdin, os.Stdout)
}

// listenStdio is serveStdio without the process plumbing, so tests can drive
// it with in-memory streams. A cancelled context is a clean stop, not an error.
func (s *DroidServer) listenStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	stdio := server.NewStdioServer(s.MCPServer)
	stdio.SetErrorLogger(slog.NewLogLogger(logger.Log.Handler(), slog.LevelError))
	if err := stdio.Listen(ctx, in, out); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// serveHTTP serves MCP over HTTP: Streamable HTTP at /mcp and the legacy SSE
// transport at /sse + /message, on one listener.
// The listener is always bound to 127.0.0.1 so the server is unreachable from
// external network interfaces. Every request must also carry a loopback Host
// header and, when a browser sends one, a loopback Origin (hostGuard) so a
// rebinding or cross-site page cannot reach the dev-mode servers.
// All MCP routes are wrapped in the API key middleware; /healthz is exposed
// unauthenticated for supervisors. TLS is enabled when both DROIDMCP_TLS_CERT
// and DROIDMCP_TLS_KEY are set.
func (s *DroidServer) serveHTTP(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	tlsCert := os.Getenv(envTLSCert)
	tlsKey := os.Getenv(envTLSKey)
	useTLS := tlsCert != "" && tlsKey != ""

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, addr)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           s.httpHandler(baseURL, useTLS),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Failed keys are not rate limited: every client is 127.0.0.1, so a lockout
	// would let any local app lock the real client out. Key length is the
	// defence against guessing, so flag a short one.
	if s.APIKey != "" && len(s.APIKey) < minKeyLength {
		logger.Log.Warn("API key is shorter than the recommended length; generate one with `openssl rand -base64 32`",
			"length", len(s.APIKey), "recommended", minKeyLength)
	}

	if s.APIKey == "" {
		logger.Info("Starting MCP HTTP server (auth DISABLED — dev mode)",
			"addr", addr, "url", baseURL+streamablePath, "sse_url", baseURL+"/sse", "tls", useTLS)
	} else {
		logger.Info("Starting MCP HTTP server",
			"addr", addr, "url", baseURL+streamablePath, "sse_url", baseURL+"/sse", "auth", "enabled", "tls", useTLS)
	}

	// Wait for SIGINT/SIGTERM in a separate goroutine and trigger a graceful
	// shutdown so in-flight handlers can finish (within shutdownGrace).
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		if useTLS {
			serveErr <- httpSrv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			serveErr <- httpSrv.ListenAndServe()
		}
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

// httpHandler builds the complete HTTP handler: both MCP transports behind the
// middleware chain. baseURL is the origin the SSE transport advertises for its
// /message endpoint.
func (s *DroidServer) httpHandler(baseURL string, tlsEnabled bool) http.Handler {
	sseServer := server.NewSSEServer(s.MCPServer, server.WithBaseURL(baseURL))
	streamable := server.NewStreamableHTTPServer(s.MCPServer, server.WithEndpointPath(streamablePath))
	return buildHandler(s, tlsEnabled, mcpRoutes(streamable, sseServer))
}

// mcpRoutes mounts both MCP transports: Streamable HTTP at streamablePath and
// everything else on the SSE server, which answers /sse and /message and 404s
// any other path.
func mcpRoutes(streamable, sse http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(streamablePath, streamable)
	mux.Handle("/", sse)
	return mux
}

// buildHandler composes the full middleware chain around the MCP transport
// handler. It is separate from serveHTTP so tests can exercise the chain
// without binding a network listener.
func buildHandler(s *DroidServer, tlsEnabled bool, mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/", authMiddleware(s.APIKey, mcpHandler))
	guarded := hostGuard(parseAllowedHosts(os.Getenv(envAllowedHosts)), mux)
	return requestLogger(securityHeaders(tlsEnabled, guarded))
}

// parseAllowedHosts turns the comma-separated DROIDMCP_ALLOWED_HOSTS value into
// a lowercased set of extra hostnames permitted in the Host header, on top of
// the always-allowed loopback names. Empty entries are ignored.
func parseAllowedHosts(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, h := range strings.Split(raw, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out[h] = struct{}{}
		}
	}
	return out
}

// hostGuard rejects requests whose Host header is neither a loopback
// destination nor in the operator-configured allowlist. The listener only
// binds 127.0.0.1, but a browser tricked into DNS rebinding can still reach it
// with an attacker-chosen Host; pinning Host to loopback closes that vector,
// which matters most for the key-less (dev-mode) servers where auth would not
// otherwise stop it. Reverse-proxy / port-forward front-ends can add their
// external hostname via DROIDMCP_ALLOWED_HOSTS.
//
// A browser also names the page that issued the request in Origin; the
// Streamable HTTP spec requires servers to validate it. MCP clients outside a
// browser send none, so only a present Origin is checked, against the same
// loopback-or-allowlisted rule ("null", from sandboxed or file:// pages, fails).
func hostGuard(extra map[string]struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, extra) {
			logger.Log.Warn("host rejected",
				"remote", r.RemoteAddr, "host", r.Host, "path", r.URL.Path)
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, extra) {
			logger.Log.Warn("origin rejected",
				"remote", r.RemoteAddr, "origin", origin, "path", r.URL.Path)
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether an Origin header ("scheme://host[:port]")
// names a host hostAllowed accepts.
func originAllowed(origin string, extra map[string]struct{}) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return hostAllowed(u.Host, extra)
}

// hostAllowed reports whether the Host header names a loopback destination
// ("localhost" or any loopback IP literal) or an operator-allowed hostname.
func hostAllowed(host string, extra map[string]struct{}) bool {
	name := hostnameOnly(host)
	if name == "" {
		return false
	}
	if strings.EqualFold(name, "localhost") {
		return true
	}
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	_, ok := extra[strings.ToLower(name)]
	return ok
}

// hostnameOnly strips an optional port and IPv6 brackets from a Host header
// value, returning just the hostname/IP.
func hostnameOnly(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}

// handleHealthz responds with a small JSON document identifying the server.
// It is mounted *before* the auth middleware so external supervisors (systemd
// watchdog, Docker healthcheck, etc.) can probe liveness without needing the
// API key.
func (s *DroidServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(map[string]any{
		"status":  "ok",
		"server":  s.Name,
		"version": s.Version,
	})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// securityHeaders sets a small set of defensive response headers on every
// reply. Cache-Control + X-Content-Type-Options are always emitted; HSTS is
// only emitted when the server is actually listening on TLS, so we never
// advertise a transport we cannot honor.
func securityHeaders(tlsEnabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		if tlsEnabled {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
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

// statusRecorder wraps http.ResponseWriter so requestLogger can read back the
// final status code and signal whether the connection was hijacked (SSE
// streams). It also forwards Flush/Hijack so the SSE server can still keep
// long-lived connections open.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	hijacked    bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	r.hijacked = true
	return h.Hijack()
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLogger logs one line per request after the handler returns. For SSE
// streams (Hijack used) the latency reflects the full stream lifetime and the
// log line is deferred until the connection closes — this is the cleanest
// signal we can give an operator without spamming a line per chunk. The
// X-DroidMCP-Key header is never read here, so the API key cannot leak.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"latency_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"hijacked", rec.hijacked,
		)
	})
}
