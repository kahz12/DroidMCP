package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func newTestServer(apiKey string) *DroidServer {
	ds := &DroidServer{
		MCPServer: server.NewMCPServer("test", "0.0.1"),
		Name:      "test",
		Version:   "0.0.1",
		APIKey:    apiKey,
	}
	return ds
}

func TestHealthzReturnsJSONAndBypassesAuth(t *testing.T) {
	ds := newTestServer("super-secret")
	h := buildHandler(ds, false, http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, w.Body.String())
	}
	if body["status"] != "ok" || body["server"] != "test" || body["version"] != "0.0.1" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestSecurityHeadersAlwaysSet(t *testing.T) {
	ds := newTestServer("")
	h := buildHandler(ds, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: %q", got)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should not be set without TLS, got %q", got)
	}
}

func TestSecurityHeadersHSTSOnlyWithTLS(t *testing.T) {
	ds := newTestServer("")
	h := buildHandler(ds, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if !strings.Contains(hsts, "max-age=") {
		t.Errorf("expected HSTS with max-age, got %q", hsts)
	}
}

func TestAuthMiddlewareDevModeBypasses(t *testing.T) {
	called := false
	h := authMiddleware("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler should have been called in dev mode")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status: %d", w.Code)
	}
}

func TestAuthMiddlewareRejectsMissingKey(t *testing.T) {
	h := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler should not be called when key is missing")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d", w.Code)
	}
}

func TestAuthMiddlewareRejectsWrongKey(t *testing.T) {
	h := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler should not be called with wrong key")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(authHeader, "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsCorrectKey(t *testing.T) {
	called := false
	h := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(authHeader, "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler should have been called")
	}
}

func TestRequestLoggerCapturesStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	rec.WriteHeader(http.StatusTeapot)
	rec.WriteHeader(http.StatusInternalServerError) // ignored — only first call wins
	if rec.status != http.StatusTeapot {
		t.Errorf("recorder should keep first status, got %d", rec.status)
	}
}

func TestRequestLoggerWriteDefaultsTo200(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	_, _ = rec.Write([]byte("hi"))
	if rec.status != http.StatusOK {
		t.Errorf("status: %d", rec.status)
	}
	if !rec.wroteHeader {
		t.Error("Write should mark the header as written")
	}
}

func TestPipelineHealthzWithSecretSet(t *testing.T) {
	// End-to-end check: even with auth configured, /healthz still returns 200
	// without a key, and the security-headers + logger middlewares fire.
	ds := newTestServer("topsecret")
	h := buildHandler(ds, false, http.NotFoundHandler())
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control missing on end-to-end /healthz")
	}
}

func TestPipelineProtectedRouteRequiresKey(t *testing.T) {
	ds := newTestServer("topsecret")
	// Downstream handler that should not be reached without a valid key.
	protected := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("inside"))
	})
	h := buildHandler(ds, false, protected)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unprotected request should be 401, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/sse", nil)
	req.Header.Set(authHeader, "topsecret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("authenticated request should be 200, got %d", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != "inside" {
		t.Errorf("body: %q", body)
	}
}

func TestHostGuardRejectsNonLoopback(t *testing.T) {
	ds := newTestServer("")
	reached := false
	h := buildHandler(ds, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	// A rebinding browser would present its own domain as the Host header.
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-loopback Host should be 403, got %d", w.Code)
	}
	if reached {
		t.Error("handler must not run for a rejected Host")
	}
}

func TestHostGuardAllowsLoopbackForms(t *testing.T) {
	ds := newTestServer("")
	h := buildHandler(ds, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, host := range []string{"127.0.0.1", "127.0.0.1:3000", "localhost:8080", "[::1]:9000", "127.5.6.7"} {
		req := httptest.NewRequest(http.MethodGet, "http://placeholder/anything", nil)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Host %q should be allowed, got %d", host, w.Code)
		}
	}
}

