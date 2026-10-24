package volfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// B1: a write whose stream ends early (what the helper sees when the client
// aborts: EOF on stdin) replaces the original with the partial bytes, no error.
func TestReviewB1PartialWriteReplacesOriginal(t *testing.T) {
	dir := t.TempDir()
	original := bytes.Repeat([]byte("ORIGINAL-DATA-"), 1000)
	if err := os.WriteFile(filepath.Join(dir, "config.db"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	upload := bytes.Repeat([]byte("NEW-"), 10000) // client meant to send 40000 bytes
	err = fs.Write("config.db", io.LimitReader(bytes.NewReader(upload), 100))
	got, _ := os.ReadFile(filepath.Join(dir, "config.db"))
	t.Logf("B1 clean EOF after 100 bytes: Write err=%v; original %d bytes -> now %d bytes (%q...)", err, len(original), len(got), got[:12])

	if err := os.WriteFile(filepath.Join(dir, "config.db"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	err = fs.Write("config.db", io.MultiReader(bytes.NewReader(upload[:100]), errReader{}))
	got, _ = os.ReadFile(filepath.Join(dir, "config.db"))
	t.Logf("B1 I/O error after 100 bytes: Write err=%v; original %d bytes -> now %d bytes", err, len(original), len(got))
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// A2: how big the helper's list JSON is per entry, and how much heap a
// listing of N entries takes in the panel (bytes.Buffer + json.Valid).
func TestReviewA2ListingSize(t *testing.T) {
	dir := t.TempDir()
	const n = 50000
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("cache-entry-%08d.tmp", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	entries, err := fs.List(".")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := json.NewEncoder(&out).Encode(entries); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	per := float64(out.Len()) / n
	t.Logf("A2 %d entries -> %d bytes JSON (%.0f B/entry); helper-side alloc %.1f MB", n, out.Len(), per, float64(after.TotalAlloc-before.TotalAlloc)/1e6)
	// panel side: metadata() buffers stdout in a bytes.Buffer fed by io.Copy-style writes.
	runtime.GC()
	runtime.ReadMemStats(&before)
	var panel bytes.Buffer
	src := bytes.NewReader(out.Bytes())
	buf := make([]byte, 32<<10)
	for {
		k, err := src.Read(buf)
		panel.Write(buf[:k])
		if err != nil {
			break
		}
	}
	_ = json.Valid(panel.Bytes())
	runtime.ReadMemStats(&after)
	t.Logf("A2 panel-side alloc for buffering: %.1f MB (cap %d)", float64(after.TotalAlloc-before.TotalAlloc)/1e6, panel.Cap())
	t.Logf("A2 extrapolated: 1,000,000 entries ~ %.0f MB JSON in the panel", per*1e6/1e6)
}

func TestReviewA2PeakHeapAtOneMillion(t *testing.T) {
	line := []byte(`{"name":"cache-entry-00000000.tmp","kind":"file","size":0,"mode":420,"mtime":"2026-10-24T15:30:00.123456789+07:00","uid":0,"gid":0},`)
	const n = 1000000
	chunk := bytes.Repeat(line, 32<<10/len(line))
	var peak uint64
	var ms runtime.MemStats
	runtime.GC()
	var panel bytes.Buffer
	panel.WriteByte('[')
	for written := 0; written < n*len(line); written += len(chunk) {
		panel.Write(chunk)
		if written%(8<<20) < len(chunk) {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapInuse)
		}
	}
	runtime.ReadMemStats(&ms)
	peak = max(peak, ms.HeapInuse)
	t.Logf("A2 1M entries: JSON %d MB, buffer cap %d MB, peak HeapInuse ~%d MB", panel.Len()>>20, panel.Cap()>>20, peak>>20)
}
