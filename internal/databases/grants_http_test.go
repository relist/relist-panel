package databases

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"relist-panel/internal/auth"
	"relist-panel/internal/authz"
	"relist-panel/internal/dbquery"
	"relist-panel/internal/grants"
)

// granteeCtx is a request context the way the middleware builds it for a
// grantee on a LevelDatabase route.
func granteeCtx(user auth.User, g authz.DatabaseGrant) context.Context {
	ctx := auth.WithUser(context.Background(), user)
	ctx = authz.WithAccess(ctx, grants.Access{})
	return authz.WithDatabaseGrant(ctx, g)
}

// memberCtx is the same without a grant: a non-Admin the middleware let
// through (a LevelSignedIn route, or a misconfigured one).
func memberCtx(user auth.User) context.Context {
	ctx := auth.WithUser(context.Background(), user)
	return authz.WithAccess(ctx, grants.Access{})
}

func plainMux(r *adminRig) *http.ServeMux {
	mux := http.NewServeMux()
	NewAPI(r.svc).RegisterRoutes(mux)
	return mux
}

func doCtx(mux http.Handler, ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestGranteeCredentialsForcedToOwnUser(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.store.UpsertUser(ctx, d.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}
	mux := plainMux(r)
	alice := auth.User{ID: 2, Email: "alice@example.com"}
	grant := authz.DatabaseGrant{DatabaseID: d.ID, DBUser: "shop_ro"}

	for _, path := range []string{"/api/databases/1/credentials?user=postgres", "/api/databases/1/credentials"} {
		got := doCtx(mux, granteeCtx(alice, grant), http.MethodGet, path, "")
		if got.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, got.Code, got.Body)
		}
		var body map[string]string
		_ = json.Unmarshal(got.Body.Bytes(), &body)
		if body["user"] != "shop_ro" || body["password"] != "pw" {
			t.Fatalf("%s user = %q, password %q; want shop_ro's", path, body["user"], body["password"])
		}
		super, _ := r.store.Get(ctx, d.ID)
		if !strings.Contains(body["url"], "shop_ro:pw@") || strings.Contains(body["url"], super.Password) {
			t.Fatalf("%s url = %q leaks the superuser", path, body["url"])
		}
	}

	c := r.create(t, "Cache", "redis", "8")
	redis := authz.DatabaseGrant{DatabaseID: c.ID, Level: "read"}
	got := doCtx(mux, granteeCtx(alice, redis), http.MethodGet, "/api/databases/2/credentials", "")
	if got.Code != http.StatusForbidden {
		t.Fatalf("redis credentials = %d: %s", got.Code, got.Body)
	}
}

func TestGranteeCredentialsRefusedForServerWideUser(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.store.UpsertUser(ctx, d.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}
	r.admin.answer(mustAccounts(t, "postgres").ServerWideQuery("shop_ro"), [][]string{{"postgres"}})
	mux := plainMux(r)
	alice := auth.User{ID: 2, Email: "alice@example.com"}
	grant := authz.DatabaseGrant{DatabaseID: d.ID, DBUser: "shop_ro"}
	got := doCtx(mux, granteeCtx(alice, grant), http.MethodGet, "/api/databases/1/credentials", "")
	if got.Code != http.StatusUnprocessableEntity || !strings.Contains(got.Body.String(), "server-wide privileges") {
		t.Fatalf("credentials of a server-wide user = %d: %s", got.Code, got.Body)
	}
	// An Admin still reveals it.
	admin := doCtx(mux, context.Background(), http.MethodGet, "/api/databases/1/credentials?user=shop_ro", "")
	if admin.Code != http.StatusOK {
		t.Fatalf("admin credentials = %d: %s", admin.Code, admin.Body)
	}
}

