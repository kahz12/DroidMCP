package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunShellPipe(t *testing.T) {
	opts := execOptions{Command: shellPath(), Args: []string{"-lc", "echo hello world | tr a-z A-Z"}}
	res, err := runCommand(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "HELLO WORLD") {
		t.Fatalf("got %q", res.Stdout)
	}
}

func TestBackgroundJob(t *testing.T) {
	m, err := startBackgroundShell("sleep 1; echo done", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := jobState(m); st != "running" {
		t.Fatalf("want running, got %s", st)
	}
	time.Sleep(2 * time.Second)
	if st, code := jobState(m); st != "exited" || code != 0 {
		t.Fatalf("want exited 0, got %s %d", st, code)
	}
}
