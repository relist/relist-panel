# Databases phase 3 (Schedule, Rotation, Backup destinations) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Each SQL Database can back up on a schedule, rotate its scheduled Backups locally and in a Backup destination (S3-compatible), and pin the ones Rotation must keep.

**Architecture:**
- **Part A (Tasks 1–4) is local only and ships on its own:**
  - the schedule rules move from the Registry into `internal/schedule`;
  - each Database gets a schedule row, a minute ticker that runs due schedules one at a time host-wide inside the existing job slot, and a pure Rotation function;
  - Pin.
- **Part B (Tasks 5–8) adds Backup destinations** in a new package, `internal/backupdest`, with a library page, store and minio-go client. It also adds uploads after scheduled Backups, and remote Rotation and deletion.

**Tech Stack:** Go, SQLite/goose, `github.com/minio/minio-go/v7` v7.4.1 (new), React 19, antd 6.5.1, vitest and Playwright.

**Spec:** `.scratch/databases/spec.md`, sections "Backup schedule and Rotation" and "Backup destinations". The phase 3 decisions are already applied there. It builds on phases 2 and 2b (PR #81, `plan-phase-2.md` and `plan-phase-2b.md`).

## Global Constraints

- **Migrations.** `00046` and up only. `00041`–`00045` are committed and pushed. Bump the schema-version asserts in `internal/database/*_test.go` with each migration.
- **Admin only.**
  - Every new route goes in `adminOnly` (`internal/authz/routescan_test.go`).
  - Every change goes in `authz.Actions`, and so does the destination Test.
  - A destination's secret never appears in a response, a log, an error or audit detail.
- **Triggers.** Add `TriggerScheduled = "scheduled"`. Remove the comment "Phase 2 never deletes any of them automatically". Only `scheduled` Backups are ever rotated or pinned.
- **Schedule defaults** (a Database with no schedule row reads these):
  - enabled `false`;
  - days `["daily"]`, minute `180` (03:00);
  - all databases;
  - no destination;
  - local keep last `7`;
  - remote keep last `30`;
  - both "days" rules blank.
- **Rotation limits:**
  - "keep last N" and "keep at most X days" are each `null` (no limit) or ≥ 1;
  - weekdays are `mon`–`sun` or `daily`;
  - minute is 0–1439.
- **The shared schedule package is a move, not a rewrite.** The Registry's existing schedule tests move unchanged and pass. The days/time controls extracted from `RegistryCleanupCard.tsx` keep the Registry's tests passing unchanged.
- **Uploads** pass the file's size to `PutObject` (never `-1`), so minio-go does not buffer parts in memory.
- **Docs.** Write ADR-0040 "Backup destinations", covering the library, secrets in the panel database, the remote-deletion rule and the dependency. Add `CONTEXT.md` terms for Backup destination, Rotation, Pin and Remote only.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **Frontend.** Read `https://ant.design/llms.txt` and the pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.

## State tables (the decisions the tasks implement)

**A Backup's copies.**

| Local file | Live remote copy | What the row is |
|---|---|---|
| yes | no | as in phase 2 |
| yes | yes | as in phase 2, plus "Copied to <destination>" |
| no (rotated) | yes | **Remote only**: download and restore answer 409 `ErrRemoteOnly` |
| no | no | the row is deleted |

- A "live" copy is an upload row with `status = 'uploaded'` and `deleted_at IS NULL`.
- A failed Backup has neither, and stays until it is rotated (scheduled) or deleted by hand.
- A tombstone's "last Backup" counts every row, including Remote only rows.

**A scheduled run** (one tick, one Database):

| Situation | `last_run_at` | What happens |
|---|---|---|
| `last_run_at` is null, or the schedule was just enabled | set to now | Armed; nothing runs. |
| A job is running on the Database, or the Migration or Panel update lock is held | unchanged | Tried again next minute. |
| The Database is not running | set | `last_error` = "skipped: the Database was not running"; no rows. |
| Due and runnable | set **before** running | One `scheduled` row per database; then uploads; then Rotation. `last_error` = "" or the first failure. |
| A selected database is gone | set | A failed row naming it; the others run. |
| The panel stops mid-run | already set | `Startup` fails the running rows and uploads. No repeat until the next due time. |

## Review Focus

1. **A run of failed scheduled Backups** (the dump keeps failing for a week). Rotation must keep the newest succeeded Backup of each database, whatever N and X say. *Test:* Task 2 `TestRotate` rows "failures never empty" and "age keeps newest ok".
2. **Local Rotation of a Backup that still has a remote copy.** Only the file goes. The row stays Remote only, and remote Rotation or a manual delete removes the copy later. *Test:* Task 7 `TestRotateLocalKeepsRemoteOnlyRow`.
3. **A remote delete that fails.** For a manual delete, or a Database delete with its Backups: no local file, row or container is removed, and the error names the destination. *Test:* Task 7 `TestDeleteBackupRemoteFailureKeepsAll` and `TestDeleteDatabaseRemoteFailureKeepsAll`.
4. **Editing a destination's endpoint and leaving the secret blank.** It is refused, so the stored secret is never sent to the new host. *Test:* Task 5 `TestUpdateEndpointNeedsSecret`.
5. **A schedule due while a manual Backup runs, or while the panel updates.** It runs on a later tick, once the slot is free; it is not lost and not marked run. *Test:* Task 3 `TestSchedulerRetriesWhenBusy`.

---

## File map

| File | Responsibility |
|---|---|
| `internal/schedule/schedule.go` (new) | `Rule`, `Due`, `Next`, `NormalizeDays`, `TimeZone`, moved from `internal/registry/schedule.go` and `registry/http.go` |
| `internal/migrations/00046_database_backup_schedules.sql` | schedules table; `database_backups.pinned` and `local_removed_at` |
| `internal/databases/schedule_store.go` (new) | schedule CRUD |
| `internal/databases/rotation.go` (new) | `Rotate`, the pure function |
| `internal/databases/scheduler.go` (new) | `RunScheduler`, `tick`, `runScheduled`, `rotate` |
| `internal/databases/schedule_http.go` (new) | schedule and pin routes |
| `internal/migrations/00047_backup_destinations.sql` | `backup_destinations`, `database_backup_uploads`, `database_backup_schedules.destination_id` |
| `internal/backupdest/` (new) | store, validation, the S3 client, HTTP library routes |
| `internal/databases/uploads.go` (new) | upload, retry, remote delete, remote Rotation |
| `web/src/components/ScheduleFields.tsx` (new) | days and time controls shared with `RegistryCleanupCard` |
| `web/src/components/DatabaseScheduleCard.tsx` (new) | schedule form on the Backups tab |
| `web/src/pages/BackupDestinationsPage.tsx` (new) | Data → Backup destinations |

---

## Part A: schedule, local Rotation and Pin

### Task 1: Shared schedule package

**Files:**
- Create: `internal/schedule/schedule.go` and `schedule_test.go` (moved from `internal/registry/schedule_test.go`)
- Modify: `internal/registry/schedule.go` (delete), `cleaner.go`, `http.go`, `http_test.go`

**Interfaces — Produces:**
```go
package schedule
type Rule struct{ Days []string; Minute int } // Days: "daily" or "mon".."sun"
func Due(now time.Time, r Rule, lastRunAt *time.Time) bool // as registry.Due, minus Enabled; nil lastRunAt is never due (armed by the caller)
func Next(now time.Time, r Rule) *time.Time
func NormalizeDays(in []string) ([]string, error) // registry putCleanup's rules and message: "daily" wins, sorted mon..sun, deduplicated
func TimeZone(now time.Time) string            // registry's timeZone, "UTC (UTC+00:00)"
```

- [ ] **Step 1: Move the tests.**
  - `TestDueWeekdaysAndMakeUp` and the other schedule tests go to the new package, adapted only in the call shape: `Due(now, Rule{Days, Minute}, lastRunAt)`.
  - The registry tests that exercise `CleanupSettings` stay where they are.
  - Add `TestNormalizeDays`, with these rows:
    - `["thu","mon","mon"]` → `["mon","thu"]`;
    - `["mon","daily"]` → `["daily"]`;
    - `[]` → error;
    - `["sat","xyz"]` → error "days must be daily or weekdays mon to sun".
- [ ] **Step 2: Run** `go test ./internal/schedule/`. Expected: FAIL (no package).
- [ ] **Step 3: Implement** the package by moving the code. `registry` calls `schedule.Due(now, schedule.Rule{Days: c.Days, Minute: c.Minute}, c.LastRunAt)` behind its own `Enabled` check. `putCleanup` uses `NormalizeDays`.
- [ ] **Step 4: Run** `go test ./internal/schedule/ ./internal/registry/...`. Expected: PASS. **Commit** `refactor: move the schedule rules into a shared package`

### Task 2: Schedule store, Pin and Rotation

**Files:**
- Create: `internal/migrations/00046_database_backup_schedules.sql`, `internal/databases/schedule_store.go`, `rotation.go`, and the matching `*_test.go`
- Modify: `backup_store.go`, `backup.go` (`BackupPath` and `ListBackups` respect `LocalRemovedAt`)

**Migration:**
- `database_backup_schedules`, with columns:
  - `database_id INTEGER PRIMARY KEY REFERENCES databases(id) ON DELETE CASCADE`;
  - `enabled INTEGER NOT NULL`;
  - `days TEXT NOT NULL` (JSON);
  - `minute INTEGER NOT NULL`;
  - `all_databases INTEGER NOT NULL`;
  - `databases TEXT NOT NULL` (JSON);
  - `local_keep_count INTEGER`, `local_keep_days INTEGER`;
  - `remote_keep_count INTEGER`, `remote_keep_days INTEGER`;
  - `last_run_at INTEGER`;
  - `last_error TEXT NOT NULL DEFAULT ''`.
- `ALTER TABLE database_backups ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0` and `ADD COLUMN local_removed_at INTEGER`.

**Interfaces — Produces:**
```go
type Keep struct{ Count, Days *int }
type Schedule struct {
	DatabaseID int64; Enabled bool; Days []string; Minute int
	All bool; Databases []string
	Local, Remote Keep
	LastRunAt *time.Time; LastError string
}
func DefaultSchedule(id int64) Schedule // the Global Constraints defaults
func (s *Store) GetSchedule(ctx, id int64) (Schedule, error)  // DefaultSchedule when no row
func (s *Store) SaveSchedule(ctx, sc Schedule) error          // upsert; enabled false→true sets last_run_at = now (armed)
func (s *Store) ListEnabledSchedules(ctx) ([]Schedule, error) // ordered by database_id
func (s *Store) SetScheduleRun(ctx, id int64, at time.Time, lastErr string) error
func (s *Store) SetPinned(ctx, backupID int64, pinned bool) error
func (s *Store) SetLocalRemoved(ctx, backupID int64, at time.Time) error
// Backup gains Pinned bool, LocalRemovedAt *time.Time.

type RotItem struct{ ID int64; Group string; At time.Time; OK bool }
// Rotate returns the IDs to delete. The caller passes only candidates
// (scheduled, finished, not pinned, and the copy in question still present).
func Rotate(items []RotItem, k Keep, now time.Time) []int64
var ErrRemoteOnly = errors.New("this Backup is only in its Backup destination; download it from the bucket")
var ErrNotScheduled = errors.New("only scheduled Backups can be pinned")
```

**`Rotate` algorithm:**
- Group by `Group`, which is the logical name.
- In each group, sort newest first by `At`, with ties broken by higher `ID` first. `keep` is the first item with `OK`.
- Every other item at index `i` is deleted when either holds:
  - `k.Count != nil && i >= *k.Count`;
  - `k.Days != nil && now.Sub(At) > Days×24h`.
- Failed items count toward N, like any other.

- [ ] **Step 1: Write the failing tests.**
  - `TestRotate` is a table test. `ok` and `fail` are items in one group, newest first.

    | Row | Items | Keep | Expected deletion |
    |---|---|---|---|
    | count | 5 × ok | N=3 | the 2 oldest |
    | age | ok at 1d, 5d and 9d | X=7 | the 9d one |
    | both | 4 × ok at 1d, 2d, 3d and 10d | N=3, X=7 | the 10d one only |
    | failures never empty | `fail, fail, fail, ok` | N=2 | `fail#3`; the `ok` survives at index 3 |
    | age keeps newest ok | `ok` at 30d | X=7 | none |
    | no limits | any | nothing set | none |
    | per database | `a`×3 and `b`×1 | N=1 | 2 from `a`, none from `b` |
  - Store tests:
    - `GetSchedule` on an absent row equals `DefaultSchedule`;
    - `SaveSchedule` round-trips;
    - enabling sets `last_run_at`;
    - saving while already enabled keeps it;
    - `SetPinned` round-trips.
  - `TestBackupPathRemoteOnly`: with `LocalRemovedAt` set, `BackupPath` returns `ErrRemoteOnly`, and the restore routes do too.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'Rotate|Schedule|Pinned|RemoteOnly'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Bump the schema asserts to 46.
- [ ] **Step 4: Run** the Step 2 command. Expected: PASS. **Commit** `feat: store backup schedules, pins and a rotation rule`

### Task 3: Scheduler, scheduled runs and local Rotation

**Files:** Create `internal/databases/scheduler.go` and `scheduler_test.go`. Modify `backup.go`, `service.go` (Delete removes the schedule row) and `internal/httpapi/httpapi.go` (start `RunScheduler` beside the Registry cleaner).

**Interfaces — Produces:**
```go
func (s *Service) RunScheduler(ctx context.Context)             // tick, then sleep a minute, until ctx ends (registry Cleaner's loop)
func (s *Service) tick(ctx context.Context)                      // every enabled schedule in order; then rotateAll
func (s *Service) runScheduled(ctx context.Context, sc Schedule) // inside the job slot, synchronous
func (s *Service) rotateLocal(ctx context.Context, id int64, k Keep) error
// tryJob takes the slot without waiting; false when busy.
func (s *Service) tryJob(id int64, kind string) bool
```

- The tick follows the scheduled-run table above.
- `runScheduled` calls `backupNow(…, TriggerScheduled, nil)` per name.
  - Names come from the list (All), or from `sc.Databases`. A missing name inserts a failed row with `ErrUnknownLogical{[]string{name}}.Error()`.
  - Afterwards it runs `rotateLocal`.
- **`rotateLocal`:**
  - Candidates: `scheduled`, not running, not pinned, `LocalRemovedAt == nil`. `OK` is `status == succeeded`.
  - For each ID to delete: remove the file. If there is a live upload (Part B; until then, never), call `SetLocalRemoved`. Otherwise delete the row.
- **`rotateAll`:** runs `rotateLocal` for each enabled schedule whose Database `tryJob(id, "rotate")` gets. A busy one is skipped this tick.
- A tombstone or a Redis Database never has an enabled schedule. `SaveSchedule` refuses them with `ErrNotFound` / `ErrNoBackups`.

- [ ] **Step 1: Write the failing tests** (fake clock and fake host):
  - `TestSchedulerArmsNewSchedule`: the first tick sets `last_run_at` and makes no rows.
  - `TestSchedulerRunsDue`:
    - with clock 03:00 and `last_run_at` yesterday, there is one `scheduled` row per listed database and `last_run_at = now`;
    - a second tick in the same minute makes no rows.
  - `TestSchedulerRetriesWhenBusy` (Review Focus 5):
    - while a manual Backup holds the slot, the tick makes no rows and `last_run_at` stays unchanged;
    - after the slot frees, the next tick runs;
    - the same holds with `UpdateLocked` returning true.
  - `TestSchedulerSkipsStopped`: the Database is not running. `last_error` = "skipped: the Database was not running", `last_run_at` is set, and there are no rows.
  - `TestScheduledMissingDatabaseFailsOnlyIt`: `sc.Databases = ["app","gone"]` gives `app` succeeded and `gone` failed with "no such database: gone".
  - `TestRotateLocalAfterRun`: N=2 and 3 runs leave the files of the 2 newest. Manual and `pre_restore` rows are untouched, and a pinned old one survives.
  - `TestSchedulerRunsOneAtATime`: two Databases due in the same tick. Their dump execs never overlap; the fake host records start and end times.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Scheduler -race`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the Step 2 command and `go test ./internal/databases/...`. Expected: PASS. **Commit** `feat: run scheduled database backups and rotate them`

### Task 4: Schedule API and UI (local)

**Files:**
- Create `internal/databases/schedule_http.go` and its test.
- Modify `authz/actions.go` and `routescan_test.go`.
- Web: create `components/ScheduleFields.tsx` and `components/DatabaseScheduleCard.tsx`, with tests. Modify `RegistryCleanupCard.tsx` to use `ScheduleFields`. Modify `pages/DatabaseBackupsTab.tsx`, `databases-view.ts`, `data/api.ts` and `data/types.ts`.

**Routes:**

| Route | Body → reply | Action |
|---|---|---|
| `GET /api/databases/{id}/schedule` | `ScheduleJSON` + `next` + `timeZone` | — |
| `PUT /api/databases/{id}/schedule` | `ScheduleJSON` → 200 the same | `database.schedule` |
| `PUT /api/databases/{id}/backups/{backupID}/pin` | `{pinned}` → 204 | `database.backup_pin` |

- **ScheduleJSON:** `{enabled, days, minute, all, databases, localKeepCount, localKeepDays, remoteKeepCount, remoteKeepDays, lastRunAt, lastError}`. A keep value is a number or null.
- **Errors:**
  - 422 with the `NormalizeDays` message, or "the time must be between 00:00 and 23:59";
  - 422 "keep at least 1" for 0 or a negative;
  - 400 `ErrNoScope` for `all:false` with no names;
  - 409 `ErrNotScheduled` on pin.
- **BackupJSON** gains `pinned` and `localRemoved`.

**UI:**
- **`ScheduleFields`** is the Registry card's "Runs" radio (Daily or Weekdays), the weekday checkboxes and "At" TimePicker, with "Panel time zone: …" underneath, lifted as is. `RegistryCleanupCard` renders it.
- **`DatabaseScheduleCard`** sits above the Backups table. It has:
  - a Switch "Scheduled backups";
  - `ScheduleFields`;
  - a Radio "All databases" / "Choose databases" (the same control as Back up now);
  - "Keep on this server": two `InputNumber`s, "last N backups" and "at most X days", with placeholder "No limit" and min 1;
  - "Next run …" and `lastError` as an Alert `type="warning"`;
  - Save.
- **The Backups table:**
  - the Trigger tag "Scheduled";
  - a pin toggle (`PushpinOutlined` button, `aria-label` "Pin" / "Unpin") on scheduled rows only;
  - a "Remote only" Tag with Download and Restore disabled when `localRemoved`.

- [ ] **Step 1: Write the failing tests.**
  - Go HTTP: each error above. A PUT then GET round-trips. Pinning a manual Backup answers 409.
  - Vitest:
    - the card posts `{enabled:true, days:["mon","thu"], minute:150, all:true, localKeepCount:7, localKeepDays:null, …}`;
    - a cleared keep field sends `null`;
    - pin posts `{pinned:true}`;
    - the pin button is absent on manual rows;
    - a `localRemoved` row has Download disabled;
    - `RegistryCleanupCard`'s tests pass unchanged.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run HTTP` and `cd web && pnpm vitest run src/pages/Database src/components/DatabaseScheduleCard src/components/RegistryCleanupCard`. Expected: FAIL.
- [ ] **Step 3: Implement.** **Step 4: Run** `pnpm typecheck && pnpm vitest run --maxWorkers=6 && pnpm build`, `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: schedule database backups from the backups tab`

**Part A is complete here:** schedules, local Rotation and Pin work without any destination.

---

## Part B: Backup destinations

### Task 5: Backup destinations library (backend)

**Files:**
- Create `internal/migrations/00047_backup_destinations.sql` and `internal/backupdest/{store,config,s3,http}.go`, with tests.
- Create `docs/adr/0040-backup-destinations.md`.
- Modify `go.mod` (`github.com/minio/minio-go/v7 v7.4.1`), `authz`, `httpapi.go` and `CONTEXT.md`.

**Migration:**
- `backup_destinations`, with columns:
  - `id`;
  - `name TEXT NOT NULL UNIQUE`;
  - `type TEXT NOT NULL`;
  - `config TEXT NOT NULL`;
  - `secret TEXT NOT NULL`;
  - `last_test_at INTEGER`, `last_test_ok INTEGER NOT NULL DEFAULT 0`, `last_test_error TEXT NOT NULL DEFAULT ''`;
  - `created_at`, `updated_at`.
- `database_backup_uploads`, with columns:
  - `id`;
  - `backup_id REFERENCES database_backups(id) ON DELETE CASCADE`;
  - `destination_id REFERENCES backup_destinations(id) ON DELETE CASCADE`;
  - `object_key TEXT NOT NULL`;
  - `status TEXT NOT NULL` (`running`, `uploaded` or `failed`);
  - `error TEXT NOT NULL DEFAULT ''`;
  - `uploaded_at INTEGER`, `deleted_at INTEGER`.
- `ALTER TABLE database_backup_schedules ADD COLUMN destination_id INTEGER REFERENCES backup_destinations(id)`.

**Interfaces — Produces:**
```go
package backupdest
type S3Config struct{ Endpoint, Region, Bucket, Prefix, AccessKeyID string; PathStyle bool }
type Destination struct{ ID int64; Name, Type string; S3 S3Config; Secret string; LastTestAt *time.Time; LastTestOK bool; LastTestError string }
type Remote interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error // checks the object's size after
	Delete(ctx context.Context, key string) error                        // a missing object is not an error
	Test(ctx context.Context) error                                      // put, get, compare, delete <prefix>/.relist-test-<16 hex>
}
type Opener interface{ Open(ctx context.Context, id int64) (Destination, Remote, error) }
func ValidateS3(c S3Config) (S3Config, error) // trims; Endpoint is http(s)://host[:port], no path; Bucket non-empty, no "/"; Prefix trimmed of "/"
func ObjectKey(prefix, dbSlug string, dbID int64, logical, file string) string
// Usage reports what still uses a destination, for delete.
type Usage struct{ Schedules []string /* Database names */; Copies int }
```

- **`ObjectKey`:** joins the non-empty parts with `/`. `logical` keeps `[A-Za-z0-9._-]` and maps any other byte to `_`.
- **The S3 client:** `minio.New(host, &minio.Options{Creds: credentials.NewStaticV4(id, secret, ""), Secure: scheme=="https", Region: region, BucketLookup: PathStyle ? minio.BucketLookupPath : minio.BucketLookupAuto})`.
- **Errors** returned to the browser are minio's message text, with the secret never included.

**Routes:**

| Route | Body → reply | Action |
|---|---|---|
| `GET /api/backup-destinations` | `[]DestinationJSON` (no secret) | — |
| `POST /api/backup-destinations` | `{name, type:"s3", s3:{…}, secret}` → 201 | `backup_destination.create` |
| `PUT /api/backup-destinations/{id}` | same; empty `secret` keeps the stored one | `backup_destination.update` |
| `DELETE /api/backup-destinations/{id}?confirm=<copies>` | → 204 | `backup_destination.delete` |
| `POST /api/backup-destinations/{id}/test` | → 200 `DestinationJSON` | `backup_destination.test` |

**Delete:**
- While `Usage.Schedules` is non-empty, it answers 409 "used by the schedules of: A, B".
- While there are copies and `confirm` ≠ the copy count, it answers 409 with `{copies:N}`.
- Otherwise the delete cascades the upload rows. Then it deletes each Backup row that is left with `local_removed_at` set and no upload, in the same transaction.

- [ ] **Step 1: Write the failing tests:**
  - `TestValidateS3`:
    - `https://s3.amazonaws.com/` is accepted;
    - `http://minio:9000` is accepted;
    - `ftp://x`, `https://h/path`, an empty bucket and `a/b` as the bucket are refused;
    - the prefix `/x/y/` becomes `x/y`.
  - `TestObjectKey`:
    - `("", "shop", 3, "app", "f.sql.gz")` → `shop-3/app/f.sql.gz`;
    - `("b/c", …, "my db", …)` → `b/c/shop-3/my_db/f.sql.gz`.
  - `TestUpdateEndpointNeedsSecret` (Review Focus 4): a PUT with a new endpoint and an empty secret answers 422 on the `secret` field. The same endpoint with an empty secret keeps the stored secret.
  - `TestResponsesNeverCarrySecret`: list, create, update and test bodies don't contain the secret string. Neither does the audit detail; the authz record tests cover it.
  - `TestDeleteDestination`:
    - used by a schedule → 409 naming the Database;
    - with 2 copies and no confirm → 409 `{copies:2}`;
    - with `confirm=2` → 204;
    - a Remote only Backup that had its only copy there is gone.
  - `TestTestRecordsResult`: with a fake Remote, ok and failed results are stored.
- [ ] **Step 2: Run** `go test ./internal/backupdest/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Bump the schema asserts to 47.
- [ ] **Step 4: Run** `go test ./... && go vet ./...`. Expected: PASS. **Commit** `feat: add backup destinations for s3-compatible storage`

### Task 6: Uploads after scheduled Backups, and Retry

**Files:** Create `internal/databases/uploads.go` and `uploads_test.go`. Modify `scheduler.go`, `jobs.go` (`Startup`), `schedule_store.go` (`DestinationID *int64`), `backup_store.go` (upload rows) and `schedule_http.go`.

**Interfaces — Produces:**
```go
type Upload struct{ ID, BackupID, DestinationID int64; ObjectKey, Status, Error string; UploadedAt, DeletedAt *time.Time }
func (s *Store) InsertUpload(ctx, u Upload) (Upload, error)
func (s *Store) FinishUpload(ctx, id int64, status, errMsg string, at time.Time) error
func (s *Store) LiveUpload(ctx, backupID int64) (*Upload, error) // status uploaded, not deleted
func (s *Service) upload(ctx context.Context, d Database, b Backup, destID int64) Upload
func (s *Service) RetryUpload(ctx context.Context, id, backupID int64) error // job slot "upload"; only a failed upload of a Backup whose file is present
```

- Deps gain `Destinations backupdest.Opener`. With it nil, schedules cannot set a destination.
- `runScheduled` uploads each succeeded Backup when `sc.DestinationID != nil`. It does this before `rotateLocal`, so a fresh copy exists before old files go.
- An upload failure sets the upload `failed` and `last_error` "upload to <name> failed: …". The Backup stays succeeded.
- `Startup` also fails `running` uploads with "the panel stopped during this run".
- **Routes:**
  - `POST /api/databases/{id}/backups/{backupID}/retry-upload` → 202 (`database.backup_upload`).
  - The schedule JSON gains `destinationId`.
  - BackupJSON gains `upload: {destination, status, error, uploadedAt} | null`.

- [ ] **Step 1: Write the failing tests** (fake Opener and Remote):
  - `TestScheduledRunUploads`:
    - after a run with a destination, each succeeded Backup has an `uploaded` row;
    - its key is `ObjectKey(prefix, slug, id, logical, file)`;
    - `Put` got the file's exact size;
    - a failed dump has no upload.
  - `TestUploadFailureKeepsBackup`: `Put` errors. The Backup stays succeeded, the upload is `failed`, and `last_error` names the destination.
  - `TestRetryUpload`:
    - a failed upload succeeds on retry;
    - retry on an uploaded one → 409;
    - retry on a Remote only one → `ErrRemoteOnly`.
  - `TestStartupFailsInterruptedUploads`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'Upload'`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test ./internal/databases/... -race`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: copy scheduled database backups to a backup destination`

### Task 7: Remote Rotation and deletes with remote copies

**Files:** Modify `internal/databases/uploads.go`, `scheduler.go`, `backup.go` (`DeleteBackup`), `service.go` (`Delete`), and their tests.

**Interfaces:**
```go
func (s *Service) rotateRemote(ctx context.Context, id int64, sc Schedule) error
var ErrRemoteDelete = errors.New("could not delete the copy in the Backup destination") // wrapped with the destination name and the remote error; HTTP 502
```

- **`rotateRemote`:**
  - Candidates: live uploads to `*sc.DestinationID` of `scheduled`, not pinned Backups. `OK` is true for all.
  - For each ID: `Remote.Delete`, then set `deleted_at`. If the Backup's file is already gone, delete the row too.
  - A failed delete is logged and stays, to be tried next tick.
- **`rotateLocal`** (Task 3) now calls `SetLocalRemoved` when `LiveUpload` exists.
- **`DeleteBackup`:** if there is a live upload, it calls `Remote.Delete` first. If that fails, it returns `ErrRemoteDelete` and changes nothing. Otherwise it continues as in phase 2.
- **`Delete(…, deleteBackups=true)`:** it deletes every live remote copy first. On the first failure it returns `ErrRemoteDelete`. The copies already deleted get `deleted_at`, and the container, volume and rows are untouched.

- [ ] **Step 1: Write the failing tests:**
  - `TestRotateLocalKeepsRemoteOnlyRow` (Review Focus 2):
    - with local N=1 and remote N=3, after 3 runs: 2 rows have `LocalRemovedAt` set and their copies live, and 1 row is normal;
    - then remote N=1 and a tick: those 2 copies are deleted and their rows are gone.
  - `TestDeleteBackupRemoteFailureKeepsAll` (Review Focus 3): `Remote.Delete` errors. The answer is `ErrRemoteDelete`, the file still exists, the row is unchanged, and the upload is live.
  - `TestDeleteDatabaseRemoteFailureKeepsAll`: the container and volume Remove are never called. Rows and the schedule remain.
  - `TestRotateRemoteOnlyCurrentDestination`: copies in a former destination are never deleted by Rotation.
  - `TestRotateRemoteKeepsNewest`: with remote X=7 and a single copy 30 days old, it is kept.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run 'Rotate|Delete'`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test ./internal/databases/... -race`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: rotate and delete remote backup copies`

### Task 8: Destinations UI, schedule destination, e2e and live check

**Files:**
- Web: create `pages/BackupDestinationsPage.tsx`, `pages/backup-destinations-view.ts` and their tests. Modify `app/AppShell.tsx` (the Data group gains "Backup destinations", `CloudUploadOutlined`, below Databases), `app/paths.ts`, `app/routes.tsx`, `DatabaseScheduleCard.tsx` and `DatabaseBackupsTab.tsx`.
- e2e: create `e2e/tests/backup-destinations.spec.ts`.

**UI:**
- **The Destinations page** follows `ExternalRegistriesTab` and `SshKeysPage`:
  - **The table:** Name, Endpoint, Bucket/Prefix, and Last test (Tag OK, or Failed with the error in a Tooltip).
  - **Actions:** Test, Edit, Delete.
  - **The Add/Edit modal:**
    - fields: Name, Endpoint (help text "https://s3.amazonaws.com, https://<account>.r2.cloudflarestorage.com or http://minio:9000"), Region (optional), Bucket, Prefix, Access key ID, Secret access key (`Input.Password`; on edit, the placeholder "Leave blank to keep"), and a Path-style Switch;
    - an `http://` endpoint shows a warning Alert: "The secret and backups travel unencrypted."
  - **Delete:**
    - a 409 with `copies` opens a confirm: "N backup copies stay in the bucket. The panel stops tracking them."
    - a 409 naming schedules shows that message.
- **The schedule card:**
  - a "Copy to" Select (None + destinations);
  - "Keep in the destination", with the same two InputNumbers, shown only when a destination is chosen.
- **The Backups table:**
  - an upload status Tag: "Copied", "Copy failed" with a Retry button, or "Copying…";
  - the Delete confirm says "Also deletes its copy in <destination>." when there is one.

- [ ] **Step 1: Write the failing tests:**
  - Vitest:
    - create posts the exact body;
    - editing with a blank secret omits nothing and sends `secret:""`;
    - the http warning shows;
    - the copies confirm sends `?confirm=2`;
    - the schedule card shows the remote keep fields only with a destination;
    - Retry posts to `retry-upload`.
  - e2e `backup-destinations.spec.ts`, against MinIO started by the spec through the sandbox Docker:
    1. run `minio/minio` as `e2e-minio` on `relist-network` with root credentials;
    2. create the bucket with `mc` in a one-off `minio/mc` container;
    3. add the destination `http://e2e-minio:9000`, path-style;
    4. Test → OK;
    5. set a Postgres Database's schedule with Copy to;
    6. set `minute` to the next minute in the panel's time zone, and poll up to 150 s for a `scheduled` row with `upload.status == "uploaded"` (there is no run-now route);
    7. check the object with `mc stat`;
    8. delete the Backup; `mc stat` then fails;
    9. remove MinIO.

    Pin the MinIO and mc images to the newest release tags that are at least two weeks old at implementation time, and record them in the spec file.
- [ ] **Step 2: Run** `cd web && pnpm vitest run src/pages/BackupDestinations src/components/DatabaseScheduleCard`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the full suites:
  - `go test ./... && go vet ./...`;
  - `go test -race ./internal/databases/ ./internal/backupdest/`;
  - `pnpm typecheck && pnpm vitest run --maxWorkers=6 && pnpm build`;
  - `task e2e:reload && task e2e`.

  Expected: all PASS.
- [ ] **Step 5: Manual check** (sandbox, `docs/agents/live-checks.md`), on a fresh pg18 and a fresh mariadb Database with MinIO as above:
  1. With no destination, schedule daily at the next minute, local N=2. Let it run 3 times, moving `minute` forward each time. Expect only the 2 newest scheduled files left, and manual and pinned rows kept.
  2. With Copy to MinIO, local N=1 and remote N=3: after 3 runs, MinIO holds 3 objects under `shop-<id>/app/`. The 2 older rows show Remote only, with Download disabled (409 from the route).
  3. Stop MinIO and delete a Backup with a copy. Expect a 502 naming the destination, and the file and row still there. Start MinIO again, delete again, and expect it gone from MinIO.
  4. Stop the Database before its due minute. Expect the schedule's warning "skipped…", and no rows.
  5. Start a manual Backup of a large database (2M rows) just before the due minute. Expect the scheduled run to start right after it ends, not be lost.
  6. Restart the panel (`docker restart relist-panel`) during an upload of the large Backup. Expect the upload `failed` ("the panel stopped…"), and Retry succeeds.
  7. Edit the destination's endpoint with the secret blank. Expect a 422 on the secret.
  8. Delete the destination while a schedule uses it (409 naming the Database). Remove it from the schedule, then delete it with its copies confirmed. Expect the Remote only rows gone, and the objects still in MinIO (`mc ls`).
  9. Clean up: delete the check's Databases with their Backups, and MinIO. Do not run `task e2e:down`.
- [ ] **Step 6: Commit** `feat: manage backup destinations and copy schedules to them`
