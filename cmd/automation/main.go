// Command automation provides an MCP server that schedules shell scripts on the
// device: create_task stores a script with a cron expression or an interval,
// and an in-process scheduler runs it while the server is up. run_task fires a
// task on demand, task_history returns its recent runs, and delete_task removes
// it (stopping it if it is running).
//
// A task is code that runs later with nobody watching, so this server is
// locked down harder than mcp-termux:
//
//   - No dev mode: it refuses to start without DROIDMCP_AUTOMATION_KEY or
//     DROIDMCP_API_KEY.
//   - The command allowlist is mandatory (DROIDMCP_AUTOMATION_ALLOWLIST). Scripts
//     run in an embedded shell interpreter that checks every external command
//     against it when the command starts, including those inside pipelines,
//     $(…) and eval. Allowlisted names resolve through the server's PATH, not
//     the script's.
//   - Scripts cannot change dynamic-linker variables (LD_*, DYLD_*) or write
//     files through shell redirections; writing needs an allowlisted command.
//   - Runs have a timeout and capped output. On timeout, task deletion or
//     shutdown their process group gets SIGTERM, then the command SIGKILL.
//
// Tasks and their last runs are kept in a JSON file (DROIDMCP_AUTOMATION_DB).
// Cron expressions use DROIDMCP_AUTOMATION_TZ, TZ, or Android's own time zone.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embedded zone database: Termux has no /usr/share/zoneinfo for Go to read.
	_ "time/tzdata"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	allowlistEnv = "DROIDMCP_AUTOMATION_ALLOWLIST"
	storeEnv     = "DROIDMCP_AUTOMATION_DB"
	tzEnv        = "DROIDMCP_AUTOMATION_TZ"

	maxTasks           = 50
	maxScriptBytes     = 16 << 10
	maxNameLen         = 64
	minInterval        = time.Minute
	maxInterval        = 366 * 24 * time.Hour
	defaultTaskTimeout = 60 * time.Second
	maxTaskTimeout     = 30 * time.Minute
	captureBytes       = 256 << 10 // per stream, returned by run_task
	storedOutputBytes  = 4 << 10   // per stream, kept in task_history
	historyPerTask     = 20
	maxSleep           = 30 * time.Second
	shutdownGrace      = 10 * time.Second
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	apiKey := config.ResolveAPIKey("automation")
	if apiKey == "" {
		logger.Log.Error("mcp-automation requires DROIDMCP_AUTOMATION_KEY or DROIDMCP_API_KEY to be set. Refusing to start (tasks run commands unattended).")
		os.Exit(1)
	}
	allow, err := parseAllowlist(os.Getenv(allowlistEnv))
	if err != nil {
		logger.Log.Error("mcp-automation: " + err.Error())
		os.Exit(1)
	}
	if allow.empty() {
		logger.Log.Error("mcp-automation requires " + allowlistEnv + " (comma-separated commands tasks may run). Refusing to start: scheduled tasks run unattended, so there is no allow-all default.")
		os.Exit(1)
	}
	loc, err := taskLocation()
	if err != nil {
		logger.Log.Error("mcp-automation: invalid " + tzEnv + ": " + err.Error())
		os.Exit(1)
	}
	path := storePath()
	st, err := openStore(path, loc, time.Now())
	if err != nil {
		logger.Log.Error("mcp-automation: " + err.Error())
		os.Exit(1)
	}

	sched = newScheduler(st, allow, loc)
	go sched.loop(sched.life)
	// Stop task runs as soon as a shutdown signal arrives (core.ServeSSE also
	// receives it and drains HTTP): a run_task call blocked on a long script
	// would otherwise hold the drain open until its deadline.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	context.AfterFunc(sigCtx, sched.stop)
	logger.Info("mcp-automation scheduler started", "tasks", st.count(), "time_zone", loc.String(), "store", path)

	server := core.NewDroidServer("mcp-automation", buildinfo.Version)
	server.APIKey = apiKey
	registerTools(server)

	err = server.ServeSSE(cfg.Port)
	sched.shutdown()
	if err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("create_task",
		mcp.WithDescription("Schedule a shell script to run on the device. Give exactly one of `cron` (5 fields: minute hour day-of-month month day-of-week, or @hourly/@daily/@weekly/@monthly/@yearly, in the server's time zone) or `interval_seconds` (60 to 31622400). The script runs in an embedded POSIX/bash-compatible interpreter: builtins (echo, printf, test, read, cd, …) always work, and every external command — including inside pipes, $(…) and eval — must be in DROIDMCP_AUTOMATION_ALLOWLIST. Scripts cannot write files with > or >> (only to /dev/null) nor change LD_*/DYLD_* variables. Runs missed while the server is down are skipped. Returns the task with its id and next_run."),
		mcp.WithString("script", mcp.Required(), mcp.Description("Shell script to run, e.g. \"termux-battery-status | jq .percentage\". Max 16 KiB")),
		mcp.WithString("cron", mcp.Description("Cron expression, e.g. \"*/15 * * * *\" or \"0 8 * * mon-fri\"")),
		mcp.WithNumber("interval_seconds", mcp.Description("Run every N seconds (60 to 31622400) instead of a cron expression")),
		mcp.WithString("name", mcp.Description("Optional label, max 64 bytes")),
		mcp.WithString("cwd", mcp.Description("Absolute working directory. Default: the home directory")),
		mcp.WithObject("env_extra", mcp.Description("Extra environment variables for the script (string values; LD_*/DYLD_* refused)")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-run timeout. Default 60s, max 1800s")),
	), handleCreateTask)

	s.MCPServer.AddTool(mcp.NewTool("list_tasks",
		mcp.WithDescription("List scheduled tasks with their script, schedule, next_run (in the server's time zone), whether each is running now, and a summary of its last run. Returns {time_zone, count, tasks:[…]}."),
	), handleListTasks)

	s.MCPServer.AddTool(mcp.NewTool("run_task",
		mcp.WithDescription("Run a task now and wait for it. Returns the run: exit_code, stdout and stderr (up to 256 KiB each), duration_ms, timed_out, and error when the script was stopped (e.g. a command outside the allowlist). A failed run is an error result. Fails if the task is already running. The run is recorded in task_history with trigger \"manual\"."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task id from create_task or list_tasks")),
	), handleRunTask)

	s.MCPServer.AddTool(mcp.NewTool("delete_task",
		mcp.WithDescription("Delete a task and its history. A run in progress is stopped (SIGTERM, then SIGKILL). Returns {deleted, stopped_running}."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task id")),
	), handleDeleteTask)

	s.MCPServer.AddTool(mcp.NewTool("task_history",
		mcp.WithDescription("Recent runs of a task, newest first: trigger (schedule/manual), started_at, duration_ms, exit_code, timed_out, error, and the last 4 KiB of stdout/stderr. The newest 20 runs are kept per task."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Task id")),
		mcp.WithNumber("limit", mcp.Description("Max runs to return. Default and max 20")),
	), handleTaskHistory)
}
