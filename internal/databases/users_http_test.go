package databases

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/authz"
)

// usersRig serves the Database routes through the real authz middleware,
// with the fake Admin, so the audit rows are the ones production writes.
type usersRig struct {
	*adminRig
	h      http.Handler
	mu     sync.Mutex
	events []audit.Event
}

func newUsersRig(t *testing.T, engine, version string) (*usersRig, Database) {
	t.Helper()
	ar := newAdminRig(t)
	d := ar.create(t, "Shop", engine, version)
	ur := &usersRig{adminRig: ar}
	mux := http.NewServeMux()
	NewAPI(ar.svc).RegisterRoutes(mux)
	lookup := func(*http.Request) (auth.User, bool, error) { return auth.User{}, false, nil }
	mw := authz.New(ar.store.db, lookup).WithRecorder(func(_ context.Context, e audit.Event) error {
		ur.mu.Lock()
		defer ur.mu.Unlock()
		ur.events = append(ur.events, e)
		return nil
	}).Middleware(mux, mux)
	ur.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mw.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Email: "owner@example.com", IsAdmin: true})))
	})
	return ur, d
}

func (ur *usersRig) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	ur.h.ServeHTTP(w, req)
	ur.svc.Wait()
	return w
}

func (ur *usersRig) take() []audit.Event {
	ur.mu.Lock()
	defer ur.mu.Unlock()
	out := ur.events
	ur.events = nil
	return out
}

func TestUsersRoutesAudit(t *testing.T) {
	ur, d := newUsersRig(t, "postgres", "18")
	ctx := context.Background()
	pg := mustAccounts(t, "postgres")
	ur.admin.answer(pg.DatabasesQuery(), [][]string{{"app", "10", "app", "t"}, {"web", "20", "owner", "f"}})
	ur.admin.answer(pg.UsersQuery(), [][]string{{"app"}, {"legacy"}})
	ur.admin.pingDefault = true
	_ = ur.store.UpsertUser(ctx, d.ID, "app", "stored-secret")

	// The two reads are not audited and carry no password.
	for _, path := range []string{"/api/databases/1/logical-databases/details", "/api/databases/1/users"} {
		w := ur.do("GET", path, "")
		if w.Code != 200 || len(ur.take()) != 0 || strings.Contains(w.Body.String(), "stored-secret") || strings.Contains(strings.ToLower(w.Body.String()), "password") {
			t.Fatalf("GET %s = %d %s (events %d)", path, w.Code, w.Body, len(ur.events))
		}
	}

	cases := []struct {
		method, path, body string
		status             int
		action, key, value string
	}{
		{"POST", "/logical-databases", `{"name":"newdb","createOwner":true}`, 201, "database.logical_create", "database", "newdb"},
		{"POST", "/logical-databases/app/restrict", ``, 204, "database.logical_restrict", "database", "app"},
		{"DELETE", "/logical-databases/web", `{"confirm":"web","backupFirst":false}`, 204, "database.logical_drop", "database", "web"},
		{"POST", "/users", `{"name":"bob"}`, 201, "database.user_create", "user", "bob"},
		{"PUT", "/users/app/access", `{"access":{"web":"read"}}`, 204, "database.user_access", "user", "app"},
		{"POST", "/users/app/reset-password", ``, 204, "database.user_reset_password", "user", "app"},
		{"DELETE", "/users/app", ``, 204, "database.user_delete", "user", "app"},
		{"POST", "/reset-password", `{"confirm":"Shop"}`, 202, "database.reset_password", "", ""},
	}
	for _, c := range cases {
		w := ur.do(c.method, "/api/databases/1"+c.path, c.body)
		ev := ur.take()
		if w.Code != c.status || len(ev) != 1 {
			t.Fatalf("%s %s = %d %s (%d events)", c.method, c.path, w.Code, w.Body, len(ev))
		}
		e := ev[0]
		if e.Action != c.action || e.Target != "Shop" || (c.key != "" && e.Detail[c.key] != c.value) {
			t.Errorf("%s %s: event = %+v", c.method, c.path, e)
		}
		raw, _ := json.Marshal(e.Detail)
		if strings.Contains(strings.ToLower(string(raw)), "password") || strings.Contains(string(raw), "stored-secret") {
			t.Errorf("%s %s: the audit detail holds a password: %s", c.method, c.path, raw)
		}
	}
}

