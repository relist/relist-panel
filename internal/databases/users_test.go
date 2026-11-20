package databases

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// pgUsersRig is a running Postgres Database with the fake Admin answering the
// phase 7 queries.
type pgUsersRig struct {
	*adminRig
	d  Database
	pg accountSQL
}

func newPgUsersRig(t *testing.T) *pgUsersRig {
	t.Helper()
	r := newAdminRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	return &pgUsersRig{adminRig: r, d: d, pg: mustAccounts(t, "postgres")}
}

func (r *pgUsersRig) databases(rows ...[]string) { r.admin.answer(r.pg.DatabasesQuery(), rows) }
func (r *pgUsersRig) users(names ...string) {
	var rows [][]string
	for _, n := range names {
		rows = append(rows, []string{n})
	}
	r.admin.answer(r.pg.UsersQuery(), rows)
}

func TestUsersMergesManaged(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app", "legacy")
	r.databases([]string{"app", "1", "app", "f"}, []string{"web", "1", "postgres", "f"})
	r.admin.answer(r.pg.AccessQuery(), [][]string{{"app", "app", "full"}, {"legacy", "web", "read"}})
	_ = r.store.UpsertUser(ctx, r.d.ID, "app", "p")
	_ = r.store.UpsertUser(ctx, r.d.ID, "gone", "p")
	got, err := r.svc.Users(ctx, r.d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("users = %+v", got)
	}
	app, legacy := got[0], got[1]
	if app.Name != "app" || !app.Managed || !slices.Equal(app.Owns, []string{"app"}) ||
		app.Access["app"] != AccessFull || app.Access["web"] != AccessNone {
		t.Errorf("app = %+v", app)
	}
	if legacy.Name != "legacy" || legacy.Managed || len(legacy.Owns) != 0 || legacy.Owns == nil ||
		legacy.Access["web"] != AccessRead || legacy.Access["app"] != AccessNone {
		t.Errorf("legacy = %+v", legacy)
	}
}

func TestCreateUser(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("bob")
	if err := r.svc.CreateUser(ctx, r.d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	u, err := r.store.GetUser(ctx, r.d.ID, "app")
	if err != nil || len(u.Password) != 43 {
		t.Fatalf("managed = %+v, %v", u, err)
	}
	if got := r.admin.stmts(); len(got) != 1 || got[0] != `CREATE ROLE "app" LOGIN PASSWORD '`+u.Password+`'` {
		t.Fatalf("statements = %q", got)
	}
	var ue ErrUserExists
	if err := r.svc.CreateUser(ctx, r.d.ID, "bob"); !errors.As(err, &ue) {
		t.Errorf("exists = %v", err)
	}
	if err := r.svc.CreateUser(ctx, r.d.ID, "postgres"); !errors.As(err, new(ErrReservedUser)) {
		t.Errorf("reserved = %v", err)
	}
	if err := r.svc.CreateUser(ctx, r.d.ID, "-a"); !errors.Is(err, ErrBadUserName) {
		t.Errorf("bad = %v", err)
	}
	if len(r.admin.calls()) != 1 {
		t.Errorf("a refused create sent %q", r.admin.stmts())
	}
}

// Review Focus 5: a failed CREATE ROLE carries the password in its text.
func TestCreateUserErrorIsScrubbed(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	var sent string
	r.admin.execErr = func(_ string, stmts []string) error {
		sent = stmts[0]
		return errors.New("syntax error in: " + stmts[0] + " (connected with " + r.d.Password + ")")
	}
	err := r.svc.CreateUser(ctx, r.d.ID, "app")
	if err == nil {
		t.Fatal("want an error")
	}
	pw := sent[strings.Index(sent, "'")+1 : len(sent)-1]
	if len(pw) != 43 {
		t.Fatalf("sent = %q", sent)
	}
	if strings.Contains(err.Error(), pw) || strings.Contains(err.Error(), r.d.Password) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("error = %q", err)
	}
	if _, gerr := r.store.GetUser(ctx, r.d.ID, "app"); !errors.Is(gerr, ErrUserNotManaged) {
		t.Fatalf("a failed create left a managed row: %v", gerr)
	}
}

