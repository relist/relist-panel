package registry

import (
	"slices"
	"sort"
	"strings"
)

// SelectDeletions picks what cleanup deletes from one repository. A tag is
// kept when it is among the keep newest (by Created, ties by tag name
// descending) or in use; a digest is deleted only when none of its tags is
// kept, because deleting a manifest removes every tag on it. Both lists come
// back sorted; deletedTags is every tag of the deleted digests. keep below 1
// counts as 1.
func SelectDeletions(tags []TagInfo, inUse map[string]bool, keep int) (deleteDigests []string, deletedTags []string) {
	keep = max(keep, 1)
	sorted := slices.Clone(tags)
	sortNewestFirst(sorted)
	kept := map[string]bool{}
	for i, t := range sorted {
		if i < keep || inUse[t.Tag] {
			kept[t.Digest] = true
		}
	}
	seen := map[string]bool{}
	for _, t := range sorted {
		if kept[t.Digest] {
			continue
		}
		deletedTags = append(deletedTags, t.Tag)
		if !seen[t.Digest] {
			seen[t.Digest] = true
			deleteDigests = append(deleteDigests, t.Digest)
		}
	}
	sort.Strings(deleteDigests)
	sort.Strings(deletedTags)
	return deleteDigests, deletedTags
}

// sortNewestFirst orders tags by Created descending, ties by tag name
// descending (reproducible builds share one fixed created time).
func sortNewestFirst(tags []TagInfo) {
	slices.SortStableFunc(tags, func(a, b TagInfo) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(b.Tag, a.Tag)
	})
}
