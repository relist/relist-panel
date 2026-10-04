package volfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tarEntry is one hand-built archive entry: the header as planted, with its
// body (nil for entries that carry none).
type tarEntry struct {
	Header tar.Header
	Body   []byte
}

// extractArchive builds a tar.gz from entries exactly as given — hand-built,
// bypassing Archive, so shapes the writer never produces can be tested.
func extractArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		hdr := e.Header
		if e.Body != nil {
			hdr.Size = int64(len(e.Body))
		}
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("write header %q: %v", hdr.Name, err)
		}
		if len(e.Body) > 0 {
			if _, err := tw.Write(e.Body); err != nil {
				t.Fatalf("write body %q: %v", hdr.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// tree lists every path under dir, relative and slash-separated, sorted.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel != "." {
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	slices.Sort(paths)
	return paths
}

// treeContents maps every path under root to its content — "dir" for a
// directory, the bytes for a file — so two extracted trees can be compared
// without dumping megabytes into a failure message.
func treeContents(t *testing.T, root string) map[string]string {
	t.Helper()
	contents := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			contents[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		contents[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return contents
}

// assertSameTree compares two roots file by file: every path in one is in the
// other with the same kind and the same bytes, and neither has extras.
func assertSameTree(t *testing.T, wantRoot, gotRoot string) {
	t.Helper()
	want := treeContents(t, wantRoot)
	got := treeContents(t, gotRoot)
	for name, content := range want {
		gotContent, ok := got[name]
		if !ok {
			t.Errorf("%s missing from the extracted tree", name)
			continue
		}
		if gotContent != content {
			t.Errorf("%s differs (%d bytes vs %d)", name, len(gotContent), len(content))
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s was extracted but is not in the source tree", name)
		}
	}
}

// A nested tree written by Archive and extracted into a second root comes out
// byte-identical — the 1 MB binary, the nested file and the newline-bearing
// name alike — with the empty directory and the header modes intact.
func TestExtractRoundTripsAnArchivedTree(t *testing.T) {
	src := newFixture(t)
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src.path("big.bin"), payload, 0o644); err != nil {
		t.Fatalf("write big.bin: %v", err)
	}
	src.plant(t, "notes/deep/nested.bin", "nested bytes")
	src.plant(t, "notes/readme.txt", "hello, volume\n")
	src.plant(t, "a\nb.txt", "newline name")
	if err := os.Mkdir(src.path("empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	for rel, mode := range map[string]fs.FileMode{"notes": 0o750, "notes/readme.txt": 0o640} {
		if err := os.Chmod(src.path(rel), mode); err != nil {
			t.Fatalf("chmod %s: %v", rel, err)
		}
	}

	var buf bytes.Buffer
	if err := src.Archive(".", &buf); err != nil {
		t.Fatalf("Archive(\".\") error = %v", err)
	}

	dst := newFixture(t)
	if err := dst.Extract(".", &buf); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	assertSameTree(t, src.root, dst.root)
	for rel, want := range map[string]fs.FileMode{
		"notes":            0o750,
		"notes/readme.txt": 0o640,
		"big.bin":          0o644,
		"empty":            0o755,
	} {
		info, err := os.Stat(dst.path(rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", rel, got, want)
		}
	}
}

// An existing file is overwritten — truncated, not appended to — and an
// existing directory merges: its other files and everything outside the
// target stay exactly as they were.
func TestExtractOverwritesFilesAndMergesDirectories(t *testing.T) {
	src := newFixture(t)
	src.plant(t, "notes/readme.txt", "new")
	src.plant(t, "notes/added.txt", "added")

	dst := newFixture(t)
	dst.plant(t, "notes/readme.txt", "the longer old content")
	dst.plant(t, "notes/kept.txt", "kept")
	dst.plant(t, "outside.txt", "untouched")

	var buf bytes.Buffer
	if err := src.Archive("notes", &buf); err != nil {
		t.Fatalf("Archive(\"notes\") error = %v", err)
	}
	if err := dst.Extract(".", &buf); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	for rel, want := range map[string]string{
		"notes/readme.txt": "new",
		"notes/added.txt":  "added",
		"notes/kept.txt":   "kept",
		"outside.txt":      "untouched",
	} {
		got, err := os.ReadFile(dst.path(rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// Every name that is absolute, climbs out, carries an empty or `..` segment,
// or is dressed as a Windows drive or UNC path refuses the stream before it
// reaches the filesystem, and the Volume is byte-for-byte unchanged.
func TestExtractRefusesUnsafeEntryNames(t *testing.T) {
	for _, name := range []string{
		"../escape",
		"../../escape",
		"a/../../escape",
		"a/..",
		"..",
		"//etc/passwd",
		"/etc/passwd",
		"a//b",
		"",
		`..\..\escape`,
		`foo\..\..\escape`,
		`C:\escape`,
		`C:escape`,
		`\\host\share\escape`,
		`\rooted`,
	} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			f := newFixture(t)
			before := tree(t, f.out)

			archive := extractArchive(t, tarEntry{
				Header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644},
				Body:   []byte("evil"),
			})

			err := f.Extract(".", bytes.NewReader(archive))
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("Extract(%q) error = %v, want ErrUnsafeArchive", name, err)
			}
			if got := tree(t, f.out); !reflect.DeepEqual(got, before) {
				t.Errorf("refused entry %q changed the tree: %q, want %q", name, got, before)
			}
		})
	}
}

// A symlink, a hardlink, a FIFO, a character device and a block device are
// each refused outright, and none of them leaves an entry behind.
func TestExtractRefusesSymlinksHardlinksAndOtherTypes(t *testing.T) {
	for _, tc := range []struct {
		what string
		hdr  tar.Header
	}{
		{"symlink", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../outside", Mode: 0o777}},
		{"hardlink", tar.Header{Name: "hard", Typeflag: tar.TypeLink, Linkname: "real.txt"}},
		{"fifo", tar.Header{Name: "pipe", Typeflag: tar.TypeFifo, Mode: 0o644}},
		{"character device", tar.Header{Name: "zero", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 5}},
		{"block device", tar.Header{Name: "disk", Typeflag: tar.TypeBlock, Mode: 0o660, Devmajor: 8, Devminor: 0}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newFixture(t)
			before := tree(t, f.out)

			archive := extractArchive(t, tarEntry{Header: tc.hdr})

			err := f.Extract(".", bytes.NewReader(archive))
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("Extract() error = %v, want ErrUnsafeArchive", err)
			}
			if got := tree(t, f.out); !reflect.DeepEqual(got, before) {
				t.Errorf("refused %s changed the tree: %q, want %q", tc.what, got, before)
			}
		})
	}
}

// The entry cap counts directories too, and the entries before the one that
// trips it stay written.
func TestExtractEnforcesTheEntryCap(t *testing.T) {
	f := newFixture(t)
	archive := extractArchive(t,
		tarEntry{Header: tar.Header{Name: "one.txt", Typeflag: tar.TypeReg, Mode: 0o644}, Body: []byte("one")},
		tarEntry{Header: tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
		tarEntry{Header: tar.Header{Name: "sub/two.txt", Typeflag: tar.TypeReg, Mode: 0o644}, Body: []byte("two")},
		tarEntry{Header: tar.Header{Name: "three.txt", Typeflag: tar.TypeReg, Mode: 0o644}, Body: []byte("three")},
	)

	err := f.Extract(".", bytes.NewReader(archive), ExtractLimits{MaxEntries: 3, MaxBytes: 1 << 20})
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Extract with a 3-entry cap error = %v, want ErrArchiveTooLarge", err)
	}

	for rel, want := range map[string]string{"one.txt": "one", "sub/two.txt": "two"} {
		got, err := os.ReadFile(f.path(rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(f.path("three.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the entry past the cap was written: Lstat error = %v", err)
	}
}

// The uncompressed cap counts bytes actually written: the file that trips it
// is cut at the cap and left as the partial extraction it disclosed, while a
// body that ends exactly at the cap is not an overflow.
func TestExtractEnforcesTheUncompressedByteCap(t *testing.T) {
	f := newFixture(t)
	content := strings.Repeat("x", 100)
	archive := extractArchive(t, tarEntry{
		Header: tar.Header{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644},
		Body:   []byte(content),
	})

	err := f.Extract(".", bytes.NewReader(archive), ExtractLimits{MaxEntries: 10, MaxBytes: 10})
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Extract with a 10-byte cap error = %v, want ErrArchiveTooLarge", err)
	}
	partial, err := os.ReadFile(f.path("big.bin"))
	if err != nil {
		t.Fatalf("read the partial file: %v", err)
	}
	if len(partial) != 10 {
		t.Errorf("partial file = %d bytes, want the 10 the cap allowed", len(partial))
	}

	if err := f.Extract(".", bytes.NewReader(archive), ExtractLimits{MaxEntries: 10, MaxBytes: int64(len(content))}); err != nil {
		t.Fatalf("Extract at exactly the cap error = %v, want nil", err)
	}
	got, err := os.ReadFile(f.path("big.bin"))
	if err != nil || string(got) != content {
		t.Errorf("exact-fit extract = %q, %v; want the whole body", got, err)
	}
}

// truncatedArchive is one entry whose header declares 1 GiB but carries 13
// bytes: the stream ends mid-entry. The cap is counted from bytes actually
// written, so this is a malformed archive — classified ErrInvalidArchive —
// not a size refusal based on a header's claim.
func truncatedArchive(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	hdr := tar.Header{Name: "huge.bin", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1 << 30}
	if err := tw.WriteHeader(&hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("partial bytes")); err != nil {
		t.Fatalf("write partial body: %v", err)
	}
	// Deliberately no tw.Close(): the declared size was never written, so the
	// tar stream ends mid-entry.

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip the truncated tar: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// A declared size larger than the cap, with a stream that ends before the
// declaration, is a malformed archive — ErrInvalidArchive — and the bytes
// that did arrive stay written.
func TestExtractClassifiesATruncatedEntryAsInvalidArchive(t *testing.T) {
	f := newFixture(t)

	err := f.Extract(".", bytes.NewReader(truncatedArchive(t)), ExtractLimits{MaxEntries: 10, MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Extract() = nil, want the truncated entry to fail")
	}
	if !errors.Is(err, ErrInvalidArchive) {
		t.Errorf("truncated entry error = %v, want ErrInvalidArchive", err)
	}
	if errors.Is(err, ErrArchiveTooLarge) || errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("truncated entry error = %v, want no other refusal class", err)
	}

	got, readErr := os.ReadFile(f.path("huge.bin"))
	if readErr != nil {
		t.Fatalf("read the partial file: %v", readErr)
	}
	if string(got) != "partial bytes" {
		t.Errorf("partial file = %q, want %q", got, "partial bytes")
	}
}

// The target must already be a directory — missing, nested under a missing
// parent, a file, `..` or an absolute path all fail — and a missing one is
// checked before the stream is read, never created.
func TestExtractNeedsAnExistingDirectoryTarget(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "file.txt", "x")
	archive := extractArchive(t, tarEntry{
		Header: tar.Header{Name: "new.txt", Typeflag: tar.TypeReg, Mode: 0o644},
		Body:   []byte("new"),
	})

	for _, target := range []string{"missing", "a/b", "file.txt", "..", "/etc"} {
		t.Run(target, func(t *testing.T) {
			err := f.Extract(target, bytes.NewReader(archive))
			if err == nil {
				t.Fatalf("Extract(%q) = nil, want an error", target)
			}
			if errors.Is(err, ErrUnsafeArchive) || errors.Is(err, ErrArchiveTooLarge) || errors.Is(err, ErrInvalidArchive) {
				t.Errorf("Extract(%q) error = %v, want an ordinary failure", target, err)
			}
		})
	}

	if _, err := os.Lstat(f.path("missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the missing target was created: Lstat error = %v", err)
	}
	if _, err := os.Lstat(f.path("a")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the missing target's parent was created: Lstat error = %v", err)
	}

	err := f.Extract("missing", errorReader{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Extract(missing, failing reader) error = %v, want fs.ErrNotExist", err)
	}
	if errors.Is(err, ErrInvalidArchive) {
		t.Errorf("Extract(missing, failing reader) error = %v, want the target checked before the stream", err)
	}
}

func TestExtractValidatesTheGzipTrailerAfterTheTarEnds(t *testing.T) {
	valid := extractArchive(t, tarEntry{
		Header: tar.Header{Name: "saved.txt", Mode: 0o644, Typeflag: tar.TypeReg},
		Body:   []byte("saved"),
	})
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-8] ^= 1 // CRC32 lives in the first four trailer bytes.
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"corrupt checksum", corrupt},
		{"truncated trailer", valid[:len(valid)-4]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := newFixture(t)
			err := dst.Extract(".", bytes.NewReader(tc.body))
			if !errors.Is(err, ErrInvalidArchive) {
				t.Fatalf("Extract = %v, want ErrInvalidArchive", err)
			}
			got, readErr := os.ReadFile(dst.path("saved.txt"))
			if readErr != nil || string(got) != "saved" {
				t.Fatalf("partial extraction = %q, want saved", got)
			}
		})
	}
}

func TestExtractCapsUncompressedBytesAfterTheTarEnds(t *testing.T) {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	tw := tar.NewWriter(zw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	// A valid tar end followed by compressible junk must not allow an
	// unbounded drain merely to validate gzip's trailer.
	if _, err := zw.Write(bytes.Repeat([]byte("x"), 4096)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dst := newFixture(t)
	err := dst.Extract(".", bytes.NewReader(body.Bytes()), ExtractLimits{MaxEntries: 10, MaxBytes: 1024})
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Extract = %v, want ErrArchiveTooLarge", err)
	}
	if got := tree(t, dst.root); len(got) != 0 {
		t.Fatalf("trailing bytes created files: %v", got)
	}
}

// An empty stream, plain garbage and a gzip stream that is not a tar are all
// classified ErrInvalidArchive — the body is not a usable tar.gz — rather
// than as an unsafe entry or a size refusal, and none of them writes anything.
func TestExtractRefusesMalformedStreams(t *testing.T) {
	for _, tc := range []struct {
		what   string
		stream func(t *testing.T) []byte
	}{
		{"empty", func(*testing.T) []byte { return nil }},
		{"garbage", func(*testing.T) []byte { return []byte("this is not a tar.gz") }},
		{"gzipped but not a tar", func(t *testing.T) []byte {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			if _, err := zw.Write(bytes.Repeat([]byte("x"), 600)); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newFixture(t)
			before := tree(t, f.out)

			err := f.Extract(".", bytes.NewReader(tc.stream(t)))
			if err == nil {
				t.Fatal("Extract() = nil, want a malformed stream to fail")
			}
			if !errors.Is(err, ErrInvalidArchive) {
				t.Errorf("Extract() error = %v, want ErrInvalidArchive", err)
			}
			if errors.Is(err, ErrUnsafeArchive) || errors.Is(err, ErrArchiveTooLarge) {
				t.Errorf("Extract() error = %v, want no other refusal class", err)
			}
			if got := tree(t, f.out); !reflect.DeepEqual(got, before) {
				t.Errorf("a malformed stream changed the tree: %q, want %q", got, before)
			}
		})
	}
}

// A refusal aborts the rest of the stream but is not a rollback: the entries
// already written stay, and the refused entry writes nothing — not under the
// target, and not at the root its climb aimed for.
func TestExtractLeavesEarlierEntriesWhenALaterEntryIsRefused(t *testing.T) {
	f := newFixture(t)
	if err := os.Mkdir(f.path("notes"), 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	archive := extractArchive(t,
		tarEntry{Header: tar.Header{Name: "kept.txt", Typeflag: tar.TypeReg, Mode: 0o644}, Body: []byte("kept")},
		tarEntry{Header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644}, Body: []byte("escape")},
	)

	err := f.Extract("notes", bytes.NewReader(archive))
	if !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("Extract() error = %v, want ErrUnsafeArchive", err)
	}

	got, err := os.ReadFile(f.path("notes/kept.txt"))
	if err != nil || string(got) != "kept" {
		t.Errorf("kept.txt = %q, %v; want the earlier entry left in place", got, err)
	}
	for _, escaped := range []string{
		f.path("escape"),
		filepath.Join(f.out, "escape"),
	} {
		if _, err := os.Lstat(escaped); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the refused entry wrote %s: Lstat error = %v", escaped, err)
		}
	}
}

// A directory whose archived mode forbids the owner to write is still created
// writable while its children stream in, then takes the header's exact mode:
// restoring a read-only tree as its owner works.
func TestExtractWritesIntoAnArchivedReadOnlyDirectory(t *testing.T) {
	src := newFixture(t)
	src.plant(t, "ro/inner.txt", "inner")
	if err := os.Chmod(src.path("ro"), 0o500); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}

	var buf bytes.Buffer
	if err := src.Archive("ro", &buf); err != nil {
		t.Fatalf("Archive(\"ro\") error = %v", err)
	}

	dst := newFixture(t)
	if err := dst.Extract(".", &buf); err != nil {
		t.Fatalf("Extract() into a read-only directory error = %v", err)
	}
	got, err := os.ReadFile(dst.path("ro/inner.txt"))
	if err != nil || string(got) != "inner" {
		t.Errorf("ro/inner.txt = %q, %v; want the entry written", got, err)
	}
	info, err := os.Stat(dst.path("ro"))
	if err != nil {
		t.Fatalf("stat ro: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o500 {
		t.Errorf("ro mode = %o, want the header's 0500", got)
	}

	// The temp roots must be writable again before cleanup can remove them.
	t.Cleanup(func() {
		_ = os.Chmod(src.path("ro"), 0o700)
		_ = os.Chmod(dst.path("ro"), 0o700)
	})
}

// The default caps are the spec's restore caps, and passing more than one
// ExtractLimits is a caller bug rather than something to guess at.
func TestDefaultExtractLimitsAreTheSpecsRestoreCaps(t *testing.T) {
	got := DefaultExtractLimits()
	want := ExtractLimits{MaxEntries: 10_000, MaxBytes: 4 << 30}
	if got != want {
		t.Errorf("DefaultExtractLimits() = %+v, want %+v", got, want)
	}

	f := newFixture(t)
	if err := f.Extract(".", bytes.NewReader(nil), ExtractLimits{}, ExtractLimits{}); err == nil {
		t.Error("Extract with two ExtractLimits = nil, want an error")
	}
}
