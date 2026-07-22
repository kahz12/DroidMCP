package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// execOptions carries everything runCommand needs. Trusted=true skips the
// allowlist (used by the dedicated termux-* tool wrappers; the operator
// already opted into those by registering this server).
type execOptions struct {
	Command  string
	Args     []string
	Cwd      string
	EnvExtra map[string]string
	Stdin    string
	Timeout  time.Duration
	MaxBytes int64
	Trusted  bool
}

// execResult is the JSON wire format every tool returns. stdout and stderr
// are kept separate (rather than mixed via CombinedOutput) so callers can
// grep one without the other.
type execResult struct {
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	ExitCode   int      `json:"exit_code"`
	TimedOut   bool     `json:"timed_out,omitempty"`
	Cancelled  bool     `json:"cancelled,omitempty"`
	DurationMs int64    `json:"duration_ms"`
	Truncated  bool     `json:"truncated,omitempty"`
}

const (
	defaultExecTimeout = 30 * time.Second
	maxExecTimeout     = 5 * time.Minute
	defaultMaxBytes    = 1 << 20 // 1 MiB per stream
	allowlistEnv       = "DROIDMCP_TERMUX_ALLOWLIST"
)

// runCommand is the single execution path used by every handler in this
// server. It enforces the allowlist (unless opts.Trusted), applies the
// per-call timeout, captures stdout/stderr separately with byte caps, and
// SIGTERMs the child process group on context cancellation (panic button)
// before escalating to SIGKILL after a short grace period.
func runCommand(ctx context.Context, opts execOptions) (*execResult, error) {
	if strings.TrimSpace(opts.Command) == "" {
		return nil, errors.New("command is empty")
	}
	if !opts.Trusted {
		if err := allowlistCheck(opts.Command); err != nil {
			return nil, err
		}
		// A caller-supplied dynamic-linker override (LD_PRELOAD, LD_LIBRARY_PATH,
		// …) could turn an allowlisted, benign binary into arbitrary code
		// execution, so refuse those keys in env_extra. Operators that genuinely
		// need them can set them in the server's own environment, which the
		// child still inherits.
		if err := checkEnvExtra(opts.EnvExtra); err != nil {
			return nil, err
		}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultExecTimeout
	}
	if opts.Timeout > maxExecTimeout {
		opts.Timeout = maxExecTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}

	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, opts.Command, opts.Args...)
	if opts.Cwd != "" {
		info, err := os.Stat(opts.Cwd)
		if err != nil {
			return nil, fmt.Errorf("cwd %q: %w", opts.Cwd, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("cwd %q is not a directory", opts.Cwd)
		}
		cmd.Dir = opts.Cwd
	}
	if len(opts.EnvExtra) > 0 {
		env := os.Environ()
		for k, v := range opts.EnvExtra {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	stdout := &cappedBuffer{max: opts.MaxBytes}
	stderr := &cappedBuffer{max: opts.MaxBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Place the child in its own process group so signal-on-cancel reaches
	// any helpers it forked (e.g. shell pipelines).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Panic button: when ctx (or our timeout) fires, send SIGTERM to the
	// whole group and let the runtime SIGKILL after WaitDelay.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	waitErr := cmd.Run()
	elapsed := time.Since(start)

	res := &execResult{
		Command:    opts.Command,
		Args:       opts.Args,
		Stdout:     safeUTF8(stdout.Bytes()),
		Stderr:     safeUTF8(stderr.Bytes()),
		DurationMs: elapsed.Milliseconds(),
		Truncated:  stdout.truncated || stderr.truncated,
	}
	// Distinguish "we ran out of time" from "the parent cancelled us".
	if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		res.TimedOut = true
	} else if ctx.Err() != nil {
		res.Cancelled = true
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
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

// allowlistCheck returns nil if the command is permitted under
// DROIDMCP_TERMUX_ALLOWLIST. An empty/unset env var means "allow all"
// (preserves the prior behaviour). Comparison is on the command's basename
// so callers can pass either "ls" or "/usr/bin/ls".
func allowlistCheck(command string) error {
	raw := strings.TrimSpace(os.Getenv(allowlistEnv))
	if raw == "" {
		return nil
	}
	allowed := parseAllowlist(raw)
	if len(allowed) == 0 {
		return nil
	}
	base := filepath.Base(command)
	if _, ok := allowed[command]; ok {
		return nil
	}
	if _, ok := allowed[base]; ok {
		return nil
	}
	return fmt.Errorf("command %q not in DROIDMCP_TERMUX_ALLOWLIST", command)
}

// dangerousEnvPrefixes name environment variables that steer the dynamic
// linker (LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT, bionic's LD_CONFIG_FILE, and
// the DYLD_* family). Allowing an untrusted caller to set them would let an
// allowlisted binary load and run arbitrary code, so they are rejected in
// caller-supplied env_extra.
var dangerousEnvPrefixes = []string{"LD_", "DYLD_"}

// checkEnvExtra rejects any env_extra key that matches a dynamic-linker prefix.
// Comparison is case-insensitive so a differently-cased key cannot slip a
// linker override through (the linker itself only honours the canonical case,
// but refusing is harmless and defensive).
func checkEnvExtra(env map[string]string) error {
	for k := range env {
		up := strings.ToUpper(strings.TrimSpace(k))
		for _, p := range dangerousEnvPrefixes {
			if strings.HasPrefix(up, p) {
				return fmt.Errorf("environment variable %q is not allowed: dynamic-linker overrides can bypass the command allowlist", k)
			}
		}
	}
	return nil
}

func parseAllowlist(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

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
