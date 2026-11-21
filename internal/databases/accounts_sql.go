package databases

import (
	"errors"
	"strings"
)

// Access is what a user may do in one logical database.
type Access string

const (
	AccessNone Access = "none"
	AccessRead Access = "read"
	AccessFull Access = "full"
)

// ErrBadUserName is a user name outside the rule.
var ErrBadUserName = errors.New("user names are letters, digits, _ and -, not starting with -, at most 63 characters (32 on MySQL and MariaDB)")

// ErrReservedUser is a name the panel keeps for the engine's own users.
type ErrReservedUser struct{ Name string }

func (e ErrReservedUser) Error() string { return e.Name + " is reserved" }

// reservedUsers are never created, whatever the engine and the case.
var reservedUsers = []string{"postgres", "root", "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys", "healthcheck", "public"}

// checkUserName validates the name of a user to create.
func checkUserName(engine, name string) error {
	if !targetPattern.MatchString(name) {
		return ErrBadUserName
	}
	if (engine == "mysql" || engine == "mariadb") && len(name) > 32 {
		return ErrBadUserName
	}
	if strings.HasPrefix(strings.ToLower(name), "pg_") {
		return ErrReservedUser{Name: name}
	}
	for _, r := range reservedUsers {
		if strings.EqualFold(name, r) {
			return ErrReservedUser{Name: name}
		}
	}
	return nil
}

// myGrantDB quotes a database name for GRANT ... ON db.*, where _ and % are
// wildcards: without the \ a grant on app_1 would also reach appx1.
func myGrantDB(name string) string { return myIdent(strings.ReplaceAll(name, "_", `\_`)) }

// accountSQL builds one engine's statements for logical databases, users and
// grants. Each []string is a list of statements to run in order; the
// database they run in is the caller's choice ("" is the engine default;
// Postgres uses postgres).
type accountSQL interface {
	// DatabasesQuery: rows name, size_bytes, owner_or_charset, public_connect
	// ("t"/"f", "" on MySQL). System databases and templates are left out.
	DatabasesQuery() string
	// UsersQuery: rows name; system users are left out.
	UsersQuery() string
	// AccessQuery: rows user, database, level ("full" or "read").
	AccessQuery() string
	// SuperuserRolesQuery: Postgres rows rolname of every superuser role.
	SuperuserRolesQuery() string
	CreateUser(name, password string) []string
	SetPassword(name, password string) []string
	SuperuserPassword(password string) []string
	CreateDatabase(name, owner string) []string
	Restrict(name string) []string
	DropDatabase(name string) []string
	// ServerAccess are the statements run on the default database.
	ServerAccess(user, db, owner string, from, to Access) []string
	// SchemasQuery lists the schemas of the database it is run in.
	SchemasQuery() string
	// InDatabaseAccess runs connected to db (Postgres only).
	InDatabaseAccess(user, owner string, schemas []string, to Access) []string
	// OwnedQuery: Postgres rows kind ("database" | "objects"), database.
	OwnedQuery(user string) string
	// OwnsInQuery: Postgres rows database, one for each database where the
	// user owns objects.
	OwnsInQuery(user string) string
	// PrivilegedDatabasesQuery: Postgres rows database where DROP OWNED has
	// something to remove.
	PrivilegedDatabasesQuery(user string) string
	DropUser(name string) []string
	DropOwned(name string) []string
}

// accountsFor returns the statements of an engine, ErrNoQuery for Redis.
func accountsFor(engine string) (accountSQL, error) {
	switch engine {
	case "postgres":
		return pgAccounts{}, nil
	case "mysql", "mariadb":
		return myAccounts{engine: engine}, nil
	}
	return nil, ErrNoQuery
}

type pgAccounts struct{}

// pgLogin is the roles the Users tab lists.
const pgLogin = `rolcanlogin AND rolname <> 'postgres' AND rolname NOT LIKE 'pg\_%'`