func TestHostGuardEnvAllowlist(t *testing.T) {
	t.Setenv(envAllowedHosts, "proxy.internal, mcp.example")
	ds := newTestServer("")
	h := buildHandler(ds, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://mcp.example/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("allowlisted Host should pass, got %d", w.Code)
	}

	// A host outside both loopback and the allowlist is still rejected.
	req2 := httptest.NewRequest(http.MethodGet, "http://other.example/anything", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("non-allowlisted Host should be 403, got %d", w2.Code)
	}
}

func TestHostnameOnly(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:3000": "127.0.0.1",
		"127.0.0.1":      "127.0.0.1",
		"localhost:8080": "localhost",
		"[::1]:9000":     "::1",
		"::1":            "::1",
		"[::1]":          "::1",
		"":               "",
	}
	for in, want := range cases {
		if got := hostnameOnly(in); got != want {
			t.Errorf("hostnameOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOriginGuard(t *testing.T) {
	t.Setenv(envAllowedHosts, "mcp.example")
	ds := newTestServer("")
	h := buildHandler(ds, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		origin string
		want   int
	}{
		{"", http.StatusOK}, // MCP clients outside a browser send no Origin
		{"http://localhost:6274", http.StatusOK},
		{"http://127.0.0.1:3000", http.StatusOK},
		{"https://mcp.example", http.StatusOK},
		{"https://evil.example", http.StatusForbidden},
		{"http://localhost.evil.example", http.StatusForbidden},
		{"null", http.StatusForbidden},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3000/mcp", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("Origin %q: got %d, want %d", c.origin, w.Code, c.want)
		}
	}
}

// newEchoServer returns a test server with one tool, echo, that returns its
// text argument.
func newEchoServer(apiKey string) *DroidServer {
	ds := newTestServer(apiKey)
	ds.MCPServer.AddTool(mcp.NewTool("echo",
		mcp.WithString("text", mcp.Required()),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(req.GetString("text", "")), nil
	})
	return ds
}

// startHTTP serves ds through the production handler on a loopback listener
// and returns its base URL. The listener is opened first so the SSE transport
// can advertise the real address, as serveHTTP does.
func startHTTP(t *testing.T, ds *DroidServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + ln.Addr().String()
	srv := httptest.NewUnstartedServer(ds.httpHandler(baseURL, false))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return baseURL
}

// initializeAndEcho runs the MCP handshake on c and one echo tools/call.
func initializeAndEcho(t *testing.T, c *client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "core-test", Version: "0"}
	res, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "test" {
		t.Errorf("server name: %q", res.ServerInfo.Name)
	}

	var call mcp.CallToolRequest
	call.Params.Name = "echo"
	call.Params.Arguments = map[string]any{"text": "hola"}
	out, err := c.CallTool(ctx, call)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("content: %+v", out.Content)
	}
	if text, ok := out.Content[0].(mcp.TextContent); !ok || text.Text != "hola" {
		t.Errorf("echo returned %+v", out.Content[0])
	}
}

