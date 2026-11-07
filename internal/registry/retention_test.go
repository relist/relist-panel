package registry

import (
	"slices"
	"testing"
	"time"
)

func tagAt(tag, digest string, hour int) TagInfo {
	return TagInfo{Tag: tag, Digest: digest, Created: time.Date(2026, 11, 7, hour, 0, 0, 0, time.UTC)}
}

func TestKeepsNewestN(t *testing.T) {
	tags := []TagInfo{tagAt("a", "d1", 1), tagAt("b", "d2", 2), tagAt("c", "d3", 3)}
	digests, deleted := SelectDeletions(tags, nil, 2)
	if !slices.Equal(digests, []string{"d1"}) || !slices.Equal(deleted, []string{"a"}) {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
}

func TestKeepsInUse(t *testing.T) {
	tags := []TagInfo{tagAt("a", "d1", 1), tagAt("b", "d2", 2), tagAt("c", "d3", 3)}
	digests, deleted := SelectDeletions(tags, map[string]bool{"a": true}, 1)
	if !slices.Equal(digests, []string{"d2"}) || !slices.Equal(deleted, []string{"b"}) {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
}

func TestSharedDigestKeptByAnyTag(t *testing.T) {
	// "latest" is newest and shares d1 with old "v1": d1 stays, v1 too.
	tags := []TagInfo{tagAt("v1", "d1", 1), tagAt("v2", "d2", 2), tagAt("latest", "d1", 3)}
	digests, deleted := SelectDeletions(tags, nil, 1)
	if !slices.Equal(digests, []string{"d2"}) || !slices.Equal(deleted, []string{"v2"}) {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
	// Both tags of a deleted digest are listed.
	tags = []TagInfo{tagAt("v1", "d1", 1), tagAt("old", "d1", 1), tagAt("v2", "d2", 2)}
	digests, deleted = SelectDeletions(tags, nil, 1)
	if !slices.Equal(digests, []string{"d1"}) || !slices.Equal(deleted, []string{"old", "v1"}) {
		t.Fatalf("shared delete: digests = %v, deleted = %v", digests, deleted)
	}
}

func TestTiesBreakByTagDesc(t *testing.T) {
	tags := []TagInfo{tagAt("1.0.0", "d1", 0), tagAt("1.0.2", "d3", 0), tagAt("1.0.1", "d2", 0)}
	digests, deleted := SelectDeletions(tags, nil, 1)
	if !slices.Equal(digests, []string{"d1", "d2"}) || !slices.Equal(deleted, []string{"1.0.0", "1.0.1"}) {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
}

func TestAllInUseDeletesNothing(t *testing.T) {
	tags := []TagInfo{tagAt("a", "d1", 1), tagAt("b", "d2", 2)}
	digests, deleted := SelectDeletions(tags, map[string]bool{"a": true, "b": true}, 1)
	if len(digests) != 0 || len(deleted) != 0 {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
}

func TestKeepLargerThanTags(t *testing.T) {
	tags := []TagInfo{tagAt("a", "d1", 1)}
	digests, deleted := SelectDeletions(tags, nil, 10)
	if len(digests) != 0 || len(deleted) != 0 {
		t.Fatalf("digests = %v, deleted = %v", digests, deleted)
	}
	if d, _ := SelectDeletions(tags, nil, 0); len(d) != 0 {
		t.Fatalf("keep 0 must keep at least one, deleted %v", d)
	}
}
