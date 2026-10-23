package volfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func openRoot(t *testing.T, dir string) *FS {
	t.Helper()
	v, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func ownerOf(t *testing.T, path string) (uint32, uint32) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	return st.Uid, st.Gid
}

func TestPackUnpackKeepsOwnershipAndModes(t *testing.T) {
	src := t.TempDir()
	mtime := time.Unix(1_700_000_000, 0)
	if err := os.MkdirAll(filepath.Join(src, "pgdata", "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pgdata", "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "shared"), 0o755|fs.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pgdata/PG_VERSION", filepath.Join(src, "version")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(src, "pgdata", "PG_VERSION"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	asRoot := os.Geteuid() == 0
	if asRoot {
		for _, p := range []string{"pgdata", "pgdata/base", "pgdata/PG_VERSION"} {
			if err := os.Lchown(filepath.Join(src, p), 999, 999); err != nil {
				t.Fatal(err)
			}
		}
	}

	var buf bytes.Buffer
	if err := openRoot(t, src).Pack(".", &buf); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	dst := t.TempDir()
	if err := openRoot(t, dst).Unpack(&buf); err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	for _, p := range []string{"pgdata", "pgdata/base", "pgdata/PG_VERSION", "shared", "version"} {
		want, err := os.Lstat(filepath.Join(src, p))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.Lstat(filepath.Join(dst, p))
		if err != nil {
			t.Fatalf("%s missing after unpack: %v", p, err)
		}
		if got.Mode() != want.Mode() {
			t.Errorf("%s mode = %v, want %v", p, got.Mode(), want.Mode())
		}
		wu, wg := ownerOf(t, filepath.Join(src, p))
		gu, gg := ownerOf(t, filepath.Join(dst, p))
		if gu != wu || gg != wg {
			t.Errorf("%s owner = %d:%d, want %d:%d", p, gu, gg, wu, wg)
		}
	}
	if link, _ := os.Readlink(filepath.Join(dst, "version")); link != "pgdata/PG_VERSION" {
		t.Errorf("symlink target = %q", link)
	}
	if info, _ := os.Stat(filepath.Join(dst, "pgdata", "PG_VERSION")); !info.ModTime().Equal(mtime) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), mtime)
	}
	if asRoot {
		if u, g := ownerOf(t, filepath.Join(dst, "pgdata", "PG_VERSION")); u != 999 || g != 999 {
			t.Errorf("owner = %d:%d, want 999:999", u, g)
		}
	}
}

func TestPackHasNoSizeCap(t *testing.T) {
	// The unpacked copy is 5 GiB of real data, and the default temp dir may be
	// a tmpfs; the migration probe covers the same case against Docker.
	if os.Getenv("RELIST_BIG_TESTS") != "1" {
		t.Skip("set RELIST_BIG_TESTS=1 to unpack a 5 GiB file")
	}
	src := t.TempDir()
	f, err := os.Create(filepath.Join(src, "big"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(5 << 30); err != nil {
		t.Fatal(err)
	}
	f.Close()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(openRoot(t, src).Pack(".", pw)) }()
	dst := t.TempDir()
	if err := openRoot(t, dst).Unpack(pr); err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}
	if info, err := os.Stat(filepath.Join(dst, "big")); err != nil || info.Size() != 5<<30 {
		t.Fatalf("big = %v, %v", info, err)
	}
}

func tarOf(t *testing.T, headers ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range headers {
		// Owned by the test's own user, so a non-root run can chown to it.
		h.Uid, h.Gid = os.Getuid(), os.Getgid()
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
		}
	}
	tw.Close()
	return &buf
}

func TestUnpackRefusesEscapes(t *testing.T) {
	cases := map[string]*bytes.Buffer{
		"dotdot":   tarOf(t, &tar.Header{Name: "../x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"absolute": tarOf(t, &tar.Header{Name: "/etc/x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"through symlink": tarOf(t,
			&tar.Header{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "../../etc", Mode: 0o777},
			&tar.Header{Name: "a/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"fifo": tarOf(t, &tar.Header{Name: "p", Typeflag: tar.TypeFifo, Mode: 0o644}),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			os.Mkdir(root, 0o755)
			err := openRoot(t, root).Unpack(body)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("Unpack() error = %v, want ErrUnsafeArchive", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "x")); err == nil {
				t.Fatal("wrote outside the root")
			}
		})
	}
}

func TestPackSingleFile(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "conf.yml"), []byte("a: 1\n"), 0o640)
	var buf bytes.Buffer
	if err := openRoot(t, src).Pack("conf.yml", &buf); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if len(names) != 1 || names[0] != "conf.yml" {
		t.Fatalf("entries = %q, want [conf.yml]", names)
	}
}

