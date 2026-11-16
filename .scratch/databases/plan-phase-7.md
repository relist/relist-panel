# Databases phase 7 (Logical databases, users, superuser password reset) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin manages the logical databases and login users inside a SQL Database from two new tabs, Databases and Users, so each app gets its own database and user instead of the superuser. The Admin can also reset the superuser password of any Database, Redis included, without a crash ever locking the panel out.

**Architecture:**
- **One admin path over the network.** Every phase 7 statement goes through new `internal/dbquery` admin functions. They connect as the superuser to `relist-db-<id>` on `relist-network`, the way the Query panel does.
  - The password probe needs this path. Inside the container, Postgres trusts the local socket and MariaDB's `root@localhost` accepts `unix_socket`, so `docker exec` proves nothing about a password.
  - Postgres grants inside one database also need a connection to that database, which pgx gives with `Database`.
  - Restore stays on `docker exec`, as it is today.
- **`internal/databases`** gains:
  - `accounts_sql.go`: pure SQL builders per engine, nearly all of the risk, unit-tested.
  - `logical.go`: the Databases tab.
  - `users.go`: the Users tab.
  - `superuser.go`: the reset job and the pending-password recovery.
  - `user_store.go`: the `database_users` table and `databases.pending_password`.
  - An `Admin` dependency, faked in tests.
- **Superuser reset** is a job (`reset_password`). Its order (pending, live, current, recreate) is the spec's. Step 4 rewrites only the `Env` of the stored applied spec, because `reconcile` never reads `specFor`.
- **UI:**
  - two tabs, `DatabaseDatabasesTab` and `DatabaseUsersTab`;
  - on the Connection tab, a user picker and a Reset superuser password card;
  - in the Backups tab, a link from a `role "x" does not exist` restore to the Users tab.
