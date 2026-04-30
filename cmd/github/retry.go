// Retry transport: wraps an underlying RoundTripper to retry transient
// failures (5xx, 429 with Retry-After) using exponential backoff. Secondary
// rate-limit errors that the go-github client surfaces as
// *github.AbuseRateLimitError are handled at the call site (the body has
// already been consumed by then), so this layer focuses on wire-level retries.

package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	retryMaxAttempts = 3
	retryMaxDelay    = 30 * time.Second
	retryMaxJitterMS = 250
	retryAfterHeader = "Retry-After"
)

// retryBaseDelay is a var (not const) so tests can shrink it without waiting
// the full backoff window. Production callers should leave it alone.
var retryBaseDelay = 1 * time.Second

// retryTransport retries idempotent (GET/HEAD) requests on 5xx and 429.
// Mutating verbs (POST/PUT/PATCH/DELETE) are NOT retried automatically because
// GitHub may have applied the side effect even when the transport reports a
// network error; doing the same call twice could create duplicate issues or
// commits. The caller can still observe the failure and decide.
type retryTransport struct {
	base http.RoundTripper
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{base: base}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isRetriable(req) {
		return t.base.RoundTrip(req)
	}

	var lastResp *http.Response
	var lastErr error
	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
		resp, err := t.base.RoundTrip(req)
		lastResp, lastErr = resp, err

		// Network-level error: keep retrying until attempts exhausted.
		if err != nil {
			if !sleepWithCtx(req.Context(), backoffFor(attempt, 0)) {
				return nil, err
			}
			continue
		}
		// 5xx and 429 are retriable; everything else (2xx/3xx/4xx) is final.
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		// Drain and close the body so the connection can be reused on retry.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		retryAfter := parseRetryAfter(resp.Header.Get(retryAfterHeader))
		if !sleepWithCtx(req.Context(), backoffFor(attempt, retryAfter)) {
			return nil, fmt.Errorf("retry aborted: %w", req.Context().Err())
		}
	}
	return lastResp, lastErr
}

func isRetriable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return true
	default:
		return false
	}
}

// backoffFor returns the delay before the next attempt. If the server provided
// a Retry-After value we honor it (capped at retryMaxDelay); otherwise we fall
// back to exponential backoff with jitter.
func backoffFor(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > retryMaxDelay {
			return retryMaxDelay
		}
		return retryAfter
	}
	d := retryBaseDelay << attempt
	if d > retryMaxDelay {
		d = retryMaxDelay
	}
	jitter := time.Duration(rand.Intn(retryMaxJitterMS)) * time.Millisecond
	return d + jitter
}

// parseRetryAfter accepts either a delta-seconds value or an HTTP-date.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepWithCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
