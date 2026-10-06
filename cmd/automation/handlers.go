package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

// sched is the server's scheduler, set up in main (and by tests).
var sched *scheduler

// taskView is a task as the tools return it: next_run in the server's time
// zone, whether it is running now, and a summary of its last run.
type taskView struct {
	task
	Running bool        `json:"running"`
	LastRun *runSummary `json:"last_run,omitempty"`
}

type runSummary struct {
	Trigger   string    `json:"trigger"`
	StartedAt time.Time `json:"started_at"`
	ExitCode  int       `json:"exit_code"`
	OK        bool      `json:"ok"`
}

func (s *scheduler) view(t task) taskView {
	t.NextRun = t.NextRun.In(s.loc)
	v := taskView{task: t, Running: s.isRunning(t.ID)}
	if runs, err := s.store.runs(t.ID, 1); err == nil && len(runs) == 1 {
		r := runs[0]
		v.LastRun = &runSummary{Trigger: r.Trigger, StartedAt: r.StartedAt.In(s.loc), ExitCode: r.ExitCode, OK: r.ok()}
	}
	return v
}

func handleCreateTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t, err := buildTask(req, sched.now())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sched.store.add(t); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sched.poke()
	logger.Info("task created", "task", t.ID, "name", t.Name, "next_run", t.NextRun.In(sched.loc).Format(time.RFC3339))
	return jsonResult(sched.view(t))
}

// buildTask validates a create_task call. Everything that would make the task
// fail later — a syntax error, a command outside the allowlist, a schedule
// that never fires, a missing cwd — is rejected now.
func buildTask(req mcp.CallToolRequest, now time.Time) (task, error) {
	script, err := req.RequireString("script")
	if err != nil {
		return task{}, err
	}
	if strings.TrimSpace(script) == "" {
		return task{}, errors.New("script is empty")
	}
	if len(script) > maxScriptBytes {
		return task{}, fmt.Errorf("script is longer than %d bytes", maxScriptBytes)
	}
	file, err := parseScript(script)
	if err != nil {
		return task{}, err
	}
	if err := checkScript(sched.allow, file); err != nil {
		return task{}, err
	}

	name := strings.TrimSpace(req.GetString("name", ""))
	if len(name) > maxNameLen {
		return task{}, fmt.Errorf("name is longer than %d bytes", maxNameLen)
	}

	cronExpr := strings.TrimSpace(req.GetString("cron", ""))
	var every time.Duration
	if _, ok := req.GetArguments()["interval_seconds"]; ok {
		// Check the range on the float before converting, so a huge value
		// cannot overflow time.Duration into something that passes.
		secs := req.GetFloat("interval_seconds", 0)
		if secs != math.Trunc(secs) || secs < minInterval.Seconds() || secs > maxInterval.Seconds() {
			return task{}, fmt.Errorf("interval_seconds must be a whole number between %d and %d", int(minInterval.Seconds()), int(maxInterval.Seconds()))
		}
		every = time.Duration(secs) * time.Second
	}
	sch, err := parseSchedule(cronExpr, every, sched.loc)
	if err != nil {
		return task{}, err
	}
	next := sch.next(now, time.Time{})
	if next.IsZero() {
		return task{}, fmt.Errorf("cron %q never fires", cronExpr)
	}

	cwd := strings.TrimSpace(req.GetString("cwd", ""))
	if cwd != "" {
		if !filepath.IsAbs(cwd) {
			return task{}, fmt.Errorf("cwd %q must be an absolute path", cwd)
		}
		info, err := os.Stat(cwd)
		if err != nil {
			return task{}, fmt.Errorf("cwd %q: %w", cwd, err)
		}
		if !info.IsDir() {
			return task{}, fmt.Errorf("cwd %q is not a directory", cwd)
		}
	}

	env := stringMapArg(req, "env_extra")
	for k := range env {
		if k == "" || strings.ContainsAny(k, "= ") {
			return task{}, fmt.Errorf("env_extra key %q is not a valid variable name", k)
		}
		if isLinkerVar(k) {
			return task{}, fmt.Errorf("env_extra cannot set %s: dynamic-linker variables could bypass %s", k, allowlistEnv)
		}
	}

	id, err := newTaskID()
	if err != nil {
		return task{}, err
	}
	interval := 0
	if every > 0 {
		interval = int(every / time.Second)
	}
	return task{
		ID:              id,
		Name:            name,
		Script:          script,
		Cron:            cronExpr,
		IntervalSeconds: interval,
		Cwd:             cwd,
		EnvExtra:        env,
		TimeoutSeconds:  int(core.TimeoutArg(req, defaultTaskTimeout, maxTaskTimeout) / time.Second),
		CreatedAt:       now,
		NextRun:         next,
		sched:           sch,
	}, nil
}

func newTaskID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func handleListTasks(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasks := sched.store.list()
	views := make([]taskView, len(tasks))
	for i, t := range tasks {
		views[i] = sched.view(t)
	}
	return jsonResult(map[string]any{
		"time_zone": sched.loc.String(),
		"count":     len(views),
		"tasks":     views,
	})
}

// runResult is what run_task returns: the full run record (output capped at
// captureBytes per stream, not trimmed as in the history) plus the task id.
type runResult struct {
	ID string `json:"id"`
	runRecord
}

func handleRunTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rec, err := sched.runNow(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(taskError(id, err)), nil
	}
	body, err := json.Marshal(runResult{ID: id, runRecord: rec})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !rec.ok() {
		return mcp.NewToolResultError(string(body)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func handleDeleteTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	wasRunning, err := sched.remove(id)
	if err != nil {
		return mcp.NewToolResultError(taskError(id, err)), nil
	}
	logger.Info("task deleted", "task", id, "stopped_running", wasRunning)
	return jsonResult(map[string]any{"deleted": id, "stopped_running": wasRunning})
}

func handleTaskHistory(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", historyPerTask)
	if limit <= 0 || limit > historyPerTask {
		limit = historyPerTask
	}
	runs, err := sched.store.runs(id, limit)
	if err != nil {
		return mcp.NewToolResultError(taskError(id, err)), nil
	}
	for i := range runs {
		runs[i].StartedAt = runs[i].StartedAt.In(sched.loc)
	}
	return jsonResult(map[string]any{"id": id, "count": len(runs), "runs": runs})
}

func taskError(id string, err error) string {
	if errors.Is(err, errTaskNotFound) {
		return fmt.Sprintf("task %q not found", id)
	}
	return err.Error()
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// stringMapArg pulls a string->string map out of a JSON-decoded object arg,
// dropping non-string values. Mirrors the helper in cmd/termux.
func stringMapArg(req mcp.CallToolRequest, name string) map[string]string {
	raw, ok := req.GetArguments()[name].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
