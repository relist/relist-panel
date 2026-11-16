package dbquery

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestMySQLServerWide(t *testing.T) {
	clean := []string{
		"GRANT USAGE ON *.* TO `a`@`%`",
		"GRANT USAGE ON *.* TO `a`@`%` IDENTIFIED BY PASSWORD '*AB12'",
		"GRANT SELECT, SHOW VIEW ON `app\\_1`.* TO `a`@`%`",
		"GRANT SELECT (`id`, `name`) ON `app`.`t` TO `a`@`%`",
		"GRANT EXECUTE ON PROCEDURE `app`.`p` TO `a`@`%`",
	}
	for _, line := range clean {
		if MySQLServerWide([]string{line}) {
			t.Errorf("clean line read as server-wide: %s", line)
		}
	}
	wide := []string{
		"GRANT FILE ON *.* TO `a`@`%`",
		// Schema wildcards: % and unescaped _ match other schemas, mysql
		// included; the panel's own grants always escape _ as \_.
		"GRANT ALL PRIVILEGES ON `%`.* TO `a`@`%`",
		"GRANT SELECT ON `_____`.* TO `a`@`%`",
		"GRANT ALL PRIVILEGES ON `app%`.* TO `a`@`%`",
		// System schemas: writing mysql.* is global privileges at the next
		// FLUSH PRIVILEGES, and reading mysql.user is the root hash.
		"GRANT ALL PRIVILEGES ON `mysql`.* TO `a`@`%`",
		"GRANT SELECT ON `mysql`.`user` TO `a`@`%`",
		"GRANT SELECT ON sys.* TO `a`@`%`",
		"GRANT SELECT ON `performance_schema`.* TO `a`@`%`",
		"GRANT SELECT ON *.* TO `a`@`%`",
		"GRANT BACKUP_ADMIN ON *.* TO `a`@`%`",
		"GRANT `r`@`%` TO `a`@`%`",
		"GRANT PROXY ON ''@'' TO `a`@`%`",
		"GRANT ALL PRIVILEGES ON `app`.* TO `a`@`%` WITH GRANT OPTION",
		"GRANT USAGE ON *.* TO `a`@`%` WITH GRANT OPTION",
		"something unparsable",
	}
	for _, line := range wide {
		if !MySQLServerWide([]string{line}) {
			t.Errorf("server-wide line read as clean: %s", line)
		}
	}
	if MySQLServerWide(nil) {
		t.Error("no lines must be clean")
	}
	if !MySQLServerWide([]string{clean[0], wide[0]}) {
		t.Error("one wide line among clean ones must be server-wide")
	}
}

func TestPgServerWideQuery(t *testing.T) {
	q := PgServerWideQuery("session_user")
	for _, want := range []string{
		"pg_has_role(session_user, r.oid, 'MEMBER')",
		"rolsuper", "rolcreaterole", "rolreplication", "rolbypassrls",
		"pg_read_server_files", "pg_write_server_files", "pg_execute_server_program",
		"pg_read_all_data", "pg_write_all_data", "pg_signal_backend",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query is missing %q:\n%s", want, q)
		}
	}
	// The run path reads session_user, never current_user: SET role (phase 7)
	// makes current_user the owner, which would let a superuser login pass.
	src, err := os.ReadFile("postgres.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `PgServerWideQuery("current_user")`) {
		t.Error("postgres.go passes current_user to PgServerWideQuery")
	}
	if !strings.Contains(string(src), `PgServerWideQuery("session_user")`) {
		t.Error("postgres.go does not run PgServerWideQuery(\"session_user\")")
	}
}

func TestRunRefusesServerWide(t *testing.T) {
	ctx := context.Background()
	q := &fakeQueryer{script: map[string]*fakeCursor{
		"SHOW GRANTS": {cols: []Column{{"Grants for a@%", "VARCHAR"}}, rows: [][]Cell{{"GRANT ALL PRIVILEGES ON *.* TO `a`@`%`"}}},
	}}
	resp, err := refuseServerWide(ctx, q, "u")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Error == nil || resp.Error.Message != ServerWideMessage("u") {
		t.Fatalf("refusal = %+v", resp)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if len(q.log) != 1 || q.log[0] != "SHOW GRANTS" {
		t.Fatalf("statements sent = %v, want only SHOW GRANTS", q.log)
	}

	// A clean session is let through.
	q = &fakeQueryer{script: map[string]*fakeCursor{
		"SHOW GRANTS": {cols: []Column{{"Grants for a@%", "VARCHAR"}}, rows: [][]Cell{{"GRANT USAGE ON *.* TO `a`@`%`"}}},
	}}
	resp, err = refuseServerWide(ctx, q, "u")
	if err != nil || resp != nil {
		t.Fatalf("clean = %+v, %v", resp, err)
	}
}
