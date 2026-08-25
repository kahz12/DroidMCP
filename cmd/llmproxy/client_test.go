package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestClient points a client at a stub Ollama, with the off-device guard
// active as it is in production.
func newTestClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	return newClient(base, false)
}

// A redirect is the hole in a "the destination is fixed by the operator" guard:
// Go replays the whole body on a 307, so a LAN daemon could bounce every prompt
// to a public collector. Ollama's API never redirects, so none are followed.
func TestClientRefusesToFollowRedirects(t *testing.T) {
	var stolen string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stolen = string(body)
	}))
	t.Cleanup(collector.Close)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, collector.URL+"/steal", http.StatusTemporaryRedirect)
	})

	err := c.post(context.Background(), "/api/generate", map[string]any{"prompt": "user secret"}, nil)
	if err == nil {
		t.Fatal("post through a redirect returned no error")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %q, want it to name the refused redirect", err)
	}
	if stolen != "" {
		t.Errorf("the redirect target received %q; the prompt must never leave the configured host", stolen)
	}
}

// The startup check resolves the host once; the transport re-resolves on every
// dial. Without a check at dial time a hostname whose answer changes sends
// prompts off-device with no error, which is the gap cmd/scraper closes the
// same way.
func TestCheckDialAddress(t *testing.T) {
	cases := []struct {
		name        string
		address     string
		allowRemote bool
		wantErr     bool
	}{
		{"loopback is fine", "127.0.0.1:11434", false, false},
		{"ipv6 loopback is fine", "[::1]:11434", false, false},
		{"lan address is fine", "192.168.1.40:11434", false, false},
		{"cgnat is fine", "100.64.3.9:11434", false, false},
		{"public is refused", "198.51.100.7:11434", false, true},
		{"public is allowed when opted in", "198.51.100.7:11434", true, false},
		{"ipv4-mapped public is refused", "[::ffff:198.51.100.7]:11434", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDialAddress(tc.address, tc.allowRemote)
			if tc.wantErr && !errors.Is(err, errRemoteHost) {
				t.Errorf("checkDialAddress(%q, %v) = %v, want errRemoteHost", tc.address, tc.allowRemote, err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("checkDialAddress(%q, %v) = %v, want nil", tc.address, tc.allowRemote, err)
			}
		})
	}
}

// The dial guard must not break the normal loopback case it wraps.
func TestClientReachesLoopbackWithTheDialGuardOn(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	})

	var out map[string]any
	if err := c.get(context.Background(), "/api/tags", &out); err != nil {
		t.Fatalf("get against loopback returned error: %v", err)
	}
}

func TestClientPostSendsJSONBodyAndDecodesResponse(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotBody map[string]any

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":"hola","done_reason":"stop"}`)
	})

	var out struct {
		Response   string `json:"response"`
		DoneReason string `json:"done_reason"`
	}
	if err := c.post(context.Background(), "/api/generate", map[string]any{"model": "qwen"}, &out); err != nil {
		t.Fatalf("post returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/generate" {
		t.Errorf("path = %q, want /api/generate", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody["model"] != "qwen" {
		t.Errorf("request body model = %v, want qwen", gotBody["model"])
	}
	if out.Response != "hola" {
		t.Errorf("decoded response = %q, want hola", out.Response)
	}
}

func TestClientGetDecodesResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen:0.5b"}]}`)
	})

	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.get(context.Background(), "/api/tags", &out); err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if len(out.Models) != 1 || out.Models[0].Name != "qwen:0.5b" {
		t.Errorf("decoded models = %+v, want one entry named qwen:0.5b", out.Models)
	}
}

func TestClientSurfacesOllamaErrorField(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model \"ghost\" not found"}`)
	})

	err := c.post(context.Background(), "/api/generate", map[string]any{}, nil)
	if err == nil {
		t.Fatal("post on a 404 returned no error")
	}
	if !strings.Contains(err.Error(), `model "ghost" not found`) {
		t.Errorf("error = %q, want it to carry ollama's own message", err)
	}
	if !strings.Contains(err.Error(), "list_models") {
		t.Errorf("error = %q, want a hint pointing at list_models", err)
	}
}

func TestClientReportsNon2xxWithoutErrorField(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	})

	err := c.get(context.Background(), "/api/tags", nil)
	if err == nil {
		t.Fatal("get on a 500 returned no error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to name the status and the body", err)
	}
}

func TestClientRejectsMalformedJSON(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>not json</html>")
	})

	var out map[string]any
	err := c.get(context.Background(), "/api/tags", &out)
	if err == nil {
		t.Fatal("get on a non-JSON body returned no error")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error = %q, want it to say the body is not valid JSON", err)
	}
}

func TestClientTimeoutSuggestsRaisingIt(t *testing.T) {
	// The handler stalls until the test is done. Releasing it explicitly (rather
	// than waiting on the request context) keeps httptest's Close from blocking:
	// cleanups run last-registered-first, so this fires before the server stops.
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.post(ctx, "/api/generate", map[string]any{}, nil)
	if err == nil {
		t.Fatal("post past the deadline returned no error")
	}
	if !strings.Contains(err.Error(), "timeout_seconds") {
		t.Errorf("error = %q, want it to point at timeout_seconds", err)
	}
}

func TestClientUnreachableDaemonSuggestsOllamaServe(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	srv.Close() // nothing is listening on that port any more

	c := newClient(base, false)
	err = c.get(context.Background(), "/api/tags", nil)
	if err == nil {
		t.Fatal("get against a closed port returned no error")
	}
	if !strings.Contains(err.Error(), "ollama serve") {
		t.Errorf("error = %q, want it to suggest starting ollama serve", err)
	}
	if !strings.Contains(err.Error(), "DROIDMCP_OLLAMA_HOST") {
		t.Errorf("error = %q, want it to name the host variable", err)
	}
}

func TestClientCapsOversizedResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
	})
	c.maxBytes = 1024

	var out map[string]any
	err := c.get(context.Background(), "/api/tags", &out)
	if err == nil {
		t.Fatal("get on an oversized body returned no error")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error = %q, want it to say the response exceeded the cap", err)
	}
}
