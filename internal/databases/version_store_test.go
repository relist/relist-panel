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

// moveVolume commits a finished dump-and-restore change of d to volume v.
func moveVolume(t *testing.T, s *Store, d Database, v string) {
	t.Helper()
	c := insertChange(t, s, d, VersionMethodDumpRestore)
	err := s.CommitVersionChange(context.Background(), VersionCommit{ChangeID: c.ID, DatabaseID: d.ID, Version: d.Version,
		Volume: v, AppliedSpec: d.AppliedSpec, FromVolume: d.Volume})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpecUsesStoredVolume(t *testing.T) {
	d := Database{ID: 3, Slug: "shop", Engine: "postgres", Version: "18", Password: "pw", Volume: "relist-db-3-v9"}
	spec, _, err := specFor(d)
	if err != nil || spec.Volume != "relist-db-3-v9" {
		t.Fatalf("specFor volume = %q, %v", spec.Volume, err)
	}
	d.AppliedSpec = `{"Volume":"relist-db-3","Image":"postgres:18"}`
	applied, err := appliedSpec(d)
	if err != nil || applied.Volume != "relist-db-3-v9" {
		t.Fatalf("appliedSpec volume = %q, %v", applied.Volume, err)
	}
}

func TestVolumesListsCurrentAndPrevious(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "16")
	d, _ = r.store.Get(ctx, d.ID)
	to := fmt.Sprintf("relist-db-%d-v9", d.ID)
	moveVolume(t, r.store, d, to)
	vols, err := r.svc.Volumes(ctx)
	if err != nil || !slices.Equal(vols, []string{to, d.Volume}) {
		t.Fatalf("Volumes = %v, %v; want [%s %s]", vols, err, to, d.Volume)
	}
	v := r.view(t, d.ID)
	if v.Volume != to || !slices.Equal(v.PreviousVolumes, []string{d.Volume}) {
		t.Fatalf("view volume %q previous %v", v.Volume, v.PreviousVolumes)
	}
}

func TestDeleteRemovesPreviousVolumes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "16")
	d, _ = r.store.Get(ctx, d.ID)
	to := fmt.Sprintf("relist-db-%d-v9", d.ID)
	moveVolume(t, r.store, d, to)
	if err := r.svc.Delete(ctx, d.ID, "Shop", true); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{to, d.Volume} {
		if !r.host.saw("remove-volume " + v) {
			t.Fatalf("no remove-volume %s in %v", v, r.host.all())
		}
	}
}

func TestTargetVolume(t *testing.T) {
	if got := targetVolume(3, 9); got != "relist-db-3-v9" {
		t.Fatalf("targetVolume = %q", got)
	}
}
