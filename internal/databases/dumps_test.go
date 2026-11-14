package databases

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDumpCommands(t *testing.T) {
	flags := []string{"--single-transaction", "--routines", "--events", "--triggers"}
	cases := []struct {
		engine  string
		scope   Scope
		want    [][]string
		restore string
		env     string
	}{
		{"postgres", Scope{All: true}, [][]string{{"pg_dumpall", "-U", "postgres", "--clean", "--if-exists"}},
			"psql -U postgres -d template1 -v ON_ERROR_STOP=0", "PGPASSWORD=pw"},
		{"postgres", Scope{Databases: []string{"a", "b"}}, [][]string{
			{"pg_dump", "-U", "postgres", "--create", "--clean", "--if-exists", "a"},
			{"pg_dump", "-U", "postgres", "--create", "--clean", "--if-exists", "b"}},
			"psql -U postgres -d template1 -v ON_ERROR_STOP=0", "PGPASSWORD=pw"},
		{"mysql", Scope{All: true}, [][]string{append([]string{"mysqldump", "-uroot", "--all-databases"}, flags...)}, "mysql -uroot", "MYSQL_PWD=pw"},
		{"mysql", Scope{Databases: []string{"a", "b"}}, [][]string{append([]string{"mysqldump", "-uroot", "--databases", "a", "b", "--add-drop-database"}, flags...)}, "mysql -uroot", "MYSQL_PWD=pw"},
		{"mariadb", Scope{All: true}, [][]string{append([]string{"mariadb-dump", "-uroot", "--all-databases"}, flags...)}, "mariadb -uroot", "MYSQL_PWD=pw"},
		{"mariadb", Scope{Databases: []string{"a"}}, [][]string{append([]string{"mariadb-dump", "-uroot", "--databases", "a", "--add-drop-database"}, flags...)}, "mariadb -uroot", "MYSQL_PWD=pw"},
	}
	for _, c := range cases {
		d, err := DumperFor(c.engine)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.DumpCmds(c.scope); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %+v: got %v, want %v", c.engine, c.scope, got, c.want)
		}
		if got := strings.Join(d.RestoreCmd(), " "); got != c.restore {
			t.Errorf("%s restore = %q", c.engine, got)
		}
		if got := d.Env("pw"); len(got) != 1 || got[0] != c.env {
			t.Errorf("%s env = %v", c.engine, got)
		}
	}
	if _, err := DumperFor("redis"); !errors.Is(err, ErrNoBackups) {
		t.Fatalf("redis: %v", err)
	}
}

func TestPostgresRestoreFailed(t *testing.T) {
	d, _ := DumperFor("postgres")
	// Seen on a live postgres 18 restore of a pg_dumpall file: the dump drops
	// and recreates template1, which is the database psql is connected to.
	known := "psql:<stdin>:12: ERROR:  role \"postgres\" already exists\npsql:<stdin>:40: ERROR:  current user cannot be dropped\n" +
		"psql:<stdin>:82: ERROR:  cannot drop the currently open database\npsql:<stdin>:87: ERROR:  database \"template1\" already exists\n"
	if d.RestoreFailed(known) {
		t.Fatal("the errors pg_dumpall always causes failed the restore")
	}
	if !d.RestoreFailed(known + "psql:<stdin>:99: ERROR:  syntax error at or near \"x\"\n") {
		t.Fatal("a real error was tolerated")
	}
}
