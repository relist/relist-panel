package dbquery

import (
	"context"
	"strings"
)

// ServerWideMessage refuses a restricted run (or a grant) whose database user
// has powers beyond its own databases.
func ServerWideMessage(user string) string {
	return user + " has server-wide privileges (superuser, role management, server files or global grants), so a non-Admin cannot use it. Remove them or pick another user."
}

// PgServerWideQuery answers one row per server-wide role the given role is a
// member of, and no rows when it is clean. role is an SQL expression: the run
// path passes session_user — never current_user, which phase 7's per-database
// SET role turns into the owner before the first statement.
func PgServerWideQuery(role string) string {
	return `SELECT r.rolname FROM pg_roles r
		WHERE pg_has_role(` + role + `, r.oid, 'MEMBER')
		  AND (r.rolsuper OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls
		       OR r.rolname IN ('pg_read_server_files','pg_write_server_files','pg_execute_server_program',
		                        'pg_read_all_data','pg_write_all_data','pg_signal_backend'))`
}

// MySQLServerWide reads SHOW GRANTS lines. It is false only when every line
// is harmless: USAGE on *.*, or a grant on one schema's objects without WITH
// GRANT OPTION. Anything else — global privileges, roles, PROXY, a line it
// cannot parse — is server-wide. Lines never go into errors: MariaDB's carry
// the password hash.
func MySQLServerWide(lines []string) bool {
	for _, l := range lines {
		if !mysqlGrantClean(l) {
			return true
		}
	}
	return false
}

// mysqlGrantClean reads one SHOW GRANTS line.
func mysqlGrantClean(line string) bool {
	s := strings.TrimSpace(line)
	up := strings.ToUpper(s)
	if !strings.HasPrefix(up, "GRANT ") || strings.Contains(up, "WITH GRANT OPTION") {
		return false
	}
	on := strings.Index(up, " ON ")
	if on < 0 {
		// A role grant (GRANT `r`@`%` TO …), or something else entirely.
		return false
	}
	privs := strings.TrimSpace(s[len("GRANT "):on])
	rest := strings.TrimSpace(s[on+len(" ON "):])
	for _, kw := range []string{"PROCEDURE ", "FUNCTION "} {
		if strings.HasPrefix(strings.ToUpper(rest), kw) {
			rest = strings.TrimSpace(rest[len(kw):])
			break
		}
	}
	to := strings.Index(strings.ToUpper(rest), " TO ")
	if to < 0 {
		return false
	}
	target := strings.TrimSpace(rest[:to])
	if target == "*.*" {
		return strings.EqualFold(privs, "USAGE")
	}
	db, ok := grantTargetDB(target)
	return ok && db != "" && db != "*" && !schemaWildcard(db) && !systemSchema(db)
}

// schemaWildcard: an unescaped % or _ in a grant's schema is a wildcard over
// other schemas, mysql included. The panel's own grants escape _ as \_.
func schemaWildcard(db string) bool {
	for i := 0; i < len(db); i++ {
		if db[i] == '\\' {
			i++
			continue
		}
		if db[i] == '%' || db[i] == '_' {
			return true
		}
	}
	return false
}

// systemSchema: a grant on the server's own schemas is server-wide — writing
// mysql.* is global privileges at the next FLUSH PRIVILEGES, and reading
// mysql.user is the root password hash.
func systemSchema(db string) bool {
	plain := strings.NewReplacer(`\_`, "_", `\%`, "%").Replace(db)
	switch strings.ToLower(plain) {
	case "mysql", "sys", "performance_schema", "information_schema":
		return true
	}
	return false
}

// grantTargetDB reads the schema of a grant target: `db`.* , db.`t` , db.t.
// ok is false without a dot-separated schema (PROXY's ''@'', for one).
func grantTargetDB(target string) (string, bool) {
	if !strings.HasPrefix(target, "`") {
		db, _, found := strings.Cut(target, ".")
		return db, found
	}
	rest := target[1:]
	var db strings.Builder
	for {
		k := strings.IndexByte(rest, '`')
		if k < 0 {
			return "", false
		}
		if k+1 < len(rest) && rest[k+1] == '`' { // `` is a backquote in the name
			db.WriteString(rest[:k+1])
			rest = rest[k+2:]
			continue
		}
		db.WriteString(rest[:k])
		rest = rest[k+1:]
		break
	}
	if !strings.HasPrefix(rest, ".") {
		return "", false
	}
	return db.String(), true
}

// mysqlServerWide reads the session's own SHOW GRANTS through q.
func mysqlServerWide(ctx context.Context, q queryer) (bool, error) {
	cur, err := q.query(ctx, "SHOW GRANTS")
	if err != nil {
		return false, err
	}
	defer cur.Close()
	if _, err := cur.Columns(); err != nil {
		return false, err
	}
	var lines []string
	for cur.Next() {
		cells, err := cur.Cells()
		if err != nil {
			return false, err
		}
		if len(cells) == 0 {
			return true, nil
		}
		s, ok := cells[0].(string)
		if !ok {
			return true, nil
		}
		lines = append(lines, s)
	}
	if err := cur.Err(); err != nil {
		return false, err
	}
	return MySQLServerWide(lines), nil
}

// refuseServerWide is the restricted gate of a MySQL / MariaDB run: the
// refusal response when the session has server-wide powers, nil to go on.
func refuseServerWide(ctx context.Context, q queryer, user string) (*QueryResponse, error) {
	wide, err := mysqlServerWide(ctx, q)
	if err != nil {
		return nil, err
	}
	if !wide {
		return nil, nil
	}
	return &QueryResponse{Results: []QueryResult{}, Error: &QueryError{Message: ServerWideMessage(user)}}, nil
}
