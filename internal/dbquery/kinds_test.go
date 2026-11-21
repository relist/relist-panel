package dbquery

import (
	"strings"
	"testing"
)

// The audit log keeps what kinds of statement ran, never their text: every
// literal, quoted or not, stays out.
func TestStatementKinds(t *testing.T) {
	cases := []struct{ engine, sql, want string }{
		{"postgres", "SELECT 1; select 2", "SELECT ×2"},
		{"postgres", "ALTER ROLE app PASSWORD $pw$hunter2$pw$", "ALTER ROLE"},
		{"postgres", "ALTER ROLE app PASSWORD U&'hunter2'", "ALTER ROLE"},
		{"postgres", "ALTER ROLE app PASSWORD /* c */ 'hunter2'", "ALTER ROLE"},
		{"postgres", "/* hunter2 */ CREATE OR REPLACE FUNCTION f() RETURNS int AS 'SELECT 1' LANGUAGE sql", "CREATE FUNCTION"},
		{"postgres", "-- hunter2\nCREATE UNIQUE INDEX i ON t (a)", "CREATE INDEX"},
		{"postgres", "CREATE MATERIALIZED VIEW v AS SELECT 1", "CREATE MATERIALIZED VIEW"},
		{"postgres", "DROP TABLE IF EXISTS t; INSERT INTO t VALUES ('hunter2'); SELECT 1", "DROP TABLE, INSERT, SELECT"},
		{"postgres", "'hunter2'", "OTHER"},
		{"postgres", "hunter2", "OTHER"},
		{"postgres", "CREATE hunter2", "CREATE"},
		{"postgres", "SELECT 'unterminated", "OTHER"},
		{"mysql", "SET PASSWORD = _utf8mb4'hunter2'; ALTER USER u IDENTIFIED BY X'68756e74657232'", "SET, ALTER USER"},
		{"mariadb", "# hunter2\nGRANT ALL ON app.* TO 'u'@'%' IDENTIFIED BY 'hunter2'", "GRANT"},
		{"mysql", "", ""},
	}
	for _, c := range cases {
		got := StatementKinds(c.engine, c.sql)
		if got != c.want {
			t.Errorf("StatementKinds(%s, %q) = %q, want %q", c.engine, c.sql, got, c.want)
		}
		if strings.Contains(strings.ToLower(got), "hunter2") {
			t.Errorf("StatementKinds(%q) leaked the literal: %q", c.sql, got)
		}
	}
}