// A grant whose managed user happens to carry the superuser's name must
// still answer that managed user's stored password, never the superuser's.
func TestGranteeNamedLikeSuperuserGetsOwnPassword(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.store.UpsertUser(ctx, d.ID, "postgres", "managed-pw"); err != nil {
		t.Fatal(err)
	}
	mux := plainMux(r)
	alice := auth.User{ID: 2, Email: "alice@example.com"}
	grant := authz.DatabaseGrant{DatabaseID: d.ID, DBUser: "postgres"}
	got := doCtx(mux, granteeCtx(alice, grant), http.MethodGet, "/api/databases/1/credentials", "")
	if got.Code != http.StatusOK {
		t.Fatalf("credentials = %d: %s", got.Code, got.Body)
	}
	var body map[string]string
	_ = json.Unmarshal(got.Body.Bytes(), &body)
	super, _ := r.store.Get(ctx, d.ID)
	if body["password"] != "managed-pw" || body["password"] == super.Password {
		t.Fatalf("password = %q, want the managed user's, never the superuser's", body["password"])
	}
}

// A non-Admin the middleware let onto the list route without an identity in
// the context sees nothing: the list fails closed, never open.
func TestMemberListWithoutIdentityIsEmpty(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	mux := http.NewServeMux()
	NewAPI(qr.svc).RegisterRoutes(mux)
	ctx := authz.WithAccess(context.Background(), grants.Access{})
	got := doCtx(mux, ctx, http.MethodGet, "/api/databases", "")
	if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatalf("member list without identity = %d: %s", got.Code, got.Body)
	}
}

func TestNonAdminWithoutGrantIs404(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	mux := http.NewServeMux()
	NewAPI(qr.svc).RegisterRoutes(mux)
	alice := auth.User{ID: 2, Email: "alice@example.com"}
	paths := []struct{ method, path, body string }{
		{http.MethodGet, "/api/databases/1", ""},
		{http.MethodGet, "/api/databases/1/credentials", ""},
		{http.MethodPost, "/api/databases/1/query", `{"sql":"SELECT 1"}`},
		{http.MethodPost, "/api/databases/1/redis/command", `{"command":"GET k"}`},
	}
	for _, c := range paths {
		got := doCtx(mux, memberCtx(alice), c.method, c.path, c.body)
		if got.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, got.Code)
		}
	}
	qr.q.mu.Lock()
	defer qr.q.mu.Unlock()
	if len(qr.q.runs) != 0 || len(qr.q.calls) != 0 || len(qr.q.granteeCalls) != 0 {
		t.Fatalf("the querier was reached: %+v %+v", qr.q.runs, qr.q.calls)
	}
}

// seedGrantee gives member alice a grant on a postgres Database (shop_ro)
// and, when withRedis, on a redis one at Read.
func seedGrantee(t *testing.T, qr *queryRig, withRedis bool) (alice auth.User, pg Database, redis Database) {
	t.Helper()
	ctx := context.Background()
	id := insertPanelUser(t, qr.store, "alice@example.com", false)
	alice = auth.User{ID: id, Email: "alice@example.com"}
	pg = qr.create(t, "Shop", "postgres", "18")
	if err := qr.store.UpsertUser(ctx, pg.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := qr.store.SetGrant(ctx, Grant{DatabaseID: pg.ID, UserID: id, DBUser: "shop_ro"}); err != nil {
		t.Fatal(err)
	}
	if withRedis {
		redis = qr.create(t, "Cache", "redis", "8")
		if err := qr.store.SetGrant(ctx, Grant{DatabaseID: redis.ID, UserID: id, Level: LevelRead}); err != nil {
			t.Fatal(err)
		}
	}
	return alice, pg, redis
}

func TestGranteeListAndGet(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	alice, pg, redis := seedGrantee(t, qr, true)
	qr.create(t, "Other", "mysql", "8.4")
	qr.create(t, "More", "mariadb", "11.8")
	if err := qr.store.SetLastError(ctx, pg.ID, "boom"); err != nil {
		t.Fatal(err)
	}

	qr.user = &alice
	got := qr.do(context.Background(), http.MethodGet, "/api/databases", "")
	if got.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", got.Code, got.Body)
	}
	var list []DatabaseJSON
	if err := json.Unmarshal(got.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != pg.ID || list[1].ID != redis.ID {
		t.Fatalf("list = %+v, want the two granted Databases", list)
	}
	if list[0].LastError != "" || list[0].SettingsOutdated || len(list[0].Warnings) != 0 || list[0].MemoryMB != 0 || list[0].CPUs != 0 {
		t.Errorf("admin details leaked: %+v", list[0])
	}
	if list[0].User != "shop_ro" || list[0].Access == nil || list[0].Access.DBUser != "shop_ro" {
		t.Errorf("pg row = %+v, want user shop_ro with its access", list[0])
	}
	if list[0].DefaultDatabase != "" {
		t.Errorf("defaultDatabase = %q: a grantee's query panel must not default to the engine's database", list[0].DefaultDatabase)
	}
	if list[1].User != "" || list[1].Access == nil || list[1].Access.Level != "read" {
		t.Errorf("redis row = %+v, want no user and level read", list[1])
	}

	got = qr.do(context.Background(), http.MethodGet, "/api/databases/1", "")
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", got.Code, got.Body)
	}
	var one DatabaseJSON
	_ = json.Unmarshal(got.Body.Bytes(), &one)
	if one.User != "shop_ro" || one.LastError != "" || one.Access == nil {
		t.Fatalf("get = %+v", one)
	}

	// A member with no grants gets an empty list.
	bobID := insertPanelUser(t, qr.store, "bob@example.com", false)
	qr.user = &auth.User{ID: bobID, Email: "bob@example.com"}
	got = qr.do(context.Background(), http.MethodGet, "/api/databases", "")
	if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatalf("bob's list = %d %q, want []", got.Code, got.Body)
	}
}

