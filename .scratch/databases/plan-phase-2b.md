# Databases phase 2b (per-database Backups, restore into a target) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework phase 2 so that:
- a Backup is always one logical database in one plain `.sql.gz`;
- every restore goes into a target the Admin picks;
- an upload that would leave its target or touch users is refused before anything runs.

**Why:** the review of phase 2 (HEAD `89d3651`) reproduced two bugs in the sandbox:
1. An upload made by plain `pg_dump mydb` ran connected to `template1`. Its tables landed in `template1`, and the restore reported `succeeded`.
2. A whole-instance upload from another server replaced the `postgres` password. With MySQL it replaced the `root` password: after a restart, the apps and the panel's own Backups (`mysqldump exited with code 2`) were locked out.

Both came from whole-instance dumps and from letting the file pick its database. The Admin chose to drop whole-instance Backups.

**Architecture:**
- **Built on phase 2, which is kept:** the exec, the job slot, `Startup`, tombstones, quiesce, Migration wait, the sha and gzip checks, and the routes.
- **The Dumper changes** to one logical database per command.
- **The Backup service** makes one row and one file per logical database.
- **The restore** replaces or creates its target itself, then streams the file into it with every engine stopping at the first error.
- **The `pgTolerated` allowlist and `terminateSQL` are deleted.**

**Tech Stack:** Go, SQLite/goose, the Docker Engine API exec, React 19, antd 6.5.1, vitest and Playwright.

**Spec:** `.scratch/databases/spec.md`, sections "Backups" and "Restore", already updated for this plan. It builds on `.scratch/databases/plan-phase-2.md`. That plan's decisions 1 (selected scope has no roles) and 2 (Postgres error allowlist) are void, and so is its "Scope JSON" constraint.

## Global Constraints

- **Everything in `plan-phase-2.md`'s Global Constraints still holds, except "Scope JSON".** That covers files, modes, `.partial`, env-only passwords, the 64 KiB log tail, admin-only routes, and commits.
- **Migrations.** Add `00045_database_backups_per_database.sql`, or the next free number. Never edit `00041`–`00044`.
- **No Backup holds users or roles, and no restore changes them.**
- **Names.**
  - A logical database name that starts with `-` is refused everywhere (`ErrUnsafeName`, 400).
  - A new target name must match `^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$` (`ErrBadTarget`, 400).
  - `template0`, `template1`, `information_schema`, `performance_schema`, `mysql` and `sys` are never a target (`ErrBadTarget`).
- **Identifiers in SQL** are quoted by the panel:
  - Postgres: `"` doubled inside `"…"`; literals use `'` doubled.
  - MySQL: `` ` `` doubled inside `` `…` ``.
  - MySQL literals: `\` doubled first, then `'` doubled, inside `'…'` (MySQL reads `\` as an escape).
  - `InspectSQL`:
    - postgres: `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = '<t>';`
    - mysql/mariadb: `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '<t>';`
- **Error strings** (exact):
  - `ErrUnsafeName`: "database names starting with - are not supported"
  - `ErrBadTarget`: "pick a database to restore into: letters, digits, _ and -, at most 63 characters"
  - `ErrNothingToBackUp`: "there are no databases to back up"
  - `ErrNotRestorable`: "this Backup holds several databases (an earlier format); download it and restore by hand"
  - `ErrForbiddenStatement`: `line %d: %q is not allowed in an upload; dump one database with pg_dump <db> or mysqldump <db>`

## Review Focus

1. **A plain `pg_dump mydb` upload into a new target.** Its tables are in the target, and `template1` has no tables. *Tests:* Task 3 `TestRestoreUploadRunsInTarget` (the exec env/argv names the target), and Task 5 live step 4.
2. **Foreign whole-instance uploads** (`pg_dumpall`, `mysqldump --all-databases`, `mysqldump --databases x`). They are refused with the line number before any Backup, drop or exec, and the panel password still works. *Tests:* Task 1 `TestCheckUpload` and Task 3 `TestRestoreUploadRefusedBeforeAnything`, and Task 5 live step 5.
3. **Replacing a target while an app keeps reconnecting.** `DROP DATABASE … WITH (FORCE)` succeeds. *Test:* Task 5 live step 6, run with the reconnecting client below.
4. **A Postgres target owned by another role.** It keeps its owner after a replace. *Tests:* Task 3 `TestReplaceKeepsOwner` (the SQL sent), and Task 5 live step 7.
5. **COPY data that looks like a forbidden statement.** A text row `CREATE DATABASE x` inside a `COPY … FROM stdin;` block is not refused. *Test:* Task 1 `TestCheckUpload` row "copy data".

