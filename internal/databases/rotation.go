package databases

import (
	"slices"
	"time"
)

// The Rotation modes. An empty Mode reads as Simple everywhere.
const (
	RotateSimple = "simple"
	RotateGFS    = "gfs"
)

// Keep is one Rotation rule set: a nil field is no limit. Count and Days
// drive Simple mode; Count and the GFS tiers drive GFS mode (Days is
// Simple-only and normalized to nil on a GFS save).
type Keep struct {
	Mode                                   string
	Count, Days                            *int
	GFSDays, GFSWeeks, GFSMonths, GFSYears *int
}

// RotItem is a Backup, or a copy of one, that Rotation may delete. Group is
// the logical database name: Rotation counts per group.
type RotItem struct {
	ID    int64
	Group string
	At    time.Time
	OK    bool
}

// Rotate returns the IDs of the items to delete. The caller passes only
// candidates: scheduled, finished, not pinned, and the copy in question
// still present.
//
// In each group, newest first (a tie goes to the higher ID), the newest OK
// item is always kept, so a run of failures can never empty a group. Any
// other item is deleted when it is past the keep-last count or older than
// the keep-days limit. Failed items count toward the count like any other.
func Rotate(items []RotItem, k Keep, now time.Time) []int64 {
	groups := map[string][]RotItem{}
	for _, it := range items {
		groups[it.Group] = append(groups[it.Group], it)
	}
	var out []int64
	for _, g := range groups {
		slices.SortFunc(g, func(a, b RotItem) int {
			if c := b.At.Compare(a.At); c != 0 {
				return c
			}
			switch {
			case a.ID > b.ID:
				return -1
			case a.ID < b.ID:
				return 1
			}
			return 0
		})
		keep := slices.IndexFunc(g, func(it RotItem) bool { return it.OK })
		if k.Mode == RotateGFS {
			tiers := gfsKept(g, k, now)
			for i, it := range g {
				if i == keep || (k.Count != nil && i < *k.Count) || tiers[it.ID] {
					continue
				}
				out = append(out, it.ID)
			}
			continue
		}
		for i, it := range g {
			if i == keep {
				continue
			}
			if (k.Count != nil && i >= *k.Count) || (k.Days != nil && now.Sub(it.At) > time.Duration(*k.Days)*24*time.Hour) {
				out = append(out, it.ID)
			}
		}
	}
	slices.Sort(out)
	return out
}

// gfsKept is the IDs of g (sorted newest first) that a GFS tier keeps: the
// newest OK item of each calendar bucket within the last D days, W ISO weeks,
// M months and Y years, bucketed in now's time zone. Failed items never fill
// a slot.
func gfsKept(g []RotItem, k Keep, now time.Time) map[int64]bool {
	kept := map[int64]bool{}
	taken := map[[2]int]bool{} // {tier, bucket}
	tiers := []struct {
		limit *int
		diff  func(now, at time.Time) int
	}{{k.GFSDays, dayDiff}, {k.GFSWeeks, weekDiff}, {k.GFSMonths, monthDiff}, {k.GFSYears, yearDiff}}
	for _, it := range g {
		if !it.OK {
			continue
		}
		for ti, tier := range tiers {
			if tier.limit == nil {
				continue
			}
			d := tier.diff(now, it.At)
			if d < 0 || d >= *tier.limit {
				continue
			}
			if b := [2]int{ti, d}; !taken[b] {
				taken[b] = true
				kept[it.ID] = true
			}
		}
	}
	return kept
}

// midnight is the calendar day of t in loc, as a comparable UTC instant.
func midnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dayDiff is how many calendar days before now's day at's day falls, in
// now's time zone.
func dayDiff(now, at time.Time) int {
	loc := now.Location()
	return int(midnight(now, loc).Sub(midnight(at, loc)).Hours() / 24)
}

// weekDiff counts ISO weeks: the days between the two Mondays, over seven.
func weekDiff(now, at time.Time) int {
	monday := func(t time.Time) time.Time {
		back := (int(t.In(now.Location()).Weekday()) + 6) % 7
		return t.AddDate(0, 0, -back)
	}
	return dayDiff(monday(now), monday(at)) / 7
}

// monthDiff counts calendar months, yearDiff calendar years, in now's zone.
func monthDiff(now, at time.Time) int {
	ny, nm, _ := now.In(now.Location()).Date()
	ay, am, _ := at.In(now.Location()).Date()
	return (ny-ay)*12 + int(nm) - int(am)
}

func yearDiff(now, at time.Time) int {
	return now.In(now.Location()).Year() - at.In(now.Location()).Year()
}