func (pgAccounts) DatabasesQuery() string {
	return `SELECT d.datname, pg_database_size(d.datname)::text, pg_get_userbyid(d.datdba)::text,
 CASE WHEN d.datacl IS NULL OR EXISTS (SELECT 1 FROM aclexplode(d.datacl) a WHERE a.grantee = 0 AND a.privilege_type = 'CONNECT') THEN 't' ELSE 'f' END
 FROM pg_database d WHERE NOT d.datistemplate ORDER BY d.datname`
}

func (pgAccounts) UsersQuery() string {
	return `SELECT rolname FROM pg_roles WHERE ` + pgLogin + ` ORDER BY rolname`
}

func (pgAccounts) AccessQuery() string {
	return `SELECT r.rolname, d.datname,
 CASE WHEN pg_has_role(r.oid, d.datdba, 'MEMBER') THEN 'full' ELSE 'read' END
 FROM pg_roles r CROSS JOIN pg_database d
 WHERE r.rolcanlogin AND r.rolname <> 'postgres' AND r.rolname NOT LIKE 'pg\_%' AND NOT d.datistemplate
 AND (pg_has_role(r.oid, d.datdba, 'MEMBER') OR EXISTS (SELECT 1 FROM aclexplode(d.datacl) a WHERE a.grantee = r.oid AND a.privilege_type = 'CONNECT'))
 ORDER BY r.rolname, d.datname`
}

func (pgAccounts) SuperuserRolesQuery() string { return `SELECT rolname FROM pg_roles WHERE rolsuper` }

func (pgAccounts) CreateUser(name, password string) []string {
	return []string{"CREATE ROLE " + pgIdent(name) + " LOGIN PASSWORD " + pgLit(password)}
}

func (pgAccounts) SetPassword(name, password string) []string {
	return []string{"ALTER ROLE " + pgIdent(name) + " PASSWORD " + pgLit(password)}
}

func (pgAccounts) SuperuserPassword(password string) []string {
	return []string{"ALTER ROLE postgres WITH PASSWORD " + pgLit(password)}
}

func (pgAccounts) CreateDatabase(name, owner string) []string {
	create := "CREATE DATABASE " + pgIdent(name)
	if owner != "" {
		create += " OWNER " + pgIdent(owner)
	}
	return []string{create, "REVOKE CONNECT ON DATABASE " + pgIdent(name) + " FROM PUBLIC"}
}

func (pgAccounts) Restrict(name string) []string {
	return []string{"REVOKE CONNECT ON DATABASE " + pgIdent(name) + " FROM PUBLIC"}
}

func (pgAccounts) DropDatabase(name string) []string {
	return []string{"DROP DATABASE " + pgIdent(name) + " WITH (FORCE)"}
}

// ServerAccess, Postgres. Full for a non-owner is membership in the owner
// role plus a per-database setting that makes every session of the user in
// that database act as the owner (SET role), so what it creates belongs to the
// owner and the owner's default privileges cover it. Leaving Full resets the
// setting first: a role setting naming a role the user is no longer a member
// of would break its logins. The reset is also sent when from is not Full,
// which cleans a stale setting and does nothing otherwise.
func (pgAccounts) ServerAccess(user, db, owner string, from, to Access) []string {
	u, d, o := pgIdent(user), pgIdent(db), pgIdent(owner)
	connect := "GRANT CONNECT ON DATABASE " + d + " TO " + u
	var out []string
	if to == AccessFull {
		out = append(out, connect)
		if user != owner {
			out = append(out, "GRANT "+o+" TO "+u,
				"ALTER ROLE "+u+" IN DATABASE "+d+" SET role = "+pgLit(owner))
		}
		return out
	}
	if user != owner {
		out = append(out, "ALTER ROLE "+u+" IN DATABASE "+d+" RESET role")
		if from == AccessFull {
			out = append(out, "REVOKE "+o+" FROM "+u)
		}
	}
	if to == AccessRead {
		return append(out, connect)
	}
	return append(out, "REVOKE ALL ON DATABASE "+d+" FROM "+u)
}