func TestResetUserPassword(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app", "legacy")
	_ = r.store.UpsertUser(ctx, r.d.ID, "app", "old")
	if err := r.svc.ResetUserPassword(ctx, r.d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	u, _ := r.store.GetUser(ctx, r.d.ID, "app")
	if u.Password == "old" || len(u.Password) != 43 {
		t.Fatalf("password = %q", u.Password)
	}
	if got := r.admin.stmts(); len(got) != 1 || got[0] != `ALTER ROLE "app" PASSWORD '`+u.Password+`'` {
		t.Fatalf("statements = %q", got)
	}
	// An unmanaged user becomes managed.
	if err := r.svc.ResetUserPassword(ctx, r.d.ID, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.GetUser(ctx, r.d.ID, "legacy"); err != nil {
		t.Fatalf("legacy not managed after a reset: %v", err)
	}
	var unk ErrUnknownUser
	if err := r.svc.ResetUserPassword(ctx, r.d.ID, "nobody"); !errors.As(err, &unk) {
		t.Fatalf("unknown = %v", err)
	}
}

func TestResetUserPasswordRestoresRowOnFailure(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app", "legacy")
	_ = r.store.UpsertUser(ctx, r.d.ID, "app", "old")
	r.admin.execErr = func(string, []string) error { return errors.New("down") }
	if err := r.svc.ResetUserPassword(ctx, r.d.ID, "app"); err == nil {
		t.Fatal("want an error")
	}
	if u, _ := r.store.GetUser(ctx, r.d.ID, "app"); u.Password != "old" {
		t.Fatalf("old password not restored: %q", u.Password)
	}
	if err := r.svc.ResetUserPassword(ctx, r.d.ID, "legacy"); err == nil {
		t.Fatal("want an error")
	}
	if _, err := r.store.GetUser(ctx, r.d.ID, "legacy"); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("a row was left for a user whose reset failed: %v", err)
	}
}

// Review Focus 2: GRANT postgres TO app would hand the user everything
// postgres owns.
func TestSetAccessRefusesFullOnSuperuserOwned(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app")
	r.databases([]string{"postgres", "1", "postgres", "t"}, []string{"web", "1", "web", "f"})
	r.admin.answer(r.pg.SuperuserRolesQuery(), [][]string{{"postgres"}})
	err := r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"web": AccessRead, "postgres": AccessFull})
	var fe ErrFullOnSuperuserDB
	if !errors.As(err, &fe) || fe.Database != "postgres" {
		t.Fatalf("err = %v", err)
	}
	want := "postgres is owned by a superuser, so Full access would make the user one; give it Read-only, or create the database with an owning user"
	if err.Error() != want {
		t.Fatalf("text = %q", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("statements sent despite the refusal: %q", r.admin.stmts())
	}
	for _, s := range r.admin.stmts() {
		if strings.Contains(s, `GRANT "postgres"`) {
			t.Fatalf("GRANT postgres sent: %q", s)
		}
	}
	// Read-only on it is fine.
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"postgres": AccessRead}); err != nil {
		t.Fatal(err)
	}
}

func TestSetAccessOwnerFixed(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app")
	r.databases([]string{"app", "1", "app", "f"})
	err := r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"app": AccessRead})
	var oe ErrOwnerAccess
	if !errors.As(err, &oe) || err.Error() != "app owns app; an owner always has Full access" {
		t.Fatalf("err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("sent %q", r.admin.stmts())
	}
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"app": AccessFull}); err != nil {
		t.Fatal(err)
	}
	// The owner is never a member of its own role, nor stripped of its own tables.
	for _, c := range r.admin.calls() {
		if c.Database == "app" {
			t.Fatalf("in-database statements for the owner: %+v", c)
		}
		for _, s := range c.Stmts {
			if strings.Contains(s, `GRANT "app" TO "app"`) || strings.Contains(s, "REVOKE ALL ON ALL TABLES") {
				t.Fatalf("bad statement %q", s)
			}
		}
	}
}

