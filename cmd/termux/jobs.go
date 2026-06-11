package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)


// cappedFileWriter implements an io.Writer that limits total bytes written.
type cappedFileWriter struct {
	f     *os.File
	max   int64
	wrote int64
}

func (c *cappedFileWriter) Write(p []byte) (n int, err error) {
	if c.wrote >= c.max {
		return len(p), nil // Drop the rest to prevent disk exhaustion
	}
	allowed := c.max - c.wrote
	if int64(len(p)) > allowed {
		n, err = c.f.Write(p[:allowed])
		c.wrote += int64(n)
		return len(p), err // Claim all was written to prevent broken pipe
	}
	n, err = c.f.Write(p)
	c.wrote += int64(n)
	return n, err
}

func (c *cappedFileWriter) Close() error {
	return c.f.Close()
}

type jobMeta struct {
	ID        string
	PID       int
	Script    string
	Cwd       string
	StartedAt time.Time
	LogPath   string
	ExitPath  string
}

func jobsDir() string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "droidmcp-jobs")
}

func metaPath(id string) string { return filepath.Join(jobsDir(), id+".json") }

func newJobID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func writeJobMeta(m jobMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(m.ID), b, 0o600)
}

func readJobMeta(id string) (jobMeta, error) {
	var m jobMeta
	b, err := os.ReadFile(metaPath(id))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func startBackgroundShell(script, cwd string, env map[string]string) (jobMeta, error) {
	dir := jobsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return jobMeta{}, err
	}
	id := newJobID()
	m := jobMeta{
		ID:        id,
		Script:    script,
		Cwd:       cwd,
		StartedAt: time.Now().UTC(),
		LogPath:   filepath.Join(dir, id+".log"),
		ExitPath:  filepath.Join(dir, id+".exit"),
	}
	logFile, err := os.Create(m.LogPath)
	if err != nil {
		return jobMeta{}, err
	}
	cmd := exec.Command(shellPath(), "-lc", script)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		e := os.Environ()
		for k, v := range env {
			if k == "" || strings.ContainsRune(k, '=') || strings.ContainsRune(k, '\x00') || strings.ContainsRune(v, '\x00') {
				continue
			}
			if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "PATH" || k == "BASH_ENV" || k == "ENV" || k == "PROMPT_COMMAND" {
				continue
			}
			e = append(e, k+"="+v)
		}
		cmd.Env = e
	}
	writer := &cappedFileWriter{f: logFile, max: 10 * 1024 * 1024} // 10 MiB limit
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return jobMeta{}, err
	}
	m.PID = cmd.Process.Pid
	if err := writeJobMeta(m); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logFile.Close()
		_ = os.Remove(m.LogPath)
		_ = os.Remove(m.ExitPath)
		_ = os.Remove(metaPath(m.ID))
		return jobMeta{}, err
	}
	go reapJob(cmd, logFile, m.ExitPath)
	return m, nil
}

func reapJob(cmd *exec.Cmd, logFile *os.File, exitPath string) {
	err := cmd.Wait()
	logFile.Close()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	_ = os.WriteFile(exitPath, []byte(strconv.Itoa(code)), 0o600)
}

func jobState(m jobMeta) (string, int) {
	if b, err := os.ReadFile(m.ExitPath); err == nil {
		c, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return "exited", c
	}
	if m.PID > 0 && syscall.Kill(m.PID, 0) == nil {
		return "running", 0
	}
	return "dead", -1
}

func jobStop(m jobMeta, sig syscall.Signal) error {
	if m.PID <= 0 {
		return errors.New("job has no pid")
	}
	if err := syscall.Kill(-m.PID, sig); err != nil {
		return syscall.Kill(m.PID, sig)
	}
	return nil
}

func listJobs() []jobMeta {
	entries, err := os.ReadDir(jobsDir())
	if err != nil {
		return nil
	}
	out := make([]jobMeta, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if m, err := readJobMeta(strings.TrimSuffix(name, ".json")); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func cleanJobs(maxAge time.Duration) int {
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, m := range listJobs() {
		if state, _ := jobState(m); state == "running" {
			continue
		}
		if maxAge > 0 && m.StartedAt.After(cutoff) {
			continue
		}
		_ = os.Remove(m.LogPath)
		_ = os.Remove(m.ExitPath)
		_ = os.Remove(metaPath(m.ID))
		removed++
	}
	return removed
}
