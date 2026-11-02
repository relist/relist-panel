# One Updater and the Install script — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework Panel update so one `relist-panel update` command does the whole run for both
the web and a shell script, and add a `curl | sh` Install script.

**Architecture:** The Preflight, the quiesce request, the backup and the apply move out of the
panel's `Service` into a standalone `Updater` that runs in a Container of the current image. The
panel answers a quiesce request through `panel.json` in the run directory and starts the same
Container for Preview and Update. `install.sh`, filled in by the release workflow, calls the new
`setup` and `update` commands.

**Tech Stack:** Go (standard library, `modernc.org/sqlite` already in use), Docker Engine API
and `docker compose` CLI through `internal/dockerexec`, React + antd 6.5.1, POSIX `sh`,
GitHub Actions.

**Spec:** `.scratch/self-update/spec.md` (binding). ADRs: `docs/adr/0024-panel-self-update.md`,
`docs/adr/0025-one-updater-and-install-script.md`. The previous plan (tickets 01–07) is in git
history at `432091e`; everything it built stays unless a task below removes it.

## Global Constraints

- **Never touch the live install.** This checkout is the owner's live panel: Compose project
  `relist-panel` (Containers `relist-panel-panel-1`, `relist-panel-traefik-1`), data dir
  `/var/lib/relist-panel`, network `relist-network`, ports 80/443. Never run
  `docker compose up/down/restart/pull/build` in the repo root; never stop, recreate or exec into
  those Containers; never write under `/var/lib/relist-panel`. Real-Docker checks run only inside
  docker:dind (`scripts/probe-panel-update.sh`).
- **Never run the `relist-panel` binary without a temp `RELIST_DATA_DIR`.** With no arguments it
  starts the server.
- **Never edit an existing schema migration** (`internal/migrations/00001`–`00027`). No schema
  change is expected; if one is needed it is `00028_*.sql`.
- Commits: Conventional Commits, no scope, imperative, lowercase after the colon. **No
  `Co-Authored-By` or `Claude-Session` trailers.** Commit on `feat/panel-self-update`. Never push.
- Done means: `go vet ./...` and `go test ./...` green; for web changes also
  `pnpm --dir web typecheck` and `pnpm --dir web test`; for `release/install.sh`,
  `shellcheck -s sh release/install.sh` clean.
- Vocabulary from `CONTEXT.md`: Install, Install file, Install override, Install script, Panel
  update, Updater, Update manifest, Install invariant.
- Frontend: read https://ant.design/llms.txt and the linked pages for the components you touch;
  check props against antd 6.5.1. Follow `docs/GUIDELINES.md` §4.15.
- Go standard library only for crypto. No new Go module dependencies.
- Fixed values from the spec: quiesce heartbeat every 2 s, "panel answers" window 20 s, health
  180 s polling every 2 s, compose `up` timeout 15 minutes, install health wait 60 s on
  `http://127.0.0.1:8888/healthz`, default `--dir` `/opt/relist-panel`, Container names
  `relist-panel` and `relist-traefik`, Updater name `relist-panel-updater-<run id>`, `update`
  exit codes 0 ready / 1 not ready / 2 usage / 3 up to date.

## Review Focus

1. **A `panel.json` from an earlier run** must not count as the panel answering: the Updater only
   reads an ack whose `run` equals its own run id (test in Task 5).
2. **Clock skew between Containers:** the Updater decides "answering" by a heartbeat that changes,
   never by comparing the panel's timestamp with its own clock (test in Task 5).
3. **The Updater dies after the backup, before `up`:** the old panel is still running and locked;
   its watcher must mark the run `interrupted` and release the lock (test in Task 8).
4. **`install.sh` under `dash` with no terminal** (cron, CI, `ssh host 'curl … | sh'`): it must
   stop with a message telling the operator to pass `--auto`, never hang on `/dev/tty` (test in
   Task 11).
5. **A domain typed as a URL** (`https://panel.example.com/`, a space, an empty answer): refused
   before anything is checked or written, never put into `.env` (test in Task 11).

---

### Task 1: Install file — Container names, published image, build block in the override

**Files:**
- Modify: `docker-compose.yml`, `docker-compose.override.example.yml`, `.gitignore`
- Test: `install_compose_test.go`

**Interfaces:**
- Produces: the panel Service is `container_name: relist-panel`,
  `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}`, no `build:`, no
  `pull_policy`; Traefik is `container_name: relist-traefik`. The embedded copy (`main.go`
  `installFile`) follows automatically.

- [ ] **Step 1: Write the failing tests** in `install_compose_test.go`:
  - `TestInstallFileUsesPublishedImage`: panel `image` equals
    `${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}`, `build` absent, `pull_policy`
    absent.
  - `TestInstallFileNamesContainers`: panel `container_name` is `relist-panel`, traefik
    `container_name` is `relist-traefik`.
  - `TestOverrideExampleBuildsFromSource`: `docker-compose.override.example.yml` parses as YAML
    and its panel Service has `build: .`, `image: relist-panel:local`, `pull_policy: build`.
  - `TestGitignoreKeepsInstallLocalFiles`: `.gitignore` has lines `docker-compose.override.yml`
    and `.env`.
