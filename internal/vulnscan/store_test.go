package vulnscan

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var t0 = time.Unix(1_800_000_000, 0)

func TestSettingsDefaults(t *testing.T) {
	s := NewStore(newDB(t))
	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.AfterDeploy || got.Daily {
		t.Fatalf("switches = %+v, want all off", got)
	}
	if got.DailyMinute != 180 {
		t.Fatalf("DailyMinute = %d, want 180", got.DailyMinute)
	}
	if got.EffectiveImage() != DefaultScannerImage {
		t.Fatalf("EffectiveImage = %q", got.EffectiveImage())
	}
	if got.LastDailyAt != nil || got.DBUpdatedAt != nil || got.ActiveImage != "" {
		t.Fatalf("unexpected state %+v", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	want := Settings{Enabled: true, AfterDeploy: true, Daily: true, DailyMinute: 75, ScannerImage: "x/y@sha256:" + hex64}
	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveImage(ctx, "x/y@sha256:"+hex64); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastDaily(ctx, t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.AfterDeploy || !got.Daily || got.DailyMinute != 75 || got.ScannerImage != want.ScannerImage {
		t.Fatalf("got %+v", got)
	}
	if got.EffectiveImage() != want.ScannerImage || got.ActiveImage != want.ScannerImage {
		t.Fatalf("images %q %q", got.EffectiveImage(), got.ActiveImage)
	}
	if got.LastDailyAt == nil || !got.LastDailyAt.Equal(t0) {
		t.Fatalf("LastDailyAt = %v", got.LastDailyAt)
	}
}

func TestSetDBErrorKeepsVersion(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	if err := s.SetDB(ctx, "2026-10-27T00:00:00Z", t0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDB(ctx, "ignored", t0.Add(time.Hour), "network down"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Settings(ctx)
	if got.DBVersion != "2026-10-27T00:00:00Z" || !got.DBUpdatedAt.Equal(t0) || got.DBError != "network down" {
		t.Fatalf("got %+v", got)
	}
	if err := s.SetDB(ctx, "2026-10-28T00:00:00Z", t0.Add(2*time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Settings(ctx)
	if got.DBVersion != "2026-10-28T00:00:00Z" || got.DBError != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestEnqueueDedupes(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	added, err := s.Enqueue(ctx, KindImage, "sha256:a", ReasonDeploy, t0)
	if err != nil || !added {
		t.Fatalf("first = %v, %v", added, err)
	}
	added, err = s.Enqueue(ctx, KindImage, "sha256:a", ReasonDaily, t0)
	if err != nil || added {
		t.Fatalf("second = %v, %v", added, err)
	}
	if added, err := s.Enqueue(ctx, KindDBUpdate, "", ReasonDaily, t0); err != nil || !added {
		t.Fatalf("db = %v, %v", added, err)
	}
	if added, err := s.Enqueue(ctx, KindDBUpdate, "", ReasonDaily, t0); err != nil || added {
		t.Fatalf("db again = %v, %v", added, err)
	}
	running, queued, err := s.QueueState(ctx)
	if err != nil || running != nil || queued != 2 {
		t.Fatalf("QueueState = %v, %d, %v", running, queued, err)
	}
}

func TestEnqueueManualUpgradesReason(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.Enqueue(ctx, KindImage, "sha256:a", ReasonDaily, t0)
	if added, err := s.Enqueue(ctx, KindImage, "sha256:a", ReasonManual, t0); err != nil || added {
		t.Fatalf("manual = %v, %v", added, err)
	}
	task, ok, err := s.Next(ctx)
	if err != nil || !ok || task.Reason != ReasonManual {
		t.Fatalf("Next = %+v, %v, %v", task, ok, err)
	}
}

func TestNextOrder(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.Enqueue(ctx, KindImage, "sha256:a", ReasonDaily, t0)
	s.Enqueue(ctx, KindImage, "sha256:b", ReasonDaily, t0.Add(time.Second))
	s.Enqueue(ctx, KindDBUpdate, "", ReasonDaily, t0.Add(2*time.Second))
	var order []string
	for {
		task, ok, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		order = append(order, string(task.Kind)+":"+task.ImageID)
		if err := s.Done(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"db_update:", "image:sha256:a", "image:sha256:b"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("order = %v", order)
	}
}

func TestUnstart(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.Enqueue(ctx, KindImage, "sha256:a", ReasonDaily, t0)
	task, _, _ := s.Next(ctx)
	if err := s.Start(ctx, task.ID, t0); err != nil {
		t.Fatal(err)
	}
	running, queued, _ := s.QueueState(ctx)
	if running == nil || running.ImageID != "sha256:a" || queued != 0 {
		t.Fatalf("running = %+v, queued = %d", running, queued)
	}
	n, err := s.Unstart(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Unstart = %d, %v", n, err)
	}
	running, queued, _ = s.QueueState(ctx)
	if running != nil || queued != 1 {
		t.Fatalf("after Unstart running = %+v, queued = %d", running, queued)
	}
}

func TestClearQueue(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.Enqueue(ctx, KindImage, "sha256:a", ReasonDaily, t0)
	s.Enqueue(ctx, KindDBUpdate, "", ReasonDaily, t0)
	if err := s.ClearQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Next(ctx); ok {
		t.Fatal("queue not empty")
	}
}

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