func TestCreateUserResponseHasNoPassword(t *testing.T) {
	ur, d := newUsersRig(t, "postgres", "18")
	w := ur.do("POST", "/api/databases/1/users", `{"name":"bob"}`)
	u, err := ur.store.GetUser(context.Background(), d.ID, "bob")
	if w.Code != 201 || err != nil {
		t.Fatalf("create = %d %s, %v", w.Code, w.Body, err)
	}
	if strings.Contains(w.Body.String(), u.Password) || strings.Contains(strings.ToLower(w.Body.String()), "password") {
		t.Fatalf("body = %s", w.Body)
	}
	w = ur.do("POST", "/api/databases/1/logical-databases", `{"name":"app","createOwner":true}`)
	app, _ := ur.store.GetUser(context.Background(), d.ID, "app")
	if w.Code != 201 || strings.Contains(w.Body.String(), app.Password) || app.Password == "" {
		t.Fatalf("create database = %d %s", w.Code, w.Body)
	}
}

func TestUsersRoutesJSON(t *testing.T) {
	ur, d := newUsersRig(t, "postgres", "18")
	pg := mustAccounts(t, "postgres")
	ur.admin.answer(pg.DatabasesQuery(), [][]string{{"app", "10", "app", "t"}, {"postgres", "5", "postgres", "t"}})
	ur.admin.answer(pg.UsersQuery(), [][]string{{"app"}})
	_ = ur.store.UpsertUser(context.Background(), d.ID, "app", "x")
	w := ur.do("GET", "/api/databases/1/logical-databases/details", "")
	var dbs struct {
		Databases []map[string]any `json:"databases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dbs); err != nil || len(dbs.Databases) != 2 {
		t.Fatalf("details = %s, %v", w.Body, err)
	}
	if g := dbs.Databases[0]; g["name"] != "app" || g["sizeBytes"] != float64(10) || g["owner"] != "app" || g["anyUserCanConnect"] != true || g["protected"] != false || g["charset"] != "" {
		t.Fatalf("first = %v", g)
	}
	w = ur.do("GET", "/api/databases/1/users", "")
	var us struct {
		Users []struct {
			Name    string            `json:"name"`
			Managed bool              `json:"managed"`
			Owns    []string          `json:"owns"`
			Access  map[string]string `json:"access"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &us); err != nil || len(us.Users) != 1 {
		t.Fatalf("users = %s, %v", w.Body, err)
	}
	if u := us.Users[0]; u.Name != "app" || !u.Managed || len(u.Owns) != 1 || u.Access["app"] != "full" || u.Access["postgres"] != "none" {
		t.Fatalf("user = %+v", u)
	}
}

