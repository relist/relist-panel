package databases

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time shared by the Service and the Store.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// 2026-11-15 is a Sunday.
func day(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }

func (r *rig) useClock(t time.Time) *clock {
	c := &clock{t: t}
	r.svc.d.Now = c.now
	r.store.now = c.now
	return c
}

// schedule saves an enabled daily 03:00 schedule for id, last run at lastRun.
func (r *rig) schedule(t *testing.T, id int64, edit func(*Schedule), lastRun time.Time) {
	t.Helper()
	sc := DefaultSchedule(id)
	sc.Enabled = true
	if edit != nil {
		edit(&sc)
	}
	if err := r.store.SaveSchedule(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetScheduleRun(context.Background(), id, lastRun, ""); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) getSchedule(t *testing.T, id int64) Schedule {
	t.Helper()
	sc, err := r.store.GetSchedule(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func (r *rig) backups(t *testing.T, id int64) []Backup {
	t.Helper()
	list, err := r.store.ListBackups(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestSchedulerArmsNewSchedule(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	clk := r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, nil, day(14, 3, 0))
	// A schedule with no last run (an old row) is armed, not run.
	if _, err := r.store.db.Exec(`UPDATE database_backup_schedules SET last_run_at = NULL`); err != nil {
		t.Fatal(err)
	}
	r.svc.tick(context.Background())
	if sc := r.getSchedule(t, d.ID); sc.LastRunAt == nil || !sc.LastRunAt.Equal(clk.now()) {
		t.Fatalf("last_run_at = %v, want armed at now", sc.LastRunAt)
	}
	if n := len(r.backups(t, d.ID)); n != 0 {
		t.Fatalf("%d rows made while arming", n)
	}
}

func TestSchedulerRunsDue(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("psql -U postgres -At -c SELECT datname", func(req ExecRequest) (int, error) {
		_, err := req.Stdout.Write([]byte("app\nweb\n"))
		return 0, err
	})
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, nil, day(14, 3, 0))
	r.svc.tick(context.Background())
	list := r.backups(t, d.ID)
	if len(list) != 2 {
		t.Fatalf("rows = %+v", list)
	}
	for _, b := range list {
		if b.Trigger != TriggerScheduled || b.Status != JobSucceeded {
			t.Fatalf("row = %+v", b)
		}
	}
	sc := r.getSchedule(t, d.ID)
	if sc.LastRunAt == nil || !sc.LastRunAt.Equal(clk.now()) || sc.LastError != "" {
		t.Fatalf("schedule = %+v", sc)
	}
	r.svc.tick(context.Background())
	if n := len(r.backups(t, d.ID)); n != 2 {
		t.Fatalf("a second tick in the same minute made rows: %d", n)
	}
	if r.svc.Busy() {
		t.Fatal("the job slot was not freed")
	}
}

// Review Focus 5: a schedule due while the slot is taken, or a lock is held,
// runs on a later tick: it is not lost and not marked run.
func TestSchedulerRetriesWhenBusy(t *testing.T) {
	for _, c := range []struct {
		name string
		hold func(r *rig, id int64) (release func())
	}{
		{"manual job", func(r *rig, id int64) func() {
			if err := r.svc.beginJob(id, JobKindBackup); err != nil {
				t.Fatal(err)
			}
			return func() { r.svc.endJob(id) }
		}},
		{"panel update lock", func(r *rig, id int64) func() {
			r.updateLocked = true
			return func() { r.updateLocked = false }
		}},
		{"migration lock", func(r *rig, id int64) func() {
			r.locked = true
			return func() { r.locked = false }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			d := r.create(t, "Shop", "postgres", "18")
			r.dumps("pg_dump -U postgres", "SELECT 1;\n")
			clk := r.useClock(day(15, 3, 0))
			yesterday := day(14, 3, 0)
			r.schedule(t, d.ID, nil, yesterday)
			release := c.hold(r, d.ID)
			r.svc.tick(context.Background())
			if n := len(r.backups(t, d.ID)); n != 0 {
				t.Fatalf("%d rows made while busy", n)
			}
			if sc := r.getSchedule(t, d.ID); sc.LastRunAt == nil || !sc.LastRunAt.Equal(yesterday) {
				t.Fatalf("last_run_at = %v, want unchanged %v", sc.LastRunAt, yesterday)
			}
			release()
			clk.set(day(15, 3, 1))
			r.svc.tick(context.Background())
			if n := len(r.backups(t, d.ID)); n != 1 {
				t.Fatalf("rows after the slot freed = %d, want 1", n)
			}
		})
	}
}

func TestSchedulerSkipsStopped(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	clk := r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, nil, day(14, 3, 0))
	r.svc.tick(context.Background())
	sc := r.getSchedule(t, d.ID)
	if sc.LastError != "skipped: the Database was not running" || sc.LastRunAt == nil || !sc.LastRunAt.Equal(clk.now()) {
		t.Fatalf("schedule = %+v", sc)
	}
	if n := len(r.backups(t, d.ID)); n != 0 {
		t.Fatalf("%d rows", n)
	}
}

func TestScheduledMissingDatabaseFailsOnlyIt(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, func(sc *Schedule) { sc.All, sc.Databases = false, []string{"app", "gone"} }, day(14, 3, 0))
	r.svc.tick(context.Background())
	got := map[string]Backup{}
	for _, b := range r.backups(t, d.ID) {
		got[b.LogicalName] = b
	}
	if len(got) != 2 || got["app"].Status != JobSucceeded || got["gone"].Status != JobFailed || got["gone"].Error != "no such database: gone" {
		t.Fatalf("rows = %+v", got)
	}
	if sc := r.getSchedule(t, d.ID); sc.LastError != "no such database: gone" {
		t.Fatalf("last_error = %q", sc.LastError)
	}
}

func TestRotateLocalAfterRun(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(10, 3, 0))
	manual := r.manualBackup(t, d.ID)
	pre, err := r.store.InsertBackup(context.Background(), Backup{DatabaseID: d.ID, Trigger: TriggerPreRestore, LogicalName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	preFile := "pre.sql.gz"
	_ = os.WriteFile(filepath.Join(r.backupsDir(d.ID), preFile), []byte("x"), 0o600)
	_ = r.store.FinishBackup(context.Background(), pre.ID, Backup{Status: JobSucceeded, FileName: preFile})
	r.schedule(t, d.ID, func(sc *Schedule) { sc.Local = Keep{Count: intp(2)} }, day(10, 3, 0))
	var ids []int64
	for i := 11; i <= 14; i++ {
		clk.set(day(i, 3, 0))
		r.svc.tick(context.Background())
		var latest Backup
		for _, b := range r.backups(t, d.ID) {
			if b.Trigger == TriggerScheduled && (latest.ID == 0 || b.ID > latest.ID) {
				latest = b
			}
		}
		ids = append(ids, latest.ID)
		if i == 11 { // pin the oldest
			if err := r.store.SetPinned(context.Background(), latest.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	have := map[int64]bool{}
	for _, b := range r.backups(t, d.ID) {
		have[b.ID] = true
		if b.FileName != "" {
			if _, err := os.Stat(filepath.Join(r.backupsDir(d.ID), b.FileName)); err != nil {
				t.Fatalf("row %d has no file: %v", b.ID, err)
			}
		}
	}
	// ids[0] pinned, ids[1] rotated away, ids[2] and ids[3] are the 2 newest.
	if !have[ids[0]] || have[ids[1]] || !have[ids[2]] || !have[ids[3]] || !have[manual.ID] || !have[pre.ID] {
		t.Fatalf("rows left = %v (ids %v, manual %d, pre %d)", have, ids, manual.ID, pre.ID)
	}
	files, _ := os.ReadDir(r.backupsDir(d.ID))
	if len(files) != 5 { // pinned, 2 newest, manual, pre_restore
		t.Fatalf("%d files left, want 5: %v", len(files), files)
	}
}

func TestSchedulerRunsOneAtATime(t *testing.T) {
	r := newRig(t)
	a := r.create(t, "Alpha", "postgres", "18")
	b := r.create(t, "Beta", "postgres", "18")
	var running, maxRunning atomic.Int32
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		_, err := req.Stdout.Write([]byte("SELECT 1;\n"))
		return 0, err
	})
	r.useClock(day(15, 3, 0))
	r.schedule(t, a.ID, nil, day(14, 3, 0))
	r.schedule(t, b.ID, nil, day(14, 3, 0))
	r.svc.tick(context.Background())
	if len(r.backups(t, a.ID)) != 1 || len(r.backups(t, b.ID)) != 1 {
		t.Fatalf("rows: %d %d", len(r.backups(t, a.ID)), len(r.backups(t, b.ID)))
	}
	if maxRunning.Load() != 1 {
		t.Fatalf("%d dumps overlapped", maxRunning.Load())
	}
}

func TestDeleteWithBackupsKeptDropsSchedule(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.manualBackup(t, d.ID)
	r.schedule(t, d.ID, nil, day(14, 3, 0))
	if err := r.svc.Delete(context.Background(), d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.store.ListEnabledSchedules(context.Background()); len(list) != 0 {
		t.Fatalf("tombstone still scheduled: %+v", list)
	}
}