func TestQueryAuditDetails(t *testing.T) {
	qr := newQueryRig(t)
	alice, pg, _ := seedGrantee(t, qr, false)
	body := `{"sql":"SELECT 1","database":"shop"}`

	qr.do(context.Background(), http.MethodPost, "/api/databases/1/query", body)
	events := qr.take()
	if len(events) != 1 || events[0].Detail["dbUser"] != "postgres" {
		t.Fatalf("admin events = %+v, want dbUser postgres", events)
	}

	qr.user = &alice
	qr.do(context.Background(), http.MethodPost, "/api/databases/1/query", body)
	events = qr.take()
	if len(events) != 1 || events[0].Detail["dbUser"] != "shop_ro" {
		t.Fatalf("grantee events = %+v, want dbUser shop_ro", events)
	}

	qr.user = nil
	qr.q.resp["DELETE FROM t"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Preview: true}
	qr.do(context.Background(), http.MethodPost, "/api/databases/1/query", `{"sql":"DELETE FROM t","database":"shop","writes":true,"preview":true}`)
	events = qr.take()
	if len(events) != 1 || events[0].Detail["outcome"] != "preview" {
		t.Fatalf("preview events = %+v, want outcome preview", events)
	}
	_ = pg
}

func TestGrantRoutesAudited(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	id := insertPanelUser(t, qr.store, "alice@example.com", false)
	d := qr.create(t, "Shop", "postgres", "18")
	if err := qr.store.UpsertUser(ctx, d.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}

	grantsPath := "/api/databases/1/grants/" + strconv.FormatInt(id, 10)
	got := qr.do(context.Background(), http.MethodPut, grantsPath, `{"dbUser":"shop_ro"}`)
	if got.Code != http.StatusNoContent {
		t.Fatalf("put = %d: %s", got.Code, got.Body)
	}
	events := qr.take()
	if len(events) != 1 || events[0].Action != "database.grant_set" || events[0].Detail["user"] != "alice@example.com" || events[0].Detail["dbUser"] != "shop_ro" {
		t.Fatalf("set events = %+v", events)
	}

	got = qr.do(context.Background(), http.MethodDelete, grantsPath, "")
	if got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", got.Code, got.Body)
	}
	events = qr.take()
	if len(events) != 1 || events[0].Action != "database.grant_delete" || events[0].Detail["user"] != "alice@example.com" {
		t.Fatalf("delete events = %+v", events)
	}

	list := qr.do(context.Background(), http.MethodGet, "/api/databases/1/grants", "")
	if list.Code != http.StatusOK || strings.TrimSpace(list.Body.String()) != "[]" {
		t.Fatalf("grants after delete = %d %q", list.Code, list.Body)
	}
}
