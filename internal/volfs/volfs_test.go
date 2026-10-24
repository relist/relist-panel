package volfs

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// fixture is a Volume root opened over a real temp directory, with its parent
// kept on hand so escape attempts have real data outside to fail to reach.
type fixture struct {
	*FS
	root string // the Volume root's host path, for planting data directly
	out  string // root's parent — outside the root
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	out := t.TempDir()
	root := filepath.Join(out, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	fsys, err := Open(root)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", root, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return fixture{FS: fsys, root: root, out: out}
}

// path is a root-relative path's host path, for direct setup and assertions.
func (f fixture) path(rel string) string {
	return filepath.Join(f.root, filepath.FromSlash(rel))
}

// plant writes a file directly on the host, under the root — test setup, not
// the code under test.
func (f fixture) plant(t *testing.T, rel, content string) {
	t.Helper()
	full := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// link plants a symlink directly: symlink creation is out of scope for the
// API under test.
func (f fixture) link(t *testing.T, target, rel string) {
	t.Helper()
	if err := os.Symlink(target, f.path(rel)); err != nil {
		t.Fatalf("symlink %s -> %s: %v", rel, target, err)
	}
}

// assertGone fails unless rel no longer exists under the root.
func (f fixture) assertGone(t *testing.T, rel string) {
	t.Helper()
	if _, err := f.Stat(rel); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%q) error = %v, want fs.ErrNotExist", rel, err)
	}
}

// os.Root is the containment, so every escaping shape fails with its own
// "path escapes from parent" — not an error this package hand-rolled — and
// nothing outside the root is touched.
func TestEscapingPathsFailWithGoRootsOwnError(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "dir/placeholder.txt", "inside")
	outside := filepath.Join(f.out, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	absoluteTarget := filepath.Join(f.out, "absolute.txt")

	ops := []struct {
		name string
		run  func() error
	}{
		{"List ..", func() error { _, err := f.List(".."); return err }},
		{"List absolute", func() error { _, err := f.List("/etc/passwd"); return err }},
		{"List dir/../../x", func() error { _, err := f.List("dir/../../x"); return err }},
		{"Stat ..", func() error { _, err := f.Stat("../secret.txt"); return err }},
		{"Stat absolute", func() error { _, err := f.Stat("/etc/passwd"); return err }},
		{"Stat dir/../../x", func() error { _, err := f.Stat("dir/../../secret.txt"); return err }},
		{"Read ..", func() error { return f.Read("../secret.txt", io.Discard) }},
		{"Read absolute", func() error { return f.Read("/etc/passwd", io.Discard) }},
		{"Read dir/../../x", func() error { return f.Read("dir/../../secret.txt", io.Discard) }},
		{"Write ..", func() error { return f.Write("../escaped.txt", strings.NewReader("x"), -1) }},
		{"Write absolute", func() error { return f.Write(absoluteTarget, strings.NewReader("x"), -1) }},
		{"Delete absolute", func() error { return f.Delete("/etc/passwd") }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			err := op.run()
			if err == nil {
				t.Fatal("error = nil, want the root to refuse the path")
			}
			if !strings.Contains(err.Error(), "path escapes from parent") {
				t.Errorf("error = %v, want os.Root's own %q", err, "path escapes from parent")
			}
		})
	}

	// Nothing outside was read, written or deleted.
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "secret" {
		t.Errorf("outside secret.txt = %q, %v; want it untouched", data, err)
	}
	if _, err := os.Stat(absoluteTarget); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an escaping Write created %s", absoluteTarget)
	}
	if _, err := os.Stat(filepath.Join(f.out, "escaped.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an escaping Write created %s", filepath.Join(f.out, "escaped.txt"))
	}

	// Percent-encoding is decoded by the panel's HTTP layer, not here: the
	// encoded bytes arrive as one plain component and name a file inside the
	// root that does not exist — never the sibling they would decode to.
	if _, err := f.Stat("%2e%2e%2fsecret.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(percent-encoded) error = %v, want fs.ErrNotExist", err)
	}
}

