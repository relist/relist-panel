package requestmetrics

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

func at(min, sec int) time.Time {
	return t0.Add(time.Duration(min)*time.Minute + time.Duration(sec)*time.Second)
}

func lookup(composeID, domainID int64) (string, bool) {
	if composeID == 12 && domainID == 3 {
		return "a.test", true
	}
	return "", false
}

func newAgg() *Aggregator { return NewAggregator(t0, lookup) }

func line(start time.Time, router string, status int) Line {
	return Line{Start: start, Router: router, Status: status, Duration: 5 * time.Millisecond}
}

func rowsByKey(rows []Row) map[Key]Counts {
	m := map[Key]Counts{}
	for _, r := range rows {
		c := m[r.Key]
		c.Merge(&r.Counts)
		m[r.Key] = c
	}
	return m
}

// closeAll pushes the watermark far enough to close every earlier minute; the
// closer line itself stays open.
func closeAll(a *Aggregator) []Row {
	a.Add(line(at(60, 0), "", 200), 1<<40, at(60, 0))
	return a.CloseReady(at(60, 0))
}

func TestSeriesMapping(t *testing.T) {
	a := newAgg()
	now := at(0, 5)
	for i, router := range []string{"panel@docker", "relist-c12-d3@docker", "relist-c12-d9@docker", "relist-c12-d3-http@docker", "myapp@docker"} {
		a.Add(line(at(0, 1), router, 200), int64(i), now)
	}
	m := rowsByKey(closeAll(a))
	if got := m[Key{Scope: ScopeTotal}].Requests(); got != 5 {
		t.Errorf("total = %d, want 5", got)
	}
	if got := m[Key{Scope: ScopePanel}].Requests(); got != 1 {
		t.Errorf("panel = %d, want 1", got)
	}
	if got := m[Key{Scope: ScopeHost, ComposeID: 12, Host: "a.test"}].Requests(); got != 1 {
		t.Errorf("host = %d, want 1", got)
	}
	if len(m) != 3 {
		t.Errorf("series = %d, want 3", len(m))
	}
}

func TestStatusClasses(t *testing.T) {
	a := newAgg()
	for i, s := range []int{101, 200, 302, 404, 503, 0, 600} {
		a.Add(line(at(0, 1), "", s), int64(i), at(0, 5))
	}
	c := rowsByKey(closeAll(a))[Key{Scope: ScopeTotal}]
	if c.C1xx != 1 || c.C2xx != 1 || c.C3xx != 1 || c.C4xx != 1 || c.C5xx != 1 || c.Cother != 2 {
		t.Errorf("counts = %+v", c)
	}
}

func TestWatermarkClose(t *testing.T) {
	a := newAgg()
	a.Add(line(at(0, 10), "", 200), 0, at(0, 10))
	a.Add(line(at(1, 20), "", 200), 1, at(1, 20))
	if rows := a.CloseReady(at(1, 20)); len(rows) != 0 {
		t.Fatalf("closed too early: %d rows", len(rows))
	}
	a.Add(line(at(1, 30), "", 200), 2, at(1, 30))
	rows := a.CloseReady(at(1, 30))
	if len(rows) != 1 || !rows[0].BucketAt.Equal(at(0, 0)) || rows[0].Counts.Requests() != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestOutOfOrderWithinGrace(t *testing.T) {
	a := newAgg()
	a.Add(line(at(0, 5), "", 200), 0, at(1, 25))
	a.Add(line(at(1, 25), "", 200), 1, at(1, 25))
	if !a.Add(line(at(0, 59), "", 200), 2, at(1, 26)) {
		t.Fatal("line within grace dropped")
	}
	a.Add(line(at(1, 40), "", 200), 3, at(1, 40))
	rows := a.CloseReady(at(1, 40))
	if len(rows) != 1 || rows[0].Counts.Requests() != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestLateLineDropped(t *testing.T) {
	a := newAgg()
	a.Add(line(at(0, 10), "", 200), 0, at(0, 10))
	a.Add(line(at(1, 40), "", 200), 1, at(1, 40))
	if len(a.CloseReady(at(1, 40))) != 1 {
		t.Fatal("10:00 should have closed")
	}
	if a.Add(line(at(0, 50), "", 200), 2, at(1, 41)) {
		t.Fatal("late line accepted")
	}
	if a.Dropped() != 1 {
		t.Fatalf("dropped = %d", a.Dropped())
	}
}

func TestIdleClose(t *testing.T) {
	a := newAgg()
	a.Add(line(at(0, 10), "", 200), 0, at(0, 11))
	if rows := a.CloseReady(at(2, 10)); len(rows) != 0 {
		t.Fatal("closed before 2 idle minutes")
	}
	if rows := a.CloseReady(at(2, 11)); len(rows) != 1 {
		t.Fatalf("idle close rows = %d", len(rows))
	}
}

func TestCursor(t *testing.T) {
	a := newAgg()
	for i, off := range []int64{0, 100, 200} {
		a.Add(line(at(0, 10+i), "", 200), off, at(0, 20))
	}
	a.Add(line(at(1, 5), "", 200), 300, at(1, 5))
	a.Add(line(at(1, 31), "", 200), 400, at(1, 31)) // closes 10:00, opens nothing new... it is in 10:01
	if rows := a.CloseReady(at(1, 31)); len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	off, through := a.Cursor(500)
	if off != 300 || !through.Equal(at(1, 0)) {
		t.Fatalf("cursor = %d, %v", off, through)
	}
}

func TestCursorNothingOpen(t *testing.T) {
	a := newAgg()
	if off, through := a.Cursor(77); off != 77 || !through.Equal(t0) {
		t.Fatalf("empty cursor = %d, %v", off, through)
	}
	a.Add(line(at(0, 10), "", 200), 0, at(0, 11))
	a.CloseReady(at(5, 0))
	if off, through := a.Cursor(77); off != 77 || !through.Equal(at(1, 0)) {
		t.Fatalf("after close = %d, %v", off, through)
	}
}

func TestCursorAfterNewFile(t *testing.T) {
	a := newAgg()
	a.Add(line(at(1, 5), "", 200), 500, at(1, 5))
	a.NewFile()
	if off, through := a.Cursor(50); off != 50 || !through.Equal(at(1, 0)) {
		t.Fatalf("no new-file line = %d, %v", off, through)
	}
	a.Add(line(at(1, 6), "", 200), 0, at(1, 6))
	if off, _ := a.Cursor(50); off != 0 {
		t.Fatalf("with new-file line = %d", off)
	}
}
