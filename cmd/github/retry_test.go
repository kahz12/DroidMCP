package main

import (
	"context"
	"errors"
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
	// The body of the final response must still be readable: callers
	// (go-github's CheckResponse) rely on it to surface GitHub's error detail.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("final response body must be readable, got err: %v", err)
	}
	if string(body) != "body" {
		t.Fatalf("expected intact body %q, got %q", "body", string(body))
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

// An HTTP-date Retry-After yields the time left until that date, and a date in
// the past gives no hint. A date far ahead passes through here and is capped by
// backoffFor.
func TestParseRetryAfterHTTPDate(t *testing.T) {
	soon := time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(soon); d <= 10*time.Second || d > 20*time.Second {
		t.Errorf("parseRetryAfter(%q) = %v, want about 20s", soon, d)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(past); d != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, want 0", d)
	}
	far := time.Now().AddDate(1, 0, 0).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(far)
	if d <= retryMaxDelay {
		t.Fatalf("parseRetryAfter(a year ahead) = %v, want more than %v", d, retryMaxDelay)
	}
	if got := backoffFor(0, d); got != retryMaxDelay {
		t.Errorf("backoffFor(a year) = %v, want the %v cap", got, retryMaxDelay)
	}
}

func TestBackoffFor(t *testing.T) {
	if got := backoffFor(0, 5*time.Second); got != 5*time.Second {
		t.Errorf("Retry-After of 5s gave %v", got)
	}
	jitter := time.Duration(retryMaxJitterMS) * time.Millisecond
	for _, tc := range []struct {
		attempt int
		base    time.Duration
	}{
		{0, retryBaseDelay},
		{1, 2 * retryBaseDelay},
		{2, 4 * retryBaseDelay},
		{10, retryMaxDelay}, // 1024 x base is past the cap
	} {
		got := backoffFor(tc.attempt, 0)
		if got < tc.base || got >= tc.base+jitter {
			t.Errorf("backoffFor(%d, 0) = %v, want in [%v, %v)", tc.attempt, got, tc.base, tc.base+jitter)
		}
	}
}

func TestNewRetryTransportDefaultsBase(t *testing.T) {
	if got := newRetryTransport(nil).base; got != http.DefaultTransport {
		t.Fatalf("nil base = %v, want http.DefaultTransport", got)
	}
}

func TestRetryTransportRetriesNetworkError(t *testing.T) {
	rt := &fakeRT{err: errors.New("connection reset")}
	transport_setNoSleep(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test/x", nil)
	resp, err := newRetryTransport(rt).RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.StatusCode != http.StatusOK || rt.calls.Load() != 2 {
		t.Fatalf("got status %d after %d attempts, want 200 after 2", resp.StatusCode, rt.calls.Load())
	}
}

// A cancelled request must stop waiting out the backoff instead of sleeping
// for the server's Retry-After.
func TestRetryTransportStopsWhenCancelledDuringBackoff(t *testing.T) {
	rt := &fakeRT{statuses: []int{http.StatusTooManyRequests, http.StatusOK}} // Retry-After: 1
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/x", nil)
	start := time.Now()
	resp, err := newRetryTransport(rt).RoundTrip(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the retry to be aborted")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap the context error", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("waited %v, the full Retry-After, despite the cancellation", elapsed)
	}
	if got := rt.calls.Load(); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
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

// A huge Retry-After must hit the cap, not wrap time.Duration into a tiny or
// negative delay.
func TestParseRetryAfterClampsHugeValues(t *testing.T) {
	for _, v := range []string{"86400", "18446744074", "9223372036"} {
		if d := parseRetryAfter(v); d != retryMaxDelay {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", v, d, retryMaxDelay)
		}
	}
	if d := parseRetryAfter("30"); d != 30*time.Second {
		t.Errorf("parseRetryAfter(30) = %v", d)
	}
}
