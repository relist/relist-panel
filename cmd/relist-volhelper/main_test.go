package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/volfs"
)

// runHelper drives one helper invocation through run — the exact seam main
// calls — with stdin and both streams captured, so every test exercises the
// real protocol without a process or a container.
func runHelper(t *testing.T, root string, stdin []byte, args ...string) (code int, stdout []byte, stderr string) {
	t.Helper()
	return runHelperWith(t, root, bytes.NewReader(stdin), (*volfs.FS).FreeBytes, args...)
}

// runHelperWith is runHelper with the free-space lookup injected: a test can
// pin a tiny filesystem without filling the real one, and stdin can be any
// reader — including one that fails the test if the helper reads it.
func runHelperWith(t *testing.T, root string, stdin io.Reader, freeSpace func(*volfs.FS) (uint64, error), args ...string) (code int, stdout []byte, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, stdin, &out, &errOut, root, freeSpace)
	return code, out.Bytes(), errOut.String()
}

// unreadReader fails the test if anything reads it: the pre-flight refusal
// must happen before stdin is touched.
type unreadReader struct{ t *testing.T }

func (r *unreadReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read, want the refusal before any read")
	return 0, errors.New("stdin read")
}

// plant writes a file directly under the root: fixture setup, not the code
// under test.
func plant(t *testing.T, root, rel string, content []byte) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