func TestCredentialsForManagedUser(t *testing.T) {
	ur, d := newUsersRig(t, "postgres", "18")
	_ = ur.store.UpsertUser(context.Background(), d.ID, "app", "pw1")
	w := ur.do("GET", "/api/databases/1/credentials?user=app&database=app", "")
	if w.Code != 200 {
		t.Fatalf("credentials = %d %s", w.Code, w.Body)
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["url"] != "postgres://app:pw1@relist-db-1:5432/app" || got["user"] != "app" || got["password"] != "pw1" ||
		got["urlReadable"] != "postgres://app:pw1@shop.db.internal:5432/app" || got["env"] != "DATABASE_URL="+got["url"] {
		t.Fatalf("got = %v", got)
	}
	ev := ur.take()
	found := false
	for _, e := range ev {
		if e.Action == "secret.read" && e.Detail["user"] == "app" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v", ev)
	}
	// Without a user: the superuser, as before, now naming it.
	w = ur.do("GET", "/api/databases/1/credentials", "")
	got = map[string]string{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["user"] != "postgres" || got["password"] != d.Password || !strings.HasPrefix(got["url"], "postgres://postgres:") || !strings.HasSuffix(got["url"], "/postgres") {
		t.Fatalf("superuser = %v", got)
	}
	// Naming the superuser is the same.
	w = ur.do("GET", "/api/databases/1/credentials?user=postgres", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), d.Password) {
		t.Fatalf("named superuser = %d", w.Code)
	}
	// A database name outside the rule is refused before anything is read.
	if w := ur.do("GET", "/api/databases/1/credentials?user=app&database=a%2Fb", ""); w.Code != 422 {
		t.Fatalf("bad database = %d", w.Code)
	}
	ur.take()
}

func TestCredentialsUnmanagedUser(t *testing.T) {
	ur, _ := newUsersRig(t, "postgres", "18")
	w := ur.do("GET", "/api/databases/1/credentials?user=legacy", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "password is unknown") {
		t.Fatalf("= %d %s", w.Code, w.Body)
	}
	for _, e := range ur.take() {
		if e.Action == "secret.read" {
			t.Fatalf("a refused read was audited as a secret read: %+v", e)
		}
	}
}

func TestCredentialsDefaultDatabaseForMySQLUser(t *testing.T) {
	ur, d := newUsersRig(t, "mysql", "8.4")
	_ = ur.store.UpsertUser(context.Background(), d.ID, "app", "pw")
	w := ur.do("GET", "/api/databases/1/credentials?user=app", "")
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["url"] != "mysql://app:pw@relist-db-1:3306" {
		t.Fatalf("got = %v", got)
	}
}

func TestUserURLRedis(t *testing.T) {
	e := Engines["redis"]
	if got, want := e.UserURL("h", "ignored", "p", "0"), e.URL("h", "p", "0"); got != want || got != "redis://:p@h:6379/0" {
		t.Fatalf("UserURL = %s, URL = %s", got, want)
	}
	if got := Engines["postgres"].UserURL("h", "app", "p w", "db"); got != "postgres://app:p%20w@h:5432/db" {
		t.Fatalf("postgres = %s", got)
	}
	if got, want := Engines["postgres"].URL("h", "p", "d"), Engines["postgres"].UserURL("h", "postgres", "p", "d"); got != want {
		t.Fatalf("URL %s != UserURL %s", got, want)
	}
}

func TestUsersRoutesErrorCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{ErrFullOnSuperuserDB{Database: "d"}, 422},
		{ErrBadUserName, 422},
		{ErrReservedUser{Name: "root"}, 422},
		{ErrOwnerAccess{User: "u", Database: "d"}, 422},
		{ErrProtectedDatabase{Name: "postgres"}, 422},
		{ErrUserOwns{User: "u", Databases: []string{"d"}}, 409},
		{ErrUserOwnsIn{User: "u", Database: "d", Owner: "o"}, 409},
		{ErrUserExists{Name: "u"}, 409},
		{ErrDatabaseExists{Name: "d"}, 409},
		{ErrPendingPassword, 409},
		{ErrUserNotManaged, 409},
		{ErrUnknownUser{Name: "u"}, 404},
		{ErrBadAccess, 400},
		{ErrBadTarget, 400},
		{scrubErr(errors.New("role x does not exist")), 502},
	} {
		w := httptest.NewRecorder()
		writeError(w, c.err)
		if w.Code != c.code {
			t.Errorf("%v = %d, want %d", c.err, w.Code, c.code)
		}
	}
}

func TestUsersRoutesRefuseRedis(t *testing.T) {
	ur, _ := newUsersRig(t, "redis", "8")
	for _, c := range [][2]string{{"GET", "/logical-databases/details"}, {"GET", "/users"}} {
		if w := ur.do(c[0], "/api/databases/1"+c[1], ""); w.Code != 400 {
			t.Errorf("%s %s = %d", c[0], c[1], w.Code)
		}
	}
	// The superuser reset covers Redis.
	ur.admin.pingDefault = true
	if w := ur.do("POST", "/api/databases/1/reset-password", `{"confirm":"Shop"}`); w.Code != 202 {
		t.Errorf("redis reset = %d %s", w.Code, w.Body)
	}
	_ = url.Values{}
}
