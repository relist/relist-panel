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

func TestReplaceSrcDoesNotWriteMarkerThroughLink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "docker-compose.yml")
	write(t, outside, "services: {}")
	newDir := filepath.Join(dir, ".src.new")
	write(t, filepath.Join(newDir, "a.txt"), "a")
	if err := os.Symlink(outside, filepath.Join(newDir, MarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSrc(dir, newDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "services: {}" {
		t.Errorf("outside file changed: %q", b)
	}
	info, err := os.Lstat(filepath.Join(dir, "src", MarkerName))
	if err != nil || !info.Mode().IsRegular() {
		t.Errorf("marker = %v, %v", info, err)
	}
}

func TestCleanStagingWithSrcRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", "app.go"), "keep")
	write(t, filepath.Join(dir, ".upload-123.zip"), "z")
	write(t, filepath.Join(dir, ".src.new-9", "a"), "x")
	write(t, filepath.Join(dir, ".src.old", "b"), "y")
	write(t, filepath.Join(dir, "docker-compose.yml"), "c")
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{".upload-123.zip", ".src.new-9", ".src.old"} {
		if exists(filepath.Join(dir, gone)) {
			t.Errorf("%s survived", gone)
		}
	}
	for _, kept := range []string{"src/app.go", "docker-compose.yml"} {
		if !exists(filepath.Join(dir, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
}

func TestCleanStagingRestoresHalfSwappedSrc(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".src.old", "app.go"), "previous")
	write(t, filepath.Join(dir, ".src.new-9", "a"), "x")
	write(t, filepath.Join(dir, ".upload-1.zip"), "z")
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "src", "app.go"))
	if err != nil || string(got) != "previous" {
		t.Fatalf("src not restored: %q, %v", got, err)
	}
	for _, gone := range []string{".src.old", ".src.new-9", ".upload-1.zip"} {
		if exists(filepath.Join(dir, gone)) {
			t.Errorf("%s survived", gone)
		}
	}
}

func TestCleanStagingWithoutAnyTreeKeepsStagedTree(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".src.new-9", "a"), "x")
	write(t, filepath.Join(dir, ".upload-1.zip"), "z")
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, ".src.new-9", "a")) {
		t.Error(".src.new-9 was removed though it may be the only copy")
	}
	if exists(filepath.Join(dir, ".upload-1.zip")) {
		t.Error("raw upload survived")
	}
}

func TestCleanStagingRemovesSymlinksAsLinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "p")
	write(t, filepath.Join(dir, "src", "app.go"), "keep")
	for _, name := range []string{".src.new-1", ".src.old", ".upload-1.zip"} {
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(outside, "precious")) {
		t.Fatal("cleanup followed a symlink")
	}
	for _, name := range []string{".src.new-1", ".src.old", ".upload-1.zip"} {
		if exists(filepath.Join(dir, name)) {
			t.Errorf("link %s survived", name)
		}
	}
}

func TestCleanStagingIgnoresSymlinkedOldWhenSrcMissing(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".src.old")); err != nil {
		t.Fatal(err)
	}
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "src")) {
		t.Error("a symlink was restored as src")
	}
}

func TestCleanStagingMissingDir(t *testing.T) {
	if err := CleanStaging(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Errorf("missing dir: %v", err)
	}
}
