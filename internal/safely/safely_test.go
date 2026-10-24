package safely

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestDoRecovers(t *testing.T) {
	buf := captureLog(t)
	Do("test job", func() { panic("boom") })
	out := buf.String()
	if !strings.Contains(out, "test job panicked: boom") {
		t.Fatalf("log = %q, want the job name and panic value", out)
	}
	if !strings.Contains(out, "goroutine") {
		t.Fatalf("log = %q, want a stack trace", out)
	}
}

func TestDoRunsFunctionWithoutPanic(t *testing.T) {
	ran := false
	Do("quiet", func() { ran = true })
	if !ran {
		t.Fatal("fn was not run")
	}
}

func TestCallReturnsPanicAsError(t *testing.T) {
	captureLog(t)
	err := Call("test job", func() error { panic("boom") })
	if err == nil || err.Error() != "test job panicked: boom" {
		t.Fatalf("err = %v, want \"test job panicked: boom\"", err)
	}
}

func TestCallPassesErrorThrough(t *testing.T) {
	want := errors.New("plain failure")
	if got := Call("job", func() error { return want }); got != want {
		t.Fatalf("err = %v, want %v", got, want)
	}
	if got := Call("job", func() error { return nil }); got != nil {
		t.Fatalf("err = %v, want nil", got)
	}
}
