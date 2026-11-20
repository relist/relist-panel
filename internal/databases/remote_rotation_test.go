package databases

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/backupdest"
)

// runDays runs the schedule once on each of the given days of November 2026.
func (r *rig) runDays(clk *clock, id int64, days ...int) {
	for _, d := range days {
		clk.set(day(d, 3, 0))
		r.svc.tick(context.Background())
	}
}

// Review Focus 2: local Rotation of a Backup that still has a copy removes
// only the file. The row stays Remote only, and remote Rotation removes the
// copy and the row later.
func TestRotateLocalKeepsRemoteOnlyRow(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(10, 3, 0))
	r.schedule(t, d.ID, func(sc *Schedule) {
		r.toDestination(sc)
		sc.Local = Keep{Count: intp(1)}
		sc.Remote = Keep{Count: intp(3)}
	}, day(10, 3, 0))
	r.runDays(clk, d.ID, 11, 12, 13)

	list := r.backups(t, d.ID)
	if len(list) != 3 {
		t.Fatalf("rows = %d, want 3", len(list))
	}
	var removed, normal int
	for _, b := range list {
		if b.Upload == nil || b.Upload.Status != UploadUploaded || b.Upload.DeletedAt != nil || !remote.has(b.Upload.ObjectKey) {
			t.Fatalf("copy of %d is not live: %+v", b.ID, b.Upload)
		}
		_, statErr := os.Stat(r.svc.filePath(d.ID, b))
		if b.LocalRemovedAt != nil {
			removed++
			if statErr == nil {
				t.Fatalf("Remote only row %d still has its file", b.ID)
			}
		} else {
			normal++
			if statErr != nil {
				t.Fatalf("row %d lost its file: %v", b.ID, statErr)
			}
		}
	}
	if removed != 2 || normal != 1 {
		t.Fatalf("Remote only = %d, normal = %d; want 2 and 1", removed, normal)
	}
	if remote.count() != 3 {
		t.Fatalf("bucket has %d objects, want 3", remote.count())
	}

	// Remote N=1: the next tick deletes the 2 older copies and their rows.
	newest := list[0]
	sc := r.getSchedule(t, d.ID)
	sc.Remote = Keep{Count: intp(1)}
	if err := r.store.SaveSchedule(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
	clk.set(day(13, 3, 1))
	r.svc.tick(context.Background())
	left := r.backups(t, d.ID)
	if len(left) != 1 || left[0].ID != newest.ID {
		t.Fatalf("rows left = %+v", left)
	}
	if remote.count() != 1 || !remote.has(newest.Upload.ObjectKey) {
		t.Fatalf("bucket = %v", remote.objects)
	}
}

// Review Focus 3: a remote delete that fails changes nothing.
func TestDeleteBackupRemoteFailureKeepsAll(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	remote.mu.Lock()
	remote.delErr = errors.New("connection refused")
	remote.mu.Unlock()

	err := r.svc.DeleteBackup(context.Background(), d.ID, b.ID)
	if !errors.Is(err, ErrRemoteDelete) || !strings.Contains(err.Error(), `"minio"`) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want ErrRemoteDelete naming minio", err)
	}
	if _, statErr := os.Stat(r.svc.filePath(d.ID, b)); statErr != nil {
		t.Fatalf("the file is gone: %v", statErr)
	}
	after := r.lastBackup(t, d.ID)
	if after.ID != b.ID || after.Upload == nil || after.Upload.Status != UploadUploaded || after.Upload.DeletedAt != nil || !remote.has(b.Upload.ObjectKey) {
		t.Fatalf("row changed: %+v %+v", after, after.Upload)
	}

	// With the bucket back, the delete removes the copy, the file and the row.
	remote.mu.Lock()
	remote.delErr = nil
	remote.mu.Unlock()
	if err := r.svc.DeleteBackup(context.Background(), d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if remote.has(after.Upload.ObjectKey) || len(r.backups(t, d.ID)) != 0 {
		t.Fatalf("copy or row left: %v", remote.objects)
	}
}

