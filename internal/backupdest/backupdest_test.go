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
	seen   string
}

func (f *fakeRemote) SeenHostKey() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen
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
		ctx, p := audit.Begin(req.Context(), audit.Event{}, nil, func(_ context.Context, e audit.Event) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, e)
			return nil
		})
		mux.ServeHTTP(w, req.WithContext(ctx))
		// What the request ends up recording: its target and details.
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, p.Event())
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

func TestValidateSFTP(t *testing.T) {
	ok := func(in SFTPConfig) SFTPConfig {
		t.Helper()
		if in.Host == "" {
			in.Host = "box"
		}
		if in.User == "" {
			in.User = "u"
		}
		if in.Auth == "" {
			in.Auth = AuthKey
		}
		got, err := ValidateSFTP(in)
		if err != nil {
			t.Fatalf("ValidateSFTP(%+v) = %v", in, err)
		}
		return got
	}
	if got := ok(SFTPConfig{Host: " box.example "}); got.Host != "box.example" || got.Port != 22 {
		t.Fatalf("got %+v", got)
	}
	if got := ok(SFTPConfig{Host: "::1"}); got.Host != "::1" {
		t.Fatalf("ipv6 = %+v", got)
	}
	for in, want := range map[string]string{"backups/": "backups", "/srv//b/": "/srv/b", ".": "", "": "", " /x ": "/x"} {
		if got := ok(SFTPConfig{Path: in}); got.Path != want {
			t.Errorf("path %q = %q, want %q", in, got.Path, want)
		}
	}
	base := SFTPConfig{Host: "box", User: "u", Auth: AuthKey}
	for field, mut := range map[string]func(*SFTPConfig){
		"path": func(c *SFTPConfig) { c.Path = "/" },
		"host": func(c *SFTPConfig) { c.Host = "" },
		"port": func(c *SFTPConfig) { c.Port = 70000 },
		"user": func(c *SFTPConfig) { c.User = "" },
		"auth": func(c *SFTPConfig) { c.Auth = "cert" },
	} {
		c := base
		mut(&c)
		_, err := ValidateSFTP(c)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
	for _, p := range []string{"a/../../x", "../x", "a/.."} {
		c := base
		c.Path = p
		if _, err := ValidateSFTP(c); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	for _, h := range []string{"a b", "a/b", "a\tb"} {
		c := base
		c.Host = h
		if _, err := ValidateSFTP(c); err == nil {
			t.Errorf("host %q accepted", h)
		}
	}
	c := base
	c.User = "a b"
	if _, err := ValidateSFTP(c); err == nil {
		t.Error("user with space accepted")
	}
	c = base
	c.Path = "/"
	if _, err := ValidateSFTP(c); err == nil || !strings.Contains(err.Error(), "base path cannot be /") {
		t.Errorf("slash error = %v", err)
	}
	c.Path = "a/../../x"
	if _, err := ValidateSFTP(c); err == nil || !strings.Contains(err.Error(), "base path cannot contain ..") {
		t.Errorf("dotdot error = %v", err)
	}
}

func TestKeyPrefix(t *testing.T) {
	if got := (Destination{Type: TypeS3, S3: S3Config{Prefix: "p"}}).KeyPrefix(); got != "p" {
		t.Fatalf("s3 = %q", got)
	}
	if got := (Destination{Type: TypeSFTP, SFTP: SFTPConfig{Path: "/srv/b"}}).KeyPrefix(); got != "/srv/b" {
		t.Fatalf("sftp = %q", got)
	}
	if got := ObjectKey("/srv/b", "app", 3, "main", "x.sql.gz"); got != "/srv/b/app-3/main/x.sql.gz" {
		t.Fatalf("key = %q", got)
	}
}

const sshPriv = "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-private-material\n-----END OPENSSH PRIVATE KEY-----\n"
const sshPub = "ssh-ed25519 AAAAC3fakepub relist-deploy"

func (r *rig) addKey(t *testing.T) {
	t.Helper()
	r.exec(t, `INSERT INTO ssh_keys (id, name, private_key, public_key, created_at) VALUES (1, 'deploy', ?, ?, 1)`, sshPriv, sshPub)
}

func sftpForm(host, auth, path, secretValue string) string {
	key := "null"
	if auth == "key" {
		key = "1"
	}
	return `{"name":"box","type":"sftp","sftp":{"host":"` + host + `","port":22,"user":"u","path":"` + path + `","auth":"` + auth + `","sshKeyId":` + key + `},"secret":"` + secretValue + `"}`
}

func TestCreateSFTPWithKey(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	w := r.do("POST", "/api/backup-destinations", sftpForm("box", "key", "/b", ""))
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var got DestinationJSON
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.SFTP == nil || got.SFTP.PublicKey != sshPub || got.SFTP.HostKey != "" || got.SFTP.SSHKeyName != "deploy" || got.SFTP.Port != 22 {
		t.Fatalf("body = %s", w.Body)
	}
	if strings.Contains(w.Body.String(), `"s3"`) {
		t.Fatalf("body has s3: %s", w.Body)
	}
	if w := r.do("POST", "/api/backup-destinations", strings.Replace(sftpForm("box2", "key", "", ""), `"sshKeyId":1`, `"sshKeyId":null`, 1)); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"sshKeyId"`) || !strings.Contains(w.Body.String(), "choose an SSH key") {
		t.Fatalf("no key = %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/backup-destinations", strings.Replace(sftpForm("box3", "key", "", ""), `"sshKeyId":1`, `"sshKeyId":99`, 1)); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"sshKeyId"`) {
		t.Fatalf("missing key = %d %s", w.Code, w.Body)
	}
}

func TestSFTPPasswordRules(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	if w := r.do("POST", "/api/backup-destinations", sftpForm("box", "password", "/b", "")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"secret"`) {
		t.Fatalf("create without password = %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/api/backup-destinations", sftpForm("box", "password", "/b", "pw-123")); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	stored := func() Destination { d, _ := r.svc.store.Get(context.Background(), 1); return d }
	if w := r.do("PUT", "/api/backup-destinations/1", sftpForm("box", "password", "/other", "")); w.Code != 200 || stored().Secret != "pw-123" || stored().SFTP.Path != "/other" {
		t.Fatalf("path edit = %d %s secret=%q", w.Code, w.Body, stored().Secret)
	}
	if w := r.do("PUT", "/api/backup-destinations/1", sftpForm("elsewhere", "password", "/other", "")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"secret"`) || !strings.Contains(w.Body.String(), "enter the password again") {
		t.Fatalf("host edit = %d %s", w.Code, w.Body)
	}
	if stored().SFTP.Host != "box" || stored().Secret != "pw-123" {
		t.Fatalf("a refused edit changed the row: %+v", stored())
	}
	// Key to password also asks again.
	r.do("POST", "/api/backup-destinations", strings.Replace(sftpForm("k", "key", "", ""), `"name":"box"`, `"name":"k"`, 1))
	if w := r.do("PUT", "/api/backup-destinations/2", strings.Replace(sftpForm("k", "password", "", ""), `"name":"box"`, `"name":"k"`, 1)); w.Code != 422 || !strings.Contains(w.Body.String(), "enter the password again") {
		t.Fatalf("key to password = %d %s", w.Code, w.Body)
	}
	if w := r.do("PUT", "/api/backup-destinations/1", sftpForm("box", "key", "/other", "")); w.Code != 200 || stored().Secret != "" {
		t.Fatalf("to key auth = %d %s secret=%q", w.Code, w.Body, stored().Secret)
	}
}

func TestTypeCannotChange(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	r.do("POST", "/api/backup-destinations", sftpForm("box", "key", "", ""))
	w := r.do("PUT", "/api/backup-destinations/1", form("http://minio:9000", secret))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"type"`) || !strings.Contains(w.Body.String(), "the type of a Backup destination cannot change") {
		t.Fatalf("= %d %s", w.Code, w.Body)
	}
}

func TestTestRecordsHostKeyOnlyOnSuccess(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	r.do("POST", "/api/backup-destinations", sftpForm("box", "key", "", ""))
	line := hostKeyLine(newSigner(t, "ed25519"))
	r.remote.seen = line
	r.remote.testFn = func() error { return errors.New("login refused") }
	r.do("POST", "/api/backup-destinations/1/test", "")
	if d, _ := r.svc.store.Get(context.Background(), 1); d.HostKey != "" {
		t.Fatalf("a failed Test recorded %q", d.HostKey)
	}
	r.remote.testFn = nil
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
	w := r.do("POST", "/api/backup-destinations/1/test", "")
	var got DestinationJSON
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.SFTP == nil || !strings.HasPrefix(got.SFTP.HostKey, "ssh-ed25519 SHA256:") || !got.LastTestOK {
		t.Fatalf("body = %s", w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.HostKey != line {
		t.Fatalf("stored %q", d.HostKey)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for _, e := range r.events {
		if v, ok := e.Detail["hostKey"]; ok && v == got.SFTP.HostKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("no hostKey detail in %+v", r.events)
	}
}

func TestResetHostKeyRoute(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	r.do("POST", "/api/backup-destinations", sftpForm("box", "key", "", ""))
	r.remote.seen = hostKeyLine(newSigner(t, "ed25519"))
	r.do("POST", "/api/backup-destinations/1/test", "")
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
	w := r.do("POST", "/api/backup-destinations/1/reset-host-key", "")
	var got struct {
		DestinationJSON
		LastTestAt *time.Time `json:"lastTestAt"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.SFTP == nil || got.SFTP.HostKey != "" || got.LastTestAt != nil || !strings.Contains(w.Body.String(), `"lastTestAt":null`) {
		t.Fatalf("= %d %s", w.Code, w.Body)
	}
	r.mu.Lock()
	n, target := len(r.events), ""
	if n > 0 {
		target = r.events[0].Target
	}
	r.mu.Unlock()
	if n != 1 || target != "box" {
		t.Fatalf("events = %d, target %q", n, target)
	}
	if w := r.do("POST", "/api/backup-destinations/9/reset-host-key", ""); w.Code != 404 {
		t.Fatalf("missing = %d", w.Code)
	}
}

// Review Focus 3: a tested destination moved to another host forgets its key.
func TestChangingHostForgetsHostKey(t *testing.T) {
	r := newRig(t)
	r.do("POST", "/api/backup-destinations", sftpForm("box", "password", "/b", "pw-123"))
	r.remote.seen = hostKeyLine(newSigner(t, "ed25519"))
	r.do("POST", "/api/backup-destinations/1/test", "")
	if d, _ := r.svc.store.Get(context.Background(), 1); d.HostKey == "" || !d.LastTestOK {
		t.Fatalf("not trusted: %+v", d)
	}
	if w := r.do("PUT", "/api/backup-destinations/1", sftpForm("moved", "password", "/b", "pw-456")); w.Code != 200 {
		t.Fatalf("move = %d %s", w.Code, w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.HostKey != "" || d.LastTestOK {
		t.Fatalf("after the move: %+v", d)
	}
}

func TestSFTPResponsesNeverCarrySecrets(t *testing.T) {
	r := newRig(t)
	r.addKey(t)
	const pw = "sftp-pw-very-secret-1"
	r.remote.testFn = func() error { return errors.New("denied for " + pw) }
	var bodies []string
	add := func(w *httptest.ResponseRecorder) { bodies = append(bodies, w.Body.String()) }
	add(r.do("POST", "/api/backup-destinations", sftpForm("box", "password", "/b", pw)))
	add(r.do("POST", "/api/backup-destinations", strings.Replace(sftpForm("k", "key", "", ""), `"name":"box"`, `"name":"k"`, 1)))
	add(r.do("GET", "/api/backup-destinations", ""))
	add(r.do("PUT", "/api/backup-destinations/1", sftpForm("box", "password", "/c", "")))
	add(r.do("POST", "/api/backup-destinations/1/test", ""))
	r.remote.testFn = func() error { return errors.New("denied") }
	add(r.do("POST", "/api/backup-destinations/2/test", ""))
	add(r.do("POST", "/api/backup-destinations/1/reset-host-key", ""))
	add(r.do("GET", "/api/backup-destinations", ""))
	for i, b := range bodies {
		if strings.Contains(b, pw) || strings.Contains(b, "BEGIN OPENSSH PRIVATE KEY") || strings.Contains(b, "fake-private-material") {
			t.Fatalf("response %d carries a secret: %s", i, b)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if j := jsonString(e); strings.Contains(j, pw) || strings.Contains(j, "fake-private-material") {
			t.Fatalf("audit event carries a secret: %+v", e)
		}
	}
}

func TestEncryptPassphraseRoundTrip(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewStore(db)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	d, err := s.Create(ctx, Destination{Name: "minio", Type: TypeS3,
		S3:     S3Config{Endpoint: "http://minio:9000", Bucket: "b", AccessKeyID: "AKIA"},
		Secret: "sec", Encrypt: true, Passphrase: "p-0123456789-0123456789"}, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Encrypt || got.Passphrase != "p-0123456789-0123456789" {
		t.Fatalf("got = encrypt %v passphrase %q", got.Encrypt, got.Passphrase)
	}
	// An empty Passphrase on Update keeps the stored one, like Secret.
	got.Passphrase = ""
	if err := s.Update(ctx, got, now); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, d.ID); !got.Encrypt || got.Passphrase != "p-0123456789-0123456789" {
		t.Fatalf("after keep-update = encrypt %v passphrase %q", got.Encrypt, got.Passphrase)
	}
	// Turning encryption off clears the stored passphrase.
	got.Encrypt = false
	if err := s.Update(ctx, got, now); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, d.ID); got.Encrypt || got.Passphrase != "" {
		t.Fatalf("after off-update = encrypt %v passphrase %q", got.Encrypt, got.Passphrase)
	}
}

const passphrase23 = "p-0123456789-012345678x"

func encForm(encrypt bool, pass string) string {
	return `{"name":"vault","type":"s3","s3":{"endpoint":"http://minio:9000","region":"","bucket":"backups","prefix":"p","accessKeyId":"AKIA","pathStyle":true},"secret":"` + secret + `","encrypt":` + map[bool]string{true: "true", false: "false"}[encrypt] + `,"passphrase":"` + pass + `"}`
}

func TestEncryptionAPI(t *testing.T) {
	r := newRig(t)
	// A short passphrase is refused by field.
	if w := r.do("POST", "/api/backup-destinations", encForm(true, "short-pass")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"passphrase"`) {
		t.Fatalf("short passphrase = %d %s", w.Code, w.Body)
	}
	// Encryption without a passphrase is refused too.
	if w := r.do("POST", "/api/backup-destinations", encForm(true, "")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"passphrase"`) {
		t.Fatalf("missing passphrase = %d %s", w.Code, w.Body)
	}
	w := r.do("POST", "/api/backup-destinations", encForm(true, passphrase23))
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"encrypt":true`) {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), passphrase23) {
		t.Fatalf("create response carries the passphrase: %s", w.Body)
	}
	// Reveal answers the passphrase and audits it as a secret read.
	w = r.do("GET", "/api/backup-destinations/1/passphrase", "")
	var rev struct {
		Passphrase string `json:"passphrase"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rev)
	if w.Code != 200 || rev.Passphrase != passphrase23 {
		t.Fatalf("reveal = %d", w.Code)
	}
	r.mu.Lock()
	seen := false
	for _, e := range r.events {
		if e.Action == "secret.read" {
			seen = true
		}
	}
	r.mu.Unlock()
	if !seen {
		t.Fatal("no secret.read audit event for the reveal")
	}
	// An update with a blank passphrase keeps the stored one.
	if w := r.do("PUT", "/api/backup-destinations/1", encForm(true, "")); w.Code != 200 {
		t.Fatalf("keep update = %d %s", w.Code, w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.Passphrase != passphrase23 {
		t.Fatal("stored passphrase lost on a keep update")
	}
	// A short new passphrase on update is refused.
	if w := r.do("PUT", "/api/backup-destinations/1", encForm(true, "short-pass")); w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"passphrase"`) {
		t.Fatalf("short update = %d %s", w.Code, w.Body)
	}
	// A test failure naming the passphrase never reaches the browser.
	r.remote.testFn = func() error { return errors.New("denied for " + passphrase23) }
	if w := r.do("POST", "/api/backup-destinations/1/test", ""); strings.Contains(w.Body.String(), passphrase23) {
		t.Fatalf("test response carries the passphrase: %s", w.Body)
	}
	// Turning encryption off forgets the passphrase; the reveal then refuses.
	if w := r.do("PUT", "/api/backup-destinations/1", encForm(false, "")); w.Code != 200 {
		t.Fatalf("off update = %d %s", w.Code, w.Body)
	}
	w = r.do("GET", "/api/backup-destinations/1/passphrase", "")
	if w.Code != 422 || !strings.Contains(w.Body.String(), "does not encrypt") {
		t.Fatalf("reveal on plain = %d %s", w.Code, w.Body)
	}
}

// Review: turning encryption on over the API without any passphrase, stored
// or sent, must refuse — the row would otherwise encrypt with an empty passphrase and every
// copy would fail.
func TestEncryptOnNeedsAPassphrase(t *testing.T) {
	r := newRig(t)
	if w := r.do("POST", "/api/backup-destinations", encForm(false, "")); w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	w := r.do("PUT", "/api/backup-destinations/1", encForm(true, ""))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"passphrase"`) {
		t.Fatalf("encrypt on without passphrase = %d %s", w.Code, w.Body)
	}
	if d, _ := r.svc.store.Get(context.Background(), 1); d.Encrypt {
		t.Fatalf("refused update changed the row: %+v", d)
	}
}
