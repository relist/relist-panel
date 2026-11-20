package databases

import (
	"errors"
	"regexp"
	"strings"
)

// ErrNoBackups is returned for an engine that has no Backups (Redis).
var ErrNoBackups = errors.New("this engine has no backups")

var (
	ErrUnsafeName      = errors.New("database names starting with - are not supported")
	ErrBadTarget       = errors.New("pick a database to restore into: letters, digits, _ and -, at most 63 characters")
	ErrNothingToBackUp = errors.New("there are no databases to back up")
	ErrNotRestorable   = errors.New("this Backup holds several databases (an earlier format); download it and restore by hand")
)

// Dumper holds one SQL engine's commands for listing, dumping and restoring
// one logical database. They run by `docker exec` in the Database's own
// container, so the tool always matches the server version (ADR-0039).
type Dumper interface {
	ListCmd() []string         // prints one logical database per line
	SystemDatabases() []string // hidden from the list of logical databases
	// DumpCmd and RestoreCmd return the command and the env it needs beside
	// Env(password): PGDATABASE for postgres, nothing for mysql/mariadb.
	DumpCmd(db string) (cmd, env []string) // plain SQL of one database on stdout
	RestoreCmd(target string) (cmd, env []string)
	AdminCmd() []string // reads SQL on stdin; stops at the first error
	Env(password string) []string
	// InspectSQL prints, for target, one line "<owner>" (postgres) or
	// "<charset>\t<collation>" (mysql/mariadb), or nothing when it does not exist.
	InspectSQL(target string) string
	// ReplaceSQL drops and recreates target. prev is InspectSQL's output
	// ("" = the target is new).
	ReplaceSQL(target, prev string) string
	// NewSQL creates target as a new database the way a Backup's state says
	// (ADR-0050): from is the owner (Postgres, restricted like Create
	// database) or "<charset>\t<collation>" (MySQL, MariaDB).
	NewSQL(target, from string) string
}

// DumperFor returns the Dumper of an engine, ErrNoBackups for Redis.
func DumperFor(engine string) (Dumper, error) {
	switch engine {
	case "postgres":
		return pgDumper{}, nil
	case "mysql":
		return mysqlDumper{client: "mysql", dump: "mysqldump"}, nil
	case "mariadb":
		return mysqlDumper{client: "mariadb", dump: "mariadb-dump"}, nil
	}
	return nil, ErrNoBackups
}

// checkName refuses a logical database name that a tool would read as an
// option.
func checkName(name string) error {
	if strings.HasPrefix(name, "-") {
		return ErrUnsafeName
	}
	return nil
}