func TestSetAccessReadPostgres(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("reader")
	r.databases([]string{"app", "1", "owner", "f"})
	r.admin.answerOn("app", r.pg.SchemasQuery(), [][]string{{"public"}})
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "reader", map[string]Access{"app": AccessRead}); err != nil {
		t.Fatal(err)
	}
	want := []adminCall{
		{"postgres", []string{`ALTER ROLE "reader" IN DATABASE "app" RESET role`, `GRANT CONNECT ON DATABASE "app" TO "reader"`}},
		{"app", r.pg.InDatabaseAccess("reader", "owner", []string{"public"}, AccessRead)},
	}
	got := r.admin.calls()
	if len(got) != 2 || got[0].Database != want[0].Database || !slices.Equal(got[0].Stmts, want[0].Stmts) ||
		got[1].Database != "app" || !slices.Equal(got[1].Stmts, want[1].Stmts) || len(got[1].Stmts) != 4 {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
}

func TestSetAccessUsesCurrentLevel(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("w")
	r.databases([]string{"app", "1", "owner", "f"})
	r.admin.answer(r.pg.AccessQuery(), [][]string{{"w", "app", "full"}})
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "w", map[string]Access{"app": AccessNone}); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.calls()[0].Stmts; !slices.Equal(got, []string{`ALTER ROLE "w" IN DATABASE "app" RESET role`, `REVOKE "owner" FROM "w"`, `REVOKE ALL ON DATABASE "app" FROM "w"`}) {
		t.Fatalf("full -> none = %q", got)
	}
}