func TestStreamableHTTPEndToEnd(t *testing.T) {
	baseURL := startHTTP(t, newEchoServer("topsecret"))

	// The key is enforced on /mcp like on the SSE routes.
	resp, err := http.Post(baseURL+streamablePath, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unkeyed POST /mcp: got %d, want 401", resp.StatusCode)
	}

	c, err := client.NewStreamableHttpClient(baseURL+streamablePath,
		transport.WithHTTPHeaders(map[string]string{authHeader: "topsecret"}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	initializeAndEcho(t, c)
}

func TestLegacySSEEndToEnd(t *testing.T) {
	baseURL := startHTTP(t, newEchoServer("topsecret"))

	c, err := client.NewSSEMCPClient(baseURL+"/sse",
		transport.WithHeaders(map[string]string{authHeader: "topsecret"}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	initializeAndEcho(t, c)
}

// stdioSession drives ds over stdio the way a client does: it sends
// initialize, then requests (each must carry a unique, non-zero id), reads
// until every one has its reply, and only then closes stdin. A client that
// closes stdin is gone, and from mcp-go v1 on the server cancels — and may
// drop — whatever is still queued at that point, so hanging up early would
// race the replies. It returns the replies to requests, by id.
func stdioSession(t *testing.T, ds *DroidServer, requests ...string) map[float64]map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() {
		served <- ds.listenStdio(ctx, inR, outW)
		_ = outW.Close()
	}()
	// A reply that never comes fails the test instead of hanging it.
	timer := time.AfterFunc(10*time.Second, func() {
		_ = outR.CloseWithError(errors.New("timed out waiting for replies"))
	})
	defer timer.Stop()

	messages := append([]string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` + mcp.LATEST_PROTOCOL_VERSION + `","capabilities":{},"clientInfo":{"name":"core-test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}, requests...)
	go func() {
		for _, m := range messages {
			if _, err := io.WriteString(inW, m+"\n"); err != nil {
				return
			}
		}
	}()

	want := len(requests) + 1 // initialize is answered too
	replies := map[float64]map[string]any{}
	sc := bufio.NewScanner(outR)
	for len(replies) < want && sc.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			t.Fatalf("stdout carried a non-JSON line %q: %v", sc.Text(), err)
		}
		if id, ok := msg["id"].(float64); ok {
			replies[id] = msg
		}
	}
	if len(replies) < want {
		t.Fatalf("got %d of %d replies: %v", len(replies), want, sc.Err())
	}
	if _, ok := replies[0]["result"]; !ok {
		t.Fatalf("initialize failed: %+v", replies[0])
	}

	// Hang up, as a client does, and let the server wind down.
	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	if err := <-served; err != nil {
		t.Fatalf("listenStdio: %v", err)
	}
	delete(replies, 0)
	return replies
}

func TestStdioEndToEnd(t *testing.T) {
	replies := stdioSession(t, newEchoServer(""),
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hola"}}}`)
	result, _ := replies[2]["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "hola" {
		t.Errorf("tools/call response: %+v", replies[2])
	}
}

func TestStdioCancelIsCleanStop(t *testing.T) {
	ds := newTestServer("")
	in, w := io.Pipe()
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ds.listenStdio(ctx, in, io.Discard); err != nil {
		t.Errorf("a cancelled context should stop cleanly, got %v", err)
	}
}

func TestDisableToolsRemovesThem(t *testing.T) {
	ds := newEchoServer("")
	ds.MCPServer.AddTool(mcp.NewTool("dangerous"),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Fatal("a disabled tool must not run")
			return nil, nil
		})

	if err := ds.disableTools(&config.Config{Server: "termux", DisabledTools: []string{"dangerous"}}); err != nil {
		t.Fatal(err)
	}
	if ds.MCPServer.GetTool("dangerous") != nil {
		t.Error("disabled tool is still registered")
	}
	if ds.MCPServer.GetTool("echo") == nil {
		t.Error("a tool that was not listed was removed too")
	}

	// A client sees it neither in tools/list nor through tools/call.
	replies := stdioSession(t, ds,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"dangerous","arguments":{}}}`)
	if listed, _ := json.Marshal(replies[2]); strings.Contains(string(listed), `"name":"dangerous"`) {
		t.Errorf("tools/list still offers the disabled tool: %s", listed)
	}
	if _, isErr := replies[3]["error"]; !isErr {
		t.Errorf("calling the disabled tool should be a JSON-RPC error: %+v", replies[3])
	}
}

func TestDisableToolsRejectsUnknownNames(t *testing.T) {
	ds := newEchoServer("")
	err := ds.disableTools(&config.Config{Server: "termux", DisabledTools: []string{"echo", "run_comand"}})
	if err == nil {
		t.Fatal("a misspelled tool name must stop the server, not be ignored")
	}
	for _, want := range []string{"DROIDMCP_TERMUX_DISABLED_TOOLS", "run_comand", "has: echo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
	// Nothing is removed when the list is rejected.
	if ds.MCPServer.GetTool("echo") == nil {
		t.Error("echo was removed although the list was rejected")
	}
}