func TestDeleteDatabaseRemoteFailureKeepsAll(t *testing.T) {
	r := newRig(t)
	closer := &fakeCloser{host: r.host}
	r.svc.d.DirectAccess = closer
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(10, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(10, 3, 0))
	r.runDays(clk, d.ID, 11, 12)
	if remote.count() != 2 {
		t.Fatalf("bucket has %d objects", remote.count())
	}
	remote.mu.Lock()
	remote.delErr = errors.New("connection refused")
	remote.mu.Unlock()
	r.host.reset()

	err := r.svc.Delete(context.Background(), d.ID, "Shop", true)
	if !errors.Is(err, ErrRemoteDelete) || !strings.Contains(err.Error(), `"minio"`) {
		t.Fatalf("err = %v", err)
	}
	if r.host.count("remove ") != 0 || r.host.count("remove-volume ") != 0 {
		t.Fatalf("the container or volume was removed: %v", r.host.all())
	}
	if len(r.backups(t, d.ID)) != 2 {
		t.Fatal("rows were removed")
	}
	if sc := r.getSchedule(t, d.ID); !sc.Enabled || sc.DestinationID == nil {
		t.Fatalf("schedule = %+v", sc)
	}
	if _, err := r.store.Get(context.Background(), d.ID); err != nil {
		t.Fatalf("Database record gone: %v", err)
	}
	if len(closer.calls) != 0 {
		t.Fatalf("the Direct access was closed though nothing else was deleted: %v", closer.calls)
	}

	// Bucket back: everything goes, copies first.
	remote.mu.Lock()
	remote.delErr = nil
	remote.mu.Unlock()
	if err := r.svc.Delete(context.Background(), d.ID, "Shop", true); err != nil {
		t.Fatal(err)
	}
	if remote.count() != 0 {
		t.Fatalf("copies left: %v", remote.objects)
	}
}

// Copies in a destination the schedule used before are never rotated.
func TestRotateRemoteOnlyCurrentDestination(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(10, 3, 0))
	r.schedule(t, d.ID, func(sc *Schedule) {
		r.toDestination(sc)
		sc.Local = Keep{}
		sc.Remote = Keep{Count: intp(1)}
	}, day(10, 3, 0))
	r.runDays(clk, d.ID, 11, 12, 13)
	if remote.count() != 1 {
		t.Fatalf("bucket has %d objects after rotation, want 1", remote.count())
	}
	// The schedule moves to no destination (a former one is left).
	sc := r.getSchedule(t, d.ID)
	sc.DestinationID = nil
	sc.Remote = Keep{Count: intp(1)}
	if err := r.store.SaveSchedule(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
	r.runDays(clk, d.ID, 14, 15, 16)
	if remote.count() != 1 {
		t.Fatalf("a former destination's copy was rotated: %v", remote.objects)
	}
	if len(remote.deletes) != 2 {
		t.Fatalf("deletes = %v, want the 2 from the first phase only", remote.deletes)
	}
}

func TestRotateRemoteKeepsNewest(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	clk := r.useClock(day(1, 3, 0))
	r.schedule(t, d.ID, func(sc *Schedule) {
		r.toDestination(sc)
		sc.Remote = Keep{Days: intp(7)}
	}, day(1, 3, 0))
	r.runDays(clk, d.ID, 2)
	// 30 days later, on a day the schedule does not run: only Rotation ticks.
	sc := r.getSchedule(t, d.ID)
	sc.Days = []string{"sun"}
	if err := r.store.SaveSchedule(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
	clk.set(day(2, 3, 0).Add(30 * 24 * time.Hour))
	r.svc.tick(context.Background())
	if len(r.backups(t, d.ID)) != 1 {
		t.Fatalf("rows = %d", len(r.backups(t, d.ID)))
	}
	if remote.count() != 1 || len(remote.deletes) != 0 {
		t.Fatalf("the only copy was rotated away: %v", remote.objects)
	}
}

var _ = backupdest.ErrNotFound
