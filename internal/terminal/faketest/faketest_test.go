package faketest_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"relist-panel/internal/terminal"
	"relist-panel/internal/terminal/faketest"
)

// The contract downstream HTTP-seam tests rely on: listings and shell probes
// come back exactly as scripted, a missing key reads as absent, and Available
// answers with its scripted error.
func TestExecAnswersScriptedListings(t *testing.T) {
	fake := &faketest.Exec{
		Containers: map[string][]terminal.Container{
			"relist-c1/app": {{ID: "c1", Name: "relist-c1-app-1", Number: 1}},
		},
		Shells: map[string]bool{"c1:/bin/bash": true},
	}

	containers, err := fake.ServiceContainers(context.Background(), "relist-c1", "app")
	if err != nil {
		t.Fatalf("ServiceContainers() error = %v", err)
	}
	if len(containers) != 1 || containers[0].ID != "c1" || containers[0].Name != "relist-c1-app-1" || containers[0].Number != 1 {
		t.Fatalf("containers = %+v, want the scripted one", containers)
	}
	if absent, err := fake.ServiceContainers(context.Background(), "relist-c1", "worker"); err != nil || absent != nil {
		t.Fatalf("unknown service = %+v, %v, want nil, nil", absent, err)
	}

	if bash, err := fake.HasShell(context.Background(), "c1", "/bin/bash"); err != nil || !bash {
		t.Fatalf("HasShell(bash) = %v, %v, want true, nil", bash, err)
	}
	if sh, err := fake.HasShell(context.Background(), "c1", "/bin/sh"); err != nil || sh {
		t.Fatalf("HasShell(sh) = %v, %v, want false, nil", sh, err)
	}

	if err := fake.Available(context.Background()); err != nil {
		t.Fatalf("Available() error = %v, want nil", err)
	}
	fake.AvailableErr = errors.New("docker socket not reachable")
	if err := fake.Available(context.Background()); !errors.Is(err, fake.AvailableErr) {
		t.Fatalf("Available() error = %v, want the scripted error", err)
	}
}

// Start records the call and returns a session whose two sides are the net.Pipe
// pair: bytes written to the session are readable on Remote, and vice versa.
func TestStartRecordsCallsAndPipesBytes(t *testing.T) {
	fake := &faketest.Exec{ExitCode: 7}
	session, err := fake.Start(context.Background(), "c1", terminal.ExecOptions{Shell: "/bin/bash", User: "0", Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(fake.Starts) != 1 {
		t.Fatalf("Starts = %d, want 1", len(fake.Starts))
	}
	call := fake.Starts[0]
	if call.ContainerID != "c1" || call.Opts.Shell != "/bin/bash" || call.Opts.User != "0" || call.Opts.Rows != 24 || call.Opts.Cols != 80 {
		t.Fatalf("Start call = %+v, want the passed options", call)
	}

	fakeSession, ok := session.(*faketest.Session)
	if !ok {
		t.Fatalf("Start returned %T, want *faketest.Session", session)
	}

	go func() { _, _ = fakeSession.Write([]byte("ls\r")) }()
	fromPanel := make([]byte, 3)
	if _, err := io.ReadFull(fakeSession.Remote, fromPanel); err != nil {
		t.Fatalf("read on Remote: %v", err)
	}
	if string(fromPanel) != "ls\r" {
		t.Fatalf("Remote read %q, want %q", fromPanel, "ls\r")
	}

	go func() { _, _ = fakeSession.Remote.Write([]byte("out")) }()
	toPanel := make([]byte, 3)
	if _, err := io.ReadFull(fakeSession, toPanel); err != nil {
		t.Fatalf("read on session: %v", err)
	}
	if string(toPanel) != "out" {
		t.Fatalf("session read %q, want %q", toPanel, "out")
	}

	if err := fakeSession.Resize(context.Background(), 40, 120); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	if resizes := fakeSession.ResizesSnapshot(); len(resizes) != 1 || resizes[0] != [2]uint16{40, 120} {
		t.Fatalf("Resizes = %v, want [[40 120]]", resizes)
	}

	if code, err := fakeSession.ExitCode(context.Background()); err != nil || code != 7 {
		t.Fatalf("ExitCode() = %d, %v, want 7, nil", code, err)
	}

	select {
	case <-fakeSession.Closed():
		t.Fatal("Closed signalled before Close")
	default:
	}
	if err := fakeSession.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-fakeSession.Closed():
	default:
		t.Fatal("Closed not signalled after Close")
	}
}

// A scripted Start error is returned as-is, and nothing is recorded.
func TestStartReturnsTheScriptedError(t *testing.T) {
	fake := &faketest.Exec{StartErr: errors.New("docker api /containers/x/exec: 409 Conflict: container is paused")}
	if _, err := fake.Start(context.Background(), "c1", terminal.ExecOptions{}); !errors.Is(err, fake.StartErr) {
		t.Fatalf("Start() error = %v, want the scripted error", err)
	}
	if len(fake.Starts) != 0 {
		t.Fatalf("Starts = %d after a failed Start, want 0", len(fake.Starts))
	}
}
