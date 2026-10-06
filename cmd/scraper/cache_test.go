package main

import (
	"testing"
	"time"
)

func TestLRUCacheGetSet(t *testing.T) {
	c := newLRUCache(2, time.Minute)
	c.Set("a", &cachedResponse{Body: []byte("A")})
	c.Set("b", &cachedResponse{Body: []byte("B")})
	if v, ok := c.Get("a"); !ok || string(v.Body) != "A" {
		t.Fatalf("expected hit on a, got %v %v", ok, v)
	}
	// Adding c with cap=2 evicts the LRU. After Get("a") above, b is LRU.
	c.Set("c", &cachedResponse{Body: []byte("C")})
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b to be evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to survive (most recently used)")
	}
}

func TestLRUCacheTTL(t *testing.T) {
	c := newLRUCache(4, 10*time.Millisecond)
	now := time.Now()
	c.clock = func() time.Time { return now }
	c.Set("a", &cachedResponse{Body: []byte("A")})

	now = now.Add(20 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a to be expired")
	}
}

// An entry is valid strictly before expiresAt; at the TTL instant it is stale.
func TestLRUCacheExpiresAtExactTTL(t *testing.T) {
	c := newLRUCache(4, 10*time.Second)
	start := time.Now()
	now := start
	c.clock = func() time.Time { return now }
	c.Set("a", &cachedResponse{Body: []byte("A")})

	now = start.Add(10*time.Second - time.Nanosecond)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry expired before its TTL")
	}
	now = start.Add(10 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry still served at the exact TTL instant")
	}
	if c.Len() != 0 || c.bytes != 0 {
		t.Fatalf("expired entry not released: entries=%d bytes=%d", c.Len(), c.bytes)
	}
}

func TestLRUCacheUpdate(t *testing.T) {
	c := newLRUCache(2, time.Minute)
	c.Set("a", &cachedResponse{Body: []byte("A")})
	c.Set("a", &cachedResponse{Body: []byte("A2")})
	if v, _ := c.Get("a"); string(v.Body) != "A2" {
		t.Fatalf("expected updated value, got %s", v.Body)
	}
	if c.Len() != 1 {
		t.Fatalf("expected len 1 after update, got %d", c.Len())
	}
}

func TestLRUCacheBodyBudget(t *testing.T) {
	c := newLRUCache(64, time.Minute)
	c.maxBytes = 5
	c.Set("a", &cachedResponse{Body: []byte("123")})
	c.Set("b", &cachedResponse{Body: []byte("456")})
	if _, ok := c.Get("a"); ok {
		t.Fatal("byte budget did not evict oldest body")
	}
	c.Set("b", &cachedResponse{Body: []byte("1")})
	c.Set("c", &cachedResponse{Body: []byte("2345")})
	if c.Len() != 2 || c.bytes != 5 {
		t.Fatalf("incorrect replacement accounting: entries=%d bytes=%d", c.Len(), c.bytes)
	}
	c.Set("c", &cachedResponse{Body: []byte("123456")})
	if _, ok := c.Get("c"); ok {
		t.Fatal("oversized entry retained")
	}
	if c.bytes != 1 {
		t.Fatalf("incorrect eviction accounting: %d", c.bytes)
	}
}