---

## File map

| File | Change |
|---|---|
| `internal/migrations/00045_database_backups_per_database.sql` | `database_backups.logical_name`, `database_restores.target` |
| `internal/databases/backup_store.go` | `Backup.LogicalName` replaces `Backup.Scope`; `Restore.Target` |
| `internal/databases/dumps.go` | One-database Dumper, target SQL, name rules |
| `internal/databases/upload_check.go` (new) | `CheckUpload` |
| `internal/databases/backup.go` | `StartBackup(…, BackupRequest)`, which makes one row per database |
| `internal/databases/restore.go` | Target, replace/create, `ON_ERROR_STOP=1`; drops `terminateSQL` and the allowlist |
| `internal/databases/service.go` | `pre_update` backs up every database |
| `internal/databases/backup_http.go` | Request and response shapes |
| `web/src/pages/DatabaseBackupsTab.tsx`, `databases-view.ts`, `data/types.ts`, `data/api.ts` | Back up modal, Database column, restore target |
| `CONTEXT.md`, `docs/adr/0039-panel-managed-databases.md` | Terms and decision |
| `e2e/tests/databases-backup.spec.ts` | Per-database flow |

---

### Task 1: Schema, Dumper and the upload check

**Files:**
- Create: `internal/migrations/00045_database_backups_per_database.sql`, `internal/databases/upload_check.go`, `internal/databases/upload_check_test.go`
- Modify: `internal/databases/backup_store.go`, `backup_store_test.go`, `dumps.go`, `dumps_test.go`, `internal/database/*_test.go` (schema version 45), `CONTEXT.md`, `docs/adr/0039-panel-managed-databases.md`

**Interfaces — Produces:**
```go
// Backup: Scope is removed; LogicalName string is added. The scope column is
// still written ('{}') and never read.
// Restore: Target string is added.

type Dumper interface {
	ListCmd() []string                         // every non-template database, one per line
	SystemDatabases() []string                 // postgres: none; mysql/mariadb: the four system schemas
	DumpCmd(db string) (cmd, env []string)     // env adds PGDATABASE=db for postgres
	RestoreCmd(target string) (cmd, env []string)
	AdminCmd() []string                        // reads SQL on stdin; stops at the first error
	Env(password string) []string
	// InspectSQL prints, for target, one line "<owner>" (postgres) or
	// "<charset>\t<collation>" (mysql/mariadb), or nothing when it does not exist.
	InspectSQL(target string) string
	// ReplaceSQL drops and recreates target. prev is InspectSQL's output ("" = new).
	ReplaceSQL(target, prev string) string
}

var ErrUnsafeName, ErrBadTarget, ErrNothingToBackUp, ErrNotRestorable error
type ErrForbiddenStatement struct{ Line int; Text string } // Text: the line, cut to 80 bytes

func checkTarget(name string, existing []string) (replace bool, err error)
func CheckUpload(engine string, r io.Reader) error // reads plain SQL; the caller un-gzips
```

**Commands** (`env` always also has the password from `Env`):

| | postgres | mysql (`mysql`/`mysqldump`), mariadb (`mariadb`/`mariadb-dump`) |
|---|---|---|
| `DumpCmd(db)` | `pg_dump -U postgres`, env `PGDATABASE=db` | `<dump> -uroot --single-transaction --routines --events --triggers db` |
| `RestoreCmd(t)` | `psql -U postgres -X -q -v ON_ERROR_STOP=1`, env `PGDATABASE=t` | `<client> -uroot --database=t` |
| `AdminCmd()` | `psql -U postgres -X -At -v ON_ERROR_STOP=1 -d template1` | `<client> -uroot -N` |
| `ReplaceSQL(t, prev)` | `DROP DATABASE IF EXISTS "t" WITH (FORCE);` then a newline and `CREATE DATABASE "t" TEMPLATE template0 OWNER "<prev or postgres>";` | ``DROP DATABASE IF EXISTS `t`;`` then ``CREATE DATABASE `t` `` + (prev ≠ "": ` CHARACTER SET <cs> COLLATE <coll>`) + `;` |

