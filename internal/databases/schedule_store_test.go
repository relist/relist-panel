package databases

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestGetScheduleDefaults(t *testing.T) {
	s := newStore(t)
	d := seedDB(t, s, "Shop")
	got, err := s.GetSchedule(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultSchedule(d.ID)) {
		t.Fatalf("not DefaultSchedule: %+v", got)
	}
	if got.Enabled || !slices.Equal(got.Days, []string{"daily"}) || got.Minute != 180 || !got.All ||
		*got.Local.Count != 7 || got.Local.Days != nil || *got.Remote.Count != 30 || got.Remote.Days != nil || got.LastRunAt != nil {
		t.Fatalf("default = %+v", got)
	}
}

func TestSaveScheduleRoundTripAndArming(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	now := time.UnixMilli(5_000_000)
	s.now = func() time.Time { return now }
	sc := Schedule{DatabaseID: d.ID, Enabled: true, Days: []string{"mon", "thu"}, Minute: 150, Databases: []string{"a", "b"},
		Local: Keep{Count: intp(4), Days: intp(10)}, Remote: Keep{Days: intp(60)}}
	if err := s.SaveSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSchedule(ctx, d.ID)
	if !got.Enabled || !slices.Equal(got.Days, []string{"mon", "thu"}) || got.Minute != 150 || got.All || !slices.Equal(got.Databases, []string{"a", "b"}) ||
		*got.Local.Count != 4 || *got.Local.Days != 10 || got.Remote.Count != nil || *got.Remote.Days != 60 {
		t.Fatalf("got = %+v", got)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(now) {
		t.Fatalf("enabling must arm: last_run_at = %v, want %v", got.LastRunAt, now)
	}
	// Saving again while enabled keeps last_run_at.
	now = now.Add(time.Hour)
	if err := s.SaveSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSchedule(ctx, d.ID)
	if !got.LastRunAt.Equal(time.UnixMilli(5_000_000)) {
		t.Fatalf("last_run_at moved to %v", got.LastRunAt)
	}
	// Off then on arms again.
	sc.Enabled = false
	_ = s.SaveSchedule(ctx, sc)
	sc.Enabled = true
	_ = s.SaveSchedule(ctx, sc)
	got, _ = s.GetSchedule(ctx, d.ID)
	if !got.LastRunAt.Equal(now) {
		t.Fatalf("re-enabling must arm again: %v", got.LastRunAt)
	}
	list, _ := s.ListEnabledSchedules(ctx)
	if len(list) != 1 || list[0].DatabaseID != d.ID {
		t.Fatalf("enabled = %+v", list)
	}
	if err := s.SetScheduleRun(ctx, d.ID, now.Add(time.Minute), "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSchedule(ctx, d.ID)
	if got.LastError != "boom" || !got.LastRunAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("run = %+v", got)
	}
	sc.Enabled = false
	_ = s.SaveSchedule(ctx, sc)
	if list, _ := s.ListEnabledSchedules(ctx); len(list) != 0 {
		t.Fatalf("disabled listed: %+v", list)
	}
}

func TestSetPinnedAndLocalRemoved(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := seedDB(t, s, "Shop")
	b, _ := s.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerScheduled, LogicalName: "app"})
	if got, _ := s.GetBackup(ctx, d.ID, b.ID); got.Pinned || got.LocalRemovedAt != nil {
		t.Fatalf("fresh = %+v", got)
	}
	if err := s.SetPinned(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(9_000)
	if err := s.SetLocalRemoved(ctx, b.ID, at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBackup(ctx, d.ID, b.ID)
	if !got.Pinned || got.LocalRemovedAt == nil || !got.LocalRemovedAt.Equal(at) {
		t.Fatalf("got = %+v", got)
	}
	if err := s.SetPinned(ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackup(ctx, d.ID, b.ID); got.Pinned {
		t.Fatal("still pinned")
	}
	if err := s.SetPinned(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin unknown = %v", err)
	}
}

func TestBackupPathRemoteOnly(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	b := r.manualBackup(t, d.ID)
	if err := r.store.SetLocalRemoved(context.Background(), b.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.BackupPath(context.Background(), d.ID, b.ID); !errors.Is(err, ErrRemoteOnly) {
		t.Fatalf("BackupPath = %v, want ErrRemoteOnly", err)
	}
	if _, err := r.svc.RestoreFromBackup(context.Background(), d.ID, b.ID, "app", d.Name); !errors.Is(err, ErrRemoteOnly) {
		t.Fatalf("restore = %v, want ErrRemoteOnly", err)
	}
}
