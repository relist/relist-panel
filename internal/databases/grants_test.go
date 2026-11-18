package databases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/dbquery"
)

func TestGranteeNeverGetsSuperuserTarget(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Shop", "postgres", "18")
	if err := qr.store.UpsertUser(ctx, d.ID, "shop_ro", "sekret"); err != nil {
		t.Fatal(err)
	}
	super, _ := qr.store.Get(ctx, d.ID)

	if _, err := qr.svc.Query(ctx, d.ID, "shop_ro", dbquery.Request{SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := qr.svc.Schema(ctx, d.ID, "shop_ro", "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := qr.svc.Query(ctx, d.ID, "shop_ro", dbquery.Request{SQL: "DELETE FROM t", Preview: true}); err != nil {
		t.Fatal(err)
	}
	qr.q.mu.Lock()
	defer qr.q.mu.Unlock()
	if len(qr.q.targets) != 3 {
		t.Fatalf("targets = %d, want 3", len(qr.q.targets))
	}
	for i, target := range qr.q.targets {
		if target.User != "shop_ro" || target.Password != "sekret" || target.Password == super.Password {
			t.Errorf("target %d = %+v: a grantee run must use the managed user", i, target)
		}
	}
	for i, run := range qr.q.runs {
		if !run.Restricted {
			t.Errorf("run %d is not restricted: %+v", i, run)
		}
	}
	preview := qr.q.runs[len(qr.q.runs)-1]
	if !preview.Preview || !preview.Writes {
		t.Errorf("preview run = %+v, want Preview and Writes", preview)
	}

	// An unmanaged user never falls back to the superuser.
	if _, err := qr.svc.Query(ctx, d.ID, "ghost", dbquery.Request{SQL: "SELECT 1"}); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("ghost = %v, want ErrUserNotManaged", err)
	}
}

func TestRedisGranteeLevels(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Cache", "redis", "8")

	if _, err := qr.svc.RedisCommand(ctx, d.ID, LevelRead, 0, "SET k v", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := qr.svc.RedisCommand(ctx, d.ID, LevelReadWrite, 0, "SET k v", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := qr.svc.RedisCommand(ctx, d.ID, "", 0, "GET k", false, false); err != nil {
		t.Fatal(err)
	}
	qr.q.mu.Lock()
	defer qr.q.mu.Unlock()
	if len(qr.q.granteeCalls) != 2 {
		t.Fatalf("grantee calls = %+v, want 2", qr.q.granteeCalls)
	}
	if qr.q.granteeCalls[0].readWrite {
		t.Errorf("Read must never pass readWrite, got %+v", qr.q.granteeCalls[0])
	}
	if !qr.q.granteeCalls[1].readWrite {
		t.Errorf("Read-write with writes on = %+v, want readWrite", qr.q.granteeCalls[1])
	}
	if len(qr.q.calls) != 1 || qr.q.calls[0].line != "GET k" {
		t.Errorf("admin calls = %+v, want only GET k through RunRedis", qr.q.calls)
	}
}

func TestSetGrantRefuses(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	member := insertPanelUser(t, r.store, "alice@example.com", false)
	root := insertPanelUser(t, r.store, "root@example.com", true)

	if err := r.svc.SetGrant(ctx, d.ID+99, member, "shop_ro", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing database = %v", err)
	}
	if err := r.svc.SetGrant(ctx, d.ID, member+100, "shop_ro", ""); !errors.Is(err, ErrUnknownPanelUser) {
		t.Fatalf("unknown panel user = %v", err)
	}
	var toAdmin ErrGrantToAdmin
	if err := r.svc.SetGrant(ctx, d.ID, root, "shop_ro", ""); !errors.As(err, &toAdmin) || !strings.Contains(err.Error(), "root@example.com is an Admin") {
		t.Fatalf("admin grantee = %v", err)
	}
	var bad ErrBadGrant
	if err := r.svc.SetGrant(ctx, d.ID, member, "", ""); !errors.As(err, &bad) || !strings.Contains(err.Error(), "pick a database user") {
		t.Fatalf("no db user = %v", err)
	}
	if err := r.svc.SetGrant(ctx, d.ID, member, "shop_ro", LevelRead); !errors.As(err, &bad) || !strings.Contains(err.Error(), "only for Redis") {
		t.Fatalf("level on sql = %v", err)
	}
	if err := r.svc.SetGrant(ctx, d.ID, member, "shop_ro", ""); !errors.As(err, &bad) || !strings.Contains(err.Error(), "password is unknown") {
		t.Fatalf("unmanaged user = %v", err)
	}
	if err := r.store.UpsertUser(ctx, d.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetGrant(ctx, d.ID, member, "shop_ro", ""); err != nil {
		t.Fatalf("clean grant = %v", err)
	}
	if got, _ := r.store.Grants(ctx, d.ID); len(got) != 1 || got[0].DBUser != "shop_ro" {
		t.Fatalf("stored = %+v", got)
	}
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetGrant(ctx, d.ID, member, "shop_ro", ""); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stopped = %v", err)
	}

	c := r.create(t, "Cache", "redis", "8")
	if err := r.svc.SetGrant(ctx, c.ID, member, "", RedisLevel("admin")); !errors.As(err, &bad) {
		t.Fatalf("bad level = %v", err)
	}
	if err := r.svc.SetGrant(ctx, c.ID, member, "shop_ro", LevelRead); !errors.As(err, &bad) {
		t.Fatalf("db user on redis = %v", err)
	}
	if err := r.svc.SetGrant(ctx, c.ID, member, "", LevelRead); err != nil {
		t.Fatalf("redis grant = %v", err)
	}
	if got, _ := r.store.Grants(ctx, c.ID); len(got) != 1 || got[0].Level != LevelRead {
		t.Fatalf("stored redis = %+v", got)
	}
}

func TestSetGrantRefusesServerWideUser(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	member := insertPanelUser(t, r.store, "alice@example.com", false)
	if err := r.store.UpsertUser(ctx, d.ID, "boss", "pw"); err != nil {
		t.Fatal(err)
	}
	r.admin.answer(mustAccounts(t, "postgres").ServerWideQuery("boss"), [][]string{{"postgres"}})
	var wide ErrServerWideUser
	err := r.svc.SetGrant(ctx, d.ID, member, "boss", "")
	if !errors.As(err, &wide) || !strings.Contains(err.Error(), "server-wide privileges") {
		t.Fatalf("server-wide = %v", err)
	}
	if got, _ := r.store.Grants(ctx, d.ID); len(got) != 0 {
		t.Fatalf("stored = %+v, want none", got)
	}
}

func TestDeleteUserConfirmsGrants(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	member := insertPanelUser(t, r.store, "alice@example.com", false)
	if err := r.store.UpsertUser(ctx, r.d.ID, "shop_ro", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetGrant(ctx, Grant{DatabaseID: r.d.ID, UserID: member, DBUser: "shop_ro"}); err != nil {
		t.Fatal(err)
	}
	r.users("shop_ro")
	r.admin.answer(r.pg.OwnedQuery("shop_ro"), nil)
	r.admin.answer(r.pg.PrivilegedDatabasesQuery("shop_ro"), nil)

	var inUse ErrUserInUse
	err := r.svc.DeleteUser(ctx, r.d.ID, "shop_ro", false)
	if !errors.As(err, &inUse) || !slices.Equal(inUse.Emails, []string{"alice@example.com"}) {
		t.Fatalf("in use = %v", err)
	}
	if err := r.svc.DeleteUser(ctx, r.d.ID, "shop_ro", true); err != nil {
		t.Fatalf("confirmed = %v", err)
	}
	if got, _ := r.store.Grants(ctx, r.d.ID); len(got) != 0 {
		t.Fatalf("grants left = %+v", got)
	}
}

func TestGranteeDatabases(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.databases([]string{"owned", "1", "alice", "f"}, []string{"readable", "1", "bob", "f"}, []string{"hidden", "1", "bob", "f"})
	r.admin.answer(r.pg.AccessQuery(), [][]string{{"alice", "readable", "read"}})
	got, err := r.svc.GranteeDatabases(ctx, r.d.ID, "alice")
	if err != nil || !slices.Equal(got, []string{"owned", "readable"}) {
		t.Fatalf("GranteeDatabases = %v, %v", got, err)
	}
}

// Security review: an Admin's run inside a database another role owns ran as
// the superuser, so the owner's views, triggers and functions ran with
// superuser rights. It now connects as the owner, and the superuser is an
// explicit choice.
func TestAdminQueryRunsAsTheOwner(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Shop", "postgres", "18")
	super, _ := qr.store.Get(ctx, d.ID)
	if err := qr.store.UpsertUser(ctx, d.ID, "shop", "owner-pw"); err != nil {
		t.Fatal(err)
	}
	if err := qr.store.UpsertUser(ctx, d.ID, "shop_ro", "ro-pw"); err != nil {
		t.Fatal(err)
	}
	qr.admin.answer(pgOwnerQuery("shop"), [][]string{{"shop", "f"}})
	qr.admin.answer(pgOwnerQuery("legacy"), [][]string{{"postgres", "t"}})
	qr.admin.answer(pgOwnerQuery("appdb"), [][]string{{"app_role", "f"}})

	for _, c := range []struct {
		dbUser, database   string
		asSuperuser        bool
		wantUser, wantPass string
	}{
		{"", "shop", false, "shop", "owner-pw"},
		{"", "shop", true, "postgres", super.Password},
		{"", "legacy", false, "postgres", super.Password},
		{"", "appdb", true, "postgres", super.Password},
		{"shop_ro", "shop", true, "shop_ro", "ro-pw"},
	} {
		resp, err := qr.svc.Query(ctx, d.ID, c.dbUser, dbquery.Request{Database: c.database, SQL: "SELECT 1", AsSuperuser: c.asSuperuser})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		qr.q.mu.Lock()
		target, run := qr.q.targets[len(qr.q.targets)-1], qr.q.runs[len(qr.q.runs)-1]
		qr.q.mu.Unlock()
		if resp.User != c.wantUser || target.User != c.wantUser || target.Password != c.wantPass || run.Restricted != (c.dbUser != "") {
			t.Errorf("%+v: user %q, target %+v, restricted %v", c, resp.User, target, run.Restricted)
		}
	}

	// Review: an opt-in superuser run inside a database another role owns
	// starts on the catalog search_path, so the owner's ALTER DATABASE … SET
	// search_path cannot shadow an operator it uses.
	for _, c := range []struct {
		database    string
		asSuperuser bool
		want        bool
	}{{"shop", true, true}, {"appdb", true, true}, {"shop", false, false}, {"legacy", false, false}, {"legacy", true, false}} {
		if _, err := qr.svc.Query(ctx, d.ID, "", dbquery.Request{Database: c.database, SQL: "SELECT 1", AsSuperuser: c.asSuperuser}); err != nil {
			t.Fatal(err)
		}
		if got := qr.q.last().CatalogSearchPath; got != c.want {
			t.Errorf("%+v: CatalogSearchPath = %v", c, got)
		}
	}

	// Review: an owner can change its own password; the failed login says
	// how to get back, not only "turn on the superuser".
	qr.q.resp["SELECT 2"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{},
		Error: &dbquery.QueryError{Message: `failed to connect: FATAL: password authentication failed for user "shop" (SQLSTATE 28P01)`}}
	resp, err := qr.svc.Query(ctx, d.ID, "", dbquery.Request{Database: "shop", SQL: "SELECT 2"})
	if err != nil || resp.Error == nil || !strings.Contains(resp.Error.Message, "Reset password") {
		t.Fatalf("owner login failure = %+v, %v", resp.Error, err)
	}

	// An owner the panel has no password for is never silently the superuser.
	var unmanaged ErrOwnerUnmanaged
	if _, err := qr.svc.Query(ctx, d.ID, "", dbquery.Request{Database: "appdb", SQL: "SELECT 1"}); !errors.As(err, &unmanaged) || unmanaged.Owner != "app_role" {
		t.Fatalf("appdb = %v, want ErrOwnerUnmanaged", err)
	}
}

func TestAdminQueryAsSuperuserOverHTTP(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Shop", "postgres", "18")
	if err := qr.store.UpsertUser(ctx, d.ID, "shop", "owner-pw"); err != nil {
		t.Fatal(err)
	}
	qr.admin.answer(pgOwnerQuery("shop"), [][]string{{"shop", "f"}})
	qr.admin.answer(pgOwnerQuery("appdb"), [][]string{{"app_role", "f"}})
	path := fmt.Sprintf("/api/databases/%d/query", d.ID)

	rec := qr.do(ctx, "POST", path, `{"database":"shop","sql":"SELECT 1"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user":"shop"`) {
		t.Fatalf("owner run = %d %s", rec.Code, rec.Body)
	}
	if ev := qr.take(); len(ev) != 1 || ev[0].Detail["dbUser"] != "shop" {
		t.Fatalf("owner run audit = %+v", ev)
	}
	rec = qr.do(ctx, "POST", path, `{"database":"shop","sql":"SELECT 1","asSuperuser":true}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user":"postgres"`) {
		t.Fatalf("superuser run = %d %s", rec.Code, rec.Body)
	}
	if ev := qr.take(); len(ev) != 1 || ev[0].Detail["dbUser"] != "postgres" {
		t.Fatalf("superuser run audit = %+v", ev)
	}
	rec = qr.do(ctx, "POST", path, `{"database":"appdb","sql":"SELECT 1"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "app_role") || !strings.Contains(rec.Body.String(), "Reset password") {
		t.Fatalf("unmanaged owner = %d %s", rec.Code, rec.Body)
	}
}

// Review: the audit tells an opt-in superuser run from a run in a database
// the superuser owns.
func TestAdminQueryAuditsTheSuperuserChoice(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Shop", "postgres", "18")
	qr.admin.answer(pgOwnerQuery("legacy"), [][]string{{"postgres", "t"}})
	path := fmt.Sprintf("/api/databases/%d/query", d.ID)
	for _, c := range []struct {
		body string
		want bool
	}{{`{"database":"legacy","sql":"SELECT 1"}`, false}, {`{"database":"legacy","sql":"SELECT 1","asSuperuser":true}`, true}} {
		if rec := qr.do(ctx, "POST", path, c.body); rec.Code != 200 {
			t.Fatalf("%s = %d %s", c.body, rec.Code, rec.Body)
		}
		if ev := qr.take(); len(ev) != 1 || ev[0].Detail["asSuperuser"] != c.want {
			t.Fatalf("%s audit = %+v", c.body, ev)
		}
	}
}

// Review: MariaDB has no owner, and a user with Full access can create a
// SQL SECURITY INVOKER view over a function, which then runs as root for an
// Admin who reads it. Root is an explicit choice in such a database.
func TestAdminMySQLQueryNeedsOptInWhereUsersHaveFull(t *testing.T) {
	qr := newQueryRig(t)
	ctx := context.Background()
	d := qr.create(t, "Maria", "mariadb", "11.8")
	my, _ := accountsFor("mariadb")
	qr.admin.answer(my.AccessQuery(), [][]string{{"shop", "shop", "full"}, {"reader", "zoo", "read"}})

	var optIn ErrRootOptIn
	if _, err := qr.svc.Query(ctx, d.ID, "", dbquery.Request{Database: "shop", SQL: "SELECT 1"}); !errors.As(err, &optIn) ||
		!slices.Equal(optIn.Users, []string{"shop"}) {
		t.Fatalf("shop = %v, want ErrRootOptIn naming shop", err)
	}
	for _, r := range []dbquery.Request{{Database: "zoo", SQL: "SELECT 1"}, {Database: "shop", SQL: "SELECT 1", AsSuperuser: true}} {
		resp, err := qr.svc.Query(ctx, d.ID, "", r)
		if err != nil || resp.User != "root" {
			t.Fatalf("%+v = %+v, %v", r, resp.User, err)
		}
	}
}
