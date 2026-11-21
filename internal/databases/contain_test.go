package databases

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/backupdest"
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

// A dump that panics half-way leaves no .partial file and no open handle.
func TestPanicMidDumpLeavesNoPartialFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, _ = io.WriteString(req.Stdout, "half a dump;\n")
		panic("boom")
	})
	if _, err := r.svc.StartBackup(ctx, d.ID, BackupRequest{Databases: []string{"app"}}); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	left, err := filepath.Glob(filepath.Join(r.backupsDir(d.ID), "*.partial"))
	if err != nil || len(left) != 0 {
		t.Fatalf("left behind: %v, %v", left, err)
	}
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

// A version change that panics is healed at once, the way a panel restart
// would heal it: the change fails with the panic in its log, the container
// goes back to what the Database's record names, and the slot is freed.
func TestPanicBeforeAVersionChangeTouchesTheHostIsRecovered(t *testing.T) {
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
	if job := r.svc.Jobs(r.d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
	got, err := r.store.GetVersionChange(ctx, r.d.ID, c.ID)
	if err != nil || got.Status != JobFailed || !strings.Contains(got.Error, "internal error") || !strings.Contains(got.Log, "internal error") {
		t.Fatalf("version change = %+v, %v", got, err)
	}
	if d, _ := r.store.Get(ctx, r.d.ID); d.Version != "16" {
		t.Fatalf("version = %q", d.Version)
	}
}

func TestPanicMidVersionChangeRollsBackInProcess(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	// The new server is up on its own volume and the old one stopped when
	// the first database's restore panics.
	r.host.override(pgRestore, func(ExecRequest) (int, error) { panic("boom") })
	c, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false)
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if job := r.svc.Jobs(r.d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
	got, _ := r.store.GetVersionChange(ctx, r.d.ID, c.ID)
	if got.Status != JobFailed || !strings.Contains(got.Error, "internal error") {
		t.Fatalf("version change = %+v", got)
	}
	d, _ := r.store.Get(ctx, r.d.ID)
	r.host.mu.Lock()
	defer r.host.mu.Unlock()
	spec, st := r.host.specs[ContainerName(d.ID)], r.host.containers[ContainerName(d.ID)]
	if d.Version != "16" || spec.Image != "postgres:16" || spec.Volume != d.Volume || st == nil || !st.Running {
		t.Fatalf("after recovery: version %q, spec %+v, state %+v", d.Version, spec, st)
	}
	if got.ToVolume == "" || r.host.volumes[got.ToVolume] {
		t.Fatalf("the half-made volume %q is still there", got.ToVolume)
	}
}

// Reading the role settings runs while the request holds the job slot.
func TestPanicReadingRoleSettingsReleasesTheSlot(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	r.host.override(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		if strings.Contains(string(b), "setdatabase = 0") {
			panic("boom")
		}
		return 0, nil
	})
	if _, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v", err)
	}
	if job := r.svc.Jobs(r.d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
}

// A panic resuming one interrupted change fails that change and frees its
// slot; the rest of the start-up recovery still runs.
func TestPanicResumingAnInPlaceChangeIsContained(t *testing.T) {
	r, d := inPlaceRig(t, "mariadb", "11.4")
	ctx := context.Background()
	c := interruptedAfterSwitch(t, r, d, "11.8")
	partial := filepath.Join(r.dataDir, ".databases", "99", "backups", "x.sql.gz.partial")
	if err := os.MkdirAll(filepath.Dir(partial), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.host.mu.Lock()
	r.host.panicOn["create "] = "boom"
	r.host.mu.Unlock()
	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if job := r.svc.Jobs(d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
	if row, _ := r.store.GetVersionChange(ctx, d.ID, c.ID); row.Status != JobFailed || !strings.Contains(row.Error, "internal error") {
		t.Fatalf("row = %+v", row)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("the rest of Startup did not run: %v", err)
	}
}

func TestPanicReconcilingOneDatabaseStillReconcilesTheRest(t *testing.T) {
	r := newRig(t)
	a := r.create(t, "Shop", "postgres", "18")
	b := r.create(t, "Blog", "postgres", "18")
	r.host.mu.Lock()
	r.host.panicOn["inspect "+ContainerName(a.ID)] = "boom"
	r.host.calls = nil
	r.host.mu.Unlock()
	if err := r.svc.ReconcileAll(context.Background()); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("ReconcileAll = %v", err)
	}
	if !r.host.saw("inspect " + ContainerName(b.ID)) {
		t.Fatalf("Blog was not reconciled: %v", r.host.all())
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

// A job that makes no Backups, restores or copies never fails one: a
// password reset, the settle after it and the start after Create only record
// the panic. Failing rows there could hit a Backup another job just started.
func TestPanicInAPasswordResetLeavesBackupsAlone(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.pingDefault = true
	b, err := r.store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual})
	if err != nil || b.Status != JobRunning {
		t.Fatalf("backup = %+v, %v", b, err)
	}
	r.admin.mu.Lock()
	r.admin.execErr = func(string, []string) error { panic("boom") }
	r.admin.mu.Unlock()
	if err := r.svc.ResetSuperuserPassword(ctx, d.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got, _ := r.store.GetBackup(ctx, d.ID, b.ID); got.Status != JobRunning {
		t.Fatalf("another job's backup = %+v", got)
	}
	r.wantContained(t, d.ID)
}

// The schedule loop outlives a tick that panics.
func TestSchedulerLoopOutlivesAPanickingTick(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.svc.runSchedulerEvery(ctx, time.Millisecond, func(context.Context) {
			ticks++
			if ticks == 1 {
				panic("boom")
			}
			cancel()
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not end")
	}
	if ticks != 2 {
		t.Fatalf("ticks = %d, want a second tick after the panic", ticks)
	}
}

// An encrypted copy's temp file goes whether the panic hits while sending it
// or while encrypting it; a start-up sweep is not the only cleanup.
func TestPanicSendingAnEncryptedCopyLeavesNoTemp(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	remote.putPanic = "boom"
	r.svc.d.Destinations = fakeOpener{remote: remote, dest: &backupdest.Destination{
		ID: 1, Name: "minio", Type: "s3", S3: backupdest.S3Config{Prefix: "pfx"}, Encrypt: true, Passphrase: "upload-passphrase-0123456789"}}
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	if b := r.lastBackup(t, d.ID); b.Upload == nil || b.Upload.Status != UploadFailed {
		t.Fatalf("upload = %+v", b.Upload)
	}
	left, err := filepath.Glob(filepath.Join(r.backupsDir(d.ID), "*.age.tmp"))
	if err != nil || len(left) != 0 {
		t.Fatalf("left behind: %v, %v", left, err)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

func TestPanicEncryptingACopyLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("encrypting did not panic")
			}
		}()
		_, _ = encryptReaderToTemp(filepath.Join(dir, "app.sql.gz"), "upload-passphrase-0123456789", panicReader{})
	}()
	left, err := filepath.Glob(filepath.Join(dir, "*.age.tmp"))
	if err != nil || len(left) != 0 {
		t.Fatalf("left behind: %v, %v", left, err)
	}
}
