# Stability fixes implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the failure modes the 2026-10-24 stability review verified — silent data loss, boots against the wrong schema, disk growth, queue stalls and panel lockout — one ticket per task.

**Architecture:** Each task is one ticket under `.scratch/stability-review/issues/` (plus volume-files ticket 19). Fixes stay inside the package that owns the behaviour: `internal/database` for SQLite, `internal/volfs` + the helper for Volume writes, `internal/composes` + `internal/dockerexec` for teardown, `internal/deployments` for the queue and snapshots, `internal/httpapi` for coordination. Optional runner capabilities follow the existing `DownWithVolumesTracked` pattern (type assertion on the runner) so existing fakes keep compiling.

**Tech Stack:** Go 1.26 (modernc.org/sqlite, goose v3.24.3, net/http), React 19 + antd 6.5.1 + Vitest, Docker Compose v5 CLI.

**Spec:** `.scratch/stability-review/spec.md` (Thai; findings, evidence, decisions) and the tickets in `.scratch/stability-review/issues/01`–`16` (English; acceptance checklists). Volume write atomicity is `.scratch/volume-files/issues/19-atomic-writes.md`. Reproductions: `.scratch/stability-review/verification/`.

## Global Constraints

- Work on branch `fix/stability-review` in a worktree; never commit to `main`.
- Commits: Conventional Commits, no scope (`feat: …`, `fix: …`, `chore: …`); no `Co-Authored-By` or `Claude-Session` trailers (`docs/agents/commit-conventions.md`).
- Each task ends with a `chore: close ticket NN <slug>` commit that sets the ticket's `**Status:**` line to `closed` (repo convention).
- Never edit a committed migration; the only new migration is `internal/migrations/00020_migration_files.sql`.
- No new Go modules or npm packages.
- Frontend changes: read https://ant.design/llms.txt and the linked page of each component used, check APIs against antd 6.5.1, follow `docs/GUIDELINES.md` (AGENTS.md).
- Match the surrounding code: comment density, naming, error-message style (lowercase Go errors, user-facing JSON `{"error": "..."}`).
- Go verification for every task: `go vet ./... && go test ./...`, plus `go test -race` on the packages the task touched.
- Web verification (no Node on the host): `docker run --rm -v "$PWD/web":/w -w /w -e CI=true node:24 sh -c 'corepack enable >/dev/null 2>&1; pnpm install --frozen-lockfile >/dev/null && pnpm typecheck && pnpm test'` — baseline 52 files / 568 tests pass.
- Never run Docker commands against the live install's resources; the Go tests use fakes.

## Review Focus

1. **Upgrading the live database (schema 17) must still boot** after Task 2 adds a schema check and `00020`. Pinned by `TestUpgradeFrom17Boots` in Task 2.
2. **A database copied by a server Migration (VACUUM INTO) must boot on a same-version target** with the new `migration_files` table, and `migration.ClearTables` must not empty it. Pinned by `TestVacuumCopyBoots` in Task 2.
3. **Snapshot GC must never delete a snapshot a concurrent Enqueue is still capturing** (capture creates the directory before the row exists). Task 9 runs GC under the Project gate, which capture holds until the row is saved; pinned by `TestSnapshotGCWaitsForTheProjectGate`.
4. **A Compose that was never deployed** (no compose file on disk) must delete, plan and GC without calling Docker. Pinned by `TestRemovalPlanForUndeployedCompose` (Task 5) and the undeployed case in Task 4's tests.
5. **An empty file upload** (0 bytes, so no `--expect`) must still create or empty the target. Pinned by `TestWriteEmptyFileWithoutExpect` in Task 3.

---

### Task 1: Foreign keys on every connection (ticket 01)

**Files:**
- Modify: `internal/database/database.go` (DSN line 20; new func)
- Modify: `main.go` (after `OpenAndMigrate`, log violations)
- Test: `internal/database/database_test.go`

**Interfaces:**
- Produces: `func ForeignKeyViolations(ctx context.Context, db *sql.DB) (int, error)` in `internal/database`.

- [ ] **Step 1: Write the failing tests** in `database_test.go`:
  - `TestCanceledQueryKeepsForeignKeys`: `OpenAndMigrate`; five times run `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 100000000) SELECT count(*) FROM c` under a 20 ms context (expect `context.DeadlineExceeded`); after each, `PRAGMA foreign_keys` == 1. Then insert project 1 + an environment, delete the project, assert 0 environments left.
  - `TestReplacementConnectionKeepsForeignKeys`: `SetMaxIdleConns(0)`, run `SELECT 1`, `SetMaxIdleConns(1)`, `PRAGMA foreign_keys` == 1.
  - `TestForeignKeyViolationsCountsOrphans`: open, `PRAGMA foreign_keys = OFF`, insert an environment with `project_id = 99`, `PRAGMA foreign_keys = ON`, `ForeignKeyViolations` == 1.
