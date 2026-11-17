package databases

import (
	"slices"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func TestRotate(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ago := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	// items builds newest-first items of one group; the id is its position+1.
	type it struct {
		group string
		days  int
		ok    bool
	}
	build := func(in ...it) []RotItem {
		var out []RotItem
		for i, x := range in {
			out = append(out, RotItem{ID: int64(i + 1), Group: x.group, At: ago(x.days), OK: x.ok})
		}
		return out
	}
	for _, c := range []struct {
		name  string
		items []RotItem
		keep  Keep
		want  []int64
	}{
		{"count", build(it{"a", 0, true}, it{"a", 1, true}, it{"a", 2, true}, it{"a", 3, true}, it{"a", 4, true}), Keep{Count: intp(3)}, []int64{4, 5}},
		{"age", build(it{"a", 1, true}, it{"a", 5, true}, it{"a", 9, true}), Keep{Days: intp(7)}, []int64{3}},
		{"both", build(it{"a", 1, true}, it{"a", 2, true}, it{"a", 3, true}, it{"a", 10, true}), Keep{Count: intp(3), Days: intp(7)}, []int64{4}},
		{"failures never empty", build(it{"a", 0, false}, it{"a", 1, false}, it{"a", 2, false}, it{"a", 3, true}), Keep{Count: intp(2)}, []int64{3}},
		{"age keeps newest ok", build(it{"a", 30, true}), Keep{Days: intp(7)}, nil},
		{"age keeps newest ok past failures", build(it{"a", 1, false}, it{"a", 30, true}, it{"a", 40, true}), Keep{Days: intp(7)}, []int64{3}},
		{"no limits", build(it{"a", 0, true}, it{"a", 100, false}, it{"a", 200, true}), Keep{}, nil},
		{"per database", build(it{"a", 0, true}, it{"b", 0, true}, it{"a", 1, true}, it{"a", 2, true}), Keep{Count: intp(1)}, []int64{3, 4}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Rotate(c.items, c.keep, now)
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Fatalf("Rotate = %v, want %v", got, c.want)
			}
		})
	}
}

// Newest first is by time, then by higher ID; the input order is irrelevant.
func TestRotateOrdersByTimeThenID(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	items := []RotItem{
		{ID: 1, Group: "a", At: now, OK: true},
		{ID: 3, Group: "a", At: now, OK: true},
		{ID: 2, Group: "a", At: now.Add(-time.Hour), OK: true},
	}
	got := Rotate(items, Keep{Count: intp(1)}, now)
	slices.Sort(got)
	if !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("Rotate = %v, want [1 2] (3 is newest by id)", got)
	}
}

func TestRotateGFS(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) // Tuesday, ISO week 47
	day := func(d, hour int) time.Time { return time.Date(2026, 11, 17-d, hour, 0, 0, 0, time.UTC) }
	one := func(at ...time.Time) []RotItem {
		var out []RotItem
		for i, a := range at {
			out = append(out, RotItem{ID: int64(i + 1), Group: "a", At: a, OK: true})
		}
		return out
	}
	for _, c := range []struct {
		name  string
		items []RotItem
		keep  Keep
		now   time.Time
		want  []int64
	}{
		{"count", one(day(0, 10), day(1, 10), day(2, 10), day(3, 10)),
			Keep{Mode: RotateGFS, Count: intp(2)}, now, []int64{3, 4}},
		{"daily tier", one(day(0, 10), day(0, 8), day(1, 10), day(1, 8), day(2, 10), day(2, 8), day(3, 10), day(3, 8), day(4, 10), day(4, 8)),
			Keep{Mode: RotateGFS, GFSDays: intp(3)}, now, []int64{2, 4, 6, 7, 8, 9, 10}},
		// Review Focus 1: a Backup at 23:59 and one at 00:01 are different days.
		{"midnight one day", one(time.Date(2026, 11, 17, 0, 1, 0, 0, time.UTC), time.Date(2026, 11, 16, 23, 59, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSDays: intp(1)}, time.Date(2026, 11, 17, 0, 5, 0, 0, time.UTC), []int64{2}},
		{"midnight two days", one(time.Date(2026, 11, 17, 0, 1, 0, 0, time.UTC), time.Date(2026, 11, 16, 23, 59, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSDays: intp(2)}, time.Date(2026, 11, 17, 0, 5, 0, 0, time.UTC), nil},
		{"weekly", one(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSWeeks: intp(2)}, now, []int64{3}},
		{"monthly", one(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSMonths: intp(2)}, now, []int64{3}},
		{"yearly", one(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSYears: intp(1)}, now, []int64{2}},
		{"failed never fills a slot", []RotItem{
			{ID: 1, Group: "a", At: day(0, 10), OK: true},
			{ID: 2, Group: "a", At: day(1, 10), OK: false},
			{ID: 3, Group: "a", At: day(2, 10), OK: false},
			{ID: 4, Group: "a", At: day(2, 8), OK: true},
		}, Keep{Mode: RotateGFS, GFSDays: intp(3)}, now, []int64{2, 3}},
		{"any rule keeps", one(day(0, 10), day(7, 10)),
			Keep{Mode: RotateGFS, Count: intp(1), GFSWeeks: intp(2)}, now, nil},
		{"gfs ignores simple days", one(day(0, 10), day(1, 10)),
			Keep{Mode: RotateGFS, Days: intp(1), GFSDays: intp(2)}, now, nil},
		// Buckets follow now.Location(): 18:00 UTC Nov 16 is already Nov 17 in
		// ICT, so in ICT the two items sit in different days and both stay.
		{"timezone utc", one(time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSDays: intp(2)}, time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC), []int64{2}},
		{"timezone ict", one(time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)),
			Keep{Mode: RotateGFS, GFSDays: intp(2)}, time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC).In(time.FixedZone("ICT", 7*3600)), nil},
		// Simple is untouched, whatever spells it.
		{"simple empty mode", one(day(0, 10), day(1, 10), day(2, 10), day(3, 10), day(4, 10)),
			Keep{Count: intp(3)}, now, []int64{4, 5}},
		{"simple named mode", one(day(0, 10), day(1, 10), day(2, 10), day(3, 10), day(4, 10)),
			Keep{Mode: RotateSimple, Count: intp(3)}, now, []int64{4, 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Rotate(c.items, c.keep, c.now)
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Fatalf("Rotate = %v, want %v", got, c.want)
			}
		})
	}
}