// TestListReturnsAJSONArrayOfEntries pins list's output shape: one JSON array,
// each element volfs.Entry's shape, one level deep, with a symlink's target
// described.
func TestListReturnsAJSONArrayOfEntries(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "plain.txt", []byte("hello"))
	plant(t, root, "sub/nested.txt", []byte("nested"))
	if err := os.Symlink("plain.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runHelper(t, root, nil, "list", "--", ".")
	if code != 0 {
		t.Fatalf("list exit = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("list stderr = %q, want empty", stderr)
	}

	var entries []volfs.Entry
	if err := json.Unmarshal(stdout, &entries); err != nil {
		t.Fatalf("list stdout is not a JSON array: %v\n%s", err, stdout)
	}
	names := make([]string, 0, len(entries))
	kinds := map[string]volfs.Kind{}
	for _, entry := range entries {
		names = append(names, entry.Name)
		kinds[entry.Name] = entry.Kind
	}
	want := []string{"link", "plain.txt", "sub"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("list names = %q, want %q", names, want)
	}
	if kinds["plain.txt"] != volfs.KindFile || kinds["sub"] != volfs.KindDir || kinds["link"] != volfs.KindSymlink {
		t.Errorf("list kinds = %v, want a file, a directory and a symlink", kinds)
	}
	if entry := entries[0]; entry.Link != "plain.txt" || entry.Size != int64(len("plain.txt")) {
		t.Errorf("list link = %+v, want symlink -> plain.txt", entry)
	}

	// A directory below the root lists its own level, and an empty directory
	// is an empty JSON array, never null.
	if code, stdout, stderr := runHelper(t, root, nil, "list", "--", "sub"); code != 0 {
		t.Fatalf("list sub exit = %d, stderr = %q", code, stderr)
	} else if !strings.Contains(string(stdout), `"nested.txt"`) {
		t.Errorf("list sub stdout = %s, want nested.txt", stdout)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runHelper(t, root, nil, "list", "--", "empty")
	if code != 0 {
		t.Fatalf("list empty exit = %d, stderr = %q", code, stderr)
	}
	if strings.TrimSpace(string(stdout)) != "[]" {
		t.Errorf("list empty stdout = %q, want []", stdout)
	}

	// An operation failure goes to stderr only; stdout stays empty and the
	// process exits non-zero.
	code, stdout, stderr = runHelper(t, root, nil, "list", "--", "missing")
	if code == 0 {
		t.Error("list missing exit = 0, want non-zero")
	}
	if len(stdout) != 0 {
		t.Errorf("list missing stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no such file") {
		t.Errorf("list missing stderr = %q, want a not-exist error", stderr)
	}
	if strings.Contains(stderr, "usage:") {
		t.Errorf("list missing stderr = %q, want no usage block for an operation error", stderr)
	}
}

// TestStatReturnsAJSONObject pins stat's output shape and matches List's.
func TestStatReturnsAJSONObject(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "data.bin", []byte("0123456789"))

	code, stdout, stderr := runHelper(t, root, nil, "stat", "--", "data.bin")
	if code != 0 {
		t.Fatalf("stat exit = %d, stderr = %q", code, stderr)
	}
	var entry volfs.Entry
	if err := json.Unmarshal(stdout, &entry); err != nil {
		t.Fatalf("stat stdout is not a JSON object: %v\n%s", err, stdout)
	}
	if entry.Name != "data.bin" || entry.Kind != volfs.KindFile || entry.Size != 10 {
		t.Errorf("stat = %+v, want a 10-byte data.bin", entry)
	}
	if entry.Mode.Perm() != 0o644 || entry.ModTime.IsZero() {
		t.Errorf("stat = %+v, want mode and mtime from disk", entry)
	}

	// A symlink is described as itself, target included.
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link-etc")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runHelper(t, root, nil, "stat", "--", "link-etc")
	if code != 0 {
		t.Fatalf("stat link-etc exit = %d", code)
	}
	if err := json.Unmarshal(stdout, &entry); err != nil {
		t.Fatalf("stat link-etc stdout: %v", err)
	}
	if entry.Kind != volfs.KindSymlink || entry.Link != "/etc/passwd" {
		t.Errorf("stat link-etc = %+v, want symlink -> /etc/passwd", entry)
	}
}

// TestReadStreamsRawBytes pins read's output: the file's bytes verbatim, with
// no JSON wrapping and nothing else mixed in, even for bytes that are not
// valid JSON or UTF-8.
func TestReadStreamsRawBytes(t *testing.T) {
	root := t.TempDir()
	payload := append(bytes.Repeat([]byte{0x00, 0xff, '\n', '{'}, 1<<12), []byte("tail")...)
	plant(t, root, "raw.bin", payload)

	code, stdout, stderr := runHelper(t, root, nil, "read", "--", "raw.bin")
	if code != 0 {
		t.Fatalf("read exit = %d, stderr = %q", code, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Errorf("read returned %d bytes, want the %d planted verbatim", len(stdout), len(payload))
	}

	// An inside-root symlink is followed; one that points outside is refused
	// by os.Root behind the helper and neither leaks bytes nor exits 0.
	if err := os.Symlink("raw.bin", filepath.Join(root, "link-in")); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runHelper(t, root, nil, "read", "--", "link-in"); code != 0 || !bytes.Equal(stdout, payload) {
		t.Errorf("read link-in = %d bytes, exit %d; want the target's bytes", len(stdout), code)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link-etc")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runHelper(t, root, nil, "read", "--", "link-etc")
	if code == 0 {
		t.Error("read link-etc exit = 0, want the escape refused")
	}
	if len(stdout) != 0 {
		t.Errorf("read link-etc leaked %d bytes", len(stdout))
	}
	if !strings.Contains(stderr, "path escapes from parent") {
		t.Errorf("read link-etc stderr = %q, want os.Root's refusal", stderr)
	}
}

// TestWriteStreamsStdinAndRoundTrips proves write consumes stdin as raw bytes
// and truncates on overwrite, by reading the result back both from disk and
// through the helper's own read.
func TestWriteStreamsStdinAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte("volume-bytes\x00\n"), 1<<13)

	// The upload lands in an existing directory, as the panel's upload does.
	if err := os.Mkdir(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runHelper(t, root, payload, "write", "--", "uploads/blob.bin")
	if code != 0 {
		t.Fatalf("write exit = %d, stderr = %q", code, stderr)
	}
	if len(stdout) != 0 {
		t.Errorf("write stdout = %q, want nothing", stdout)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "uploads", "blob.bin"))
	if err != nil {
		t.Fatalf("reading the written file: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Errorf("written file = %d bytes, want the %d streamed", len(onDisk), len(payload))
	}

	code, readBack, stderr := runHelper(t, root, nil, "read", "--", "uploads/blob.bin")
	if code != 0 || !bytes.Equal(readBack, payload) {
		t.Errorf("read-back = %d bytes, exit %d, stderr %q; want the written payload", len(readBack), code, stderr)
	}

	// A second, shorter write truncates: the old tail must not survive.
	if code, _, stderr := runHelper(t, root, []byte("short"), "write", "--", "uploads/blob.bin"); code != 0 {
		t.Fatalf("overwrite exit = %d, stderr = %q", code, stderr)
	}
	code, readBack, _ = runHelper(t, root, nil, "read", "--", "uploads/blob.bin")
	if code != 0 || string(readBack) != "short" {
		t.Errorf("after overwrite read = %q (exit %d), want %q", readBack, code, "short")
	}

	// A failed write reports on stderr and exits non-zero: the parent
	// directory does not exist.
	code, _, stderr = runHelper(t, root, []byte("x"), "write", "--", "no/such/dir/f")
	if code == 0 {
		t.Error("write to a missing directory exit = 0, want non-zero")
	}
	if !strings.Contains(stderr, "no such file") {
		t.Errorf("write to a missing directory stderr = %q", stderr)
	}
}

// TestWriteRefusesADeclaredSizeThatCannotFit: with --expect the helper
// measures the Volume's free space before it reads any stdin or opens the
// target. A declared size over the free space exits 3 — distinct from the
// operation-failure 1 and the usage 2 — with a message naming need and
// available, and leaves the target exactly as it was.
func TestWriteRefusesADeclaredSizeThatCannotFit(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "notes.txt", []byte("original"))

	free := func(*volfs.FS) (uint64, error) { return 10, nil }
	code, stdout, stderr := runHelperWith(t, root, &unreadReader{t: t}, free, "write", "--expect", "100", "--", "notes.txt")
	if code != exitRefused {
		t.Fatalf("write exit = %d, stderr = %q; want %d", code, stderr, exitRefused)
	}
	if len(stdout) != 0 {
		t.Errorf("write stdout = %q, want nothing", stdout)
	}
	for _, want := range []string{"needs 100 bytes", "only 10 free"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("write stderr = %q, want it to contain %q", stderr, want)
		}
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatalf("reading the target after the refusal: %v", err)
	}
	if string(onDisk) != "original" {
		t.Errorf("target = %q after the refusal, want it untouched", onDisk)
	}

	// A target that does not exist yet is not created either.
	code, _, stderr = runHelperWith(t, root, &unreadReader{t: t}, free, "write", "--expect", "11", "--", "new.bin")
	if code != exitRefused {
		t.Fatalf("write to a new path exit = %d, stderr = %q; want %d", code, stderr, exitRefused)
	}
	if _, err := os.Stat(filepath.Join(root, "new.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused write created new.bin: %v", err)
	}
}

// TestWriteAcceptsADeclaredSizeThatFits: a declared size within the free
// space writes normally, including a stream that ends exactly at the budget —
// which is not an overflow — and a write with no --expect at all still works
// under the stream guard.
func TestWriteAcceptsADeclaredSizeThatFits(t *testing.T) {
	root := t.TempDir()
	free := func(*volfs.FS) (uint64, error) { return 10, nil }

	exact := []byte("0123456789")
	code, _, stderr := runHelperWith(t, root, bytes.NewReader(exact), free, "write", "--expect", "10", "--", "exact.bin")
	if code != exitOK {
		t.Fatalf("exact-fit write exit = %d, stderr = %q", code, stderr)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "exact.bin"))
	if err != nil || !bytes.Equal(onDisk, exact) {
		t.Errorf("exact-fit file = %q, %v; want %q", onDisk, err, exact)
	}

	code, _, stderr = runHelperWith(t, root, bytes.NewReader([]byte("hi")), free, "write", "--expect", "2", "--", "spare.bin")
	if code != exitOK {
		t.Fatalf("write with room to spare exit = %d, stderr = %q", code, stderr)
	}
	if onDisk, err := os.ReadFile(filepath.Join(root, "spare.bin")); err != nil || string(onDisk) != "hi" {
		t.Errorf("spare.bin = %q, %v; want %q", onDisk, err, "hi")
	}

	code, _, stderr = runHelperWith(t, root, bytes.NewReader([]byte("undeclared")), free, "write", "--", "plain.bin")
	if code != exitOK {
		t.Fatalf("write without --expect exit = %d, stderr = %q", code, stderr)
	}
	if onDisk, err := os.ReadFile(filepath.Join(root, "plain.bin")); err != nil || string(onDisk) != "undeclared" {
		t.Errorf("plain.bin = %q, %v; want %q", onDisk, err, "undeclared")
	}
}