- **Delivery:** one PR, branch `claude/databases-phase-7` on `claude/databases-phase-6a` (#88).

**Tech Stack:** Go, pgx v5, go-sql-driver/mysql, the existing RESP client in `internal/dbquery`, SQLite/goose, React 19, antd 6.5.1, vitest, Playwright.

**Spec:** `.scratch/databases/spec-2.md`, "Phase 7: Logical databases and users" and the Testing line for 7. Phases 1–6a still hold (`spec.md`, ADR-0039).

## Decisions beyond the spec (tell the user at handoff)

1. **Postgres Full is refused on a database owned by a superuser role** (every pre-phase-7 database, and `postgres` itself). Full means `GRANT <owner> TO <user>`, and `GRANT postgres TO app` hands the user everything `postgres` owns, in every database. Such a database offers None and Read-only only.
2. **The owning user stays at Full.** Its Access row is fixed at Full and cannot be lowered.
3. **A replace-restore keeps the database's ACL.** `ReplaceSQL` drops and recreates the target, and a one-database `pg_dump` carries no database ACL. Without this, a restore would reopen a restricted database to every user and drop the Read-only users' `CONNECT`.
4. **Dropping a database removes it from the Backup schedule's named list.** Otherwise every later scheduled run fails with "no such database". MySQL / MariaDB also revoke every user's grants on it, because their schema grants outlive the schema and would apply again to a new database with that name.
5. **`postgres` is never offered for Drop.** It is the Connection tab's default database.
6. **MySQL / MariaDB: only `'%'` accounts are listed.** Those are the only accounts the panel manages, and the tab says so.
7. **The phase 8 confirmation is skipped.** On Delete user, the spec's "panel users that use it" cannot exist yet, so phase 8 adds it.

## Global Constraints

- **Engines.** Only Postgres, MySQL and MariaDB have the Databases and Users tabs. The superuser reset covers all four engines.
- **Logical database names.** Use the restore target rule, `targetPattern` `^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`, and refuse the `neverTarget` names. Error: `ErrBadTarget` (existing text).
- **User names.**
  - The pattern is the same as for databases, at most 63 characters.
  - On MySQL / MariaDB it is at most 32: `ErrBadUserName`, "user names are letters, digits, _ and -, not starting with -, at most 63 characters (32 on MySQL and MariaDB)".
  - Reserved names, compared case-insensitively, give `ErrReservedUser{Name}`, "<name> is reserved":
    - every system user the spec lists: `postgres`, `root`, `mysql.sys`, `mysql.session`, `mysql.infoschema`, `mariadb.sys`, `healthcheck`;
    - `public`;
    - any name starting `pg_`.
  - Create database with **Create a user that owns it** checks the database name against the user rule too. A name over 32 characters on MySQL / MariaDB gets 422 before anything runs.
- **System users hidden from the Users tab.** Postgres: roles starting `pg_`, `postgres`, and every role without `rolcanlogin`. MySQL / MariaDB: the list above, and every account whose host is not `%`.
- **Generated passwords** come from the existing `newPassword()` (43 characters, base64url).
- **No secret in logs.**
  - No password ever appears in a response other than the credentials route, nor in an error, the audit, or argv.
  - Every error text from an admin call is passed through `scrub` for each password involved: the superuser's, and any new one.
  - Postgres admin connections set the startup parameters `log_min_error_statement=panic` and `log_statement=none`. Otherwise a failed `CREATE ROLE … PASSWORD '…'` is written to the container log, and the Logs tab shows it.
- **Quoting.**
  - Postgres uses `pgIdent` and `pgLit`; MySQL uses `myIdent` and `myLit`. A MySQL account is always `myLit(name)+"@'%'"`.
  - MySQL / MariaDB schema grants escape `_` as `\_` inside the backquotes (`myGrantDB`), because `_` and `%` are wildcards in `GRANT … ON db.*`.
  - Reading grants back un-escapes `\_`. Names can never hold `%` or `\`.
- **Access levels.** `type Access string`, with the values `"none"`, `"read"` and `"full"`. The statements are the spec's verbatim:
  - **Postgres Full:** owner, or `GRANT <owner> TO <user>`, plus `GRANT CONNECT ON DATABASE`.
  - **Postgres Read-only:**
    - `CONNECT`;
    - in that database, for every schema except `pg_catalog`, `information_schema`, and those starting `pg_toast` or `pg_temp`: `USAGE`, `SELECT ON ALL TABLES`, `SELECT ON ALL SEQUENCES`;
    - `ALTER DEFAULT PRIVILEGES FOR ROLE <owner> GRANT SELECT ON TABLES TO <user>`.
  - **MySQL / MariaDB Full:** `GRANT ALL ON \`db\`.*`. **Read-only:** `GRANT SELECT, SHOW VIEW ON \`db\`.*`.
- **Postgres new databases.** A new database gets `REVOKE CONNECT ON DATABASE … FROM PUBLIC`. That covers Create database and Restore into a new database.
- **Migration.** `internal/migrations/00050_database_users.sql` is the only new migration. Never edit 00041–00049.
  - New table `database_users`:
    - `database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE`
    - `name TEXT NOT NULL`
    - `password TEXT NOT NULL`
    - `created_at INTEGER NOT NULL`
    - `PRIMARY KEY (database_id, name)`
  - New column `databases.pending_password TEXT NOT NULL DEFAULT ''`.
  - Down drops both. Run Down once by hand.
- **Jobs.**
  - New kinds: `JobKindDropDatabase = "drop_database"` (drop with Back up first) and `JobKindResetPassword = "reset_password"`.
  - New Backup trigger `TriggerPreDrop = "pre_drop"`, labelled "Before drop" in the UI. Rotation never touches it.
- **Routes and audit actions,** added in `internal/authz/actions.go`. All are Admin only, and nothing is added to authz `Routes`. The target is the Database name, and the detail carries `database` or `user` (names only).

  | Route | Action |
  |---|---|
  | `GET /api/databases/{id}/logical-databases/details` | none |
  | `POST /api/databases/{id}/logical-databases` | `database.logical_create` |
  | `POST /api/databases/{id}/logical-databases/{name}/restrict` | `database.logical_restrict` |
  | `DELETE /api/databases/{id}/logical-databases/{name}` | `database.logical_drop` |
  | `GET /api/databases/{id}/users` | none |
  | `POST /api/databases/{id}/users` | `database.user_create` |
  | `PUT /api/databases/{id}/users/{name}/access` | `database.user_access` |
  | `POST /api/databases/{id}/users/{name}/reset-password` | `database.user_reset_password` |
  | `DELETE /api/databases/{id}/users/{name}` | `database.user_delete` |
  | `POST /api/databases/{id}/reset-password` | `database.reset_password` |

  The existing `GET /api/databases/{id}/credentials` takes `?user=<name>&database=<db>`. It adds `audit.Detail(ctx, "user", name)` and keeps `SecretRead`.
- **ADR.** `docs/adr/0044-logical-databases-and-users.md` (0043 is taken by #89).
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages for every component you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.
- **Commits.** Conventional Commits, no scope, with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Go tests.** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`.
- **Docker.** Only through the sandbox `DOCKER_HOST` in `.data/e2e/env`, never the host's default Docker.

## Review Focus

1. **A database name with `_` on MySQL / MariaDB.** Read-only on `app_1` must not reach `appx1`. Tests: `TestMyGrantEscapesUnderscore` (Task 2) and the two-database e2e (Task 8).
2. **Full for a database `postgres` owns.** The request is refused with `ErrFullOnSuperuserDB`, and no `GRANT postgres` statement is ever sent. Test: `TestSetAccessRefusesFullOnSuperuserOwned` (Task 4).
3. **Replace-restoring a restricted Postgres database.** It is still restricted afterwards, and its Read-only users can still connect. Tests: `TestParseDatACL` and `TestPgReplaceSQLKeepsACL` (Task 3).
4. **A crash between "set live" and "make current".** At the next start the new password is found and the container recreated. Neither working keeps it pending with a warning. Test: `TestResolvePendingOutcomes` (Task 5).
5. **A failed `CREATE ROLE` with a password.** The password is not in the container log nor in the error. Tests: `TestAdminPgConfigSilencesStatements` (Task 1) and `TestCreateUserErrorIsScrubbed` (Task 4).

---

### Task 1: Migration, store and the admin driver

**Files:**
- Create: `internal/migrations/00050_database_users.sql`, `internal/databases/user_store.go`, `internal/databases/user_store_test.go`, `internal/dbquery/admin.go`, `internal/dbquery/admin_test.go`
- Modify: `internal/databases/store.go` (the `PendingPassword` column; `Tombstone` also deletes the Database's `database_users` rows), `internal/databases/service.go` (`Deps.Admin`), `internal/databases/fakes_test.go` (`fakeAdmin`)

**Interfaces:**
- Produces, in `internal/dbquery`:
  - `func AdminExec(ctx context.Context, t Target, engine, database string, stmts []string) error` runs each statement on its own, in order, outside any transaction, and stops at the first error.
  - `func AdminRows(ctx context.Context, t Target, engine, database, query string) ([][]string, error)`: NULL becomes `""`.
  - `func Ping(ctx context.Context, t Target, engine string) error`: for `redis`, `AUTH` through `dialRedis`.
  - `func SetRedisPassword(ctx context.Context, t Target, password string) error`: `CONFIG SET requirepass`.
  - Timeouts: connect 5 s, each call 60 s.
- Produces, in `internal/databases`:
  - `type Admin interface { Exec(ctx, t dbquery.Target, engine, database string, stmts []string) error; Rows(ctx, t dbquery.Target, engine, database, query string) ([][]string, error); Ping(ctx, t dbquery.Target, engine string) error; SetRedisPassword(ctx, t dbquery.Target, password string) error }`
  - `Deps.Admin Admin`: nil means `engineAdmin{}`, which wraps the four dbquery functions.
  - `Database.PendingPassword string`.
  - `type ManagedUser struct { DatabaseID int64; Name, Password string; CreatedAt time.Time }`
  - `ErrUserNotManaged = errors.New("this user's password is unknown: use Reset password")`
  - `(*Store).UpsertUser(ctx, databaseID int64, name, password string) error`
  - `(*Store).GetUser(ctx, databaseID int64, name string) (ManagedUser, error)` (`ErrUserNotManaged` when absent)
  - `(*Store).ListUsers(ctx, databaseID int64) ([]ManagedUser, error)`
  - `(*Store).DeleteUser(ctx, databaseID int64, name string) error` (a missing row is fine)
  - `(*Store).SetPendingPassword(ctx, id int64, pw string) error`, `(*Store).PromotePending(ctx, id int64) error` (`password = pending_password, pending_password = ''`, only if pending is not empty), `(*Store).ClearPending(ctx, id int64) error`
- `fakeAdmin` records every `Exec` as `(database, stmts)`, answers `Rows` from a map keyed by query text, and takes per-password `Ping` results.

- [ ] **Step 1: Write the failing tests.**
  - `TestUserStoreRoundTrip`: upsert, get, list, upsert again replaces the password, delete.
  - `TestTombstoneRemovesUsers`.
  - `TestDeleteDatabaseCascadesUsers`.
  - `TestPendingPassword`: set pending, then promote; afterwards `Password` is the new one and `PendingPassword` is `""`. A promote with no pending changes nothing. Clear works.
  - `TestAdminPgConfigSilencesStatements` builds `adminPgConfig(t, "app")` and asserts:
    - `RuntimeParams["log_min_error_statement"] == "panic"`;
    - `RuntimeParams["log_statement"] == "none"`;
    - there is no `default_transaction_read_only`;
    - `Database == "app"`.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/ ./internal/dbquery/ -run 'UserStore|Tombstone|Cascades|Pending|AdminPgConfig'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement.**
  - The migration and the store methods.
  - `adminPgConfig(t Target, database string) (*pgx.ConnConfig, error)` beside `pgConfig`, with the same cancel handler.
  - MySQL uses `mysqlConfig` and `openConn`.
  - Postgres `Exec` uses `conn.Exec(ctx, stmt)` once per statement (simple protocol, so `CREATE DATABASE` is not inside a transaction).
- [ ] **Step 4: Run the Step 2 command.** Expected: PASS. Run goose Down then Up once on a scratch copy of a panel database.
- [ ] **Step 5: Commit.** `feat: store managed database users and a pending superuser password`

### Task 2: SQL builders per engine

**Files:**
- Create: `internal/databases/accounts_sql.go`, `internal/databases/accounts_sql_test.go`

**Interfaces:**
- Produces:
  - `type Access string` with `AccessNone`, `AccessRead` and `AccessFull`.
  - `func checkUserName(engine, name string) error`.
  - `func myGrantDB(name string) string`: `myIdent` of the name with `_` → `\_`.
  - `func accountsFor(engine string) (accountSQL, error)`: `ErrNoQuery` for Redis.
  - `type accountSQL interface` with the methods below. Each `[]string` is a list of statements to run in order; `db` is where they run (`""` means the engine default; Postgres uses `postgres`).

| Method | Returns |
|---|---|
| `DatabasesQuery() string` | rows `name, size_bytes, owner_or_charset, public_connect ("t"/"f"/"" for MySQL)`, system databases excluded (`SystemDatabases()` and templates) |
| `UsersQuery() string` | rows `name`, system users excluded (Global Constraints) |
| `AccessQuery() string` | rows `user, database, level` with level `full`/`read`. Postgres: `full` if `pg_has_role(user, datdba, 'MEMBER')`, else `read` if `aclexplode(datacl)` has `CONNECT` for the user. MySQL: from `information_schema.SCHEMA_PRIVILEGES` for `'<user>'@'%'`, un-escaping `\_`; `INSERT` present → `full`, else `SELECT` → `read` |
| `SuperuserRolesQuery() string` | Postgres: rows `rolname` with `rolsuper`; MySQL: `""` (not used) |
| `CreateUser(name, password string) []string` | PG `CREATE ROLE "n" LOGIN PASSWORD '…'`; My `CREATE USER 'n'@'%' IDENTIFIED BY '…'` |
| `SetPassword(name, password string) []string` | PG `ALTER ROLE … PASSWORD`; My `ALTER USER 'n'@'%' IDENTIFIED BY` |
| `SuperuserPassword(password string) []string` | PG `ALTER ROLE postgres WITH PASSWORD '…'`; My one statement `ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY '…', 'root'@'localhost' IDENTIFIED BY '…'` |
| `CreateDatabase(name, owner string) []string` | PG `CREATE DATABASE "n"` [`OWNER "o"`] then `REVOKE CONNECT ON DATABASE "n" FROM PUBLIC`; My `CREATE DATABASE \`n\`` and, with an owner, `GRANT ALL ON <myGrantDB(n)>.* TO 'o'@'%'` |
| `Restrict(name string) []string` | PG the `REVOKE` above; My nil |
| `DropDatabase(name string) []string` | PG `DROP DATABASE "n" WITH (FORCE)`; My `DROP DATABASE \`n\`` |
| `ServerAccess(user, db, owner string, from, to Access) []string` | statements on the default database (see below) |
| `SchemasQuery() string` | PG: non-system schemas of the connected database; My `""` |
| `InDatabaseAccess(user, owner string, schemas []string, to Access) []string` | PG only, run connected to `db`; My nil |
| `OwnedQuery(user string) string` | PG rows `kind, database`: `database` for each `datdba` match, `objects` for each database with a `pg_shdepend` row of `deptype = 'o'` for the role. My `""` |
| `PrivilegedDatabasesQuery(user string) string` | PG: the databases with a `pg_shdepend` row of `deptype IN ('a','d')` for the role, plus `postgres` when one has `dbid = 0`. My `""` |
| `DropUser(name string) []string` | PG `DROP ROLE "n"`; My `DROP USER 'n'@'%'` |
| `DropOwned(name string) []string` | PG `DROP OWNED BY "n"` (run in each privileged database); My nil |

- **`ServerAccess`, Postgres:**
  - to `full`: `GRANT CONNECT ON DATABASE`, then `GRANT "owner" TO "user"`.
  - to `read`: `REVOKE "owner" FROM "user"` (only when `from` is `full`), then `GRANT CONNECT`.
  - to `none`: `REVOKE "owner" FROM "user"` (only when `from` is `full`), then `REVOKE ALL ON DATABASE … FROM "user"`.
- **`ServerAccess`, MySQL:**
  - `REVOKE ALL PRIVILEGES ON <g>.* FROM 'u'@'%'` only when `from` is not `none`;
  - then the spec's `GRANT` for `read` or `full`.
- **`InDatabaseAccess`, Postgres:**
  - `read`: for each schema, `GRANT USAGE ON SCHEMA`, `GRANT SELECT ON ALL TABLES IN SCHEMA` and `GRANT SELECT ON ALL SEQUENCES IN SCHEMA`; then once, `ALTER DEFAULT PRIVILEGES FOR ROLE "owner" GRANT SELECT ON TABLES TO "user"`.
  - `none` and `full`: the matching `REVOKE ALL` on tables, sequences and schemas, and `ALTER DEFAULT PRIVILEGES FOR ROLE "owner" REVOKE ALL ON TABLES FROM "user"`. A Full member gets everything through the owner role, so the leftover Read-only grants go.

- [ ] **Step 1: Write the failing table tests.**
  - `TestCheckUserName`:
    - accepted: `app`, `app-1`, `a_b`;
    - refused: `-x`, `pg_x` (pg), `Root` (mysql);
    - a 33-character name is refused on mysql and mariadb and accepted on postgres;
    - a 64-character name is refused everywhere.
  - `TestMyGrantEscapesUnderscore`: `myGrantDB("app_1") == "`app\\_1`"`, and MySQL `ServerAccess("u","app_1","",AccessNone,AccessRead)` equals ``[]string{"GRANT SELECT, SHOW VIEW ON `app\\_1`.* TO 'u'@'%'"}``.
  - `TestPgServerAccess`: all nine `from`/`to` pairs, with exact statements for `none→full` and `full→read`.
  - `TestPgInDatabaseRead`: schemas `public` and `sales` give six `GRANT`s and one `ALTER DEFAULT PRIVILEGES`, in that order.
  - `TestQuotingHostileNames`: a user `a"b` on Postgres comes out as `"a""b"`, and a password with `'` and `\` on MySQL is escaped by `myLit`. These names can't pass `checkUserName`; the test only proves the builders are safe on their own.
  - `TestSuperuserPasswordMySQLOneStatement`: `len(...) == 1`, and the statement contains both `'root'@'%'` and `'root'@'localhost'`.
  - `TestPgCreateDatabaseRevokes`: the last statement is `REVOKE CONNECT ON DATABASE "n" FROM PUBLIC`, with and without an owner.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/ -run 'UserName|MyGrant|PgServer|PgInDatabase|Quoting|SuperuserPassword|PgCreateDatabase'`. Expected: FAIL.
- [ ] **Step 3: Implement** `pgAccounts{}` and `myAccounts{engine string}` (MariaDB differs only in the system list, and reuses `mysqlDumper.SystemDatabases()`).
- [ ] **Step 4: Run the Step 2 command.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: build per-engine sql for databases, users and grants`

### Task 3: Databases tab service and restore ACL

**Files:**
- Create: `internal/databases/logical.go`, `internal/databases/logical_test.go`
- Modify:
  - `internal/databases/backup.go`: split `acquire`. The new `begin` does the gate, lock, get, tombstone, busy, running and `beginJob` steps; `acquire` = `begin` + `DataDir` + `DumperFor`.
  - `internal/databases/backup_store.go`: `TriggerPreDrop`.
  - `internal/databases/jobs.go`: the two job kinds; update the `Jobs` comment.
  - `internal/databases/dumps.go`: Postgres `InspectSQL` and `ReplaceSQL`.
  - `internal/databases/schedule_store.go`: `RemoveScheduledDatabase`.
  - `internal/databases/restore_test.go`.

**Interfaces:**
- Consumes: `Admin` (Task 1); `accountsFor`, `checkUserName` and `Access` (Task 2).
- Produces:
  - `type LogicalDatabase struct { Name string; SizeBytes int64; Owner, Charset string; AnyUserCanConnect, Protected bool }`. `Protected` covers `postgres`; Drop refuses it.
  - `func (s *Service) adminTarget(ctx context.Context, id int64) (Database, dbquery.Target, accountSQL, error)` is `queryTarget(ctx, id, false)`, plus `ErrBusy` while any job runs, plus `accountsFor`.
  - `func (s *Service) LogicalDatabases(ctx, id int64) ([]LogicalDatabase, error)`
  - `func (s *Service) CreateLogicalDatabase(ctx, id int64, name string, withOwner bool) error`
    - It refuses an existing name (`ErrDatabaseExists{Name}`, "a database named <n> already exists", 409) and, with an owner, an existing user (`ErrUserExists{Name}`, "a user named <n> already exists", 409).
    - With an owner, the order is: `CreateUser`, `UpsertUser`, then `CreateDatabase`. A failure after `CreateUser` runs `DropUser` and `DeleteUser`.
  - `func (s *Service) RestrictLogicalDatabase(ctx, id int64, name string) error`: Postgres only, otherwise `ErrNoQuery`.
  - `func (s *Service) DropLogicalDatabase(ctx, id int64, name, confirm string, backupFirst bool) error`
    - `confirm` must equal `name` (`ErrConfirm`).
    - It refuses `Protected` names and names that do not exist: `ErrProtectedDatabase{Name}`, "<n> cannot be dropped", 422.
    - Without a backup it runs synchronously and returns the error.
    - With a backup it works like `UpdateImage`: `acquire(…, JobKindDropDatabase)`, then `backupNow(…, name, TriggerPreDrop, nil)`. A failed Backup sets `LastError` to "the pre-drop backup failed, so <n> was not dropped: …".
    - After a successful drop:
      - MySQL / MariaDB: `REVOKE ALL PRIVILEGES` on it for every user `AccessQuery` lists for it, run before the drop.
      - `RemoveScheduledDatabase(ctx, id, name)`.
  - `func (s *Store) RemoveScheduledDatabase(ctx, databaseID int64, name string) error`: removes the name from `databases` JSON. Nothing changes when the schedule covers all databases or has no row.
  - Postgres `InspectSQL(target)` now prints one line, `<owner>\t<datacl::text or empty>`. `ReplaceSQL(target, prev)`:
    - recreates with that owner;
    - with an empty ACL, the target is new or default, so it appends the `REVOKE CONNECT … FROM PUBLIC` only when `prev == ""`;
    - with an ACL, it appends `REVOKE ALL ON DATABASE … FROM PUBLIC`, then one `GRANT` per entry (`c`→CONNECT, `C`→CREATE, `T`→TEMPORARY; an empty grantee is PUBLIC), skipping the owner's own entry.
  - `func parseDatACL(acl string) []aclEntry`, with `type aclEntry struct{ Grantee, Privs string }`, un-quoting `"a""b"`.

- [ ] **Step 1: Write the failing tests** (fake Host running, fake Admin):
  - `TestLogicalDatabasesPostgres`: the rows `app|8192|app|f` and `postgres|7000|postgres|t` give `AnyUserCanConnect` false and true, and `postgres` has `Protected`.
  - `TestCreateWithOwnerPostgres`: the Exec order is `CREATE ROLE`, then `CREATE DATABASE … OWNER`, then `REVOKE`. A managed row exists, and its password is 43 characters.
  - `TestCreateWithOwnerRollsBack`: `CREATE DATABASE` fails. Then `DROP ROLE "app"` is sent, no managed row is left, and the error does not contain the generated password.
  - `TestCreateMySQLOwnerTooLong`: a 40-character name with an owner on mysql gives `ErrBadUserName`, and nothing is sent.
  - `TestDropProtectedRefused` (`postgres`) and `TestDropConfirm`.
  - `TestDropBackupFirstFailureKeepsDatabase`: the Backup fails, no `DROP` is sent, and `LastError` starts "the pre-drop backup failed".
  - `TestDropRemovesFromSchedule`: the schedule `["app","web"]` becomes `["web"]`.
  - `TestDropMySQLRevokesGrants`.
  - `TestParseDatACL`: `{=c/postgres,postgres=CTc/postgres,app=c/postgres,"a""b"=c/postgres}` gives four entries, and the last has grantee `a"b`.
  - `TestPgReplaceSQLKeepsACL`: prev `app\t{=Tc/app,app=CTc/app,ro=c/app}` gives `REVOKE ALL … FROM PUBLIC`, `GRANT TEMPORARY, CONNECT … TO PUBLIC` and `GRANT CONNECT … TO "ro"`. Prev `app\t` gives no `REVOKE`. Prev `""` gives `REVOKE CONNECT … FROM PUBLIC`.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/`. Expected: the new tests fail, and the existing restore tests still pass (fix their expected `InspectSQL` text if they pin it).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the whole package. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: list, create, restrict and drop logical databases`

### Task 4: Users service

**Files:**
- Create: `internal/databases/users.go`, `internal/databases/users_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`adminTarget`, the store, the builders).
- Produces:
  - `type DBUser struct { Name string; Managed bool; Owns []string; Access map[string]Access }`. `Owns` lists the databases the user owns (Postgres `datdba`); their Access is fixed at Full.
  - `func (s *Service) Users(ctx, id int64) ([]DBUser, error)`: the live `UsersQuery` merged with `ListUsers`. A managed row whose user no longer exists is left alone and not listed.
  - `func (s *Service) CreateUser(ctx, id int64, name string) error`: refuses an existing user, then `CreateUser` and `UpsertUser`. If the insert fails, it sends `DropUser`.
  - `func (s *Service) ResetUserPassword(ctx, id int64, name string) error`: refuses an unknown user, then `UpsertUser(new)`, then `SetPassword`. On failure it puts back the old row, or deletes it if there was none.
  - `func (s *Service) SetUserAccess(ctx, id int64, name string, access map[string]Access) error`
    - Every key must be a live database (`ErrUnknownLogical`).
    - Postgres, per database, reads `from` from `AccessQuery` and the owner from `DatabasesQuery`.
    - The owner's own database must stay `full`: `ErrOwnerAccess{User, Database}`, "<u> owns <db>; an owner always has Full access", 422.
    - `full` on a database whose owner is in `SuperuserRolesQuery` gives `ErrFullOnSuperuserDB{Database}`, "<db> is owned by a superuser, so Full access would make the user one; give it Read-only, or create the database with an owning user", 422.
    - All checks run before any statement.
    - For each database: `ServerAccess`, then on Postgres, with `SchemasQuery` run on that database, `InDatabaseAccess` connected to it. Every listed database is applied again, unchanged levels included.
  - `func (s *Service) DeleteUser(ctx, id int64, name string) error`
    - Postgres: rows from `OwnedQuery` give `ErrUserOwns{User; Databases, ObjectsIn []string}`, "<u> owns databases: a; owns objects in: b. Give them another owner or drop them first", 409.
    - Otherwise `DropOwned` in each of `PrivilegedDatabasesQuery`, then `DropUser`, then `DeleteUser`.
    - MySQL: `DropUser`, then `DeleteUser`.
  - `func (s *Service) UserCredentials(ctx, id int64, name string) (string, error)`: from the store, otherwise `ErrUserNotManaged` (409).
- Every error from `Admin` is passed through `scrub` with the superuser password and any password generated in that call.

- [ ] **Step 1: Write the failing tests.**
  - `TestUsersMergesManaged`: live `app`, `legacy`; managed `app`, `gone`. The result is `app` (managed) and `legacy` (not managed).
  - `TestSetAccessRefusesFullOnSuperuserOwned`: the owner is `postgres`, and no `Exec` is recorded.
  - `TestSetAccessOwnerFixed`.
  - `TestSetAccessReadPostgres`: the `Exec` calls are: on `postgres`, `GRANT CONNECT`; then on `app`, the `InDatabaseAccess` list.
  - `TestSetAccessUnknownDatabase`.
  - `TestDeleteUserOwnsRefused`: the error names both lists, and nothing is dropped.
  - `TestDeleteUserDropsOwnedFirst`: privileged `app` and `postgres` give `DROP OWNED` on each, then `DROP ROLE`.
  - `TestResetUserPasswordRestoresRowOnFailure`.
  - `TestCreateUserErrorIsScrubbed`: the fake Admin fails with a message that contains the statement. The returned error contains `***` and not the password.
- [ ] **Step 2: Run** the `users_test.go` tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the package. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: manage database users and their access`

### Task 5: Superuser password reset and recovery

**Files:**
- Create: `internal/databases/superuser.go`, `internal/databases/superuser_test.go`
- Modify:
  - `internal/databases/service.go`: `Start` resolves a pending password after reconcile; `view` adds the pending warning; the `pendingRetry`/`pendingTries` fields.
  - `internal/httpapi/httpapi.go`: call `ResolvePendingPasswords` after `ReconcileAll`.

**Interfaces:**
- Consumes: `begin` (Task 3), `Admin`, the store (Task 1), `accountsFor(...).SuperuserPassword` (Task 2).
- Produces:
  - `ErrPendingPassword = errors.New("a previous superuser password change is not settled yet; it is retried when the Database starts")` (409).
  - `func (s *Service) ResetSuperuserPassword(ctx, id int64, confirm string) error`
    - `confirm` must equal the Database name.
    - A pending password gives `ErrPendingPassword`.
    - It takes `begin(ctx, id, JobKindResetPassword)`, so Redis is allowed. It answers at once and runs in the background:
      1. `SetPendingPassword(new)`.
      2. Set it live: Redis `SetRedisPassword`; otherwise `Exec(SuperuserPassword(new))` as the old password. Then `Ping` as the new one.
         - If step 2 errored but `Ping(new)` works, carry on.
         - If `Ping(new)` fails: `ClearPending`, and set `LastError` to "the superuser password was not changed: <scrubbed>".
      3. `PromotePending`.
      4. Under the Database's lock, re-read it, then:
         - `spec := appliedSpec(d)`;
         - `spec.Env = swapEnv(spec.Env, eng.Env(new))`;
         - `saveApplied`, then `reconcile` (the hash differs, so it recreates).
         - A failure here lands in `LastError` through `record`, and Repair retries it with the saved spec.
  - `func swapEnv(env, want []string) []string` replaces each entry whose key (the text before `=`) matches a `want` entry, and appends any `want` key that is missing.
  - `func (s *Service) ResolvePendingPasswords(ctx context.Context) error`: for each live Database with a pending password and `Desired == running`, call `resolvePending`.
  - `func (s *Service) resolvePending(ctx context.Context, id int64)` tries up to `pendingTries` (24) times, `pendingRetry` (5 s) apart, while the container is not running or neither `Ping` succeeds:
    - `Ping(new)` works: `PromotePending`, then step 4.
    - `Ping(old)` works: `ClearPending`.
    - Neither: keep pending.
  - The `View.Warnings` text while a password is pending: "A superuser password change was interrupted and is not settled yet. It is retried when the panel or this Database starts."

- [ ] **Step 1: Write the failing tests** (fakes; set `pendingRetry` to 1 ms):
  - `TestResetSuperuserPostgres`: the `Exec` holds `ALTER ROLE postgres`, and after `Wait()`:
    - the password is new and pending is empty;
    - the applied spec's `Env` holds `POSTGRES_PASSWORD=<new>`;
    - the container was recreated once;
    - `SettingsOutdated` is false.
  - `TestResetSuperuserRedis`: `SetRedisPassword` is called, and no Dumper is needed.
  - `TestResetSuperuserLiveFails`: pending is cleared, the password is unchanged, and `LastError` holds neither password.
  - `TestResetSuperuserConfirm`, and `TestResetSuperuserBusy` (another job is running).
  - `TestSwapEnv`: a key is replaced, other entries are kept, and a missing key is appended.
  - `TestResolvePendingOutcomes`, a table of three cases:
    - new works: promoted and recreated;
    - old works: cleared and not recreated;
    - neither: still pending, and the view warning is present.
  - `TestStartResolvesPending`.
- [ ] **Step 2: Run** the `superuser_test.go` tests. Expected: FAIL.
- [ ] **Step 3: Implement.** Wire `ResolvePendingPasswords` in `httpapi.go` right after the `ReconcileAll` call, logging any error.
- [ ] **Step 4: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/ ./internal/httpapi/`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: reset the superuser password without locking the panel out`

### Task 6: HTTP, audit, ADR and context

**Files:**
- Create: `internal/databases/users_http.go`, `internal/databases/users_http_test.go`, `docs/adr/0044-logical-databases-and-users.md`
- Modify:
  - `internal/databases/http.go`: the routes; `writeError` mappings; `credentials` with `user` and `database`.
  - `internal/databases/engines.go`: `UserURL`.
  - `internal/authz/actions.go`.
  - `CONTEXT.md`.

**Interfaces:**
- Consumes: Tasks 3–5.
- Produces:
  - `Engine.UserURL(host, user, password, database string) string`; `URL` calls it with `User()`.
  - JSON:
    - `LogicalDatabaseJSON{name, sizeBytes, owner, charset, anyUserCanConnect, protected}`
    - `DatabaseUserJSON{name, managed, owns: [], access: {db: "none"|"read"|"full"}}`
  - Request bodies:
    - `POST logical-databases` `{name, createOwner}`
    - `DELETE logical-databases/{name}` `{confirm, backupFirst}`
    - `POST users` `{name}`
    - `PUT access` `{access: {…}}`
    - `POST reset-password` `{confirm}`
  - Answers: `201` for create, `202` for Drop with Back up first and for the superuser reset, `204` otherwise. No body ever carries a password.
  - `credentials?user=u&database=db`:
    - `user` empty or the engine's `User()`: as today.
    - Otherwise `UserCredentials`; `database` must match `targetPattern` (otherwise 422) and defaults to the engine default.
    - The response adds `"user"`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUsersRoutesAudit`: each mutating route records its action, the target is the Database name, and the detail holds `user`/`database` but no `password`.
  - `TestCredentialsForManagedUser`: the URL is `postgres://app:<pw>@relist-db-1:5432/app`, `secret.read` is emitted, and the detail has `user=app`.
  - `TestCredentialsUnmanagedUser` gives 409.
  - `TestCreateUserResponseHasNoPassword`.
  - `TestUserURLRedis`: Redis `UserURL` keeps the empty user, as `URL` does.
  - Error codes: `ErrFullOnSuperuserDB` and `ErrBadUserName` give 422; `ErrUserOwns`, `ErrUserExists` and `ErrPendingPassword` give 409.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
  - **ADR-0044** records:
    - the admin path over the network, and why;
    - managed users;
    - Full as owner membership, and why it is refused on superuser-owned databases;
    - the MySQL `\_` escape;
    - the pending-password order and recovery;
    - that a replace-restore keeps the database ACL.
  - **`CONTEXT.md`:**
    - add **Managed user** ("a login user of a Database whose password the panel stores, because it created or reset it") and **Access** ("None, Read-only or Full, per user per logical database");
    - add `pre_drop` to the Backup triggers.
- [ ] **Step 4: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: serve databases and users routes with audit`

### Task 7: UI

**Files:**
- Create: `web/src/pages/DatabaseDatabasesTab.tsx`, `web/src/pages/DatabaseDatabasesTab.test.tsx`, `web/src/pages/DatabaseUsersTab.tsx`, `web/src/pages/DatabaseUsersTab.test.tsx`
- Modify:
  - `web/src/pages/DatabasePage.tsx`: tabs; a controlled `activeKey` from `?tab=`; the Connection tab user picker and Reset superuser password card.
  - `web/src/pages/DatabasePage.test.tsx`.
  - `web/src/pages/DatabaseBackupsTab.tsx` and its test.
  - `web/src/pages/databases-view.ts`: trigger and job labels.
  - `web/src/data/types.ts`, `web/src/data/api.ts`.

**Interfaces:**
- Consumes: the Task 6 JSON and routes.
- Tab order for SQL engines: Connection, Databases, Users, Query, Backups, Logs, Settings. Redis is unchanged.

- [ ] **Step 0: Read** `https://ant.design/llms.txt`, then Table, Modal, Form, Checkbox, Select, Segmented and Popconfirm for antd 6.5.1.
- [ ] **Step 1: Write the failing vitest tests.**
  - **Databases tab:**
    - It lists the size and the owner (pg) or the charset (mysql).
    - The **Any user can connect** tag and **Restrict** appear only when `anyUserCanConnect`.
    - Create: the **Create a user that owns it** tick is on by default.
    - Drop:
      - the button is disabled until the typed name matches;
      - **Back up first** is ticked by default;
      - there is no Drop for `protected`;
      - when the schedule names the database, the dialog says "It is also removed from the Backup schedule."
  - **Users tab:**
    - A user that is not managed shows "password unknown" and **Reset password**.
    - The Access dialog has one None/Read-only/Full `Segmented` per database. The owner's row is fixed at Full.
    - Full is disabled, with a tooltip, on a database owned by `postgres`.
    - On Postgres, choosing Full shows "A member gets everything the owner role has, in every database it owns."
    - The note "Only accounts on host % are shown." appears on MySQL / MariaDB.
    - Delete shows the 409 error text.
  - **Connection tab:**
    - The user picker lists the superuser and the managed users, and Reveal fetches `credentials?user=app&database=app`. The database is the user's same-named one if it has access to it, else the first database it can access, else the default.
    - Reset superuser password: the button is disabled until the typed name matches. The dialog says that apps using the superuser password stop connecting until given the new one, and that the Database restarts.
  - **Backups tab:** a failed restore whose log has `role "app" does not exist` shows "Create the user app on the Users tab", which goes to `?tab=users`.
  - **Labels:** `pre_drop` is "Before drop", `drop_database` is "Dropping a database", and `reset_password` is "Resetting the password".
- [ ] **Step 2: Run** `cd web && pnpm vitest run src/pages/Database`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `cd web && pnpm vitest run && pnpm tsc --noEmit && pnpm lint`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add databases and users tabs and superuser password reset`

### Task 8: End-to-end

**Files:**
- Create: `e2e/tests/databases-users.spec.ts`

**Sandbox:** this worktree's sandbox is the old socket-style one and is down. Before `task e2e:serve` here, ask the user, or wait until #91 is merged. Clients run as `docker run --rm --network relist-network <engine image> …` against `relist-db-<id>`, with images the Database already pulled. Check the network name against `e2e/tests/databases*.spec.ts`.

- [ ] **Step 1: Write the spec.** Every step goes through the panel UI or API, and is checked with a client.
  - **Postgres 18:**
    - create the databases `app1` and `app2` with owners, and a user `reader` with Read-only on `app1`;
    - `app1` can `CREATE TABLE` and `INSERT` in `app1`;
    - `reader` can `SELECT` in `app1`, its `INSERT` is refused, and it can `SELECT` from a table `app1` creates after the grant (default privileges);
    - `app1` cannot connect to `app2`;
    - deleting `reader` succeeds;
    - Restore `app1`'s Backup over `app1`: `app2`'s user still cannot connect to it.
  - **MySQL 8.4 and MariaDB 11.8** (parametrised):
    - databases `app_1` and `appx1`; `reader` Read-only on `app_1`;
    - `reader` reads `app_1` and is refused on `appx1`;
    - its `INSERT` into `app_1` is refused;
    - Full lets it write.
  - **Superuser reset, Postgres, MySQL and Redis:**
    - write a row (or key), reset, and wait for the job to end;
    - the old URL is refused, the new URL from Reveal works, and the row is still there;
    - the Databases list shows no warning;
    - the Logs tab text contains neither password.
- [ ] **Step 2: Run** `task e2e -- databases-users`. Expected: PASS. Then run the whole `task e2e` once.
- [ ] **Step 3: Commit.** `test: cover database users and superuser reset end to end`

Open the PR against `claude/databases-phase-6a` with the title `feat: databases phase 7, logical databases, users and superuser reset`. Its body lists the seven decisions above.
