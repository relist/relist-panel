# Databases phase 8 (Per-Database access for non-Admins, and Preview) Implementation Plan

> **Dropped 2026-11-19 (ADR-0049), except Preview.** Databases are Admin-only; the grants and their protections were removed on `claude/databases-admin-only`. Kept for the record.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin gives a panel user who is not an Admin access to one Database. The user can then see it under **Data → Databases** and work in its Query panel or console:
- on SQL engines, as one of the Database's managed users;
- on Redis, at a level, Read or Read-write.

Admins and these users also get **Preview**, a dry run of data-changing SQL.

**Architecture:**
- **Storage.** A new table `database_grants` holds one row per panel user per Database. Foreign keys remove a row when its panel user, its managed user or its Database goes. A tombstone removes the rows explicitly.
- **authz.**
  - A new level, `LevelDatabase`, reads `{id}` after `databases` and looks the grant up directly in SQL, the way `parentsMatch` reads parents. It puts an `authz.DatabaseGrant` in the context.
  - A non-Admin with no grant gets 404. Admins never carry a grant.
- **Who connects.** The HTTP layer turns the grant into a caller and passes it to every Query, Schema, Redis and credentials call:
  - an SQL grantee's `dbUser`;
  - a Redis grantee's level.

  An SQL grantee always connects as its managed user with the stored password. It never gets a `dbquery.Target` that holds the superuser password.
- **Server-wide check.** A managed user with server-wide powers cannot back a grant. The check runs:
  - at grant time, over the admin path;
  - before every grantee run, in the run's own session.
- **Redis grantees** go through a stricter classifier, `ClassifyGrantee`. It refuses the dangerous list outright, with no confirm, and refuses every command flagged `admin` or in the `@admin` / `@dangerous` categories.
- **Preview** runs DML-only batches between `BEGIN` / `START TRANSACTION` and `ROLLBACK`.
  - On Postgres each statement is split by a new `SplitPostgres` and sent alone over the extended protocol, so a mis-split fails instead of running a hidden `COMMIT`.
- **UI.**
  - Non-Admins with grants see Data → Databases with only their Databases. A Database page shows only Connection (SQL) and Query, or Console (Redis).
  - Admins get a **Panel access** tab.