var (
	targetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`)
	// neverTarget are never a restore target, whatever the engine.
	neverTarget = []string{"template0", "template1", "information_schema", "performance_schema", "mysql", "sys"}
)

// checkTarget validates a restore target. replace is true when it is one of
// existing, false when the restore creates it.
func checkTarget(name string, existing []string) (replace bool, err error) {
	if !targetPattern.MatchString(name) {
		return false, ErrBadTarget
	}
	for _, n := range neverTarget {
		if strings.EqualFold(name, n) {
			return false, ErrBadTarget
		}
	}
	for _, e := range existing {
		if e == name {
			return true, nil
		}
	}
	return false, nil
}

// pgIdent quotes a Postgres identifier; pgLit a literal.
func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func pgLit(s string) string   { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// myIdent quotes a MySQL identifier; myLit a literal, where \ is an escape.
func myIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func myLit(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

type pgDumper struct{}

func (pgDumper) ListCmd() []string {
	return []string{"psql", "-U", "postgres", "-At", "-c", "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1"}
}

func (pgDumper) SystemDatabases() []string { return nil }

func (pgDumper) DumpCmd(db string) (cmd, env []string) {
	return []string{"pg_dump", "-U", "postgres"}, []string{"PGDATABASE=" + db}
}

func (pgDumper) RestoreCmd(target string) (cmd, env []string) {
	return []string{"psql", "-U", "postgres", "-X", "-q", "-v", "ON_ERROR_STOP=1"}, []string{"PGDATABASE=" + target}
}

func (pgDumper) AdminCmd() []string {
	return []string{"psql", "-U", "postgres", "-X", "-At", "-v", "ON_ERROR_STOP=1", "-d", "template1"}
}

func (pgDumper) Env(password string) []string { return []string{"PGPASSWORD=" + password} }

// InspectSQL prints one line "<owner>\t<datacl>": the ACL is empty while the
// database has the default one. coalesce keeps a NULL ACL from turning the
// whole line into NULL, which would read as "no such database".
func (pgDumper) InspectSQL(target string) string {
	return `SELECT pg_get_userbyid(datdba)::text || E'\t' || coalesce(datacl::text, '') FROM pg_database WHERE datname = ` + pgLit(target) + ";"
}

// ReplaceSQL drops and recreates target with its owner, and gives the new
// database its ACL back: a one-database pg_dump carries none, so without it a
// restore would reopen a restricted database to every user and drop the
// Read-only users' CONNECT. A new target (prev "") is restricted like Create
// database. prev is the trimmed InspectSQL line, so "app" and "app\t" both
// mean the default ACL.
func (pgDumper) ReplaceSQL(target, prev string) string {
	ownerPart, acl, _ := strings.Cut(prev, "\t")
	owner := strings.TrimSpace(ownerPart)
	if owner == "" {
		owner = "postgres"
	}
	if strings.TrimSpace(prev) == "" {
		return pgDumper{}.NewSQL(target, "")
	}
	t := pgIdent(target)
	out := "DROP DATABASE IF EXISTS " + t + " WITH (FORCE);\n" +
		"CREATE DATABASE " + t + " TEMPLATE template0 OWNER " + pgIdent(owner) + ";\n"
	entries := parseDatACL(strings.TrimSpace(acl))
	if len(entries) == 0 {
		return out
	}
	out += "REVOKE ALL ON DATABASE " + t + " FROM PUBLIC;\n"
	for _, e := range entries {
		if e.Grantee == owner {
			continue
		}
		var privs []string
		for _, c := range e.Privs {
			switch c {
			case 'c':
				privs = append(privs, "CONNECT")
			case 'C':
				privs = append(privs, "CREATE")
			case 'T':
				privs = append(privs, "TEMPORARY")
			}
		}
		if len(privs) == 0 {
			continue
		}
		to := "PUBLIC"
		if e.Grantee != "" {
			to = pgIdent(e.Grantee)
		}
		out += "GRANT " + strings.Join(privs, ", ") + " ON DATABASE " + t + " TO " + to + ";\n"
	}
	return out
}

// NewSQL creates target owned by owner ("" = postgres), restricted like
// Create database: only the owner and those granted CONNECT may connect.
func (pgDumper) NewSQL(target, owner string) string {
	if owner = strings.TrimSpace(owner); owner == "" {
		owner = "postgres"
	}
	t := pgIdent(target)
	return "DROP DATABASE IF EXISTS " + t + " WITH (FORCE);\n" +
		"CREATE DATABASE " + t + " TEMPLATE template0 OWNER " + pgIdent(owner) + ";\n" +
		"REVOKE CONNECT ON DATABASE " + t + " FROM PUBLIC;\n"
}

// aclEntry is one grant of a database ACL; an empty Grantee is PUBLIC.
type aclEntry struct{ Grantee, Privs string }

// parseDatACL reads the text of a datacl (aclitem[]): {=Tc/o,"\"a-b\"=c/o"}.
// It undoes two layers: the array quotes an element that holds a quote with
// backslashes, and an aclitem quotes a role name that is not plain with "" for
// a quote. An element that is not array-quoted is read as the aclitem it is.
func parseDatACL(acl string) []aclEntry {
	acl = strings.TrimSpace(acl)
	acl = strings.TrimPrefix(acl, "{")
	acl = strings.TrimSuffix(acl, "}")
	var elems []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(acl); i++ {
		c := acl[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(acl):
			cur.WriteByte(c)
			i++
			cur.WriteByte(acl[i])
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			elems = append(elems, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		elems = append(elems, cur.String())
	}
	var out []aclEntry
	for _, el := range elems {
		if len(el) >= 2 && el[0] == '"' && el[len(el)-1] == '"' {
			var b strings.Builder
			inner := el[1 : len(el)-1]
			for i := 0; i < len(inner); i++ {
				if inner[i] == '\\' && i+1 < len(inner) {
					i++
				}
				b.WriteByte(inner[i])
			}
			el = b.String()
		}
		if e, ok := parseACLItem(el); ok {
			out = append(out, e)
		}
	}
	return out
}

// parseACLItem reads grantee=privs/grantor.
func parseACLItem(item string) (aclEntry, bool) {
	var grantee strings.Builder
	i := 0
	if strings.HasPrefix(item, `"`) {
		i = 1
		for ; i < len(item); i++ {
			if item[i] == '"' {
				if i+1 < len(item) && item[i+1] == '"' {
					grantee.WriteByte('"')
					i++
					continue
				}
				i++
				break
			}
			grantee.WriteByte(item[i])
		}
	} else {
		for ; i < len(item) && item[i] != '='; i++ {
			grantee.WriteByte(item[i])
		}
	}
	if i >= len(item) || item[i] != '=' {
		return aclEntry{}, false
	}
	privs, _, _ := strings.Cut(item[i+1:], "/")
	return aclEntry{Grantee: grantee.String(), Privs: privs}, true
}