- [ ] **Step 2: Run** `go test ./internal/database -run 'ForeignKey|CanceledQuery|ReplacementConnection' -v` — expect the first two to FAIL with `foreign_keys = 0`, the third not to compile.
- [ ] **Step 3: Implement.** DSN becomes `"file:"+databasePath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"`; keep the `Exec` in `Migrate` (it covers callers that open the DB with `sql.Open` themselves, like tests). `ForeignKeyViolations` counts rows of `PRAGMA foreign_key_check`. In `main.go` after `OpenAndMigrate`: on `n > 0` log `warning: %d rows reference deleted parents (PRAGMA foreign_key_check); they were left by an earlier connection without foreign keys`, on error log it; never fatal.
- [ ] **Step 4: Run** the package tests and the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: enable SQLite foreign keys on every connection, so an interrupted query can no longer turn cascades off`; then close ticket 01 (`chore: close ticket 01 foreign keys on every connection`).

### Task 2: Refuse a schema this binary does not match (ticket 02)

**Files:**
- Create: `internal/migrations/00020_migration_files.sql`
- Create: `internal/database/files.go`
- Modify: `internal/database/database.go` (`Migrate`)
- Test: `internal/database/database_test.go`, `internal/database/version_test.go`; update every test that pins the latest version to 19 (grep `19` near `KnownVersion`/`version_id` across `internal/`).

**Interfaces:**
- Produces: `var ErrSchemaAhead = errors.New("database schema is newer than this binary")`, `var ErrMigrationMismatch = errors.New("applied migration differs from this binary's")` in `internal/database`. `Migrate` returns errors wrapping them.

- [ ] **Step 1: Write the failing tests:**
  - `TestOpenAndMigrateRefusesASchemaNewerThanTheBinary`: open; `INSERT INTO goose_db_version (version_id, is_applied) VALUES (known+1, 1)`; close; reopen → `errors.Is(err, ErrSchemaAhead)`; message contains both version numbers.
  - `TestOpenAndMigrateRefusesADifferentFileForAnAppliedVersion`: open; `UPDATE migration_files SET name = '00019_other.sql' WHERE version = 19`; close; reopen → `errors.Is(err, ErrMigrationMismatch)`; message names `00019_other.sql` and the embedded file name.
  - `TestOpenAndMigrateRecordsEveryAppliedFile`: open; `migration_files` has one row per embedded `.sql` file, names equal the embedded names, `sha256` equals `hex(sha256(file bytes))`.
  - `TestUpgradeFrom17Boots`: `goose.UpTo(db, ".", 17)` on a fresh DB with one project row, then `Migrate` → nil, project kept, version 20.
  - `TestVacuumCopyBoots`: open, `VACUUM INTO` a second path, `OpenAndMigrate` the copy → nil.
  - Update `TestOpenAndMigrateCreatesDatabaseAndRunsEmbeddedMigrations` and `TestKnownVersionIsHighestEmbeddedMigration` to 20.
- [ ] **Step 2: Run** `go test ./internal/database -v` — expect the new tests to FAIL.
- [ ] **Step 3: Implement.**
  - Migration `00020_migration_files.sql`: Up creates `migration_files (version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL)`; Down drops it.
  - `files.go`: `func checkSchema(db *sql.DB) error` called at the end of `Migrate` after `goose.Up`. (1) `SchemaVersion > KnownVersion` → `fmt.Errorf("%w: database schema %d is newer than this binary (%d); run the binary that applied it or restore a backup", ErrSchemaAhead, schema, known)`. (2) For every embedded migration whose version is applied in `goose_db_version`: a missing row is inserted with the embedded name and hash; an existing row with a different name or hash → `fmt.Errorf("%w: migration %d was applied from %s but this binary ships %s; run the binary that applied it or restore a backup", ErrMigrationMismatch, version, recordedName, embeddedName)`.
  - Do not touch `internal/migration.ClearTables` (it must not list `migration_files`); verify by reading it.
- [ ] **Step 4: Run** the Go verification (includes `internal/migration` tests that compare versions) — expect PASS.
- [ ] **Step 5: Commit** `feat: refuse to boot against a schema newer than the binary or an applied migration from another file`; close ticket 02.

### Task 3: Atomic Volume writes (volume-files ticket 19)

**Files:**
- Modify: `internal/volfs/volfs.go:210` (`Write`)
- Modify: `cmd/relist-volhelper/main.go:266-290` (`runWrite`)
- Modify: `.scratch/volume-files/spec.md` (delete the Protocol and Caps sentences describing the failure window)
- Test: `internal/volfs/volfs_test.go`, `cmd/relist-volhelper/main_test.go`

**Interfaces:**
- Produces: `func (fs *FS) Write(name string, src io.Reader, expect int64) error` (`expect < 0` = size unknown); `var ErrIncompleteWrite = errors.New("stream ended before the declared size")` in `internal/volfs`.

- [ ] **Step 1: Write the failing tests** (`volfs_test.go`):
  - `TestWriteFailingMidStreamKeepsOriginal`: 14 000-byte original; `Write("config.db", io.MultiReader(100 bytes, reader returning "connection reset"), -1)` → error; original byte-identical; no `.relist-write-*` left in the directory.
  - `TestWriteShortStreamWithExpectKeepsOriginal`: `Write(name, io.LimitReader(src, 100), 40000)` → `errors.Is(err, ErrIncompleteWrite)`; original byte-identical.
  - `TestWriteReplacesAndKeepsMode`: original mode 0o600; successful write → new content, mode 0o600.
  - `TestWriteEmptyFileWithoutExpect`: `Write(name, strings.NewReader(""), -1)` on an existing file → file exists, size 0; on a new name → created.
  - `TestWriteThroughSymlinkFollowsLink`: target is a symlink to `real.txt` inside the root → `real.txt` gets the content, the link stays a link.
  - Helper (`main_test.go`): `TestWriteShortStdinKeepsOriginal` (`--expect 40000`, 100 bytes on stdin → non-zero exit, not 0 or 3; original intact) and `TestWriteOverflowKeepsOriginal` (space guard overflow → exit 3; original intact, never deleted).
- [ ] **Step 2: Run** `go test ./internal/volfs ./cmd/relist-volhelper -v` — expect FAIL (signature, and originals destroyed).
- [ ] **Step 3: Implement.** Follow ticket 19: regular (or missing) target → `OpenFile` a `.relist-write-<random hex>` temp in the target's directory with `O_CREATE|O_EXCL|O_WRONLY, 0o666`; copy; `expect >= 0 && written != expect` → remove temp, return `fmt.Errorf("write %q: %w (%d of %d bytes)", name, ErrIncompleteWrite, written, expect)`; any error removes the temp; an existing target's mode is applied with `root.Chmod` before `root.Rename(temp, name)`. Symlink target (`root.Lstat`) keeps today's truncate-and-stream path. `runWrite` passes `cmd.expect` when `cmd.declared`, else `-1`, and drops `vol.Delete(cmd.name)`; the space-guard overflow still exits 3.
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: write Volume files to a temp file and rename it, so a failed or cut-off upload leaves the original untouched`; close volume-files ticket 19 (`chore: close ticket 19 atomic writes`).

