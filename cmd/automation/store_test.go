package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var t0 = time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)

// intervalTask is a valid task that fires every `every`, next at t0+every.
func intervalTask(t *testing.T, id string, every time.Duration) task {
	t.Helper()
	sch, err := parseSchedule("", every, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return task{
		ID: id, Script: "echo " + id, IntervalSeconds: int(every / time.Second),
		TimeoutSeconds: 60, CreatedAt: t0, NextRun: t0.Add(every), sched: sch,
	}
}

func newStore(t *testing.T) (*store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "tasks.json")
	s, err := openStore(path, time.UTC, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreRoundTrip(t *testing.T) {
	s, path := newStore(t)
	if s.count() != 0 {
		t.Fatal("a missing file should give an empty store")
	}
	if err := s.add(intervalTask(t, "a1", time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.appendRun("a1", runRecord{Trigger: triggerManual, StartedAt: t0, Stdout: "hi"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("store mode = %v, want 0600", info.Mode().Perm())
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("store directory mode = %v, want 0700", dir.Mode().Perm())
	}

	reopened, err := openStore(path, time.UTC, t0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.get("a1")
	if !ok || got.Script != "echo a1" || !got.NextRun.Equal(t0.Add(time.Hour)) || got.sched.every != time.Hour {
		t.Fatalf("reloaded task = %+v, %v", got, ok)
	}
	runs, err := reopened.runs("a1", 10)
	if err != nil || len(runs) != 1 || runs[0].Stdout != "hi" {
		t.Fatalf("reloaded history = %+v, %v", runs, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".automation-tasks-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// A corrupt store must stop the server instead of being replaced by an empty
// one on the next save.
func TestOpenStoreRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	for _, content := range []string{
		"{not json",
		`{"version":1,"tasks":[{"id":"x","script":"echo","cron":"99 * * * *"}]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := openStore(path, time.UTC, t0); err == nil {
			t.Errorf("accepted %q", content)
		}
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Fatalf("store file was modified: %q", data)
		}
	}
}

// Runs missed while the server was down are skipped, like cron, and interval
// tasks keep their phase.
func TestOpenStoreSkipsMissedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	file := storeFile{Version: 1, Tasks: []*task{
		{ID: "every", Script: "echo", IntervalSeconds: 3600, NextRun: t0},
		{ID: "cron", Script: "echo", Cron: "0 9 * * *", NextRun: t0.Add(-24 * time.Hour)},
		{ID: "future", Script: "echo", IntervalSeconds: 3600, NextRun: t0.Add(10 * time.Hour)},
	}}
	data, _ := json.Marshal(file)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(150 * time.Minute) // 12:30
	s, err := openStore(path, time.UTC, now)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]time.Time{
		"every":  t0.Add(3 * time.Hour),                        // 13:00, still on the hour
		"cron":   time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), // tomorrow, not now
		"future": t0.Add(10 * time.Hour),                       // untouched
	} {
		if got, _ := s.get(id); !got.NextRun.Equal(want) {
			t.Errorf("%s: NextRun = %v, want %v", id, got.NextRun, want)
		}
	}
}

func TestTakeDueAdvancesNextRun(t *testing.T) {
	s, _ := newStore(t)
	for _, tk := range []task{intervalTask(t, "hourly", time.Hour), intervalTask(t, "daily", 24*time.Hour)} {
		if err := s.add(tk); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.takeDue(t0.Add(time.Hour))
	if err != nil || len(due) != 1 || due[0].ID != "hourly" {
		t.Fatalf("due = %+v, %v; want only hourly", due, err)
	}
	if got, _ := s.get("hourly"); !got.NextRun.Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("hourly NextRun = %v, want the following hour", got.NextRun)
	}
	if again, _ := s.takeDue(t0.Add(time.Hour)); len(again) != 0 {
		t.Errorf("a task fired twice for the same slot: %+v", again)
	}
	if next, ok := s.earliestNext(); !ok || !next.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("earliestNext = %v, %v", next, ok)
	}
}

func TestAppendRunCapsHistoryAndOutput(t *testing.T) {
	s, _ := newStore(t)
	if err := s.add(intervalTask(t, "a1", time.Hour)); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 3*storedOutputBytes) + "END"
	for i := 0; i < historyPerTask+5; i++ {
		rec := runRecord{Trigger: triggerSchedule, StartedAt: t0.Add(time.Duration(i) * time.Minute), ExitCode: i, Stdout: big, Stderr: "e"}
		if err := s.appendRun("a1", rec); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.runs("a1", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != historyPerTask || runs[0].ExitCode != historyPerTask+4 {
		t.Fatalf("kept %d runs, newest exit %d; want %d runs, newest first", len(runs), runs[0].ExitCode, historyPerTask)
	}
	r := runs[0]
	if len(r.Stdout) > storedOutputBytes || !strings.HasSuffix(r.Stdout, "END") || !r.Truncated {
		t.Errorf("stored stdout: len %d, truncated %v; want the tail, at most %d bytes", len(r.Stdout), r.Truncated, storedOutputBytes)
	}
	if r.Stderr != "e" {
		t.Errorf("short stderr changed: %q", r.Stderr)
	}
	if limited, _ := s.runs("a1", 3); len(limited) != 3 {
		t.Errorf("limit 3 returned %d runs", len(limited))
	}
}

func TestStoreLimitsAndMissingTasks(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < maxTasks; i++ {
		if err := s.add(intervalTask(t, fmt.Sprintf("t%02d", i), time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.add(intervalTask(t, "extra", time.Hour)); err == nil {
		t.Fatalf("accepted task number %d", maxTasks+1)
	}
	if err := s.add(intervalTask(t, "t00", time.Hour)); err == nil {
		t.Fatal("accepted a duplicate id")
	}
	if err := s.remove("t00"); err != nil {
		t.Fatal(err)
	}
	if err := s.remove("t00"); err != errTaskNotFound {
		t.Errorf("second remove = %v, want errTaskNotFound", err)
	}
	if _, err := s.runs("t00", 5); err != errTaskNotFound {
		t.Errorf("runs of a deleted task = %v, want errTaskNotFound", err)
	}
	// A run that finishes after its task was deleted is dropped.
	if err := s.appendRun("t00", runRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.history["t00"]; ok {
		t.Error("history recreated for a deleted task")
	}
}

func TestTailKeepsRuneBoundary(t *testing.T) {
	got, cut := tail(strings.Repeat("ñ", 10), 5)
	if !cut || !utf8.ValidString(got) || len(got) > 5 {
		t.Fatalf("tail = %q (cut %v)", got, cut)
	}
	if got, cut := tail("short", 10); got != "short" || cut {
		t.Fatalf("tail of a short string = %q (cut %v)", got, cut)
	}
}
