package requestmetrics

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/domains"
)

type rig struct {
	t     *testing.T
	dir   string
	db    *sql.DB
	store *Store
	now   time.Time
	in    *Ingester
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db := openDB(t)
	r := &rig{t: t, dir: t.TempDir(), db: db, store: NewStore(db), now: at(0, 0)}
	r.in = r.ingester()
	return r
}

func (r *rig) ingester() *Ingester {
	hosts := NewDomainHosts(func(context.Context) ([]domains.Domain, error) {
		return []domains.Domain{{ID: 3, ComposeID: 12, Host: "a.test"}}, nil
	}, func() time.Time { return r.now })
	return &Ingester{Dir: r.dir, Store: r.store, Hosts: hosts.Lookup, Now: func() time.Time { return r.now }}
}

func (r *rig) path() string { return filepath.Join(r.dir, liveName) }

func (r *rig) step(now time.Time) {
	r.t.Helper()
	r.now = now
	if err := r.in.Step(context.Background()); err != nil {
		r.t.Fatalf("Step: %v", err)
	}
}

func (r *rig) append(path string, s string) {
	r.t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) totals() map[int64]uint64 {
	r.t.Helper()
	rows, err := r.store.Rows(context.Background(), Minute, []Key{total}, at(-600, 0))
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[int64]uint64{}
	for _, row := range rows {
		out[row.BucketAt.Unix()] += row.Counts.Requests()
	}
	return out
}

func jsonLine(start time.Time, router string, status int) string {
	return fmt.Sprintf(`{"StartUTC":%q,"RouterName":%q,"DownstreamStatus":%d,"Duration":5000000,"RequestHost":"x.test"}`+"\n",
		start.Format(time.RFC3339Nano), router, status)
}

func TestIngesterReadsAppendedLines(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	r.append(r.path(), jsonLine(at(0, 1), "panel@docker", 200)+jsonLine(at(0, 2), "relist-c12-d3@docker", 404)+jsonLine(at(0, 3), "", 200)+jsonLine(at(1, 31), "", 200))
	r.step(at(1, 31))
	rows, _ := r.store.Rows(context.Background(), Minute, []Key{total, {Scope: ScopePanel}, {Scope: ScopeHost, ComposeID: 12, Host: "a.test"}}, at(0, 0))
	got := map[Key]uint64{}
	for _, row := range rows {
		got[row.Key] += row.Counts.Requests()
	}
	if got[total] != 3 || got[Key{Scope: ScopePanel}] != 1 || got[Key{Scope: ScopeHost, ComposeID: 12, Host: "a.test"}] != 1 {
		t.Fatalf("rows = %v", got)
	}
}

func TestIngesterPartialLine(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	l := jsonLine(at(0, 5), "", 200)
	r.append(r.path(), l[:30])
	r.step(at(0, 6))
	r.append(r.path(), l[30:]+jsonLine(at(1, 31), "", 200))
	r.step(at(1, 31))
	if got := r.totals()[at(0, 0).Unix()]; got != 1 {
		t.Fatalf("minute 10:00 = %d, want 1", got)
	}
}

func TestIngesterRotationKeepsOpenMinutes(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	r.append(r.path(), jsonLine(at(0, 1), "", 200)+jsonLine(at(0, 2), "", 200))
	r.step(at(0, 30))
	rotated := filepath.Join(r.dir, "access-2026-11-06.log")
	if err := os.Rename(r.path(), rotated); err != nil {
		t.Fatal(err)
	}
	r.append(rotated, jsonLine(at(0, 40), "", 200)) // written to the old file after the rename
	r.append(r.path(), jsonLine(at(1, 31), "", 200))
	r.step(at(1, 31))
	if got := r.totals()[at(0, 0).Unix()]; got != 3 {
		t.Fatalf("minute 10:00 = %d, want 3", got)
	}
	cur, ok, _ := r.store.Cursor(context.Background())
	fi, _ := os.Stat(r.path())
	if !ok || cur.Inode != inodeOf(fi) {
		t.Fatalf("cursor inode = %d, want the new file's %d", cur.Inode, inodeOf(fi))
	}
	// The new file's line is still open and the cursor points into the new file.
	if cur.ResumeOffset != 0 {
		t.Fatalf("resume offset = %d, want 0 (start of the new file)", cur.ResumeOffset)
	}
}

