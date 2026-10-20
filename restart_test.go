package main

import (
	"errors"
	"slices"
	"testing"
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