// ListFirst is List capped: it keeps the first limit names in name order and
// reports that it cut the rest off, and it stats nothing past the cap.
func TestListFirstTruncatesSortedByName(t *testing.T) {
	f := newFixture(t)
	for i := 11; i >= 0; i-- {
		f.plant(t, fmt.Sprintf("f%02d.txt", i), "x")
	}

	entries, truncated, err := f.ListFirst(".", "", 10)
	if err != nil {
		t.Fatalf("ListFirst limit 10 error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	want := []string{"f00.txt", "f01.txt", "f02.txt", "f03.txt", "f04.txt", "f05.txt", "f06.txt", "f07.txt", "f08.txt", "f09.txt"}
	if !reflect.DeepEqual(names, want) || !truncated {
		t.Errorf("ListFirst limit 10 = %q truncated=%v, want the first ten by name and truncated", names, truncated)
	}

	entries, truncated, err = f.ListFirst(".", "", 12)
	if err != nil || len(entries) != 12 || truncated {
		t.Errorf("ListFirst limit 12 = %d entries truncated=%v err=%v, want 12 and not truncated", len(entries), truncated, err)
	}
	entries, truncated, err = f.ListFirst(".", "", 20)
	if err != nil || len(entries) != 12 || truncated {
		t.Errorf("ListFirst limit 20 = %d entries truncated=%v err=%v, want 12 and not truncated", len(entries), truncated, err)
	}
}

// With after, ListFirst keeps only the names sorting strictly after it, then
// applies the cap: the pages of a listing chain through the last name.
func TestListFirstPagesAfterACursor(t *testing.T) {
	f := newFixture(t)
	for i := 11; i >= 0; i-- {
		f.plant(t, fmt.Sprintf("f%02d.txt", i), "x")
	}
	namesOf := func(entries []Entry) []string {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
		return names
	}

	entries, truncated, err := f.ListFirst(".", "f04.txt", 3)
	if want := []string{"f05.txt", "f06.txt", "f07.txt"}; err != nil || !reflect.DeepEqual(namesOf(entries), want) || !truncated {
		t.Errorf("after f04.txt limit 3 = %q truncated=%v err=%v, want %q and truncated", namesOf(entries), truncated, err, want)
	}
	// A cursor that names no entry still splits the order.
	entries, truncated, err = f.ListFirst(".", "f09.z", 10)
	if want := []string{"f10.txt", "f11.txt"}; err != nil || !reflect.DeepEqual(namesOf(entries), want) || truncated {
		t.Errorf("after f09.z = %q truncated=%v err=%v, want %q and not truncated", namesOf(entries), truncated, err, want)
	}
	// The last name leaves nothing, and exactly limit names left is not truncated.
	entries, truncated, err = f.ListFirst(".", "f11.txt", 10)
	if err != nil || len(entries) != 0 || truncated {
		t.Errorf("after the last name = %d entries truncated=%v err=%v, want none", len(entries), truncated, err)
	}
	entries, truncated, err = f.ListFirst(".", "f08.txt", 3)
	if err != nil || len(entries) != 3 || truncated {
		t.Errorf("after f08.txt limit 3 = %d entries truncated=%v err=%v, want 3 and not truncated", len(entries), truncated, err)
	}
}

// List is one directory level of raw ReadDir names with Lstat metadata and
// Readlink targets: sorted, one level deep, and a name containing a newline
// round-trips intact.
func TestListIsOneLevelOfRawNamesWithMetadata(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "plain.txt", "hello")
	if err := os.Chmod(f.path("plain.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	f.plant(t, "sub/nested.txt", "nested")
	f.plant(t, "a\nb.txt", "newline")
	if err := os.Mkdir(f.path("empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.link(t, "plain.txt", "link-plain")

	entries, err := f.List(".")
	if err != nil {
		t.Fatalf("List(\".\") error = %v", err)
	}

	names := make([]string, 0, len(entries))
	byName := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		byName[entry.Name] = entry
	}
	want := []string{"a\nb.txt", "empty", "link-plain", "plain.txt", "sub"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("List(\".\") names = %q, want %q (raw names, sorted by name)", names, want)
	}

	plain := byName["plain.txt"]
	if plain.Kind != KindFile || plain.Size != 5 || plain.Mode.Perm() != 0o640 {
		t.Errorf("plain.txt = %+v, want a 5-byte 0640 file", plain)
	}
	if plain.UID != uint32(os.Getuid()) || plain.GID != uint32(os.Getgid()) {
		t.Errorf("plain.txt owner = %d:%d, want %d:%d", plain.UID, plain.GID, os.Getuid(), os.Getgid())
	}
	if plain.ModTime.IsZero() {
		t.Error("plain.txt mtime = zero, want a real timestamp")
	}
	if plain.Link != "" {
		t.Errorf("plain.txt link = %q, want empty", plain.Link)
	}

	if dir := byName["empty"]; dir.Kind != KindDir {
		t.Errorf("empty = %+v, want a directory", dir)
	}
	link := byName["link-plain"]
	if link.Kind != KindSymlink || link.Link != "plain.txt" || link.Size != int64(len("plain.txt")) {
		t.Errorf("link-plain = %+v, want symlink -> plain.txt with the target's length", link)
	}

	// One level only: sub's child is not in the root listing, and listing sub
	// finds it.
	if _, ok := byName["nested.txt"]; ok {
		t.Error("List(\".\") walked into sub; want one level")
	}
	sub, err := f.List("sub")
	if err != nil {
		t.Fatalf("List(\"sub\") error = %v", err)
	}
	if len(sub) != 1 || sub[0].Name != "nested.txt" {
		t.Errorf("List(\"sub\") = %+v, want exactly nested.txt", sub)
	}

	// The exact bytes ReadDir saw are the bytes that open the file.
	var content bytes.Buffer
	if err := f.Read("a\nb.txt", &content); err != nil {
		t.Fatalf("Read(newline name) error = %v", err)
	}
	if content.String() != "newline" {
		t.Errorf("Read(newline name) = %q, want %q", content.String(), "newline")
	}

	rootEntry, err := f.Stat(".")
	if err != nil {
		t.Fatalf("Stat(\".\") error = %v", err)
	}
	if rootEntry.Kind != KindDir {
		t.Errorf("Stat(\".\") = %+v, want the root directory", rootEntry)
	}
}

// Stat answers with the same metadata as the listing, for a plain file and a
// symlink alike.
func TestStatDescribesTheSameEntryAsList(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "sub/file.txt", "x")
	f.link(t, "file.txt", "sub/link.txt")

	entries, err := f.List("sub")
	if err != nil {
		t.Fatalf("List(\"sub\") error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(\"sub\") = %+v, want two entries", entries)
	}

	for _, entry := range entries {
		got, err := f.Stat("sub/" + entry.Name)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", entry.Name, err)
		}
		if !reflect.DeepEqual(got, entry) {
			t.Errorf("Stat(%q) = %+v, want the List entry %+v", entry.Name, got, entry)
		}
	}
}

// The spec's symlink table: every link is listed with its target, only one
// that stays inside the root opens, and the ones pointing outside — a
// relative climb and /etc/passwd — are refused with os.Root's own error while
// remaining describable.
func TestSymlinksAreListedAndOnlyFollowedInsideTheRoot(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "inside/real.txt", "real")
	f.plant(t, "dir-inside/nested.txt", "nested")
	if err := os.WriteFile(filepath.Join(f.out, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.link(t, "inside/real.txt", "link-inside")
	f.link(t, "dir-inside", "link-dir")
	f.link(t, "../secret.txt", "link-rel-outside")
	f.link(t, "/etc/passwd", "link-etc")
	f.link(t, "missing.txt", "link-dangling")

	entries, err := f.List(".")
	if err != nil {
		t.Fatalf("List(\".\") error = %v", err)
	}
	links := map[string]string{}
	for _, entry := range entries {
		if entry.Kind == KindSymlink {
			links[entry.Name] = entry.Link
		}
	}
	want := map[string]string{
		"link-inside":      "inside/real.txt",
		"link-dir":         "dir-inside",
		"link-rel-outside": "../secret.txt",
		"link-etc":         "/etc/passwd",
		"link-dangling":    "missing.txt",
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("listed symlinks = %v, want %v (targets reported, escapes included)", links, want)
	}

	// A link that stays inside is followed by Read and by List.
	var content bytes.Buffer
	if err := f.Read("link-inside", &content); err != nil {
		t.Fatalf("Read(link-inside) error = %v", err)
	}
	if content.String() != "real" {
		t.Errorf("Read(link-inside) = %q, want %q", content.String(), "real")
	}
	nested, err := f.List("link-dir")
	if err != nil {
		t.Fatalf("List(link-dir) error = %v", err)
	}
	if len(nested) != 1 || nested[0].Name != "nested.txt" {
		t.Errorf("List(link-dir) = %+v, want dir-inside's child", nested)
	}

	// Stat describes the link itself, never the target, so an escaping link
	// is still describable.
	stat, err := f.Stat("link-etc")
	if err != nil {
		t.Fatalf("Stat(link-etc) error = %v", err)
	}
	if stat.Kind != KindSymlink || stat.Link != "/etc/passwd" {
		t.Errorf("Stat(link-etc) = %+v, want symlink -> /etc/passwd", stat)
	}

	// Every operation that resolves through the link is refused.
	for _, name := range []string{"link-rel-outside", "link-etc"} {
		t.Run(name, func(t *testing.T) {
			if err := f.Read(name, io.Discard); err == nil || !strings.Contains(err.Error(), "path escapes from parent") {
				t.Errorf("Read(%q) error = %v, want %q", name, err, "path escapes from parent")
			}
			if err := f.Write(name, strings.NewReader("x"), -1); err == nil || !strings.Contains(err.Error(), "path escapes from parent") {
				t.Errorf("Write(%q) error = %v, want %q", name, err, "path escapes from parent")
			}
		})
	}

	// A dangling link is still listed with its target; following it fails as a
	// missing file, not as an escape.
	if err := f.Read("link-dangling", io.Discard); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(link-dangling) error = %v, want fs.ErrNotExist", err)
	}
}

// Read and Write stream: a payload well past any single buffer round-trips
// byte-for-byte, an overwrite truncates, and a failing source is reported.
func TestWriteAndReadStreamRoundTrip(t *testing.T) {
	f := newFixture(t)

	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := f.Write("big.bin", bytes.NewReader(payload), -1); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got bytes.Buffer
	if err := f.Read("big.bin", &got); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Errorf("Read() returned %d bytes differing from the %d written", got.Len(), len(payload))
	}

	// Overwriting truncates: the old tail must not survive a shorter write.
	if err := f.Write("big.bin", strings.NewReader("short"), -1); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	got.Reset()
	if err := f.Read("big.bin", &got); err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if got.String() != "short" {
		t.Errorf("after overwrite Read() = %q, want %q", got.String(), "short")
	}

	entry, err := f.Stat("big.bin")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if entry.Size != int64(len("short")) {
		t.Errorf("Stat().Size = %d, want %d", entry.Size, len("short"))
	}

	if err := f.Write("failed.bin", errorReader{}, -1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Write(failing reader) error = %v, want the reader's failure", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// randomBytes is n bytes that cannot be mistaken for each other.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// noTempLeft fails if a write left a `.relist-write-*` temp in dir.
func noTempLeft(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".relist-write-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("a failed write left temp files behind: %v", matches)
	}
}

// A write that fails partway must leave the original byte-identical: the
// stream goes to a temp file beside the target and only a complete one is
// renamed over it (ticket 19).
func TestWriteFailingMidStreamKeepsOriginal(t *testing.T) {
	f := newFixture(t)
	original := randomBytes(t, 14000)
	f.plant(t, "config.db", string(original))

	src := io.MultiReader(bytes.NewReader(randomBytes(t, 100)), failingReader{errors.New("connection reset")})
	err := f.Write("config.db", src, -1)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("Write() error = %v, want the stream's failure", err)
	}

	got, err := os.ReadFile(f.path("config.db"))
	if err != nil || !bytes.Equal(got, original) {
		t.Errorf("config.db after the failed write = %d bytes, %v; want the original %d bytes", len(got), err, len(original))
	}
	noTempLeft(t, f.root)
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// A stream that ends before the declared size is a cut-off upload, not a
// smaller file: the original stays and the error names ErrIncompleteWrite.
func TestWriteShortStreamWithExpectKeepsOriginal(t *testing.T) {
	f := newFixture(t)
	original := randomBytes(t, 14000)
	f.plant(t, "config.db", string(original))

	err := f.Write("config.db", io.LimitReader(bytes.NewReader(randomBytes(t, 40000)), 100), 40000)
	if !errors.Is(err, ErrIncompleteWrite) {
		t.Fatalf("Write() error = %v, want ErrIncompleteWrite", err)
	}

	got, err := os.ReadFile(f.path("config.db"))
	if err != nil || !bytes.Equal(got, original) {
		t.Errorf("config.db after the short write = %d bytes, %v; want the original %d bytes", len(got), err, len(original))
	}
	noTempLeft(t, f.root)
}

// A successful replacement carries the new bytes and the old file's mode,
// not the umask-derived mode of the temp file.
func TestWriteReplacesAndKeepsMode(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "secret.key", "old and longer than the new content")
	if err := os.Chmod(f.path("secret.key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.Write("secret.key", strings.NewReader("new"), 3); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := os.ReadFile(f.path("secret.key"))
	if err != nil || string(got) != "new" {
		t.Errorf("secret.key = %q, %v; want %q", got, err, "new")
	}
	info, err := os.Stat(f.path("secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	noTempLeft(t, f.root)
}

// An empty stream with no declared size is a valid empty file, replacing an
// existing one or creating a new one.
func TestWriteEmptyFileWithoutExpect(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "existing.txt", "content")

	for _, name := range []string{"existing.txt", "new.txt"} {
		if err := f.Write(name, strings.NewReader(""), -1); err != nil {
			t.Fatalf("Write(%q) error = %v", name, err)
		}
		info, err := os.Stat(f.path(name))
		if err != nil || info.Size() != 0 {
			t.Errorf("%s after an empty write = %v, %v; want an empty file", name, info, err)
		}
	}
	noTempLeft(t, f.root)
}

// A symlink target keeps the truncate-and-stream path: the content lands in
// the file the link points at and the link itself stays a link.
func TestWriteThroughSymlinkFollowsLink(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "real.txt", "old")
	if err := os.Symlink("real.txt", f.path("link.txt")); err != nil {
		t.Fatal(err)
	}

	if err := f.Write("link.txt", strings.NewReader("through the link"), -1); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := os.ReadFile(f.path("real.txt"))
	if err != nil || string(got) != "through the link" {
		t.Errorf("real.txt = %q, %v; want the new content", got, err)
	}
	info, err := os.Lstat(f.path("link.txt"))
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("link.txt = %v, %v; want it to stay a symlink", info, err)
	}
	noTempLeft(t, f.root)
}

// FreeBytes answers with the root's own filesystem: a real temp directory has
// room, and a zero would mean the helper had nothing to compare an upload
// against.
func TestFreeBytesReportsTheRootsFilesystem(t *testing.T) {
	f := newFixture(t)

	free, err := f.FreeBytes()
	if err != nil {
		t.Fatalf("FreeBytes() error = %v", err)
	}
	if free == 0 {
		t.Error("FreeBytes() = 0, want the temp directory's free space")
	}
}

// Delete removes a file and a whole tree, removes a symlink without touching
// its target, refuses the root itself under every spelling, and treats an
// already-absent path as the no-op RemoveAll is. A `..` segment never reaches
// RemoveAll: os.Root.RemoveAll("sub/..") empties the root before it reports
// `invalid argument` (Go 1.27.1), so the guard is the load-bearing check for
// that one shape.
func TestDeleteRemovesFilesSymlinksAndTrees(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "file.txt", "x")
	f.plant(t, "tree/a.txt", "a")
	f.plant(t, "tree/deep/b.txt", "b")
	f.plant(t, "guard/inside.txt", "guard")
	f.plant(t, "keep.txt", "keep")
	f.link(t, "keep.txt", "link-keep")

	if err := f.Delete("file.txt"); err != nil {
		t.Fatalf("Delete(file) error = %v", err)
	}
	f.assertGone(t, "file.txt")

	if err := f.Delete("tree"); err != nil {
		t.Fatalf("Delete(tree) error = %v", err)
	}
	f.assertGone(t, "tree")
	f.assertGone(t, "tree/deep/b.txt")
	if _, err := f.Stat("keep.txt"); err != nil {
		t.Errorf("Delete(tree) touched a sibling: %v", err)
	}

	if err := f.Delete("link-keep"); err != nil {
		t.Fatalf("Delete(link) error = %v", err)
	}
	f.assertGone(t, "link-keep")
	if _, err := f.Stat("keep.txt"); err != nil {
		t.Errorf("Delete(link) removed the target: %v", err)
	}

	if err := f.Delete("."); err == nil {
		t.Error("Delete(\".\") = nil, want the root to survive")
	}
	if _, err := f.Stat("keep.txt"); err != nil {
		t.Errorf("Delete(\".\") damaged the root: %v", err)
	}

	// The disguised root: `guard/..` resolves to the root, and RemoveAll would
	// empty it. Every spelling is refused, and nothing under it moves.
	for _, name := range []string{"guard/..", "guard/../..", "./.", "../secret.txt"} {
		if err := f.Delete(name); err == nil {
			t.Errorf("Delete(%q) = nil, want the root to survive", name)
		}
	}
	for _, kept := range []string{"guard/inside.txt", "keep.txt"} {
		if _, err := f.Stat(kept); err != nil {
			t.Errorf("Delete of a disguised root damaged %s: %v", kept, err)
		}
	}

	if err := f.Delete("never-existed"); err != nil {
		t.Errorf("Delete(missing) error = %v, want nil", err)
	}
}

func TestOpenRefusesAMissingDirectory(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("Open(missing) = nil error, want a failure")
	}
}

