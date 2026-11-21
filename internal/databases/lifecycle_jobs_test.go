package databases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockBackup makes pg_dump wait for release; started closes when it runs.
func (r *rig) blockBackup() (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) {
		close(started)
		<-release
		return 0, nil
	})
	return
}

func TestLifecycleBusyDuringBackup(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	started, release := r.blockBackup()
	if _, err := r.svc.StartBackup(ctx, d.ID, BackupRequest{All: true}); err != nil {
		t.Fatal(err)
	}
	<-started
	r.host.reset()
	for name, run := range map[string]func() error{
		"Start":         func() error { return r.svc.Start(ctx, d.ID) },
		"Stop":          func() error { return r.svc.Stop(ctx, d.ID) },
		"Restart":       func() error { return r.svc.Restart(ctx, d.ID) },
		"UpdateImage":   func() error { return r.svc.UpdateImage(ctx, d.ID, false) },
		"ApplySettings": func() error { return r.svc.ApplySettings(ctx, d.ID) },
		"Repair":        func() error { return r.svc.Repair(ctx, d.ID) },
		"Delete":        func() error { return r.svc.Delete(ctx, d.ID, "Shop", false) },
	} {
		if err := run(); !errors.Is(err, ErrBusy) {
			t.Errorf("%s = %v, want ErrBusy", name, err)
		}
	}
	if err := r.svc.Rename(ctx, d.ID, "Store"); err != nil {
		t.Fatalf("Rename during a backup: %v", err)
	}
	if got := r.host.mutating(); len(got) != 1 || !strings.HasPrefix(got[0], "realias") {
		t.Fatalf("a busy call touched the host: %v", got)
	}
	if v := r.view(t, d.ID); v.Job != "backup" || v.LastError != "" || v.Status != StatusRunning {
		t.Fatalf("view = %+v", v)
	}
	close(release)
	r.svc.Wait()
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatalf("Stop after the backup: %v", err)
	}
}

func TestUpdateImageBackupFirst(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if r.svc.Jobs(d.ID) != "update_image" && r.svc.Jobs(d.ID) != "" {
		t.Fatalf("Jobs = %q", r.svc.Jobs(d.ID))
	}
	r.svc.Wait()
	b := r.lastBackup(t, d.ID)
	if b.Trigger != TriggerPreUpdate || b.Status != JobSucceeded || b.LogicalName != "app" {
		t.Fatalf("backup = %+v", b)
	}
	calls := r.host.all()
	dump, pull := -1, -1
	for i, c := range calls {
		if strings.Contains(c, "pg_dump -U postgres") {
			dump = i
		}
		if strings.HasPrefix(c, "pull ") {
			pull = i
		}
	}
	if dump < 0 || pull < 0 || dump > pull {
		t.Fatalf("order: %v", calls)
	}
}