// TestWriteAbortsWhenTheStreamOverrunsTheFreeSpace: the guard delivers at
// most the budget measured at the start. A stream that runs past it aborts
// with exit 3 and the partial file is removed — for a declared upload whose
// declared size lied, and without --expect too, where the guard is the only
// protection.
func TestWriteAbortsWhenTheStreamOverrunsTheFreeSpace(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "with --expect", args: []string{"write", "--expect", "5", "--", "upload.bin"}},
		{name: "without --expect", args: []string{"write", "--", "upload.bin"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			plant(t, root, "upload.bin", []byte("original"))

			free := func(*volfs.FS) (uint64, error) { return 10, nil }
			code, stdout, stderr := runHelperWith(t, root, bytes.NewReader(bytes.Repeat([]byte("x"), 100)), free, tt.args...)
			if code != exitRefused {
				t.Fatalf("overrunning write exit = %d, stderr = %q; want %d", code, stderr, exitRefused)
			}
			if len(stdout) != 0 {
				t.Errorf("overrunning write stdout = %q, want nothing", stdout)
			}
			if !strings.Contains(stderr, "free space") {
				t.Errorf("overrunning write stderr = %q, want the free-space refusal", stderr)
			}
			if _, err := os.Stat(filepath.Join(root, "upload.bin")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the partial file survived the abort: %v", err)
			}
		})
	}
}

