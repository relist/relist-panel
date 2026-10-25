package sources

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sshkeys"
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
	if hasGlob(filepath.Join(f.dir, ".src.new-*")) || exists(filepath.Join(f.dir, "src")) {
		t.Fatalf("directory not clean: %s", f.dirListing(t))
	}
	if m, _ := filepath.Glob(filepath.Join(f.dir, ".upload-*.zip")); len(m) != 0 {
		t.Fatalf("temp upload left behind: %v", m)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func hasGlob(pattern string) bool { m, _ := filepath.Glob(pattern); return len(m) > 0 }

// deletingGate removes the Compose row while the upload waits for the gate,
// which is a Delete landing between the stream and the swap.
type deletingGate struct {
	db      *sql.DB
	compose int64
}

func (g deletingGate) Acquire(context.Context, int64) (func(), error) {
	_, err := g.db.Exec(`DELETE FROM composes WHERE id = ?`, g.compose)
	return func() {}, err
}

func TestUploadToADeletedComposeIs404AndLeavesNothing(t *testing.T) {
	f := newHTTPFixture(t)
	f.svc.gate = deletingGate{db: f.db, compose: f.compose}
	rec := f.upload(t, map[string]string{"Dockerfile": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("upload = %d %s, want 404", rec.Code, rec.Body)
	}
	if exists(f.dir) {
		t.Fatalf("directory left behind: %s", f.dirListing(t))
	}
}

func TestPutGitOverZipRemovesTheUploadedTree(t *testing.T) {
	f := newHTTPFixture(t)
	if rec := f.upload(t, map[string]string{"Dockerfile": "x", ".git/config": "[core]"}); rec.Code != http.StatusOK {
		t.Fatalf("setup upload = %d %s", rec.Code, rec.Body)
	}
	put := `{"kind":"git","repoUrl":"git@github.com:a/b.git","branch":"main","submodules":false}`
	rec := f.serve(httptest.NewRequest(http.MethodPut, f.base, strings.NewReader(put)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if exists(filepath.Join(f.dir, "src")) {
		t.Fatalf("uploaded src/ survived the switch to git: %s", f.dirListing(t))
	}
}

func (f *httpFixture) newKey(t *testing.T, name string) sshkeys.Key {
	t.Helper()
	k, err := sshkeys.NewStore(f.db).Create(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (f *httpFixture) putSource(t *testing.T, keyID *int64, repo string) (int, map[string]any) {
	t.Helper()
	in := map[string]any{"kind": "git", "repoUrl": repo, "branch": "main", "submodules": false}
	if keyID != nil {
		in["sshKeyId"] = *keyID
	}
	raw, _ := json.Marshal(in)
	rec := f.serve(httptest.NewRequest(http.MethodPut, f.base, bytes.NewReader(raw)))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

const repoA = "git@github.com:a/b.git"

func TestPutWithKeyAndWithout(t *testing.T) {
	f := newHTTPFixture(t)
	k := f.newKey(t, "ci")
	code, body := f.putSource(t, &k.ID, repoA)
	sk, _ := body["sshKey"].(map[string]any)
	if code != http.StatusOK || sk == nil || int64(sk["id"].(float64)) != k.ID || sk["name"] != "ci" || sk["publicKey"] != k.PublicKey {
		t.Fatalf("PUT with key = %d %v", code, body)
	}
	code, body = f.putSource(t, nil, repoA)
	if v, present := body["sshKey"]; code != http.StatusOK || !present || v != nil {
		t.Fatalf("PUT without key = %d %v, want sshKey null", code, body)
	}
}

func TestPutUnknownKeyIs400(t *testing.T) {
	f := newHTTPFixture(t)
	id := int64(999)
	if code, body := f.putSource(t, &id, repoA); code != http.StatusBadRequest || body["error"] != "unknown SSH key" {
		t.Fatalf("PUT unknown key = %d %v", code, body)
	}
}

func TestChangingKeyClearsTheTest(t *testing.T) {
	f := newHTTPFixture(t)
	k1, k2 := f.newKey(t, "one"), f.newKey(t, "two")
	f.putSource(t, &k1.ID, repoA)
	if err := f.svc.store.RecordTest(t.Context(), f.compose, true, "abc123", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, body := f.putSource(t, &k1.ID, repoA); body["lastTest"] == nil || body["headSha"] != "abc123" {
		t.Fatalf("same key and URL lost the test: %v", body)
	}
	if _, body := f.putSource(t, &k2.ID, repoA); body["lastTest"] != nil || body["headSha"] != "" {
		t.Fatalf("another key kept the test: %v", body)
	}
}

func TestTestWithoutKeyIs400(t *testing.T) {
	f := newHTTPFixture(t)
	marker := filepath.Join(t.TempDir(), "called")
	bin := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.svc.git = Git{Bin: bin}
	f.putSource(t, nil, repoA)
	rec := f.serve(httptest.NewRequest(http.MethodPost, f.base+"/test", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Choose an SSH key first") {
		t.Fatalf("test = %d %s", rec.Code, rec.Body)
	}
	if exists(marker) {
		t.Fatal("git was called")
	}
}

func TestKeyRouteIsGone(t *testing.T) {
	f := newHTTPFixture(t)
	rec := f.serve(httptest.NewRequest(http.MethodPost, f.base+"/key", nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /key = %d, want 404 or 405", rec.Code)
	}
}