- `ListCmd` for postgres becomes `SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1`, and `postgres` is no longer hidden.
- `DumpCmd` returns `ErrUnsafeName` through `checkName` before any exec; the callers check it.

**`CheckUpload` rules** (line by line; leading spaces trimmed; case-insensitive):
- **Postgres:**
  - After a line that starts with `COPY ` and ends with `FROM stdin;`, skip lines until one that is exactly `\.`.
  - Refuse a line that starts with `\`, unless it starts with `\restrict` or `\unrestrict`.
- **MySQL and MariaDB:** strip a leading `/*!` + digits + space before matching. Also refuse `USE `, `CREATE SCHEMA`, `DROP SCHEMA` and `CREATE DATABASE`.
- **All engines:** refuse a line that starts with `CREATE`, `ALTER` or `DROP`, followed by whitespace, an optional `IF [NOT] EXISTS`, and then `DATABASE`, `ROLE` or `USER`. Also refuse `SET PASSWORD`.
- The first refused line wins, and its 1-based line number is returned.

- [ ] **Step 1: Write the failing tests.**
  - `TestCheckUpload` is a table test. Each row gives an engine and a file; `refused` means the error is `ErrForbiddenStatement` with that line number.

    | Row | Engine | File | Want |
    |---|---|---|---|
    | plain pg_dump | postgres | `\restrict abc` then `SET x = 1;` then `CREATE TABLE t (id int);` | ok |
    | pg_dumpall | postgres | `CREATE ROLE postgres;` on line 3 | refused, line 3 |
    | connect | postgres | `\connect app` | refused |
    | shell | postgres | `\! id` | refused |
    | copy data | postgres | `COPY public.t (s) FROM stdin;` then `CREATE DATABASE x` then `\.` | ok |
    | after copy | postgres | the copy-data file followed by `DROP DATABASE y;` | refused, line 4 |
    | create schema | postgres | `CREATE SCHEMA app;` | ok |
    | if exists | postgres | `DROP ROLE IF EXISTS app;` | refused |
    | use | mysql | `USE `+"`shop`"+`;` | refused |
    | add-drop-database | mysql | `/*!40000 DROP DATABASE IF EXISTS `+"`shop`"+`*/;` | refused |
    | create schema | mysql | `CREATE SCHEMA shop;` | refused |
    | alter user | mariadb | `ALTER USER root IDENTIFIED BY 'x';` | refused |
    | plain mysqldump | mysql | `CREATE TABLE t (id int);` and `INSERT INTO t VALUES (1);` | ok |
  - `TestDumperCommands`: one row per engine, each command exactly as in the table above. Include `ReplaceSQL` with a name holding `"` and `` ` ``, and `ReplaceSQL` with `prev=""`.
  - `TestCheckTarget`:
    - `("app", ["app"])` → `(true, nil)`;
    - `("new_db", ["app"])` → `(false, nil)`;
    - `"template1"`, `"mysql"`, `"-x"`, `""`, `"a b"` and a 64-character name → `ErrBadTarget`.
  - Store: `InsertBackup` / `GetBackup` round-trip `LogicalName`, and `InsertRestore` round-trips `Target`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'CheckUpload|DumperCommands|CheckTarget|Store'`. Expected: FAIL (compile).
- [ ] **Step 3: Write the migration.**
  - Up: `ALTER TABLE database_backups ADD COLUMN logical_name TEXT NOT NULL DEFAULT '';` and `ALTER TABLE database_restores ADD COLUMN target TEXT NOT NULL DEFAULT '';`
  - Down: drop both columns.
  - Bump the schema-version asserts to 45.
- [ ] **Step 4: Implement** the interfaces above.
  - Delete `Scope`, `DumpCmds`, `RestoreFailed` and `pgTolerated`.
  - The compile errors elsewhere are fixed in Tasks 2–3. To keep this task's commit building, stub the callers minimally; Task 2 replaces the stubs.
- [ ] **Step 5: Update the docs.**
  - `CONTEXT.md`:
    - **Backup:** "One gzipped plain-SQL dump of one logical database of a Database …; never users or roles."
    - Replace **Backup scope** with: "**All databases / selected databases**: what a backup run covers; a run makes one Backup per logical database."
    - **Restore:** "Replaying a Backup, or an uploaded `.sql`/`.sql.gz` of one database, into a target logical database the Admin picks, replacing it (after a `pre_restore` Backup of it) or creating it."
  - ADR-0039: add a bullet: "Backups are per logical database and never carry roles or users; a restore goes into a target the Admin picks (phase 2 review: whole-instance dumps let an upload land in `template1` and replace the panel's passwords)."
- [ ] **Step 6: Run** the Step 2 command. Expected: PASS. **Commit** `feat: back up one database per file and check uploads before restoring`

### Task 2: Backups per logical database

**Files:** Modify `internal/databases/backup.go`, `backup_test.go`, `service.go`, `lifecycle_jobs_test.go`, `fakes_test.go`

**Interfaces:**
```go
type BackupRequest struct {
	All       bool     `json:"all"`
	Databases []string `json:"databases"`
}
// StartBackup checks the request, inserts one running row per database (names
// sorted), and dumps them one after another in one job. It returns the rows.
func (s *Service) StartBackup(ctx context.Context, id int64, req BackupRequest) ([]Backup, error)
// backupAll backs up every logical database for pre_update; ok is false if any failed.
func (s *Service) backupAll(ctx context.Context, d Database, dp Dumper, trigger string) (ok bool, firstErr string)
```

**Rules:**
- `All` resolves the list when the request is made.
  - An empty list gives `ErrNothingToBackUp`.
  - `All:false` with no names gives `ErrNoScope`.
  - An unknown name gives `ErrUnknownLogical` before any row is inserted.
  - A name starting with `-` gives `ErrUnsafeName`.
- A failed database fails only its own row. The job goes on to the next one.
- `UpdateImage(…, backupFirst=true)` runs `backupAll(TriggerPreUpdate)`.
  - With no databases at all, it updates straight away.
  - If any Backup fails, there is no pull, and `LastError` names the failed database.

- [ ] **Step 1: Write the failing tests:**
  - `TestBackupAllMakesOneFilePerDatabase`:
    - the fake lists `app` and `postgres`;
    - two rows come back, with `LogicalName` `app` and `postgres`, both succeeded;
    - two files exist;
    - each `DumpCmd` env has `PGDATABASE=<name>`.
  - `TestBackupOneFailureKeepsOthers`: the dump of `a` exits 1. Then `a` is failed, `b` is succeeded, and only `b`'s file exists.
  - `TestBackupNothingToBackUp`: a MySQL fake listing only system schemas gives `ErrNothingToBackUp`, and no row is inserted.
  - `TestUpdateImageBackupFirstEveryDatabase`:
    - a `pre_update` row is made per database;
    - one failing means no pull, and `LastError` contains its name;
    - with no databases, the update runs and there is no row.
  - Update `TestBackupFailsOnNonZeroExit` and `TestStartupFailsInterruptedJobs` to the new shapes. Their assertions do not change.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'Backup|UpdateImage|Startup'`. Expected: FAIL.