- [ ] **Step 2: Run** `go test -run 'TestInstallFile|TestOverrideExample|TestGitignore' .` —
  expected FAIL.
- [ ] **Step 3: Edit the files.** Keep the Install file's comment header. In the override example
  put the build block first, under a comment saying a checkout that builds from source copies it
  to `docker-compose.override.yml`; keep the existing label/volume examples after it, commented as
  examples.
- [ ] **Step 4: Run** `go test .` — expected PASS (the existing Install file tests too).
- [ ] **Step 5: Commit** `feat: name the install containers and run the published panel image`

### Task 2: Run files — `aborted`, `waitingOn`, `panel.json`

**Files:**
- Modify: `internal/panelupdate/run.go`
- Create: `internal/panelupdate/ack.go`, `internal/panelupdate/ack_test.go`
- Test: `internal/panelupdate/run_test.go` (create if absent)

**Interfaces:**
- Produces:
  - states: remove `StateStartingUpdater`; add `StateAborted = "aborted"`.
    `IsTerminal` is true for `succeeded`, `rolled-back`, `rollback-failed`, `aborted`,
    `interrupted`.
  - `RunStatus` gains `WaitingOn string \`json:"waitingOn,omitempty"\`` and
    `Note string \`json:"note,omitempty"\`` (for `panel did not answer; a running Deployment may
    have been cut`).
  - `const PanelNoAnswerNote = "panel did not answer; a running Deployment may have been cut"`.
  - `type PanelAck struct { Run string \`json:"run"\`; State string \`json:"state"\`; WaitingOn string \`json:"waitingOn,omitempty"\`; Reason string \`json:"reason,omitempty"\`; Heartbeat int64 \`json:"heartbeat"\` }`
    with `const AckWaiting = "waiting"`, `AckReady = "ready"`, `AckRefused = "refused"`.
  - `func WriteAck(dir string, a PanelAck) error` (temp file + rename, `0600`, like
    `WriteStatus`), `func ReadAck(dir string) (PanelAck, error)` (`fs.ErrNotExist` when absent).
  - `Heartbeat` is a counter the panel increments on every write, not a time.

- [ ] **Step 1: Write the failing tests:** `TestIsTerminalAborted` (`aborted` terminal,
  `quiescing` not); `TestAckRoundTrip` (write then read equals); `TestReadAckMissing`
  (`errors.Is(err, fs.ErrNotExist)`); `TestWriteAckIsAtomic` (no temp file left in the dir).
- [ ] **Step 2: Run** `go test ./internal/panelupdate -run 'Aborted|Ack'` — expected FAIL.
- [ ] **Step 3: Implement** in `run.go` and `ack.go`. Rename `StateStartingUpdater` to the
  unexported `stateStartingUpdater` so its remaining users still compile; Task 8 deletes its last
  use. It is not terminal.
- [ ] **Step 4: Run** `go test ./internal/panelupdate` — expected PASS.
- [ ] **Step 5: Commit** `feat: add the aborted state and the panel's quiesce ack file`

### Task 3: The Install on disk — direct reads, discovery by directory, local compose

**Files:**
- Modify: `internal/panelupdate/install.go`
- Create: `internal/panelupdate/install_local.go`, `internal/panelupdate/install_local_test.go`
- Modify: `internal/dockerexec/panel_updater.go` (add container listing by working dir)

