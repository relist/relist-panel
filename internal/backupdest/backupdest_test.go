package backupdest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/database"
)

func TestValidateS3(t *testing.T) {
	ok := func(in S3Config) S3Config {
		t.Helper()
		in.Bucket, in.AccessKeyID = "b", "k"
		got, err := ValidateS3(in)
		if err != nil {
			t.Fatalf("ValidateS3(%+v) = %v", in, err)
		}
		return got
	}
	if got := ok(S3Config{Endpoint: "https://s3.amazonaws.com/"}); got.Endpoint != "https://s3.amazonaws.com" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if got := ok(S3Config{Endpoint: "http://minio:9000"}); got.Endpoint != "http://minio:9000" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if got := ok(S3Config{Endpoint: "http://minio:9000", Prefix: "/x/y/"}); got.Prefix != "x/y" {
		t.Fatalf("prefix = %q", got.Prefix)
	}
	for _, c := range []S3Config{
		{Endpoint: "ftp://x", Bucket: "b"},
		{Endpoint: "https://h/path", Bucket: "b"},
		{Endpoint: "https://h", Bucket: ""},
		{Endpoint: "https://h", Bucket: "a/b"},
		{Endpoint: "minio:9000", Bucket: "b"},
		{Endpoint: "https://", Bucket: "b"},
	} {
		c.AccessKeyID = "k"
		if _, err := ValidateS3(c); err == nil {
			t.Errorf("ValidateS3(%+v) accepted", c)
		}
	}
}

func TestObjectKey(t *testing.T) {
	if got := ObjectKey("", "shop", 3, "app", "f.sql.gz"); got != "shop-3/app/f.sql.gz" {
		t.Fatalf("key = %q", got)
	}
	if got := ObjectKey("b/c", "shop", 3, "my db", "f.sql.gz"); got != "b/c/shop-3/my_db/f.sql.gz" {
		t.Fatalf("key = %q", got)
	}
	if got := ObjectKey("", "shop", 3, "..", "f.sql.gz"); strings.Contains(got, "..") {
		t.Fatalf("key = %q", got)
	}
}

type fakeRemote struct {
	mu     sync.Mutex
	testFn func() error
}

func (f *fakeRemote) Put(context.Context, string, io.Reader, int64) error { return nil }
func (f *fakeRemote) Delete(context.Context, string) error                { return nil }
func (f *fakeRemote) Test(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.testFn != nil {
		return f.testFn()
	}
	return nil
}

type rig struct {
	db     *sql.DB
	svc    *Service
	remote *fakeRemote
	h      http.Handler
	mu     sync.Mutex
	events []audit.Event
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{db: db, remote: &fakeRemote{}}
	r.svc = New(NewStore(db), nil)
	r.svc.connect = func(Destination) (Remote, error) { return r.remote, nil }
	mux := http.NewServeMux()
	r.svc.RegisterRoutes(mux)
	r.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, _ := audit.Begin(req.Context(), audit.Event{}, nil, func(_ context.Context, e audit.Event) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, e)
			return nil
		})
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
	return r
}

