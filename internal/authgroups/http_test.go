package authgroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"relist-panel/internal/authz"
	"relist-panel/internal/database"
	"relist-panel/internal/grants"
)

type testService struct {
	mux     *http.ServeMux
	db      *sql.DB
	store   *Store
	dataDir string
}

func newService(t *testing.T) testService {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dataDir := t.TempDir()
	s := New(db, dataDir)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return testService{mux: mux, db: db, store: s.store, dataDir: dataDir}
}

func (ts testService) doCtx(ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
	var raw string
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(raw)).WithContext(ctx)
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	return rec
}

func (ts testService) do(method, path string, body any) *httptest.ResponseRecorder {
	return ts.doCtx(context.Background(), method, path, body)
}

func (ts testService) file(id int64) string {
	return filepath.Join(ts.dataDir, ".traefik", "dynamic", "auth-"+strconv.FormatInt(id, 10)+".yml")
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func (ts testService) createGroup(t *testing.T) int64 {
	t.Helper()
	rec := ts.do("POST", "/api/auth-groups", map[string]any{"name": "Staging", "users": []map[string]string{{"username": "alice", "password": "password-one"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	return int64(decode(t, rec)["id"].(float64))
}

func assertNoSecrets(t *testing.T, body string) {
	t.Helper()
	for _, bad := range []string{"password", "hash", "$2a$", "password-one"} {
		if strings.Contains(body, bad) {
			t.Errorf("response carries %q: %s", bad, body)
		}
	}
}

func TestCreateGroupWithFirstUser(t *testing.T) {
	ts := newService(t)
	rec := ts.do("POST", "/api/auth-groups", map[string]any{"name": " Staging ", "users": []map[string]string{{"username": "alice", "password": "password-one"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	assertNoSecrets(t, rec.Body.String())
	got := decode(t, rec)
	if got["name"] != "Staging" || got["userCount"].(float64) != 1 {
		t.Errorf("body = %v", got)
	}
	if _, err := os.Stat(ts.file(int64(got["id"].(float64)))); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

func TestFieldErrors(t *testing.T) {
	ts := newService(t)
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		code   int
		field  string
	}{
		{"bad name", "POST", "/api/auth-groups", map[string]any{"name": ""}, 400, "name"},
		{"short password", "POST", "/api/auth-groups", map[string]any{"name": "x", "users": []map[string]string{{"username": "a", "password": "1234567"}}}, 400, "password"},
		{"bad username", "POST", "/api/auth-groups", map[string]any{"name": "x", "users": []map[string]string{{"username": "a:b", "password": "12345678"}}}, 400, "username"},
		{"duplicate username in body", "POST", "/api/auth-groups", map[string]any{"name": "x", "users": []map[string]string{{"username": "a", "password": "12345678"}, {"username": "a", "password": "12345678"}}}, 409, "username"},
	}
	for _, c := range cases {
		rec := ts.do(c.method, c.path, c.body)
		got := decode(t, rec)
		if rec.Code != c.code || got["field"] != c.field {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	id := ts.createGroup(t)
	base := "/api/auth-groups/" + strconv.FormatInt(id, 10)
	if rec := ts.do("POST", base+"/users", map[string]string{"username": "alice", "password": "password-two"}); rec.Code != 409 || decode(t, rec)["field"] != "username" {
		t.Errorf("duplicate username: %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do("POST", base+"/users", map[string]string{"username": "bob", "password": "1234567"}); rec.Code != 400 || decode(t, rec)["field"] != "password" {
		t.Errorf("short password: %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do("POST", "/api/auth-groups", map[string]any{"name": "staging"}); rec.Code != 409 || decode(t, rec)["field"] != "name" {
		t.Errorf("duplicate name: %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do("GET", "/api/auth-groups/999", nil); rec.Code != 404 {
		t.Errorf("missing group = %d", rec.Code)
	}
}

func TestGroupDetail(t *testing.T) {
	ts := newService(t)
	id := ts.createGroup(t)
	rec := ts.do("GET", "/api/auth-groups/"+strconv.FormatInt(id, 10), nil)
	if rec.Code != 200 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	assertNoSecrets(t, rec.Body.String())
	got := decode(t, rec)
	users := got["users"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["username"] != "alice" {
		t.Errorf("users = %v", users)
	}
	if domains, ok := got["domains"].([]any); !ok || len(domains) != 0 {
		t.Errorf("domains = %v", got["domains"])
	}
	list := ts.do("GET", "/api/auth-groups", nil)
	assertNoSecrets(t, list.Body.String())
}

func firstUserID(t *testing.T, ts testService, gid int64) int64 {
	t.Helper()
	users, err := ts.store.Users(context.Background(), gid)
	if err != nil || len(users) == 0 {
		t.Fatalf("Users = %v, %v", users, err)
	}
	return users[0].ID
}

func TestRevealPassword(t *testing.T) {
	ts := newService(t)
	gid := ts.createGroup(t)
	uid := firstUserID(t, ts, gid)
	rec := ts.do("GET", "/api/auth-groups/"+strconv.FormatInt(gid, 10)+"/users/"+strconv.FormatInt(uid, 10)+"/password", nil)
	if rec.Code != 200 || decode(t, rec)["password"] != "password-one" {
		t.Errorf("reveal = %d %s", rec.Code, rec.Body)
	}
}

func TestPatchUserWithoutPasswordKeepsIt(t *testing.T) {
	ts := newService(t)
	gid := ts.createGroup(t)
	uid := firstUserID(t, ts, gid)
	url := "/api/auth-groups/" + strconv.FormatInt(gid, 10) + "/users/" + strconv.FormatInt(uid, 10)
	rec := ts.do("PATCH", url, map[string]string{"username": "al"})
	if rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	assertNoSecrets(t, rec.Body.String())
	if rec := ts.do("GET", url+"/password", nil); decode(t, rec)["password"] != "password-one" {
		t.Errorf("reveal = %s", rec.Body)
	}
	body, _ := os.ReadFile(ts.file(gid))
	if !strings.Contains(string(body), "al:$2") {
		t.Errorf("file does not carry the new username:\n%s", body)
	}
	rec = ts.do("PATCH", url, map[string]string{"password": "password-two"})
	if rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do("GET", url+"/password", nil); decode(t, rec)["password"] != "password-two" {
		t.Errorf("reveal = %s", rec.Body)
	}
	if rec := ts.do("DELETE", url, nil); rec.Code != 204 {
		t.Errorf("delete user = %d", rec.Code)
	}
}

func useGroup(t *testing.T, ts testService, gid int64) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'Shop', '', 1, 1)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (2, 1, 'prod', 1)`,
		`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (3, 2, 'web', '', 1, 1)`,
	} {
		if _, err := ts.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ts.db.Exec(`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at, auth_group_id) VALUES (3, 'a.example.com', 'web', 80, 1, 1, ?)`, gid); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteInUse(t *testing.T) {
	ts := newService(t)
	gid := ts.createGroup(t)
	useGroup(t, ts, gid)
	rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil)
	if rec.Code != 409 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	domains := decode(t, rec)["domains"].([]any)
	if len(domains) != 1 || domains[0].(map[string]any)["host"] != "a.example.com" {
		t.Errorf("domains = %v", domains)
	}
	if _, err := os.Stat(ts.file(gid)); err != nil {
		t.Errorf("file removed: %v", err)
	}
	detail := decode(t, ts.do("GET", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil))
	if d := detail["domains"].([]any); len(d) != 1 || d[0].(map[string]any)["composeName"] != "web" {
		t.Errorf("detail domains = %v", d)
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	ts := newService(t)
	gid := ts.createGroup(t)
	rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil)
	if rec.Code != 204 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(ts.file(gid)); !os.IsNotExist(err) {
		t.Errorf("file kept: %v", err)
	}
}

func TestRenameUpdatesRealm(t *testing.T) {
	ts := newService(t)
	gid := ts.createGroup(t)
	rec := ts.do("PATCH", "/api/auth-groups/"+strconv.FormatInt(gid, 10), map[string]string{"name": "Prod"})
	if rec.Code != 200 || decode(t, rec)["name"] != "Prod" {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	body, _ := os.ReadFile(ts.file(gid))
	if !strings.Contains(string(body), "realm: Prod") {
		t.Errorf("file:\n%s", body)
	}
}

func TestOptionsForMember(t *testing.T) {
	ts := newService(t)
	ts.createGroup(t)
	for _, q := range []string{
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'Shop', '', 1, 1)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (2, 1, 'prod', 1), (3, 1, 'dev', 1)`,
		`INSERT INTO users (id, email, is_admin, created_at) VALUES (10, 'e@x.io', 0, 1), (11, 'v@x.io', 0, 1)`,
		`INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (10, 1, 2, 'editor'), (11, 1, 2, 'viewer'), (11, 1, 3, 'operator')`,
	} {
		if _, err := ts.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	as := func(id int64) context.Context {
		access, err := grants.Load(context.Background(), ts.db, id, false)
		if err != nil {
			t.Fatal(err)
		}
		return authz.WithAccess(context.Background(), access)
	}
	var opts []map[string]any
	rec := ts.doCtx(as(10), "GET", "/api/auth-group-options", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil || len(opts) != 1 || opts[0]["name"] != "Staging" {
		t.Errorf("editor options = %s (%v)", rec.Body, err)
	}
	if strings.Contains(rec.Body.String(), "user") {
		t.Errorf("options carry users: %s", rec.Body)
	}
	rec = ts.doCtx(as(11), "GET", "/api/auth-group-options", nil)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("viewer/operator options = %s", rec.Body)
	}
	rec = ts.doCtx(authz.WithAccess(context.Background(), grants.Access{Admin: true}), "GET", "/api/auth-group-options", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil || len(opts) != 1 {
		t.Errorf("admin options = %s", rec.Body)
	}
}

func newServiceWithRunning(t *testing.T, running RunningRefs) testService {
	t.Helper()
	ts := newService(t)
	s := New(ts.db, ts.dataDir).WithRunning(running)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	ts.mux = mux
	return ts
}

// A Domain switched to None but not redeployed still has a running Container
// whose router names the group's middleware; deleting the group would make
// Traefik drop that router.
func TestDeleteRefusedWhileRunningContainerUsesIt(t *testing.T) {
	var asked string
	ts := newServiceWithRunning(t, func(_ context.Context, middleware string) ([]string, error) {
		asked = middleware
		return []string{"relist-c1-web-1"}, nil
	})
	gid := ts.createGroup(t)
	rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil)
	if rec.Code != 409 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	if want := "relist-auth-" + strconv.FormatInt(gid, 10) + "@file"; asked != want {
		t.Errorf("asked about %q, want %q", asked, want)
	}
	if msg, _ := decode(t, rec)["error"].(string); !strings.Contains(msg, "relist-c1-web-1") || !strings.Contains(msg, "Redeploy") {
		t.Errorf("error = %q", msg)
	}
	if _, err := ts.store.Get(context.Background(), gid); err != nil {
		t.Errorf("group deleted: %v", err)
	}
	if _, err := os.Stat(ts.file(gid)); err != nil {
		t.Errorf("file removed: %v", err)
	}
}

func TestDeleteRefusedWhenRunningCheckFails(t *testing.T) {
	ts := newServiceWithRunning(t, func(context.Context, string) ([]string, error) {
		return nil, errors.New("docker: cannot connect")
	})
	gid := ts.createGroup(t)
	rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil)
	if rec.Code != 503 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	if _, err := ts.store.Get(context.Background(), gid); err != nil {
		t.Errorf("group deleted: %v", err)
	}
}

func TestDeleteAllowedWhenNoRunningContainerUsesIt(t *testing.T) {
	ts := newServiceWithRunning(t, func(context.Context, string) ([]string, error) { return nil, nil })
	gid := ts.createGroup(t)
	if rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil); rec.Code != 204 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
}

// A group a Domain still uses is refused with those Domains, even though its
// running Container also routes through it: picking another group on the
// Domain is the fix, redeploying alone is not.
func TestDeleteInUseListsDomainsBeforeRunningContainers(t *testing.T) {
	ts := newServiceWithRunning(t, func(context.Context, string) ([]string, error) {
		return []string{"relist-c1-web-1"}, nil
	})
	gid := ts.createGroup(t)
	useGroup(t, ts, gid)
	rec := ts.do("DELETE", "/api/auth-groups/"+strconv.FormatInt(gid, 10), nil)
	if rec.Code != 409 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	body := decode(t, rec)
	if domains := body["domains"].([]any); len(domains) != 1 {
		t.Errorf("domains = %v", domains)
	}
	if msg, _ := body["error"].(string); strings.Contains(msg, "Redeploy") {
		t.Errorf("error = %q", msg)
	}
}
