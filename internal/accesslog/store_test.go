package accesslog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestStoreDefaults(t *testing.T) {
	got, err := newStore(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != (Settings{RetentionDays: 90}) {
		t.Fatalf("settings = %+v, want 90 days, not forever, never run", got)
	}
}

func TestStoreSaveRetention(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.SaveRetention(ctx, 30, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRetention(ctx, 0, false); !errors.Is(err, ErrInvalidRetention) {
		t.Fatalf("SaveRetention(0) = %v, want ErrInvalidRetention", err)
	}
	got, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionDays != 30 || !got.KeepForever {
		t.Fatalf("settings = %+v, want 30 days, forever", got)
	}
}

func TestStoreRecordRun(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	t0 := time.Date(2026, 11, 3, 0, 0, 30, 0, time.UTC)
	if err := s.RecordRun(ctx, t0, "boom"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRotatedAt == nil || !got.LastRotatedAt.Equal(t0) || got.LastError != "boom" {
		t.Fatalf("settings = %+v, want run at %v with error boom", got, t0)
	}
	if err := s.RecordRun(ctx, t0.Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx); got.LastError != "" || !got.LastRotatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("settings = %+v, want the error cleared", got)
	}
}
