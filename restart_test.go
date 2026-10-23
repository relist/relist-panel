package main

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestReexecRunsTheSameBinaryWithTheSameArgs(t *testing.T) {
	var gotExe string
	var gotArgs, gotEnv []string
	exited := -1
	reexec("/usr/local/bin/relist-panel", []string{"relist-panel", "--insecure-migration-source"}, []string{"A=1"},
		func(exe string, args, env []string) error {
			gotExe, gotArgs, gotEnv = exe, args, env
			return nil
		}, func(code int) { exited = code })
	if gotExe != "/usr/local/bin/relist-panel" || !slices.Equal(gotArgs, []string{"relist-panel", "--insecure-migration-source"}) || !slices.Equal(gotEnv, []string{"A=1"}) {
		t.Fatalf("exec(%q, %q, %q)", gotExe, gotArgs, gotEnv)
	}
	if exited != -1 {
		t.Fatalf("exit(%d) after a successful exec", exited)
	}
}

func TestReexecFallsBackToExitOne(t *testing.T) {
	exited := -1
	reexec("/x", []string{"x"}, nil, func(string, []string, []string) error { return errors.New("exec format error") },
		func(code int) { exited = code })
	if exited != 1 {
		t.Fatalf("exit(%d), want exit(1) when exec fails", exited)
	}
}

func TestRestartAfterAHungShutdownStillExecutes(t *testing.T) {
	hung := make(chan struct{})
	defer close(hung)
	var order []string
	done := make(chan struct{})
	go func() {
		restartAfter(20*time.Millisecond, func() { <-hung }, func() error { order = append(order, "close"); return nil },
			func() { order = append(order, "exec") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a hung shutdown kept the panel from re-executing")
	}
	if !slices.Equal(order, []string{"close", "exec"}) {
		t.Fatalf("order = %q, want close then exec", order)
	}
}

func TestRestartAfterShutdownRunsInOrder(t *testing.T) {
	var order []string
	restartAfter(time.Second, func() { order = append(order, "shutdown") }, func() error { order = append(order, "close"); return nil },
		func() { order = append(order, "exec") })
	if !slices.Equal(order, []string{"shutdown", "close", "exec"}) {
		t.Fatalf("order = %q", order)
	}
}