func (pgAccounts) SchemasQuery() string {
	return `SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('pg_catalog', 'information_schema') AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%' ORDER BY nspname`
}

func (pgAccounts) InDatabaseAccess(user, owner string, schemas []string, to Access) []string {
	if to == AccessFull {
		// A Full member acts as the owner in this database and may own
		// tables of its own: nothing is revoked from it.
		return nil
	}
	u, o := pgIdent(user), pgIdent(owner)
	var out []string
	if to == AccessRead {
		for _, s := range schemas {
			sc := pgIdent(s)
			out = append(out,
				"GRANT USAGE ON SCHEMA "+sc+" TO "+u,
				"GRANT SELECT ON ALL TABLES IN SCHEMA "+sc+" TO "+u,
				"GRANT SELECT ON ALL SEQUENCES IN SCHEMA "+sc+" TO "+u)
		}
		return append(out, "ALTER DEFAULT PRIVILEGES FOR ROLE "+o+" GRANT SELECT ON TABLES TO "+u)
	}
	// None: the Read-only grants go. Grants on objects a Full member lost
	// ownership of stay with the owner.
	for _, s := range schemas {
		sc := pgIdent(s)
		out = append(out,
			"REVOKE ALL ON ALL TABLES IN SCHEMA "+sc+" FROM "+u,
			"REVOKE ALL ON ALL SEQUENCES IN SCHEMA "+sc+" FROM "+u,
			"REVOKE ALL ON SCHEMA "+sc+" FROM "+u)
	}
	return append(out, "ALTER DEFAULT PRIVILEGES FOR ROLE "+o+" REVOKE ALL ON TABLES FROM "+u)
}

func pgRoleOID(user string) string {
	return "(SELECT oid FROM pg_roles WHERE rolname = " + pgLit(user) + ")"
}

func (pgAccounts) OwnedQuery(user string) string {
	return `SELECT 'database', datname FROM pg_database WHERE datdba = ` + pgRoleOID(user) + `
 UNION ALL
 SELECT 'objects', d.datname FROM pg_database d WHERE EXISTS (SELECT 1 FROM pg_shdepend s
 WHERE s.dbid = d.oid AND s.refclassid = 'pg_authid'::regclass AND s.refobjid = ` + pgRoleOID(user) + ` AND s.deptype = 'o')`
}

func (pgAccounts) OwnsInQuery(user string) string {
	return `SELECT d.datname FROM pg_database d WHERE EXISTS (SELECT 1 FROM pg_shdepend s
 WHERE s.dbid = d.oid AND s.refclassid = 'pg_authid'::regclass AND s.refobjid = ` + pgRoleOID(user) + ` AND s.deptype = 'o')
 ORDER BY d.datname`
}

func (pgAccounts) PrivilegedDatabasesQuery(user string) string {
	dep := `s.refclassid = 'pg_authid'::regclass AND s.refobjid = ` + pgRoleOID(user) + ` AND s.deptype IN ('a', 'd')`
	return `SELECT d.datname FROM pg_database d WHERE NOT d.datistemplate AND d.datallowconn AND (
 EXISTS (SELECT 1 FROM pg_shdepend s WHERE s.dbid = d.oid AND ` + dep + `)
 OR (d.datname = 'postgres' AND EXISTS (SELECT 1 FROM pg_shdepend s WHERE s.dbid = 0 AND ` + dep + `)))
 ORDER BY d.datname`
}

func (pgAccounts) DropUser(name string) []string  { return []string{"DROP ROLE " + pgIdent(name)} }
func (pgAccounts) DropOwned(name string) []string { return []string{"DROP OWNED BY " + pgIdent(name)} }

type myAccounts struct{ engine string }

// myAccount is a user's account: the panel only manages host '%'.
func myAccount(name string) string { return myLit(name) + "@'%'" }

