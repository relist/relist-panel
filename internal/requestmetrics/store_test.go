package requestmetrics

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mkRow(k Key, when time.Time, c2xx uint64, latencies ...time.Duration) Row {
	r := Row{Key: k, BucketAt: when}
	r.Counts.C2xx = c2xx
	for _, d := range latencies {
		r.Counts.Latency.Observe(d)
	}
	return r
}

var total = Key{Scope: ScopeTotal}

func TestWriteMinutesAndCursorAreOneTransaction(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openDB(t))
	if _, ok, err := s.Cursor(ctx); ok || err != nil {
		t.Fatalf("empty cursor = %v, %v", ok, err)
	}
	row := mkRow(total, at(0, 0), 3, 5*time.Millisecond, 50*time.Millisecond)
	row.Counts.C5xx = 1
	cur := Cursor{Inode: 4242, ResumeOffset: 900, FlushedThrough: at(1, 0)}
	if err := s.WriteMinutes(ctx, []Row{row}, cur); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Cursor(ctx)
	if err != nil || !ok || got.Inode != 4242 || got.ResumeOffset != 900 || !got.FlushedThrough.Equal(at(1, 0)) {
		t.Fatalf("cursor = %+v, %v, %v", got, ok, err)
	}
	rows, err := s.Rows(ctx, Minute, []Key{total}, at(-5, 0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if rows[0].Counts.C2xx != 3 || rows[0].Counts.C5xx != 1 || rows[0].Counts.Latency.counts != row.Counts.Latency.counts || !rows[0].BucketAt.Equal(at(0, 0)) {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestCompactHour(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openDB(t))
	var rows []Row
	for m := 0; m < 3; m++ {
		rows = append(rows, mkRow(total, at(m, 0), 10, time.Duration(m+1)*10*time.Millisecond))
	}
	rows = append(rows, mkRow(total, at(60, 0), 7, time.Millisecond))
	if err := s.WriteMinutes(ctx, rows, Cursor{FlushedThrough: at(61, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LatestHour(ctx); ok {
		t.Fatal("no hourly rows yet")
	}
	if err := s.CompactHour(ctx, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	h, ok, err := s.LatestHour(ctx)
	if err != nil || !ok || !h.Equal(at(0, 0)) {
		t.Fatalf("latest hour = %v, %v, %v", h, ok, err)
	}
	hours, err := s.Rows(ctx, Hour, []Key{total}, at(-60, 0))
	if err != nil || len(hours) != 1 {
		t.Fatalf("hourly = %v, %v", hours, err)
	}
	if hours[0].Counts.C2xx != 30 || hours[0].Counts.Latency.Total() != 3 {
		t.Fatalf("hour = %+v", hours[0].Counts)
	}
}

func TestDeleteBefore(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openDB(t))
	if err := s.WriteMinutes(ctx, []Row{mkRow(total, at(0, 0), 1), mkRow(total, at(1, 0), 1), mkRow(total, at(2, 0), 1)}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompactHour(ctx, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBefore(ctx, at(1, 0), at(60, 0)); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Rows(ctx, Minute, []Key{total}, at(-60, 0))
	if len(rows) != 2 || !rows[0].BucketAt.Equal(at(1, 0)) {
		t.Fatalf("minutes left = %+v", rows)
	}
	hours, _ := s.Rows(ctx, Hour, []Key{total}, at(-60, 0))
	if len(hours) != 0 {
		t.Fatalf("hours left = %+v", hours)
	}
}

func TestRetention(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openDB(t))
	if d, err := s.RetentionDays(ctx); err != nil || d != 1 {
		t.Fatalf("default = %d, %v", d, err)
	}
	if err := s.SaveRetention(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.RetentionDays(ctx); d != 90 {
		t.Fatalf("saved = %d", d)
	}
	for _, bad := range []int{0, 91, -1} {
		if err := s.SaveRetention(ctx, bad); !errors.Is(err, ErrInvalidRetention) {
			t.Errorf("SaveRetention(%d) = %v", bad, err)
		}
	}
}

func TestRowsFiltersKeys(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openDB(t))
	a := Key{Scope: ScopeHost, ComposeID: 1, Host: "a.test"}
	b := Key{Scope: ScopeHost, ComposeID: 1, Host: "b.test"}
	if err := s.WriteMinutes(ctx, []Row{mkRow(total, at(0, 0), 9), mkRow(a, at(0, 0), 1), mkRow(b, at(0, 0), 2), mkRow(a, at(1, 0), 3)}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows(ctx, Minute, []Key{a}, at(-1, 0))
	if err != nil || len(rows) != 2 || rows[0].Key != a || rows[1].Counts.C2xx != 3 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	rows, _ = s.Rows(ctx, Minute, []Key{a, b}, at(1, 0))
	if len(rows) != 1 {
		t.Fatalf("from filter: %+v", rows)
	}
	if rows, _ := s.Rows(ctx, Minute, nil, at(-1, 0)); len(rows) != 0 {
		t.Fatal("no keys, no rows")
	}
}

func openDBB(b *testing.B) *sql.DB {
	b.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(b.TempDir(), "panel.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}