// TestWriteReportsAFailedFreeSpaceLookupAsAnOperationFailure: a free-space
// lookup that fails is not a refusal — there is no space answer to name — so
// it stays the operation failure 1.
func TestWriteReportsAFailedFreeSpaceLookupAsAnOperationFailure(t *testing.T) {
	root := t.TempDir()
	broken := func(*volfs.FS) (uint64, error) { return 0, errors.New("statfs: boom") }

	code, _, stderr := runHelperWith(t, root, bytes.NewReader([]byte("x")), broken, "write", "--expect", "1", "--", "f.bin")
	if code != exitError {
		t.Errorf("write exit = %d, stderr = %q; want %d", code, stderr, exitError)
	}
	if !strings.Contains(stderr, "boom") {
		t.Errorf("write stderr = %q, want the lookup failure", stderr)
	}
}

// TestWriteRefusesAMalformedExpect: --expect's value must be a non-negative
// integer in the panel's exact shape; anything else is the usage error 2,
// before any file is touched.
func TestWriteRefusesAMalformedExpect(t *testing.T) {
	root := t.TempDir()

	malformed := [][]string{
		{"write", "--expect", "abc", "--", "f.bin"},
		{"write", "--expect", "-1", "--", "f.bin"},
		{"write", "--expect", "1.5", "--", "f.bin"},
		{"write", "--expect", "--", "f.bin"},
		{"write", "--expect", ""},
		{"write", "--expect"},
		{"read", "--expect", "5", "--", "f.bin"},
	}
	for _, args := range malformed {
		code, stdout, stderr := runHelper(t, root, []byte("x"), args...)
		if code != exitUsage {
			t.Errorf("%v exit = %d, want %d", args, code, exitUsage)
		}
		if len(stdout) != 0 {
			t.Errorf("%v stdout = %q, want empty", args, stdout)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Errorf("%v stderr = %q, want the usage block", args, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "f.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a malformed invocation created f.bin: %v", err)
	}
}

// TestPathAfterDoubleDashMayStartWithADash proves `--` is what separates the
// path from flags: `-leading.txt` is a legal name after `--` and a usage
// error without it.
func TestPathAfterDoubleDashMayStartWithADash(t *testing.T) {
	root := t.TempDir()

	code, _, stderr := runHelper(t, root, []byte("dashed"), "write", "--", "-leading.txt")
	if code != 0 {
		t.Fatalf("write -- -leading.txt exit = %d, stderr = %q", code, stderr)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "-leading.txt"))
	if err != nil || string(onDisk) != "dashed" {
		t.Fatalf("read -leading.txt from disk = %q, %v; want %q", onDisk, err, "dashed")
	}
	code, stdout, stderr := runHelper(t, root, nil, "read", "--", "-leading.txt")
	if code != 0 || string(stdout) != "dashed" {
		t.Errorf("read -- -leading.txt = %q, exit %d, stderr %q", stdout, code, stderr)
	}

	// Without `--`, the same name is not accepted as a path at all.
	code, stdout, stderr = runHelper(t, root, nil, "read", "-leading.txt")
	if code == 0 {
		t.Error("read -leading.txt (no --) exit = 0, want a usage error")
	}
	if len(stdout) != 0 {
		t.Errorf("read -leading.txt (no --) stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("read -leading.txt (no --) stderr = %q, want the usage block", stderr)
	}
}

// TestAbsolutePathsAreRefusedBeforeAnyFilesystemCall proves the refusal does
// not depend on the Volume existing: the root here is an absent directory, so
// if the path were checked after volfs.Open the error would name the root
// instead.
func TestAbsolutePathsAreRefusedBeforeAnyFilesystemCall(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "absent")

	code, stdout, stderr := runHelper(t, missingRoot, nil, "list", "--", "/etc/passwd")
	if code == 0 {
		t.Error("list absolute exit = 0, want the path refused")
	}
	if len(stdout) != 0 {
		t.Errorf("list absolute stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "absolute path") || !strings.Contains(stderr, "/etc/passwd") {
		t.Errorf("list absolute stderr = %q, want a clear absolute-path refusal", stderr)
	}
	if strings.Contains(stderr, "open volume root") {
		t.Errorf("list absolute stderr = %q, want the refusal before any filesystem call", stderr)
	}

	// The refusal applies to every op, write included.
	code, _, stderr = runHelper(t, missingRoot, []byte("x"), "write", "--", "/etc/passwd")
	if code == 0 || !strings.Contains(stderr, "absolute path") {
		t.Errorf("write absolute = exit %d, stderr %q; want the refusal", code, stderr)
	}
}

// TestDeleteRemovesAFileAndAWholeTree drives delete through the helper: a
// tree disappears, a missing path is the no-op RemoveAll semantics already
// promise, and the root itself is refused.
func TestDeleteRemovesAFileAndAWholeTree(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "file.txt", []byte("x"))
	plant(t, root, "tree/deep/nested.txt", []byte("nested"))

	code, stdout, stderr := runHelper(t, root, nil, "delete", "--", "tree")
	if code != 0 {
		t.Fatalf("delete tree exit = %d, stderr = %q", code, stderr)
	}
	if len(stdout) != 0 {
		t.Errorf("delete stdout = %q, want nothing", stdout)
	}
	if _, err := os.Stat(filepath.Join(root, "tree")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("delete tree left the tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); err != nil {
		t.Errorf("delete tree touched a sibling: %v", err)
	}

	if code, _, stderr := runHelper(t, root, nil, "delete", "--", "missing"); code != 0 {
		t.Errorf("delete missing exit = %d, stderr = %q; want the RemoveAll no-op", code, stderr)
	}
	if code, _, _ := runHelper(t, root, nil, "delete", "--", "."); code == 0 {
		t.Error("delete . exit = 0, want the root refused")
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); err != nil {
		t.Errorf("delete . damaged the root: %v", err)
	}
}

// TestArchivePinsTheFormatAndStreamsTarGz: `archive --format tar.gz` answers
// with a real gzip stream on stdout whose tar entries are the path as given,
// and every other or missing format is refused before any byte is written.
func TestArchivePinsTheFormatAndStreamsTarGz(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "file.txt", []byte("hello"))
	plant(t, root, "dir/nested.bin", []byte("nested"))

	code, stdout, stderr := runHelper(t, root, nil, "archive", "--format", "tar.gz", "--", "dir")
	if code != 0 {
		t.Fatalf("archive exit = %d, stderr = %q", code, stderr)
	}
	if len(stdout) < 2 || stdout[0] != 0x1f || stdout[1] != 0x8b {
		t.Fatalf("archive stdout does not start with the gzip magic: %q", stdout[:2])
	}
	zr, err := gzip.NewReader(bytes.NewReader(stdout))
	if err != nil {
		t.Fatalf("archive stdout is not gzipped: %v", err)
	}
	tr := tar.NewReader(zr)
	var names []string
	contents := map[string]string{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading the tar stream: %v", err)
		}
		names = append(names, header.Name)
		if header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("reading %s: %v", header.Name, err)
			}
			contents[header.Name] = string(data)
		}
	}
	want := "dir/,dir/nested.bin"
	if strings.Join(names, ",") != want {
		t.Errorf("archive entries = %q, want %q", names, want)
	}
	if contents["dir/nested.bin"] != "nested" {
		t.Errorf("archive dir/nested.bin = %q, want %q", contents["dir/nested.bin"], "nested")
	}

	// Bad and missing formats are errors, with nothing on stdout.
	for _, args := range [][]string{
		{"archive", "--format", "zip", "--", "file.txt"},
		{"archive", "--format", "gzip", "--", "file.txt"},
		{"archive", "--", "file.txt"},
	} {
		code, stdout, stderr := runHelper(t, root, nil, args...)
		if code == 0 {
			t.Errorf("%v exit = 0, want the format refused", args)
		}
		if len(stdout) != 0 {
			t.Errorf("%v stdout = %q, want empty", args, stdout)
		}
		if !strings.Contains(stderr, "tar.gz") {
			t.Errorf("%v stderr = %q, want tar.gz named as the only format", args, stderr)
		}
	}
}

