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
