package databases

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/dbquery"
)

// adminRig is a rig whose Admin is the fake.
type adminRig struct {
	*rig
	admin *fakeAdmin
}

func newAdminRig(t *testing.T) *adminRig {
	t.Helper()
	r := newRig(t)
	a := newFakeAdmin()
	r.svc = New(Deps{Store: r.store, Host: r.host, DataDir: r.dataDir, Admin: a,
		Locked:       func(context.Context) bool { return r.locked },
		UpdateLocked: func() bool { return r.updateLocked }})
	return &adminRig{rig: r, admin: a}
}

func mustAccounts(t *testing.T, engine string) accountSQL {
	t.Helper()
	a, err := accountsFor(engine)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLogicalDatabasesPostgres(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{
		{"app", "8192", "app", "f"}, {"postgres", "7000", "postgres", "t"}})
	got, err := r.svc.LogicalDatabases(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []LogicalDatabase{
		{Name: "app", SizeBytes: 8192, Owner: "app"},
		{Name: "postgres", SizeBytes: 7000, Owner: "postgres", AnyUserCanConnect: true, Protected: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestLogicalDatabasesMySQLUsesCharset(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	r.admin.answer(mustAccounts(t, "mysql").DatabasesQuery(), [][]string{{"app_1", "100", "utf8mb4", ""}})
	got, err := r.svc.LogicalDatabases(context.Background(), d.ID)
	if err != nil || len(got) != 1 || got[0].Charset != "utf8mb4" || got[0].Owner != "" || got[0].AnyUserCanConnect || got[0].Protected {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLogicalDatabasesRedisAndStopped(t *testing.T) {
	r := newAdminRig(t)
	rd := r.create(t, "Cache", "redis", "8")
	if _, err := r.svc.LogicalDatabases(context.Background(), rd.ID); !errors.Is(err, ErrNoQuery) {
		t.Fatalf("redis err = %v", err)
	}
	d := r.create(t, "Shop", "postgres", "18")
	r.host.containers["relist-db-2"].Running = false
	if _, err := r.svc.LogicalDatabases(context.Background(), d.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stopped err = %v", err)
	}
}

func TestCreateWithOwnerPostgres(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "app", true); err != nil {
		t.Fatal(err)
	}
	got := r.admin.stmts()
	if len(got) != 3 || !strings.HasPrefix(got[0], `CREATE ROLE "app" LOGIN PASSWORD '`) ||
		got[1] != `CREATE DATABASE "app" OWNER "app"` || got[2] != `REVOKE CONNECT ON DATABASE "app" FROM PUBLIC` {
		t.Fatalf("statements = %q", got)
	}
	u, err := r.store.GetUser(ctx, d.ID, "app")
	if err != nil || len(u.Password) != 43 || !strings.Contains(got[0], "'"+u.Password+"'") {
		t.Fatalf("managed user = %+v, %v", u, err)
	}
	if tg := r.admin.targets[0]; tg.Host != "relist-db-1" || tg.User != "postgres" || tg.Password != d.Password || tg.Port != 5432 {
		t.Fatalf("target = %+v", tg)
	}
}

func TestCreateWithoutOwner(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.CreateLogicalDatabase(context.Background(), d.ID, "app", false); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.stmts(); !slices.Equal(got, []string{`CREATE DATABASE "app"`, `REVOKE CONNECT ON DATABASE "app" FROM PUBLIC`}) {
		t.Fatalf("statements = %q", got)
	}
	if list, _ := r.store.ListUsers(context.Background(), d.ID); len(list) != 0 {
		t.Fatalf("users = %+v", list)
	}
}

func TestCreateRefusals(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	pg := mustAccounts(t, "postgres")
	r.admin.answer(pg.DatabasesQuery(), [][]string{{"taken", "1", "postgres", "t"}})
	r.admin.answer(pg.UsersQuery(), [][]string{{"bob"}})
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "-x", false); !errors.Is(err, ErrBadTarget) {
		t.Errorf("bad name = %v", err)
	}
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "template1", false); !errors.Is(err, ErrBadTarget) {
		t.Errorf("never target = %v", err)
	}
	var ex ErrDatabaseExists
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "taken", false); !errors.As(err, &ex) || err.Error() != "a database named taken already exists" {
		t.Errorf("exists = %v", err)
	}
	var ue ErrUserExists
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "bob", true); !errors.As(err, &ue) || err.Error() != "a user named bob already exists" {
		t.Errorf("user exists = %v", err)
	}
	if err := r.svc.CreateLogicalDatabase(ctx, d.ID, "pg_x", true); !errors.As(err, new(ErrReservedUser)) {
		t.Errorf("reserved owner = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("a refused create sent %q", r.admin.stmts())
	}
}

func TestCreateWithOwnerRollsBack(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.execErr = func(_ string, stmts []string) error {
		if strings.HasPrefix(stmts[0], "CREATE DATABASE") {
			return errors.New("boom: " + strings.Join(stmts, "; "))
		}
		return nil
	}
	err := r.svc.CreateLogicalDatabase(ctx, d.ID, "app", true)
	if err == nil {
		t.Fatal("want an error")
	}
	got := r.admin.stmts()
	if got[len(got)-1] != `DROP ROLE "app"` {
		t.Fatalf("statements = %q", got)
	}
	if _, gerr := r.store.GetUser(ctx, d.ID, "app"); !errors.Is(gerr, ErrUserNotManaged) {
		t.Fatalf("managed row left behind: %v", gerr)
	}
	created := got[0]
	pw := created[strings.Index(created, "'")+1 : len(created)-1]
	if len(pw) != 43 || strings.Contains(err.Error(), pw) {
		t.Fatalf("error %q leaks %q", err, pw)
	}
}

func TestCreateMySQLOwnerTooLong(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	err := r.svc.CreateLogicalDatabase(context.Background(), d.ID, strings.Repeat("a", 40), true)
	if !errors.Is(err, ErrBadUserName) {
		t.Fatalf("err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("sent %q", r.admin.stmts())
	}
	// Without an owner the same name is a fine database name.
	if err := r.svc.CreateLogicalDatabase(context.Background(), d.ID, strings.Repeat("a", 40), false); err != nil {
		t.Fatal(err)
	}
}

func TestCreateMySQLWithOwner(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "mariadb", "11.8")
	if err := r.svc.CreateLogicalDatabase(context.Background(), d.ID, "app_1", true); err != nil {
		t.Fatal(err)
	}
	got := r.admin.stmts()
	if len(got) != 3 || !strings.HasPrefix(got[0], "CREATE USER 'app_1'@'%' IDENTIFIED BY '") ||
		got[1] != "CREATE DATABASE `app_1`" || got[2] != "GRANT ALL ON `app\\_1`.* TO 'app_1'@'%'" {
		t.Fatalf("statements = %q", got)
	}
}

func TestRestrictLogicalDatabase(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"app", "1", "app", "t"}})
	if err := r.svc.RestrictLogicalDatabase(ctx, d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.stmts(); !slices.Equal(got, []string{`REVOKE CONNECT ON DATABASE "app" FROM PUBLIC`}) {
		t.Fatalf("statements = %q", got)
	}
	var unk ErrUnknownLogical
	if err := r.svc.RestrictLogicalDatabase(ctx, d.ID, "nope"); !errors.As(err, &unk) {
		t.Fatalf("unknown = %v", err)
	}
	my := r.create(t, "Blog", "mysql", "8.4")
	if err := r.svc.RestrictLogicalDatabase(ctx, my.ID, "app"); !errors.Is(err, ErrNoQuery) {
		t.Fatalf("mysql = %v", err)
	}
}

