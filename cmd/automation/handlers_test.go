package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func call(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (string, bool) {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	return resultText(t, res)
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	return v
}

// withServer installs a scheduler with a fresh store, the given allowlist and
// time zone, and a clock fixed at t0. It does not start the loop.
func withServer(t *testing.T, allow string, loc *time.Location) *scheduler {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "tasks.json"), loc, t0)
	if err != nil {
		t.Fatal(err)
	}
	prev := sched
	sched = newScheduler(st, mustAllowlist(t, allow), loc)
	sched.now = func() time.Time { return t0 }
	t.Cleanup(func() {
		sched.wg.Wait()
		sched = prev
	})
	return sched
}

func TestCreateTaskRejectsInvalidInput(t *testing.T) {
	requireCommands(t, "cat")
	s := withServer(t, "cat", time.UTC)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := map[string]any{"script": "printf a | cat", "interval_seconds": 3600.0}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for key, val := range ok {
			m[key] = val
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for name, args := range map[string]map[string]any{
		"no script":           with("script", nil),
		"blank script":        with("script", "  \n"),
		"script too long":     with("script", strings.Repeat("#", maxScriptBytes+1)),
		"syntax error":        with("script", "if then fi"),
		"command not allowed": with("script", "cat x | grep y"),
		"no schedule":         with("interval_seconds", nil),
		"cron and interval":   with("cron", "* * * * *"),
		"interval too short":  with("interval_seconds", 59.0),
		"interval fractional": with("interval_seconds", 90.5),
		"interval too long":   with("interval_seconds", maxInterval.Seconds()+60),
		"interval huge":       with("interval_seconds", 1e300),
		"bad cron":            map[string]any{"script": "true", "cron": "61 * * * *"},
		"cron never fires":    map[string]any{"script": "true", "cron": "0 0 30 2 *"},
		"relative cwd":        with("cwd", "tmp"),
		"missing cwd":         with("cwd", "/nonexistent/droidmcp"),
		"cwd is a file":       with("cwd", file),
		"linker env":          with("env_extra", map[string]any{"LD_PRELOAD": "/tmp/x.so"}),
		"bad env name":        with("env_extra", map[string]any{"A=B": "x"}),
		"name too long":       with("name", strings.Repeat("n", maxNameLen+1)),
	} {
		if text, isErr := call(t, handleCreateTask, args); !isErr {
			t.Errorf("%s: accepted: %s", name, text)
		}
	}
	if n := s.store.count(); n != 0 {
		t.Fatalf("%d invalid tasks were stored", n)
	}
}

func TestTaskLifecycle(t *testing.T) {
	requireCommands(t, "cat")
	withServer(t, "cat", time.UTC)

	text, isErr := call(t, handleCreateTask, map[string]any{
		"name": "greet", "script": "printf \"$GREETING\" | cat", "interval_seconds": 3600.0,
		"env_extra": map[string]any{"GREETING": "hola"}, "timeout_seconds": 5.0,
	})
	if isErr {
		t.Fatalf("create_task: %s", text)
	}
	created := decode[taskView](t, text)
	if created.ID == "" || created.Name != "greet" || created.TimeoutSeconds != 5 || !created.NextRun.Equal(t0.Add(time.Hour)) || created.Running {
		t.Fatalf("created = %+v", created)
	}
	id := created.ID

	type list struct {
		TimeZone string     `json:"time_zone"`
		Count    int        `json:"count"`
		Tasks    []taskView `json:"tasks"`
	}
	text, _ = call(t, handleListTasks, nil)
	if l := decode[list](t, text); l.TimeZone != "UTC" || l.Count != 1 || l.Tasks[0].ID != id || l.Tasks[0].LastRun != nil {
		t.Fatalf("list_tasks = %+v", l)
	}

	text, isErr = call(t, handleRunTask, map[string]any{"id": id})
	if isErr {
		t.Fatalf("run_task: %s", text)
	}
	if r := decode[runResult](t, text); r.ID != id || r.Stdout != "hola" || r.ExitCode != 0 || r.Trigger != triggerManual {
		t.Fatalf("run_task = %+v", r)
	}

	text, _ = call(t, handleListTasks, nil)
	if last := decode[list](t, text).Tasks[0].LastRun; last == nil || !last.OK || last.Trigger != triggerManual {
		t.Fatalf("last_run = %+v", last)
	}

	type history struct {
		ID    string      `json:"id"`
		Count int         `json:"count"`
		Runs  []runRecord `json:"runs"`
	}
	text, _ = call(t, handleTaskHistory, map[string]any{"id": id})
	if h := decode[history](t, text); h.Count != 1 || h.Runs[0].Stdout != "hola" {
		t.Fatalf("task_history = %+v", h)
	}

	text, isErr = call(t, handleDeleteTask, map[string]any{"id": id})
	if isErr || !strings.Contains(text, `"deleted":"`+id+`"`) {
		t.Fatalf("delete_task: %s", text)
	}
	for name, h := range map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error){
		"run_task": handleRunTask, "task_history": handleTaskHistory, "delete_task": handleDeleteTask,
	} {
		if text, isErr := call(t, h, map[string]any{"id": id}); !isErr || !strings.Contains(text, "not found") {
			t.Errorf("%s after delete: %s", name, text)
		}
	}
}

