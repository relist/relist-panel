package databases

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// A panic in a Database's background job is contained (internal/safely):
// the panel carries on, the records the job left running are failed, and the
// Database's last error says what happened.

func (r *rig) wantContained(t *testing.T, id int64) {
	t.Helper()
	if job := r.svc.Jobs(id); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
	d, err := r.store.Get(context.Background(), id)
	if err != nil || !strings.Contains(d.LastError, "internal error") {
		t.Fatalf("last error = %q, %v", d.LastError, err)
	}
}

func TestPanicInABackupIsContained(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) { panic("boom") })
	if _, err := r.svc.StartBackup(ctx, d.ID, BackupRequest{Databases: []string{"app"}}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if b := r.lastBackup(t, d.ID); b.Status != JobFailed || !strings.Contains(b.Error, "internal error") {
		t.Fatalf("backup = %+v", b)
	}
	r.wantContained(t, d.ID)
	// The next job runs.
	r.host.override("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, err := io.WriteString(req.Stdout, "SELECT 1;\n")
		return 0, err
	})
	r.manualBackup(t, d.ID)
}

func TestPanicInARestoreIsContained(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID)
	r.adminSQL(pgAdmin, "postgres\n", nil)
	r.host.script(pgRestore, func(ExecRequest) (int, error) { panic("boom") })
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got := r.lastRestore(t, d.ID); got.Status != JobFailed || !strings.Contains(got.Error, "internal error") {
		t.Fatalf("restore = %+v", got)
	}
	r.wantContained(t, d.ID)
}

// A version change that panics may have left the Database half changed: it
// keeps its job slot and its running record, so nothing else touches it
// until a panel restart, whose start-up recovery finishes or rolls it back.
func TestPanicInAVersionChangeKeepsTheDatabaseForRecovery(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	r.host.mu.Lock()
	r.host.panicOn["pull postgres:18"] = "boom"
	r.host.mu.Unlock()
	c, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false)
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if job := r.svc.Jobs(r.d.ID); job != JobKindChangeVersion {
		t.Fatalf("job slot = %q, want it held for recovery", job)
	}
	got, err := r.store.GetVersionChange(ctx, r.d.ID, c.ID)
	if err != nil || got.Status != JobRunning {
		t.Fatalf("version change = %+v, %v", got, err)
	}
	d, _ := r.store.Get(ctx, r.d.ID)
	if !strings.Contains(d.LastError, "internal error") || !strings.Contains(d.LastError, "Restart the panel") {
		t.Fatalf("last error = %q", d.LastError)
	}
}

func TestPanicInAScheduledBackupKeepsTheSchedulerGoing(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, nil, day(14, 3, 0))
	r.host.mu.Lock()
	r.host.panicOn["inspect "+ContainerName(d.ID)] = "boom"
	r.host.mu.Unlock()
	r.svc.tick(context.Background())
	r.wantContained(t, d.ID)
}

func TestPanicInStartUpRecoveryIsContained(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.create(t, "Shop", "postgres", "18")
	r.host.mu.Lock()
	r.host.panicOn["inspect "] = "boom"
	r.host.mu.Unlock()
	r.svc.HoldJobs()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.svc.Recover(ctx)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Recover did not return")
	}
	if err := r.svc.gate(ctx); err != nil {
		t.Fatalf("jobs still held after recovery: %v", err)
	}
}

func TestPanicWhileStartingANewDatabaseIsContained(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.host.mu.Lock()
	r.host.panicOn["create "] = "boom"
	r.host.mu.Unlock()
	d, err := r.svc.Create(ctx, CreateInput{Name: "Shop", Engine: "postgres", Version: "18"})
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	r.wantContained(t, d.ID)
}