func TestDropProtectedRefused(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"postgres", "1", "postgres", "t"}, {"app", "1", "app", "f"}})
	for _, name := range []string{"postgres", "missing"} {
		var pe ErrProtectedDatabase
		err := r.svc.DropLogicalDatabase(context.Background(), d.ID, name, name, false)
		if !errors.As(err, &pe) || err.Error() != name+" cannot be dropped" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("sent %q", r.admin.stmts())
	}
}

func TestDropConfirm(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"app", "1", "app", "f"}})
	if err := r.svc.DropLogicalDatabase(context.Background(), d.ID, "app", "ap", false); !errors.Is(err, ErrConfirm) {
		t.Fatalf("err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatal("dropped without a confirmation")
	}
	if err := r.svc.DropLogicalDatabase(context.Background(), d.ID, "app", "app", false); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.stmts(); !slices.Equal(got, []string{`DROP DATABASE "app" WITH (FORCE)`}) {
		t.Fatalf("statements = %q", got)
	}
}

func TestDropBackupFirstFailureKeepsDatabase(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"app", "1", "app", "f"}})
	r.host.script("pg_dump -U postgres", func(ExecRequest) (int, error) { return 1, nil })
	if err := r.svc.DropLogicalDatabase(ctx, d.ID, "app", "app", true); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if len(r.admin.calls()) != 0 {
		t.Fatalf("dropped after a failed backup: %q", r.admin.stmts())
	}
	got, _ := r.store.Get(ctx, d.ID)
	if !strings.HasPrefix(got.LastError, "the pre-drop backup failed, so app was not dropped: ") {
		t.Fatalf("LastError = %q", got.LastError)
	}
	if r.svc.Jobs(d.ID) != "" {
		t.Fatal("slot not released")
	}
}