### Task 4: Keep volumes another Compose owns or declares (ticket 03)

**Files:**
- Create: `internal/composes/volumeplan.go`
- Modify: `internal/composes/composes.go` (`delete`), `internal/composes/teardown.go` (`teardown`), `internal/composes/deploy.go` (`RunDeploy` fresh-volumes branch)
- Create: `internal/dockerexec/volume_plan.go`; Test: `internal/dockerexec/volume_plan_test.go`
- Test: `internal/httpapi/volume_plan_test.go`

**Interfaces:**
- Produces (package `composes`):
  ```go
  type VolumeCandidate struct{ Name string; Exists bool; Project string } // Project = com.docker.compose.project label, "" when absent
  type VolumeInspector interface { // optional runner capability
      CandidateVolumes(ctx context.Context, req RunRequest) ([]VolumeCandidate, error)
      DownAndRemoveVolumes(ctx context.Context, req RunRequest, names []string) (removed []string, err error)
  }
  type KeptVolume struct{ Name string `json:"name"`; Reason string `json:"reason"` }
  type VolumePlan struct{ Remove []string `json:"remove"`; Keep []KeptVolume `json:"keep"` }
  func (s *Service) volumePlan(ctx context.Context, c Compose, req RunRequest) (plan VolumePlan, supported bool, err error)
  func (s *Service) destroyStack(ctx context.Context, c Compose, req RunRequest) (plan VolumePlan, removedAny bool, err error)
  func declaredVolumeNames(yamlContent string) []string            // literal `name:` of non-external top-level volumes; values containing "$" skipped
  func (s *Service) volumeNamesElsewhere(ctx context.Context, composeID int64) (map[string]string, error) // volume name -> other Compose's name
  ```
- `dockerexec.Runner` implements `composes.VolumeInspector`.

