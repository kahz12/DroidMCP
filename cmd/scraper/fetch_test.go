package main

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchCacheRespectsLimitsAndWaitSelector(t *testing.T) {
	var calls atomic.Int32
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Write([]byte("<p>loading</p>"))
		} else {
			w.Write([]byte("<p id='ready'>done</p>"))
		}
	}))
	opts := fetchOptions{URL: srv.URL}
	first, err := fetch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	first.Body[0] = 'X'
	again, err := fetch(context.Background(), opts)
	if err != nil || again.Body[0] != '<' {
		t.Fatalf("cache mutated: %v %v", again, err)
	}
	opts.MaxBodyBytes = 1
	if _, err := fetch(context.Background(), opts); err == nil {
		t.Fatal("cache bypassed body cap")
	}
	opts.MaxBodyBytes = 0
	opts.WaitSelector = "#ready"
	ready, err := fetch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if ready.FromCache || !selectorMatches(ready.Body, "#ready") || calls.Load() != 2 {
		t.Fatalf("wait_selector returned stale cache: %+v", ready)
	}
}

// The copy handed to callers must not share header maps with the cache entry,
// either for the response that filled the cache or for a later hit.
func TestFetchCacheCopiesHeaders(t *testing.T) {
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Origin", "server")
		w.Write([]byte("ok"))
	}))
	opts := fetchOptions{URL: srv.URL}
	first, err := fetch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	first.Header["X-Origin"][0] = "mutated-first"
	first.Header["X-Added"] = []string{"first"}

	hit, err := fetch(context.Background(), opts)
	if err != nil || !hit.FromCache {
		t.Fatalf("expected a cache hit: %+v %v", hit, err)
	}
	hit.Header["X-Origin"][0] = "mutated-hit"

	again, err := fetch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Header["X-Origin"]; len(got) != 1 || got[0] != "server" {
		t.Fatalf("cached header mutated: X-Origin = %v", got)
	}
	if _, ok := again.Header["X-Added"]; ok {
		t.Fatal("header added by a caller leaked into the cache")
	}
}

// Headers are attacker-controlled too, so they count against the cache budget.
func TestCacheBudgetCountsHeaders(t *testing.T) {
	c := newLRUCache(10, time.Minute)
	c.maxBytes = 100
	big := cachedResponse{Body: []byte("x"), Header: map[string][]string{"X-Pad": {strings.Repeat("a", 200)}}}
	c.Set("big", &big)
	if c.Len() != 0 || c.bytes != 0 {
		t.Fatalf("entry over budget was kept: entries=%d bytes=%d", c.Len(), c.bytes)
	}
	small := cachedResponse{Body: []byte("x"), Header: map[string][]string{"K": {"vv"}}}
	c.Set("small", &small)
	if want := len("x") + len("K") + len("vv"); c.bytes != want {
		t.Fatalf("bytes = %d, want %d", c.bytes, want)
	}
}

func TestSafeTransportCapsResponseHeaders(t *testing.T) {
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Pad", strings.Repeat("a", 2*maxResponseHeaderBytes))
		w.Write([]byte("ok"))
	}))
	if _, err := fetch(context.Background(), fetchOptions{URL: srv.URL}); err == nil {
		t.Fatal("accepted a response header block over the cap")
	}
}

func TestParseFetchOptionsClampsWaitInterval(t *testing.T) {
	for _, ms := range []float64{86400000, 9.2e12, 9.3e12, 1e15} {
		opts, err := parseFetchOptions(callRequest(map[string]any{"url": "http://example.com", "wait_interval_ms": ms}))
		if err != nil {
			t.Fatal(err)
		}
		if opts.WaitInterval < 0 || opts.WaitInterval > maxWaitInterval {
			t.Errorf("wait_interval_ms=%g gave %v, want within (0, %v]", ms, opts.WaitInterval, maxWaitInterval)
		}
	}
	opts, _ := parseFetchOptions(callRequest(map[string]any{"url": "http://example.com", "wait_interval_ms": 250}))
	if opts.WaitInterval != 250*time.Millisecond {
		t.Errorf("wait_interval_ms=250 gave %v", opts.WaitInterval)
	}
}

// fetchOptions built in code (not from a tool call) are clamped by normalize.
func TestNormalizeClampsWaitInterval(t *testing.T) {
	o := fetchOptions{URL: "http://example.com", WaitSelector: "#ready", WaitInterval: time.Hour}
	o.normalize()
	if o.WaitInterval != maxWaitInterval {
		t.Fatalf("WaitInterval = %v, want the %v cap", o.WaitInterval, maxWaitInterval)
	}
}

// A cached page that does not satisfy wait_selector is not served while the
// network works, but it is still better than an error when the network fails.
func TestFetchFallsBackToCachedPageWhenSelectorRetriesFail(t *testing.T) {
	var down atomic.Bool
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			panic(http.ErrAbortHandler)
		}
		w.Write([]byte("<p>loading</p>"))
	}))
	if _, err := fetch(context.Background(), fetchOptions{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	down.Store(true)
	got, err := fetch(context.Background(), fetchOptions{
		URL: srv.URL, WaitSelector: "#ready", WaitAttempts: 2, WaitInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("selector fetch did not fall back to the cached page: %v", err)
	}
	if !got.FromCache || !strings.Contains(string(got.Body), "loading") {
		t.Fatalf("unexpected fallback response: %+v", got)
	}
}