// mySystemUsers are hidden from the Users tab (lower case).
var mySystemUsers = []string{"root", "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys", "healthcheck"}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = myLit(s)
	}
	return strings.Join(q, ", ")
}

func (m myAccounts) DatabasesQuery() string {
	return `SELECT s.SCHEMA_NAME, CAST(COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0) AS CHAR), s.DEFAULT_CHARACTER_SET_NAME, ''
 FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
 WHERE s.SCHEMA_NAME NOT IN (` + quoteList(mysqlDumper{}.SystemDatabases()) + `)
 GROUP BY s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME ORDER BY s.SCHEMA_NAME`
}

func (myAccounts) UsersQuery() string {
	return `SELECT User FROM mysql.user WHERE Host = '%' AND User <> '' AND LOWER(User) NOT IN (` + quoteList(mySystemUsers) + `) ORDER BY User`
}

// AccessQuery reads the schema grants of the '%' accounts. A GRANTEE is
// 'name'@'%'; the schema name holds the \_ a grant was written with.
func (myAccounts) AccessQuery() string {
	return `SELECT u, db, CASE WHEN ins > 0 THEN 'full' ELSE 'read' END AS lvl FROM (
 SELECT SUBSTRING(p.GRANTEE, 2, CHAR_LENGTH(p.GRANTEE) - 6) AS u,
 REPLACE(p.TABLE_SCHEMA, CONCAT(CHAR(92), '_'), '_') AS db,
 SUM(p.PRIVILEGE_TYPE = 'INSERT') AS ins, SUM(p.PRIVILEGE_TYPE = 'SELECT') AS sel
 FROM information_schema.SCHEMA_PRIVILEGES p
 WHERE RIGHT(p.GRANTEE, 5) = '''@''%'''
 GROUP BY u, db) x WHERE ins > 0 OR sel > 0 ORDER BY u, db`
}

func (myAccounts) SuperuserRolesQuery() string { return "" }

func (myAccounts) CreateUser(name, password string) []string {
	return []string{"CREATE USER " + myAccount(name) + " IDENTIFIED BY " + myLit(password)}
}

func (myAccounts) SetPassword(name, password string) []string {
	return []string{"ALTER USER " + myAccount(name) + " IDENTIFIED BY " + myLit(password)}
}

func (myAccounts) SuperuserPassword(password string) []string {
	p := myLit(password)
	return []string{"ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY " + p + ", 'root'@'localhost' IDENTIFIED BY " + p}
}

func (myAccounts) CreateDatabase(name, owner string) []string {
	out := []string{"CREATE DATABASE " + myIdent(name)}
	if owner != "" {
		out = append(out, "GRANT ALL ON "+myGrantDB(name)+".* TO "+myAccount(owner))
	}
	return out
}

func (myAccounts) Restrict(string) []string { return nil }

func (myAccounts) DropDatabase(name string) []string {
	return []string{"DROP DATABASE " + myIdent(name)}
}

func (myAccounts) ServerAccess(user, db, _ string, from, to Access) []string {
	var out []string
	g := myGrantDB(db) + ".*"
	if from != AccessNone {
		out = append(out, "REVOKE ALL PRIVILEGES ON "+g+" FROM "+myAccount(user))
	}
	switch to {
	case AccessRead:
		out = append(out, "GRANT SELECT, SHOW VIEW ON "+g+" TO "+myAccount(user))
	case AccessFull:
		out = append(out, "GRANT ALL ON "+g+" TO "+myAccount(user))
	}
	return out
}

func (myAccounts) SchemasQuery() string { return "" }

func (myAccounts) InDatabaseAccess(string, string, []string, Access) []string { return nil }

func (myAccounts) OwnedQuery(string) string { return "" }

func (myAccounts) OwnsInQuery(string) string { return "" }

func (myAccounts) PrivilegedDatabasesQuery(string) string { return "" }

func (myAccounts) DropUser(name string) []string {
	return []string{"DROP USER " + myAccount(name)}
}

func (myAccounts) DropOwned(string) []string { return nil }
