package databases

import (
	"errors"
	"strings"
)

// ErrNoBackups is returned for an engine that has no Backups (Redis).
var ErrNoBackups = errors.New("this engine has no backups")

// Dumper holds one SQL engine's commands for listing, dumping and restoring.
// They run by `docker exec` in the Database's own container, so the tool
// always matches the server version (ADR-0039).
type Dumper interface {
	ListCmd() []string            // prints one logical database per line
	SystemDatabases() []string    // hidden from the list of logical databases
	DumpCmds(s Scope) [][]string  // run in order, their outputs concatenated
	RestoreCmd() []string         // reads SQL on stdin
	Env(password string) []string // the password, as the tools read it
	// RestoreFailed reports whether a restore that exited 0 still failed,
	// from its stderr.
	RestoreFailed(stderr string) bool
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

type pgDumper struct{}

func (pgDumper) ListCmd() []string {
	return []string{"psql", "-U", "postgres", "-At", "-c", "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1"}
}

func (pgDumper) SystemDatabases() []string { return []string{"postgres"} }

// DumpCmds: the whole instance is pg_dumpall, with its roles. A selection is
// one pg_dump per database and no roles: restoring roles beside some
// databases would DROP ROLE roles that other databases still use.
func (pgDumper) DumpCmds(s Scope) [][]string {
	if s.All {
		return [][]string{{"pg_dumpall", "-U", "postgres", "--clean", "--if-exists"}}
	}
	var out [][]string
	for _, db := range s.Databases {
		out = append(out, []string{"pg_dump", "-U", "postgres", "--create", "--clean", "--if-exists", db})
	}
	return out
}

func (pgDumper) RestoreCmd() []string {
	return []string{"psql", "-U", "postgres", "-d", "template1", "-v", "ON_ERROR_STOP=0"}
}

func (pgDumper) Env(password string) []string { return []string{"PGPASSWORD=" + password} }

// RestoreFailed tolerates exactly the errors pg_dumpall always causes on a
// running cluster: it emits CREATE ROLE postgres, and DROP ROLE for the role
// the restore runs as. Any other ERROR line fails the restore.
func (pgDumper) RestoreFailed(stderr string) bool {
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "ERROR:") {
			continue
		}
		if strings.Contains(line, `role "postgres" already exists`) || strings.Contains(line, "current user cannot be dropped") {
			continue
		}
		return true
	}
	return false
}

type mysqlDumper struct{ client, dump string }

func (m mysqlDumper) ListCmd() []string {
	return []string{m.client, "-uroot", "-N", "-e", "SHOW DATABASES"}
}

func (mysqlDumper) SystemDatabases() []string {
	return []string{"information_schema", "performance_schema", "mysql", "sys"}
}

func (m mysqlDumper) DumpCmds(s Scope) [][]string {
	common := []string{"--single-transaction", "--routines", "--events", "--triggers"}
	if s.All {
		return [][]string{append([]string{m.dump, "-uroot", "--all-databases"}, common...)}
	}
	cmd := append([]string{m.dump, "-uroot", "--databases"}, s.Databases...)
	cmd = append(cmd, "--add-drop-database")
	return [][]string{append(cmd, common...)}
}

func (m mysqlDumper) RestoreCmd() []string { return []string{m.client, "-uroot"} }

func (mysqlDumper) Env(password string) []string { return []string{"MYSQL_PWD=" + password} }

// RestoreFailed is false: mysql and mariadb stop at the first error and exit
// non-zero.
func (mysqlDumper) RestoreFailed(string) bool { return false }