- [ ] **Step 3: Implement.** `insertScoped` becomes the request check above. `executeBackup` dumps one database with `DumpCmd(b.LogicalName)`.
- [ ] **Step 4: Run** the Step 2 command. Expected: PASS. **Commit** `feat: make one backup per logical database`

### Task 3: Restore into a target

**Files:** Modify `internal/databases/restore.go`, `restore_test.go`

**Interfaces:**
```go
func (s *Service) RestoreFromBackup(ctx context.Context, id, backupID int64, target, confirm string) (Restore, error)
func (s *Service) RestoreFromUpload(ctx context.Context, id int64, target, confirm, name string, body io.Reader) (Restore, error)
```

**Order.** Every step before step 5 runs synchronously and answers with an error.
1. Acquire the job slot. Check `confirm == Database name` (`ErrConfirm`).
2. List the logical databases. `checkTarget(target, list)` decides `replace`.
3. **From a Backup:**
   - `LogicalName == ""` gives `ErrNotRestorable`;
   - otherwise run the sha check as in phase 2.

   **From an upload:**
   - stream it to `uploads/` as in phase 2;
   - open it, un-gzipping by magic bytes;
   - run `CheckUpload`. A refusal removes the file and returns `ErrForbiddenStatement`.
4. Insert the restore row with `Target`.
5. **In the background:**
   - If `replace`, run a `pre_restore` Backup of the target alone. If it fails, finish as failed: there is no drop.
   - Run `AdminCmd` with stdin `InspectSQL(target)` to read `prev`. A non-zero exit fails the restore.
   - Run `AdminCmd` with stdin `ReplaceSQL(target, prev)`. If `replace` is false, `prev` is empty and the `DROP … IF EXISTS` is a no-op.
   - Stream the file into `RestoreCmd(target)`. Any exec error or non-zero exit fails it, with the stderr tail as its log.

