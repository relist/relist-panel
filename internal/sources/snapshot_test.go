package sources

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Fetching the branch rather than the saved revision would deploy "two".
func TestPinnedRevisionSurvivesMovingBranch(t *testing.T) {
	r := newRemoteRepo(t)
	sha := r.commit("main", "app.txt", "one", "one")
	dir := t.TempDir()
	src := gitSource(r.url, "main")
	pinned, err := (Git{}).ResolveRevision(context.Background(), src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if pinned != sha {
		t.Fatalf("revision = %q, want %q", pinned, sha)
	}
	r.commit("main", "app.txt", "two", "two")
	src.PinnedSHA = pinned
	ref, err := (Git{}).Fetch(context.Background(), src, dir, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "src", "app.txt"))
	if ref.SHA != sha || string(content) != "one" {
		t.Fatalf("fetched mutable branch: %+v %q", ref, content)
	}
}
