package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Scripts run in an embedded shell interpreter (mvdan.cc/sh), not in the
// system sh. That is what makes the allowlist mean something: the interpreter
// hands every external command it is about to start — in a pipeline, a $(…)
// substitution, an eval'd string or a sourced file — to guard.exec, which
// refuses anything outside DROIDMCP_AUTOMATION_ALLOWLIST. Under `sh -c` the
// allowlist would only ever see "sh". Shell builtins (echo, printf, test, cd,
// read, …) run inside the interpreter and are always available.

// allowlist holds the commands a task may start. Bare names are resolved
// through the server's own PATH, never the script's; absolute paths must match
// exactly.
type allowlist struct {
	names map[string]bool
	paths map[string]bool
}

// parseAllowlist reads a comma-separated list of command names and absolute
// paths. A relative path such as "bin/tool" is rejected: it would resolve
// against whatever directory a task happens to run in.
func parseAllowlist(raw string) (allowlist, error) {
	a := allowlist{names: map[string]bool{}, paths: map[string]bool{}}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
		case filepath.IsAbs(entry):
			a.paths[filepath.Clean(entry)] = true
		case strings.Contains(entry, "/"):
			return allowlist{}, fmt.Errorf("%s entry %q must be a command name or an absolute path", allowlistEnv, entry)
		default:
			a.names[entry] = true
		}
	}
	return a, nil
}

func (a allowlist) empty() bool { return len(a.names) == 0 && len(a.paths) == 0 }

// resolve returns the binary a command name runs, or an error if the allowlist
// does not permit it.
func (a allowlist) resolve(name string) (string, error) {
	if strings.Contains(name, "/") {
		if clean := filepath.Clean(name); filepath.IsAbs(clean) && a.paths[clean] {
			return clean, nil
		}
		return "", fmt.Errorf("command %q is not in %s", name, allowlistEnv)
	}
	if !a.names[name] {
		return "", fmt.Errorf("command %q is not in %s", name, allowlistEnv)
	}
	// The server's PATH, not the script's: a script may set PATH, but must not
	// be able to point an allowlisted name at a binary of its choosing.
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("command %q is allowlisted but not found in PATH", name)
	}
	return path, nil
}

// guard is the interpreter's ExecHandler for one run. A refused command is a
// fatal error, so the script stops there instead of carrying on without that
// step. The refusal is also kept here because a command refused inside a
// background job or a <(…) substitution only stops that subshell, and the
// script itself could still finish with status 0.
type guard struct {
	allow   allowlist
	mu      sync.Mutex
	refused error
}

func (g *guard) refuse(err error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refused == nil {
		g.refused = err
	}
	return err
}

func (g *guard) firstRefusal() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refused
}

func (g *guard) exec(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	path, err := g.allow.resolve(args[0])
	if err != nil {
		return g.refuse(err)
	}
	env, err := childEnv(hc.Env)
	if err != nil {
		return g.refuse(err)
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Args = args
	cmd.Env = env
	cmd.Dir = hc.Dir
	if f, ok := hc.Stdin.(*os.File); !ok || f != nil {
		cmd.Stdin = hc.Stdin
	}
	cmd.Stdout = hc.Stdout
	cmd.Stderr = hc.Stderr
	// Own process group, SIGTERM to the whole group on timeout or delete, and
	// SIGKILL to the command after WaitDelay: the same panic button as
	// mcp-termux.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 2 * time.Second

	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		return nil
	case errors.As(err, &exitErr):
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return interp.ExitStatus(uint8(128 + int(status.Signal())))
		}
		return interp.ExitStatus(uint8(exitErr.ExitCode()))
	default:
		_, _ = fmt.Fprintln(hc.Stderr, err)
		return interp.ExitStatus(126)
	}
}

// isLinkerVar reports whether name steers the dynamic linker (LD_PRELOAD,
// LD_LIBRARY_PATH, DYLD_*, …).
func isLinkerVar(name string) bool {
	up := strings.ToUpper(name)
	return strings.HasPrefix(up, "LD_") || strings.HasPrefix(up, "DYLD_")
}

// childEnv builds the environment of an external command from the
// interpreter's exported variables. Dynamic-linker variables must keep the
// server's own value: Termux itself sets LD_PRELOAD for termux-exec, but a
// script that changed it could make an allowlisted binary load arbitrary code.
// Unsetting one only loads less code, so that is allowed.
func childEnv(env expand.Environ) ([]string, error) {
	vars := map[string]string{}
	for name, vr := range env.Each {
		if !vr.IsSet() {
			delete(vars, name)
			continue
		}
		if vr.Exported && vr.Kind == expand.String {
			vars[name] = vr.String()
		}
	}
	list := make([]string, 0, len(vars))
	for name, value := range vars {
		if isLinkerVar(name) {
			if orig, ok := os.LookupEnv(name); !ok || orig != value {
				return nil, fmt.Errorf("a task cannot set %s: dynamic-linker variables could bypass %s", name, allowlistEnv)
			}
		}
		list = append(list, name+"="+value)
	}
	sort.Strings(list)
	return list, nil
}