func TestDropBackupFirstDrops(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"app", "1", "app", "f"}})
	r.dumps("pg_dump -U postgres", "X;\n")
	if err := r.svc.DropLogicalDatabase(ctx, d.ID, "app", "app", true); err != nil {
		t.Fatal(err)
	}
	if j := r.svc.Jobs(d.ID); j != "drop_database" && j != "" {
		t.Fatalf("Jobs = %q", j)
	}
	r.svc.Wait()
	b := r.lastBackup(t, d.ID)
	if b.Trigger != "pre_drop" || b.Status != JobSucceeded || b.LogicalName != "app" {
		t.Fatalf("backup = %+v", b)
	}
	if got := r.admin.stmts(); !slices.Equal(got, []string{`DROP DATABASE "app" WITH (FORCE)`}) {
		t.Fatalf("statements = %q (the job must not block itself)", got)
	}
	if got, _ := r.store.Get(ctx, d.ID); got.LastError != "" {
		t.Fatalf("LastError = %q", got.LastError)
	}
}

func TestDropRemovesFromSchedule(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.admin.answer(mustAccounts(t, "postgres").DatabasesQuery(), [][]string{{"app", "1", "app", "f"}, {"web", "1", "web", "f"}})
	r.schedule(t, d.ID, func(sc *Schedule) { sc.All = false; sc.Databases = []string{"app", "web"} }, r.svc.d.Now())
	if err := r.svc.DropLogicalDatabase(ctx, d.ID, "app", "app", false); err != nil {
		t.Fatal(err)
	}
	if sc := r.getSchedule(t, d.ID); !slices.Equal(sc.Databases, []string{"web"}) {
		t.Fatalf("schedule databases = %v", sc.Databases)
	}
}

func TestRemoveScheduledDatabaseLeavesAllAndNone(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.store.RemoveScheduledDatabase(ctx, d.ID, "app"); err != nil {
		t.Fatalf("no row: %v", err)
	}
	r.schedule(t, d.ID, func(sc *Schedule) { sc.All = true; sc.Databases = []string{"app"} }, r.svc.d.Now())
	if err := r.store.RemoveScheduledDatabase(ctx, d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	if sc := r.getSchedule(t, d.ID); !sc.All || !slices.Equal(sc.Databases, []string{"app"}) {
		t.Fatalf("an all-databases schedule changed: %+v", sc)
	}
}

func TestDropMySQLRevokesGrants(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "mysql", "8.4")
	my := mustAccounts(t, "mysql")
	r.admin.answer(my.DatabasesQuery(), [][]string{{"app_1", "1", "utf8mb4", ""}})
	r.admin.answer(my.AccessQuery(), [][]string{{"a", "app_1", "full"}, {"b", "app_1", "read"}, {"b", "other", "read"}})
	if err := r.svc.DropLogicalDatabase(context.Background(), d.ID, "app_1", "app_1", false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"REVOKE ALL PRIVILEGES ON `app\\_1`.* FROM 'a'@'%'",
		"REVOKE ALL PRIVILEGES ON `app\\_1`.* FROM 'b'@'%'",
		"DROP DATABASE `app_1`",
	}
	if got := r.admin.stmts(); !slices.Equal(got, want) {
		t.Fatalf("statements = %q", got)
	}
}

