// HTTP fetch pipeline used by every scraper tool. Wraps the SSRF-safe
// transport in an http.Client, applies per-request limits (timeout, max body,
// custom headers / user agent), and optionally retries until a wait_selector
// matches. The LRU cache fronts the whole thing so repeated calls in the same
// session do not re-hit the network.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	defaultUserAgent      = "DroidMCP-Scraper/1.0 (+https://github.com/kahz12/droidmcp)"
	defaultRequestTimeout = 20 * time.Second
	defaultMaxBodyBytes   = 10 * 1024 * 1024 // 10 MiB
	maxWaitAttempts       = 10
	defaultWaitInterval   = 1 * time.Second
	maxWaitInterval       = 30 * time.Second
)

// fetchOptions is the per-call surface every handler can populate from MCP
// arguments. Anything here that is left zero falls back to a sane default.
type fetchOptions struct {
	URL          string
	Method       string
	UserAgent    string
	Headers      map[string]string
	Timeout      time.Duration
	MaxBodyBytes int64
	NoCache      bool

	// WaitSelector, if set, makes fetch retry until the response body matches
	// the CSS selector or until WaitAttempts is exhausted. This is a poor
	// substitute for a JS engine but covers the "the page returns an empty
	// shell on first GET" case (server-rendered + lazy data fetch).
	WaitSelector string
	WaitAttempts int
	WaitInterval time.Duration
}

func (o *fetchOptions) normalize() {
	if o.Method == "" {
		o.Method = http.MethodGet
	}
	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultRequestTimeout
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = defaultMaxBodyBytes
	}
	if o.WaitSelector != "" {
		if o.WaitAttempts <= 0 {
			o.WaitAttempts = 3
		}
		if o.WaitAttempts > maxWaitAttempts {
			o.WaitAttempts = maxWaitAttempts
		}
		if o.WaitInterval <= 0 {
			o.WaitInterval = defaultWaitInterval
		}
		if o.WaitInterval > maxWaitInterval {
			o.WaitInterval = maxWaitInterval
		}
	}
}

// safeClient is the package-wide http.Client. It uses the SSRF transport and
// re-validates every redirect target. Built once at init() so each fetch is
// just a Do() call.
var safeClient = &http.Client{
	Transport:     newSafeTransport(),
	CheckRedirect: safeCheckRedirect,
}

// fetchCache is the in-process LRU. It is a package var so tests can swap it
// for a fresh cache instead of bleeding state between cases.
var fetchCache = newLRUCache(cacheDefaultMax, cacheDefaultTTL)

// fetch runs the SSRF guard, executes the HTTP request, enforces the body cap,
// and retries until WaitSelector matches if requested. The returned
// cachedResponse is also what we store in the cache.
func fetch(ctx context.Context, opts fetchOptions) (*cachedResponse, error) {
	opts.normalize()
	if err := validateURL(opts.URL); err != nil {
		return nil, err
	}

	key := cacheKey(opts)
	// stale is a cached page that does not satisfy WaitSelector. It is not served
	// (the page may have finished loading since), but it is what a failing
	// network falls back to, as a plain fetch of the same URL would.
	var stale *cachedResponse
	if !opts.NoCache {
		if hit, ok := fetchCache.Get(key); ok {
			if int64(len(hit.Body)) > opts.MaxBodyBytes {
				return nil, fmt.Errorf("response exceeded max body size of %d bytes", opts.MaxBodyBytes)
			}
			if opts.WaitSelector == "" || selectorMatches(hit.Body, opts.WaitSelector) {
				// Defensive copy so callers cannot mutate the cached entry.
				out := hit.clone()
				out.FromCache = true
				return out, nil
			}
			stale = hit
		}
	}

	attempts := 1
	if opts.WaitSelector != "" {
		attempts = opts.WaitAttempts
	}

	var last *cachedResponse
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if !sleepWithCtx(ctx, opts.WaitInterval) {
				return nil, ctx.Err()
			}
		}
		resp, err := doRequest(ctx, opts)
		if err != nil {
			lastErr = err
			continue
		}
		last = resp
		lastErr = nil
		if opts.WaitSelector == "" || selectorMatches(resp.Body, opts.WaitSelector) {
			break
		}
	}
	if last == nil {
		if stale != nil {
			out := stale.clone()
			out.FromCache = true
			return out, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("fetch produced no response")
	}
	if !opts.NoCache {
		fetchCache.Set(key, last.clone())
	}
	return last, nil
}

func doRequest(ctx context.Context, opts fetchOptions) (*cachedResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, opts.Method, opts.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, opts.MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > opts.MaxBodyBytes {
		return nil, fmt.Errorf("response exceeded max body size of %d bytes", opts.MaxBodyBytes)
	}

	return &cachedResponse{
		URL:       resp.Request.URL.String(),
		Status:    resp.StatusCode,
		Header:    resp.Header,
		Body:      body,
		FetchedAt: time.Now(),
	}, nil
}

// selectorMatches reports whether the supplied CSS selector resolves to at
// least one element in body. Errors (malformed HTML / bad selector) count as
// "no match" so the caller will keep waiting up to WaitAttempts.
func selectorMatches(body []byte, selector string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	return doc.Find(selector).Length() > 0
}

// cacheKey builds a stable hash for the opts that affect the response. URL +
// method + sorted headers + user agent are enough; the cache TTL handles
// volatility.
func cacheKey(opts fetchOptions) string {
	h := sha256.New()
	h.Write([]byte(opts.Method))
	h.Write([]byte("\x00"))
	h.Write([]byte(opts.URL))
	h.Write([]byte("\x00"))
	h.Write([]byte(opts.UserAgent))
	h.Write([]byte("\x00"))
	keys := make([]string, 0, len(opts.Headers))
	for k := range opts.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(strings.ToLower(k)))
		h.Write([]byte("="))
		h.Write([]byte(opts.Headers[k]))
		h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sleepWithCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