func TestPackExcludingSkipsMatches(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "panel.db"), []byte("db"), 0o600)
	os.Mkdir(filepath.Join(src, ".migration"), 0o700)
	os.WriteFile(filepath.Join(src, ".migration", "state.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(src, "keep"), []byte("k"), 0o644)
	var buf bytes.Buffer
	skip := func(rel string) bool { return rel == "panel.db" || rel == ".migration" }
	if err := openRoot(t, src).PackExcluding(".", skip, &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := openRoot(t, dst).Unpack(&buf); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatalf("unpacked %v, want only keep", entries)
	}
}

func TestSizeSumsRegularFiles(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "one"), make([]byte, 10), 0o644)
	os.WriteFile(filepath.Join(src, "a", "b", "two"), make([]byte, 32), 0o644)
	os.Symlink("one", filepath.Join(src, "a", "link"))
	got, err := openRoot(t, src).Size(".")
	if err != nil || got != 42 {
		t.Fatalf("Size() = %d, %v; want 42", got, err)
	}
}

func TestPackUnpackKeepsTheRootsModeAndOwner(t *testing.T) {
	src := t.TempDir()
	if err := os.Chmod(src, 0o700|fs.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "PG_VERSION"), []byte("16"), 0o600)
	mtime := time.Unix(1_600_000_000, 0)
	if os.Geteuid() == 0 {
		os.Lchown(src, 999, 999)
	}
	os.Chtimes(src, mtime, mtime)
	var buf bytes.Buffer
	if err := openRoot(t, src).Pack(".", &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	os.Chmod(dst, 0o755)
	if err := openRoot(t, dst).Unpack(&buf); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Stat(dst)
	want, _ := os.Stat(src)
	if got.Mode() != want.Mode() {
		t.Fatalf("root mode = %v, want %v", got.Mode(), want.Mode())
	}
	if !got.ModTime().Equal(mtime) {
		t.Fatalf("root mtime = %v, want %v", got.ModTime(), mtime)
	}
	wu, wg := ownerOf(t, src)
	gu, gg := ownerOf(t, dst)
	if gu != wu || gg != wg {
		t.Fatalf("root owner = %d:%d, want %d:%d", gu, gg, wu, wg)
	}
}

func TestUnpackUnderKeepsEveryEntryBeneathTop(t *testing.T) {
	refused := map[string]*bytes.Buffer{
		"sibling": tarOf(t, &tar.Header{Name: "uploads/", Typeflag: tar.TypeDir, Mode: 0o755},
			&tar.Header{Name: "cron.d/x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
		"root entry":  tarOf(t, &tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o777}),
		"prefix only": tarOf(t, &tar.Header{Name: "uploads-old", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}),
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			err := openRoot(t, root).UnpackUnder("uploads", body)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("UnpackUnder() error = %v, want ErrUnsafeArchive", err)
			}
		})
	}
	t.Run("beneath", func(t *testing.T) {
		root := t.TempDir()
		body := tarOf(t, &tar.Header{Name: "uploads/", Typeflag: tar.TypeDir, Mode: 0o755},
			&tar.Header{Name: "uploads/a", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		if err := openRoot(t, root).UnpackUnder("uploads", body); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "uploads", "a")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestUnpackUnderRefusesALinkWhereADirectoryWas(t *testing.T) {
	cases := map[string]*bytes.Buffer{
		"link replaces dir": tarOf(t,
			&tar.Header{Name: "uploads/", Typeflag: tar.TypeDir, Mode: 0o777},
			&tar.Header{Name: "uploads", Typeflag: tar.TypeSymlink, Linkname: "cron.d", Mode: 0o777}),
		"dir over link": tarOf(t,
			&tar.Header{Name: "uploads", Typeflag: tar.TypeSymlink, Linkname: "cron.d", Mode: 0o777},
			&tar.Header{Name: "uploads/", Typeflag: tar.TypeDir, Mode: 0o777}),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sibling := filepath.Join(root, "cron.d")
			os.Mkdir(sibling, 0o700)
			err := openRoot(t, root).UnpackUnder("uploads", body)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("UnpackUnder() error = %v, want ErrUnsafeArchive", err)
			}
			info, err := os.Stat(sibling)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Fatalf("sibling mode = %o, want 700", got)
			}
		})
	}
}