- **Delivery:** one PR. The branch is `claude/databases-phase-8`, on `claude/databases-phase-7` (#92).

**Tech Stack:** Go, pgx v5, go-sql-driver/mysql, the RESP client in `internal/dbquery`, SQLite/goose, React 19, antd 6.5.1, vitest, Playwright.

**Spec:** `.scratch/databases/spec-2.md`: "Phase 8: Per-Database access for non-Admins", "Preview (dry-run) in the SQL Query panel", and the Testing line for 8. Phases 1–7 still hold (`spec.md`, ADR-0039, ADR-0044).

## Decisions beyond the spec (tell the user at handoff)

1. **Redis grantees are refused admin and dangerous commands.**
   - The console refuses every command (or subcommand) flagged `admin`, or in the ACL categories `@admin` or `@dangerous`, and the dangerous list, with no confirm.
   - The spec's Read allowlist includes `CONFIG GET`, and `CONFIG GET requirepass` answers with the superuser password.
   - This also takes `INFO`, `KEYS`, `CLIENT LIST`, `MIGRATE`, `SLOWLOG` and `LATENCY` from grantees. The key browser uses `SCAN`, so it still works.
2. **Scripts are refused in the console for everyone, Admins included.** That covers `EVAL`, `EVALSHA`, `EVAL_RO`, `EVALSHA_RO`, `FCALL`, `FCALL_RO`, `FUNCTION`, `SCRIPT` and `MODULE`.
   - The spec says they are "never allowed in the console".
   - Today an Admin with writes on can run `EVAL`, which also gets around the dangerous list (`redis.call('FLUSHALL')`).
3. **A managed user with server-wide powers cannot back a grant.**
   - **Postgres:** superuser, `CREATEROLE`, or membership in `pg_read_server_files`, `pg_write_server_files` or `pg_execute_server_program`.
   - **MySQL / MariaDB:** any `SHOW GRANTS` line other than `USAGE ON *.*` or a schema / table grant without `WITH GRANT OPTION`. That covers global privileges, roles and `PROXY`.
   - An Admin can make such a user in the Query panel, and Reset password makes it managed.
   - The check runs at grant time (422), and again before every grantee run, because privileges can change later.
4. **Only non-Admins can be given a grant (422).** A user given a grant and later made Admin keeps the row, which then does nothing.
5. **The tab is called Panel access.** The Users tab already uses Access for None / Read-only / Full.
   - The CONTEXT.md term is **Database grant**.
   - The audit actions are `database.grant_set` and `database.grant_delete`.
6. **A grantee's database list is the logical databases where its user's Access is not None,** the same rule the Users tab uses. Postgres databases that any user can connect to are not listed for it.
7. **A grantee's Database JSON leaves out admin-only details.** It has no `lastError`, no `warnings`, `settingsOutdated` is false, and memory and CPU are zero. `user` is the grantee's DB user, or empty on Redis, and a new `access` field carries the grant.
8. **MySQL / MariaDB Preview only checks tables the running user can see** for a storage engine other than InnoDB. A grantee is not told the table names of schemas it cannot read.
9. **An SQL grant needs the Database running,** because the server-wide check connects to it (409 otherwise).
10. **A grantee can change its own DB password** (`ALTER ROLE … PASSWORD`, `SET PASSWORD`). Stopping that would need statement filtering, which the spec rejects. Its runs then fail to log in until an Admin uses Reset password. ADR-0045 records this.

## Global Constraints

- **No grantee path builds a Target with the superuser password.**
  - SQL grantee: `Target{User: dbUser, Password: <stored managed password>}`, and `Request.Restricted = true`.
  - Redis grantee: superuser Target, but only `RunRedisGrantee`.
  - Errors are scrubbed of every password involved, with `scrubAll`.
- **Route levels.** Missing from `authz.Routes` still means Admin.
  - `LevelSignedIn`:
    - `GET /api/databases` (the handler filters for non-Admins)
    - `GET /api/databases/engines`
  - `LevelDatabase`:
    - `GET /api/databases/{id}`
    - `GET /api/databases/{id}/credentials`
    - `GET /api/databases/{id}/logical-databases`
    - `POST /api/databases/{id}/query`
    - `GET /api/databases/{id}/query/schema`
    - `POST /api/databases/{id}/redis/command`
    - `GET /api/databases/{id}/redis/keys`
    - `GET /api/databases/{id}/redis/key`
  - Everything else under `/api/databases` stays Admin. That includes the three new grant routes.
- **Migration.** `internal/migrations/00051_database_grants.sql` is the only new migration. Never edit 00041–00050.
  ```sql
  CREATE TABLE database_grants (
      database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
      user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      db_user     TEXT NULL,
      level       TEXT NULL CHECK (level IN ('read', 'read_write')),
      created_at  INTEGER NOT NULL,
      PRIMARY KEY (database_id, user_id),
      FOREIGN KEY (database_id, db_user) REFERENCES database_users(database_id, name) ON DELETE CASCADE,
      CHECK ((db_user IS NULL) <> (level IS NULL))
  );
  CREATE INDEX database_grants_user ON database_grants(user_id);
  ```
  Down drops the table. Run Down once by hand.
- **Redis levels.** `type RedisLevel string`, with `LevelRead = "read"` and `LevelReadWrite = "read_write"`. The UI labels are **Read** and **Read-write**.
- **Preview copy.**
  - Refusal: `Preview runs only INSERT, UPDATE and DELETE statements.` (on MySQL / MariaDB: `INSERT, UPDATE, DELETE and REPLACE`), then ` Statement N starts with WORD.`
  - Executable comment: ` Statement N starts with an executable comment.`
  - Non-InnoDB: `Preview needs every table to be InnoDB, so that its changes roll back. Not InnoDB: db.t (MyISAM), …` lists up to five.
  - Result line in the UI: `Would delete 3,412 rows from orders`, with the table in code style. Without a table: `Would delete 3,412 rows`. The verbs are insert, update, delete and replace.
  - Page note: `Preview rolls everything back. It cannot undo sequence values, or anything a trigger or function does outside this database's tables.`
- **Server-wide message.** `<dbUser> has server-wide privileges (superuser, role management, server files or global grants), so a non-Admin cannot use it. Remove them or pick another user.` The same text serves the 422 at grant time and the run error.
- **Audit.**
  - Every query run gains the detail `dbUser`: the grantee's user, or the engine's superuser for an Admin.
  - A Preview has `outcome: "preview"` whatever happened, and the existing `error` detail.
  - A Redis run by a grantee gains `level`.
  - Grant set and delete carry `user` (the panel user's email), plus `dbUser` or `level`.
- **Tests:** `env -u DOCKER_CONFIG -u DOCKER_HOST go test …`. Docker only through `source .data/e2e/env`.
- **Commits:** Conventional Commits, no scope, ending with the `Co-Authored-By` trailer from the session.

## Review Focus

1. **The superuser password reaching a grantee.**
   - Credentials with `?user=postgres` or no `?user`.
   - A query, schema read or database list by a grantee.

   Tests: `TestGranteeNeverGetsSuperuserTarget` and `TestGranteeCredentialsForcedToOwnUser` (Task 6).
2. **A Redis grantee running `CONFIG GET requirepass`, `EVAL`, `FLUSHDB` or `CLIENT KILL`.** Every one is refused, with no `needsConfirm`. Tests: `TestClassifyGrantee` (Task 3), and the Redis part of the e2e (Task 8).
3. **A managed user that is, or later becomes, superuser** (or holds `FILE` / `*.*` privileges on MySQL). This includes a Full member whose per-database `SET role` makes `current_user` the owner: the check reads `session_user`. The grant is refused, and a later run is refused before any statement runs. Tests:
   - `TestMySQLServerWide` (Task 2);
   - `TestRunRefusesServerWide` (Task 2);
   - `TestSetGrantRefusesServerWideUser` (Task 6);
   - the e2e `ALTER ROLE … SUPERUSER` after the grant (Task 8).
4. **Preview fooled into running DDL or a commit.** Inputs: `;` inside strings, `$$` bodies, nested comments, `/*!50000 DROP TABLE t */`, `DELETE …; COMMIT`, `WITH … DELETE`. Tests: `TestSplitPostgres` and `TestPreviewStatements` (Task 4), and the Preview e2e (Task 8).
5. **A grant outliving what it names.** The panel user deleted, the managed user deleted, the Database deleted or tombstoned. A managed user's password reset must keep the grant. Test: `TestGrantLifecycle` (Task 1).

---

### Task 1: Migration and grant store

**Files:**
- Create: `internal/migrations/00051_database_grants.sql`, `internal/databases/grant_store.go`, `internal/databases/grant_store_test.go`
- Modify: `internal/databases/store.go` (`Tombstone` also deletes the Database's grants)

**Interfaces:**
- Produces:
  - `type RedisLevel string`, with consts `LevelRead`, `LevelReadWrite`, and `func (l RedisLevel) Valid() bool`.
  - `type Grant struct { DatabaseID, UserID int64; Email string; DBUser string; Level RedisLevel; CreatedAt time.Time }`. `Email` is read through a join with `users`.
  - `(*Store).SetGrant(ctx, g Grant) error` is an upsert on `(database_id, user_id)`. It replaces `db_user` and `level` and keeps `created_at`. An empty `DBUser` or `Level` is stored as NULL.
  - `(*Store).Grants(ctx, databaseID int64) ([]Grant, error)`, ordered by email.
  - `(*Store).GrantsForUser(ctx, userID int64) ([]Grant, error)` skips tombstoned Databases.
  - `(*Store).GrantsForDBUser(ctx, databaseID int64, dbUser string) ([]Grant, error)`.
  - `(*Store).DeleteGrant(ctx, databaseID, userID int64) error` returns `ErrNotFound` when there is no row.
  - `(*Store).PanelUser(ctx, userID int64) (email string, isAdmin bool, err error)` returns `ErrUnknownPanelUser` when absent.
  - `var ErrUnknownPanelUser = errors.New("no such panel user")`.

- [ ] **Step 1: Write the failing tests.**
  - `TestGrantStoreRoundTrip`:
    - set an SQL grant, list it with its email, and set it again with another managed user. There is still one row, and `CreatedAt` is unchanged;
    - set a Redis grant;
    - `GrantsForUser` and `GrantsForDBUser` return what was set;
    - delete it; deleting it again is `ErrNotFound`.
  - `TestGrantConstraints`:
    - a grant with both `DBUser` and `Level` fails, and so does one with neither;
    - a `DBUser` that is not in `database_users` fails (FK);
    - a level `"admin"` fails.
  - `TestGrantLifecycle`: one grant each time, then:
    - deleting the panel user (`DELETE FROM users`) removes it;
    - `Store.DeleteUser` of its managed user removes it;
    - `UpsertUser` (a password reset) keeps it;
    - `Tombstone` removes it;
    - deleting the Database row removes it;
    - `GrantsForUser` skips a tombstoned Database even if its row is still there.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/ -run 'Grant'`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** the migration and the store methods.
- [ ] **Step 4: Run the Step 2 command.** Expected: PASS. Run goose Down then Up once on a scratch copy of a panel database.
- [ ] **Step 5: Commit.** `feat: store per-database grants for panel users`

### Task 2: Restricted runs (server-wide check)

**Files:**
- Create: `internal/dbquery/serverwide.go`, `internal/dbquery/serverwide_test.go`
- Modify: `internal/dbquery/query.go` (`Request.Restricted`), `internal/dbquery/postgres.go`, `internal/dbquery/mysql.go`, `internal/databases/accounts_sql.go`, `internal/databases/accounts_sql_test.go`

**Interfaces:**
- Produces, in `internal/dbquery`:
  - `Request.Restricted bool`: run the server-wide check on the run's own connection, after the session settings and before the first statement.
  - `func PgServerWideQuery(role string) string`. `role` is an SQL expression: `session_user`, or a quoted literal. It is never `current_user`. Phase 7 gives a Full member `ALTER ROLE … IN DATABASE … SET role = '<owner>'`, so `current_user` is the owner from the first statement on. A superuser login would then pass the check, and run `RESET ROLE` in its batch. The query answers one row per offending role and no rows when clean:
    ```sql
    SELECT r.rolname FROM pg_roles r
    WHERE pg_has_role(<role>, r.oid, 'MEMBER')
      AND (r.rolsuper OR r.rolcreaterole
           OR r.rolname IN ('pg_read_server_files','pg_write_server_files','pg_execute_server_program'))
    ```
  - `func MySQLServerWide(grantLines []string) bool`: true unless every line is one of:
    - `GRANT USAGE ON *.* TO …`, which may be followed by MariaDB's `IDENTIFIED BY PASSWORD '…'`;
    - `GRANT <privs> ON <db>.<obj> TO …`, where `<db>` is not `*`. A `PROCEDURE` or `FUNCTION` keyword may come before the target.

    Any line with `WITH GRANT OPTION` is server-wide. Never put a line into an error: MariaDB lines carry the password hash.
  - `func ServerWideMessage(user string) string`: the Global Constraints text.
  - A restricted run that fails the check returns `QueryResponse{Results: [], Error: &QueryError{Message: ServerWideMessage(t.User)}}` and runs nothing. On MySQL / MariaDB the check is `SHOW GRANTS`; on Postgres it is `PgServerWideQuery("session_user")`.
- Produces, in `internal/databases`:
  - `accountSQL.ServerWideQuery(user string) string`: Postgres `PgServerWideQuery(pgLit(user))`, MySQL `SHOW GRANTS FOR ` + `myAccount(user)`.
  - `accountSQL.ServerWide(rows [][]string) bool`: Postgres when any row; MySQL `dbquery.MySQLServerWide` of the first column.

- [ ] **Step 1: Write the failing tests.**
  - `TestMySQLServerWide`, a table of lines. Clean:
    - ``GRANT USAGE ON *.* TO `a`@`%` ``
    - MariaDB's ``GRANT USAGE ON *.* TO `a`@`%` IDENTIFIED BY PASSWORD '*AB12'``
    - ``GRANT SELECT, SHOW VIEW ON `app\_1`.* TO `a`@`%` ``
    - ``GRANT SELECT (`id`, `name`) ON `app`.`t` TO `a`@`%` ``
    - ``GRANT EXECUTE ON PROCEDURE `app`.`p` TO `a`@`%` ``

    Server-wide:
    - ``GRANT FILE ON *.* TO …``
    - ``GRANT SELECT ON *.* TO …``
    - ``GRANT BACKUP_ADMIN ON *.* TO …``
    - ``GRANT `r`@`%` TO `a`@`%` ``
    - ``GRANT PROXY ON ''@'' TO …``
    - ``GRANT ALL PRIVILEGES ON `app`.* TO `a`@`%` WITH GRANT OPTION``
    - ``GRANT USAGE ON *.* TO `a`@`%` WITH GRANT OPTION``
    - an unparsable line
  - `TestPgServerWideQuery`: `PgServerWideQuery("session_user")` contains `pg_has_role(session_user, r.oid, 'MEMBER')`, `rolsuper`, `rolcreaterole` and the three role names. The run path never passes `current_user` (grep in the test is fine).
  - `TestRunRefusesServerWide`: drive the restricted branch through an injectable check (a package var `serverWideCheck` on MySQL, or the `queryer` fake that `runBatch` tests use). Assert:
    - with the check reporting server-wide, no statement reaches the fake;
    - the response error is `ServerWideMessage("u")`.
  - `TestServerWideQueryPerEngine` (in `accounts_sql_test.go`):
    - Postgres quotes the name;
    - MySQL is ``SHOW GRANTS FOR 'a'@'%'``;
    - `ServerWide` reads the rows as described.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/dbquery/ ./internal/databases/ -run 'ServerWide'`. Expected: FAIL.
- [ ] **Step 3: Implement.** In `RunPostgres`, run the check with `conn.Query` right after connecting. In `RunMySQL`, run it after `mysqlSessionSQL` and before `CONNECTION_ID()`. Leave `cfg.AllowAllFiles` false, the driver's default.
- [ ] **Step 4: Run the Step 2 command, then all of `./internal/dbquery/`.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: refuse database users with server-wide powers in restricted runs`

### Task 3: Redis classifier for grantees

**Files:**
- Modify: `internal/dbquery/redis.go`, `internal/dbquery/redis_test.go`, `internal/dbquery/redis_run_test.go`

**Interfaces:**
- Produces:
  - `CommandInfo.Categories []string` is read from entry[6], lower-case, for example `"@admin"`. `CommandInfo.SubcommandCategories map[string][]string` is read from row[6] of each subcommand.
  - `neverCommands` gains `EVAL`, `EVALSHA`, `EVAL_RO`, `EVALSHA_RO`, `FCALL`, `FCALL_RO`, `FUNCTION`, `SCRIPT` and `MODULE` (Decision 2). Remove the `SCRIPT FLUSH`, `FUNCTION FLUSH`, `MODULE LOAD` and `MODULE UNLOAD` entries that this makes dead from `dangerous`. The reason stays `<NAME> is not available in the console`.
  - `func ClassifyGrantee(info CommandInfo, args []string, readWrite bool) Class` works as follows:
    1. It takes `Classify(info, args, readWrite)`, and returns it when that is refused.
    2. Then, before anything else is allowed, it refuses with the reason `<FULL NAME> is not available to non-Admins` when any of these holds: `Dangerous`, the effective flags contain `admin`, or the effective categories contain `@admin` or `@dangerous`. The effective flags and categories are the subcommand's when there is one.
    3. It never sets `Dangerous` in what it returns.
  - `func RunRedisGrantee(parent context.Context, t Target, db int, line string, readWrite bool) RedisResponse` is `RunRedis` with `ClassifyGrantee`. It never answers `NeedsConfirm`.

- [ ] **Step 1: Write the failing tests.**
  - `TestParseCommandInfoCategories`: a Redis 7.4-shaped `CONFIG` reply gives `Categories` and `SubcommandCategories["get"]` containing `@admin`.
  - `TestClassifyGrantee`, with `CommandInfo` values written by hand from Redis 7.4's `COMMAND INFO`:
    - **Read:** `GET`, `SCAN`, `TTL` and `PING` are allowed. These are refused:
      - `SET`: "writes are off";
      - `CONFIG GET requirepass`: `CONFIG GET is not available to non-Admins`;
      - `INFO` (`@dangerous`);
      - `KEYS` (`@dangerous`);
      - `CLIENT LIST` (`admin`);
      - `SLOWLOG GET`;
      - `EVAL_RO`: "not available in the console".
    - **Read-write:** `SET` and `DEL` are allowed. `FLUSHDB` and `CONFIG SET` are refused, and neither result is `Dangerous`. `CLIENT KILL`, `MIGRATE` (`@dangerous`), `ACL LIST`, `EVAL` and `FUNCTION LOAD` are refused.
  - `TestClassifyAdminRefusesScripts`: `Classify` with writes on refuses `EVAL` and `FCALL`.
  - `TestRunRedisGranteeNeverConfirms`: the existing run test harness, with `FLUSHALL` at Read-write. `Refused` is set, `NeedsConfirm` is false, and `FLUSHALL` was never sent.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/dbquery/ -run 'Classify|CommandInfo|Grantee'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** all of `./internal/dbquery/`. Expected: PASS. Fix existing tests that expected `EVAL` to run, and say so in the commit body.
- [ ] **Step 5: Commit.** `feat: restrict the redis console for non-admins and refuse scripts`

### Task 4: Preview in dbquery

**Files:**
- Create: `internal/dbquery/split_pg.go`, `internal/dbquery/split_pg_test.go`, `internal/dbquery/preview.go`, `internal/dbquery/preview_test.go`
- Modify: `internal/dbquery/query.go`, `internal/dbquery/postgres.go`, `internal/dbquery/mysql.go`

**Interfaces:**
- Produces:
  - `func SplitPostgres(sql string) ([]Statement, error)`. It splits on `;` outside:
    - `'…'` (with `''`);
    - `E'…'` (with backslash escapes and `''`);
    - `"…"`;
    - `$tag$…$tag$` (a tag is empty or an identifier);
    - `--` line comments;
    - `/* */` comments, which nest.

    Comments stay in the text. Statements holding only comments or whitespace are dropped. `Start` counts runes, as in `SplitMySQL`. An unterminated quote, dollar body or comment is an error naming its line, with the same wording as `SplitMySQL`.
  - `func PreviewStatements(engine, sql string) ([]Statement, *QueryError)`:
    - It splits with `SplitPostgres` or `SplitMySQL`.
    - Each statement's leading keyword, after whitespace, `--` and `#` (MySQL) line comments and plain `/* */` comments, must be `INSERT`, `UPDATE` or `DELETE`. `REPLACE` is also allowed on MySQL / MariaDB.
    - A statement starting with `/*!` or `/*M!` is refused as an executable comment.
    - The first statement that fails gives a `QueryError` with its `StatementIndex`, its `StatementStart` and the Global Constraints copy.
    - An empty batch is `there is nothing to run`.
  - `func PreviewTarget(stmt string) (verb, table string)`.
    - `verb` is the lower-case keyword.
    - `table` is the first table name, with its quotes removed and a schema prefix kept. It skips `LOW_PRIORITY`, `QUICK`, `IGNORE`, `DELAYED`, `HIGH_PRIORITY`, `INTO`, `FROM` and `ONLY`.
    - `table` is `""` when unsure, for example `DELETE t1, t2 FROM …`.
  - `Request.Preview bool`, `QueryResponse.Preview bool` (`json:"preview,omitempty"`), and `QueryResult.Verb`, `QueryResult.Target` (`json:"verb,omitempty"`, `json:"target,omitempty"`).
  - **Postgres preview,** inside `RunPostgres` when `r.Preview`:
    1. Connect and run the restricted check, as in Task 2.
    2. `conn.Exec(ctx, "BEGIN")`.
    3. For each statement, `conn.PgConn().ExecParams(ctx, st.Text, nil, nil, nil, nil)`. Never `conn.Exec`, which uses the simple protocol and so would run two statements a mis-split left together. Read and discard rows, then `Close()` for the tag. Count each statement against `Budget.Result()`.
    4. `ROLLBACK`, always.

    Errors map through `finishPg`, with `StatementIndex` and `StatementStart` set.
  - **MySQL / MariaDB preview,** inside `RunMySQL` when `r.Preview`:
    1. Session SQL with writes on.
    2. The restricted check.
    3. `SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE IS NOT NULL AND ENGINE <> 'InnoDB' AND TABLE_TYPE NOT IN ('VIEW','SEQUENCE') AND TABLE_SCHEMA NOT IN (<system schemas>) ORDER BY 1, 2 LIMIT 5`, on the run's own connection. Any row refuses the Preview.
    4. `START TRANSACTION`.
    5. `runBatch`.
    6. `ROLLBACK`, deferred, so it also runs after an error or a kill.

    Set `Verb` and `Target` on each result from `PreviewTarget`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSplitPostgres`. Each case gives its statements:
    - `select 1; select 2` gives 2 statements;
    - `select ';'; select 2` gives 2;
    - `select E'\';'; x` gives 2;
    - `create function f() returns int as $$ select 1; $$ language sql; select 2` gives 2;
    - `$a$ $b$ ; $b$ $a$` gives 1;
    - `/* a /* b; */ c; */ select 1` gives 1;
    - `-- c;\nselect 1` gives 1;
    - `select "a;b"` gives 1;
    - `select 1;;  ;` gives 1;
    - `select 'x` is an error naming line 1.
    - `Start` values are checked on a two-line batch.
  - `TestPreviewStatements`:
    - **Allowed:** `-- note\nDELETE FROM t`, `/* c */ update t set a=1`, `INSERT INTO t VALUES (1); DELETE FROM u`, and MySQL `REPLACE INTO t VALUES (1)`.
    - **Refused, with these indexes:**
      - Postgres `REPLACE INTO t …` (index 0);
      - `DELETE FROM t; COMMIT` (index 1, `COMMIT`);
      - `WITH x AS (SELECT 1) DELETE FROM t` (`WITH`);
      - `TRUNCATE t`;
      - `SELECT 1`;
      - MySQL `/*!50000 DROP TABLE t */` (executable comment);
      - `/*M!100000 DROP TABLE t */`.
  - `TestPreviewTarget`:
    - `DELETE FROM "Shop".orders WHERE …` gives `delete` and `Shop.orders`;
    - ``INSERT IGNORE INTO `logs` …`` gives `insert` and `logs`;
    - `UPDATE ONLY t SET …` gives `update` and `t`;
    - `DELETE t1, t2 FROM t1 JOIN t2` gives `delete` and `""`.
  - `TestMySQLPreviewRollsBack`: through the `queryer` fake, the statements sent are, in order: `START TRANSACTION`, the batch, then `ROLLBACK`. `ROLLBACK` is also sent when the second statement fails. Make the fake able to record this.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/dbquery/ -run 'SplitPostgres|Preview'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** all of `./internal/dbquery/` with `-race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: preview data-changing sql in a rolled-back transaction`

### Task 5: authz level for Database grantees

**Files:**
- Modify: `internal/authz/authz.go`, `internal/authz/scope.go`, `internal/authz/context.go`, `internal/authz/routes.go`, `internal/authz/actions.go`, `internal/authz/routescan_test.go`, `internal/authz/matrix_test.go`, `internal/authz/authz_test.go`, `internal/authz/scope_test.go`

**Interfaces:**
- Produces:
  - `LevelDatabase` is appended after `LevelProjectEditor`.
  - `Scope.DatabaseID int64` is `{id}` right after `databases`. A non-positive or non-numeric value makes `ok` false.
  - `type DatabaseGrant struct { DatabaseID int64; DBUser string; Level string }`, with `func WithDatabaseGrant(ctx context.Context, g DatabaseGrant) context.Context` and `func DatabaseGrantFrom(ctx context.Context) (DatabaseGrant, bool)`.
  - `decide` gives Admins no grant. For a non-Admin on `LevelDatabase`, it branches before the `scope.ProjectID == 0` refusal:
    - a bad scope is 404;
    - it runs `SELECT COALESCE(g.db_user,''), COALESCE(g.level,'') FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE g.database_id = ? AND g.user_id = ? AND d.deleted_at IS NULL`;
    - no row is 404;
    - a row is let through, and `Middleware` puts the grant in the context. `decide`'s results gain the grant.
  - The `Routes` entries are the Global Constraints list. The grant routes' `Actions` and `adminOnly` lines belong to Task 6, which registers those routes, so every task commits green.

- [ ] **Step 1: Write the failing tests.**
  - `TestScopeReadsDatabaseID`.
  - `TestDatabaseLevel`, against a migrated test DB with Database 1 granted to member M, and Database 2 not granted to M:
    - an Admin passes with no grant in the context;
    - M on `GET /api/databases/1` passes, and `DatabaseGrantFrom` returns M's grant;
    - M on `/api/databases/2` gets 404;
    - M on `/api/databases/1/logs` (Admin-only) gets 403;
    - M on `/api/databases/x` gets 404;
    - after Database 1 is tombstoned, M gets 404.
  - Update `adminOnly` in `routescan_test.go`: move the grantee routes out. Extend `TestKeyRoutesHaveTheSpecLevel` with `POST /api/databases/{id}/query → LevelDatabase`.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/authz/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/authz/ ./internal/httpapi/`. Expected: PASS, the route scan included.
- [ ] **Step 5: Commit.** `feat: let database grantees reach their database routes`

### Task 6: Grants, grantee paths and Delete user confirmation in the service and HTTP

**Files:**
- Create: `internal/databases/grants.go`, `internal/databases/grants_test.go`, `internal/databases/grants_http.go`, `internal/databases/grants_http_test.go`, `docs/adr/0045-per-database-access.md`
- Modify:
  - `internal/databases/query.go`, `internal/databases/query_http.go`, `internal/databases/http.go`
  - `internal/databases/users.go`, `internal/databases/users_http.go`
  - `internal/databases/query_test.go`: `fakeQuerier` records the Target, and gains `RunRedisGrantee`
  - `CONTEXT.md`
  - `internal/authz/actions.go`: `"PUT /api/databases/{id}/grants/{userID}": "database.grant_set"` and `"DELETE /api/databases/{id}/grants/{userID}": "database.grant_delete"`
  - `internal/authz/routescan_test.go`: the three grant routes join `adminOnly`

**Interfaces:**
- Consumes:
  - Task 1's store;
  - Task 2's `Request.Restricted` and `accountSQL.ServerWide*`;
  - Task 3's `RunRedisGrantee`;
  - Task 4's `Request.Preview`;
  - Task 5's `authz.DatabaseGrantFrom`.
- Produces (service):
  - `Querier.RunRedisGrantee(ctx, t, db int, line string, readWrite bool) dbquery.RedisResponse`.
  - `(*Service).Query(ctx, id int64, dbUser string, r dbquery.Request)`. An empty `dbUser` is the superuser. Otherwise the run is restricted and uses the stored password of `dbUser`. `ErrUserNotManaged` becomes a 409. `r.Preview` forces `r.Writes`.
  - `(*Service).Schema(ctx, id int64, dbUser, database string)`, which connects as `dbUser` when it is set.
  - `(*Service).RedisCommand(ctx, id int64, level RedisLevel, db int, line string, writes, confirm bool)`. An empty `level` is an Admin. `LevelRead` calls `RunRedisGrantee(…, false)`, and `LevelReadWrite` calls it with `writes`. `confirm` is ignored for a grantee.
  - `(*Service).GranteeDatabases(ctx, id int64, dbUser string) ([]string, error)` lists the logical databases where `dbUser` owns the database or its Access is not None. It reuses `logicalList` and `accessRows`.
  - `(*Service).Grants(ctx, id int64) ([]Grant, error)` and `(*Service).DeleteGrant(ctx, id, userID int64) error`.
  - `(*Service).SetGrant(ctx, id, userID int64, dbUser string, level RedisLevel) error`. It refuses:
    - `ErrNotFound` for a missing or tombstoned Database;
    - `ErrUnknownPanelUser`, 404;
    - `ErrGrantToAdmin{Email}`, 422: "<email> is an Admin and already reaches every Database";
    - on SQL, a missing `dbUser` or a set level: `ErrBadGrant`, 422, "pick a database user" or "a level is only for Redis";
    - `ErrUserNotManaged`, 422;
    - not running: `ErrNotRunning`, 409;
    - `ErrServerWideUser{User}`, 422, whose text is `dbquery.ServerWideMessage`;
    - on Redis, a level that is not `Valid()` or a set `dbUser`: `ErrBadGrant`, 422.
  - `(*Service).DeleteUser(ctx, id int64, name string, confirmGrants bool) error`. When `GrantsForDBUser` has rows and `confirmGrants` is false, it returns `ErrUserInUse{User string; Emails []string}`, 409: "<user> is used by <emails joined by ", ">; confirm to delete it and their access". The cascade removes the grants.
- Produces (HTTP):
  - `GET /api/databases/{id}/grants` answers `[{userId, email, dbUser, level, createdAt}]`.
  - `PUT /api/databases/{id}/grants/{userID}` takes the body `{dbUser?, level?}` and answers 204.
  - `DELETE /api/databases/{id}/grants/{userID}` answers 204.
  - `DELETE /api/databases/{id}/users/{name}?confirmGrants=1`. `ErrUserInUse` answers 409 with `{error, users: [emails]}`.
  - `DatabaseJSON.Access *AccessJSON` (`json:"access,omitempty"`), where `type AccessJSON struct { DBUser string \`json:"dbUser,omitempty"\`; Level string \`json:"level,omitempty"\` }`.
  - **Who is calling,** in `func callerOf(r *http.Request) (c caller, ok bool)` with `type caller struct { Admin bool; Grant authz.DatabaseGrant }`, used by every `LevelDatabase` handler:
    - `authz.DatabaseGrantFrom` is ok: grantee;
    - otherwise `authz.Member(ctx)` is ok (a non-Admin with no grant): not ok, and the handler answers 404;
    - otherwise: Admin. That covers an Admin, and a package test that calls the handler without an Access.

    A missing grant never means superuser for a non-Admin.
  - **The list route** is `LevelSignedIn`, so `decide` never looks a grant up there. It detects a non-Admin with `authz.Member(ctx)`, takes the ID from `auth.UserFrom`, and lists only `GrantsForUser(user.ID)`.
  - For a grantee:
    - **list:** as above. A non-Admin without grants gets `[]`.
    - **get:** the `granteeJSON` of Decision 7.
    - **credentials:**
      - an SQL grantee always gets `g.DBUser`; `?user` is ignored. `?database` is checked as today. Its default is the first of `GranteeDatabases`, or the engine's default database when that list cannot be read (a stopped Database);
      - a Redis grantee gets 403: "a Redis grant has no credentials to show".
    - **logical-databases:** `GranteeDatabases`.
    - **query, schema and redis/command:** the grant's user or level.
  - `query` takes `preview` in its body.
  - Audit details are those in Global Constraints.

- [ ] **Step 1: Write the failing tests.**
  - `TestGranteeNeverGetsSuperuserTarget`: for an SQL grantee, `Query`, `Schema` and Preview each reach `fakeQuerier` with `Target.User == "shop_ro"`, the stored password, and `Restricted == true`. Asserting `Password != d.Password` on every recorded Target.
  - `TestGranteeCredentialsForcedToOwnUser`: through the handler with a grant in the context, `?user=postgres` and no `?user` both answer `user: shop_ro`. A Redis grant answers 403.
  - `TestGranteeListAndGet`. Build the list request's context the way the middleware does for a `LevelSignedIn` route: `auth` user non-Admin, `authz.WithAccess` with a non-Admin Access, and no grant. Otherwise the test passes while the filter never runs.
    - a member with grants on 1 and 3 of 4 Databases lists exactly 1 and 3, each with `access`;
    - `lastError` is `""` on a Database that has one;
    - `user` is the DB user;
    - a member without grants gets `[]`.
  - `TestNonAdminWithoutGrantIs404`: a non-Admin Access with no grant in the context gets 404 from `get`, `credentials`, `query` and `redis/command`, and the fake Querier is never called.
  - `TestRedisGranteeLevels`:
    - Read calls `RunRedisGrantee` with `readWrite=false` even when the body has `writes: true`;
    - Read-write ignores `confirmDangerous`;
    - an Admin still calls `RunRedis`.
  - `TestSetGrantRefuses`: each refusal listed above, then a success.
  - `TestSetGrantRefusesServerWideUser`: `fakeAdmin` answers the `ServerWideQuery` with a row. The result is 422, and no grant is stored.
  - `TestDeleteUserConfirmsGrants`: 409 listing the email; then success with confirm, and the grant is gone.
  - `TestGranteeDatabases`: owner, Read-only and None across three databases. Only the first two are listed.
  - `TestQueryAuditDetails`: an Admin run has `dbUser: postgres`; a grantee run has `dbUser: shop_ro`; a Preview has `outcome: preview`.
  - `TestGrantRoutesAudited`: PUT and DELETE record `database.grant_set` and `database.grant_delete`, with the email.
- [ ] **Step 2: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Then:
  - write ADR-0045, covering: the grant model; why the security boundary is the DB user and not statement filtering; the Redis classifier and its categories; the server-wide check at grant and run time; Preview's transaction and extended protocol; and Decisions 1–10.
  - add **Database grant** to CONTEXT.md, noting that it is not a Project **Grant** and not a DB user's **Access**.
- [ ] **Step 4: Run** `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...` and `go vet ./...`. Expected: PASS, the authz route scan included.
- [ ] **Step 5: Commit.** `feat: give panel users access to one database as a managed user or redis level`

### Task 7: UI

**Files:**
- Create: `web/src/pages/DatabaseGrantsTab.tsx`, `web/src/pages/DatabaseGrantsTab.test.tsx`
- Modify:
  - `web/src/app/AppShell.tsx`: `memberMay`, and the sider for members
  - `web/src/pages/DatabasesPage.tsx`, `web/src/pages/DatabasePage.tsx`
  - `web/src/pages/DatabaseQueryTab.tsx`, `web/src/pages/DatabaseRedisTab.tsx`, `web/src/pages/DatabaseUsersTab.tsx`
  - `web/src/data/api.ts`, `web/src/data/types.ts`
  - the matching `*.test.tsx`

**Interfaces:**
- Consumes the Task 6 routes and JSON.
- Produces:
  - `api.databaseGrants(id)`, `api.setDatabaseGrant(id, userId, { dbUser?, level? })`, `api.deleteDatabaseGrant(id, userId)`, and `api.deleteDatabaseUser(id, name, confirmGrants?)`.
  - `DatabaseView.access?: { dbUser?: string; level?: 'read' | 'read_write' }`.
  - `QueryResponse.preview`, and `QueryResult.verb` and `.target`.

- [ ] **Step 0: Read** `https://ant.design/llms.txt`, then Menu, Tabs, Table, Modal, Form, Select, Segmented and Alert for antd 6.5.1, and `docs/GUIDELINES.md`.
- [ ] **Step 1: Write the failing vitest tests.**
  - **AppShell:**
    - a member whose `GET /api/databases` answers one Database sees a **Data** group with **Databases**;
    - with `[]`, there is no Data group;
    - a member can open `databases/:databaseId`, and does not get the 403 Result.
  - **DatabasesPage** as a member: there is no Create Database button, and the list shows one row.
  - **DatabasePage** as an SQL grantee:
    - the tabs are exactly Connection and Query;
    - there are no header actions, no Apply settings alert, and no lastError alert;
    - `GET /users` is never requested;
    - the Connection tab shows `shop_ro` with no user picker and no Reset superuser password card.
  - **DatabasePage** as a Redis grantee: the tabs are exactly Console.
  - **DatabaseRedisTab** at Read: no writes switch.
  - **DatabaseQueryTab:**
    - **Preview** shows beside Run only while writes are on;
    - a Preview response renders `Would delete 3,412 rows from orders` and the page note from Global Constraints;
    - a refused Preview renders its message.
  - **DatabaseGrantsTab:**
    - it lists grants;
    - **Add** picks a non-Admin panel user from `GET /api/users`, with Admins left out, and then:
      - on SQL, a managed user from `GET /users`, with `managed` only;
      - on Redis, Read or Read-write (Segmented);
    - **Remove** confirms, then calls DELETE.
  - **DatabaseUsersTab** Delete: a 409 with `users` opens a confirm that lists the emails. Confirming resends with `confirmGrants`.
  - **DatabasePage** as Admin: a **Panel access** tab sits after Users (after Connection on Redis).
- [ ] **Step 2: Run** `cd web && pnpm vitest run src/pages/Database src/app/AppShell`. Expected: FAIL.
- [ ] **Step 3: Implement.** The member sider fetches `GET /api/databases` with SWR only when the user is not an Admin. A grantee's default tab is `query` on SQL and `console` on Redis.
- [ ] **Step 4: Run** `cd web && pnpm vitest run && pnpm tsc --noEmit && pnpm lint && pnpm build`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: show granted databases to non-admins, add panel access tab and preview`

### Task 8: End-to-end

**Files:**
- Create: `e2e/tests/databases-access.spec.ts`
- Modify: `e2e/lib/sandbox.ts`. Add `createMember(admin: Panel, email: string, password: string): Promise<Panel>`: it calls `POST /api/users` with `isAdmin: false`, reads the invite token from the reply (check its field name in `internal/auth/users.go`), accepts it with `POST /api/invites/{token}` and the password, and signs in.

**Sandbox:** the TLS sandbox from #91. This worktree's sandbox was removed after phase 7, so run `task e2e:serve` first. Clients run as `docker run --rm --network relist-network <engine image> …`, the same way `databases-users.spec.ts` does.

- [ ] **Step 1: Write the spec.**
  - **Postgres 18.** As Admin:
    - create Databases A and B;
    - in A, create the database `shop` with its owner, and a user `shop_ro` with Read-only on `shop`. Write a table with 10 rows as `shop`;
    - in the Query panel, run `CREATE ROLE boss SUPERUSER LOGIN`, then Reset password for `boss`;
    - granting member Alice `boss` is a 422 with the server-wide message;
    - grant Alice `shop_ro` on A.

    As Alice:
    - `GET /api/databases` lists only A, and A's `user` is `shop_ro`;
    - `GET /api/databases/<B>` and `POST /api/databases/<B>/query` are 404, and `GET /api/databases/<A>/logs` is 403;
    - `credentials?user=postgres` answers `shop_ro`;
    - in the query panel:
      - `SELECT count(*) FROM t` answers 10;
      - `DELETE FROM t` fails with a permission error;
      - `SELECT pg_read_file('/etc/passwd')` and `COPY (SELECT 1) TO PROGRAM 'id'` both fail with a permission error.

    As Admin, `ALTER ROLE shop_ro SUPERUSER`. Alice's `SELECT 1` now answers the server-wide message with no results. Then `ALTER ROLE shop_ro NOSUPERUSER`.

    **A Full non-owner member.** Create the user `shop_rw` with Full on `shop`; phase 7 gives it `SET role = 'shop'` there. Change Alice's grant to `shop_rw`, and as Admin `ALTER ROLE shop_rw SUPERUSER`. Alice's `RESET ROLE; SELECT pg_read_file('/etc/passwd')` on `shop` answers the server-wide message, and nothing runs. Then `ALTER ROLE shop_rw NOSUPERUSER`, and change the grant back to `shop_ro`.

    The audit log has a `database.query` event with `dbUser: shop_ro` and Alice as the actor.
  - **Preview, Postgres, as Admin:**
    - `DELETE FROM t WHERE id <= 3` answers `preview: true`, `affected: 3`, `verb: delete` and `target: t`, and `count(*)` is still 10;
    - `TRUNCATE t` is refused at statement index 0;
    - `DELETE FROM t WHERE id = 1; COMMIT` is refused at index 1, and `count(*)` is still 10.
  - **MySQL 8.4:**
    - create `app` with its owner user, and grant Alice `app`;
    - Alice's `SELECT 1 INTO OUTFILE '/tmp/x'` fails with an access error;
    - as Admin, `GRANT FILE ON *.* TO 'app'@'%'`; Alice's next run answers the server-wide message;
    - Admin Preview on a `MyISAM` table is refused, naming `app.<table> (MyISAM)`.
  - **Redis 8:**
    - grant Alice Read: `GET k` works; `SET k v`, `CONFIG GET requirepass` and `EVAL "return 1" 0` are refused; `credentials` is 403;
    - change the grant to Read-write: `SET k v` works; `FLUSHDB` is refused, with no `needsConfirm`.
  - **Lifecycle:**
    - deleting `shop_ro` answers 409 listing Alice's email;
    - with `confirmGrants=1` it is deleted, and A's grants list is empty;
    - deleting Alice removes her Redis grant.
  - **Browser:** Alice signs in. The sider shows Data → Databases. A's page shows the tabs Connection and Query only. Preview of `DELETE FROM t WHERE id <= 3` on `shop` is refused by permissions, since `shop_ro` is Read-only, and the error is shown.
- [ ] **Step 2: Run** `task e2e -- databases-access`. Expected: PASS. Then run the whole `task e2e` once.
- [ ] **Step 3: Commit.** `test: cover per-database access for non-admins and preview end to end`

Open the PR against `claude/databases-phase-7` with the title `feat: databases phase 8, per-database access for non-admins and preview`. Its body lists the ten decisions above.
