package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func jobTools() []ToolSpec {
	return []ToolSpec{
		jobStatusTool(),
		jobLogsTool(),
		jobStopTool(),
		jobListTool(),
		jobCleanTool(),
	}
}

func jobStatusTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("job_status",
			mcp.WithDescription("Status of a background job started by run_shell."),
			mcp.WithString("job_id", mcp.Required(), mcp.Description("Job id from run_shell")),
		),
		Handler: handleJobStatus,
	}
}

func handleJobStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	m, err := readJobMeta(id)
	if err != nil {
		return mcp.NewToolResultError("unknown job_id: " + id), nil
	}
	state, code := jobState(m)
	var logBytes int64
	if fi, e := os.Stat(m.LogPath); e == nil {
		logBytes = fi.Size()
	}
	return jsonResult(map[string]any{
		"job_id":     m.ID,
		"state":      state,
		"exit_code":  code,
		"pid":        m.PID,
		"started_at": m.StartedAt,
		"log_bytes":  logBytes,
		"log_file":   m.LogPath,
	})
}

func jobLogsTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("job_logs",
			mcp.WithDescription("Read output of a background job."),
			mcp.WithString("job_id", mcp.Required(), mcp.Description("Job id")),
			mcp.WithNumber("tail_lines", mcp.Description("Last N lines (default 200, 0 for all)")),
			mcp.WithNumber("offset", mcp.Description("Byte offset to read from")),
		),
		Handler: handleJobLogs,
	}
}

func handleJobLogs(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	m, err := readJobMeta(id)
	if err != nil {
		return mcp.NewToolResultError("unknown job_id: " + id), nil
	}
	data, err := os.ReadFile(m.LogPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	total := len(data)
	if off := req.GetInt("offset", -1); off >= 0 {
		if off > total {
			off = total
		}
		data = data[off:]
	} else if n := req.GetInt("tail_lines", 200); n > 0 {
		data = lastLines(data, n)
	}
	state, code := jobState(m)
	return jsonResult(map[string]any{
		"job_id":      m.ID,
		"state":       state,
		"exit_code":   code,
		"total_bytes": total,
		"output":      safeUTF8(data),
	})
}

func jobStopTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("job_stop",
			mcp.WithDescription("Stop a background job (SIGTERM, or SIGKILL if force=true)."),
			mcp.WithString("job_id", mcp.Required(), mcp.Description("Job id")),
			mcp.WithBoolean("force", mcp.Description("Send SIGKILL instead of SIGTERM")),
		),
		Handler: handleJobStop,
	}
}

func handleJobStop(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	m, err := readJobMeta(id)
	if err != nil {
		return mcp.NewToolResultError("unknown job_id: " + id), nil
	}
	sig := syscall.SIGTERM
	if req.GetBool("force", false) {
		sig = syscall.SIGKILL
	}
	if err := jobStop(m, sig); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"job_id": m.ID, "signal": sig.String(), "stopped": true})
}

func jobListTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("job_list",
			mcp.WithDescription("List background jobs and their state."),
		),
		Handler: handleJobList,
	}
}

func handleJobList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobs := listJobs()
	out := make([]map[string]any, 0, len(jobs))
	for _, m := range jobs {
		state, code := jobState(m)
		out = append(out, map[string]any{
			"job_id":     m.ID,
			"state":      state,
			"exit_code":  code,
			"pid":        m.PID,
			"started_at": m.StartedAt,
		})
	}
	return jsonResult(map[string]any{"jobs": out})
}

func lastLines(b []byte, n int) []byte {
	count := 0
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == 10 {
			count++
			if count > n {
				return b[i+1:]
			}
		}
	}
	return b
}

func jobCleanTool() ToolSpec {
	return ToolSpec{
		Tool: mcp.NewTool("job_clean",
			mcp.WithDescription("Delete finished background-job logs and metadata. Running jobs are kept."),
			mcp.WithNumber("max_age_hours", mcp.Description("Only remove jobs older than this. Default 24, 0 removes all finished.")),
		),
		Handler: handleJobClean,
	}
}

func handleJobClean(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hours := req.GetInt("max_age_hours", 24)
	removed := cleanJobs(time.Duration(hours) * time.Hour)
	return jsonResult(map[string]any{"removed": removed})
}
