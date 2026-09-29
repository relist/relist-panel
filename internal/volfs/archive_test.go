package volfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// archivedEntry is one entry read back out of a produced archive: the header
// exactly as it was written, plus its body.
type archivedEntry struct {
	Header tar.Header
	Body   []byte
}

// readArchive decompresses and untars a produced archive. It returns errors
// rather than failing a test, so it is also usable from another goroutine.
func readArchive(r io.Reader) (map[string]archivedEntry, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	entries := map[string]archivedEntry{}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", hdr.Name, err)
		}
		if _, dup := entries[hdr.Name]; dup {
			return nil, fmt.Errorf("entry %q appears twice", hdr.Name)
		}
		entries[hdr.Name] = archivedEntry{Header: *hdr, Body: body}
	}
}

// archiveEntries produces an archive into a buffer and reads it back.
func archiveEntries(t *testing.T, f fixture, name string) map[string]archivedEntry {
	t.Helper()
	var buf bytes.Buffer
	if err := f.Archive(name, &buf); err != nil {
		t.Fatalf("Archive(%q) error = %v", name, err)
	}
	entries, err := readArchive(&buf)
	if err != nil {
		t.Fatalf("read back Archive(%q): %v", name, err)
	}
	return entries
}

// streamArchive runs Archive over an io.Pipe — a writer that cannot seek, the
// shape of the HTTP response body the helper streams into — and reads it back
// from the other end.
func streamArchive(t *testing.T, f fixture, name string) map[string]archivedEntry {
	t.Helper()
	pr, pw := io.Pipe()
	var (
		entries map[string]archivedEntry
		readErr error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		entries, readErr = readArchive(pr)
		if readErr != nil {
			pr.CloseWithError(readErr)
		}
	}()

	archiveErr := f.Archive(name, pw)
	pw.Close() // whatever happened, unblock the reader
	<-done

	if archiveErr != nil {
		t.Fatalf("Archive(%q) error = %v", name, archiveErr)
	}
	if readErr != nil {
		t.Fatalf("read back Archive(%q): %v", name, readErr)
	}
	return entries
}

// assertRelativeName enforces the spec's rule on an entry name: relative to
// the Volume root, slash-separated, never absolute and never a `..` segment.
// A leading slash or a climb is how a download becomes a write outside the
// destination on whoever extracts it.
func assertRelativeName(t *testing.T, name string) {
	t.Helper()
	if name == "" || strings.HasPrefix(name, "/") {
		t.Errorf("entry name %q is not relative", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			t.Errorf("entry name %q contains a .. segment", name)
		}
	}
}

// A nested tree streamed through an io.Pipe comes back out of archive/tar and
// compress/gzip with every entry named relative to the Volume root, the three
// required files byte-identical by sha256, the newline-bearing name intact,
// and the empty directory present.
func TestArchiveRoundTripsANestedTree(t *testing.T) {
	f := newFixture(t)
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("big.bin"), payload, 0o644); err != nil {
		t.Fatalf("write big.bin: %v", err)
	}
	f.plant(t, "notes/deep/nested.bin", "nested bytes")
	f.plant(t, "notes/readme.txt", "hello, volume\n")
	f.plant(t, "a\nb.txt", "newline name")
	if err := os.Mkdir(f.path("empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}

	entries := streamArchive(t, f, ".")

	want := []string{
		"a\nb.txt",
		"big.bin",
		"empty/",
		"notes/",
		"notes/deep/",
		"notes/deep/nested.bin",
		"notes/readme.txt",
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		assertRelativeName(t, name)
		names = append(names, name)
	}
	slices.Sort(names)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entry names = %q, want %q", names, want)
	}

	for _, tc := range []struct{ entry, file string }{
		{"big.bin", "big.bin"},
		{"notes/deep/nested.bin", "notes/deep/nested.bin"},
		{"notes/readme.txt", "notes/readme.txt"},
	} {
		onDisk, err := os.ReadFile(f.path(tc.file))
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if got, want := sha256.Sum256(entries[tc.entry].Body), sha256.Sum256(onDisk); got != want {
			t.Errorf("%s sha256 = %x, want %x", tc.entry, got, want)
		}
	}

	if got := string(entries["a\nb.txt"].Body); got != "newline name" {
		t.Errorf("newline entry body = %q, want %q", got, "newline name")
	}
	if e := entries["empty/"]; e.Header.Typeflag != tar.TypeDir || len(e.Body) != 0 {
		t.Errorf("empty/ = %+v, want an empty directory entry", e.Header)
	}
	if e := entries["big.bin"]; e.Header.Typeflag != tar.TypeReg || e.Header.Size != int64(len(payload)) {
		t.Errorf("big.bin header = %+v, want a regular file of %d bytes", e.Header, len(payload))
	}
}

