package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kahz12/droidmcp/internal/logger"
)

const (
	triggerSchedule = "schedule"
	triggerManual   = "manual"
)

var errAlreadyRunning = errors.New("task is already running")

// scheduler fires tasks from the store while the server runs. Tasks only run
// while this process is alive, exactly like every other DroidMCP server; keep
// it up with tmux or Termux:Boot plus termux-wake-lock.
type scheduler struct {
	store *store
	allow allowlist
	loc   *time.Location
	now   func() time.Time
	// runFn executes one run; tests replace it to avoid real processes.
	runFn func(ctx context.Context, t task, trigger string) runRecord

	// life ends with the server; every run is tied to it (see shutdown).
	life context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wake    chan struct{}
	wg      sync.WaitGroup
}

func newScheduler(st *store, allow allowlist, loc *time.Location) *scheduler {
	s := &scheduler{
		store:   st,
		allow:   allow,
		loc:     loc,
		now:     time.Now,
		running: map[string]context.CancelFunc{},
		wake:    make(chan struct{}, 1),
	}
	s.life, s.stop = context.WithCancel(context.Background())
	s.runFn = s.execute
	return s
}

// shutdown stops the loop and every run in progress, then waits (up to
// shutdownGrace) for them to end. Runs live in their own process groups, which
// do not die with the server, so without this a long task would outlive it.
func (s *scheduler) shutdown() {
	s.stop()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		logger.Warn("task runs still stopping at shutdown", "grace", shutdownGrace.String())
	}
}

// loop sleeps until the next task is due and fires it. It never sleeps longer
// than maxSleep and compares wall-clock times on every wake-up: Go timers run
// on the monotonic clock, which stops while Android suspends, so a single long
// timer could fire hours late.
func (s *scheduler) loop(ctx context.Context) {
	for {
		wait := maxSleep
		if next, ok := s.store.earliestNext(); ok {
			if d := next.Sub(s.now()); d < wait {
				wait = max(d, 0)
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
			s.fireDue(ctx)
		}
	}
}

// poke makes loop recompute its sleep after a task is added or removed.
func (s *scheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// fireDue starts every task that is due. A task still running from its
// previous fire time is skipped rather than run twice at once.
func (s *scheduler) fireDue(ctx context.Context) {
	due, err := s.store.takeDue(s.now())
	if err != nil {
		logger.Warn("could not persist task schedule", "error", err.Error())
	}
	for _, t := range due {
		if _, err := s.start(ctx, t, triggerSchedule); err != nil {
			logger.Warn("skipping scheduled run", "task", t.ID, "reason", err.Error())
		}
	}
}

// start runs t in the background unless it is already running, records the
// result in its history, and delivers it on the returned channel.
func (s *scheduler) start(ctx context.Context, t task, trigger string) (<-chan runRecord, error) {
	s.mu.Lock()
	if _, busy := s.running[t.ID]; busy {
		s.mu.Unlock()
		return nil, errAlreadyRunning
	}
	rctx, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.life, cancel)
	s.running[t.ID] = cancel
	s.mu.Unlock()

	done := make(chan runRecord, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		rec := s.runFn(rctx, t, trigger)
		unlink()
		cancel()
		s.mu.Lock()
		delete(s.running, t.ID)
		s.mu.Unlock()
		if err := s.store.appendRun(t.ID, rec); err != nil {
			logger.Warn("could not save task run", "task", t.ID, "error", err.Error())
		}
		logger.Info("task run finished", "task", t.ID, "trigger", trigger, "exit_code", rec.ExitCode, "duration_ms", rec.DurationMs, "ok", rec.ok())
		done <- rec
	}()
	return done, nil
}

// runNow runs a task immediately for run_task and waits for it. Cancelling ctx
// (the tool call) stops the run.
func (s *scheduler) runNow(ctx context.Context, id string) (runRecord, error) {
	t, ok := s.store.get(id)
	if !ok {
		return runRecord{}, errTaskNotFound
	}
	done, err := s.start(ctx, t, triggerManual)
	if err != nil {
		return runRecord{}, err
	}
	return <-done, nil
}

// remove deletes a task and stops it if it is running. It reports whether a
// run was stopped.
func (s *scheduler) remove(id string) (bool, error) {
	if err := s.store.remove(id); err != nil {
		return false, err
	}
	s.mu.Lock()
	cancel, wasRunning := s.running[id]
	s.mu.Unlock()
	if wasRunning {
		cancel()
	}
	s.poke()
	return wasRunning, nil
}

func (s *scheduler) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

// execute is the real runFn: it runs the task's script under the allowlist.
func (s *scheduler) execute(ctx context.Context, t task, trigger string) runRecord {
	started := s.now()
	res := runScript(ctx, s.allow, scriptOptions{
		Script:   t.Script,
		Cwd:      t.Cwd,
		EnvExtra: t.EnvExtra,
		Timeout:  time.Duration(t.TimeoutSeconds) * time.Second,
		MaxBytes: captureBytes,
	})
	return runRecord{
		Trigger:    trigger,
		StartedAt:  started,
		DurationMs: s.now().Sub(started).Milliseconds(),
		ExitCode:   res.ExitCode,
		TimedOut:   res.TimedOut,
		Cancelled:  res.Cancelled,
		Error:      res.Error,
		Stdout:     res.Stdout,
		Stderr:     res.Stderr,
		Truncated:  res.Truncated,
	}
}

// androidTimeZone is overridable in tests. It reads the zone Android is set
// to, or "" when getprop is unavailable (any non-Android host).
var androidTimeZone = func() string {
	out, err := exec.Command("getprop", "persist.sys.timezone").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// taskLocation picks the time zone cron expressions are evaluated in:
// DROIDMCP_AUTOMATION_TZ, then TZ, then Android's own setting. A Go binary
// built for linux does not see Android's zone by itself (there is no
// /etc/localtime), so without the last step "0 8 * * *" would fire at 08:00
// UTC.
func taskLocation() (*time.Location, error) {
	if name := strings.TrimSpace(os.Getenv(tzEnv)); name != "" {
		return time.LoadLocation(name)
	}
	if os.Getenv("TZ") != "" {
		return time.Local, nil
	}
	if name := androidTimeZone(); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	return time.Local, nil
}
