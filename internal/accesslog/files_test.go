package accesslog

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"access.log", "access-2026-11-02.log", "access-2026-11-02.log.gz", "access-2026-11-02-3.log.gz"} {
		if !ValidName(name) {
			t.Errorf("ValidName(%q) = false", name)
		}
	}
	for _, name := range []string{"../panel.db", "access.log/../x", "access-2026-11-02.log.gz.tmp", "Access.log", "", "access.log.gz"} {
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true", name)
		}
	}
}

func TestListOrderAndTotal(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "access-2026-11-01.log.gz", "aa", testNow)
	write(t, dir, "access.log", "bbb", testNow)
	write(t, dir, "access-2026-11-02.log", "c", testNow)
	write(t, dir, "access-2026-11-02-2.log.gz", "dddd", testNow)
	write(t, dir, "access-2026-11-01.log.gz.tmp", "skip", testNow)
	write(t, dir, "notes.txt", "skip", testNow)
	files, total, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{Name: "access.log", Size: 3},
		{Name: "access-2026-11-02-2.log.gz", Date: "2026-11-02", Size: 4},
		{Name: "access-2026-11-02.log", Date: "2026-11-02", Size: 1},
		{Name: "access-2026-11-01.log.gz", Date: "2026-11-01", Size: 2},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %+v\nwant  %+v", files, want)
	}
	if total != 10 {
		t.Fatalf("total = %d, want 10", total)
	}
}

func TestListMissingDir(t *testing.T) {
	files, total, err := List(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(files) != 0 || total != 0 {
		t.Fatalf("List(missing) = %v, %d, %v", files, total, err)
	}
}
