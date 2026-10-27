package vulnscan

import (
	"context"
	"testing"
	"time"
)

func TestDueDaily(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	at := func(day, hour, minute int, loc *time.Location) time.Time {
		return time.Date(2026, 10, day, hour, minute, 0, 0, loc)
	}
	ptr := func(v time.Time) *time.Time { return &v }
	on := Settings{Enabled: true, Daily: true, DailyMinute: 180}
	tests := []struct {
		name string
		now  time.Time
		st   Settings
		want bool
	}{
		{"before the time", at(28, 2, 59, time.UTC), on, false},
		{"at the time, never run", at(28, 3, 0, time.UTC), on, true},
		{"after, last run today", at(28, 9, 0, time.UTC), withLast(on, ptr(at(28, 3, 1, time.UTC))), false},
		{"after, last run yesterday", at(28, 9, 0, time.UTC), withLast(on, ptr(at(27, 3, 1, time.UTC))), true},
		{"before today's time, last run yesterday", at(28, 1, 0, time.UTC), withLast(on, ptr(at(27, 3, 1, time.UTC))), false},
		{"daily off", at(28, 9, 0, time.UTC), Settings{Enabled: true, DailyMinute: 180}, false},
		{"master off", at(28, 9, 0, time.UTC), Settings{Daily: true, DailyMinute: 180}, false},
		{"local zone", at(28, 3, 30, bangkok), withLast(on, ptr(at(27, 21, 0, time.UTC))), false}, // 04:00 ICT yesterday-UTC is today's run
		{"local zone due", at(28, 3, 30, bangkok), withLast(on, ptr(at(27, 19, 0, time.UTC))), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dueDaily(tt.now, tt.st); got != tt.want {
				t.Fatalf("dueDaily = %v, want %v", got, tt.want)
			}
		})
	}
}

func withLast(st Settings, last *time.Time) Settings {
	st.LastDailyAt = last
	return st
}

func TestNextDaily(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	if NextDaily(now, Settings{Enabled: true}) != nil {
		t.Fatal("next daily while daily is off")
	}
	got := NextDaily(now, Settings{Enabled: true, Daily: true, DailyMinute: 180})
	if got == nil || !got.Equal(time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v", got)
	}
	got = NextDaily(now, Settings{Enabled: true, Daily: true, DailyMinute: 600})
	if got == nil || !got.Equal(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v", got)
	}
}

func TestRunDailyPrunesAndEnqueues(t *testing.T) {
	h := newHarness(t)
	h.enable()
	ctx := context.Background()
	h.store.RecordScan(ctx, "sha256:gone", "gone:1", Report{}, "db1", t0)
	h.store.RecordScan(ctx, "sha256:app", "app:1", Report{}, "db1", t0)
	if err := h.svc.runDaily(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := h.store.ScanState(ctx, "sha256:gone"); found {
		t.Fatal("unused image not pruned")
	}
	if _, _, found, _ := h.store.ScanState(ctx, "sha256:app"); !found {
		t.Fatal("used image pruned")
	}
	task, ok, _ := h.store.Next(ctx)
	_, queued, _ := h.store.QueueState(ctx)
	if !ok || task.Kind != KindDBUpdate || task.Reason != ReasonDaily || queued != 4 {
		t.Fatalf("first task %+v, queued %d", task, queued)
	}
	st, _ := h.store.Settings(ctx)
	if st.LastDailyAt == nil || !st.LastDailyAt.Equal(t0) {
		t.Fatalf("LastDailyAt = %v", st.LastDailyAt)
	}
}

func TestSchedulerFiresOnce(t *testing.T) {
	h := newHarness(t)
	h.enable()
	ctx := context.Background()
	st, _ := h.store.Settings(ctx)
	st.Daily, st.DailyMinute = true, 0
	h.store.SaveSettings(ctx, st)
	h.svc.tickDaily(ctx)
	h.svc.tickDaily(ctx)
	if n := countDaily(t, h); n != 1 {
		t.Fatalf("daily ran %d times", n)
	}
}

func countDaily(t *testing.T, h *harness) int {
	var n int
	h.store.db.QueryRow(`SELECT COUNT(*) FROM scan_queue WHERE kind = 'db_update' AND reason = 'daily'`).Scan(&n)
	st, _ := h.store.Settings(context.Background())
	if st.LastDailyAt == nil {
		return 0
	}
	return n
}