func (r *rig) do(method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

const secret = "s3cr3t-key-value-123"

func form(endpoint, secretValue string) string {
	return `{"name":"minio","type":"s3","s3":{"endpoint":"` + endpoint + `","region":"","bucket":"backups","prefix":"p","accessKeyId":"AKIA","pathStyle":true},"secret":"` + secretValue + `"}`
}

func (r *rig) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := r.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestCreateListUpdate(t *testing.T) {
	r := newRig(t)
	if w := r.do("POST", "/api/backup-destinations", form("http://minio:9000", "")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"secret"`) {
		t.Fatalf("create without secret = %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret)); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret)); w.Code != 409 {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body)
	}
	w := r.do("GET", "/api/backup-destinations", "")
	var list []DestinationJSON
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].S3.Bucket != "backups" || list[0].S3.AccessKeyID != "AKIA" || !list[0].S3.PathStyle {
		t.Fatalf("list = %s", w.Body)
	}
}

// Review Focus 4: the stored secret is never sent to another host.
func TestUpdateEndpointNeedsSecret(t *testing.T) {
	r := newRig(t)
	r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret))
	w := r.do("PUT", "/api/backup-destinations/1", form("http://evil.example:9000", ""))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"secret"`) {
		t.Fatalf("new endpoint, blank secret = %d %s", w.Code, w.Body)
	}
	d, _ := r.svc.store.Get(context.Background(), 1)
	if d.S3.Endpoint != "http://minio:9000" || d.Secret != secret {
		t.Fatalf("refused update changed the row: %+v", d)
	}
	// The same endpoint with a blank secret keeps the stored secret.
	if w := r.do("PUT", "/api/backup-destinations/1", form("http://minio:9000", "")); w.Code != 200 {
		t.Fatalf("same endpoint = %d %s", w.Code, w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.Secret != secret {
		t.Fatalf("secret = %q, want the stored one", d.Secret)
	}
	// A new endpoint with a new secret is fine.
	if w := r.do("PUT", "/api/backup-destinations/1", form("http://other:9000", "fresh")); w.Code != 200 {
		t.Fatalf("new endpoint with a secret = %d %s", w.Code, w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.Secret != "fresh" || d.S3.Endpoint != "http://other:9000" {
		t.Fatalf("row = %+v", d)
	}
}

func TestResponsesNeverCarrySecret(t *testing.T) {
	r := newRig(t)
	r.remote.testFn = func() error { return errors.New("denied for key " + secret) }
	var bodies []string
	add := func(w *httptest.ResponseRecorder) { bodies = append(bodies, w.Body.String()) }
	add(r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret)))
	add(r.do("GET", "/api/backup-destinations", ""))
	add(r.do("PUT", "/api/backup-destinations/1", form("http://minio:9000", "")))
	add(r.do("PUT", "/api/backup-destinations/1", form("http://elsewhere:9000", "")))
	add(r.do("POST", "/api/backup-destinations/1/test", ""))
	add(r.do("GET", "/api/backup-destinations", ""))
	for i, b := range bodies {
		if strings.Contains(b, secret) {
			t.Fatalf("response %d carries the secret: %s", i, b)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if strings.Contains(strings.ToLower(e.Target), secret) || strings.Contains(jsonString(e.Detail), secret) {
			t.Fatalf("audit event carries the secret: %+v", e)
		}
	}
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTestRecordsResult(t *testing.T) {
	r := newRig(t)
	r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret))
	w := r.do("POST", "/api/backup-destinations/1/test", "")
	var got DestinationJSON
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || !got.LastTestOK || got.LastTestAt == nil || got.LastTestError != "" {
		t.Fatalf("ok test = %d %s", w.Code, w.Body)
	}
	r.remote.testFn = func() error { return errors.New("The specified bucket does not exist") }
	w = r.do("POST", "/api/backup-destinations/1/test", "")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.LastTestOK || got.LastTestError != "The specified bucket does not exist" {
		t.Fatalf("failed test = %d %s", w.Code, w.Body)
	}
	d, _ := r.svc.store.Get(context.Background(), 1)
	if d.LastTestOK || d.LastTestError == "" {
		t.Fatalf("stored = %+v", d)
	}
	// Changing the settings forgets the result.
	r.do("PUT", "/api/backup-destinations/1", strings.Replace(form("http://minio:9000", ""), `"bucket":"backups"`, `"bucket":"other"`, 1))
	if d, _ := r.svc.store.Get(context.Background(), 1); d.LastTestAt != nil || d.LastTestError != "" {
		t.Fatalf("test result kept after a change: %+v", d)
	}
}

func TestDeleteDestination(t *testing.T) {
	r := newRig(t)
	r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret))
	now := time.Now().UnixMilli()
	// A live Database "Shop" with a schedule to the destination, and a deleted
	// Database (a tombstone) holding one Remote only Backup.
	r.exec(t, `INSERT INTO databases (id, name, slug, engine, version, password, created_at, updated_at) VALUES (1, 'Shop', 'shop', 'postgres', '18', 'x', 1, 1)`)
	r.exec(t, `INSERT INTO databases (id, name, slug, engine, version, password, created_at, updated_at, deleted_at) VALUES (2, 'Old (deleted #2)', 'deleted-2', 'postgres', '18', 'x', 1, 1, 1)`)
	r.exec(t, `INSERT INTO database_backup_schedules (database_id, enabled, days, minute, all_databases, databases, destination_id) VALUES (1, 1, '["daily"]', 180, 1, '[]', 1)`)
	back := func(id, db int64, removed any) {
		r.exec(t, `INSERT INTO database_backups (id, database_id, trigger, scope, status, started_at, local_removed_at) VALUES (?, ?, 'scheduled', '{}', 'succeeded', ?, ?)`, id, db, now, removed)
	}
	up := func(backup int64) {
		r.exec(t, `INSERT INTO database_backup_uploads (backup_id, destination_id, object_key, status, uploaded_at) VALUES (?, 1, 'k', 'uploaded', ?)`, backup, now)
	}
	back(10, 1, nil) // a local file with a copy
	up(10)
	back(11, 2, now) // Remote only: its only copy is there
	up(11)
	count := func(q string) (n int) { _ = r.db.QueryRow(q).Scan(&n); return }

	w := r.do("DELETE", "/api/backup-destinations/1", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "used by the schedules of: Shop") {
		t.Fatalf("used by a schedule = %d %s", w.Code, w.Body)
	}
	r.exec(t, `UPDATE database_backup_schedules SET destination_id = NULL`)
	w = r.do("DELETE", "/api/backup-destinations/1", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"copies":2`) {
		t.Fatalf("no confirm = %d %s", w.Code, w.Body)
	}
	if w := r.do("DELETE", "/api/backup-destinations/1?confirm=1", ""); w.Code != 409 {
		t.Fatalf("wrong confirm = %d", w.Code)
	}
	var removedFiles []int64
	r.svc.removeFiles = func(id int64) error { removedFiles = append(removedFiles, id); return nil }
	if w := r.do("DELETE", "/api/backup-destinations/1?confirm=2", ""); w.Code != 204 {
		t.Fatalf("confirmed = %d %s", w.Code, w.Body)
	}
	if n := count(`SELECT COUNT(*) FROM backup_destinations`); n != 0 {
		t.Fatal("destination kept")
	}
	if n := count(`SELECT COUNT(*) FROM database_backup_uploads`); n != 0 {
		t.Fatalf("%d upload rows left", n)
	}
	if n := count(`SELECT COUNT(*) FROM database_backups WHERE id = 11`); n != 0 {
		t.Fatal("the Remote only Backup is still listed")
	}
	if n := count(`SELECT COUNT(*) FROM database_backups WHERE id = 10`); n != 1 {
		t.Fatal("a Backup with a local file was removed")
	}
	if n := count(`SELECT COUNT(*) FROM databases WHERE id = 2`); n != 0 || len(removedFiles) != 1 || removedFiles[0] != 2 {
		t.Fatalf("the emptied tombstone stays: rows=%d files=%v", n, removedFiles)
	}
	if w := r.do("DELETE", "/api/backup-destinations/1", ""); w.Code != 404 {
		t.Fatalf("again = %d", w.Code)
	}
}

func TestNoCopiesNeedsNoConfirm(t *testing.T) {
	r := newRig(t)
	r.do("POST", "/api/backup-destinations", form("http://minio:9000", secret))
	if w := r.do("DELETE", "/api/backup-destinations/1", ""); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
}
