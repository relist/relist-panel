package sources

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
)

type stubLocker struct {
	mu     sync.Mutex
	refuse bool
	held   int
}

func (l *stubLocker) TryLockSource(int64) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refuse {
		return nil, false
	}
	l.held++
	return func() { l.mu.Lock(); l.held--; l.mu.Unlock() }, true
}

func (l *stubLocker) setRefuse(v bool) { l.mu.Lock(); l.refuse = v; l.mu.Unlock() }

type httpFixture struct {
	svc     *Service
	locker  *stubLocker
	mux     *http.ServeMux
	base    string
	dir     string
	db      *sql.DB
	compose int64
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	db, a, _ := newDB(t)
	var p, e int64
	if err := db.QueryRow(`SELECT e.project_id, e.id FROM environments e JOIN composes c ON c.environment_id = e.id WHERE c.id = ?`, a).Scan(&p, &e); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cs := composes.New(db, nil, dataDir)
	locker := &stubLocker{}
	svc := New(db, cs, Git{}, locker, openGate{})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	target, err := cs.Target(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	return &httpFixture{svc: svc, locker: locker, mux: mux, db: db, compose: a, dir: composes.ComposeDir(target.Request),
		base: fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d/source", p, e, a)}
}

type openGate struct{}

func (openGate) Acquire(_ context.Context, _ int64) (func(), error) { return func() {}, nil }

func (f *httpFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *httpFixture) upload(t *testing.T, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "a.zip")
	_, _ = part.Write(zbuf.Bytes())
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, f.base+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return f.serve(req)
}

func TestUploadOverTheCapIs413AndCleansUp(t *testing.T) {
	f := newHTTPFixture(t)
	f.svc.maxUpload = 200
	rec := f.upload(t, map[string]string{"Dockerfile": strings.Repeat("x", 4096)})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d %s, want 413", rec.Code, rec.Body)
	}
	if m, _ := filepath.Glob(filepath.Join(f.dir, ".upload-*.zip")); len(m) != 0 {
		t.Fatalf("temp upload left behind: %v", m)
	}
	if f.locker.held != 0 {
		t.Fatalf("source lock still held: %d", f.locker.held)
	}
}

func TestWritesAnswer409WhenTheSourceIsLocked(t *testing.T) {
	f := newHTTPFixture(t)
	if rec := f.upload(t, map[string]string{"Dockerfile": "x"}); rec.Code != http.StatusOK {
		t.Fatalf("setup upload = %d %s", rec.Code, rec.Body)
	}
	f.locker.setRefuse(true)
	put := `{"kind":"git","repoUrl":"git@github.com:a/b.git","branch":"","submodules":false}`
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"PUT", http.MethodPut, f.base, put},
		{"DELETE", http.MethodDelete, f.base, ""},
		{"regenerate", http.MethodPost, f.base + "/key", ""},
		{"known-hosts reset", http.MethodPost, f.base + "/known-hosts/reset", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		rec := f.serve(req)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "A deployment is running") {
			t.Errorf("%s = %d %s, want 409 A deployment is running", c.name, rec.Code, rec.Body)
		}
	}
	before := f.dirListing(t)
	rec := f.upload(t, map[string]string{"Other": "y"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "A deployment is running") {
		t.Errorf("upload = %d %s, want 409", rec.Code, rec.Body)
	}
	if after := f.dirListing(t); after != before {
		t.Errorf("a refused upload changed the directory: %q -> %q", before, after)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "src", "Dockerfile")); err != nil {
		t.Errorf("src/ changed: %v", err)
	}
}

func (f *httpFixture) dirListing(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// lockAfterFirst grants the first TryLockSource and refuses the rest, which is
// a Deployment starting while the upload extracts.
type lockAfterFirst struct {
	mu    sync.Mutex
	calls int
}

func (l *lockAfterFirst) TryLockSource(int64) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls > 1 {
		return nil, false
	}
	return func() {}, true
}

func TestUploadRechecksTheLockBeforeTheSwapAndCleansUp(t *testing.T) {
	f := newHTTPFixture(t)
	f.svc.locker = &lockAfterFirst{}
	rec := f.upload(t, map[string]string{"Dockerfile": "x"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "A deployment is running") {
		t.Fatalf("upload = %d %s, want 409", rec.Code, rec.Body)
	}
	if exists(filepath.Join(f.dir, ".src.new")) || exists(filepath.Join(f.dir, "src")) {
		t.Fatalf("directory not clean: %s", f.dirListing(t))
	}
	if m, _ := filepath.Glob(filepath.Join(f.dir, ".upload-*.zip")); len(m) != 0 {
		t.Fatalf("temp upload left behind: %v", m)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }
