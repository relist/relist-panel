package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseServe(t *testing.T) {
	var out, errb bytes.Buffer
	state := filepath.Join(t.TempDir(), "deadline")
	if code := run([]string{"serve", "--listen", ":0", "--deadline", "1900000000", "--state", state}, &out, &errb); code == 0 {
		t.Fatal("missing --target must fail")
	}
	if !strings.Contains(errb.String(), "--target") {
		t.Fatalf("stderr = %q, want it to name --target", errb.String())
	}

	// A deadline already past exits 0 without listening: ":1" would be denied
	// or taken if it tried, and the state file is never written.
	errb.Reset()
	past := time.Now().Add(-time.Minute).Unix()
	code := run([]string{"serve", "--listen", "127.0.0.1:1", "--target", "127.0.0.1:9", "--deadline", itoa(past), "--state", state}, &out, &errb)
	if code != 0 {
		t.Fatalf("past deadline exit = %d (%s), want 0", code, errb.String())
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("past deadline wrote the state file")
	}
}

func TestExtendWritesState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deadline")
	var out, errb bytes.Buffer
	if code := run([]string{"extend", "--state", p, "1900000000"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "1900000000" {
		t.Fatalf("state = %q", b)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code == 0 {
		t.Fatal("unknown command must fail")
	}
}
