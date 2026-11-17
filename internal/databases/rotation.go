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