// A failed run is an error result that still carries the run, including a
// command only refused when it runs.
func TestRunTaskFailuresAreErrorResults(t *testing.T) {
	requireCommands(t, "cat", "ls")
	withServer(t, "cat", time.UTC)
	for script, check := range map[string]func(runResult) bool{
		"echo boom >&2; exit 4": func(r runResult) bool { return r.ExitCode == 4 && r.Stderr == "boom\n" },
		"c=ls; $c":              func(r runResult) bool { return r.ExitCode == 126 && strings.Contains(r.Error, "not in "+allowlistEnv) },
	} {
		text, isErr := call(t, handleCreateTask, map[string]any{"script": script, "cron": "@daily"})
		if isErr {
			t.Fatalf("create %q: %s", script, text)
		}
		id := decode[taskView](t, text).ID
		text, isErr = call(t, handleRunTask, map[string]any{"id": id})
		if !isErr || !check(decode[runResult](t, text)) {
			t.Errorf("%q: run_task = %s (error=%v)", script, text, isErr)
		}
	}
}

func TestCreateTaskCronUsesServerTimeZone(t *testing.T) {
	bogota, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatal(err)
	}
	withServer(t, "cat", bogota) // the clock is t0 = 10:00 UTC = 05:00 in Bogotá
	text, isErr := call(t, handleCreateTask, map[string]any{"script": "echo hi", "cron": "0 8 * * *"})
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(text, `"next_run":"2026-10-06T08:00:00-05:00"`) {
		t.Fatalf("next_run not 08:00 Bogotá: %s", text)
	}
}

func TestTaskHistoryLimit(t *testing.T) {
	s := withServer(t, "cat", time.UTC)
	tk := intervalTask(t, "a1", time.Hour)
	if err := s.store.add(tk); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.store.appendRun("a1", runRecord{ExitCode: i}); err != nil {
			t.Fatal(err)
		}
	}
	for limit, want := range map[float64]int{2: 2, 0: 5, -1: 5, 1000: 5} {
		text, _ := call(t, handleTaskHistory, map[string]any{"id": "a1", "limit": limit})
		got := decode[struct {
			Runs []runRecord `json:"runs"`
		}](t, text)
		if len(got.Runs) != want || got.Runs[0].ExitCode != 4 {
			t.Errorf("limit %v: got %d runs (newest exit %d), want %d newest first", limit, len(got.Runs), got.Runs[0].ExitCode, want)
		}
	}
}

// main must refuse to start without each safeguard. The checks live in main,
// which exits, so the test re-runs its own binary as a child that calls main.
// DROIDMCP_PORT=1 makes a missing check fail on bind instead of serving.
func TestMainRefusesUnsafeStart(t *testing.T) {
	if os.Getenv("DROIDMCP_TEST_RUN_MAIN") == "1" {
		main()
		return
	}
	corrupt := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"DROIDMCP_TEST_RUN_MAIN=1", "DROIDMCP_PORT=1", "HOME=" + t.TempDir()}
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"no API key":       {[]string{allowlistEnv + "=cat"}, "requires DROIDMCP_AUTOMATION_KEY"},
		"no allowlist":     {[]string{"DROIDMCP_API_KEY=k"}, "requires " + allowlistEnv},
		"blank allowlist":  {[]string{"DROIDMCP_API_KEY=k", allowlistEnv + "= , "}, "requires " + allowlistEnv},
		"relative entry":   {[]string{"DROIDMCP_API_KEY=k", allowlistEnv + "=bin/tool"}, "must be a command name or an absolute path"},
		"unknown timezone": {[]string{"DROIDMCP_API_KEY=k", allowlistEnv + "=cat", tzEnv + "=Mars/Olympus_Mons"}, "invalid " + tzEnv},
		"corrupt store":    {[]string{"DROIDMCP_API_KEY=k", allowlistEnv + "=cat", storeEnv + "=" + corrupt}, "corrupt"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainRefusesUnsafeStart$")
			cmd.Env = append(append([]string{}, base...), tc.env...)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("main: %v, want exit status 1\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("main exited for another reason (want %q):\n%s", tc.want, out)
			}
		})
	}
}

// The published schema is all a calling agent sees: pin the five tools and
// their required arguments.
func TestRegisterToolsPublishesTheWholeSurface(t *testing.T) {
	want := map[string][]string{
		"create_task":  {"script"},
		"list_tasks":   nil,
		"run_task":     {"id"},
		"delete_task":  {"id"},
		"task_history": {"id"},
	}
	s := core.NewDroidServer("mcp-automation", "test")
	registerTools(s)
	registered := s.MCPServer.ListTools()
	if len(registered) != len(want) {
		t.Errorf("registered %d tools, want %d", len(registered), len(want))
	}
	for name, required := range want {
		st, ok := registered[name]
		if !ok {
			t.Errorf("tool %q is not registered", name)
			continue
		}
		if !slices.Equal(st.Tool.InputSchema.Required, required) {
			t.Errorf("%s: required = %v, want %v", name, st.Tool.InputSchema.Required, required)
		}
		if strings.TrimSpace(st.Tool.Description) == "" {
			t.Errorf("%s: has no description", name)
		}
	}
	if props := registered["create_task"].Tool.InputSchema.Properties; props["cron"] == nil || props["interval_seconds"] == nil || props["env_extra"] == nil {
		t.Errorf("create_task schema is missing schedule or env arguments: %v", props)
	}
}
