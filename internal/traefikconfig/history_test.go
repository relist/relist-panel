package traefikconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestHistoryPrunedTo10(t *testing.T) {
	tr := newTestRunner(t, "")
	for i := 0; i < 12; i++ {
		tr.apply(t, fmt.Sprintf("log: {level: %s}\n", LogLevels[i%len(LogLevels)]))
	}
	entries, err := tr.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("history has %d entries", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Name > entries[i-1].Name {
			t.Fatalf("not newest first: %v", entries)
		}
	}
	if entries[0].Result != StateOK || entries[0].Kind != KindApply {
		t.Errorf("entry = %+v", entries[0])
	}
}

func TestRestoreRejectsBadName(t *testing.T) {
	tr := newTestRunner(t, "")
	for _, name := range []string{"../x", "20261104T000000Z-ABCDEFGH", ""} {
		if _, err := tr.Restore(context.Background(), name); !errors.Is(err, ErrNoHistory) {
			t.Errorf("%q: err = %v", name, err)
		}
	}
}

func TestRestoreAppliesEntry(t *testing.T) {
	tr := newTestRunner(t, "log: {level: WARN}\n")
	tr.apply(t, "log: {level: DEBUG}\n")
	entries, _ := tr.History()
	if _, err := tr.Restore(context.Background(), entries[0].Name); err != nil {
		t.Fatal(err)
	}
	run := tr.wait(t)
	if run.State != StateOK || run.Kind != KindRestore {
		t.Fatalf("run = %+v", run)
	}
	b, _ := os.ReadFile(tr.p.Override())
	if string(b) != "log: {level: WARN}\n" {
		t.Fatalf("override = %q", b)
	}
}
