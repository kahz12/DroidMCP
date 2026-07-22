package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultExecTimeout = 15 * time.Second
	maxExecTimeout     = 120 * time.Second
	maxOutputBytes     = 1 << 20 // 1 MiB per stream

	binSMSList = "termux-sms-list"
	binSMSSend = "termux-sms-send"

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

// cmdResult is the captured outcome of running a backend command.
type cmdResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Cancelled  bool   `json:"cancelled,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// runCmd invokes a backend command, capturing stdout and stderr separately
// into capped buffers and applying the per-call timeout. When stdin is
// non-empty it is written to the child's standard input — this is how send_sms
// passes the message body, so caller text is never placed on the argument list.
func runCmd(ctx context.Context, name string, args []string, stdin string, timeout time.Duration) (*cmdResult, error) {
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	if timeout > maxExecTimeout {
		timeout = maxExecTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdout := &cappedBuffer{max: maxOutputBytes}
	stderr := &cappedBuffer{max: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// SIGTERM on cancel, and stop waiting for inherited pipe fds shortly after:
	// without WaitDelay a child that outlives the wrapper (or a helper it
	// forked) would keep Run blocked past the timeout.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	waitErr := cmd.Run()
	res := &cmdResult{
		DurationMs: time.Since(start).Milliseconds(),
		Stdout:     safeUTF8(stdout.Bytes()),
		Stderr:     safeUTF8(stderr.Bytes()),
		Truncated:  stdout.truncated || stderr.truncated,
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

// reqTimeout reads the optional timeout_seconds argument, clamped to
// (0, maxExecTimeout].
func reqTimeout(req mcp.CallToolRequest, def time.Duration) time.Duration {
	secs := req.GetInt("timeout_seconds", 0)
	if secs <= 0 {
		return def
	}
	d := time.Duration(secs) * time.Second
	if d > maxExecTimeout {
		return maxExecTimeout
	}
	return d
}

// cappedBuffer drops bytes past max and records the overflow, so a runaway
// child cannot exhaust memory.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int64
	truncated bool
}

// compile-time assertion that cappedBuffer satisfies io.Writer.
var _ io.Writer = (*cappedBuffer)(nil)

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
