package databases

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func seedDB(t *testing.T, s *Store, name string) Database {
	t.Helper()
	d, err := s.Insert(context.Background(), Database{Name: name, Slug: "shop", Engine: "postgres", Version: "18", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBackupRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	b, err := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{Databases: []string{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != JobRunning || b.StartedAt.IsZero() || b.FinishedAt != nil {
		t.Fatalf("inserted = %+v", b)
	}
	if err := s.FinishBackup(ctx, b.ID, Backup{Status: JobSucceeded, Log: "log", FileName: "f.sql.gz", Size: 12, SHA256: "abc"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackup(ctx, d.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Scope, Scope{Databases: []string{"a"}}) || got.Status != JobSucceeded || got.Size != 12 ||
		got.SHA256 != "abc" || got.FileName != "f.sql.gz" || got.Log != "log" || got.FinishedAt == nil || got.Trigger != TriggerManual {
		t.Fatalf("got = %+v", got)
	}
	all, err := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerPreRestore, Scope: Scope{All: true}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBackup(ctx, d.ID, all.ID)
	if !got.Scope.All || len(got.Scope.Databases) != 0 {
		t.Fatalf("scope = %+v", got.Scope)
	}
}

func TestListBackupsNewestFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	now := time.UnixMilli(1_000_000)
	s.now = func() time.Time { now = now.Add(time.Second); return now }
	var ids []int64
	for i := 0; i < 3; i++ {
		b, err := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	list, err := s.ListBackups(ctx, d.ID)
	if err != nil || len(list) != 3 || list[0].ID != ids[2] || list[2].ID != ids[0] {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestGetBackupOtherDatabase404(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d1 := seedDB(t, s, "One")
	d2, _ := s.Insert(ctx, Database{Name: "Two", Slug: "two", Engine: "postgres", Version: "18", Password: "pw"})
	b, _ := s.InsertBackup(ctx, Backup{DatabaseID: d1.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	if _, err := s.GetBackup(ctx, d2.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFailRunning(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	b, _ := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	done, _ := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	_ = s.FinishBackup(ctx, done.ID, Backup{Status: JobSucceeded})
	r, _ := s.InsertRestore(ctx, Restore{DatabaseID: d.ID, Source: "upload", UploadName: "x.sql"})
	failed, err := s.FailRunning(ctx, "the panel stopped during this run")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].ID != b.ID {
		t.Fatalf("failed backups = %+v", failed)
	}
	gb, _ := s.GetBackup(ctx, d.ID, b.ID)
	if gb.Status != JobFailed || gb.Error != "the panel stopped during this run" || gb.FinishedAt == nil {
		t.Fatalf("backup = %+v", gb)
	}
	gd, _ := s.GetBackup(ctx, d.ID, done.ID)
	if gd.Status != JobSucceeded {
		t.Fatalf("finished backup changed: %+v", gd)
	}
	rs, _ := s.ListRestores(ctx, d.ID)
	if len(rs) != 1 || rs[0].ID != r.ID || rs[0].Status != JobFailed || rs[0].Error != "the panel stopped during this run" {
		t.Fatalf("restores = %+v", rs)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	b, _ := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	r, err := s.InsertRestore(ctx, Restore{DatabaseID: d.ID, Source: "backup", BackupID: &b.ID})
	if err != nil || r.Status != JobRunning {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	pre := b.ID
	if err := s.FinishRestore(ctx, r.ID, Restore{Status: JobFailed, Error: "boom", Log: "l", PreBackupID: &pre}); err != nil {
		t.Fatal(err)
	}
	rs, _ := s.ListRestores(ctx, d.ID)
	if len(rs) != 1 || rs[0].BackupID == nil || *rs[0].BackupID != b.ID || rs[0].PreBackupID == nil || rs[0].Status != JobFailed || rs[0].Log != "l" {
		t.Fatalf("restores = %+v", rs)
	}
}

func TestTombstoneFreesName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	if err := s.Tombstone(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, d.ID)
	if got.Name != "Shop (deleted #1)" || got.Slug != "deleted-1" || got.DeletedAt == nil {
		t.Fatalf("tombstone = %+v", got)
	}
	if _, err := s.Insert(ctx, Database{Name: "Shop", Slug: "shop", Engine: "postgres", Version: "18", Password: "p"}); err != nil {
		t.Fatalf("new Shop: %v", err)
	}
}

func TestDeleteDatabaseCascades(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	b, _ := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerManual, Scope: Scope{All: true}})
	_, _ = s.InsertRestore(ctx, Restore{DatabaseID: d.ID, Source: "upload"})
	if err := s.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBackup(ctx, d.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("backup survived: %v", err)
	}
	if rs, _ := s.ListRestores(ctx, d.ID); len(rs) != 0 {
		t.Fatalf("restores survived: %+v", rs)
	}
}