func TestSetAccessUnknownDatabase(t *testing.T) {
	r := newPgUsersRig(t)
	r.users("app")
	r.databases([]string{"web", "1", "web", "f"})
	err := r.svc.SetUserAccess(context.Background(), r.d.ID, "app", map[string]Access{"web": AccessRead, "nope": AccessRead})
	var unk ErrUnknownLogical
	if !errors.As(err, &unk) || !slices.Equal(unk.Names, []string{"nope"}) {
		t.Fatalf("err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatal("sent statements")
	}
	if err := r.svc.SetUserAccess(context.Background(), r.d.ID, "app", map[string]Access{"web": "admin"}); !errors.Is(err, ErrBadAccess) {
		t.Fatalf("bad level = %v", err)
	}
	if err := r.svc.SetUserAccess(context.Background(), r.d.ID, "ghost", map[string]Access{"web": AccessRead}); !errors.As(err, new(ErrUnknownUser)) {
		t.Fatalf("unknown user = %v", err)
	}
}

func TestSetAccessMySQL(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Blog", "mysql", "8.4")
	my := mustAccounts(t, "mysql")
	r.admin.answer(my.UsersQuery(), [][]string{{"reader"}})
	r.admin.answer(my.DatabasesQuery(), [][]string{{"app_1", "1", "utf8mb4", ""}, {"appx1", "1", "utf8mb4", ""}})
	r.admin.answer(my.AccessQuery(), [][]string{{"reader", "appx1", "full"}})
	if err := r.svc.SetUserAccess(ctx, d.ID, "reader", map[string]Access{"app_1": AccessRead, "appx1": AccessNone}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GRANT SELECT, SHOW VIEW ON `app\\_1`.* TO 'reader'@'%'",
		"REVOKE ALL PRIVILEGES ON `appx1`.* FROM 'reader'@'%'",
	}
	if got := r.admin.stmts(); !slices.Equal(got, want) {
		t.Fatalf("statements = %q", got)
	}
}

func TestDeleteUserOwnsRefused(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app")
	r.admin.answer(r.pg.OwnedQuery("app"), [][]string{{"database", "app"}, {"objects", "web"}})
	err := r.svc.DeleteUser(ctx, r.d.ID, "app")
	var ow ErrUserOwns
	if !errors.As(err, &ow) || err.Error() != "app owns databases: app; owns objects in: web. Give them another owner or drop them first" {
		t.Fatalf("err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("dropped something: %q", r.admin.stmts())
	}
}

func TestDeleteUserDropsOwnedFirst(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app")
	_ = r.store.UpsertUser(ctx, r.d.ID, "app", "p")
	r.admin.answer(r.pg.PrivilegedDatabasesQuery("app"), [][]string{{"app"}, {"postgres"}})
	if err := r.svc.DeleteUser(ctx, r.d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	got := r.admin.calls()
	if len(got) != 3 || got[0].Database != "app" || got[0].Stmts[0] != `DROP OWNED BY "app"` ||
		got[1].Database != "postgres" || got[1].Stmts[0] != `DROP OWNED BY "app"` || got[2].Stmts[0] != `DROP ROLE "app"` {
		t.Fatalf("calls = %+v", got)
	}
	if _, err := r.store.GetUser(ctx, r.d.ID, "app"); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("managed row left: %v", err)
	}
	if err := r.svc.DeleteUser(ctx, r.d.ID, "nobody"); !errors.As(err, new(ErrUnknownUser)) {
		t.Fatalf("unknown = %v", err)
	}
}

func TestDeleteUserMySQL(t *testing.T) {
	r := newAdminRig(t)
	ctx := context.Background()
	d := r.create(t, "Blog", "mariadb", "11.8")
	r.admin.answer(mustAccounts(t, "mariadb").UsersQuery(), [][]string{{"app"}})
	_ = r.store.UpsertUser(ctx, d.ID, "app", "p")
	if err := r.svc.DeleteUser(ctx, d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.stmts(); !slices.Equal(got, []string{"DROP USER 'app'@'%'"}) {
		t.Fatalf("statements = %q", got)
	}
}

func TestUserCredentials(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	_ = r.store.UpsertUser(ctx, r.d.ID, "app", "secret")
	if pw, err := r.svc.UserCredentials(ctx, r.d.ID, "app"); err != nil || pw != "secret" {
		t.Fatalf("pw = %q, %v", pw, err)
	}
	if _, err := r.svc.UserCredentials(ctx, r.d.ID, "legacy"); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("unmanaged = %v", err)
	}
	if _, err := r.svc.UserCredentials(ctx, 99, "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing database = %v", err)
	}
}

// A Full member acts as the owner in the database, may own tables of its own,
// and every Save of its access re-applies it: nothing may be revoked from it.
func TestSetAccessFullKeepsMemberTables(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("report")
	r.databases([]string{"shop", "1", "shop", "f"})
	r.admin.answer(r.pg.AccessQuery(), [][]string{{"report", "shop", "full"}})
	r.admin.answerOn("shop", r.pg.SchemasQuery(), [][]string{{"public"}})
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "report", map[string]Access{"shop": AccessFull}); err != nil {
		t.Fatal(err)
	}
	want := []string{`GRANT CONNECT ON DATABASE "shop" TO "report"`, `GRANT "shop" TO "report"`, `ALTER ROLE "report" IN DATABASE "shop" SET role = 'shop'`}
	if got := r.admin.stmts(); !slices.Equal(got, want) {
		t.Fatalf("statements = %q, want %q", got, want)
	}
	for _, c := range r.admin.calls() {
		if c.Database == "shop" {
			t.Fatalf("in-database statements for a Full member: %+v", c)
		}
	}
}

