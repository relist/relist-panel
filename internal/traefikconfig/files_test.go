package traefikconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsFor(t *testing.T) {
	p := PathsFor("/d")
	for got, want := range map[string]string{
		p.Effective():     "/d/.traefik/static/traefik.yml",
		p.Override():      "/d/.traefik/static/traefik.override.yml",
		p.ApplyFile():     "/d/.traefik/static/apply.json",
		p.HistoryDir():    "/d/.traefik/static/history",
		p.DryRunDir("ab"): "/d/.traefik/static/.dryrun-ab",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestWriteAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.yml")
	if err := WriteAtomic(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "two" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}
