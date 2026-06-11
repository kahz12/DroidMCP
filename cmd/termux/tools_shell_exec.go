package main

import (
	"context"
	"os/exec"

	"github.com/mark3labs/mcp-go/mcp"
)

func runShellTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("run_shell",
			mcp.WithDescription("Run a shell script (bash, else sh): pipes, redirection, globbing, and chains. Returns JSON with stdout, stderr, exit_code. Set background=true for long tasks (detached; survives timeout and server restart) then poll job_status/job_logs and stop with job_stop. WARNING: bypasses the per-command allowlist."),
			mcp.WithString("script", mcp.Required(), mcp.Description("Shell script to run")),
			mcp.WithString("cwd", mcp.Description("Working directory")),
			mcp.WithObject("env_extra", mcp.Description("Extra environment variables")),
			mcp.WithString("stdin", mcp.Description("Data piped to stdin (foreground only)")),
			mcp.WithBoolean("background", mcp.Description("Run detached; returns job_id, pid, log_file")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Foreground timeout. Default 30s, max 300s.")),
		),
		Handler: handleRunShell,
	}
}

func handleRunShell(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	script, err := req.RequireString("script")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cwd := req.GetString("cwd", "")
	env := stringMapArg(req, "env_extra")
	if req.GetBool("background", false) {
		m, err := startBackgroundShell(script, cwd, env)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"job_id":   m.ID,
			"pid":      m.PID,
			"log_file": m.LogPath,
		})
	}
	opts := execOptions{
		Command:  shellPath(),
		Args:     []string{"-lc", "--", script},
		Cwd:      cwd,
		EnvExtra: env,
		Stdin:    req.GetString("stdin", ""),
		Timeout:  timeoutFromReq(req),
		Trusted:  true,
	}
	return runAndRender(ctx, opts)
}

func shellPath() string {
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	return "/bin/sh"
}