func TestAdminBusyRefused(t *testing.T) {
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	_ = r.svc.beginJob(d.ID, JobKindBackup)
	if err := r.svc.CreateLogicalDatabase(context.Background(), d.ID, "app", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	// Reading the list is not a write.
	if _, err := r.svc.LogicalDatabases(context.Background(), d.ID); err != nil {
		t.Fatalf("list during a job: %v", err)
	}
}

func TestParseDatACL(t *testing.T) {
	got := parseDatACL(`{=c/postgres,postgres=CTc/postgres,app=c/postgres,"a""b"=c/postgres}`)
	want := []aclEntry{{"", "c"}, {"postgres", "CTc"}, {"app", "c"}, {`a"b`, "c"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// What Postgres really prints quotes the element again.
	real := parseDatACL(`{=Tc/app,app=CTc/app,"\"app-1\"=c/app","\"a\"\"b\"=c*/app"}`)
	want = []aclEntry{{"", "Tc"}, {"app", "CTc"}, {"app-1", "c"}, {`a"b`, "c*"}}
	if !slices.Equal(real, want) {
		t.Fatalf("real got %+v\nwant %+v", real, want)
	}
	if got := parseDatACL(""); len(got) != 0 {
		t.Fatalf("empty = %+v", got)
	}
}

func TestPgReplaceSQLKeepsACL(t *testing.T) {
	pg, _ := DumperFor("postgres")
	got := pg.ReplaceSQL("app", "app\t{=Tc/app,app=CTc/app,ro=c/app,\"\\\"r-w\\\"=c/app\"}")
	for _, want := range []string{
		`REVOKE ALL ON DATABASE "app" FROM PUBLIC;`,
		`GRANT TEMPORARY, CONNECT ON DATABASE "app" TO PUBLIC;`,
		`GRANT CONNECT ON DATABASE "app" TO "ro";`,
		`GRANT CONNECT ON DATABASE "app" TO "r-w";`,
		`OWNER "app";`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, `TO "app";`) {
		t.Errorf("the owner's own entry must be skipped: %q", got)
	}
	if strings.Index(got, "REVOKE ALL") < strings.Index(got, "CREATE DATABASE") {
		t.Errorf("the ACL is applied after the database exists: %q", got)
	}
	// An existing database with the default ACL: nothing to carry over.
	if got := pg.ReplaceSQL("app", "app\t"); strings.Contains(got, "REVOKE") || strings.Contains(got, "GRANT") {
		t.Errorf("default ACL = %q", got)
	}
	if got := pg.ReplaceSQL("app", "app"); strings.Contains(got, "REVOKE") {
		t.Errorf("no tab = %q", got)
	}
	// A new target is restricted like Create database.
	if got := pg.ReplaceSQL("app", ""); !strings.Contains(got, `REVOKE CONNECT ON DATABASE "app" FROM PUBLIC;`) {
		t.Errorf("new target = %q", got)
	}
}

// The same flow through a real restore: the admin session carries the ACL.
func TestRestoreReplaceKeepsACL(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	b := r.manualBackup(t, d.ID)
	admin := r.adminSQL(pgAdmin, "app\t{=Tc/app,app=CTc/app,ro=c/app}\n", nil)
	r.captureRestore(pgRestore, "")
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	// inspect, the per-database settings, then the replace.
	if len(*admin) != 3 || !strings.Contains((*admin)[2], `GRANT CONNECT ON DATABASE "app" TO "ro";`) || !strings.Contains((*admin)[2], `REVOKE ALL ON DATABASE "app" FROM PUBLIC;`) {
		t.Fatalf("admin sql = %q", *admin)
	}
}

// Restoring into a new Postgres database restricts it.
func TestRestoreIntoNewRestricts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "X;\n")
	b := r.manualBackup(t, d.ID)
	admin := r.adminSQL(pgAdmin, "", nil)
	r.captureRestore(pgRestore, "")
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "fresh", "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if len(*admin) != 2 || !strings.Contains((*admin)[1], `REVOKE CONNECT ON DATABASE "fresh" FROM PUBLIC;`) {
		t.Fatalf("admin sql = %q", *admin)
	}
}

var _ = io.EOF
var _ dbquery.Target