// Symlinks are recorded as symlinks with their targets — inside the root and
// pointing outside it alike — and a link to a directory is never followed into
// the archive: the archive records what the Volume holds, not what the links
// resolve to. A symlink archived on its own is a one-entry archive.
func TestArchiveStoresSymlinksAsSymlinks(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "inside/real.txt", "real")
	f.plant(t, "dir-inside/nested.txt", "nested")
	if err := os.WriteFile(filepath.Join(f.out, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.link(t, "inside/real.txt", "link-inside")
	f.link(t, "../secret.txt", "link-rel-outside")
	f.link(t, "/etc/passwd", "link-etc")
	f.link(t, "dir-inside", "link-dir")

	entries := archiveEntries(t, f, ".")
	for name := range entries {
		assertRelativeName(t, name)
	}
	for name, target := range map[string]string{
		"link-inside":      "inside/real.txt",
		"link-rel-outside": "../secret.txt",
		"link-etc":         "/etc/passwd",
		"link-dir":         "dir-inside",
	} {
		e, ok := entries[name]
		if !ok {
			t.Errorf("entry %q missing from the archive", name)
			continue
		}
		if e.Header.Typeflag != tar.TypeSymlink {
			t.Errorf("%s Typeflag = %d, want TypeSymlink", name, e.Header.Typeflag)
		}
		if e.Header.Linkname != target {
			t.Errorf("%s Linkname = %q, want %q", name, e.Header.Linkname, target)
		}
		if e.Header.Size != 0 {
			t.Errorf("%s Size = %d, want 0 for a symlink", name, e.Header.Size)
		}
	}

	// The dir link is stored as a link, never walked: no entry lives beneath
	// it, or it would be duplicated as real paths.
	for name := range entries {
		if strings.HasPrefix(name, "link-dir/") {
			t.Errorf("archive followed link-dir: entry %q", name)
		}
	}

	single := archiveEntries(t, f, "link-inside")
	if len(single) != 1 {
		t.Fatalf("Archive(link-inside) has %d entries, want 1", len(single))
	}
	if e := single["link-inside"]; e.Header.Typeflag != tar.TypeSymlink || e.Header.Linkname != "inside/real.txt" {
		t.Errorf("Archive(link-inside) entry = %+v, want a symlink -> inside/real.txt", e.Header)
	}
}

// A hardlink is an ordinary regular file on Unix, so it arrives as a regular
// file with the right bytes — never as a hardlink entry, which is how the
// hardlink tar-slip vector is sidestepped.
func TestArchiveWritesHardlinksAsRegularFiles(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "notes/readme.txt", "hello, volume\n")
	if err := os.Link(f.path("notes/readme.txt"), f.path("hard.txt")); err != nil {
		t.Fatalf("os.Link: %v", err)
	}

	entries := archiveEntries(t, f, ".")
	e, ok := entries["hard.txt"]
	if !ok {
		t.Fatal("hard.txt missing from the archive")
	}
	if e.Header.Typeflag != tar.TypeReg {
		t.Errorf("hard.txt Typeflag = %d, want TypeReg", e.Header.Typeflag)
	}
	if e.Header.Linkname != "" {
		t.Errorf("hard.txt Linkname = %q, want empty — never a hardlink entry", e.Header.Linkname)
	}
	if got, want := string(e.Body), "hello, volume\n"; got != want {
		t.Errorf("hard.txt body = %q, want %q", got, want)
	}
	if want := entries["notes/readme.txt"].Header.Size; e.Header.Size != want {
		t.Errorf("hard.txt Size = %d, want its target's %d", e.Header.Size, want)
	}
}