func TestUpdateImageBackupFirstFailureStopsBeforePull(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) { return 1, nil })
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if r.host.count("pull ") != 0 {
		t.Fatalf("pulled after a failed backup: %v", r.host.all())
	}
	got, _ := r.store.Get(ctx, d.ID)
	if !strings.Contains(got.LastError, "pre-update backup failed") {
		t.Fatalf("LastError = %q", got.LastError)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

func TestUpdateImageBackupFirstWhileStopped(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
	r.svc.Wait()
	if bl, _ := r.store.ListBackups(ctx, d.ID); len(bl) != 0 {
		t.Fatalf("a backup row was made: %+v", bl)
	}
	if r.host.count("pull ") != 0 || r.svc.Jobs(d.ID) != "" {
		t.Fatalf("calls = %v", r.host.all())
	}
	if got, _ := r.store.Get(ctx, d.ID); got.LastError != "" {
		t.Fatalf("a refusal marked the Database errored: %q", got.LastError)
	}
	if err := r.svc.UpdateImage(ctx, d.ID, false); err != nil {
		t.Fatalf("update without backup: %v", err)
	}
	if v := r.view(t, d.ID); v.Status != StatusStopped {
		t.Fatalf("status = %s, want stopped", v.Status)
	}
}

func TestDeleteKeepsBackupsAsTombstone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	b := r.manualBackup(t, d.ID)
	if err := r.svc.Delete(ctx, d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("remove relist-db-1") || !r.host.saw("remove-volume relist-db-1") {
		t.Fatalf("calls = %v", r.host.all())
	}
	r.host.reset()
	v := r.view(t, d.ID)
	if v.Status != StatusDeleted || !v.Deleted || v.Name != "Shop (deleted #1)" {
		t.Fatalf("view = %+v", v)
	}
	if len(r.host.all()) != 0 {
		t.Fatalf("a tombstone's view called the host: %v", r.host.all())
	}
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.StopAll(ctx)
	_ = r.svc.StartWanted(ctx)
	if len(r.host.all()) != 0 {
		t.Fatalf("lifecycle sweeps touched a tombstone: %v", r.host.all())
	}
	if vols, _ := r.svc.Volumes(ctx); len(vols) != 0 {
		t.Fatalf("Volumes = %v", vols)
	}
	for name, run := range map[string]func() error{
		"Start":  func() error { return r.svc.Start(ctx, d.ID) },
		"Stop":   func() error { return r.svc.Stop(ctx, d.ID) },
		"Rename": func() error { return r.svc.Rename(ctx, d.ID, "x") },
		"Delete": func() error { return r.svc.Delete(ctx, d.ID, "Shop (deleted #1)", false) },
		"Backup": func() error { _, err := r.svc.StartBackup(ctx, d.ID, BackupRequest{All: true}); return err },
		"Logs":   func() error { _, err := r.svc.Logs(ctx, d.ID, 10); return err },
	} {
		if err := run(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a tombstone = %v, want ErrNotFound", name, err)
		}
	}
	if _, _, err := r.svc.BackupPath(ctx, d.ID, b.ID); err != nil {
		t.Fatalf("download from a tombstone: %v", err)
	}
	if _, err := r.svc.Create(ctx, CreateInput{Name: "Shop", Engine: "postgres", Version: "18"}); err != nil {
		t.Fatalf("a new Shop: %v", err)
	}
}

// A version change's roles dump holds every role's password hash and is no
// Backup: a Delete that keeps the Backups removes it (plan-phase-10 J).
func TestTombstoneDropsTheRolesDumps(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	b := r.manualBackup(t, d.ID)
	roles := filepath.Join(r.backupsDir(d.ID), "version-change-7-roles.sql")
	if err := os.WriteFile(roles, []byte("CREATE ROLE app;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Delete(ctx, d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(roles); !os.IsNotExist(err) {
		t.Fatalf("the roles dump survived the Delete: %v", err)
	}
	if _, _, err := r.svc.BackupPath(ctx, d.ID, b.ID); err != nil {
		t.Fatalf("the kept Backup went too: %v", err)
	}
}

// A tombstone an earlier build left still holds the roles dump of a version
// change, with every role's password hash: Startup removes it. A live
// Database keeps its own.
func TestStartupRemovesRolesDumpsLeftOnTombstones(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	gone := r.create(t, "Gone", "postgres", "18")
	live := r.create(t, "Live", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	r.manualBackup(t, gone.ID)
	if err := r.svc.Delete(ctx, gone.ID, "Gone", false); err != nil {
		t.Fatal(err)
	}
	dump := func(id int64) string {
		dir := filepath.Join(r.dataDir, ".databases", fmt.Sprint(id), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "version-change-3-roles.sql")
		if err := os.WriteFile(p, []byte("CREATE ROLE app;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, kept := dump(gone.ID), dump(live.ID)
	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the tombstone's roles dump survived: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the live Database's roles dump went: %v", err)
	}
}

func TestDeletingLastBackupOfTombstoneDeletesRow(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	a := r.manualBackup(t, d.ID)
	b := r.manualBackup(t, d.ID)
	if err := r.svc.Delete(ctx, d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.DeleteBackup(ctx, d.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, d.ID); err != nil {
		t.Fatalf("row gone with a Backup left: %v", err)
	}
	if err := r.svc.DeleteBackup(ctx, d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row survived its last Backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dataDir, ".databases", "1")); !os.IsNotExist(err) {
		t.Fatal("directory survived")
	}
}

func TestDeleteWithBackupsRemovesDir(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	r.manualBackup(t, d.ID)
	if err := r.svc.Delete(ctx, d.ID, "Shop", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dataDir, ".databases", "1")); !os.IsNotExist(err) {
		t.Fatal("directory survived")
	}
}

func TestDeleteWithoutBackupsRemovesRow(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Delete(ctx, d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row survived: %v", err)
	}
}

func TestUpdateImageBackupFirstEveryDatabase(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("app\nother\n")
	r.pgDumps()
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	list, _ := r.store.ListBackups(ctx, d.ID)
	if len(list) != 2 || list[0].Trigger != TriggerPreUpdate || list[1].Trigger != TriggerPreUpdate ||
		list[0].Status != JobSucceeded || list[1].Status != JobSucceeded {
		t.Fatalf("backups = %+v", list)
	}
	if r.host.count("pull ") != 1 {
		t.Fatalf("calls = %v", r.host.all())
	}
}

func TestUpdateImageBackupFirstOneFailureNamesIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.pgList("app\nother\n")
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		if pgDB(req) == "other" {
			return 1, nil
		}
		return 0, nil
	})
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if r.host.count("pull ") != 0 {
		t.Fatalf("pulled after a failed backup: %v", r.host.all())
	}
	got, _ := r.store.Get(ctx, d.ID)
	if !strings.Contains(got.LastError, "other") || !strings.Contains(got.LastError, "pre-update backup failed") {
		t.Fatalf("LastError = %q", got.LastError)
	}
}

func TestUpdateImageBackupFirstWithNoDatabases(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "mysql", "8.4")
	r.dumps("mysql -uroot -N -e SHOW DATABASES", "information_schema\nmysql\n")
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if list, _ := r.store.ListBackups(ctx, d.ID); len(list) != 0 {
		t.Fatalf("backups = %+v", list)
	}
	if r.host.count("pull ") != 1 {
		t.Fatalf("the update did not run: %v", r.host.all())
	}
}
