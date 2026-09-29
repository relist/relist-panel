package faketest_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"relist-panel/internal/volumes"
	"relist-panel/internal/volumes/faketest"
)

// The contract downstream HTTP-seam tests rely on: every Run is recorded
// (Request plus the bytes drained from stdin), and the scripted stdout and
// error come back.
func TestRunnerRecordsCallsAndScriptsAnswers(t *testing.T) {
	fake := &faketest.Runner{}
	req := volumes.Request{
		Source:   "demo_pgdata",
		User:     "0:0",
		Op:       volumes.OpWrite,
		Path:     "notes.txt",
		ReadOnly: false,
	}

	var stdout bytes.Buffer
	if err := fake.Run(context.Background(), req, strings.NewReader("hello"), &stdout); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("Calls = %d, want 1", len(fake.Calls))
	}
	call := fake.Calls[0]
	if call.Request != req {
		t.Errorf("recorded Request = %+v, want %+v", call.Request, req)
	}
	if string(call.Stdin) != "hello" {
		t.Errorf("recorded stdin = %q, want %q", call.Stdin, "hello")
	}
	if stdout.Len() != 0 {
		t.Errorf("unscripted stdout = %q, want empty", stdout.String())
	}

	fake.Stdout = `{"entries":[]}`
	fake.Err = errors.New("helper failed")
	if err := fake.Run(context.Background(), req, nil, &stdout); !errors.Is(err, fake.Err) {
		t.Fatalf("Run() error = %v, want the scripted error", err)
	}
	if stdout.String() != `{"entries":[]}` {
		t.Errorf("scripted stdout = %q, want %q", stdout.String(), `{"entries":[]}`)
	}
	if len(fake.Calls) != 2 {
		t.Errorf("Calls = %d after two Runs, want 2", len(fake.Calls))
	}
	if len(fake.Calls[1].Stdin) != 0 {
		t.Errorf("second call recorded stdin = %q, want empty", fake.Calls[1].Stdin)
	}
}
