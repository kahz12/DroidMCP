package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeRunner stands in for script execution. Each run reports on started and
// then waits for release (or for its context to end).
type fakeRunner struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	calls   []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{started: make(chan string, 16), release: make(chan struct{})}
}

func (f *fakeRunner) run(ctx context.Context, t task, trigger string) runRecord {
	f.mu.Lock()
	f.calls = append(f.calls, t.ID+":"+trigger)
	f.mu.Unlock()
	f.started <- t.ID
	select {
	case <-f.release:
		return runRecord{Trigger: trigger, StartedAt: t0}
	case <-ctx.Done():
		return runRecord{Trigger: trigger, StartedAt: t0, Cancelled: true, ExitCode: -1}
	}
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func waitStarted(t *testing.T, f *fakeRunner) string {
	t.Helper()
	select {
	case id := <-f.started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("no run started")
		return ""
	}
}

func newTestScheduler(t *testing.T, tasks ...task) (*scheduler, *fakeRunner) {
	t.Helper()
	st, _ := newStore(t)
	for _, tk := range tasks {
		if err := st.add(tk); err != nil {
			t.Fatal(err)
		}
	}
	s := newScheduler(st, allowlist{}, time.UTC)
	f := newFakeRunner()
	s.runFn = f.run
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runs still going after the test")
		}
	})
	return s, f
}

func TestFireDueRunsOnlyDueTasks(t *testing.T) {
	s, f := newTestScheduler(t, intervalTask(t, "hourly", time.Hour), intervalTask(t, "daily", 24*time.Hour))
	s.now = func() time.Time { return t0.Add(time.Hour) }
	close(f.release)

	s.fireDue(context.Background())
	if id := waitStarted(t, f); id != "hourly" {
		t.Fatalf("started %q, want hourly", id)
	}
	s.wg.Wait()
	if f.callCount() != 1 {
		t.Fatalf("calls = %v, want only hourly", f.calls)
	}
	runs, _ := s.store.runs("hourly", 5)
	if len(runs) != 1 || runs[0].Trigger != triggerSchedule {
		t.Fatalf("history = %+v, want one scheduled run", runs)
	}
}

// A task still running when its next slot comes is skipped, not started twice.
func TestFireDueSkipsTaskStillRunning(t *testing.T) {
	s, f := newTestScheduler(t, intervalTask(t, "slow", time.Hour))
	s.now = func() time.Time { return t0.Add(time.Hour) }

	s.fireDue(context.Background())
	waitStarted(t, f)
	s.now = func() time.Time { return t0.Add(2 * time.Hour) }
	s.fireDue(context.Background())
	// Bounded, so a broken check fails here instead of waiting for a run that
	// is never released.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.runNow(ctx, "slow"); err != errAlreadyRunning {
		t.Errorf("run_task on a running task = %v, want errAlreadyRunning", err)
	}
	close(f.release)
	s.wg.Wait()
	if n := f.callCount(); n != 1 {
		t.Fatalf("task ran %d times, want 1", n)
	}
}

func TestRemoveStopsRunningTask(t *testing.T) {
	s, f := newTestScheduler(t, intervalTask(t, "a1", time.Hour))
	done := make(chan runRecord, 1)
	go func() {
		rec, _ := s.runNow(context.Background(), "a1")
		done <- rec
	}()
	waitStarted(t, f)

	wasRunning, err := s.remove("a1")
	if err != nil || !wasRunning {
		t.Fatalf("remove = %v, %v; want a stopped run", wasRunning, err)
	}
	select {
	case rec := <-done:
		if !rec.Cancelled {
			t.Fatalf("run was not cancelled: %+v", rec)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deleting the task did not stop its run")
	}
	if _, err := s.remove("a1"); err != errTaskNotFound {
		t.Errorf("second remove = %v", err)
	}
}

func TestRunNowCancelledWithTheCall(t *testing.T) {
	s, f := newTestScheduler(t, intervalTask(t, "a1", time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-f.started
		cancel()
	}()
	rec, err := s.runNow(ctx, "a1")
	if err != nil || !rec.Cancelled || rec.Trigger != triggerManual {
		t.Fatalf("runNow = %+v, %v", rec, err)
	}
	if _, err := s.runNow(context.Background(), "missing"); err != errTaskNotFound {
		t.Errorf("unknown task = %v", err)
	}
}

// Shutdown stops runs of either kind, so their processes do not outlive the
// server.
func TestShutdownStopsRuns(t *testing.T) {
	// Only "sched" is due; "manual" runs through run_task.
	s, f := newTestScheduler(t, intervalTask(t, "sched", time.Hour), intervalTask(t, "manual", 24*time.Hour))
	s.now = func() time.Time { return t0.Add(time.Hour) }
	go s.loop(s.life)
	s.poke()
	manual := make(chan runRecord, 1)
	go func() {
		rec, _ := s.runNow(context.Background(), "manual")
		manual <- rec
	}()
	waitStarted(t, f)
	waitStarted(t, f)

	start := time.Now()
	s.shutdown()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v", elapsed)
	}
	select {
	case rec := <-manual:
		if !rec.Cancelled {
			t.Errorf("manual run not stopped: %+v", rec)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown left the manual run running")
	}
	runs, _ := s.store.runs("sched", 5)
	if len(runs) != 1 || !runs[0].Cancelled {
		t.Errorf("scheduled run not stopped: %+v", runs)
	}
}

// The real loop: a task added while the loop sleeps (poke) fires on time.
func TestLoopFiresTasksAddedWhileSleeping(t *testing.T) {
	s, f := newTestScheduler(t)
	close(f.release)
	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	go func() {
		s.loop(ctx)
		close(loopDone)
	}()
	defer func() {
		cancel()
		<-loopDone
	}()

	time.Sleep(50 * time.Millisecond) // let the loop settle into its long sleep
	tk := intervalTask(t, "soon", time.Hour)
	tk.NextRun = time.Now().Add(100 * time.Millisecond)
	if err := s.store.add(tk); err != nil {
		t.Fatal(err)
	}
	s.poke()
	if id := waitStarted(t, f); id != "soon" {
		t.Fatalf("started %q", id)
	}
}

func TestTaskLocation(t *testing.T) {
	prev := androidTimeZone
	t.Cleanup(func() { androidTimeZone = prev })
	androidTimeZone = func() string { return "Europe/Madrid" }

	t.Setenv(tzEnv, "America/Bogota")
	if loc, err := taskLocation(); err != nil || loc.String() != "America/Bogota" {
		t.Errorf("%s: %v, %v", tzEnv, loc, err)
	}
	t.Setenv(tzEnv, "Mars/Olympus_Mons")
	if _, err := taskLocation(); err == nil {
		t.Error("accepted an unknown zone")
	}

	t.Setenv(tzEnv, "")
	t.Setenv("TZ", "")
	if loc, _ := taskLocation(); loc.String() != "Europe/Madrid" {
		t.Errorf("Android zone not used: %v", loc)
	}
	androidTimeZone = func() string { return "" }
	if loc, _ := taskLocation(); loc != time.Local {
		t.Errorf("without getprop: %v, want time.Local", loc)
	}
	androidTimeZone = func() string { return "Not/A_Zone" }
	if loc, _ := taskLocation(); loc != time.Local {
		t.Errorf("with a bad Android zone: %v, want time.Local", loc)
	}
	t.Setenv("TZ", "UTC")
	androidTimeZone = func() string { return "Europe/Madrid" }
	if loc, _ := taskLocation(); loc != time.Local {
		t.Errorf("with TZ set: %v, want time.Local", loc)
	}
}
