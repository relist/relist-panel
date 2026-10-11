package sources

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"relist-panel/internal/volfs"
)

type zent struct {
	name, body string
	mode       fs.FileMode // 0 = default
	symlink    bool
}

func buildZip(t *testing.T, ents []zent) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.symlink:
			h.SetMode(fs.ModeSymlink | 0o777)
		case e.mode != 0:
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "in.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractZip(t *testing.T) {
	t.Run("strips a single root and ignores macOS junk", func(t *testing.T) {
		zp := buildZip(t, []zent{
			{name: "__MACOSX/shop-main/._a", body: "junk"},
			{name: ".DS_Store", body: "junk"},
			{name: "shop-main/", mode: fs.ModeDir | 0o755},
			{name: "shop-main/a.txt", body: "a"},
			{name: "shop-main/bin/run.sh", body: "#!/bin/sh", mode: 0o755},
			{name: "shop-main/link", body: "a.txt", symlink: true},
		})
		dest := filepath.Join(t.TempDir(), "out")
		n, err := ExtractZip(zp, dest, volfs.DefaultExtractLimits())
		if err != nil {
			t.Fatal(err)
		}
		if n != 3 {
			t.Errorf("files = %d, want 3", n)
		}
		if b, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(b) != "a" {
			t.Errorf("a.txt = %q, %v", b, err)
		}
		info, err := os.Stat(filepath.Join(dest, "bin", "run.sh"))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Errorf("run.sh = %v, %v", info, err)
		}
		if got, err := os.Readlink(filepath.Join(dest, "link")); err != nil || got != "a.txt" {
			t.Errorf("link = %q, %v", got, err)
		}
		for _, junk := range []string{"__MACOSX", ".DS_Store", "shop-main"} {
			if _, err := os.Lstat(filepath.Join(dest, junk)); !os.IsNotExist(err) {
				t.Errorf("%s present", junk)
			}
		}
	})

	t.Run("keeps several roots", func(t *testing.T) {
		zp := buildZip(t, []zent{{name: "a/x", body: "1"}, {name: "b/y", body: "2"}})
		dest := filepath.Join(t.TempDir(), "out")
		if _, err := ExtractZip(zp, dest, volfs.DefaultExtractLimits()); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dest, "a", "x")); err != nil {
			t.Error(err)
		}
	})

	t.Run("relative symlink up inside the tree", func(t *testing.T) {
		zp := buildZip(t, []zent{
			{name: "a/x", body: "1"},
			{name: "a/b/up", body: "../x", symlink: true},
			{name: "c", body: "1"},
		})
		dest := filepath.Join(t.TempDir(), "out")
		if _, err := ExtractZip(zp, dest, volfs.DefaultExtractLimits()); err != nil {
			t.Fatal(err)
		}
	})

	unsafe := map[string][]zent{
		"dotdot":           {{name: "../x", body: "x"}},
		"nested dotdot":    {{name: "a/../../x", body: "x"}},
		"absolute":         {{name: "/abs", body: "x"}},
		"backslash climb":  {{name: `..\x`, body: "x"}},
		"symlink up":       {{name: "ok", body: "x"}, {name: "l", body: "../../etc", symlink: true}},
		"symlink absolute": {{name: "ok", body: "x"}, {name: "l", body: "/etc", symlink: true}},
		"symlink deep up":  {{name: "ok", body: "x"}, {name: "a/b/l", body: "../../../x", symlink: true}},
		"device":           {{name: "dev", body: "", mode: fs.ModeDevice | 0o644}},
	}
	for name, ents := range unsafe {
		t.Run("refuses "+name, func(t *testing.T) {
			zp := buildZip(t, ents)
			dest := filepath.Join(t.TempDir(), "out")
			_, err := ExtractZip(zp, dest, volfs.DefaultExtractLimits())
			if !errors.Is(err, ErrUnsafeZip) {
				t.Fatalf("err = %v", err)
			}
			if entries, _ := os.ReadDir(dest); len(entries) != 0 {
				t.Errorf("wrote %d entries before refusing", len(entries))
			}
		})
	}

	t.Run("refuses too many entries", func(t *testing.T) {
		zp := buildZip(t, []zent{{name: "a", body: "1"}, {name: "b", body: "1"}, {name: "c", body: "1"}})
		_, err := ExtractZip(zp, filepath.Join(t.TempDir(), "out"), volfs.ExtractLimits{MaxEntries: 2, MaxBytes: 1 << 20})
		if !errors.Is(err, volfs.ErrArchiveTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("refuses too many bytes", func(t *testing.T) {
		zp := buildZip(t, []zent{{name: "a", body: "0123456789"}})
		dest := filepath.Join(t.TempDir(), "out")
		_, err := ExtractZip(zp, dest, volfs.ExtractLimits{MaxEntries: 10, MaxBytes: 5})
		if !errors.Is(err, volfs.ErrArchiveTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
}
