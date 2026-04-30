package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRT struct {
	calls    atomic.Int32
	statuses []int
	err      error
}

func (f *fakeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	idx := f.calls.Add(1) - 1
	if f.err != nil && int(idx) == 0 {
		return nil, f.err
	}
	status := http.StatusOK
	if int(idx) < len(f.statuses) {
		status = f.statuses[idx]
	}
	resp := &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("body")),
		Request:    r,
	}
	if status == http.StatusTooManyRequests {
		resp.Header.Set("Retry-After", "1")
	}
	return resp, nil
}

func TestRetryTransportRetriesGet5xxThenSucceeds(t *testing.T) {
	rt := &fakeRT{statuses: []int{500, 502, 200}}
	transport := newRetryTransport(rt)
	transport_setNoSleep(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test/x", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := rt.calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestRetryTransportDoesNotRetryPOST(t *testing.T) {
	rt := &fakeRT{statuses: []int{500, 200}}
	transport := newRetryTransport(rt)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.test/x", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("expected the 500 surfaced, got %d", resp.StatusCode)
	}
	if got := rt.calls.Load(); got != 1 {
		t.Fatalf("POST must not retry, got %d attempts", got)
	}
}

func TestRetryTransportGivesUpAfterMaxAttempts(t *testing.T) {
	rt := &fakeRT{statuses: []int{500, 500, 500}}
	transport := newRetryTransport(rt)
	transport_setNoSleep(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test/x", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("expected last 500, got %d", resp.StatusCode)
	}
	if got := rt.calls.Load(); got != retryMaxAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", retryMaxAttempts, got)
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Fatalf("expected 3s, got %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("expected zero, got %v", d)
	}
	if d := parseRetryAfter("not-a-number"); d != 0 {
		t.Fatalf("expected zero for garbage input, got %v", d)
	}
}

// transport_setNoSleep replaces the package backoff base with a tiny duration
// so the test suite stays fast. Restored on cleanup.
func transport_setNoSleep(t *testing.T) {
	t.Helper()
	orig := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = orig })
}