// Mkdir creates exactly one directory and refuses a name already taken: a
// folder may never adopt or merge an existing entry, so an existing name is
// fs.ErrExist rather than a silent success.
func TestMkdirCreatesOneDirectoryAndRefusesAnExistingName(t *testing.T) {
	f := newFixture(t)

	// One level only: the parent must exist, exactly the Explorer's rule that
	// the dialog creates into the directory it is showing.
	if err := f.Mkdir("uploads/incoming", 0o755); err == nil {
		t.Error("Mkdir(nested new parent) = nil, want a failure")
	}

	if err := f.Mkdir("uploads", 0o755); err != nil {
		t.Fatalf("Mkdir(uploads) error = %v", err)
	}
	entry, err := f.Stat("uploads")
	if err != nil {
		t.Fatalf("Stat(uploads) error = %v", err)
	}
	if entry.Kind != KindDir || entry.Mode.Perm()&0o700 != 0o700 {
		t.Errorf("Stat(uploads) = %+v, want an owner-writable directory", entry)
	}

	if err := f.Mkdir("uploads", 0o755); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mkdir(existing) error = %v, want fs.ErrExist", err)
	}
	if err := f.Mkdir("../escape", 0o755); err == nil {
		t.Error("Mkdir(../escape) = nil, want the root's escape refusal")
	}
}

