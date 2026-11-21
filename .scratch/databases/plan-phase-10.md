# Databases Phase 10 Implementation Plan — Change major version

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin moves a Database to a higher version of its engine from the Database page: Postgres by dump and restore onto a new volume (rolled back on any failure), MariaDB and Redis by changing the image in place.

**Architecture:** The volume name becomes a stored column on `databases`, and every version change is a row in a new `database_version_changes` table. That row holds the step log, the target volume `relist-db-<id>-v<changeID>`, and the previous volume the Admin can delete later. A `change_version` job runs the Postgres flow in this order: pull, cut clients off, back up every logical database plus the roles and the per-database settings, stop, start the new version on the new volume, restore, then commit everything in one transaction. Any failure removes the new container and volume and recreates the old container from the stored applied spec. MariaDB and Redis reuse Update image's recreate on the same volume. Those changes commit before the new image's first start and never roll back after it, because the new version rewrites the data.

**Tech Stack:** Go, SQLite (goose migration 00053), Docker Engine API via `internal/dockerexec`, React + antd, Playwright.

**Spec:** `.scratch/databases/spec-2.md` — "Phase 10: Change major version" and the Testing section's phase-10 line. Read both first. The **Decisions beyond the spec** below were put to the user. Each one marked *(needs approval)* changes what a task does if the user declines it, and the task says how.

**Branch:** `claude/databases-phase-10` from `claude/databases-phase-9` (e1456f3). The PR goes against `claude/databases-phase-9`.

## Decisions beyond the spec

Facts below marked *verified* were checked on 2026-11-17 in this worktree's dind sandbox (postgres 16.16 / 18.7, redis 8.10.3 / 7.4).

- **A. Clients are cut off before the Postgres dumps** *(needs approval)*. The spec's order dumps while apps still write. Anything written after a database's dump starts would be missing on the new version, with no error anywhere. The job disconnects the container from `relist-network` before step 1, which drops every client and the Direct access proxy. `docker exec` keeps working, and so does the healthcheck on 127.0.0.1. Downtime then starts at the backup instead of at the stop.
  - *If declined:* drop Task 4's Disconnect parts and Task 6's isolation step, and the modal says writes during the backup are not carried over.
- **B. In-place changes never roll back once the new image has started.**
  - Redis 7.4 refuses an RDB that Redis 8 has saved ("Can't handle RDB format version 15", *verified*), and MariaDB does not support a downgrade after `mariadb-upgrade`.
  - A failure before the new container is created (backup, pull) changes nothing.
  - The version and applied spec are committed just before the first start. Any failure after that leaves the Database on the new version, with an error naming the pre_update Backups.
- **C. Fix a phase 2–8 bug in Restore with replace** *(needs approval)*. `DROP DATABASE` deletes the database's `ALTER ROLE … IN DATABASE … SET` and `ALTER DATABASE … SET` rows (*verified*), and a one-database `pg_dump` doesn't carry them. So today a replace-restore silently turns off a Full user's `SET role` (phase 7's ADR-0044). Phase 10 needs that capture-and-reapply step anyway. Task 5 builds it as a helper, and with approval `executeRestore`'s replace path uses it too.
  - *If declined:* skip Task 5 Steps 5–7.
- **D. The `n` in `relist-db-<id>-v<n>` is the version change's row ID,** not a counter. Each attempt gets a name no other attempt or Database ever has. A volume a crashed attempt left behind can therefore be swept at any time without being mistaken for live data.
- **E. Previous version data** is the `from_volume` of a succeeded Postgres change.
  - It is listed until the Admin deletes it.
  - A server Migration copies it (`Volumes()` includes it).
  - Delete Database removes it.
  - The Docker page shows it as owned by Databases, so a cleanup never offers it.
- **F. `MARIADB_AUTO_UPGRADE=1` is only on the first start.** Once the upgraded container is healthy, the job recreates it without that variable and waits for healthy again. The variable is part of the hashed spec: keeping it would leave "Apply settings" lit forever, and dropping it from the stored spec would recreate the container at the next panel start.
- **G. Refused before anything starts:**
  - a superuser password reset still pending (`ErrPendingPassword`)
  - a target tag that is not strictly higher, which the server checks (422)
  - an unknown tag
  - a stopped Database
  - a running job
  - a Migration or Panel update lock
- **H. The new image is pulled before any downtime.** A failed pull changes nothing.
- **I. History and log.** Every change, in place or not, is a `database_version_changes` row with a step log, shown in a "Version changes" card on the Settings tab. The error is `<step>: <cause>`, so it says which step failed.
- **J. The roles dump** is kept as `backups/version-change-<changeID>-roles.sql` (0600) in the Database's data dir. No API serves it, and it goes when the Database is deleted.
- **K. MySQL** has a single offered tag (8.4), so Change version is never offered for it.

## Global Constraints