// openRedirect is the interpreter's OpenHandler. Scripts may read files but not
// write them through the shell: a redirection is invisible to the allowlist,
// and `>` into ~/.bashrc or a Termux:Boot script would run arbitrary commands
// later, outside it. Discarding output into /dev/null is fine. To keep output,
// read it back with task_history, or allowlist a command such as tee.
func openRedirect(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
	if flag&writeFlags != 0 && path != os.DevNull {
		return nil, redirectError(path)
	}
	return interp.DefaultOpenHandler()(ctx, path, flag, perm)
}

// waitForJobs is run after every script; see runScript.
var waitForJobs = &syntax.CallExpr{Args: []*syntax.Word{{Parts: []syntax.WordPart{&syntax.Lit{Value: "wait"}}}}}

// parseScript parses script with the interpreter's (bash-compatible) grammar.
func parseScript(script string) (*syntax.File, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, fmt.Errorf("script syntax error: %w", err)
	}
	return file, nil
}

// checkScript reports the first thing in file that a run would refuse — a
// command named literally that the allowlist does not permit, or a write
// redirection to a literal path — so create_task fails at once instead of at
// the first scheduled run. Anything only known at run time ($cmd, $(…), eval,
// > "$file") is still checked by guard.exec and openRedirect when it runs.
func checkScript(a allowlist, file *syntax.File) error {
	funcs := map[string]bool{}
	syntax.Walk(file, func(node syntax.Node) bool {
		if fn, ok := node.(*syntax.FuncDecl); ok {
			funcs[fn.Name.Value] = true
		}
		return true
	})
	var err error
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				break
			}
			if name := n.Args[0].Lit(); name != "" && !funcs[name] && !interp.IsBuiltin(name) {
				_, err = a.resolve(name)
			}
		case *syntax.Redirect:
			if writesFile(n.Op) && n.Word != nil {
				if target := n.Word.Lit(); target != "" && target != os.DevNull {
					err = redirectError(target)
				}
			}
		}
		return err == nil
	})
	return err
}

// writesFile reports whether a redirection operator opens its target for
// writing (>, >>, >|, <>, &>, &>>). Duplications such as >&2 do not open a file.
func writesFile(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob, syntax.AppClob,
		syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
		return true
	}
	return false
}

func redirectError(path string) error {
	return fmt.Errorf("cannot redirect output to %q: tasks may only write files through an allowlisted command", path)
}

// scriptOptions describes one run of a task's script.
type scriptOptions struct {
	Script   string
	Cwd      string
	EnvExtra map[string]string
	Timeout  time.Duration
	MaxBytes int64
}

// scriptResult is the outcome of one run. Error explains a run that was
// stopped rather than finished (a refused command, a refused redirection).
type scriptResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	TimedOut  bool
	Cancelled bool
	Truncated bool
	Error     string
}

// runScript runs opts.Script under the allowlist, with a timeout and capped
// output. ctx cancellation (delete_task, a cancelled run_task) stops it.
func runScript(ctx context.Context, a allowlist, opts scriptOptions) scriptResult {
	file, err := parseScript(opts.Script)
	if err != nil {
		return scriptResult{ExitCode: 2, Error: err.Error()}
	}
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	stdout := &cappedBuffer{max: opts.MaxBytes}
	stderr := &cappedBuffer{max: opts.MaxBytes}
	env := os.Environ()
	for k, v := range opts.EnvExtra {
		env = append(env, k+"="+v)
	}
	g := &guard{allow: a}
	runner, err := interp.New(
		interp.Env(expand.ListEnviron(env...)),
		interp.Dir(taskDir(opts.Cwd)),
		interp.StdIO(nil, stdout, stderr),
		interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc { return g.exec }),
		interp.OpenHandler(openRedirect),
	)
	if err != nil {
		return scriptResult{ExitCode: 126, Error: err.Error()}
	}

	runErr := runner.Run(cctx, file)
	// Background jobs (`cmd &`, <(…)) belong to the run: wait for them within
	// the same timeout, so their output and any refused command are recorded
	// instead of being cut off when the run ends.
	if cctx.Err() == nil {
		_ = runner.Run(cctx, waitForJobs)
	}
	res := scriptResult{
		Stdout:    safeUTF8(stdout.Bytes()),
		Stderr:    safeUTF8(stderr.Bytes()),
		Truncated: stdout.truncated || stderr.truncated,
	}
	switch {
	case cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil:
		res.TimedOut, res.ExitCode = true, -1
	case ctx.Err() != nil:
		res.Cancelled, res.ExitCode = true, -1
	case runErr == nil:
	default:
		var status interp.ExitStatus
		if errors.As(runErr, &status) {
			res.ExitCode = int(status)
		} else {
			res.ExitCode, res.Error = 126, runErr.Error()
		}
	}
	if refused := g.firstRefusal(); refused != nil && res.Error == "" && !res.TimedOut && !res.Cancelled {
		res.ExitCode, res.Error = 126, refused.Error()
	}
	return res
}

// taskDir is the directory a script starts in: its cwd, or the home directory
// so scheduled runs do not depend on where the server was launched.
func taskDir(cwd string) string {
	if cwd != "" {
		return cwd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "/"
}

// cappedBuffer is an io.Writer that drops bytes past max and records the
// overflow, so a runaway script cannot exhaust memory. Pipelines write to it
// from several goroutines, hence the mutex.
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
	return append([]byte(nil), c.buf...)
}

// safeUTF8 returns b verbatim when it is valid UTF-8, otherwise with invalid
// bytes replaced by U+FFFD so the result is safe to embed in JSON.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}