// A file archived on its own is a one-entry archive named as that path — the
// path relative to the Volume root, never a base name — and a newline in that
// name survives the header.
func TestArchiveOfOneFileNamesItAsThatPath(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "notes/readme.txt", "hello, volume\n")
	f.plant(t, "a\nb.txt", "newline name")
	f.plant(t, "elsewhere.txt", "not wanted")

	for _, name := range []string{"notes/readme.txt", "a\nb.txt"} {
		entries := archiveEntries(t, f, name)
		if len(entries) != 1 {
			t.Fatalf("Archive(%q) has %d entries, want 1", name, len(entries))
		}
		e, ok := entries[name]
		if !ok {
			t.Fatalf("Archive(%q) entries = %v, want the one entry named as the path", name, entries)
		}
		if e.Header.Typeflag != tar.TypeReg {
			t.Errorf("Archive(%q) Typeflag = %d, want TypeReg", name, e.Header.Typeflag)
		}
	}
}

// A named directory gets its own entry, so an empty directory — the shape a
// round trip otherwise loses — survives.
func TestArchiveStoresAnEmptyDirectoryAsAnEntry(t *testing.T) {
	f := newFixture(t)
	if err := os.Mkdir(f.path("empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries := archiveEntries(t, f, "empty")
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want only empty/", entries)
	}
	e, ok := entries["empty/"]
	if !ok {
		t.Fatalf("entry empty/ missing; got %v", entries)
	}
	if e.Header.Typeflag != tar.TypeDir {
		t.Errorf("empty/ Typeflag = %d, want TypeDir", e.Header.Typeflag)
	}

	// The root itself is the extraction target, so an archive of it carries
	// no invented "." entry; a child directory is the entry that matters.
	if nested := archiveEntries(t, f, "."); len(nested) != 1 {
		t.Errorf("Archive(\".\") of a one-directory root = %v, want only empty/", nested)
	}
}

// A caller's path that walks and re-enters the root still yields names
// relative to it, and a path that escapes or does not exist is refused before
// a single byte reaches dst — so an HTTP handler can still send a real status.
func TestArchiveRefusesEscapingAndMissingPathsBeforeWriting(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "notes/readme.txt", "hello")

	entries := archiveEntries(t, f, "./notes/../notes")
	names := make([]string, 0, len(entries))
	for name := range entries {
		assertRelativeName(t, name)
		names = append(names, name)
	}
	slices.Sort(names)
	if want := []string{"notes/", "notes/readme.txt"}; !reflect.DeepEqual(names, want) {
		t.Errorf("Archive(./notes/../notes) names = %q, want %q", names, want)
	}

	for _, name := range []string{"..", "/etc/passwd", "never-existed"} {
		var buf bytes.Buffer
		if err := f.Archive(name, &buf); err == nil {
			t.Errorf("Archive(%q) = nil error, want a refusal", name)
		}
		if buf.Len() != 0 {
			t.Errorf("Archive(%q) wrote %d bytes before refusing, want none", name, buf.Len())
		}
	}
}

// A destination that fails — a cancelled HTTP response — aborts the walk and
// returns the failure, rather than spinning through the rest of the tree.
func TestArchiveAbortsWhenTheDestinationDies(t *testing.T) {
	f := newFixture(t)
	for i := range 32 {
		f.plant(t, fmt.Sprintf("dir/file-%02d.txt", i), strings.Repeat("x", 4096))
	}

	pr, pw := io.Pipe()
	pr.Close() // the client went away; every write now fails

	if err := f.Archive(".", pw); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Archive() error = %v, want io.ErrClosedPipe", err)
	}
}

// A FIFO cannot be opened for reading without blocking until a writer appears,
// so the walk skips KindOther instead of streaming it — and the rest of the
// tree still archives.
func TestArchiveSkipsOtherKinds(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "real.txt", "real")
	if err := syscall.Mkfifo(f.path("pipe"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	entries := archiveEntries(t, f, ".")
	if _, ok := entries["pipe"]; ok {
		t.Error("the archive contains the FIFO; want it skipped, never opened")
	}
	if got := string(entries["real.txt"].Body); got != "real" {
		t.Errorf("real.txt = %q, want %q", got, "real")
	}
}
