package sources

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureOwnedSrc(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureOwnedSrc(dir); err != nil {
		t.Errorf("absent src: %v", err)
	}
	write(t, filepath.Join(dir, "src", MarkerName), "")
	if err := EnsureOwnedSrc(dir); err != nil {
		t.Errorf("marked src: %v", err)
	}
	foreign := t.TempDir()
	write(t, filepath.Join(foreign, "src", "app.go"), "x")
	if err := EnsureOwnedSrc(foreign); !errors.Is(err, ErrForeignSrc) {
		t.Errorf("unmarked src: err = %v", err)
	}
}

func TestReplaceSrcSwapsAndMarks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", MarkerName), "")
	write(t, filepath.Join(dir, "src", "old.txt"), "old")
	write(t, filepath.Join(dir, ".src.new", "new.txt"), "new")

	if err := ReplaceSrc(dir, filepath.Join(dir, ".src.new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old content remains: %v", err)
	}
	for _, f := range []string{"new.txt", MarkerName} {
		if _, err := os.Stat(filepath.Join(dir, "src", f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "src" {
			t.Errorf("leftover %q", e.Name())
		}
	}
}

func TestReplaceSrcWithoutExistingSrc(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".src.new", "new.txt"), "new")
	if err := ReplaceSrc(dir, filepath.Join(dir, ".src.new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", MarkerName)); err != nil {
		t.Error(err)
	}
}

func TestReplaceSrcKeepsOldWhenNewDirIsMissing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", MarkerName), "")
	write(t, filepath.Join(dir, "src", "old.txt"), "old")
	if err := ReplaceSrc(dir, filepath.Join(dir, ".src.new")); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "old.txt")); err != nil {
		t.Errorf("old src lost: %v", err)
	}
}