func TestIngesterRotatedFileRemovedWhileOpen(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	r.append(r.path(), jsonLine(at(0, 1), "", 200))
	r.step(at(0, 30))
	rotated := filepath.Join(r.dir, "access-2026-11-06.log")
	if err := os.Rename(r.path(), rotated); err != nil {
		t.Fatal(err)
	}
	r.append(rotated, jsonLine(at(0, 50), "", 200))
	if err := os.Remove(rotated); err != nil {
		t.Fatal(err)
	}
	r.append(r.path(), jsonLine(at(1, 31), "", 200))
	r.step(at(1, 31))
	r.append(r.path(), jsonLine(at(2, 40), "", 200))
	r.step(at(2, 40))
	got := r.totals()
	if got[at(0, 0).Unix()] != 2 || got[at(1, 0).Unix()] != 1 {
		t.Fatalf("totals = %v", got)
	}
}

func TestIngesterMissingFile(t *testing.T) {
	r := newRig(t)
	r.step(at(1, 31))
	if !r.in.missing || len(r.totals()) != 0 {
		t.Fatalf("missing=%v totals=%v", r.in.missing, r.totals())
	}
	r.append(r.path(), jsonLine(at(0, 1), "", 200)+jsonLine(at(0, 2), "", 200)+jsonLine(at(1, 31), "", 200))
	r.step(at(1, 32))
	if got := r.totals()[at(0, 0).Unix()]; got != 2 {
		t.Fatalf("minute 10:00 = %d, want 2", got)
	}
}

func TestIngesterBadLinesAreSkipped(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	r.append(r.path(), "not json\n{\"RouterName\":\"x\"}\n\n"+jsonLine(at(0, 1), "", 200)+jsonLine(at(1, 31), "", 200))
	r.step(at(1, 31))
	if got := r.totals()[at(0, 0).Unix()]; got != 1 || r.in.badLines != 2 {
		t.Fatalf("minute = %d, bad = %d", got, r.in.badLines)
	}
}

func TestIngesterResumeIsExact(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))

	var lines []string
	for i := 0; i < 1000; i++ {
		start := at(i/200, (i%200)*60/200)
		router := ""
		if i%3 == 0 {
			router = "relist-c12-d3@docker"
		}
		lines = append(lines, jsonLine(start, router, 200))
	}
	// A reads the first half and a torn line, then is discarded as a crash.
	r.append(r.path(), strings.Join(lines[:500], "")+lines[500][:20])
	r.step(at(2, 30))
	// B resumes from the saved cursor.
	r.in = r.ingester()
	r.append(r.path(), lines[500][20:]+strings.Join(lines[501:], "")+jsonLine(at(6, 40), "", 200))
	r.step(at(6, 40))

	got := r.totals()
	var sum uint64
	for m := 0; m < 5; m++ {
		if got[at(m, 0).Unix()] != 200 {
			t.Errorf("minute %d = %d, want 200", m, got[at(m, 0).Unix()])
		}
		sum += got[at(m, 0).Unix()]
	}
	if sum != 1000 {
		t.Fatalf("sum = %d, want 1000", sum)
	}
}

