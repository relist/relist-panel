package deployments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "1.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadLogRuneBoundary(t *testing.T) {
	const want = "สวัสดี" // every rune is 3 bytes
	path := writeLog(t, want)

	text, next, err := ReadLog(path, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if text != "ส" || next != 3 {
		t.Fatalf("first read = %q next %d, want one whole rune and offset 3", text, next)
	}

	var got strings.Builder
	var offset int64
	for i := 0; i < 20; i++ {
		text, next, err := ReadLog(path, offset, 4)
		if err != nil {
			t.Fatal(err)
		}
		if next == offset {
			break
		}
		got.WriteString(text)
		offset = next
	}
	if got.String() != want {
		t.Fatalf("rebuilt %q, want %q", got.String(), want)
	}
}

func TestReadLogMaxSmallerThanARune(t *testing.T) {
	path := writeLog(t, "สวัสดี")
	text, next, err := ReadLog(path, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || next != 0 {
		t.Fatalf("read = %q next %d, want nothing while no whole rune fits", text, next)
	}
}

func TestReadLogCapsAt1MiB(t *testing.T) {
	path := writeLog(t, strings.Repeat("a", 3<<20))
	text, next, err := ReadLog(path, 0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 1<<20 || next != 1<<20 {
		t.Fatalf("read %d bytes next %d, want exactly 1 MiB", len(text), next)
	}
}

func TestReadLogPastTheEnd(t *testing.T) {
	path := writeLog(t, "abc")
	text, next, err := ReadLog(path, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || next != 10 {
		t.Fatalf("read = %q next %d, want empty at the requested offset", text, next)
	}
}
