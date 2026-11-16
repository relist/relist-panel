package databases

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestCheckUserName(t *testing.T) {
	for _, tc := range []struct {
		engine, name string
		want         func(error) bool
		label        string
	}{
		{"postgres", "app", nil, "plain"},
		{"postgres", "app-1", nil, "hyphen"},
		{"postgres", "a_b", nil, "underscore"},
		{"postgres", "-x", func(e error) bool { return errors.Is(e, ErrBadUserName) }, "leading hyphen"},
		{"postgres", "pg_x", func(e error) bool { return errors.As(e, new(ErrReservedUser)) }, "pg_ prefix"},
		{"postgres", "PG_x", func(e error) bool { return errors.As(e, new(ErrReservedUser)) }, "pg_ prefix, any case"},
		{"postgres", "Public", func(e error) bool { return errors.As(e, new(ErrReservedUser)) }, "public"},
		{"postgres", "postgres", func(e error) bool { return errors.As(e, new(ErrReservedUser)) }, "superuser"},
		{"mysql", "Root", func(e error) bool { return errors.As(e, new(ErrReservedUser)) }, "root, any case"},
		{"mariadb", "mariadb.sys", func(e error) bool { return errors.Is(e, ErrBadUserName) }, "a dot is not allowed"},
		{"mysql", strings.Repeat("a", 33), func(e error) bool { return errors.Is(e, ErrBadUserName) }, "33 on mysql"},
		{"mariadb", strings.Repeat("a", 33), func(e error) bool { return errors.Is(e, ErrBadUserName) }, "33 on mariadb"},
		{"mysql", strings.Repeat("a", 32), nil, "32 on mysql"},
		{"postgres", strings.Repeat("a", 33), nil, "33 on postgres"},
		{"postgres", strings.Repeat("a", 64), func(e error) bool { return errors.Is(e, ErrBadUserName) }, "64 on postgres"},
		{"mysql", strings.Repeat("a", 64), func(e error) bool { return errors.Is(e, ErrBadUserName) }, "64 on mysql"},
	} {
		err := checkUserName(tc.engine, tc.name)
		if tc.want == nil && err != nil {
			t.Errorf("%s: %q on %s: unexpected %v", tc.label, tc.name, tc.engine, err)
		}
		if tc.want != nil && (err == nil || !tc.want(err)) {
			t.Errorf("%s: %q on %s: err = %v", tc.label, tc.name, tc.engine, err)
		}
	}
	if got := (ErrReservedUser{Name: "root"}).Error(); got != "root is reserved" {
		t.Errorf("reserved text = %q", got)
	}
	if !strings.Contains(ErrBadUserName.Error(), "at most 63 characters (32 on MySQL and MariaDB)") {
		t.Errorf("ErrBadUserName = %q", ErrBadUserName)
	}
}

// Review Focus 1: GRANT ... ON db.* reads _ and % as wildcards, so Read-only
// on app_1 would reach appx1 unless the _ is escaped.
func TestMyGrantEscapesUnderscore(t *testing.T) {
	if got, want := myGrantDB("app_1"), "`app\\_1`"; got != want {
		t.Fatalf("myGrantDB = %q, want %q", got, want)
	}
	a, _ := accountsFor("mysql")
	got := a.ServerAccess("u", "app_1", "", AccessNone, AccessRead)
	want := []string{"GRANT SELECT, SHOW VIEW ON `app\\_1`.* TO 'u'@'%'"}
	if !slices.Equal(got, want) {
		t.Fatalf("ServerAccess = %q, want %q", got, want)
	}
	// The create-with-owner grant is a schema grant too.
	cd := a.CreateDatabase("app_1", "app_1")
	if !slices.Contains(cd, "GRANT ALL ON `app\\_1`.* TO 'app_1'@'%'") {
		t.Fatalf("CreateDatabase = %q", cd)
	}
}

func TestMyServerAccess(t *testing.T) {
	a, _ := accountsFor("mariadb")
	if got := a.ServerAccess("u", "d", "", AccessRead, AccessFull); !slices.Equal(got, []string{
		"REVOKE ALL PRIVILEGES ON `d`.* FROM 'u'@'%'", "GRANT ALL ON `d`.* TO 'u'@'%'"}) {
		t.Fatalf("read->full = %q", got)
	}
	if got := a.ServerAccess("u", "d", "", AccessFull, AccessNone); !slices.Equal(got, []string{
		"REVOKE ALL PRIVILEGES ON `d`.* FROM 'u'@'%'"}) {
		t.Fatalf("full->none = %q", got)
	}
	if got := a.ServerAccess("u", "d", "", AccessNone, AccessNone); len(got) != 0 {
		t.Fatalf("none->none = %q", got)
	}
}