- [ ] **Step 1: Write the failing tests:**
  - `TestRestoreUploadRunsInTarget`: an upload into the new target `shop`.
    - The execs, in order: inspect, replace, restore.
    - The restore env has `PGDATABASE=shop`.
    - There is no `pre_restore` row.
    - The restore's `Target` is `shop`.
  - `TestRestoreUploadRefusedBeforeAnything`: an upload holding `CREATE ROLE x;` gives `ErrForbiddenStatement{Line:1}`. There are no execs, no Backup row and no restore row, and `uploads/` is empty.
  - `TestReplaceKeepsOwner`:
    - the inspect exec prints `appuser`;
    - the replace stdin is exactly `DROP DATABASE IF EXISTS "app" WITH (FORCE);\nCREATE DATABASE "app" TEMPLATE template0 OWNER "appuser";\n`;
    - a `pre_restore` row with `LogicalName` `app` exists before the replace exec.
  - `TestRestoreAbortsWhenPreBackupFails`, adapted: when replacing, a failed pre-backup means no inspect, replace or restore exec.
  - `TestRestoreRefusesChangedFile`, adapted.
  - `TestRestoreUploadDetectsGzipByContent`, adapted. `CheckUpload` must also see the un-gzipped text: a gzipped upload holding `\connect x` is refused.
  - `TestRestoreRefusesOldFormat`: a Backup row with `LogicalName ""` gives `ErrNotRestorable`.
  - `TestRestoreStopsAtFirstError`: the restore exec exits 3. The restore fails with "the restore command exited with code 3" and keeps the log.
  - Delete `TestRestoreSelectedTerminatesOnlyItsDatabases` and the Postgres allowlist tests.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Restore`. Expected: FAIL.
- [ ] **Step 3: Implement.** Delete `terminateSQL` and the `RestoreFailed` branch.
- [ ] **Step 4: Run** `go test ./internal/databases/...`. Expected: PASS. **Commit** `feat: restore into a database the admin picks`

### Task 4: API and Backups tab

**Files:**
- Modify `internal/databases/backup_http.go`, `backup_http_test.go`, `http.go` (error mapping)
- Modify `web/src/data/types.ts`, `api.ts`, `pages/DatabaseBackupsTab.tsx` and its test, `pages/databases-view.ts` and its test, `e2e/tests/databases-backup.spec.ts`

**API changes:**

| Route | Body → reply |
|---|---|
| `POST /api/databases/{id}/backups` | `{all:true}` or `{databases:[…]}` → 202 `[]BackupJSON` |
| `POST /api/databases/{id}/restores` | `{backupId, target, confirm}` → 202 |
| `POST /api/databases/{id}/restores/upload?confirm=&name=&target=` | raw body → 202 |

- `BackupJSON` loses `scope` and gains `database` (the logical name; `""` for the earlier format).
- `RestoreJSON` gains `target`.
- **Errors, 400:** `ErrUnsafeName`, `ErrBadTarget`, `ErrNothingToBackUp`, `ErrNotRestorable`, `ErrForbiddenStatement` (with its message).

**UI** (read the antd pages first: Radio, Select, Checkbox, Input, Table, Tag):
- **Back up now:** a Radio, "All databases" (the default) or "Choose databases" (Checkbox list).
  - The help text: "One file per database."
  - OK is disabled when "Choose" has none ticked.
- **Table:** a **Database** column (first column). An earlier-format row shows the Tag "Several databases", and its Restore button is disabled with the tooltip of `ErrNotRestorable`.
- **Restore modal (`DangerModal`)** gains a target control:
  - A Radio: "Replace an existing database" (a `Select` of the current logical databases) or "Restore into a new database" (an `Input`).
  - **For a Backup:** the default is Replace with the Backup's database, if it exists; otherwise New with that name.
  - **For an upload:** there is no default, and OK is disabled until a target is chosen.
  - The text comes from `restoreWarning(target, replace)`:
    - replace: "Replaces the database <target>. It is backed up first. Apps connected to it are disconnected."
    - new: "Creates the database <target>."
  - The upload hint under the button: "A dump of one database: pg_dump <db> or mysqldump <db>."

- [ ] **Step 1: Write the failing tests:**
  - **Go HTTP:**
    - each 400 above;
    - a Backup posts `{all:true}` and gets two rows;
    - a restore body without `target` gives `ErrBadTarget`.
  - **Vitest:**
    - "All databases" posts `{all:true}`;
    - "Choose" posts `{databases:["a"]}`;
    - the Restore of Backup `app` defaults to replacing `app` and posts `{backupId, target:"app", confirm}`;
    - "new" posts the typed name;
    - an upload's OK is disabled until a target is picked, and then the target is sent in the query;
    - an earlier-format row has Restore disabled;
    - `restoreWarning` returns both exact strings.
  - **e2e:** update `databases-backup.spec.ts` to:
    1. back up all databases;
    2. restore `app` into the new target `app_copy`;
    3. check that both have the row.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run HTTP` and `cd web && pnpm vitest run src/pages/Database src/pages/databases`. Expected: FAIL.
