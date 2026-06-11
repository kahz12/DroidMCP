import sys

with open("cmd/termux/jobs.go", "r") as f:
    content = f.read()

# Add cappedFileWriter and import io if needed
if "type cappedFileWriter" not in content:
    capped_writer = """
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
"""
    content = content.replace("type jobMeta struct {", capped_writer + "\ntype jobMeta struct {")

# Update startBackgroundShell environment variables
env_orig = """	if len(env) > 0 {
		e := os.Environ()
		for k, v := range env {
			e = append(e, k+"="+v)
		}
		cmd.Env = e
	}"""
env_new = """	if len(env) > 0 {
		e := os.Environ()
		for k, v := range env {
			if k == "" || strings.ContainsRune(k, '=') || strings.ContainsRune(k, '\\x00') || strings.ContainsRune(v, '\\x00') {
				continue
			}
			if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "PATH" || k == "BASH_ENV" || k == "ENV" || k == "PROMPT_COMMAND" {
				continue
			}
			e = append(e, k+"="+v)
		}
		cmd.Env = e
	}"""
content = content.replace(env_orig, env_new)

# Update stdout/stderr and process kill logic
old_run = """	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return jobMeta{}, err
	}
	m.PID = cmd.Process.Pid
	if err := writeJobMeta(m); err != nil {
		return jobMeta{}, err
	}
	go reapJob(cmd, logFile, m.ExitPath)"""

new_run = """	writer := &cappedFileWriter{f: logFile, max: 10 * 1024 * 1024} // 10 MiB limit
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
	go reapJob(cmd, logFile, m.ExitPath)"""
content = content.replace(old_run, new_run)

with open("cmd/termux/jobs.go", "w") as f:
    f.write(content)
