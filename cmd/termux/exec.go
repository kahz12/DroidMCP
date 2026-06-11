package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
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
			if k == "" || strings.ContainsRune(k, '=') || strings.ContainsRune(k, '\x00') || strings.ContainsRune(v, '\x00') {
				continue
			}
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