- [ ] **Step 3: Implement.** **Step 4: Run** `pnpm typecheck && pnpm vitest run --maxWorkers=6 && pnpm build`, `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: pick what to back up and where to restore in the backups tab`

### Task 5: Live check in the sandbox

Run `task e2e:reload`, then `task e2e`. Expected: all pass. Then run the manual check below on **pg18, pg16, mysql 8.4 and mariadb 11.8**, each a fresh Database made for the check. Check data by querying it, not by the job status alone.

**The reconnecting client** for step 6. Its `\watch` keeps one session busy, and the loop reconnects within about 0.1 s after a termination:
```bash
docker run -d --name race-client --network relist-network -e PGPASSWORD=<pw> postgres:18 bash -c "for w in \$(seq 20); do (while true; do echo 'select 1 \\watch 0.05' | psql -h relist-db-<id> -U postgres -d app -At >/dev/null 2>&1; done) & done; wait"
```

1. Seed `app` (and `postgres` on pg) with one row each. **Back up all.** Expect one succeeded row per database, including `postgres` on pg. Each file passes `gunzip -t` and holds no `CREATE DATABASE`, `\connect` or `USE`.
2. Delete the row in `app`. **Restore** its Backup, replacing `app`. Expect: the row is back, and there is a `pre_restore` row for `app` only.
3. **Restore** the `app` Backup into the new target `app_copy`. Expect: both have the row, and there is no `pre_restore` row.
4. **Upload a foreign plain dump.** On pg, upload `pg_dump -U postgres app` from another sandbox Database into the new target `imported`. Expect:
   - `imported` has the table;
   - `psql -d template1 -c '\dt'` finds no tables.

   On mysql and mariadb, use `mysqldump app` the same way.
5. **Upload foreign whole-instance dumps:** `pg_dumpall`, `mysqldump --all-databases` and `mysqldump --databases app`. Each is refused with a 400 naming the line. Expect no new Backup or restore row. Then connect with the panel's credentials from a container on `relist-network` (pg also after a restart). It works.
6. **pg only:** start the reconnecting client on `app`, then restore into `app` (replace). Expect: succeeded, and the data is right. Remove the client afterwards: `docker rm -f race-client`.
7. **pg only:**
   1. `CREATE ROLE appuser`;
   2. `ALTER DATABASE app OWNER TO appuser`;
   3. restore into `app` (replace).

   Expect `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='app'` = `appuser`.
8. **Update image with Back up first** on a Database with two logical databases. Expect two `pre_update` rows and then the update.
9. **Clean up:** delete the check's Databases with their Backups. Do not run `task e2e:down`.

- [ ] **Commit** any e2e changes: `test: cover per-database backups and target restores end to end`
