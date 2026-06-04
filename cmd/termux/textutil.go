package main

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// cappedBuffer is an io.Writer that drops bytes past max and records the
// overflow. Using one instead of bytes.Buffer keeps a runaway child from
// exhausting memory.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int64
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.max - int64(len(c.buf))
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		c.buf = append(c.buf, p[:remaining]...)
		c.truncated = true
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, len(c.buf))
	copy(out, c.buf)
	return out
}

// safeUTF8 returns b verbatim when it is already valid UTF-8 (the common
// case). Otherwise it replaces invalid bytes with U+FFFD so the result is
// safe to pass through mcp.NewToolResultText / JSON encoding (audit 2.6).
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			out.WriteRune('�')
			i++
			continue
		}
		out.WriteRune(r)
		i += size
	}
	return out.String()
}
