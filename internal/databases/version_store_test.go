package databases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestInsertSetsVolume(t *testing.T) {
	s := newStore(t)
	d := seedDB(t, s, "Shop")
	if want := fmt.Sprintf("relist-db-%d", d.ID); d.Volume != want {
		t.Fatalf("Volume = %q, want %q", d.Volume, want)
	}
}

func insertChange(t *testing.T, s *Store, d Database, method string) VersionChange {
	t.Helper()
	c, err := s.InsertVersionChange(context.Background(), VersionChange{DatabaseID: d.ID, FromVersion: "16", ToVersion: "18", Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.Status != JobRunning || c.StartedAt.IsZero() {
		t.Fatalf("inserted = %+v", c)
	}
	return c
}

func TestVersionChangeCommit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	c := insertChange(t, s, d, VersionMethodDumpRestore)
	to := fmt.Sprintf("relist-db-%d-v%d", d.ID, c.ID)
	if err := s.SetVersionChangeTarget(ctx, c.ID, to); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVersionChangeLog(ctx, c.ID, "→ Pull postgres:18\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastError(ctx, d.ID, "old trouble"); err != nil {
		t.Fatal(err)
	}
	err := s.CommitVersionChange(ctx, VersionCommit{ChangeID: c.ID, DatabaseID: d.ID, Version: "18", Volume: to,
		AppliedSpec: `{"Image":"postgres:18"}`, FromVolume: d.Volume, Log: "done\n"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "18" || got.Volume != to || got.AppliedSpec != `{"Image":"postgres:18"}` || got.LastError != "" {
		t.Fatalf("database after commit = %+v", got)
	}
	row, err := s.GetVersionChange(ctx, d.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != JobSucceeded || row.FromVolume != d.Volume || row.ToVolume != to || row.Log != "done\n" || row.FinishedAt == nil {
		t.Fatalf("row = %+v", row)
	}
	prev, err := s.PreviousVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prev[d.ID], []string{d.Volume}) {
		t.Fatalf("PreviousVolumes = %v", prev)
	}
	if err := s.MarkFromVolumeRemoved(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if prev, _ := s.PreviousVolumes(ctx); len(prev[d.ID]) != 0 {
		t.Fatalf("PreviousVolumes after removal = %v", prev)
	}
	if row, _ := s.GetVersionChange(ctx, d.ID, c.ID); row.FromVolumeRemovedAt == nil {
		t.Fatal("FromVolumeRemovedAt not set")
	}
}

func TestVersionChangeFailKeepsDatabase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	c := insertChange(t, s, d, VersionMethodDumpRestore)
	if err := s.SetVersionChangeTarget(ctx, c.ID, "relist-db-1-v9"); err != nil {
		t.Fatal(err)
	}
	if err := s.FailVersionChange(ctx, c.ID, "Restore shop: exit 1", "log"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, d.ID)
	if got.Version != d.Version || got.Volume != d.Volume {
		t.Fatalf("database changed: %+v", got)
	}
	row, _ := s.GetVersionChange(ctx, d.ID, c.ID)
	if row.Status != JobFailed || row.Error != "Restore shop: exit 1" || row.Log != "log" || row.FinishedAt == nil {
		t.Fatalf("row = %+v", row)
	}
	vols, err := s.FailedTargetVolumes(ctx)
	if err != nil || !slices.Equal(vols, []string{"relist-db-1-v9"}) {
		t.Fatalf("FailedTargetVolumes = %v, %v", vols, err)
	}
	list, err := s.ListVersionChanges(ctx, d.ID)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("ListVersionChanges = %+v, %v", list, err)
	}
}

func TestFailRunningVersionChanges(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	done := insertChange(t, s, d, VersionMethodInPlace)
	if err := s.CommitVersionChange(ctx, VersionCommit{ChangeID: done.ID, DatabaseID: d.ID, Version: "18", Volume: d.Volume}); err != nil {
		t.Fatal(err)
	}
	running := insertChange(t, s, d, VersionMethodDumpRestore)
	if err := s.FailRunningVersionChanges(ctx, interrupted); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListVersionChanges(ctx, d.ID)
	if len(list) != 2 || list[0].ID != running.ID {
		t.Fatalf("newest first: %+v", list)
	}
	if list[0].Status != JobFailed || list[0].Error != interrupted || list[1].Status != JobSucceeded {
		t.Fatalf("list = %+v", list)
	}
}

func TestGetVersionChangeOtherDatabase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := seedDB(t, s, "Shop")
	b, err := s.Insert(ctx, Database{Name: "Blog", Slug: "blog", Engine: "postgres", Version: "16", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	c := insertChange(t, s, a, VersionMethodDumpRestore)
	if _, err := s.GetVersionChange(ctx, b.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