func TestIngesterHourlyMaintenance(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 6, 10, 59, 0, 0, time.UTC)
	old := start.Add(-26 * time.Hour)
	if err := r.store.WriteMinutes(ctx, []Row{mkRow(total, old, 1)}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []time.Time{start.Add(-48 * time.Hour).Truncate(time.Hour), start.Add(-23 * time.Hour).Truncate(time.Hour)} {
		tx, _ := r.db.Begin()
		if err := execRow(ctx, tx, Hour.table(), mkRow(total, h, 1)); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	r.now = start
	r.append(r.path(), "")
	r.step(start)
	r.append(r.path(), jsonLine(start.Add(10*time.Second), "", 200)+jsonLine(start.Add(20*time.Second), "", 200)+
		jsonLine(start.Add(70*time.Second), "", 200)+jsonLine(start.Add(160*time.Second), "", 200))
	r.step(start.Add(160 * time.Second))

	hours, err := r.store.Rows(ctx, Hour, []Key{total}, start.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, h := range hours {
		seen = append(seen, fmt.Sprintf("%s=%d", h.BucketAt.Format("01-02T15"), h.Counts.Requests()))
	}
	want := []string{"11-05T11=1", "11-06T10=2"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("hourly = %v, want %v", seen, want)
	}
	minutes, _ := r.store.Rows(ctx, Minute, []Key{total}, old.Add(-time.Hour))
	for _, m := range minutes {
		if m.BucketAt.Equal(old) {
			t.Fatal("minute row older than 25h was kept")
		}
	}
}

func BenchmarkIngest(b *testing.B) {
	const n = 100000
	var sb strings.Builder
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		// Shaped like a real Traefik JSON line, ~600 bytes.
		fmt.Fprintf(&sb, `{"ClientAddr":"203.0.113.%d:51234","ClientHost":"203.0.113.%d","ClientPort":"51234","ClientUsername":"-","DownstreamContentSize":%d,"DownstreamStatus":%d,"Duration":%d,"OriginContentSize":%d,"OriginDuration":%d,"OriginStatus":200,"Overhead":120000,"RequestAddr":"app.example.com","RequestContentSize":0,"RequestCount":%d,"RequestHost":"app.example.com","RequestMethod":"GET","RequestPath":"/api/items/%d?page=2","RequestPort":"-","RequestProtocol":"HTTP/2.0","RequestScheme":"https","RetryAttempts":0,"RouterName":"relist-c12-d3@docker","ServiceAddr":"172.18.0.5:8080","ServiceName":"relist-c12-d3@docker","ServiceURL":{"Scheme":"http","Opaque":"","User":null,"Host":"172.18.0.5:8080","Path":"","RawPath":"","ForceQuery":false,"RawQuery":"","Fragment":"","RawFragment":""},"StartLocal":"2026-11-06T17:00:00.123456789+07:00","StartUTC":%q,"TLSCipher":"TLS_AES_128_GCM_SHA256","TLSVersion":"1.3","entryPointName":"websecure","level":"info","msg":"","time":"2026-11-06T10:00:00Z"}`+"\n",
			i%250, i%250, 1000+i%5000, 200, 1000000+(i%900)*100000, 900+i%5000, 800000, i, i,
			base.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano))
	}
	payload := sb.String()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		r := &rig{t: nil, now: base}
		db := openDBB(b)
		r.store, r.dir = NewStore(db), b.TempDir()
		hosts := NewDomainHosts(func(context.Context) ([]domains.Domain, error) {
			return []domains.Domain{{ID: 3, ComposeID: 12, Host: "a.test"}}, nil
		}, func() time.Time { return r.now })
		in := &Ingester{Dir: r.dir, Store: r.store, Hosts: hosts.Lookup, Now: func() time.Time { return r.now }}
		path := filepath.Join(r.dir, liveName)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			b.Fatal(err)
		}
		if err := in.Step(ctx); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := in.Step(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/n, "ns/line")
}

// With the log open and no traffic, the Ingester still writes a zero total
// row for each minute it watched, and advances the cursor past them.
func TestIngesterWritesQuietMinutes(t *testing.T) {
	r := newRig(t)
	r.append(r.path(), "")
	r.step(at(0, 0))
	r.step(at(5, 0))
	got := r.totals()
	for m := 0; m <= 3; m++ {
		n, ok := got[at(m, 0).Unix()]
		if !ok || n != 0 {
			t.Errorf("minute 10:0%d: present %v, requests %d; want a zero row", m, ok, n)
		}
	}
	if _, ok := got[at(4, 0).Unix()]; ok {
		t.Error("10:04 closed before it was 30 seconds past its end")
	}
	cur, ok, err := r.store.Cursor(context.Background())
	if err != nil || !ok || !cur.FlushedThrough.Equal(at(4, 0)) {
		t.Fatalf("cursor = %+v, %v, %v; want flushed through 10:04", cur, ok, err)
	}
}