- Never edit a committed migration. All schema change goes in `internal/migrations/00053_version_changes.sql`, and Down→Up is verified once on a scratch DB. The schema-version pin in `internal/database/files_test.go` moves from `{"53","52"}` to `{"54","53"}`.
- Secrets never reach a response, a stored log, an error, an audit detail or e2e output. That covers the superuser password, managed users' passwords, and the `PASSWORD 'SCRAM-SHA-256$…'` hashes in a roles dump or in psql's echo of a failing statement. Every stderr tail a version change stores goes through `redactPasswords` (Task 3).
- The Database keeps its ID, name, container name `relist-db-<id>`, alias, port, superuser password and managed users. Apps change nothing.
- Tags compare numerically by their dot-separated parts (`7.4` < `8`, `11.4` < `11.8`, `16` < `18`). Downgrades are never offered or accepted.
- `hashSpec` does not include the volume, and must not start to: doing so would recreate every existing Database at the next panel start. Crash recovery relies on the image changing with every version change. Write that down in the ADR.
- Before touching `web/`, read https://ant.design/llms.txt and the pages for Modal, Select, Table, Collapse/Typography. Check them against the installed `antd` version and `docs/GUIDELINES.md`.
- Frontend checks: `pnpm exec tsc -b --noEmit`, `pnpm vitest run` and `pnpm build`. There is no `pnpm lint`.
- e2e runs on a scratch branch merged with `origin/main`, because the sandbox scripts on this stack predate #91. Docker only through this worktree's sandbox (`source .data/e2e/env`), never the host's default. Ask before `task e2e:down`.
- Conventional Commits, no scope. Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

Conditions the spec implies but no task would otherwise test. Each line's test is added to the task that owns the code:

1. **A Database with no alias** (slug fallback `x`) must still be reconnected to `relist-network` after an interrupted change. Reconcile reconnects on `OnNetwork == false`, not on a missing alias. → Task 4 test.
2. **Logical database and role names that need quoting** (`my-app`, `Shop`, a role `report"x`) survive capture and reapply of the owner, ACL and per-database settings. → Task 5 test.
3. **The panel restarts in the middle of a change.** The Database comes back on the old version and old volume, the row is `failed` with the interrupted error, and the half-made volume is swept. → Task 8 test.
4. **Start, Stop, Restart, Update image, Delete or another change while a change runs** each answer 409 (`ErrBusy`) at once, and none of them blocks on the Database's lock for the length of a restore. → Task 6 test.
5. **The rollback itself fails** (the old container won't start). The row is `failed` with both causes, the new volume is still removed, and the Database's LastError names both. → Task 6 test.

---

### Task 1: Migration 00053 and the stores

**Files:**
- Create: `internal/migrations/00053_version_changes.sql`
- Modify: `internal/databases/store.go` (Database.Volume, columns, scan, Insert), `internal/database/files_test.go` (pin)
- Create: `internal/databases/version_store.go`, `internal/databases/version_store_test.go`

**Interfaces:**
- Produces:
  - `Database.Volume string`. `Store.Insert` stores `relist-db-<id>` from the new row's ID, in the same transaction.
  - `type VersionChange struct { ID, DatabaseID int64; FromVersion, ToVersion, Method, Status, Error, Log, FromVolume, ToVolume string; FromVolumeRemovedAt *time.Time; StartedAt time.Time; FinishedAt *time.Time }`
  - consts `VersionMethodDumpRestore = "dump_restore"`, `VersionMethodInPlace = "in_place"`
  - `Status` uses the existing `JobRunning` / `JobSucceeded` / `JobFailed`
  - `(*Store) InsertVersionChange(ctx, VersionChange) (VersionChange, error)`. It fills ID and StartedAt and sets status running.
  - `(*Store) SetVersionChangeLog(ctx, id int64, log string) error`. The UI polls this while the job runs.
  - `(*Store) SetVersionChangeTarget(ctx, id int64, toVolume string) error`
  - `(*Store) FailVersionChange(ctx, id int64, errMsg, log string) error`
  - `(*Store) CommitVersionChange(ctx, VersionCommit) error`, with `type VersionCommit struct { ChangeID, DatabaseID int64; Version, Volume, AppliedSpec, FromVolume, Log string }`. In one transaction it sets `databases.version`, `volume`, `applied_spec` and `last_error=''`, and sets the row to succeeded with `from_volume` and the log. For in place, Volume is the current volume and FromVolume is `""`.
  - `(*Store) ListVersionChanges(ctx, databaseID int64) ([]VersionChange, error)`, newest first
  - `(*Store) GetVersionChange(ctx, databaseID, id int64) (VersionChange, error)`. It returns ErrNotFound when the row doesn't belong to the Database.
  - `(*Store) MarkFromVolumeRemoved(ctx, id int64) error`
  - `(*Store) FailRunningVersionChanges(ctx, msg string) error`
  - `(*Store) FailedTargetVolumes(ctx) ([]string, error)`: the non-empty `to_volume` of failed rows
  - `(*Store) PreviousVolumes(ctx) (map[int64][]string, error)`: Database ID to the `from_volume`s not yet removed

- [ ] **Step 1: Write the failing tests.**
  - `TestInsertSetsVolume`: a new Database reads back `Volume == "relist-db-<id>"`.
  - `TestVersionChangeCommit`: insert a change, commit it, then Get shows version `18` and volume `relist-db-1-v<id>`. The row reads `succeeded` with its FromVolume, and `PreviousVolumes` lists it until `MarkFromVolumeRemoved`.
  - `TestVersionChangeFailKeepsDatabase`: after `FailVersionChange` the Database's version and volume are unchanged. `FailedTargetVolumes` lists the row's ToVolume.
  - `TestFailRunningVersionChanges`.
  - `TestGetVersionChangeOtherDatabase` returns ErrNotFound.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'TestInsertSetsVolume|TestVersionChange|TestFailRunningVersionChanges|TestGetVersionChange'`. Expected: build failure on the undefined names.
- [ ] **Step 3: Write the migration.**
  - `ALTER TABLE databases ADD COLUMN volume TEXT NOT NULL DEFAULT ''`, then `UPDATE databases SET volume = 'relist-db-' || id`. Tombstones get the value too; it is harmless there.
  - `CREATE TABLE database_version_changes` with:
    - `id INTEGER PRIMARY KEY`
    - `database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE`
    - `from_version TEXT NOT NULL`, `to_version TEXT NOT NULL`
    - `method TEXT NOT NULL`, `status TEXT NOT NULL`
    - `error TEXT NOT NULL DEFAULT ''`, `log TEXT NOT NULL DEFAULT ''`
    - `from_volume TEXT NOT NULL DEFAULT ''`, `to_volume TEXT NOT NULL DEFAULT ''`
    - `from_volume_removed_at INTEGER`
    - `started_at INTEGER NOT NULL`, `finished_at INTEGER`
  - An index on `database_id`.
  - Down drops the table, then the column (`ALTER TABLE databases DROP COLUMN volume`).
- [ ] **Step 4: Implement the store methods,** following `backup_store.go`'s unix-second time columns. Move the version pin.
- [ ] **Step 5: Run** the Step 2 command and `go test ./internal/database/`. Expected: PASS. Verify Down→Up on a scratch DB, using phase 9 Task 1's method: a throwaway program under the repo, deleted afterwards.
- [ ] **Step 6: Commit** `feat: store the database volume and version changes`.

### Task 2: Every volume reference reads the stored column

**Files:**
- Modify: `internal/databases/service.go` (specFor, appliedSpec, Delete, Volumes), `internal/databases/ports.go` (targetVolume), `internal/httpapi/databases.go` (ownDatabases)
- Test: `internal/databases/service_test.go` (or the file that tests Delete/Volumes today), `internal/httpapi/databases_test.go`

**Interfaces:**
- Consumes: Task 1's `Database.Volume`, `PreviousVolumes`, `MarkFromVolumeRemoved`.
- Produces:
  - `func targetVolume(id, changeID int64) string`, which gives `"relist-db-<id>-v<changeID>"`.
  - `View.PreviousVolumes []string`.
  - `specFor` and `appliedSpec` use `d.Volume`, falling back to `VolumeName(d.ID)` only when it is `""`. `appliedSpec` no longer overwrites a stored spec's volume with `VolumeName`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSpecUsesStoredVolume`: a Database with `Volume "relist-db-3-v9"` creates its container on that volume.
  - `TestVolumesListsCurrentAndPrevious`: `Volumes()` lists `relist-db-3-v9` and the not-yet-removed previous `relist-db-3`, and never the plain `VolumeName` once it has changed. This is the Migration manifest's source.
  - `TestDeleteRemovesPreviousVolumes`: the fake host records `remove-volume` for the current volume and each previous one.
  - In httpapi, `TestOwnDatabasesMarksPreviousVolumes`.
- [ ] **Step 2: Run** `go test ./internal/databases/ ./internal/httpapi/ -run 'TestSpecUsesStoredVolume|TestVolumesListsCurrentAndPrevious|TestDeleteRemovesPreviousVolumes|TestOwnDatabasesMarksPreviousVolumes'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Then `grep -rn 'VolumeName(' internal | grep -v _test` must show only `ports.go`, the `specFor`/`appliedSpec` fallback and `Store.Insert`.
- [ ] **Step 4: Run** the Step 2 command, then `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: read the database volume from its record`.

### Task 3: Pure helpers — versions, roles dump, redaction, per-database settings SQL

**Files:**
- Create: `internal/databases/version.go`, `internal/databases/version_test.go`
- Modify: `internal/databases/http.go` (DatabaseJSON.Upgrades)

**Interfaces:**
- Produces:
  - `func compareTags(a, b string) int`: numeric by dot parts, with a missing part read as 0.
  - `func HigherVersions(engine, current string) []string`: the engine's offered tags above `current`, ascending, and `[]` (never nil) when there are none.
  - `DatabaseJSON.Upgrades []string` with JSON `upgrades`. It is empty for a tombstone and for grantee JSON.
  - `func filterRoles(dump string) string`. It drops exactly the lines `CREATE ROLE postgres;` and those starting `ALTER ROLE postgres WITH `, and passes everything else through byte for byte, including `\restrict …` / `\unrestrict …`, `ALTER ROLE postgres SET …` and `GRANT … GRANTED BY postgres`.
  - `func redactPasswords(s string) string`: `PASSWORD '<anything up to the closing quote, '' escapes included>'` becomes `PASSWORD '***'`, case-insensitive.
  - `const dbSettingsSQL`: a query for `AdminCmd` that prints one statement per line for every `pg_db_role_setting` row of a database whose name is given as the `pgLit` in a `%s`.
    - For a row with a role: `format('ALTER ROLE %I IN DATABASE %I SET %s TO %L;', role, db, key, value)`.
    - For a row without a role: `format('ALTER DATABASE %I SET %s TO %L;', db, key, value)`.
    - It splits each `setconfig` entry at the first `=`, and passes the key through `quote_ident`.
  - `func dbSettingsQuery(name string) string`.

- [ ] **Step 1: Write the failing tests.**
  - `TestCompareTags` table: `16<18`, `7.4<8`, `11.4<11.8`, `11.8==11.8`, `8.4` vs `8.4.1`.
  - `TestHigherVersions`: postgres from 16 gives `["17","18"]`; mariadb from 11.4 gives `["11.8"]`; redis from 7.4 gives `["8"]`; mysql from 8.4 and postgres from 18 give `[]`.
  - `TestFilterRoles`: the fixture is the *verified* `pg_dumpall -U postgres --roles-only` output from postgres 16.16 with the SCRAM values replaced by fixed fake hashes. Copy it from the sandbox capture in the plan's history: roles `app`, `full_u` and `postgres`, a `User Config "app"` block, and `GRANT app TO full_u WITH INHERIT TRUE GRANTED BY postgres;`. Assert:
    - `postgres`'s two lines are gone
    - `\restrict` / `\unrestrict` and every other line are unchanged
    - the line count drops by exactly 2
  - `TestRedactPasswords`: a psql error echo `LINE 1: ALTER ROLE app WITH … PASSWORD 'SCRAM-SHA-256$4096:abc''d'` contains `***` and none of `SCRAM`, `abc` or `''d`.
  - `TestDBSettingsQueryQuotes`: `dbSettingsQuery("my'db")` contains `'my''db'`.
  - `TestDatabaseJSONUpgrades` in http_test.go: a postgres 16 Database's GET carries `upgrades:["17","18"]`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'TestCompareTags|TestHigherVersions|TestFilterRoles|TestRedactPasswords|TestDBSettingsQuery|TestDatabaseJSONUpgrades'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Run the settings query for real** against a postgres:16 container in the sandbox. Set up `ALTER ROLE full_u IN DATABASE shop SET role = 'app'` and `ALTER DATABASE shop SET work_mem = '8MB'`. Feed the printed lines into a fresh postgres:18 that has the role and the database, and confirm `pg_db_role_setting` matches. Remove the containers afterwards. Record the result in the commit body.
- [ ] **Step 6: Commit** `feat: version, roles and settings helpers for version changes`.

### Task 4: Host — disconnect, network state, health wait *(Disconnect parts need decision A)*

**Files:**
- Modify: `internal/databases/ports.go` (Host, ContainerState), `internal/dockerexec/databases.go`, `internal/databases/service.go` (reconcile, Service fields), `internal/databases/fakes_test.go`
- Test: `internal/dockerexec/databases_test.go`, `internal/databases/service_test.go`

**Interfaces:**
- Produces:
  - `Host.Disconnect(ctx, name string) error`: disconnect from `relist-network`. A container that is not connected returns nil, and so does one that doesn't exist.
  - `ContainerState.OnNetwork bool`
  - `Realias` tolerates a container that is not connected.
  - `reconcile` calls `Realias(name, spec.Alias)` when a running container has `!st.OnNetwork`, even when `spec.Alias == ""`.
  - `(*Service) waitHealthy(ctx, name string) error`. It polls `Inspect` every `s.healthPoll` (default `2*time.Second`) until `Health == "healthy"`.
    - Container not running: it fails at once with `"the container stopped"` plus the last 20 log lines, put through `redactPasswords`.
    - Past `s.healthTimeout` (default `3*time.Minute`): it fails with `"the container did not become healthy within 3m0s"`.
  - fakeHost gains `health map[string]string` (image to the Health after Create, default `healthy`), `stopOnStart map[string]bool` (image to a container that exits) and a `Disconnect` that records `disconnect <name>` and sets `OnNetwork=false`. `Create` sets `OnNetwork=true`.

- [ ] **Step 1: Write the failing tests.**
  - dockerexec `TestDatabaseHostDisconnect` (the request path and body against the fake engine, with 404 / "is not connected" read as nil).
  - dockerexec `TestDatabaseHostInspectOnNetwork`.
  - `TestReconcileReconnectsWithoutAlias`, for Review Focus 1: slug `x`, the container is running, and `OnNetwork=false` gives `realias relist-db-1 ""`.
  - `TestWaitHealthy`: healthy right away; times out with `healthTimeout=20ms`, `healthPoll=1ms`; a stopped container fails fast.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/ ./internal/databases/ -run 'Disconnect|OnNetwork|ReconnectsWithoutAlias|TestWaitHealthy'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Inspect reads `NetworkSettings.Networks[relist-network]` presence for `OnNetwork`.
- [ ] **Step 4: Run** the Step 2 command, then `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: disconnect a database and wait for it to be healthy`.

### Task 5: Shared restore helper and per-database state capture *(Steps 5–7 need decision C)*

**Files:**
- Modify: `internal/databases/restore.go`
- Create: `internal/databases/dbstate.go`
- Test: `internal/databases/restore_test.go`, `internal/databases/dbstate_test.go`

**Interfaces:**
- Consumes: Task 3's `dbSettingsQuery`.
- Produces:
  - `type dbState struct { Name, Prev, Settings string }`. Prev is the trimmed `InspectSQL` line (owner `\t` ACL). Settings is the printed statements of `dbSettingsQuery`.
  - `(*Service) captureState(ctx, d Database, dp Dumper, name string) (dbState, string /*log*/, error)`, Postgres only. For mysql/mariadb, Settings is `""`.
  - `(*Service) restoreInto(ctx, d Database, dp Dumper, st dbState, path string) (log string, err error)`: `ReplaceSQL(st.Name, st.Prev)`, then the file through `RestoreCmd`, then `st.Settings` through `AdminCmd` when it isn't empty. It is the body of today's `executeRestore` after the pre_restore Backup and the InspectSQL.

- [ ] **Step 1: Write the failing tests.**
  - `TestCaptureStateQuotedNames`, for Review Focus 2: names `my-app` and `Shop`, and a scripted admin output with a role `report"x`. The captured Settings reapply verbatim, and the fake's exec log shows `"my-app"` quoted in `ReplaceSQL`.
  - `TestRestoreIntoAppliesSettingsLast`: the order of the fake's exec calls is replace SQL, restore command, settings SQL.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'TestCaptureState|TestRestoreInto'`. Expected: FAIL.
- [ ] **Step 3: Implement,** and make `executeRestore` call `restoreInto` with `dbState{Name: target, Prev: prev}` (no Settings). Every existing restore test must stay green unchanged.
- [ ] **Step 4: Run** `go test ./internal/databases/`. Expected: PASS.
- [ ] **Step 5 (C): Write the failing test** `TestReplaceRestoreKeepsRoleSettings`: a replace-restore of `shop`, where the scripted settings query prints `ALTER ROLE "full_u" IN DATABASE "shop" SET role TO 'app';`, ends with that statement sent through `AdminCmd`. Run it. Expected: FAIL.
- [ ] **Step 6 (C): Implement.** In `executeRestore`'s replace path, call `captureState` before the drop and pass its Settings. A new target has none.
- [ ] **Step 7 (C): Run** `go test ./internal/databases/`. Expected: PASS.
- [ ] **Step 8: Commit** `refactor: share the restore steps and carry per-database settings`. With C, it is `fix: keep per-database role settings on a replace restore` as a separate commit for Steps 5–7.

### Task 6: The Postgres change job

**Files:**
- Create: `internal/databases/version_change.go`, `internal/databases/version_change_test.go`
- Modify: `internal/databases/jobs.go` (`JobKindChangeVersion = "change_version"`, its doc in `Jobs`)

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - `var ErrNotHigher = errors.New("pick a version higher than the current one")`
  - `(*Service) ChangeVersion(ctx, id int64, version string, backupFirst bool) (VersionChange, error)`. It refuses synchronously, in the order of decision G, then inserts the row, takes the job slot and returns the running row. The background job runs the engine's flow. `backupFirst` matters only for MariaDB.
  - The Postgres flow writes one log line per step (`→ <step>`, and `✗ <step>: <cause>` on failure). After each step it calls `SetVersionChangeLog`. On failure the row's error is `<step>: <cause>`, put through `redactPasswords`. The steps, in order and with these exact names:
    1. `Pull postgres:<to>` (`PullImage`)
    2. `Disconnect clients` *(A)*
    3. `Back up <db>` for each logical database (`backupNow` with `TriggerPreUpdate`), then `Back up the roles` (`pg_dumpall -U postgres --roles-only` → `backups/version-change-<changeID>-roles.sql`, 0600), then `Read the settings of <db>` (`captureState`) for each
    4. `Stop postgres:<from>`
    5. `Start postgres:<to> on <toVolume>`: `SetVersionChangeTarget`, then `Create(newSpec)` with `newSpec` = `specFor(d with Version=to, Volume=toVolume)`, then `waitHealthy`
    6. `Restore the roles` (`filterRoles(file)` through `AdminCmd`)
    7. `Restore <db>` for each (`restoreInto` on the new container, with the captured state and the step-3 Backup file)
    8. `Switch to postgres:<to>` (`CommitVersionChange` with the JSON of `newSpec`)
  - **Failure in step 2 or 3:** the old container never stopped, so the rollback is only `Realias(name, alias)`, which reconnects the clients.
  - **Failure in step 4 or later:** the rollback runs `Remove(container)`, then `RemoveVolume(toVolume)` when it was set, then `Create(oldApplied)` and `waitHealthy`. `oldApplied` is `appliedSpec(d)` as stored before the job. It logs `Rolled back to postgres:<from> on <fromVolume>`.
  - **Locking:** `s.lock(id)` is held only around each host mutation (stop, create, the rollback) and the commit, never across a dump or a restore. The job slot keeps every `opIdle` action out, with `ErrBusy`.

- [ ] **Step 1: Write the failing tests.** Use the fakeHost and scripted execs; `pgAdmin` and the dump helpers already exist in restore_test.go.
  - `TestChangeVersionPostgresSucceeds`: from 16 with logical databases `app` and `shop`. Assert:
    - the call order: pull, disconnect, the dumps, stop, `create relist-db-1` on `relist-db-1-v<id>`, the roles restore, the two restores
    - the Database reads version `18` and volume `relist-db-1-v<id>`, with the applied spec's image `postgres:18` and DataPath `/var/lib/postgresql`
    - the row is succeeded with FromVolume `relist-db-1`
    - the roles file exists with mode 0600, and the stdin of the roles restore has no `CREATE ROLE postgres;`
  - `TestChangeVersionRefusals`: not higher (`ErrNotHigher`), unknown tag, a pending superuser password (`ErrPendingPassword`), a stopped Database (`ErrNotRunning`), and a second change while one runs (`ErrBusy`). In every case no row is inserted and the host has no mutating calls.
  - `TestChangeVersionPullFailureNoDowntime`: pull fails. Nothing is disconnected or stopped, the row is failed with error `Pull postgres:18: …`, and the Database is unchanged.
  - `TestChangeVersionRestoreFailureRollsBack`: the spec's step 4 failure. The restore of `shop` exits 1 with stderr echoing `PASSWORD 'SCRAM-SHA-256$x'`. Assert:
    - the row is failed with an error that starts `Restore shop: `
    - neither the error nor the log contains `SCRAM`
    - `remove-volume relist-db-1-v<id>` was recorded
    - the last create is on `relist-db-1` with image `postgres:16`
    - the Database reads version `16` and volume `relist-db-1`
  - `TestChangeVersionUnhealthyRollsBack`: the `postgres:18` health override is `unhealthy`. Rolled back, with an error that starts `Start postgres:18 on `.
  - `TestChangeVersionBusyDoesNotBlock`, for Review Focus 4: during the job, held by a scripted exec that blocks on a channel, Stop, Start, Restart, UpdateImage, Delete and ChangeVersion each return `ErrBusy` within 100ms.
  - `TestChangeVersionRollbackFailure`, for Review Focus 5: both the restore and the old container's re-create fail. The row's error names both, `remove-volume` of the target was still recorded, and the Database's LastError names both.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run TestChangeVersion`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** the entry point and the Postgres flow.
- [ ] **Step 4: Run** the same command, then `go test -race ./internal/databases/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: change a postgres database's major version by dump and restore`.

### Task 7: The in-place change (MariaDB, Redis)

**Files:**
- Modify: `internal/databases/version_change.go`, `internal/databases/version_change_test.go`

**Interfaces:**
- Consumes: Task 6's entry point and log helpers. It reuses `updateImage`'s recreate path through `create`.
- Produces the in-place flow. Redis takes its slot through `begin(ctx, id, JobKindChangeVersion, nil)` because Redis has no Dumper. The steps, in order:
  1. `Back up <db>` for each logical database: MariaDB with `backupFirst` only. A failure stops the job with nothing changed.
  2. `Pull <image>:<to>`. A failure means nothing changed.
  3. `Switch to <image>:<to>`: `CommitVersionChange` with Volume = the current volume, FromVolume `""`, and the applied spec of the new plain spec.
  4. `Start <image>:<to>`. For MariaDB, `create` with the new spec plus env `MARIADB_AUTO_UPGRADE=1`, then `waitHealthy`, then `Recreate without MARIADB_AUTO_UPGRADE` (`create` with the plain spec, then `waitHealthy`). For Redis, `create` plain, then `waitHealthy`.
- A failure in step 4 leaves the version committed. The row is then failed with the step error, and the Database's LastError reads `"<Label> <to> did not start: <cause>. It is not rolled back: <to> has rewritten the data, which <from> cannot read. The pre-update Backups #<ids> hold the data from before the change."`. For Redis, and for MariaDB without a backup, the sentence about Backups is left out.

  The row's status after a step 4 failure is `failed`, even though the version did change. The card shows it as failed, and the Database's version shows the new tag.

- [ ] **Step 1: Write the failing tests.**
  - `TestChangeVersionMariaDBInPlace`: from 11.4 with `backupFirst`. Assert:
    - pre_update Backups exist
    - the first `create` env contains `MARIADB_AUTO_UPGRADE=1`, and the second create's env doesn't
    - the final applied spec has no such env, and `SettingsOutdated` is false
    - version `11.8`, volume unchanged, row succeeded
  - `TestChangeVersionRedisInPlace`: from 7.4. No Backups are attempted, version `8`.
  - `TestChangeVersionInPlaceNoRollback`: the MariaDB 11.8 container stays unhealthy. Version `11.8` stays, there is no create with image `mariadb:11.4` after the switch, the row is failed, and LastError contains `It is not rolled back`.
  - `TestChangeVersionInPlaceBackupFailureChangesNothing`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'TestChangeVersion(MariaDB|Redis|InPlace)'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command, then `go test -race ./internal/databases/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: change mariadb and redis versions in place`.

### Task 8: HTTP, startup recovery, previous version data

**Files:**
- Modify:
  - `internal/databases/http.go`
  - `internal/databases/jobs.go` (Startup)
  - `internal/databases/version_change.go` (DeletePreviousData, SweepFailedVersionVolumes)
  - `internal/httpapi/httpapi.go` (the sweep after ReconcileAll)
  - `internal/authz/routescan_test.go`
- Test: `internal/databases/version_http_test.go`, `internal/databases/version_change_test.go`

**Interfaces:**
- Produces:
  - `POST /api/databases/{id}/change-version` takes `{"version": "18", "backupFirst": true}` and returns 202 with the row's JSON.
  - `GET /api/databases/{id}/version-changes` returns `VersionChangeJSON[]`, where `VersionChangeJSON` is `{id, fromVersion, toVersion, method, status, error, log, previousData /* from_volume when not removed, else "" */, startedAt, finishedAt}`.
  - `DELETE /api/databases/{id}/version-changes/{changeID}/previous-data` returns 204. `ErrNotFound` covers a row with no live previous data.
  - All three are Admin-only and carry `a.target(r, n)`. The POST adds `audit.Detail(ctx, "version", body.version)`.
  - In `writeError`, `ErrNotHigher` gives 422.
  - `(*Service) DeletePreviousData(ctx, id, changeID int64) error`, through `opIdle`: `RemoveVolume(from_volume)`, then `MarkFromVolumeRemoved`.
  - `Startup` calls `FailRunningVersionChanges(ctx, interrupted)`.
  - `(*Service) SweepFailedVersionVolumes(ctx) error` runs `RemoveVolume` for each of `FailedTargetVolumes` and logs a volume still in use without failing. `httpapi.go` calls it right after `ReconcileAll`.

- [ ] **Step 1: Write the failing tests.**
  - `TestChangeVersionAPI`: 202 with the row; 422 on not-higher with the error text; a grantee gets 404 or 403, as the other Admin routes answer.
  - `TestVersionChangesList`.
  - `TestDeletePreviousData`: 204, then the row's `previousData` is `""`, and a second DELETE gives 404.
  - `TestInterruptedChangeRecovers`, for Review Focus 3. Simulate a crash: a running row whose target volume exists, with the container recreated from the new spec but not committed. Then run `Startup`, `ReconcileAll` and `SweepFailedVersionVolumes`. Assert the row is failed with the interrupted error, the last create is on the old volume with the old image, and the target volume was removed.
  - Add the three routes to the Admin list in routescan_test.go.
- [ ] **Step 2: Run** `go test ./internal/databases/ ./internal/authz/ -run 'TestChangeVersionAPI|TestVersionChangesList|TestDeletePreviousData|TestInterruptedChangeRecovers|RouteScan'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command, then `go vet ./... && go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: version change api and recovery after a restart`.

### Task 9: UI — Change version and the Version changes card

Read antd's llms.txt and the Modal, Select, Table, Typography and Collapse pages first.

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/pages/DatabasePage.tsx`, `web/src/pages/databases-view.ts`
- Create: `web/src/components/DatabaseVersionChangesCard.tsx`
- Test: `web/src/pages/DatabasePage.test.tsx`, `web/src/components/DatabaseVersionChangesCard.test.tsx`, `web/src/data/api.test.ts`, `web/src/pages/databases-view.test.ts`

**Interfaces:**
- Consumes: Task 8's JSON.
- Produces:
  - `Database.upgrades: string[]`, the `VersionChange` type, `api.changeDatabaseVersion(id, version, backupFirst)`, `api.listVersionChanges(id)` and `api.deletePreviousData(id, changeID)`.
  - The header gains a **Change version** button next to Update image. It shows when `upgrades.length > 0` and the Database isn't a tombstone, and is disabled while a job runs or the Database isn't running. Its Tooltip says "Start the Database first" when stopped.
  - The modal is titled `Change version`, with a Select labelled `Version` listing `upgrades`; the newest is preselected. The body text depends on the engine:
    - **postgres:** `Backs up every database and the roles, then moves the data to <Label> <to> on a new volume. Apps keep the same host, user and password. Clients are disconnected from the start, and the Database is unavailable until it ends. If any step fails, <from> comes back as it was. The old data is kept as Previous version data until you delete it.`
      - If decision A is declined, the sentence about clients reads instead: `The Database is unavailable from the stop until it ends; anything written during the backup is not carried over.`
    - **mariadb:** `Changes the image in place; MariaDB upgrades its data on the first start of <to>. There is no going back to <from> once it starts.` It also has a **Back up first** Checkbox, ticked by default.
    - **redis:** `Changes the image in place. Redis <to> rewrites the data file in a format <from> cannot read, so there is no going back.`
  - okText is `Change to <to>`. On success: `message.success('Changing <name> to <to>')`.
  - `jobNotice('change_version')` is `'The version is being changed. The Database is unavailable until it ends; other changes wait.'`, and `jobLabel` is `'Changing the version'`.
  - The Settings tab gains a `DatabaseVersionChangesCard`, rendered only when the list isn't empty. It is a Table with these columns:
    - **Change**, showing `<from> → <to>`
    - **Method**, showing `Dump and restore` / `In place`
    - **Status**, a Tag
    - **Started**, a RelativeTime
    - **Error**
  - Expanding a row shows its log in a `<pre>`. While a row runs, it polls every 2s.
  - Each row with `previousData` shows `Previous version data: <volume>` and a danger **Delete** button. It opens `Modal.confirm` with content `Deletes the <from> data kept from before the change. It cannot be undone.` and okText `Delete previous data`.

- [ ] **Step 1: Write the failing tests.**
  - The button hides with no `upgrades`, and is disabled while a job runs.
  - The modal lists `17` and `18` with `18` preselected.
  - Confirming calls `changeDatabaseVersion(1, '18', false)` for postgres. For mariadb it calls `(…, '11.8', true)`, or `false` after unticking.
  - There's no Back up first checkbox for postgres or redis.
  - The card renders the rows and the expanded log, and Delete calls `deletePreviousData` after the confirm.
  - `jobNotice` and `jobLabel` for `change_version`.
  - The api request bodies.
- [ ] **Step 2: Run** `pnpm vitest run src/pages/DatabasePage.test.tsx src/components/DatabaseVersionChangesCard.test.tsx src/data/api.test.ts src/pages/databases-view.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same tests, then `pnpm exec tsc -b --noEmit && pnpm vitest run && pnpm build`. Expected: all green. Under full-suite load, a 5s timeout on a heavy page test is the known flake; rerun alone before believing it.
- [ ] **Step 5: Commit** `feat: change a database's version from its page`.

### Task 10: End to end, ADR, glossary

**Files:**
- Create: `e2e/tests/databases-version.spec.ts`, `docs/adr/0047-major-version-changes.md`
- Modify: `CONTEXT.md`, `.scratch/databases/spec-2.md` (the approved decisions noted under Phase 10 as "phase 10 decision")

Use the helper pattern of `e2e/tests/databases-users.spec.ts` (`createRunning`, `creds`, `settled`, `psqlArgs`, `removeDatabase`). Each test removes its Database at the end.

- [ ] **Step 1: Write the spec's tests.**
  1. **`postgres 16 → 18 keeps data, roles and a managed user`**
     - Setup: a postgres 16 Database with logical databases `app` (a table and a row) and `secret` (restricted, no grants). Add a managed user `full_u` with Full on `app`, reading its password through `creds`.
     - Change to 18 through the header's **Change version** button, picking `18` in the modal. Then `settled`.
     - Assert:
       - the GET shows version `18`
       - `psqlArgs` as postgres reads the row in `app`
       - as `full_u` on `app`, `SELECT current_user` prints the owner of `app` (read from `pg_database` before the change), which proves `SET role` came across
       - `full_u` connecting to `secret` is refused
       - the Version changes card shows `16 → 18`, `Succeeded` and `Previous version data`
     - Delete the previous data, and confirm `docker volume ls` through the sandbox env no longer lists `relist-db-<id>` while it lists `relist-db-<id>-v<changeID>`.
  2. **`a failed restore leaves postgres 16 as it was`**
     - Setup: a postgres 16 Database with `CREATE EXTENSION adminpack` in a logical database `legacy` and a row in a table there. adminpack ships with 16 and is gone from 18, as *verified*.
     - Change to 18 through the API, then `settled`.
     - Assert:
       - version `16`
       - the row is still readable
       - the latest version change is `failed` with an error that starts `Restore legacy: `
       - the target volume `relist-db-<id>-v<changeID>` is not in `docker volume ls`
  3. **`mariadb 11.4 → 11.8 and redis 7.4 → 8 change in place`**
     - MariaDB with a table and row, changed with Back up first: version `11.8`, the row is readable, and `settingsOutdated` is false.
     - Redis with `SET k v`: version `8`, and `GET k` gives `v`.
- [ ] **Step 2: Run on a scratch merge.**
  1. `git checkout -b scratch/e2e-phase10 && git merge --no-edit origin/main`
  2. `task e2e -- databases-version`, with the output to a log file, reading the tail. Expected: 3 passed.
  3. Then the full `task e2e`. Expected: all pass.
  4. Fixes go back to `claude/databases-phase-10` as commits, and the scratch branch is deleted.
- [ ] **Step 3: Write ADR-0047.**
  - Decisions A–K, and the rejected options:
    - `pg_upgrade` (it needs both versions' binaries in one image)
    - downgrade
    - rolling back an in-place change
    - a counter for `v<n>`
  - The hashSpec/volume note from Global Constraints.
  - Consequences, including that previous data doubles disk use until it is deleted.
- [ ] **Step 4: Add CONTEXT.md terms** **Change version** and **Previous version data** after **Update image**.
- [ ] **Step 5: Commit** `test: prove version changes end to end` and `docs: adr 0047 major version changes`.