// Create creates exactly one empty file and never truncates: a name already
// taken fails with fs.ErrExist and the existing content survives untouched.
// That O_EXCL is the whole reason New file is not the write endpoint.
func TestCreateCreatesAnEmptyFileAndNeverTruncates(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "notes.txt", "keep me")

	if err := f.Create("fresh.txt", 0o644); err != nil {
		t.Fatalf("Create(fresh.txt) error = %v", err)
	}
	entry, err := f.Stat("fresh.txt")
	if err != nil {
		t.Fatalf("Stat(fresh.txt) error = %v", err)
	}
	if entry.Kind != KindFile || entry.Size != 0 {
		t.Errorf("Stat(fresh.txt) = %+v, want an empty file", entry)
	}

	if err := f.Create("notes.txt", 0o644); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Create(existing) error = %v, want fs.ErrExist", err)
	}
	content, err := os.ReadFile(f.path("notes.txt"))
	if err != nil {
		t.Fatalf("read notes.txt: %v", err)
	}
	if string(content) != "keep me" {
		t.Errorf("notes.txt = %q, want the original content intact", content)
	}
}

// fakeInfo is a FileInfo with just the mode and owner replaceAtomically reads.
type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
	sys  any
}

func (i fakeInfo) Mode() fs.FileMode { return i.mode }
func (i fakeInfo) Sys() any          { return i.sys }