// TestMalformedInvocationsPrintUsageAndFail: unknown operations, missing
// arguments and wrong shapes are refused with usage on stderr, a non-zero
// exit and nothing on stdout.
func TestMalformedInvocationsPrintUsageAndFail(t *testing.T) {
	root := t.TempDir()
	plant(t, root, "file.txt", []byte("x"))

	malformed := [][]string{
		nil,
		{"list"},
		{"list", "--"},
		{"list", "file.txt"},
		{"list", "--", "file.txt", "extra"},
		{"stat", "--format", "tar.gz", "--", "file.txt"},
		{"archive", "--format", "tar.gz", "--"},
		{"archive", "--format", "tar.gz", "file.txt"},
		{"archive"},
		{"frobnicate", "--", "file.txt"},
	}
	for _, args := range malformed {
		code, stdout, stderr := runHelper(t, root, nil, args...)
		if code == 0 {
			t.Errorf("%v exit = 0, want non-zero", args)
		}
		if len(stdout) != 0 {
			t.Errorf("%v stdout = %q, want empty", args, stdout)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Errorf("%v stderr = %q, want the usage block", args, stderr)
		}
	}

	// The unknown operation is named, so the panel's error reported to the
	// operator says which op went wrong.
	_, _, stderr := runHelper(t, root, nil, "frobnicate", "--", "file.txt")
	if !strings.Contains(stderr, `unknown operation "frobnicate"`) {
		t.Errorf("unknown op stderr = %q, want the op named", stderr)
	}
}