- [ ] **Step 1: Write the failing tests.**
  - `dockerexec/volume_plan_test.go` (using the existing `command` stand-in): `CandidateVolumes` runs `compose … config --format json`, skips external, defaults the name to `<project>_<logical>`, and maps `docker volume inspect --format {{index .Labels "com.docker.compose.project"}} <name>` to `Project`, "no such volume" to `Exists: false`. `DownAndRemoveVolumes` runs, in order, `compose … rm --stop --force --volumes`, `compose … down`, then `volume rm <name>` per name (never `-f`), returning the names whose `rm` succeeded.
  - `httpapi/volume_plan_test.go` with a runner embedding `*fakeComposeRunner` plus scripted `VolumeInspector`:
    - `TestDeleteKeepsVolumeOwnedByAnotherProject`: candidate `shared` labelled `relist-c99` → `DownAndRemoveVolumes` called without `shared`; `DownWithVolumes` not called; 204; row gone.
    - `TestDeleteKeepsVolumeDeclaredBySiblingCompose`: another Compose's YAML has `volumes: {db: {name: shared-db}}`; candidate `shared-db` owned by this project → kept.
    - `TestDeleteRemovesOwnedVolumesWithDownV`: no keeps → `DownWithVolumes` (today's path), `DownAndRemoveVolumes` not called.
    - `TestDeleteOfUndeployedComposeCallsNoDocker`: no compose file → no runner call.
    - `TestFreshDeployLogsKeptVolumes`: fresh-volumes Deployment with a kept volume → Deployment log contains `kept volume shared: owned by another Compose project (relist-c99)`.
    - `TestDeclaredVolumeNamesSkipsInterpolated` (package `composes`): `name: ${X}` skipped; external skipped; literal returned.
- [ ] **Step 2: Run** `go test ./internal/dockerexec ./internal/httpapi ./internal/composes -run 'Candidate|DownAndRemove|Keeps|RemovesOwned|Undeployed|KeptVolumes|DeclaredVolumeNames' -v` — expect FAIL.
- [ ] **Step 3: Implement.** Keep rule: candidates with `Exists == false` are dropped; keep when `Project != "" && Project != req.ProjectName` (reason `owned by another Compose project (<label>)`) or the name is in `volumeNamesElsewhere` (reason `declared by Compose <name>`). `destroyStack`: runner without `VolumeInspector`, or a plan with no keeps → today's behaviour (`DownWithVolumesTracked` when available, else `DownWithVolumes`); otherwise `DownAndRemoveVolumes(plan.Remove)` and `log.Printf` each kept volume. `delete`, `teardown` and the `RunDeploy` fresh branch call `destroyStack`; the fresh branch writes `kept volume <name>: <reason>` lines to `opts.Out` and sets `removed` from `removedAny`. Delete keeps answering 204 (Task 5 shows the plan before deletion).
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: keep named volumes that another Compose owns or declares when deleting, tearing down or deploying with fresh volumes`; close ticket 03.

### Task 5: Delete confirmation shows the removal plan (ticket 06)

**Files:**
- Create: `internal/composes/removalplan.go`; Modify: `internal/composes/composes.go` (`RegisterRoutes`), `internal/projects/projects.go` and `internal/environments/environments.go` (routes)
- Test: `internal/httpapi/removal_plan_test.go`
- Create: `web/src/components/RemovalPlanList.tsx` (+ `.test.tsx`); Modify: `web/src/data/api.ts`, `web/src/data/types.ts`, the delete `DangerModal`s in `web/src/pages/ComposePage.tsx`, `ProjectSettingsTab.tsx`, `ProjectsPage.tsx`

**Interfaces:**
- Consumes: Task 4's `volumePlan`, `VolumePlan`, `KeptVolume`.
- Produces:
  ```go
  type RemovalPlanEntry struct {
      ComposeID int64 `json:"composeId"`; Name string `json:"name"`; Directory string `json:"directory"`
      RemoveVolumes []string `json:"removeVolumes"`; KeepVolumes []KeptVolume `json:"keepVolumes"`
      Error string `json:"error,omitempty"`
  }
  func (s *Service) RemovalPlan(ctx context.Context, projectID, environmentID, composeID int64) ([]RemovalPlanEntry, error) // 0 = every child
  ```
  Routes (all answer `{"composes": [RemovalPlanEntry...]}`; never empty arrays as null):
  `GET /api/projects/{projectID}/removal-plan`, `GET /api/projects/{projectID}/environments/{environmentID}/removal-plan`, `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/removal-plan`.
  Web: `api.removalPlan(path: string): Promise<{ composes: RemovalPlanEntry[] }>`; `<RemovalPlanList entries={...} loading error />`.

- [ ] **Step 1: Write the failing tests.**
  - `TestRemovalPlanListsVolumesAndDirectory`: deployed Compose, inspector returns an owned and a foreign volume → entry has `removeVolumes: [owned]`, `keepVolumes: [{name, reason}]`, `directory` = the Compose directory.
  - `TestRemovalPlanForUndeployedCompose`: no compose file → empty arrays, no runner calls.
  - `TestRemovalPlanInspectorErrorIsPerCompose`: inspector error → 200 with that entry's `error` set.
  - `TestProjectRemovalPlanCoversEveryCompose`: two Environments, three Composes → three entries.
  - `RemovalPlanList.test.tsx`: renders "Volumes removed", the kept volume with its reason, the directory, and an error line.
- [ ] **Step 2: Run** the Go tests and the web verification — expect FAIL.
- [ ] **Step 3: Implement.** A runner without `VolumeInspector` yields entries with empty volume arrays and `error: "volume details unavailable"`. The modals fetch the plan when they open and pass `<RemovalPlanList>` as `consequences`; a failed fetch shows the error and keeps the typed confirmation usable. Copy: headings `Volumes removed`, `Volumes kept (used elsewhere)`, `Directory removed`.
- [ ] **Step 4: Run** the Go and web verification — expect PASS.
- [ ] **Step 5: Commit** `feat: show which volumes and directories a delete removes and which shared volumes it keeps`; close ticket 06.

### Task 6: Warnings for YAML that loses data (ticket 04)

**Files:**
- Create: `internal/composes/volume_warnings.go`, Test: `internal/composes/volume_warnings_test.go`
- Modify: `internal/composes/deploy.go` (`resolveForDeploy` appends the warnings)

**Interfaces:**
- Consumes: Task 4's `volumeNamesElsewhere`.
- Produces: `func volumeWarnings(yamlContent, composeDir string, hasSource bool, elsewhere map[string]string) []string`; `func (r *repository) hasSource(ctx context.Context, composeID int64) (bool, error)`.

- [ ] **Step 1: Write the failing table test** `TestVolumeWarnings` (exact strings):
  - `./src:/app` in service `web`, hasSource → `Service "web" mounts ./src writable at /app: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`
  - `./src:/app:ro`, long syntax `{type: bind, source: ./src/data, target: /data, read_only: true}`, `./data:/data`, and hasSource=false → no src warning; long syntax with `read_only: false` and the absolute `<composeDir>/src` path → warn.
  - `volumes: {data: {name: shared-db}}` → `Volume "data" sets name: shared-db without external: true, so deleting this Compose removes it. Declare it external: true if other stacks use it.`; with `elsewhere{"shared-db": "api"}` the same text plus ` Compose "api" also declares it.`; `external: true` → none.
  - `TestPreviewIncludesVolumeWarnings` (httpapi): preview of a Compose with a zip Source and `./src:/app` returns the warning.
- [ ] **Step 2: Run** `go test ./internal/composes -run VolumeWarnings -v` — expect FAIL.
- [ ] **Step 3: Implement** `volumeWarnings` (parse with `yaml.v3`, Services in sorted order for stable output) and append its result in `resolveForDeploy`, so Preview, Capture (snapshot warnings) and the Deployment log all carry it.
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `feat: warn when a Compose writes into ./src or names a volume it does not own`; close ticket 04.

### Task 7: Guidance under the Compose editor (ticket 05)

**Files:**
- Create: `web/src/components/ComposeEditorGuide.tsx` (+ `.test.tsx`)
- Modify: `web/src/pages/ComposePage.tsx` (below the `<YamlEditor>` near line 776)

**Interfaces:** Produces `export function ComposeEditorGuide(): JSX.Element` (no props).

- [ ] **Step 1: Write the failing test** `ComposeEditorGuide.test.tsx`: renders exactly these three lines:
  - `Containers should not write into ./src — use a named volume.`
  - `To share a volume between Composes, declare it external: true.`
  - `Relative bind mounts such as ./data live in this Compose's directory and are deleted with the Compose.`
- [ ] **Step 2: Run** the web verification — expect FAIL.
- [ ] **Step 3: Implement** with antd `Typography` (secondary, small) — read llms.txt + the Typography page first; no Alert/box; must not cause horizontal scroll at 320 px.
- [ ] **Step 4: Run** the web verification — expect PASS.
- [ ] **Step 5: Commit** `feat: note under the Compose editor what to avoid so volumes and files are not lost`; close ticket 05.

### Task 8: Refuse merging a Deploy into a queued one with other options (ticket 07)

**Files:**
- Modify: `internal/deployments/queue.go` (`Enqueue`), `internal/deployments/http.go` (create)
- Modify: `web/src/data/api.ts` (`enqueueDeployment` doc comment)
- Test: `internal/deployments/queue_test.go`, `internal/httpapi/deployments_test.go`

**Interfaces:** Produces `type QueuedConflictError struct{ Queued Deployment }` with `Error() string` = `deployment #<n> is already queued with mode=<m>, noCache=<b>, freshVolumes=<b>; cancel it first`.

- [ ] **Step 1: Write the failing tests:**
  - `TestEnqueueRefusesDifferentOptions` (queue harness): fresh then plain → `errors.As(err, *QueuedConflictError)` with `Queued.FreshVolumes == true`; plain then fresh → conflict; noCache differs → conflict; same request twice → `created == false`, no error.
  - `TestDeploymentPostWithDifferentOptionsIs409` (httpapi, worker held with `holdRunner` so the second stays queued): body `{"error": "deployment #N is already queued with …", "number": N}`. `TestSecondPostWhileQueuedReturnsTheSameNumber` must keep passing.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement** `sameRequest(d Deployment, r Request) bool` (Mode, NoCache, FreshVolumes) and apply it on every path in `Enqueue` that returns an existing queued Deployment (early `queuedFor`, `alreadyQueued` from capture, `store.enqueue` with `created == false`); on mismatch discard the captured snapshot as today and return the error. `http.go` maps it to 409 with `number`. Fix the `api.ts` comment ("200 when an identical request is already queued; 409 when one with other options is").
- [ ] **Step 4: Run** the Go and web verification — expect PASS.
- [ ] **Step 5: Commit** `fix: refuse a Deploy whose options differ from the one already queued instead of silently reusing it`; close ticket 07.

### Task 9: Garbage-collect Deployment source snapshots (ticket 08)

**Files:**
- Create: `internal/deployments/snapshot_gc.go`, Test: `internal/deployments/snapshot_gc_test.go`
- Modify: `internal/deployments/queue.go` (`Deps`, `Run`, `execute`)
- Create: `internal/dockerexec/mounts.go`, Test: `internal/dockerexec/mounts_test.go`
- Modify: `internal/httpapi/httpapi.go` (wire `Mounts`), `docs/adr/0017-deployments-freeze-inputs-when-queued.md` (retention paragraph)

**Interfaces:**
- Produces: `type MountLister interface{ MountedSources(ctx context.Context, projectName string) ([]string, error) }`; `Deps.Mounts MountLister` (nil disables GC); `func (q *Queue) collectSnapshots(ctx context.Context, composeID int64)`; `func (r Runner) MountedSources(ctx context.Context, projectName string) ([]string, error)` in dockerexec (`docker ps -aq --filter label=com.docker.compose.project=<p>`, then `docker inspect --format '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' <ids>`; no containers → empty, no inspect).

- [ ] **Step 1: Write the failing tests** (real `composes.Service`, `snapshotRunner`, zip Source with `./src:/app`, fake `MountLister`):
  - `TestSnapshotGCKeepsOnlyReferencedDirs`: fake mounts return the latest mounted path; six skip-build deploys → exactly one directory under `.deployment-sources` after each.
  - `TestSnapshotGCKeepsQueuedSnapshot`: a queued (unprocessed) Deployment's snapshot survives a GC run triggered by another Deployment settling.
  - `TestSnapshotGCKeepsMaterializedTree`: Git build records a materialization → its directory survives.
  - `TestSnapshotGCSkipsOnMountError`: `MountedSources` error → nothing deleted.
  - `TestSnapshotGCWaitsForTheProjectGate`: while a test holds the Project gate (fake gate blocking until released), `collectSnapshots` deletes nothing; after release it runs.
  - `TestRunCollectsSnapshotsAtStart`: leftover directories from a previous run are removed when `Run` starts.
  - dockerexec `TestMountedSourcesArgv`.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement.** `collectSnapshots` takes the Project gate (`Gate.Acquire`, 30 s timeout; on error skip), then keeps: `SourceDir` of snapshots whose Deployment is queued or running for that Compose, the parent of the materialization's tree directory, and any snapshot directory that is (or contains the parent of) a mounted source; it removes the rest under `<composeDir>/.deployment-sources`. Call it in `execute` after `Prune` (with `j.bg`) and in `Run` after `FailRunning` for every Compose (`SELECT id FROM composes`). `httpapi.NewHandler` sets `Mounts` when `d.Runner` implements `deployments.MountLister`. Amend ADR-0017's retention paragraph to describe the GC.
- [ ] **Step 4: Run** the Go verification with `-race` on `internal/deployments` — expect PASS.
- [ ] **Step 5: Commit** `fix: remove Deployment source snapshots nothing references any more, so disk use stops growing with every deploy`; close ticket 08.

### Task 10: Volume reads no longer block Deployments (ticket 09)

**Files:**
- Modify: `internal/httpapi/coordination.go`
- Modify: `internal/deployments/queue.go` (`Gate` interface, `deploy`), `internal/deployments/queue_test.go` (`fakeGate`)
- Test: `internal/httpapi/coordination_test.go` (internal package), `internal/httpapi/volume_gate_test.go`

**Interfaces:**
- Produces: `deployments.Gate` gains `AcquireVolumes(ctx context.Context, projectID int64) (func(), error)` (exclusive volume hold); `coordinator.AcquireVolumes` (exclusive) and `coordinator.acquireVolumesShared` (shared), both taking the host maintenance guard like `Acquire`.

- [ ] **Step 1: Write the failing tests:**
  - Unit (`coordination_test.go`): two shared holds coexist; an exclusive hold waits for shared holds; a waiting exclusive hold blocks new shared holds (no starvation); canceling a waiting acquire returns `ctx.Err()` and leaves no entry in the map.
  - HTTP (`volume_gate_test.go`, built from the C2 reproduction in `.scratch/stability-review/verification/httpapi_review_verify_test.go`): `TestArchiveDownloadDoesNotBlockDeployments` (A1 and B both `succeeded` while the archive is open); `TestComposeDeleteWaitsForArchive` (DELETE does not return until the archive is released); `TestFreshDeployWaitsForArchive` (fresh-volumes Deployment of A stays `running/deploy` until the archive is released).
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement.** Per-project volume lock in the coordinator (readers, writer, waiting writers, `changed` channel, same style as `dockerhost.Guard`). Middleware classes: GET/HEAD under `…/volumes` → shared volume hold only (no mutation gate); DELETE of a Compose, Environment or Project, and PUT/DELETE under `…/volumes` (incl. orphans) → mutation gate, then exclusive volume hold; every other coordinated request → mutation gate only. `queue.deploy()` takes `AcquireVolumes` after the mutation gate when `FreshVolumes` and releases both. Update `coordinatedProject`'s comment.
- [ ] **Step 4: Run** the Go verification with `-race` on `internal/httpapi` and `internal/deployments` — expect PASS.
- [ ] **Step 5: Commit** `fix: let Volume reads share the Project lock so an archive download no longer stalls the Deployment queue`; close ticket 09.

### Task 11: A Domain cannot take over the panel's host (ticket 10)

**Files:**
- Modify: `docker-compose.yml` (labels), `README.md` (recreate note)
- Modify: `internal/domains/store.go` (`Service`, `readInput`), `internal/httpapi/httpapi.go` (`Deps.PanelDomain`), `main.go` (pass `cfg.Install.Domain`)
- Test: `internal/httpapi/domains_test.go`

**Interfaces:** Produces `httpapi.Deps.PanelDomain string`; `func (s *Service) WithPanelHost(host string) *Service` in `internal/domains` (stores `strings.ToLower(strings.TrimSpace(host))`).

- [ ] **Step 1: Write the failing test** `TestDomainOnPanelHostIsRefused`: handler built with `PanelDomain: "panel.example.com"`; create with host `Panel.Example.com` → 400 with the host field error `host is the panel's own domain; pick another host`; update an existing Domain to that host → same; another host → 201; `PanelDomain` empty → the panel host is accepted.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement** the check in `readInput` after `Validate`, using the package's existing field-error response. Add `traefik.http.routers.panel.priority=100000` and `traefik.http.routers.panel-http.priority=100000`; README: existing installs run `docker compose up -d` to recreate the panel container.
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: refuse a Domain on the panel's own host and give the panel's routers top priority`; close ticket 10.

### Task 12: Loopback-only panel port and header timeouts (ticket 11)

**Files:**
- Modify: `docker-compose.yml` (`ports`), `README.md` (SSH tunnel)
- Create: `server.go` (package `main`); Modify: `main.go:169`
- Test: `server_test.go`

**Interfaces:** Produces `func newServer(addr string, handler http.Handler) *http.Server` (`ReadHeaderTimeout: 10 * time.Second`, `IdleTimeout: 2 * time.Minute`, no `ReadTimeout`/`WriteTimeout`).

- [ ] **Step 1: Write the failing test** `TestNewServerHasHeaderAndIdleTimeouts`: the three timeouts as above, `ReadTimeout == 0`, `WriteTimeout == 0`.
- [ ] **Step 2: Run** `go test . -run NewServer -v` — expect FAIL.
- [ ] **Step 3: Implement**; ports become `"127.0.0.1:${RELIST_PORT:-8888}:${RELIST_PORT:-8080}"`; README: reach it with `ssh -L 8888:127.0.0.1:8888 <host>` and recreate the container on existing installs.
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: publish the panel's port on loopback only and time out half-sent requests`; close ticket 11.

### Task 13: Bound Volume listing memory (ticket 12)

**Files:**
- Modify: `internal/volfs/volfs.go` (add `ListFirst`), `cmd/relist-volhelper/main.go` (`list` op), `internal/dockerexec/volume_runner.go` (`volumeArgv`), `docker-compose.yml` (`GOMEMLIMIT`)
- Modify: `web/src/data/api.ts`, `web/src/data/types.ts`, `web/src/data/store.tsx`, `web/src/pages/VolumeExplorerPage.tsx`
- Test: `internal/volfs/volfs_test.go`, `cmd/relist-volhelper/main_test.go`, `internal/dockerexec/volume_runner_test.go`, `internal/httpapi/volumeops_test.go`, `web/src/pages/VolumeExplorerPage.test.tsx`

**Interfaces:** Produces `func (fs *FS) ListFirst(name string, limit int) (entries []Entry, truncated bool, err error)`; helper `list` output `{"entries": [...], "truncated": bool}` with `const listLimit = 10000`; web `type VolumeListing = { entries: VolumeEntry[]; truncated: boolean }` returned by `listVolumeEntries`.

- [ ] **Step 1: Write the failing tests:** `TestListFirstTruncatesSortedByName` (12 files, limit 10 → first 10 by name, `truncated` true; limit 20 → 12, false); helper `TestListPrintsEntriesAndTruncated`; `TestVolumeArgvLimitsMemory` (`--memory`, `256m` before the image); update httpapi list fixtures to the object shape; web test: a truncated listing shows `Showing the first 10,000 entries.`
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement.** `ListFirst` uses `Readdirnames(-1)`, sorts, and `Lstat`s only the first `limit` (keep `List` for `pack`). Explorer reads `entries` and shows the notice (antd `Alert` type info; read its page first). Add `GOMEMLIMIT: 200MiB` to the panel service.
- [ ] **Step 4: Run** the Go and web verification — expect PASS.
- [ ] **Step 5: Commit** `fix: cap Volume listings at 10,000 entries and limit panel and helper memory`; close ticket 12.

### Task 14: Recover panics in background goroutines (ticket 13)

**Files:**
- Create: `internal/safely/safely.go`, Test: `internal/safely/safely_test.go`
- Modify: `internal/hostmetrics/hostmetrics.go` (tick, sweep and df goroutines, minute close), `internal/httpapi/httpapi.go` (`ResumeLock` goroutine), `internal/migration/target.go:352, 876`
- Test: `internal/hostmetrics/hostmetrics_test.go`

**Interfaces:** Produces `func Do(name string, fn func())` (recover, `log.Printf("%s panicked: %v\n%s", name, r, debug.Stack())`) and `func Call(name string, fn func() error) (err error)` (a panic becomes `fmt.Errorf("%s panicked: %v", name, r)`).

- [ ] **Step 1: Write the failing tests:** `TestDoRecovers`, `TestCallReturnsPanicAsError`; `TestSweepPanicIsRecoveredAndNextTickSamples` (hook panics, `waitIdle`, the next `tick` pushes a sample, `sweepInFlight` is false again).
- [ ] **Step 2: Run** — expect FAIL (the hostmetrics test kills the binary).
- [ ] **Step 3: Implement.** Sampler hooks run inside `safely.Do`; `tick`'s own sampling runs inside `safely.Do` so one bad tick is skipped; the migration target goroutines wrap their work in `safely.Call` so a panic flows into the existing error path (state reset, `t.st.Error` set).
- [ ] **Step 4: Run** the Go verification with `-race` on touched packages — expect PASS.
- [ ] **Step 5: Commit** `fix: recover panics in the sampler and migration goroutines instead of taking the panel down`; close ticket 13.

### Task 15: Lifecycle actions survive a client disconnect (ticket 16)

**Files:**
- Modify: `internal/composes/composes.go` (stop, restart, delete), `internal/projects/projects.go`, `internal/environments/environments.go` (delete)
- Test: `internal/httpapi/lifecycle_context_test.go`

**Interfaces:** Produces `func LifecycleContext(parent context.Context) (context.Context, context.CancelFunc)` in `internal/composes` = `context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)`.

- [ ] **Step 1: Write the failing tests:** `TestDeleteFinishesAfterClientDisconnects` — request built with a cancelable context; the runner's `DownWithVolumes` signals start, the test cancels the request context, the runner then asserts its own `ctx.Err() == nil` and returns; the Compose row is gone. Same shape for Stop (`Down`) and Project delete.
- [ ] **Step 2: Run** — expect FAIL.
- [ ] **Step 3: Implement**: the Docker calls and the following DB writes in those handlers use the lifecycle context; reads before them keep `r.Context()`.
- [ ] **Step 4: Run** the Go verification — expect PASS.
- [ ] **Step 5: Commit** `fix: finish Stop, Restart and Delete even when the browser goes away`; close ticket 16.

---

## After the last task

- Full verification: `go vet ./... && go test -race ./...` and the web verification.
- Mark `.scratch/stability-review/spec.md` `Status: closed` with the remaining needs-triage tickets (14, 15) listed.
- Whole-branch review, then hand back for the operator to decide on merge (superpowers:finishing-a-development-branch). The operator recreates the panel container on the live install themselves (compose labels, port, `GOMEMLIMIT`).