**Interfaces:**
- Consumes: `Install{Project, Dir, ConfigFiles, Image}` (exists), `installFromLabels` (exists).
- Produces:
  - `func ReadLocalFiles(in Install) (InstallFiles, error)` — reads each config file and `.env`
    from the local filesystem (the command's own mount). `os.Lstat` first: a symlink or a
    non-regular file returns `*Unavailable` with the existing wording; a missing `.env` is empty;
    a missing config file is an error.
  - `type ProjectFinder interface { ContainersInDir(ctx context.Context, dir string) ([]Self, error) }`
    — every Container whose `com.docker.compose.project.working_dir` label equals `dir`.
  - `func ResolveInstall(ctx context.Context, a InstallArgs, f ProjectFinder) (Install, error)` with
    `type InstallArgs struct { Project, Dir string; Files []string }`:
    - `Project`, `Dir`, `Files` all set → use them (validated like `installFromLabels`);
    - only `Dir` → labels of the first Container from `ContainersInDir`;
    - none found → project from `.env` `COMPOSE_PROJECT_NAME`, else `filepath.Base(dir)`; files
      `<dir>/docker-compose.yml` plus `<dir>/docker-compose.override.yml` when it exists.
  - `type ComposeRunner interface { Run(ctx context.Context, argv []string) ([]byte, error) }` —
    runs a `docker compose …` argv in this process's environment; a non-zero exit returns
    `*HelperExitError{Code, Stderr}` (reuse the type, rename later only if a task says so).
  - `(*dockerexec.PanelUpdaterHost).ContainersInDir` and a `dockerexec.LocalCompose` implementing
    `ComposeRunner` with the existing `execCompose`.

- [ ] **Step 1: Write the failing tests** (temp dirs, fake `ProjectFinder`):
  - `TestReadLocalFilesRefusesSymlink`: `.env` is a symlink → `*Unavailable` naming the path.
  - `TestReadLocalFilesMissingEnvIsEmpty`.
  - `TestResolveInstallFromArgs`, `TestResolveInstallFromLabelsInDir`,
    `TestResolveInstallFallbackUsesComposeProjectName` (`.env` has
    `COMPOSE_PROJECT_NAME=relist-panel`, dir base `install` → project `relist-panel`, files are the
    Install file plus the override when present),
    `TestResolveInstallFallbackUsesDirName`.
- [ ] **Step 2: Run** `go test ./internal/panelupdate -run 'ReadLocal|ResolveInstall'` — FAIL.
- [ ] **Step 3: Implement.** Parse `.env` with the existing `parseEnv`.
- [ ] **Step 4: Run** `go test ./internal/panelupdate ./internal/dockerexec` — PASS.
- [ ] **Step 5: Commit** `feat: read the install from disk and find it by directory`

### Task 4: Standalone Preflight

**Files:**
- Create: `internal/panelupdate/checker.go`, `internal/panelupdate/checker_test.go`
- Modify: `internal/panelupdate/preflight.go` (move step functions onto `Checker`; delete what
  becomes dead in Task 9, not here), `internal/panelupdate/preflight_test.go`,
  `internal/panelupdate/preflight_compose_test.go`

**Interfaces:**
- Consumes: `ReadLocalFiles`, `ComposeRunner` (Task 3); `ParseManifest`, `UnknownKinds`,
  `mergeFile`, `writeCandidate`, `composeArgs`, `parseComposeConfig`, `evaluate`, `blame`,
  `parseHashes`, `setEnvKey`, `parseEnv` (exist).
- Produces:
  - `type Checker struct { DataDir, Version string; InstallFile []byte; Key ed25519.PublicKey; ManifestURL string; Client *http.Client; Docker Docker; Compose ComposeRunner; Now func() time.Time }`
  - `func (c *Checker) Check(ctx context.Context, in Install) (*Report, *Candidate, error)` — spec
    Preflight steps 1–8 in order. The error is only for "could not run at all"; every step
    failure is `Report.Failed`.
  - `Report` gains `UpToDate bool \`json:"upToDate"\``, `Fingerprint string \`json:"fingerprint"\``,
    `Image string \`json:"image"\`` (the manifest's digest ref). `Gate` is removed from `Report`
    (the web adds the live gate itself, Task 9).
  - `type Candidate struct { In Install; Manifest *Manifest; Files InstallFiles; Install, Env []byte; ImageID string }`
    (the exported form of today's `candidate`, plus the pulled image's ID).
  - `func WriteReport(dataDir string, r *Report) error` → `<data dir>/.panel-update/preflight/<id>/report.json`
    (`0600`), keeping only the latest id directory; `func ReadReport(dataDir, id string) (*Report, error)`.
  - `func (r *Report) ExitCode() int` — 3 when `UpToDate`, 0 when `Ready`, else 1.
  - The fingerprint covers what today's `fingerprint` covers plus the manifest bytes and the
    pulled image ID.

- [ ] **Step 1: Write the failing tests** in `checker_test.go` with an `httptest` manifest server
  signed by a test key, a fake `Docker` and a fake `ComposeRunner` returning recorded JSON:
  - `TestCheckUpToDate`: manifest version equal to `Version` → `UpToDate`, `ExitCode()==3`, no
    pull happened.
  - `TestCheckUnknownKindStopsBeforePull`: `NeedsManualUpdate`, fake Docker saw no pull.
  - `TestCheckBadSignature`: `Failed.Step == "manifest"` (new step name `StepManifest`).
  - `TestCheckNotEditedReady`, `TestCheckEditedMerged`, `TestCheckConflict` (moved from
    `preflight_test.go`, now through `Check`).
  - `TestCheckFingerprintChangesWithEnv`: same inputs → same fingerprint; one byte of `.env`
    changed → different.
  - `TestWriteReportKeepsLatest`: two writes leave one directory; `ReadReport` of the second id
    returns it.
  - Keep `preflight_compose_test.go`'s override cases (`env_file: ./x.env`, `./foo:/foo`, Traefik
    `command:` override fails `service-command-has` naming the override, `build:` fails
    `service-image`) running through `Check`.
- [ ] **Step 2: Run** `go test ./internal/panelupdate -run 'Check|WriteReport'` — FAIL.
- [ ] **Step 3: Implement** `Checker`. Fetch with the existing `get` logic (30 s, 10 MiB cap)
  moved to a function both `Service` and `Checker` call. The `Service.Preflight` method stays
  compiling until Task 9.
- [ ] **Step 4: Run** `go test ./internal/panelupdate` — PASS.
- [ ] **Step 5: Commit** `feat: run the preflight outside the panel`

### Task 5: The Updater runs the whole Panel update

**Files:**
- Modify: `internal/panelupdate/updater.go`, `internal/panelupdate/updater_test.go`
- Create: `internal/panelupdate/quiesce_request.go`, `internal/panelupdate/backup.go` (move
  `Backup`, `BackupFile`, `ReadBackup` and the backup writer here from `quiesce.go`)
- Delete: `internal/panelupdate/plan.go` and its tests

**Interfaces:**
- Consumes: `Checker`, `Candidate`, `ReadReport` (Task 4); `PanelAck`, `ReadAck`, `StateAborted`,
  `PanelNoAnswerNote` (Task 2); `ResolveInstall` (Task 3); `UpdaterHost` (exists, minus `Plan`).
- Produces:
  - `UpdaterHost` methods take `Install` instead of `Plan`: `ComposeUp(ctx, in Install) error`,
    `ComposeStop(ctx, in Install, service string) error`; `Containers` and `Healthz` unchanged;
    new `UpdaterState(ctx, name string) (UpdaterState, error)` (same as the watcher's).
  - `type Updater struct` gains `Checker *Checker`, `Domain string`, `AnswerWindow time.Duration`
    (default 20 s), `AckEvery time.Duration` (default 2 s), `Signals <-chan os.Signal`.
  - `func (u *Updater) Apply(ctx context.Context, a ApplyArgs) (RunStatus, error)` with
    `type ApplyArgs struct { Run, Report string; StepsDone []string; In Install }`. Steps exactly as
    spec "Apply" 1–10. Returns the terminal status.
  - `func vacuumInto(ctx context.Context, dbPath, dst string) error` — opens
    `file:<dbPath>?_pragma=busy_timeout(5000)` with `modernc.org/sqlite`, runs `VACUUM INTO ?`,
    closes. Never runs schema migrations.
- Quiesce rules (the parts the spec leaves to the plan):
  - **Answering:** a `panel.json` with `Run == a.Run` appears within `AnswerWindow` of writing
    `quiescing`.
  - **While waiting:** wait for `AckReady` with no limit; copy `WaitingOn` into `status.json` when
    it changes. If `Heartbeat` stops changing for `AnswerWindow` and the panel Container is not
    running → treat as not answering (stop nothing more, write the note, go on). A stalled
    heartbeat with the Container still running → keep waiting.
  - **`AckRefused`** → `aborted` with `Reason`.
  - **Not answering** → `ComposeStop(in, "panel")`, `Note = PanelNoAnswerNote`, go on.
  - **Signals:** a value on `Signals` before the backup starts → `aborted` ("cancelled"), return.
    After that, ignore it.
- Run directory: `Apply` refuses if `RunDir(a.Run)` already exists. Before quiescing nothing is
  written; a failed re-check writes one `status.json` in state `aborted` with the reason.
- `RELIST_DOMAIN` for health is `u.Domain`; the command (Task 6) reads it from the Install's
  `.env`, default `panel.localhost`.

- [ ] **Step 1: Write the failing tests** (fake host, fake ack writer goroutine, `Sleep` stubbed):
  - `TestApplyPanelAnswersAndSucceeds`: ack `waiting` (WaitingOn `Deployment api #3`) then `ready`
    → `status.json` saw `waitingOn`, backup written, `succeeded`.
  - `TestApplyIgnoresAckOfOtherRun`: `panel.json` with another run id → treated as not answering:
    `ComposeStop(panel)` called, note set.
  - `TestApplyHeartbeatNotClock`: ack heartbeat counter advances while its file mtime is an hour in
    the past → still answering.
  - `TestApplyRefusedIsAborted`: ack `refused` reason `a server Migration holds the lock` →
    `aborted`, nothing written to the Install.
  - `TestApplyNoAnswerStopsPanel`.
  - `TestApplyStalledHeartbeatPanelGone`: heartbeat stops, Container not running → note set, goes
    on to `succeeded`.
  - `TestApplySignalBeforeBackupAborts` and `TestApplySignalAfterBackupIgnored`.
  - `TestApplyRecheckMismatchAborts`: report fingerprint differs → `aborted`, no quiesce.
  - `TestApplyStepNotDoneAborts`.
  - `TestApplyMarksStaleRunInterrupted`; `TestApplyRefusesWhileStaleUpdaterRuns`.
  - `TestApplyRefusesExistingRunDir`.
  - Existing health-timeout → `rolled-back`, restore-failure → `rollback-failed`, WAL removed
    before restore, symlink refusal: kept, now through `Apply(ApplyArgs)`.
  - `TestVacuumIntoWhileOpen`: open the DB with a second `*sql.DB` holding a read transaction,
    `vacuumInto` succeeds and the copy has the rows.
- [ ] **Step 2: Run** `go test ./internal/panelupdate -run 'Apply|Vacuum'` — FAIL.
- [ ] **Step 3: Implement.** The quiesce loop lives in `quiesce_request.go`. Delete `Plan`,
  `ReadPlan`, `Validate`, `writePlan`.
- [ ] **Step 4: Run** `go test ./internal/panelupdate ./internal/dockerexec` — PASS.
- [ ] **Step 5: Commit** `feat: let the updater quiesce, back up and apply the whole update`

### Task 6: `relist-panel update`

**Files:**
- Create: `update.go`, `update_test.go` (package main)
- Delete: `panel_update.go` and its test
- Modify: `dispatch.go`, `dispatch_test.go`

**Interfaces:**
- Consumes: `Checker`, `WriteReport`, `Report.ExitCode` (Task 4); `Updater.Apply`, `ApplyArgs`
  (Task 5); `ResolveInstall`, `InstallArgs` (Task 3).
- Produces: subcommand `update` replacing `panel-update` in `subcommands`, summary
  `check or apply a Panel update (runs in its own Container)`.
  - Flags: `--preflight`, `--report ID`, `--run ID`, `--steps-done ID,…`, `--dir PATH`,
    `--project NAME`, `--file PATH` (repeatable), `--json`, `--help`.
  - `--preflight` and `--report` are exclusive; neither, or both → usage, exit 2. `--report`
    needs `--run` (a valid run id, `ValidRunID`), else exit 2.
  - `RELIST_DATA_DIR` must be absolute (exit 2 otherwise, today's message).
  - `--preflight`: `ResolveInstall` → `Check` → `WriteReport` → print (text, or `--json` one object
    on stdout) → exit `Report.ExitCode()`.
  - `--report`: `Apply` with `signal.Notify(ch, syscall.SIGTERM, os.Interrupt)`; exit 0 on
    `succeeded`, 1 otherwise. Logs go to stderr with `log.Printf`, one line per step.
  - Text report format: one line per field the spec's report lists; invariants as `ok`/`FAIL`
    lines with detail and source.

- [ ] **Step 1: Write the failing tests:** `TestUpdateUsage` (no mode → 2; both modes → 2;
  `--report` without `--run` → 2; bad run id → 2; `--help` → 0 and prints usage);
  `TestUpdateNeedsDataDir`; `TestDispatchListsUpdateNotPanelUpdate`. Mode functions take their
  dependencies as parameters so the tests use fakes and never start a server.
- [ ] **Step 2: Run** `go test -run 'Update|Dispatch' .` — FAIL.
- [ ] **Step 3: Implement** `runUpdate(args []string) int`.
- [ ] **Step 4: Run** `go test .` — PASS.
- [ ] **Step 5: Commit** `feat: add the update command`

### Task 7: `relist-panel setup`

**Files:**
- Create: `setup.go`, `setup_test.go` (package main)
- Modify: `dispatch.go`

**Interfaces:**
- Consumes: `installFile` (embedded, `main.go`).
- Produces: subcommand `setup`, summary `write the Install file and .env for a new install`.
  `relist-panel setup --dir PATH --domain HOST --image REF [--acme-email ADDRESS]`.
  - Writes `<dir>/docker-compose.yml` (`0644`, the embedded bytes) and `<dir>/.env` (`0600`) with,
    in this order: `RELIST_DOMAIN=…`, `RELIST_ACME_EMAIL=…` (empty allowed), `RELIST_PANEL_IMAGE=…`,
    `COMPOSE_PROJECT_NAME=relist-panel`.
  - Refuses (exit 1, nothing written) when either file exists. `--dir` must be absolute and
    exist. Missing `--domain` or `--image` → exit 2. Values containing a newline → exit 2.

- [ ] **Step 1: Write the failing tests:** `TestSetupWritesFiles` (contents and modes),
  `TestSetupRefusesExisting` (pre-existing `.env` → exit 1, compose file not created),
  `TestSetupUsage`.
- [ ] **Step 2: Run** `go test -run Setup .` — FAIL.
- [ ] **Step 3: Implement** `runSetup(args []string, stdout, stderr io.Writer) int`.
- [ ] **Step 4: Run** `go test .` — PASS.
- [ ] **Step 5: Commit** `feat: add the setup command`

### Task 8: The panel answers the quiesce request

**Files:**
- Modify: `internal/panelupdate/watch.go`, `internal/panelupdate/lock.go`,
  `internal/panelupdate/service.go`
- Delete: `internal/panelupdate/quiesce.go` (what is left after Task 5 moved the backup out),
  `apply.go`'s `QuiesceAndBackup` callers
- Test: `internal/panelupdate/update_lock_test.go`, `internal/panelupdate/watch_test.go` (create)

**Interfaces:**
- Consumes: `PanelAck`, `WriteAck`, `StateAborted` (Task 2).
- Produces:
  - `Deps` gains `WaitingOn func() string` — what the lock waits on, `""` when idle. `httpapi`
    wires it from `(*deployments.Queue).Running()` as `Deployment <compose name> #<number>` and,
    when only the scan worker is busy, `a vulnerability scan`.
  - Watcher on a non-terminal newest run: if a Migration lock is held → `WriteAck{State: refused,
    Reason: ErrMigrationLocked.Error()}` and do not lock; else `lock()` once, start
    `Queue.WaitIdle` and `Scans.WaitIdle` in a goroutine, and every `watchEvery` write
    `WriteAck{Run, State: waiting|ready, WaitingOn, Heartbeat: n++}` until the run is terminal.
  - `Deps.Updater` keeps `UpdaterState`, `RemoveUpdater`, `ListUpdaters`, `StartUpdater`.
  - Remove `guard`, `setGuard`, `guarded`, `MarkStarted`, `AbortRun`, `QuiesceAndBackup`,
    `runMu`, `stateStartingUpdater`.
  - `allowedWhileUpdating` also allows `POST /api/panel-update/cancel`.

- [ ] **Step 1: Write the failing tests:**
  - `TestWatcherAcksReadyWhenIdle`: non-terminal `status.json` → lock on, `panel.json`
    `ready` with heartbeat ≥ 1.
  - `TestWatcherAcksWaitingWithDeployment`: fake queue busy, `WaitingOn` returns
    `Deployment api #3` → ack `waiting` with that text; after the queue goes idle → `ready` and the
    heartbeat advanced.
  - `TestWatcherRefusesUnderMigrationLock`: ack `refused`, `Locked()` false.
  - `TestWatcherInterruptsAfterBackup` (Review Focus 3): run in `backing-up`, Updater Container
    exited → `status.json` `interrupted`, lock released.
  - `TestCancelAllowedWhileLocked`: `POST /api/panel-update/cancel` passes the middleware; `POST
    /api/panel-update/apply` gets 503.
  - Existing start-locked and 503 tests keep passing.
- [ ] **Step 2: Run** `go test ./internal/panelupdate -run 'Watcher|Cancel|Lock'` — FAIL.
- [ ] **Step 3: Implement**, and wire `WaitingOn` in `internal/httpapi/httpapi.go`.
- [ ] **Step 4: Run** `go test ./internal/panelupdate ./internal/httpapi` — PASS.
- [ ] **Step 5: Commit** `feat: answer the updater's quiesce request from the panel`

### Task 9: The panel's API starts the `update` Container

**Files:**
- Modify: `internal/panelupdate/http.go`, `internal/panelupdate/apply.go`,
  `internal/panelupdate/preflight.go`, `internal/panelupdate/install.go`,
  `internal/panelupdate/service.go`, `internal/dockerexec/panel_update.go`,
  `internal/httpapi/httpapi.go`, `main.go`
- Test: `internal/panelupdate/http_test.go`, `internal/dockerexec/panel_update_test.go`

**Interfaces:**
- Consumes: `Report`, `UpdaterName`, `NewRunID`, `ValidRunID`, `ReadReport` (Tasks 2, 4).
- Produces:
  - `type UpdateRunner interface { Preflight(ctx context.Context, spec UpdateSpec) (stdout []byte, exitCode int, err error); StartUpdater(ctx context.Context, spec UpdateSpec) error; StopUpdater(ctx context.Context, name string) error }`
    replacing `Helper` and the old `StartUpdater(UpdaterSpec)`.
  - `type UpdateSpec struct { Name, Image string; Args []string; InstallDir, DataDir, Network string; Env []string }`
    — `docker run [--rm | -d --name Name] --network Network -v sock -v InstallDir:InstallDir
    -v DataDir:DataDir -e RELIST_DATA_DIR=DataDir [-e Env…] Image update Args…`, no Compose labels.
    `Env` passes `RELIST_UPDATE_MANIFEST_URL` through when the panel has it set.
  - `POST /api/panel-update/preflight`: `FindInstall` → runner `Preflight` with `--preflight --json
    --project --dir --file…` → parse stdout as `Report`; exit 1 or 3 is still a report; exit 2 or a
    parse failure is 500. The response adds `gate` (Migration lock, Deployments busy, update
    running) next to the report: `{report, gate}`. The panel remembers the latest report id.
  - `POST /api/panel-update/apply` `{report_id, manual_steps_done}`: 409 if the id is not the
    latest or a run is not terminal; `run := NewRunID(now)`; `StartUpdater` with
    `--report id --run run --steps-done … --project --dir --file…`; respond `{run}` at once.
  - `POST /api/panel-update/cancel`: newest run in `quiescing` → `StopUpdater(UpdaterName(run))`,
    202; otherwise 409 `Only a waiting update can be cancelled`.
  - `RunInfo` gains `waitingOn` and `note`.
  - Delete `Helper`, `HelperRun`, `PanelHelper`, `ReadInstallFiles`/`readFile`, `runCompose`'s
    helper path, `Service.Preflight`, `preflightLocked`, `Service.Apply`, `LatestReport`'s
    fingerprint field.

- [ ] **Step 1: Write the failing tests** (fake `UpdateRunner`):
  - `TestPreflightEndpointRunsContainer`: argv contains `--preflight --json` and the label values;
    response is the fake report plus `gate`.
  - `TestPreflightEndpointExitThreeIsReport`.
  - `TestApplyEndpointStartsDetachedAndReturns`: runner saw `StartUpdater` with name
    `relist-panel-updater-<run>` and args `--report <id> --run <run> --steps-done a,b`; response
    `{run}`.
  - `TestApplyEndpointRefusesStaleReport` (409), `TestApplyEndpointRefusesRunningRun` (409).
  - `TestCancelEndpoint`: `quiescing` → `StopUpdater` called, 202; `compose-up` → 409.
  - `dockerexec`: `TestUpdateSpecArgv` for both `--rm` and `-d --name`.
- [ ] **Step 2: Run** `go test ./internal/panelupdate ./internal/dockerexec -run 'Endpoint|UpdateSpec'` — FAIL.
- [ ] **Step 3: Implement** and rewire `main.go`/`httpapi.go` (`PanelHelper` removed,
  `PanelUpdater` becomes the `UpdateRunner`).
- [ ] **Step 4: Run** `go vet ./... && go test ./...` — PASS.
- [ ] **Step 5: Commit** `feat: start the update container from the panel's api`

### Task 10: Web — Waiting, Cancel, Aborted

**Files:**
- Modify: `web/src/data/panel-update.ts`, `web/src/data/usePanelUpdate.ts`,
  `web/src/pages/PanelUpdatePage.tsx`, their tests under `web/src/**/__tests__` or `*.test.ts(x)`
  next to them (follow the existing location)

**Interfaces:**
- Consumes: `RunInfo.waitingOn`, `RunInfo.note`, `POST /api/panel-update/cancel`, the preflight
  response `{report, gate}` (Tasks 8, 9).
- Produces:
  - `RunState` drops `starting-updater`, adds `aborted`; `UPDATE_STEPS` starts at `quiescing`.
  - `PageState` adds `'waiting'`: `status.run` non-terminal in `quiescing`.
  - `PanelUpdateRun` gains `waitingOn: string`, `note: string`.
  - `cancelUpdate(): Promise<void>` in the data module.
  - `finishedRun` includes `aborted`, shown as an info `Alert` titled `Update cancelled` (or the
    reason when refused by a Migration lock); nothing was written.
  - The confirm text says queued Deployments are cancelled and a running Deployment or scan is
    waited for (spec "Confirmation").

- [ ] **Step 1: Write the failing tests:** `pageState` returns `'waiting'` for a `quiescing` run;
  the page in Waiting shows `waitingOn` text and a **Cancel** button that calls
  `cancelUpdate`; an `aborted` run shows the info alert; a run with `note` shows it as a warning
  in Updating.
- [ ] **Step 2: Run** `pnpm --dir web test -- panel-update` — FAIL.
- [ ] **Step 3: Implement.** Use the antd components the page already uses (`Steps`, `Alert`,
  `Button`); check their 6.5.1 props on ant.design first.
- [ ] **Step 4: Run** `pnpm --dir web typecheck && pnpm --dir web test` — PASS. Then rebuild the
  embedded frontend the way commit `175729c` did and run `go test .`.
- [ ] **Step 5: Commit** `feat: show the waiting step and cancel on the panel update page`
  (the embedded build as a second commit `chore: rebuild the embedded frontend`).

### Task 11: `install.sh`

**Files:**
- Create: `release/install.sh`, `install_script_test.go` (package main)

**Interfaces:**
- Consumes: `setup` flags (Task 7), `update` flags and exit codes (Task 6),
  `create-admin --email --generate` (exists).
- Produces: a POSIX `sh` script with `VERSION="@VERSION@"` and `IMAGE="@IMAGE@"` placeholders the
  workflow fills; `RELIST_INSTALL_IMAGE` overrides `IMAGE`. Behaviour exactly as spec "The
  Install script". Order:
  1. parse and validate flags (unknown flag, bad domain, `--auto` without `--domain` → exit 2) —
     before any check, so validation needs no Docker;
  2. no terminal (`[ -t 0 ] || [ -r /dev/tty ]` fails to open) and no `--auto` → exit 2 with
     `No terminal to ask on; pass --auto with --domain and --email`;
  3. the checks, then the writes, as the spec lists.
- Domain rule: `^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`
  checked with `grep -Eq`; a scheme, path, port or space is refused with
  `Give the host name only, like panel.example.com`.
- `--update` generates the run id with `date -u +%Y%m%dT%H%M%SZ` and reads `RELIST_DATA_DIR` from
  `<dir>/.env` (default `/var/lib/relist-panel`) for the `-v` mounts.

- [ ] **Step 1: Write the failing tests** in `install_script_test.go`, each running
  `sh release/install.sh …` with `PATH` set to a temp dir containing a fake `docker` that records
  its argv and fails the test if called:
  - `TestInstallScriptRejectsURLDomain`: `--auto --domain https://panel.example.com/` → exit 2,
    stderr has `host name only`, fake docker never called.
  - `TestInstallScriptRejectsUnknownFlag` → exit 2.
  - `TestInstallScriptAutoNeedsDomain` → exit 2.
  - `TestInstallScriptNoTerminalNeedsAuto`: stdin `/dev/null`, no controlling terminal
    (`SysProcAttr{Setsid: true}`) → exit 2 with the no-terminal message (Review Focus 4).
  - Skip with `t.Skip` when `sh` is missing; run once more under `dash` when it is on `PATH`.
- [ ] **Step 2: Run** `go test -run InstallScript .` — FAIL.
- [ ] **Step 3: Write `release/install.sh`.**
- [ ] **Step 4: Run** `shellcheck -s sh release/install.sh && go test -run InstallScript .` — PASS.
- [ ] **Step 5: Commit** `feat: add the install script`

### Task 12: Release workflow ships `install.sh`

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `release/install.sh` placeholders (Task 11).
- Produces: after "Verify the signature", a step `Write the install script` that `sed`s
  `@VERSION@` and `@IMAGE@` (`ghcr.io/relist/relist-panel@<digest>`) into `install.sh`, fails if a
  placeholder is left (`grep -q '@VERSION@\|@IMAGE@'`), and runs `shellcheck -s sh install.sh`
  (preinstalled on `ubuntu-latest`). `gh release create` attaches `install.sh` too. The trigger
  stays `workflow_dispatch`.

- [ ] **Step 1: Edit the workflow.**
- [ ] **Step 2: Verify** `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"`
  succeeds and `actionlint` passes if installed (say so if it is not).
- [ ] **Step 3: Commit** `ci: attach the install script to each release`

### Task 13: End-to-end probe in dind

**Files:**
- Modify: `scripts/probe-panel-update.sh`, `Taskfile.yml` (only if the task's command changes)

**Interfaces:**
- Consumes: everything above.
- Produces: the probe, still refusing to run outside docker:dind, does the spec's six end-to-end
  steps in order with a local registry and `RELIST_INSTALL_IMAGE`/`RELIST_UPDATE_MANIFEST_URL`
  pointing at it, plus the test release key built in with `RELEASE_KEY`:
  1. `install.sh --auto --domain panel.localhost` installs 1.0.0; the password line is printed;
  2. a second `install.sh` exits non-zero with the refusal;
  3. a fake long Deployment (a Compose whose build sleeps 30 s) runs; `install.sh --update` to
     1.1.0 logs the `waitingOn` text, then succeeds;
  4. a run cancelled with `docker stop` while `quiescing` ends `aborted` and `/api/panel-update/status`
     reports `locked: false`;
  5. a run against an image whose `/healthz` fails rolls back; every Install file and `.env`
     match their pre-run bytes (`cmp`);
  6. with `relist-panel` stopped, `install.sh --update` notes the panel did not answer and
     succeeds.
- Prints `PASS`/`FAIL` per step and exits non-zero on any failure.

- [ ] **Step 1: Extend the probe.**
- [ ] **Step 2: Run** `task probe:panel-update` — all six PASS. Paste the step lines into the
  commit message body.
- [ ] **Step 3: Commit** `test: probe install and update end to end in dind`

### Task 14: Docs and tracking

**Files:**
- Modify: `README.md`, `docs/GUIDELINES.md` (§4.15, if it names states), `.scratch/self-update/followups.md`,
  `.scratch/self-update/issues/*.md`, `release/manual-steps.json` (only if a step for existing
  installs is needed)

**Interfaces:**
- Consumes: final behaviour of Tasks 1–13.
- Produces:
  - README "Self-hosted install": the `curl | sh` commands and flags first; the old clone-and-build
    steps move under "Building from source" with the override copy step; `docker exec
    relist-panel …` replaces `docker compose exec panel …` where it reads better; "Panel update"
    section names `install.sh --update` and the Cancel button; "Upgrading an existing install"
    gets the spec's three steps for existing Installs.
  - followups: remove the two items this rework closes (quiescing blocks Apply; Updater signal
    handling before the backup) and the helper-only items (helper stdout buffer, helper scratch
    dir, `working_dir` `:`/`,` in `-v` if no longer relevant); keep the rest.
  - issues: ticket 08 → `closed` (done by Task 1); add `09-one-updater-and-install-script.md`,
    `closed`, pointing at ADR-0025 and this plan; ticket 02 keeps its owner steps.

- [ ] **Step 1: Edit the docs.**
- [ ] **Step 2: Verify** every command in the README sections you touched matches a flag that
  exists (`grep` the flag names in `update.go`, `setup.go`, `release/install.sh`).
- [ ] **Step 3: Commit** `docs: document the install script and the update command`
