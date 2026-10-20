package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/deployments"
)

// logIndex is the position of the first entry whose message contains s, or -1.
func logIndex(log []LogEntry, s string) int {
	return slices.IndexFunc(log, func(e LogEntry) bool { return strings.Contains(e.Message, s) })
}

func TestActivityLogRecordsTheRun(t *testing.T) {
	f := newTargetFixture(t)
	f.src.cutOnce["volume-a"] = true
	f.connectAndStart(t)
	p := waitPhase(t, f.target, PhaseFinalizing)
	eventuallyTrue(t, "Begin", func() bool { return f.final.count() == 1 })
	p = f.target.Progress()

	order := []string{"Connected to", "Locking the old server", "Old server: stopping", "Copying volume-a", "Copied volume-a", "Finalizing"}
	last := -1
	for _, s := range order {
		i := logIndex(p.Log, s)
		if i < 0 || i < last {
			t.Fatalf("log is missing %q in order %q:\n%+v", s, order, p.Log)
		}
		last = i
	}
	i := slices.IndexFunc(p.Log, func(e LogEntry) bool { return e.Item == "volume-a" && e.Level == "warn" })
	if i < 0 {
		t.Fatalf("no warning for volume-a's failed attempt:\n%+v", p.Log)
	}
	if p.Log[i].At.IsZero() {
		t.Fatal("log entry has no time")
	}
}

func TestActivityLogKeepsTheLatestEntries(t *testing.T) {
	target := NewTarget(TargetDeps{StateDir: t.TempDir()})
	target.mu.Lock()
	for i := range maxLogEntries + 50 {
		target.record("info", "", fmt.Sprintf("entry %d", i))
	}
	target.mu.Unlock()
	log := target.Progress().Log
	if len(log) != maxLogEntries || log[0].Message != "entry 50" {
		t.Fatalf("log has %d entries starting %q, want %d from entry 50", len(log), log[0].Message, maxLogEntries)
	}
}

func TestDoneKeepsTheLog(t *testing.T) {
	f := newTargetFixture(t)
	f.connectAndStart(t)
	waitPhase(t, f.target, PhaseFinalizing)
	os.WriteFile(filepath.Join(f.deps.StateDir, markerName), []byte(`{}`), 0o600)
	restarted := NewTarget(f.deps)
	if err := restarted.ResumeIfPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := waitPhase(t, restarted, PhaseDone)
	for _, s := range []string{"Connected to", "restarted with the migrated database", "Migration complete"} {
		if logIndex(p.Log, s) < 0 {
			t.Fatalf("done log is missing %q:\n%+v", s, p.Log)
		}
	}
	// And after another restart, from summary.json.
	again := NewTarget(f.deps)
	if err := again.ResumeIfPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logIndex(again.Progress().Log, "Migration complete") < 0 {
		t.Fatal("the log did not survive the restart after done")
	}
}

func TestFinalizerReportsItsSteps(t *testing.T) {
	dataDir := t.TempDir()
	freshDatabase(t, dataDir)
	var mu sync.Mutex
	var steps []FinalizeStep
	ctx := WithFinalizeReporter(context.Background(), func(s FinalizeStep) {
		mu.Lock()
		steps = append(steps, s)
		mu.Unlock()
	})
	docker := NewFakeDocker()
	f := NewFinalizer(FinalizeDeps{Local: LocalHost{Docker: docker, DataDir: dataDir}, Restart: func() {},
		Queue: &fakeEnqueuer{calls: map[int64]deployments.Mode{}}, Sources: sourceSet{}, Domains: fakeDomains{}})
	os.MkdirAll(filepath.Join(dataDir, ".migration"), 0o700)
	if err := f.Begin(ctx, Manifest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Complete(ctx, Manifest{}); err != nil {
		t.Fatal(err)
	}
	want := []FinalizeStep{FinalizeStopTraefik, FinalizeMoveData, FinalizeRestart, FinalizeStartTraefik, FinalizeDeploy}
	if !slices.Equal(steps, want) {
		t.Fatalf("steps = %q, want %q", steps, want)
	}
}
