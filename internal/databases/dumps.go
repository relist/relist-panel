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

func (pgDumper) InspectSQL(target string) string {
	return "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = " + pgLit(target) + ";"
}

func (pgDumper) ReplaceSQL(target, prev string) string {
	owner := strings.TrimSpace(prev)
	if owner == "" {
		owner = "postgres"
	}
	return "DROP DATABASE IF EXISTS " + pgIdent(target) + " WITH (FORCE);\n" +
		"CREATE DATABASE " + pgIdent(target) + " TEMPLATE template0 OWNER " + pgIdent(owner) + ";\n"
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

// execEnv is the env of a command: the password, then the command's own.
func execEnv(dp Dumper, password string, extra []string) []string {
	return append(dp.Env(password), extra...)
}