func TestPgServerAccess(t *testing.T) {
	a, _ := accountsFor("postgres")
	const (
		connect = `GRANT CONNECT ON DATABASE "d" TO "u"`
		member  = `GRANT "o" TO "u"`
		leave   = `REVOKE "o" FROM "u"`
		revoke  = `REVOKE ALL ON DATABASE "d" FROM "u"`
	)
	for _, tc := range []struct {
		from, to Access
		want     []string
	}{
		{AccessNone, AccessNone, []string{revoke}},
		{AccessNone, AccessRead, []string{connect}},
		{AccessNone, AccessFull, []string{connect, member}},
		{AccessRead, AccessNone, []string{revoke}},
		{AccessRead, AccessRead, []string{connect}},
		{AccessRead, AccessFull, []string{connect, member}},
		{AccessFull, AccessNone, []string{leave, revoke}},
		{AccessFull, AccessRead, []string{leave, connect}},
		{AccessFull, AccessFull, []string{connect, member}},
	} {
		if got := a.ServerAccess("u", "d", "o", tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("%s->%s = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
	// An owner is never made a member of its own role.
	if got := a.ServerAccess("o", "d", "o", AccessFull, AccessFull); !slices.Equal(got, []string{`GRANT CONNECT ON DATABASE "d" TO "o"`}) {
		t.Errorf("owner full = %q", got)
	}
}

func TestPgInDatabaseRead(t *testing.T) {
	a, _ := accountsFor("postgres")
	got := a.InDatabaseAccess("u", "o", []string{"public", "sales"}, AccessRead)
	want := []string{
		`GRANT USAGE ON SCHEMA "public" TO "u"`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA "public" TO "u"`,
		`GRANT SELECT ON ALL SEQUENCES IN SCHEMA "public" TO "u"`,
		`GRANT USAGE ON SCHEMA "sales" TO "u"`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA "sales" TO "u"`,
		`GRANT SELECT ON ALL SEQUENCES IN SCHEMA "sales" TO "u"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "o" GRANT SELECT ON TABLES TO "u"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestPgInDatabaseNoneAndFullRevoke(t *testing.T) {
	a, _ := accountsFor("postgres")
	for _, lvl := range []Access{AccessNone, AccessFull} {
		got := a.InDatabaseAccess("u", "o", []string{"public"}, lvl)
		want := []string{
			`REVOKE ALL ON ALL TABLES IN SCHEMA "public" FROM "u"`,
			`REVOKE ALL ON ALL SEQUENCES IN SCHEMA "public" FROM "u"`,
			`REVOKE ALL ON SCHEMA "public" FROM "u"`,
			`ALTER DEFAULT PRIVILEGES FOR ROLE "o" REVOKE ALL ON TABLES FROM "u"`,
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %q", lvl, got)
		}
	}
	m, _ := accountsFor("mysql")
	if got := m.InDatabaseAccess("u", "", []string{"x"}, AccessRead); got != nil {
		t.Errorf("mysql has no in-database step: %q", got)
	}
}

func TestQuotingHostileNames(t *testing.T) {
	pg, _ := accountsFor("postgres")
	if got := pg.CreateUser(`a"b`, "pw")[0]; got != `CREATE ROLE "a""b" LOGIN PASSWORD 'pw'` {
		t.Errorf("pg CreateUser = %q", got)
	}
	my, _ := accountsFor("mysql")
	got := my.CreateUser("u", `p'q\r`)[0]
	if got != `CREATE USER 'u'@'%' IDENTIFIED BY 'p''q\\r'` {
		t.Errorf("my CreateUser = %q", got)
	}
	if got := my.SetPassword(`a'b`, "x")[0]; got != `ALTER USER 'a''b'@'%' IDENTIFIED BY 'x'` {
		t.Errorf("my SetPassword = %q", got)
	}
	if got := pg.SetPassword("u", "x")[0]; got != `ALTER ROLE "u" PASSWORD 'x'` {
		t.Errorf("pg SetPassword = %q", got)
	}
}

func TestSuperuserPasswordMySQLOneStatement(t *testing.T) {
	for _, e := range []string{"mysql", "mariadb"} {
		a, _ := accountsFor(e)
		got := a.SuperuserPassword("pw")
		if len(got) != 1 || !strings.Contains(got[0], "'root'@'%'") || !strings.Contains(got[0], "'root'@'localhost'") ||
			!strings.HasPrefix(got[0], "ALTER USER IF EXISTS ") {
			t.Errorf("%s: %q", e, got)
		}
	}
	pg, _ := accountsFor("postgres")
	if got := pg.SuperuserPassword("pw"); !slices.Equal(got, []string{"ALTER ROLE postgres WITH PASSWORD 'pw'"}) {
		t.Errorf("pg = %q", got)
	}
}

func TestPgCreateDatabaseRevokes(t *testing.T) {
	pg, _ := accountsFor("postgres")
	const revoke = `REVOKE CONNECT ON DATABASE "n" FROM PUBLIC`
	got := pg.CreateDatabase("n", "")
	if !slices.Equal(got, []string{`CREATE DATABASE "n"`, revoke}) {
		t.Errorf("no owner: %q", got)
	}
	got = pg.CreateDatabase("n", "o")
	if !slices.Equal(got, []string{`CREATE DATABASE "n" OWNER "o"`, revoke}) {
		t.Errorf("owner: %q", got)
	}
	if got := pg.Restrict("n"); !slices.Equal(got, []string{revoke}) {
		t.Errorf("Restrict = %q", got)
	}
	my, _ := accountsFor("mysql")
	if my.Restrict("n") != nil {
		t.Error("mysql has no Restrict")
	}
}

func TestAccountsForRedis(t *testing.T) {
	if _, err := accountsFor("redis"); !errors.Is(err, ErrNoQuery) {
		t.Fatalf("err = %v", err)
	}
}

func TestAccountsDropAndOwned(t *testing.T) {
	pg, _ := accountsFor("postgres")
	if got := pg.DropDatabase("n"); !slices.Equal(got, []string{`DROP DATABASE "n" WITH (FORCE)`}) {
		t.Errorf("pg drop = %q", got)
	}
	if got := pg.DropUser("u"); !slices.Equal(got, []string{`DROP ROLE "u"`}) {
		t.Errorf("pg drop user = %q", got)
	}
	if got := pg.DropOwned("u"); !slices.Equal(got, []string{`DROP OWNED BY "u"`}) {
		t.Errorf("pg drop owned = %q", got)
	}
	my, _ := accountsFor("mysql")
	if got := my.DropDatabase("n"); !slices.Equal(got, []string{"DROP DATABASE `n`"}) {
		t.Errorf("my drop = %q", got)
	}
	if got := my.DropUser("u"); !slices.Equal(got, []string{"DROP USER 'u'@'%'"}) {
		t.Errorf("my drop user = %q", got)
	}
	if my.DropOwned("u") != nil || my.OwnedQuery("u") != "" || my.PrivilegedDatabasesQuery("u") != "" || my.SuperuserRolesQuery() != "" || my.SchemasQuery() != "" {
		t.Error("mysql has no ownership queries")
	}
}

func TestAccountsQueriesMentionTheRules(t *testing.T) {
	pg, _ := accountsFor("postgres")
	if q := pg.UsersQuery(); !strings.Contains(q, "rolcanlogin") || !strings.Contains(q, "pg\\_%") || !strings.Contains(q, "'postgres'") {
		t.Errorf("pg users query = %s", q)
	}
	if q := pg.OwnedQuery("a'b"); !strings.Contains(q, "'a''b'") || !strings.Contains(q, "'o'") {
		t.Errorf("pg owned query = %s", q)
	}
	my, _ := accountsFor("mariadb")
	for _, name := range []string{"root", "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys", "healthcheck"} {
		if !strings.Contains(my.UsersQuery(), "'"+name+"'") {
			t.Errorf("mysql users query does not hide %s", name)
		}
	}
	if !strings.Contains(my.UsersQuery(), "Host = '%'") {
		t.Error("only host % is listed")
	}
	if q := my.AccessQuery(); !strings.Contains(q, "SCHEMA_PRIVILEGES") || !strings.Contains(q, "CHAR(92)") {
		t.Errorf("my access query = %s", q)
	}
}
