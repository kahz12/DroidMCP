package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultExecTimeout = 15 * time.Second
	maxOutputBytes     = 1 << 20 // 1 MiB per stream

	binClipboardGet = "termux-clipboard-get"
	binClipboardSet = "termux-clipboard-set"

	missingTermuxAPIHint = "termux-api package not installed; run `pkg install termux-api` and ensure the Termux:API app is installed on the device"
)

// lookPath is overridable in tests.
var lookPath = exec.LookPath

// ensureBinaries returns a clear error when the required termux-api wrappers
// are absent, so users get a useful hint instead of the bare "no such file
// or directory" from exec.
func ensureBinaries(names ...string) error {
	for _, n := range names {
		if _, err := lookPath(n); err != nil {
			return fmt.Errorf("%s: %s", n, missingTermuxAPIHint)
		}
	}
	return nil
}

// clipboardResult is the captured outcome of running a termux-clipboard-*
// binary. Stdout/Stderr are UTF-8-safe (replacement-char on invalid bytes);
// RawStdout keeps the unmodified stdout (subject to the size cap) so the
// caller can render binary content as base64.
type clipboardResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Cancelled  bool   `json:"cancelled,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	RawStdout  []byte `json:"-"`
}

// runClipboardCmd invokes a termux-clipboard-* binary, capturing stdout and
// stderr separately into capped buffers (never CombinedOutput, so the two
// streams stay distinguishable) and applying a per-call timeout.
func runClipboardCmd(ctx context.Context, name string, args []string, stdin []byte) (*clipboardResult, error) {
	cctx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	stdout := &cappedBuffer{max: maxOutputBytes}
	stderr := &cappedBuffer{max: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	start := time.Now()
	waitErr := cmd.Run()
	raw := stdout.Bytes()
	res := &clipboardResult{
		DurationMs: time.Since(start).Milliseconds(),
		Stdout:     safeUTF8(raw),
		Stderr:     safeUTF8(stderr.Bytes()),
		Truncated:  stdout.truncated || stderr.truncated,
		RawStdout:  raw,
	}
	if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		res.TimedOut = true
	} else if ctx.Err() != nil {
		res.Cancelled = true
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			if res.Stderr != "" {
				res.Stderr += "\n"
			}
			res.Stderr += waitErr.Error()
		}
	}
	return res, nil
}

// cappedBuffer drops bytes past max and records the overflow, so a runaway
// child cannot exhaust memory.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int64
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rem := c.max - int64(len(c.buf))
	if rem <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > rem {
		c.buf = append(c.buf, p[:rem]...)
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

// safeUTF8 returns b verbatim when it is valid UTF-8; otherwise it replaces
// invalid bytes with U+FFFD so the result is safe to embed in JSON.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune('�')
			i++
			continue
		}
		sb.WriteRune(r)
		i += size
	}
	return sb.String()
}
