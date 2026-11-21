package databases

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDumperCommands(t *testing.T) {
	flags := []string{"--single-transaction", "--routines", "--events", "--triggers"}
	mysqlDump := func(bin string) []string { return append(append([]string{bin, "-uroot"}, flags...), "db") }
	cases := []struct {
		engine  string
		list    string
		system  []string
		dump    []string
		dumpEnv []string
		restore string
		resEnv  []string
		admin   string
		env     []string
	}{
		{"postgres", "psql -U postgres -At -c SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1", nil,
			[]string{"pg_dump", "-U", "postgres"}, []string{"PGDATABASE=db"},
			"psql -U postgres -X -q -v ON_ERROR_STOP=1", []string{"PGDATABASE=t"},
			"psql -U postgres -X -At -v ON_ERROR_STOP=1 -d template1", []string{"PGPASSWORD=pw"}},
		{"mysql", "mysql -uroot -N -e SHOW DATABASES", []string{"information_schema", "performance_schema", "mysql", "sys"},
			mysqlDump("mysqldump"), nil,
			"mysql -uroot --binary-mode --database=t", nil, "mysql -uroot -N", []string{"MYSQL_PWD=pw"}},
		{"mariadb", "mariadb -uroot -N -e SHOW DATABASES", []string{"information_schema", "performance_schema", "mysql", "sys"},
			mysqlDump("mariadb-dump"), nil,
			"mariadb -uroot --binary-mode --database=t", nil, "mariadb -uroot -N", []string{"MYSQL_PWD=pw"}},
	}
	for _, c := range cases {
		d, err := DumperFor(c.engine)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(d.ListCmd(), " "); got != c.list {
			t.Errorf("%s list = %q", c.engine, got)
		}
		if got := d.SystemDatabases(); !reflect.DeepEqual(got, c.system) {
			t.Errorf("%s system = %v", c.engine, got)
		}
		cmd, env := d.DumpCmd("db")
		if !reflect.DeepEqual(cmd, c.dump) || !reflect.DeepEqual(env, c.dumpEnv) {
			t.Errorf("%s dump = %v %v", c.engine, cmd, env)
		}
		cmd, env = d.RestoreCmd("t")
		if got := strings.Join(cmd, " "); got != c.restore || !reflect.DeepEqual(env, c.resEnv) {
			t.Errorf("%s restore = %q %v", c.engine, got, env)
		}
		if got := strings.Join(d.AdminCmd(), " "); got != c.admin {
			t.Errorf("%s admin = %q", c.engine, got)
		}
		if got := d.Env("pw"); !reflect.DeepEqual(got, c.env) {
			t.Errorf("%s env = %v", c.engine, got)
		}
	}
	if _, err := DumperFor("redis"); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("redis: %v", err)
	}
}

func TestDumperSQL(t *testing.T) {
	pg, _ := DumperFor("postgres")
	if got, want := pg.InspectSQL(`o'b"x`), `SELECT pg_get_userbyid(datdba)::text || E'\t' || coalesce(datacl::text, '') FROM pg_database WHERE datname = 'o''b"x';`; got != want {
		t.Errorf("pg inspect = %q", got)
	}
	if got, want := pg.ReplaceSQL("app", "appuser"),
		"DROP DATABASE IF EXISTS \"app\" WITH (FORCE);\nCREATE DATABASE \"app\" TEMPLATE template0 OWNER \"appuser\";\n"; got != want {
		t.Errorf("pg replace = %q", got)
	}
	if got, want := pg.ReplaceSQL(`a"b`, ""),
		"DROP DATABASE IF EXISTS \"a\"\"b\" WITH (FORCE);\nCREATE DATABASE \"a\"\"b\" TEMPLATE template0 OWNER \"postgres\";\nREVOKE CONNECT ON DATABASE \"a\"\"b\" FROM PUBLIC;\n"; got != want {
		t.Errorf("pg replace new = %q", got)
	}
	if got := pg.ReplaceSQL("app", `we"ird`); !strings.Contains(got, `OWNER "we""ird";`) {
		t.Errorf("owner not quoted: %q", got)
	}

	my, _ := DumperFor("mysql")
	if got, want := my.InspectSQL(`a\'b`),
		"SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = 'a\\\\''b';"; got != want {
		t.Errorf("mysql inspect = %q", got)
	}
	if got, want := my.ReplaceSQL("shop", "utf8mb4\tutf8mb4_0900_ai_ci"),
		"DROP DATABASE IF EXISTS `shop`;\nCREATE DATABASE `shop` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;\n"; got != want {
		t.Errorf("mysql replace = %q", got)
	}
	if got, want := my.ReplaceSQL("a`b", ""), "DROP DATABASE IF EXISTS `a``b`;\nCREATE DATABASE `a``b`;\n"; got != want {
		t.Errorf("mysql replace new = %q", got)
	}
	if got := my.ReplaceSQL("shop", "x; DROP\ty"); strings.Contains(got, "DROP\ty") || strings.Contains(got, "CHARACTER") {
		t.Errorf("unexpected inspect output reached the SQL: %q", got)
	}
}

func TestCheckName(t *testing.T) {
	if err := checkName("-x"); !errors.Is(err, ErrUnsafeName) {
		t.Fatalf("err = %v", err)
	}
	if err := checkName("app"); err != nil {
		t.Fatal(err)
	}
}
