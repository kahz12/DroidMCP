package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	// Downstream handler that should NOT be reached without a valid key.
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
