package accesslog

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 3, 0, 0, 30, 0, time.UTC)

type fakeTraefik struct {
	reopens   int
	reopenErr error
	logs      string
	args      []string
	err       error
}

func (f *fakeTraefik) Reopen(context.Context) error {
	f.reopens++
	return f.reopenErr
}

func (f *fakeTraefik) Logs(context.Context, int) (string, error) { return f.logs, f.err }
func (f *fakeTraefik) Args(context.Context) ([]string, error)    { return f.args, f.err }

type rotatorFixture struct {
	r       *Rotator
	traefik *fakeTraefik
	dir     string
}

func newRotator(t *testing.T, now time.Time) rotatorFixture {
	t.Helper()
	traefik := &fakeTraefik{}
	dir := filepath.Join(t.TempDir(), "access")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Rotator{Dir: dir, Store: newStore(t), Traefik: traefik, Now: func() time.Time { return now }, QuietFor: 5 * time.Minute}
	return rotatorFixture{r: r, traefik: traefik, dir: dir}
}

// write creates name in dir with content and sets its mtime.
func write(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func wantNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := names(t, dir)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("files = %q, want %q", got, want)
	}
}

const line1102 = `{"StartUTC":"2026-11-02T10:00:00Z","RequestHost":"a.test"}` + "\n"

func TestRotateRenamesYesterday(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access.log", line1102, testNow)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-11-02.log")
	if f.traefik.reopens != 1 {
		t.Fatalf("reopens = %d, want 1", f.traefik.reopens)
	}
}

func TestRotateSkipsTodaysFile(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access.log", `{"StartUTC":"2026-11-03T00:00:10Z"}`+"\n", testNow)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access.log")
	if f.traefik.reopens != 0 {
		t.Fatalf("reopens = %d, want 0", f.traefik.reopens)
	}
}

func TestRotateNoFileIsNoop(t *testing.T) {
	f := newRotator(t, testNow)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir)
	f.r.Dir = filepath.Join(f.dir, "missing")
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatalf("missing dir: %v", err)
	}
	if _, err := os.Stat(f.r.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing dir was created: %v", err)
	}
	if f.traefik.reopens != 0 {
		t.Fatalf("reopens = %d, want 0", f.traefik.reopens)
	}
}

func TestRotateEmptyFileIsLeft(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access.log", "", testNow.Add(-72*time.Hour))
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access.log")
}

func TestRotateCatchUpUsesLatestDate(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	f := newRotator(t, now)
	write(t, f.dir, "access.log", line1102, now)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-11-05.log")
}

func TestRotateUnparsableFirstLineUsesMtime(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access.log", "garbage\n", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Renamed, and quiet long enough to be gzipped in the same run.
	wantNames(t, f.dir, "access-2026-11-02.log.gz")
}

func TestRotateCollisionSuffix(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access-2026-11-02.log.gz", "x", testNow)
	write(t, f.dir, "access.log", line1102, testNow)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-11-02.log.gz", "access-2026-11-02-2.log")
}

func gunzip(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGzipWaitsForQuiet(t *testing.T) {
	f := newRotator(t, testNow)
	write(t, f.dir, "access-2026-11-01.log", "busy\n", testNow.Add(-time.Minute))
	write(t, f.dir, "access-2026-10-31.log", "quiet\n", testNow.Add(-6*time.Minute))
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-11-01.log", "access-2026-10-31.log.gz")
	if got := gunzip(t, filepath.Join(f.dir, "access-2026-10-31.log.gz")); got != "quiet\n" {
		t.Fatalf("gunzip = %q", got)
	}
}

func TestGzipLeftoverTmpReplaced(t *testing.T) {
	f := newRotator(t, testNow)
	old := testNow.Add(-time.Hour)
	write(t, f.dir, "access-2026-11-01.log", "whole\n", old)
	write(t, f.dir, "access-2026-11-01.log.gz.tmp", "\x1f\x8b trunc", old)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-11-01.log.gz")
	if got := gunzip(t, filepath.Join(f.dir, "access-2026-11-01.log.gz")); got != "whole\n" {
		t.Fatalf("gunzip = %q", got)
	}
}

func TestReopenFailureKeepsFile(t *testing.T) {
	f := newRotator(t, testNow)
	f.traefik.reopenErr = errors.New("no such container")
	write(t, f.dir, "access.log", line1102, testNow)
	if err := f.r.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce succeeded, want the Reopen error")
	}
	// Fresh mtime: Traefik may still be writing it, so it stays plain.
	wantNames(t, f.dir, "access-2026-11-02.log")
	got, err := f.r.Store.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.LastError == "" {
		t.Fatal("LastError is empty")
	}
}

func TestRetentionDeletesOld(t *testing.T) {
	f := newRotator(t, time.Date(2026, 11, 3, 0, 0, 30, 0, time.UTC))
	old := testNow.Add(-time.Hour)
	write(t, f.dir, "access-2026-08-04.log.gz", "x", old)
	write(t, f.dir, "access-2026-08-05.log.gz", "x", old)
	write(t, f.dir, "access-2026-08-04-2.log.gz", "x", old)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2026-08-05.log.gz")
}

func TestRetentionForeverKeepsAll(t *testing.T) {
	f := newRotator(t, testNow)
	if err := f.r.Store.SaveRetention(context.Background(), 1, true); err != nil {
		t.Fatal(err)
	}
	write(t, f.dir, "access-2020-01-01.log.gz", "x", testNow.Add(-time.Hour))
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access-2020-01-01.log.gz")
}

func TestUnmatchedFilesUntouched(t *testing.T) {
	f := newRotator(t, testNow)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	write(t, f.dir, "notes.txt", "keep", old)
	write(t, f.dir, "access.log.bak", "keep", old)
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantNames(t, f.dir, "access.log.bak", "notes.txt")
}

func TestRecordRunOnSuccess(t *testing.T) {
	f := newRotator(t, testNow)
	if err := f.r.Store.RecordRun(context.Background(), testNow.Add(-24*time.Hour), "old"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := f.r.Store.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRotatedAt == nil || !got.LastRotatedAt.Equal(testNow.Truncate(time.Second)) || got.LastError != "" {
		t.Fatalf("settings = %+v, want run at %v, no error", got, testNow)
	}
}

func TestNextWake(t *testing.T) {
	cases := []struct {
		now     time.Time
		pending bool
		want    time.Duration
	}{
		{time.Date(2026, 11, 3, 0, 0, 30, 0, time.UTC), false, 24*time.Hour - 30*time.Second},
		{time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC), false, time.Hour},
		{time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), true, 10 * time.Minute},
		{time.Date(2026, 11, 3, 23, 55, 0, 0, time.UTC), true, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := nextWake(c.now, c.pending); got != c.want {
			t.Errorf("nextWake(%v, %v) = %v, want %v", c.now, c.pending, got, c.want)
		}
	}
}
