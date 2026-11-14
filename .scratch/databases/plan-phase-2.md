# Databases phase 2 (Manual backup and restore) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a Postgres, MySQL or MariaDB Database, an Admin can:
- back up the whole instance, or chosen logical databases, with one click;
- download a Backup;
- restore from a Backup or from an uploaded `.sql` / `.sql.gz`, always after an automatic `pre_restore` Backup;
- update the image with "Back up first";
- delete a Database while keeping its Backups.

**Architecture:**
- **Backups and restores are background jobs** of `databases.Service`, recorded in two new tables.
- **Dump and restore** run through a new non-TTY `Host.Exec`, which streams stdin and stdout through the Engine API's hijacked exec (`docker exec` in the Database's own container).
- **The panel gzips and hashes the dump** while writing `<data dir>/.databases/<id>/backups/`.
- **One job per Database at a time.**
  - While a job runs, lifecycle actions answer 409.
  - A Panel update's quiesce and a server Migration wait for running jobs.
  - The update lock and the Migration lock keep new jobs from starting.

**Tech Stack:** Go, SQLite/goose, the Docker Engine API (exec, with hijack for stdin), React 19, antd 6.5.1, vitest and Playwright.

**Spec:** `.scratch/databases/spec.md` (sections "Backups", "Restore", "Lifecycle → Delete" and "Update image"). It builds on phase 1 (`.scratch/databases/plan-phase-1.md`, PR #80). Scheduling, Rotation, Pin and Backup destinations are phase 3. The Query panel is phase 4.

## Global Constraints

- **Engines.** SQL engines only (`postgres`, `mysql`, `mariadb`). Redis has no Backups tab, and every backup or restore route answers 400 `ErrNoBackups` for it.
- **Files.**
  - Location: `<data dir>/.databases/<id>/backups/<UTC 20060102T150405Z>-<backup id>.sql.gz`.
  - Modes: directory `0700`, file `0600`.
  - Written to `<name>.partial`, then renamed on success. Any failure removes the partial.
- **Triggers:** `manual`, `pre_restore`, `pre_update`. Phase 2 never deletes any Backup automatically.
- **Scope JSON:** `{"all":true}` or `{"databases":["a","b"]}`, stored on each Backup.
- **Credentials.** Passwords reach the dump tools through exec **env** (`PGPASSWORD`, `MYSQL_PWD`), never argv.
- **Logs.** The stderr of each dump or restore is stored on its row, capped at 64 KiB, keeping the **last** 64 KiB.
- **Migrations.** New migrations only: `00044_database_backups.sql`, or the next free number. Never edit `00041`–`00043`.
- **Admin-only.** Every new route is added to `adminOnly` (`internal/authz/routescan_test.go`). Every change, and the Backup download, is added to `authz.Actions`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.

## Decisions this plan makes (spec deltas, applied to `spec.md` in Task 1)

1. **A Postgres selected-scope Backup has no roles.** It is `pg_dump --create --clean --if-exists` per database, concatenated. Restoring roles alongside some databases would `DROP ROLE` roles that other databases still use. A whole-instance Backup keeps them (`pg_dumpall --clean --if-exists`).
2. **Postgres restores tolerate exactly the errors `pg_dumpall` always causes.** They run `psql -v ON_ERROR_STOP=0`, and fail if stderr has any `ERROR:` line other than `role "<superuser>" already exists` and `current user cannot be dropped`. `pg_dumpall` always emits `CREATE ROLE postgres`, which fails on any running cluster. MySQL and MariaDB stop at the first error by default.
3. **Pin moves to phase 3.** With no Rotation, nothing could delete a Backup that Pin protects.
4. **One job at a time per Database.** Backup, restore and "update image with backup" are jobs. While one runs, Start, Stop, Restart, Update image, Apply settings and Delete answer 409 `ErrBusy`. Rename and reads still work.
5. **Update image with "Back up first"** runs as a job that does a `pre_update` Backup, then the update. The route answers 202.
6. **Delete keeping Backups** leaves a tombstone. `deleted_at` is set, the container and volume are removed, and the name becomes `<name> (deleted #<id>)` with slug `deleted-<id>`, so the name is free again. A tombstone shows only its Backups. Deleting its last Backup deletes the row.
7. **Restores are recorded** in `database_restores`. The spec only says a failed restore shows its log; the log needs a home.

## Review Focus

1. **A dump whose exec exits non-zero after writing output.** It must fail the Backup and leave no file, never a truncated `.sql.gz` that looks good. *Test:* Task 3 `TestBackupFailsOnNonZeroExit`.
2. **The panel restarts mid-job.**
   - A `running` Backup or restore row must become `failed` with "the panel stopped during this run".
   - The `.partial` file must be removed.
   - The Database must not stay busy.

   *Test:* Task 3 `TestStartupFailsInterruptedJobs`.
3. **An uploaded file that is gzip but named `.sql`, or plain but named `.sql.gz`.** Detect by the magic bytes `1f 8b`, never by name. *Test:* Task 4 `TestRestoreUploadDetectsGzipByContent`.
4. **A restore whose `pre_restore` Backup fails.** No restore command may run at all. *Test:* Task 4 `TestRestoreAbortsWhenPreBackupFails`.
5. **A Backup file changed on disk** since it was written. The restore must refuse on a sha256 mismatch, before the `pre_restore` Backup. *Test:* Task 4 `TestRestoreRefusesChangedFile`.

---

## File map

| File | Responsibility |
|---|---|
| `internal/migrations/00044_database_backups.sql` | `database_backups`, `database_restores`, `databases.deleted_at` |
| `internal/databases/backup_store.go` | CRUD for both tables |
| `internal/databases/dumps.go` | Per-engine list, dump and restore commands + Postgres error filter |
| `internal/databases/ports.go` | + `ExecRequest`, `Host.Exec` |
| `internal/dockerexec/databases_exec.go` | `DatabaseHost.Exec` over hijacked exec + streaming demux |
| `internal/databases/jobs.go` | Job slot per Database, `WaitIdle`, gate, startup cleanup |
| `internal/databases/backup.go` | `Backup`, `ListLogicalDatabases`, files, download path |
| `internal/databases/restore.go` | `RestoreFromBackup`, `RestoreFromUpload` |
| `internal/databases/service.go` | ErrBusy on lifecycle, UpdateImage with backup, Delete tombstone, deleted filtering |
| `internal/databases/backup_http.go` | Routes |
| `internal/httpapi/*.go`, `internal/panelupdate`, `internal/migration/lock.go` | Wiring, quiesce, Migration wait |
| `web/src/pages/DatabaseBackupsTab.tsx` (+ test), `DatabasePage.tsx`, `databases-view.ts`, `data/{types,api}.ts` | UI |
| `e2e/tests/databases-backup.spec.ts` | e2e |

---

### Task 1: Schema, store and spec deltas

**Files:**
- Create: `internal/migrations/00044_database_backups.sql`, `internal/databases/backup_store.go`, `internal/databases/backup_store_test.go`
- Modify: `internal/databases/store.go` (`DeletedAt`, `Tombstone`), `internal/database/*_test.go` (schema version 43 → 44), `.scratch/databases/spec.md` (the seven decisions above, in the matching sections), `CONTEXT.md` (add **Backup**, **Backup scope**, **Restore**)

**Interfaces — Produces:**
```go
const (TriggerManual, TriggerPreRestore, TriggerPreUpdate = "manual", "pre_restore", "pre_update")
const (JobRunning, JobSucceeded, JobFailed = "running", "succeeded", "failed")
type Scope struct { All bool `json:"all,omitempty"`; Databases []string `json:"databases,omitempty"` }
type Backup struct {
    ID, DatabaseID int64; Trigger string; Scope Scope; Status, Error, Log string
    FileName string; Size int64; SHA256 string; StartedAt time.Time; FinishedAt *time.Time
}
type Restore struct {
    ID, DatabaseID int64; Source string /* "backup"|"upload" */; BackupID, PreBackupID *int64
    UploadName, Status, Error, Log string; StartedAt time.Time; FinishedAt *time.Time
}
func (s *Store) InsertBackup(ctx, b Backup) (Backup, error)       // Status running, StartedAt now
func (s *Store) FinishBackup(ctx, id int64, b Backup) error       // status, error, log, file, size, sha, finished
func (s *Store) GetBackup(ctx, databaseID, id int64) (Backup, error) // ErrNotFound when not this Database's
func (s *Store) ListBackups(ctx, databaseID int64) ([]Backup, error) // newest first
func (s *Store) DeleteBackup(ctx, id int64) error
func (s *Store) InsertRestore(ctx, r Restore) (Restore, error)
func (s *Store) FinishRestore(ctx, id int64, r Restore) error
func (s *Store) ListRestores(ctx, databaseID int64) ([]Restore, error) // newest first, at most 20
func (s *Store) FailRunning(ctx, msg string) (backups []Backup, err error) // every running backup and restore → failed; returns the backups it failed
func (s *Store) Tombstone(ctx, id int64) error // deleted_at=now, name="<name> (deleted #<id>)", slug="deleted-<id>"
// Database gains: DeletedAt *time.Time
```
- **Migration.**
  - `database_backups` and `database_restores` reference `databases(id) ON DELETE CASCADE`, with columns as above (`scope` JSON text; times in ms, as phase 1).
  - Add `ALTER TABLE databases ADD COLUMN deleted_at INTEGER`.
  - Index both tables on `(database_id, started_at)`.

- [ ] **Step 1: Write failing tests:**
  - `TestBackupRoundTrip`: the scope `{databases:[a]}` survives the round trip.
  - `TestListBackupsNewestFirst`.
  - `TestGetBackupOtherDatabase404`: the backup of database 1, asked for under database 2 → `ErrNotFound`.
  - `TestFailRunning`: one running backup and one running restore both become `failed` with the message, and the backup is returned.
  - `TestTombstoneFreesName`: after `Tombstone(1)`, `Insert` of a new `Shop` succeeds, and `Get(1).Name == "Shop (deleted #1)"`.
  - `TestDeleteDatabaseCascades`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'Backup|Restore|Tombstone|FailRunning|Cascades'` → FAIL.
- [ ] **Step 3: Implement** the migration, `backup_store.go` and the `store.go` changes. Update the schema-version asserts in `internal/database/*_test.go`.
- [ ] **Step 4: Run** `go test ./internal/databases/ ./internal/database/` → PASS.
- [ ] **Step 5: Edit** `spec.md` and `CONTEXT.md` with the decisions. **Commit:** `feat: add database backup and restore records`.

---

### Task 2: Non-TTY exec with stdin and streamed output

**Files:**
- Create: `internal/dockerexec/databases_exec.go`, `internal/dockerexec/databases_exec_test.go`
- Modify: `internal/databases/ports.go`, `internal/databases/fakes_test.go`, `internal/dockerexec/databases.go` (keep `socketPath`)

**Interfaces — Produces:**
```go
// databases/ports.go
type ExecRequest struct {
    Cmd, Env       []string
    Stdin          io.Reader // nil = no stdin
    Stdout, Stderr io.Writer // nil = discard
}
// Host gains:
Exec(ctx context.Context, name string, req ExecRequest) (exitCode int, err error)
// dockerexec
func demuxStream(src io.Reader, stdout, stderr io.Writer) error // Docker's 8-byte frame header: [stream,0,0,0,size uint32 BE]
```

**Behavior:**
- `POST /containers/{name}/exec` with `{Cmd, Env, AttachStdin: Stdin != nil, AttachStdout: true, AttachStderr: true, Tty: false}`.
- Start through a hijack, as `TerminalExec.hijack` does. Move that function to a shared unexported helper over a socket path; do not copy it.
- Copy `Stdin` to the connection in a goroutine, then `CloseWrite()` the `*net.UnixConn`.
- `demuxStream` the reader into `Stdout` and `Stderr`.
- Finally `GET /exec/{id}/json` → `ExitCode`.
- Context cancel closes the connection.
- A write error on `Stdout` (for example a full disk) is returned as the error.

- [ ] **Step 1: Write failing tests**, using a fake daemon that hijacks as `TestTerminalExecStartHijacksATtyStream` does:
  - `TestDemuxStream`: three frames (stdout "ab", stderr "x", stdout "c") → stdout "abc", stderr "x". A truncated header → error.
  - `TestDatabaseExecStreamsStdinAndOutput`:
    - the fake echoes the received stdin back as one stdout frame after the client's half-close, and writes one stderr frame;
    - exit code 3 comes from `/exec/e1/json`;
    - assert stdout equals stdin (1 MiB of random bytes), stderr equals the frame, and `code == 3`;
    - the create body has `AttachStdin:true, Tty:false` and the `Env`.
  - `TestDatabaseExecWithoutStdin`: the create body has `AttachStdin:false`, and the client does not wait for stdin.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/ -run 'Demux|DatabaseExec'` → FAIL.
- [ ] **Step 3: Implement**, and add `Exec` to the `databases` fake host. The fake runs a scripted `func(req ExecRequest) (int, error)` per command prefix, so later tasks can script `pg_dump` and others.
- [ ] **Step 4: Run** `go test ./internal/dockerexec/ ./internal/databases/` → PASS. The terminal tests must stay green.
- [ ] **Step 5: Commit** `feat: run non-interactive execs with streamed stdin and output`

---

### Task 3: Dumps, the job slot, and Backup

**Files:**
- Create: `internal/databases/dumps.go`, `dumps_test.go`, `jobs.go`, `backup.go`, `backup_test.go`
- Modify: `internal/databases/service.go` (`Deps.DataDir`, `Deps.UpdateLocked`, and `Startup`)

**Interfaces — Produces:**
```go
// dumps.go
var ErrNoBackups = errors.New("this engine has no backups")
type Dumper interface {
    ListCmd() []string                        // prints one logical database per line
    SystemDatabases() []string                // hidden from ListLogicalDatabases
    DumpCmds(s Scope) [][]string              // run in order, outputs concatenated
    RestoreCmd() []string                     // reads SQL on stdin
    Env(password string) []string             // PGPASSWORD=… / MYSQL_PWD=…
    RestoreFailed(stderr string) bool         // see Decision 2
}
func DumperFor(engine string) (Dumper, error) // ErrNoBackups for redis
// jobs.go
var ErrBusy = errors.New("another backup, restore or update is running on this Database")
var ErrNotRunning = errors.New("start the Database first")
type ErrUnknownLogical struct{ Names []string } // Error(): "no such database: a, b"
func (s *Service) WaitIdle(ctx context.Context) error // panelupdate.ScanWaiter and Migration wait
func (s *Service) Startup(ctx context.Context) error  // Store.FailRunning + remove .partial files; call before ReconcileAll
// backup.go
func (s *Service) ListLogicalDatabases(ctx, id int64) ([]string, error)
func (s *Service) StartBackup(ctx, id int64, sc Scope) (Backup, error)       // 202: row running, job in background
func (s *Service) BackupPath(ctx, id, backupID int64) (string, Backup, error) // for download; only succeeded
func (s *Service) DeleteBackup(ctx, id, backupID int64) error                  // file then row; ErrBusy if it is running
func (s *Service) Jobs(id int64) string                                        // "" | "backup" | "restore" | "update_image"
```
**Commands (exact):**

| Engine | List | Dump (all) | Dump (selected, per db) | Restore | Env |
|---|---|---|---|---|---|
| postgres | `psql -U postgres -At -c "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1"` | `pg_dumpall -U postgres --clean --if-exists` | `pg_dump -U postgres --create --clean --if-exists <db>` | `psql -U postgres -d template1 -v ON_ERROR_STOP=0` | `PGPASSWORD=` |
| mysql | `mysql -uroot -N -e "SHOW DATABASES"` | `mysqldump -uroot --all-databases --single-transaction --routines --events --triggers` | `mysqldump -uroot --databases a b --add-drop-database --single-transaction --routines --events --triggers` (one command) | `mysql -uroot` | `MYSQL_PWD=` |
| mariadb | `mariadb -uroot -N -e "SHOW DATABASES"` | `mariadb-dump` with the mysql flags | `mariadb-dump` with the mysql flags | `mariadb -uroot` | `MYSQL_PWD=` |

- System databases: `postgres`; `information_schema`, `performance_schema`, `mysql`, `sys`.
- Postgres `RestoreFailed`: true when any stderr line contains `ERROR:` and does not contain `role "postgres" already exists` or `current user cannot be dropped`.

**Backup run:**
1. Preconditions:
   - `locked` (Migration) or `UpdateLocked` → `ErrLocked`;
   - `Jobs(id) != ""` → `ErrBusy`;
   - a Database not `running` → `ErrNotRunning` (409, "start the Database first");
   - selected names are checked against `ListLogicalDatabases`, and a missing one fails with `ErrUnknownLogical{Names}` (400).
2. Insert the row, take the slot, and run in the background with `context.WithoutCancel`.
3. Each dump command's stdout goes to `gzip.Writer` → `io.MultiWriter(file, sha256)`.
4. stderr goes to a ring buffer of 64 KiB.
5. A non-zero exit or any error → `FinishBackup(failed)` and remove the partial.

- [ ] **Step 1: Write failing tests** (fake host with scripted exec, `t.TempDir()` as DataDir):
  - `TestDumpCommands`: a table test of the table above, for each engine and scope. Redis → `ErrNoBackups`.
  - `TestPostgresRestoreFailed`:
    - `psql:<stdin>:12: ERROR:  role "postgres" already exists` → false;
    - adding `ERROR:  syntax error at or near "x"` → true.
  - `TestBackupWritesGzipAndHash`: the scripted `pg_dumpall` writes `"SELECT 1;\n"`. After `Wait`:
    - the row is `succeeded`, `Size` > 0, and `SHA256` is the sha of the file bytes;
    - gunzip gives `"SELECT 1;\n"`;
    - the file mode is `0600` and the dir mode `0700`;
    - the name matches `^\d{8}T\d{6}Z-1\.sql\.gz$`;
    - the exec env contains `PGPASSWORD=<password>`, and no argv element contains the password.
  - `TestBackupSelectedRunsOneDumpPerDatabase`: postgres scope `[a,b]` → two `pg_dump` execs, and the file holds both outputs in order.
  - `TestBackupFailsOnNonZeroExit` (Review Focus 1): the script writes output, then exits 1 → `failed`, the stderr is in `Log`, and the backups directory has no file at all.
  - `TestBackupUnknownLogical`: scope `[nope]` → `ErrUnknownLogical`, and no row.
  - `TestBackupBusyAndLocked`: a second `StartBackup` while the first is blocked → `ErrBusy`. With `UpdateLocked` → `ErrLocked`.
  - `TestBackupRedis400`: `ErrNoBackups`.
  - `TestBackupLogKeepsTail`: 100 KiB of stderr → `Log` is 64 KiB and ends with the last bytes.
  - `TestStartupFailsInterruptedJobs` (Review Focus 2): a `running` row and a `.partial` file → after `Startup`: the row is failed with "the panel stopped during this run", no partial is left, and `Jobs(id) == ""`.
  - `TestWaitIdleWaitsForJob`: `WaitIdle` returns only after a blocked job finishes, and returns `ctx.Err()` on cancel.
- [ ] **Step 2: Run** `go test ./internal/databases/` → FAIL.
- [ ] **Step 3: Implement.** The job slot is a `map[int64]string` guarded by `s.mu`, plus a `sync.WaitGroup` for `WaitIdle` (signal through a channel so `WaitIdle` can honour ctx).
- [ ] **Step 4: Run** `go test -race ./internal/databases/` → PASS.
- [ ] **Step 5: Commit** `feat: back up sql databases to gzipped files on the panel`

---

### Task 4: Restore

**Files:**
- Create: `internal/databases/restore.go`, `restore_test.go`

**Interfaces — Produces:**
```go
var ErrChangedFile = errors.New("the backup file changed since it was written; it was not restored")
func (s *Service) RestoreFromBackup(ctx, id, backupID int64, confirm string) (Restore, error)
func (s *Service) RestoreFromUpload(ctx, id int64, confirm, name string, body io.Reader) (Restore, error)
func (s *Service) ListRestores(ctx, id int64) ([]Restore, error)
func (s *Service) ListBackups(ctx, id int64) ([]Backup, error)
```

**Behavior:**
1. Check the preconditions as for a Backup. `confirm != Name` → `ErrConfirm`.
2. From a Backup: the Backup must be `succeeded`, and its file sha256 is recomputed (`ErrChangedFile` on a mismatch). Both checks happen before anything else.
3. An upload streams to `<data dir>/.databases/<id>/uploads/<random>.upload` (`0600`). It is deleted when the restore finishes, whatever the outcome.
4. Insert the restore row and take the slot (`restore`). In the background:
   1. run a whole-instance Backup with trigger `pre_restore`, synchronously inside the job. If it fails → the restore fails with "the pre-restore backup failed", and **no restore exec runs**.
   2. **Postgres only:** exec `psql -U postgres -d template1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND <filter>"`. For a selected-scope Backup, `<filter>` is `datname IN (<quoted names>)`, quoted with `quote_literal` semantics. For a whole-instance Backup or an upload, it is `datname IS NOT NULL AND datname <> 'template1'`.
   3. Exec `RestoreCmd` with stdin = the file. When its first two bytes are `1f 8b`, wrap it in `gzip.NewReader`.
   4. It fails on a non-zero exit, or when `RestoreFailed(stderr)`.
- The selected-scope dialog text (which databases get replaced) comes from `Backup.Scope`. The UI shows it; the server does not enforce it.

- [ ] **Step 1: Write failing tests:**
  - `TestRestoreFromBackupRunsPreBackupThenRestore`: the exec order is `pg_dumpall` (pre), then `psql … pg_terminate_backend`, then `psql … ON_ERROR_STOP=0` with stdin equal to the original SQL (decompressed). The restore is `succeeded` with `PreBackupID` set, and the pre Backup has trigger `pre_restore`.
  - `TestRestoreAbortsWhenPreBackupFails` (Review Focus 4): the scripted `pg_dumpall` exits 1 → the restore is `failed` and no `psql` exec happened.
  - `TestRestoreRefusesChangedFile` (Review Focus 5): flip one byte of the file → `ErrChangedFile`, with no row and no exec.
  - `TestRestoreUploadDetectsGzipByContent` (Review Focus 3): a gzip body named `x.sql` and a plain body named `y.sql.gz` both reach the restore stdin as plain SQL. The upload file is gone afterwards.
  - `TestRestoreFailsOnRealError`: postgres stderr has `ERROR:  syntax error` → `failed` with the log. The `role "postgres" already exists` error alone → `succeeded`.
  - `TestRestoreSelectedTerminatesOnlyItsDatabases`: a `[app]` Backup → the terminate SQL contains `datname IN ('app')` and nothing broader. A name with a `'` is doubled.
  - `TestRestoreMySQLNoTerminate`: mysql runs no terminate exec, and the restore cmd is `mysql -uroot` with `MYSQL_PWD`.
  - `TestRestoreWrongConfirm`: `ErrConfirm`, nothing started.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Restore` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/databases/` → PASS.
- [ ] **Step 5: Commit** `feat: restore sql databases after an automatic backup`

---

### Task 5: Lifecycle changes: busy, update with backup, tombstone delete

**Files:**
- Modify: `internal/databases/service.go`, `service_test.go`, `applied_test.go`

**Interfaces — Changes:**
```go
func (s *Service) UpdateImage(ctx, id int64, backupFirst bool) error // backupFirst: starts a job and returns nil at once; the job runs a pre_update Backup, then the update
func (s *Service) Delete(ctx, id int64, confirm string, deleteBackups bool) error
// View gains: Job string ("" | "backup" | "restore" | "update_image"), Deleted bool
const StatusDeleted = "deleted"
```

**Rules:**
- Start, Stop, Restart, UpdateImage, ApplySettings and Delete return `ErrBusy` while `Jobs(id) != ""`.
- A tombstone (`DeletedAt != nil`):
  - is skipped by `ReconcileAll`, `StopAll`, `StartWanted`, `Volumes` and the Docker ownership;
  - its view has `Status: deleted`, makes no host calls, and refuses every lifecycle action with `ErrNotFound`, except Backup list, download and delete.
- `DeleteBackup` on the last Backup of a tombstone also deletes the Database row and its directory.
- `Delete(..., deleteBackups=true)`, or a Database with no Backups, removes the row and `<data dir>/.databases/<id>/` entirely. Otherwise it tombstones.

- [ ] **Step 1: Write failing tests:**
  - `TestLifecycleBusyDuringBackup`: while a scripted backup blocks, `Stop` returns `ErrBusy`, and so do `Restart`, `UpdateImage`, `ApplySettings` and `Delete`. `Rename` succeeds.
  - `TestUpdateImageBackupFirst`: the `pre_update` Backup runs before `pull`. If the backup fails, there is no pull, and `LastError` says so.
  - `TestDeleteKeepsBackupsAsTombstone`:
    - the container and volume are removed;
    - the view status is `deleted` and the name is `Shop (deleted #1)`;
    - `ReconcileAll` makes no calls for it;
    - `Volumes()` excludes it;
    - a new `Shop` can be created.
  - `TestDeletingLastBackupOfTombstoneDeletesRow`.
  - `TestDeleteWithBackupsRemovesDir`: `deleteBackups=true` → the row and `.databases/1` are gone.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/databases/` → PASS.
- [ ] **Step 5: Commit** `feat: keep backups of a deleted database and block changes while a job runs`

---

### Task 6: HTTP, authz, wiring, quiesce and Migration

**Files:**
- Create: `internal/databases/backup_http.go`, `backup_http_test.go`
- Modify: `internal/databases/http.go` (Delete and UpdateImage bodies, `job` and `deleted` in JSON, new error mappings), `internal/authz/actions.go`, `routescan_test.go`, `internal/httpapi/databases.go`, `httpapi.go`, `internal/panelupdate/service.go` + `watch_quiesce.go` (`DatabaseJobs ScanWaiter`, waited for like `RegistryCleanup`; `WaitingOn` names "a Database backup or restore"), `internal/migration/lock.go` (the `Databases` interface gains `WaitIdle`, called at `StepStopping` right after the Registry cleanup wait), `internal/migration/manifest.go` (no change needed: `.databases/` moves with the data dir; add a test that asserts it)

**Routes:**

| Route | Body → answer | Action |
|---|---|---|
| `GET /api/databases/{id}/logical-databases` | `{databases:[...]}` | — |
| `GET /api/databases/{id}/backups` | `[]BackupJSON` | — |
| `POST /api/databases/{id}/backups` | `{scope}` → 202 `BackupJSON` | `database.backup` |
| `GET /api/databases/{id}/backups/{backupID}/download` | the file, `Content-Type: application/gzip`, `Content-Disposition: attachment; filename="<slug>-<UTC stamp>.sql.gz"` | `database.backup_download` |
| `DELETE /api/databases/{id}/backups/{backupID}` | 204 | `database.backup_delete` |
| `GET /api/databases/{id}/restores` | `[]RestoreJSON` | — |
| `POST /api/databases/{id}/restores` | `{backupId, confirm}` → 202 | `database.restore` |
| `POST /api/databases/{id}/restores/upload?confirm=<name>&name=<file name>` | the raw body → 202 | `database.restore` |
| `POST /api/databases/{id}/update-image` | optional `{backupFirst}`; with it → 202, without it → 204 as before | unchanged |
| `DELETE /api/databases/{id}` | `{confirm, deleteBackups}` | unchanged |

- **BackupJSON:** `{id, trigger, scope, status, error, log, size, sha256, startedAt, finishedAt}`.
- **RestoreJSON:** `{id, source, backupId, preBackupId, uploadName, status, error, log, startedAt, finishedAt}`.
- **Errors:**
  - 409: `ErrBusy`, `ErrNotRunning`, `ErrChangedFile`;
  - 400: `ErrNoBackups`, `ErrUnknownLogical` (message names the databases).
- **Wiring.**
  - `Deps.DataDir = dataDir`.
  - `UpdateLocked = panelUpdates.Locked`, through a closure, because `panelUpdates` is assigned after the service is built.
  - `puDeps.DatabaseJobs = databaseService`.
  - Call `Startup` before `ReconcileAll` in the start goroutine.

- [ ] **Step 1: Write failing tests:**
  - `TestBackupRoutes`: POST 202 → GET list shows it → download has the headers and the bytes → DELETE 204.
  - `TestRestoreUploadRoute`: the raw body reaches the service, and `confirm` is enforced (400).
  - `TestBackupRoutesRedis400`.
  - `TestDeleteBodyDeleteBackups`.
  - `TestDownloadAudited`: the action `database.backup_download` is recorded with the target name.
  - In `internal/panelupdate`, `TestQuiesceWaitsForDatabaseJobs`, in the style of the RegistryCleanup test.
  - In `internal/migration`, `TestStartWaitsForDatabaseJobs` (the order: queue idle → registry idle → database jobs idle → stop Composes), and `TestDataDirItemIncludesDatabaseBackups` (a file under `.databases/1/backups/` is counted in the data-dir size).
- [ ] **Step 2: Run** → FAIL (authz scanner included). **Step 3: Implement.** **Step 4: Run** `go test ./... && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat: serve database backups and restores over the api`

---

### Task 7: Frontend — Backups tab, restore, delete with backups, update with backup

**Files:**
- Create: `web/src/pages/DatabaseBackupsTab.tsx`, `DatabaseBackupsTab.test.tsx`
- Modify: `web/src/data/types.ts`, `api.ts`, `web/src/pages/DatabasePage.tsx`, `DatabasePage.test.tsx`, `databases-view.ts`, `databases-view.test.ts`, `DatabasesPage.tsx` (status `deleted` tag)

**UI requirements:**
- **Backups tab** (SQL engines only; hidden for Redis):
  - **Back up now** opens a `Modal` with a `Radio` "Whole instance" / "Selected databases". "Selected" shows a `Checkbox.Group` loaded from `/logical-databases`. The OK button is disabled until at least one database is ticked.
  - A `Table` of Backups with these columns:
    - started (relative + tooltip absolute);
    - trigger `Tag` (Manual / Before restore / Before update);
    - scope ("Whole instance" or the names);
    - size (human readable);
    - status `Tag`;
    - actions: Download (a link to the download route), Restore, Delete (`Popconfirm`).
  - A failed row expands to show its `error` and `log` in a monospace block.
  - **Restore** opens a `DangerModal` (the existing component) that requires the Database name. Its text:
    - for a selected-scope Backup: "Replaces these databases: a, b. Other databases are untouched.";
    - for a whole-instance Backup or an upload: "Replaces the whole instance.";
    - always: "A backup of the current state is taken first. Apps connected to the affected databases are disconnected."
  - **Restore from file…** uses `Upload` with `beforeUpload` returning false. It sends the raw file to the upload route with `confirm` and `name`, through the same DangerModal.
  - A **Restores** list under the table (last 20): status, source, and a link to the pre-restore Backup; a failed one shows its log.
  - Poll every 2 s while any Backup or restore is `running`, or while `db.job` is set. Otherwise poll every 15 s.
- **DatabasePage:**
  - While `db.job` is set, disable Start/Stop/Restart/Update image/Apply settings/Delete and show an info `Alert` "A backup is running…" (or restore / update).
  - **Update image** becomes a small `Modal` with a "Back up first" `Checkbox`, **on by default** for SQL engines and absent for Redis.
  - **Delete** modal gains an "Also delete its backups" `Checkbox`, off by default. The Settings tab copy replaces "There are no backups yet…".
  - A `deleted` tombstone page shows only the Backups tab and an info `Alert` "This Database was deleted. Its backups are kept until you delete them."
- `databases-view.ts`:
  - `triggerLabel(trigger)`;
  - `scopeLabel(scope)`;
  - `formatBytes(n)`;
  - `backupsPollInterval(db, backups, restores)`;
  - `restoreWarning(scope | 'upload')`, returning the exact strings above.

- [ ] **Step 1: Read** the antd docs: Table (expandable), Modal, Radio, Checkbox, Upload, Popconfirm, Alert, Tag.
- [ ] **Step 2: Write failing tests:**
  - view helpers: each exact string, and `formatBytes(1536) === "1.5 KB"`;
  - Backups tab:
    - selected scope posts `{scope:{databases:["a"]}}`;
    - OK is disabled with none ticked;
    - Restore requires the typed name and posts `{backupId, confirm}`;
    - an upload posts the file to the upload route with `confirm`;
    - a failed row shows its log on expand;
    - the tab is absent for Redis;
  - DatabasePage:
    - with `job: "backup"`, Stop is disabled and the alert shows;
    - Update image posts `{backupFirst:true}` by default;
    - the delete checkbox is off by default and sends `deleteBackups`;
    - a tombstone shows only Backups.
- [ ] **Step 3: Run** `cd web && pnpm vitest run src/pages/Database` → FAIL. **Step 4: Implement.** **Step 5: Run** `pnpm typecheck && pnpm vitest run --maxWorkers=6 && pnpm build` → PASS.
- [ ] **Step 6: Commit** `feat: add the database backups tab and restore`

---

### Task 8: End-to-end and live check

**Files:**
- Create: `e2e/tests/databases-backup.spec.ts`

- [ ] **Step 1:** Run `task e2e:serve` (or `task e2e:reload`), then `source .data/e2e/env`. Use Docker only through the sandbox, and never run `task e2e:down`.
- [ ] **Step 2: Manual check** (`docs/agents/live-checks.md`), for **postgres 18, postgres 16, mysql 8.4 and mariadb 11.8** each:
  1. Create `app` and `other` logical databases, each with a table and a row. On postgres, also create a role `appuser` that owns `app`.
  2. Back up the whole instance → `succeeded`. Download it, then `gunzip -t` and `sha256sum` match the API's `sha256`.
  3. Back up the selection `[app]` → `succeeded`. The file mentions `app` and not `other`.
  4. Delete the rows in both databases. Restore the whole-instance Backup → `succeeded`, and both rows are back. A `pre_restore` Backup exists.
  5. Delete the rows again. Restore the `[app]` Backup → `app`'s row is back and `other` stays empty. On postgres, `appuser` still exists and still owns `app`.
  6. Upload a hand-written `.sql` with a syntax error → the restore is `failed` and its log shows the error. A `pre_restore` Backup exists.
  7. During a long backup (insert 2M rows first), Stop → 409. Then a Panel restart mid-backup (`docker restart relist-panel`) → the row is `failed` with the "panel stopped" message, and no `.partial` is left in `/var/lib/relist-panel/.databases/<id>/backups/`.
  8. Update image with "Back up first" → a `pre_update` Backup, then the update.
  9. Delete `pg16` keeping its Backups → tombstone `pg16 (deleted #n)`. Its Backups download. Deleting the last one removes the row.
  10. Redis: no Backups tab, and `POST /backups` → 400.
- [ ] **Step 3: Write the e2e spec** (postgres only):
  1. create;
  2. insert through a throwaway Compose, or through `docker exec` in the sandbox;
  3. **Back up now** in the UI → `succeeded`;
  4. download (check the status code and the gzip magic);
  5. delete the row, **Restore** with the typed name → `succeeded` → the row is back;
  6. delete the Database keeping its Backups → tombstone.

  Run `task e2e -- databases-backup` twice, then all of `task e2e`.
- [ ] **Step 4: Record** any deviation in `spec.md`. **Commit:** `test: cover database backup and restore end to end`.
