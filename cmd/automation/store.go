package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// task is a scheduled script. NextRun is persisted so an interval task keeps
// its phase across restarts; sched is rebuilt from Cron/IntervalSeconds.
type task struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	Script          string            `json:"script"`
	Cron            string            `json:"cron,omitempty"`
	IntervalSeconds int               `json:"interval_seconds,omitempty"`
	Cwd             string            `json:"cwd,omitempty"`
	EnvExtra        map[string]string `json:"env_extra,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	CreatedAt       time.Time         `json:"created_at"`
	NextRun         time.Time         `json:"next_run"`

	sched schedule
}

// runRecord is one execution of a task, as kept in its history.
type runRecord struct {
	Trigger    string    `json:"trigger"` // "schedule" or "manual"
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	ExitCode   int       `json:"exit_code"`
	TimedOut   bool      `json:"timed_out,omitempty"`
	Cancelled  bool      `json:"cancelled,omitempty"`
	Error      string    `json:"error,omitempty"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	Truncated  bool      `json:"truncated,omitempty"`
}

func (r runRecord) ok() bool {
	return r.ExitCode == 0 && !r.TimedOut && !r.Cancelled && r.Error == ""
}

// storeFile is the on-disk layout.
type storeFile struct {
	Version int                    `json:"version"`
	Tasks   []*task                `json:"tasks"`
	History map[string][]runRecord `json:"history,omitempty"`
}

// store keeps tasks and their run history in one JSON file, rewritten
// atomically on every change. History is capped per task and its output is
// trimmed, so the file stays small however often tasks run.
type store struct {
	path    string
	mu      sync.Mutex
	tasks   map[string]*task
	history map[string][]runRecord // oldest first
}

var errTaskNotFound = errors.New("task not found")

// storePath is where tasks live: DROIDMCP_AUTOMATION_DB if set, otherwise
// ~/.droidmcp/automation-tasks.json.
func storePath() string {
	if p := strings.TrimSpace(os.Getenv(storeEnv)); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".droidmcp", "automation-tasks.json")
}

// openStore loads path. A missing file is an empty store; an unreadable or
// corrupt one is an error, because carrying on would overwrite the operator's
// tasks with an empty list on the next save. Runs missed while the server was
// down are skipped, as cron does: NextRun moves to the next time after now.
func openStore(path string, loc *time.Location, now time.Time) (*store, error) {
	s := &store{path: path, tasks: map[string]*task{}, history: map[string][]runRecord{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task store: %w", err)
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("task store %s is corrupt (move it aside to start empty): %w", path, err)
	}
	for _, t := range file.Tasks {
		if t == nil || t.ID == "" {
			continue
		}
		sched, err := parseSchedule(t.Cron, time.Duration(t.IntervalSeconds)*time.Second, loc)
		if err != nil {
			return nil, fmt.Errorf("task %s in %s: %w", t.ID, path, err)
		}
		t.sched = sched
		if !t.NextRun.After(now) {
			t.NextRun = sched.next(now, t.NextRun)
		}
		s.tasks[t.ID] = t
	}
	for id, runs := range file.History {
		if _, ok := s.tasks[id]; ok {
			s.history[id] = runs
		}
	}
	return s, nil
}

// saveLocked writes the store atomically: a temp file in the same directory,
// fsynced and renamed over the old one. Callers hold s.mu.
func (s *store) saveLocked() error {
	file := storeFile{Version: 1, Tasks: s.sortedLocked(), History: s.history}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".automation-tasks-*.json")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), s.path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

func (s *store) sortedLocked() []*task {
	out := make([]*task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// copyTask returns a copy that shares nothing mutable with the stored task.
func copyTask(t *task) task {
	out := *t
	if t.EnvExtra != nil {
		out.EnvExtra = make(map[string]string, len(t.EnvExtra))
		for k, v := range t.EnvExtra {
			out.EnvExtra[k] = v
		}
	}
	return out
}

// add stores t, refusing duplicates and more than maxTasks tasks.
func (s *store) add(t task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) >= maxTasks {
		return fmt.Errorf("task limit reached (%d); delete a task first", maxTasks)
	}
	if _, dup := s.tasks[t.ID]; dup {
		return fmt.Errorf("task id %s already exists", t.ID)
	}
	s.tasks[t.ID] = &t
	if err := s.saveLocked(); err != nil {
		delete(s.tasks, t.ID)
		return fmt.Errorf("save task store: %w", err)
	}
	return nil
}

// remove deletes a task and its history.
func (s *store) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return errTaskNotFound
	}
	runs := s.history[id]
	delete(s.tasks, id)
	delete(s.history, id)
	if err := s.saveLocked(); err != nil {
		s.tasks[id], s.history[id] = t, runs
		return fmt.Errorf("save task store: %w", err)
	}
	return nil
}

func (s *store) get(id string) (task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return task{}, false
	}
	return copyTask(t), true
}

func (s *store) list() []task {
	s.mu.Lock()
	defer s.mu.Unlock()
	sorted := s.sortedLocked()
	out := make([]task, len(sorted))
	for i, t := range sorted {
		out[i] = copyTask(t)
	}
	return out
}

func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

// takeDue returns the tasks due at now and moves each one's NextRun to its
// following fire time, so a slow run cannot make the scheduler fire it twice.
func (s *store) takeDue(now time.Time) ([]task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []task
	for _, t := range s.sortedLocked() {
		if t.NextRun.IsZero() || t.NextRun.After(now) {
			continue
		}
		due = append(due, copyTask(t))
		t.NextRun = t.sched.next(now, t.NextRun)
	}
	if len(due) == 0 {
		return nil, nil
	}
	return due, s.saveLocked()
}

// earliestNext returns the soonest NextRun among all tasks.
func (s *store) earliestNext() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Time
	for _, t := range s.tasks {
		if !t.NextRun.IsZero() && (next.IsZero() || t.NextRun.Before(next)) {
			next = t.NextRun
		}
	}
	return next, !next.IsZero()
}

// appendRun records a finished run. Output is trimmed to its last
// storedOutputBytes per stream (errors tend to be at the end) and only the
// newest historyPerTask runs are kept. A run of a task deleted meanwhile is
// dropped.
func (s *store) appendRun(id string, rec runRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return nil
	}
	var cut bool
	rec.Stdout, cut = tail(rec.Stdout, storedOutputBytes)
	rec.Truncated = rec.Truncated || cut
	rec.Stderr, cut = tail(rec.Stderr, storedOutputBytes)
	rec.Truncated = rec.Truncated || cut
	runs := append(s.history[id], rec)
	if len(runs) > historyPerTask {
		runs = append([]runRecord(nil), runs[len(runs)-historyPerTask:]...)
	}
	s.history[id] = runs
	return s.saveLocked()
}

// runs returns up to limit runs of a task, newest first.
func (s *store) runs(id string, limit int) ([]runRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return nil, errTaskNotFound
	}
	stored := s.history[id]
	out := make([]runRecord, 0, min(limit, len(stored)))
	for i := len(stored) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, stored[i])
	}
	return out, nil
}

// tail returns the last n bytes of s, starting at a rune boundary, and whether
// anything was cut.
func tail(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:], true
}