type mysqlDumper struct{ client, dump string }

func (m mysqlDumper) ListCmd() []string {
	return []string{m.client, "-uroot", "-N", "-e", "SHOW DATABASES"}
}

func (mysqlDumper) SystemDatabases() []string {
	return []string{"information_schema", "performance_schema", "mysql", "sys"}
}

func (m mysqlDumper) DumpCmd(db string) (cmd, env []string) {
	return []string{m.dump, "-uroot", "--single-transaction", "--routines", "--events", "--triggers", db}, nil
}

// RestoreCmd runs the file with --binary-mode, which turns off every client
// command but DELIMITER and \C for piped input. Without it a line could hold
// \u other, connect other or system … anywhere in it and leave the target,
// or run a shell in the container; CheckUpload only reads line starts.
func (m mysqlDumper) RestoreCmd(target string) (cmd, env []string) {
	return []string{m.client, "-uroot", "--binary-mode", "--database=" + target}, nil
}

func (m mysqlDumper) AdminCmd() []string { return []string{m.client, "-uroot", "-N"} }

func (mysqlDumper) Env(password string) []string { return []string{"MYSQL_PWD=" + password} }

func (mysqlDumper) InspectSQL(target string) string {
	return "SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = " + myLit(target) + ";"
}

var charsetWord = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func (mysqlDumper) ReplaceSQL(target, prev string) string {
	out := "DROP DATABASE IF EXISTS " + myIdent(target) + ";\nCREATE DATABASE " + myIdent(target)
	if cs, coll, ok := strings.Cut(strings.TrimSpace(prev), "\t"); ok && charsetWord.MatchString(cs) && charsetWord.MatchString(coll) {
		out += " CHARACTER SET " + cs + " COLLATE " + coll
	}
	return out + ";\n"
}

// NewSQL creates target with the character set and collation of from.
func (m mysqlDumper) NewSQL(target, from string) string { return m.ReplaceSQL(target, from) }

// execEnv is the env of a command: the password, then the command's own.
func execEnv(dp Dumper, password string, extra []string) []string {
	return append(dp.Env(password), extra...)
}
