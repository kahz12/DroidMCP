// JSON transport shared by every tool handler. Ollama's API is a handful of
// small JSON endpoints, so this stays deliberately thin: marshal, send, cap the
// read, decode, and translate failures into something an agent can act on
// instead of a raw net error.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// maxResponseBytes caps what we read back. Embedding vectors and verbose
// /api/show payloads are the large ones; past this it is a runaway response
// rather than a useful answer.
const maxResponseBytes = 32 << 20 // 32 MiB

const dialTimeout = 5 * time.Second

type client struct {
	base *url.URL
	http *http.Client

	// maxBytes is the response cap, kept per client so tests can shrink it
	// without allocating 32 MiB.
	maxBytes int64
}

// checkDialAddress is the net.Dialer.Control hook: it sees the concrete
// post-resolution address the socket is about to reach, so it closes the window
// between the startup check on the configured host and the address actually
// dialed. That matters when DROIDMCP_OLLAMA_HOST is a name rather than a
// literal, since the transport re-resolves it on every dial.
func checkDialAddress(address string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("dial address %q is not an IP literal", address)
	}
	if !isLocalIP(ip) {
		return fmt.Errorf("%w: refusing to dial %s; set %s=1 to send prompts off-device",
			errRemoteHost, ip, envAllowRemote)
	}
	return nil
}

func newClient(base *url.URL, allowRemote bool) *client {
	dialer := &net.Dialer{
		Timeout: dialTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address, allowRemote)
		},
	}
	return &client{
		base:     base,
		maxBytes: maxResponseBytes,
		// No client-level timeout: every call carries its own context deadline,
		// because a generate on a phone can legitimately run for minutes while a
		// list_models should give up in seconds.
		http: &http.Client{
			// Ollama's API never redirects, and following one would undo the whole
			// point of pinning the destination: Go replays the request body on a
			// 307, so a daemon that answers with a Location header could bounce
			// every prompt to a host nothing here ever vetted.
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return fmt.Errorf("refusing to follow a redirect to %s; the ollama address is fixed by %s",
					req.URL.Host, envOllamaHost)
			},
			Transport: &http.Transport{
				// Deliberately no proxy. The daemon is local, and honouring
				// HTTP_PROXY would route prompts through a hop that neither
				// validateBase nor the dial guard ever inspected.
				Proxy:               nil,
				DialContext:         dialer.DialContext,
				MaxIdleConns:        4,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// get issues a GET against the Ollama API and decodes the JSON body into out.
// A nil out discards the body.
func (c *client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// post issues a POST with a JSON body and decodes the JSON response into out.
func (c *client) post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return c.transportError(ctx, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return c.transportError(ctx, err)
	}
	if int64(len(raw)) > c.maxBytes {
		return fmt.Errorf("ollama response exceeded the %d byte cap", c.maxBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.statusError(resp.StatusCode, path, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ollama at %s returned a body that is not valid JSON: %w", c.base, err)
	}
	return nil
}

// transportError turns a dial, read or deadline failure into something a
// calling agent can act on. "connection refused" almost always means the daemon
// is not running, and saying so beats surfacing the raw dial error.
func (c *client) transportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("ollama at %s did not answer in time; raise timeout_seconds or use a smaller model", c.base)
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("request to ollama at %s was cancelled", c.base)
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return fmt.Errorf("cannot reach ollama at %s (%v). Is `ollama serve` running? Point DROIDMCP_OLLAMA_HOST at it if it listens elsewhere", c.base, netErr.Err)
	}
	return fmt.Errorf("request to ollama at %s failed: %w", c.base, err)
}

// statusError surfaces Ollama's own error text when it sends one, since that is
// the actionable part: unknown model, bad option, out of memory.
func (c *client) statusError(status int, path string, raw []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	detail := strings.TrimSpace(string(raw))
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Error != "" {
		detail = payload.Error
	}
	if detail == "" {
		detail = http.StatusText(status)
	}
	if status == http.StatusNotFound && strings.Contains(strings.ToLower(detail), "model") {
		return fmt.Errorf("ollama: %s. Call list_models to see what is installed, or pull it with `ollama pull`", detail)
	}
	return fmt.Errorf("ollama %s returned %d: %s", path, status, detail)
}