// A database restored from an old Backup is owned by postgres while the
// app's user owns its tables: the refusal names the one statement that
// fixes it, instead of giving the tables to a superuser.
func TestOwnsInASuperuserOwnedDatabaseNamesTheFix(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("app")
	r.databases([]string{"shop", "1", "postgres", "f"})
	r.admin.answer(r.pg.SuperuserRolesQuery(), [][]string{{"postgres"}})
	r.admin.answer(r.pg.OwnsInQuery("app"), [][]string{{"shop"}})
	err := r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"shop": AccessRead})
	want := `app owns tables or other objects in shop, which the superuser postgres owns; make app its owner first: ALTER DATABASE "shop" OWNER TO "app" in the Query tab`
	if err == nil || err.Error() != want {
		t.Fatalf("Read-only: err = %v", err)
	}
	err = r.svc.SetUserAccess(ctx, r.d.ID, "app", map[string]Access{"shop": AccessFull})
	want = `shop is owned by a superuser, so Full access would make the user one; give it Read-only, make the user its owner (ALTER DATABASE "shop" OWNER TO "app" in the Query tab), or create the database with an owning user`
	if err == nil || err.Error() != want {
		t.Fatalf("Full: err = %v", err)
	}
}

// Read-only and None would lock a user out of tables it owns.
func TestSetAccessRefusesLoweringWhenUserOwnsObjects(t *testing.T) {
	for _, to := range []Access{AccessRead, AccessNone} {
		t.Run(string(to), func(t *testing.T) {
			r := newPgUsersRig(t)
			ctx := context.Background()
			r.users("report")
			r.databases([]string{"shop", "1", "shop", "f"})
			r.admin.answer(r.pg.AccessQuery(), [][]string{{"report", "shop", "full"}})
			r.admin.answer(r.pg.OwnsInQuery("report"), [][]string{{"shop"}})
			err := r.svc.SetUserAccess(ctx, r.d.ID, "report", map[string]Access{"shop": to})
			var oe ErrUserOwnsIn
			if !errors.As(err, &oe) || oe.Database != "shop" {
				t.Fatalf("err = %v", err)
			}
			if want := "report owns tables or other objects in shop; give them to shop first, then change its access"; err.Error() != want {
				t.Fatalf("text = %q", err)
			}
			if len(r.admin.calls()) != 0 {
				t.Fatalf("statements sent despite the refusal: %q", r.admin.stmts())
			}
			// Full is not affected.
			if err := r.svc.SetUserAccess(ctx, r.d.ID, "report", map[string]Access{"shop": AccessFull}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Full's SET role would hide the tables a user already owns in a database, so
// giving Full there is refused too. An unchanged level on such a database is
// left alone, and a Save that changes another database still applies it.
func TestSetAccessOwnedObjectsBlockChangesOnly(t *testing.T) {
	r := newPgUsersRig(t)
	ctx := context.Background()
	r.users("report")
	r.databases([]string{"shop", "1", "shop", "f"}, []string{"web", "1", "web", "f"})
	r.admin.answer(r.pg.AccessQuery(), [][]string{{"report", "shop", "read"}})
	r.admin.answer(r.pg.OwnsInQuery("report"), [][]string{{"shop"}})

	var oe ErrUserOwnsIn
	if err := r.svc.SetUserAccess(ctx, r.d.ID, "report", map[string]Access{"shop": AccessFull}); !errors.As(err, &oe) {
		t.Fatalf("Full on a database it owns objects in: err = %v", err)
	}
	if len(r.admin.calls()) != 0 {
		t.Fatalf("statements sent despite the refusal: %q", r.admin.stmts())
	}

	if err := r.svc.SetUserAccess(ctx, r.d.ID, "report", map[string]Access{"shop": AccessRead, "web": AccessRead}); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.admin.calls() {
		if c.Database == "shop" {
			t.Fatalf("statements sent to shop, whose level did not change: %q", c.Stmts)
		}
		for _, s := range c.Stmts {
			if strings.Contains(s, `"shop"`) {
				t.Fatalf("statement about shop: %q", s)
			}
		}
	}
	if !slices.ContainsFunc(r.admin.stmts(), func(s string) bool { return s == `GRANT CONNECT ON DATABASE "web" TO "report"` }) {
		t.Fatalf("web was not applied: %q", r.admin.stmts())
	}
}
