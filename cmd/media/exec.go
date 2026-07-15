package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Timeouts and byte caps for external media tools. Video work can be slow, so
// the default is generous; the ceiling still protects against a hung child.
const (
	defaultToolTimeout = 120 * time.Second
	maxToolTimeout     = 10 * time.Minute
	maxOutputBytes     = 1 << 20 // 1 MiB per stream

	// Env overrides for the external binaries, so operators can pin an exact
	// path instead of relying on PATH lookup.
	envFFmpeg   = "DROIDMCP_MEDIA_FFMPEG"
	envExiftool = "DROIDMCP_MEDIA_EXIFTOOL"

	installFFmpegHint   = "ffmpeg not found; install it with `pkg install ffmpeg` (or set DROIDMCP_MEDIA_FFMPEG to its path)"
	installExiftoolHint = "exiftool not found; install it with `pkg install exiftool` (or set DROIDMCP_MEDIA_EXIFTOOL to its path)"
)

// lookPath is overridable in tests so binary-presence checks can be simulated
// without the real tools installed.
var lookPath = exec.LookPath

// ffmpegBin returns the ffmpeg executable to invoke: the DROIDMCP_MEDIA_FFMPEG
// override when set, otherwise the bare name resolved via PATH.
func ffmpegBin() string {
	if v := strings.TrimSpace(os.Getenv(envFFmpeg)); v != "" {
		return v
	}
	return "ffmpeg"
}

// exiftoolBin mirrors ffmpegBin for the optional exiftool dependency.
func exiftoolBin() string {
	if v := strings.TrimSpace(os.Getenv(envExiftool)); v != "" {
		return v
	}
	return "exiftool"
}

// ensureTool returns a friendly, actionable error (hint) when bin is not
// resolvable, instead of the bare "no such file or directory" exec would give.
func ensureTool(bin, hint string) error {
	if _, err := lookPath(bin); err != nil {
		return errors.New(hint)
	}
	return nil
}

// toolResult is the captured outcome of running an external media tool.
// stdout and stderr are UTF-8-safe (replacement char on invalid bytes) so they
// are always safe to embed in JSON; RawStdout keeps the unmodified stdout
// (subject to the size cap) for callers that need to parse machine output such
// as exiftool -json.
type toolResult struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	TimedOut   bool
	Cancelled  bool
	Truncated  bool
	DurationMs int64
	RawStdout  []byte
}

// runTool invokes an external binary with the given argv (no shell), capturing
// stdout and stderr separately into capped buffers and applying a per-call
// timeout. Argv is passed verbatim to exec.CommandContext, so there is no shell
// interpolation: callers are responsible for validating any dynamic tokens.
func runTool(ctx context.Context, bin string, args []string, timeout time.Duration) (*toolResult, error) {
	if timeout <= 0 {
		timeout = defaultToolTimeout
	}
	if timeout > maxToolTimeout {
		timeout = maxToolTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	stdout := &cappedBuffer{max: maxOutputBytes}
	stderr := &cappedBuffer{max: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Place the child in its own process group so signal-on-cancel reaches
	// any helpers it forked.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// On timeout/cancel, send SIGTERM to the whole group first (ffmpeg traps
	// it and exits promptly) and let the runtime escalate to SIGKILL after
	// WaitDelay. Mirrors cmd/termux's runCommand.
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
	raw := stdout.Bytes()
	res := &toolResult{
		DurationMs: time.Since(start).Milliseconds(),
		Stdout:     safeUTF8(raw),
		Stderr:     safeUTF8(stderr.Bytes()),
		Truncated:  stdout.truncated || stderr.truncated,
		RawStdout:  raw,
	}
	// Distinguish "we ran out of time" from "the parent cancelled us".
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

// cappedBuffer is an io.Writer that drops bytes past max and records the
// overflow, so a runaway child cannot exhaust memory.
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