// Only a missing target, or a regular file the operation user already owns, is
// replaced through a temp: a symlink keeps the in-place path, and another
// owner's file stays in place because the rename would hand it to this user
// and an unprivileged helper cannot chown it back.
func TestReplaceAtomically(t *testing.T) {
	owned := func(uid uint32) any { return &syscall.Stat_t{Uid: uid} }
	tests := []struct {
		name string
		info fs.FileInfo
		want bool
	}{
		{"missing target", nil, true},
		{"regular file owned by the euid", fakeInfo{mode: 0o644, sys: owned(1000)}, true},
		{"regular file owned by another uid", fakeInfo{mode: 0o644, sys: owned(999)}, false},
		{"symlink owned by the euid", fakeInfo{mode: fs.ModeSymlink | 0o777, sys: owned(1000)}, false},
		{"directory", fakeInfo{mode: fs.ModeDir | 0o755, sys: owned(1000)}, false},
		{"no owner information", fakeInfo{mode: 0o644}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replaceAtomically(tt.info, 1000); got != tt.want {
				t.Errorf("replaceAtomically() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A file the operation user may write inside a directory it may not write
// cannot be replaced through a temp (the rename needs the directory), so
// Write falls back to writing in place, as it always did.
func TestWriteFallsBackInPlaceWhenTheDirectoryIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newFixture(t)
	f.plant(t, "locked/app.conf", "old content")
	if err := os.Chmod(f.path("locked/app.conf"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.path("locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(f.path("locked"), 0o755) })

	if err := f.Write("locked/app.conf", strings.NewReader("new"), 3); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := os.ReadFile(f.path("locked/app.conf"))
	if err != nil || string(got) != "new" {
		t.Errorf("app.conf = %q, %v; want %q", got, err, "new")
	}
	noTempLeft(t, f.path("locked"))
}
