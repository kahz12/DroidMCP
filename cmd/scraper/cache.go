// Tiny in-process LRU for fetch responses. Same-session repeats (LLM
// re-asking for the same page) are cheap; new URLs evict the oldest entry
// once we hit the cap. Entries also have a TTL so a long-lived process does
// not return week-old HTML.
package main

import (
	"container/list"
	"net/http"
	"sync"
	"time"
)

const (
	cacheDefaultMax = 64
	cacheDefaultTTL = 5 * time.Minute
	cacheMaxBytes   = 32 << 20 // Bound retained response bodies on mobile devices.
)

// cachedResponse is exactly what a handler needs to skip the network round
// trip on the next call: the bytes, the status code, the response headers,
// and the final URL after redirects.
type cachedResponse struct {
	URL       string
	Status    int
	Header    map[string][]string
	Body      []byte
	FetchedAt time.Time
	FromCache bool
}

// size is what the entry costs against the cache's byte budget: the body plus
// the header names and values. Headers count because a hostile server controls
// both, and a small body with megabytes of headers would otherwise defeat the
// budget (newSafeTransport also caps the header block it will accept).
func (r *cachedResponse) size() int {
	n := len(r.Body)
	for k, vs := range r.Header {
		n += len(k)
		for _, v := range vs {
			n += len(v)
		}
	}
	return n
}

// clone returns a deep copy so a caller can keep or mutate the result without
// touching the entry the cache still holds.
func (r *cachedResponse) clone() *cachedResponse {
	out := *r
	out.Body = append([]byte(nil), r.Body...)
	out.Header = http.Header(r.Header).Clone()
	return &out
}

type lruEntry struct {
	key       string
	val       *cachedResponse
	size      int
	expiresAt time.Time
}

type lruCache struct {
	mu       sync.Mutex
	max      int
	bytes    int
	maxBytes int
	ttl      time.Duration
	ll       *list.List
	items    map[string]*list.Element
	clock    func() time.Time
}

func newLRUCache(max int, ttl time.Duration) *lruCache {
	if max <= 0 {
		max = cacheDefaultMax
	}
	if ttl <= 0 {
		ttl = cacheDefaultTTL
	}
	return &lruCache{
		max:      max,
		maxBytes: cacheMaxBytes,
		ttl:      ttl,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		clock:    time.Now,
	}
}

// removeElement unlinks el and releases its share of the byte budget. The
// caller holds c.mu.
func (c *lruCache) removeElement(el *list.Element) {
	e := el.Value.(*lruEntry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= e.size
}

func (c *lruCache) Get(key string) (*cachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*lruEntry)
	if !c.clock().Before(e.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *lruCache) Set(key string, val *cachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
	size := val.size()
	if size > c.maxBytes {
		return
	}
	e := &lruEntry{key: key, val: val, size: size, expiresAt: c.clock().Add(c.ttl)}
	c.items[key] = c.ll.PushFront(e)
	c.bytes += size
	for c.ll.Len() > c.max || c.bytes > c.maxBytes {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest)
	}
}

func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
