# Vulnerability scanning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scan the images the host runs for CVEs, in the background, with a locked-down Trivy container that never holds the Docker socket. Show the results in a new Vulnerabilities menu and as badges on Overview, Project and Compose.

**Architecture:**
- A new `internal/vulnscan` package owns:
  - the five tables, including the scan queue;
  - a worker that yields to the Deployment queue;
  - the daily scheduler;
  - `/api/vulnerabilities`.
- It consumes three ports, all implemented in `internal/dockerexec`:
  - `Scanner`: exports the image to a tar under `${RELIST_DATA_DIR}/.scan-work`, then runs Trivy `--input` in a container with no network, no capabilities and a read-only root filesystem;
  - `InUse`: classifies the running containers;
  - `DeployGate`: a small method added to `deployments.Queue`.
- `deployments.Deps` gains an `OnSucceeded` callback that `httpapi` wires to vulnscan.

**Tech Stack:** Go (stdlib `net/http`, `database/sql`, `modernc.org/sqlite`, goose), the Docker Engine API over the socket plus the `docker` CLI for `run`/`pull`, and Trivy 0.76.1. React + antd 6.5.1, vitest + Testing Library.

**Spec:** `.scratch/vuln-scan/spec.md`

## Global Constraints

- **Migrations:** never edit committed migrations. The only schema change is the new `internal/migrations/00022_vulnerability_scans.sql`, exactly as the spec's Data model section.
- **Default Scanner image:** `aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c`.
  - Re-check the digest once with `docker buildx imagetools inspect aquasec/trivy:0.76.1` before Task 4 is committed. It must be the OCI index digest.
  - Never `0.69.4`.
- **Scanner image reference form:** `^[a-z0-9][a-z0-9._/:-]*(:[A-Za-z0-9._-]+)?@sha256:[0-9a-f]{64}$`. Anything else is a `422` with `Scanner image must be repo[:tag]@sha256:<64 hex digits>`.
- **The scan container never gets** `/var/run/docker.sock`, `--privileged`, `--network` other than `none`, or any capability. Only the DB-update container has network.
- **Fixed names:**
  - container `relist-vulnscan-<first 12 hex of image id>`, or `relist-vulnscan-db` for the DB update;
  - label `relist.vulnscan=1`;
  - cache volume `relist-trivy-cache`;
  - work dir `${RELIST_DATA_DIR}/.scan-work`;
  - uid/gid `65532:65532`.
- **Fixed limits:**
  - `--cpus 1`, `--memory 1g`, `--tmpfs /tmp:rw,noexec,nosuid,size=512m,uid=65532`;
  - scan timeout 10 min, DB update 5 min, settings-save pull timeout 2 min;
  - disk check: image `Size` plus 512 MiB.
- **Thresholds:** "new" means first seen within 7 days. "DB stale" means `db_updated_at` is older than 7 days, or `db_error != ''`. The tag window is 14 days.
- **Ingest filter:** drop package names `linux-libc-dev`.
- **Copy, verbatim:**
  - no DB: `vulnerability DB not downloaded yet`
  - disk: `not enough disk space for export`
  - OOM (exit 137): `scanner ran out of memory (1 GB limit)`
  - off: `Scanning is off`
  - never scanned: `Not scanned yet`
  - failure tag: `Last scan failed: <reason>`
  - scan while off: `409 Scanning is off`
  - cache delete while on: `409 Turn scanning off before deleting the cache`
- **Commits:** Conventional Commits with no scope, and no `Co-Authored-By` or `Claude-Session` trailers (`docs/agents/commit-conventions.md`).
- **Frontend:** before touching `web/`, read https://ant.design/llms.txt and the pages for Table (expandable, server pagination), Tabs, Drawer, Switch, TimePicker, Modal, Badge, Tag, Alert, Descriptions and Statistic. Follow `docs/GUIDELINES.md`.
- **Tests:** `go test ./...`, `pnpm --dir web test` and `pnpm --dir web typecheck`. `task build` rebuilds `internal/webassets/dist`, which is committed.

## Decisions made while planning (sharpen the spec)

- **Daily time zone:** "local time" is the panel process's `time.Local`. In the container that is UTC unless `TZ` is set. `/status` returns `timeZone` (`time.Local.String()`), and the drawer shows it next to the TimePicker, so the operator knows what 03:00 means.
- **How containers are classified** (see `InUse`):
  - A container whose `com.docker.compose.project` is a known Compose deploy identity, from `knownComposeProjects` in `main.go`, is a Service. Its `com.docker.compose.service` label is the Service name.
  - The panel's own container is found by `os.Hostname()`. Containers with its compose project label and service `panel` or `traefik` are `panel` and `traefik`.
  - Every other container is ignored.
- **Deploy hook placement:** `OnSucceeded` fires after the Deployment's row is persisted as `succeeded`, outside the Project gate, in a new goroutine. A slow vulnscan therefore never delays the Deployment queue.
- **Where filtering happens:** the ingest filter and duplicate collapse run in `vulnscan` (`normalizeFindings`), not in the parser. `dockerexec` stays a faithful translation of Trivy's JSON.
- **Prune ownership:** the Scanner image reference and `relist-trivy-cache` are added to `dockerhost.Ownership` under the owner name `Vulnerability scanner`. Prune then keeps them unless the operator chooses "include panel resources".
- **Migration copy:** `.scan-work` joins `skipInDataDir` in `internal/migration/manifest.go`. Because the volume is not a data-dir item, nothing else changes.

## Review Focus

1. **The image is removed or retagged while it waits in the queue.**
   - Expected: the export 404s and the task fails with `image no longer exists`. The row keeps its last good findings. The worker moves on, with no crash and no retry loop.
   - Test: Task 6 (`TestWorkerImageGone`).
2. **The panel is killed in the middle of a scan.**
   - Expected: on the next start, the leftover `relist-vulnscan-*` container and `.scan-work/*.tar` are removed before any task runs. The interrupted task runs again exactly once.
   - Test: Task 6 (`TestWorkerRestartCleansAndRequeues`).
3. **Many Deployments finish in quick succession** (Project-wide deploy of 10 Composes that share images).
   - Expected: each image is queued at most once. Scans never start while the next Deployment is running.
   - Test: Task 6 (`TestWorkerYieldsAndDedupes`).
4. **The operator saves a Scanner image that does not exist, or a digest with no tag.**
   - Expected: no tag is fine. A bad digest gives `422` with Docker's error. The old image keeps working, and the setting row is unchanged.
   - Tests: Task 8 (`TestPutSettingsBadImageKeepsOld`) and Task 3 (`TestValidRef`).
5. **A huge Trivy result** (node:24: ~6,000 raw entries, 23 MB JSON).
   - Expected: it is parsed in a stream from stdout (no full `[]byte` copy kept beyond decoding), stored in one transaction, and `/cves` stays paginated.
   - Test: Task 2 (`TestRecordScanLarge`, 6,000 generated findings in under 2 s).

---

### Task 1: Migration, settings store and queue store

**Files:**
- Create: `internal/migrations/00022_vulnerability_scans.sql` (the spec's SQL verbatim, including both `INSERT`s and `-- +goose Down` dropping the five tables)
- Create: `internal/vulnscan/settings.go`
- Create: `internal/vulnscan/queue_store.go`
- Create: `internal/vulnscan/store_test.go`
- Modify: `internal/database/database_test.go`: add `TestMigration00022` asserting the five tables and the seeded `scan_settings` row.

**Interfaces:**
- Produces:

```go
package vulnscan

const DefaultScannerImage = "aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c"

type Settings struct {
    Enabled, AfterDeploy, Daily bool
    DailyMinute                 int    // 0..1439
    ScannerImage                string // "" = DefaultScannerImage
    LastDailyAt                 *time.Time
    DBVersion                   string
    DBUpdatedAt                 *time.Time
    DBError                     string
}
func (s Settings) EffectiveImage() string

type Store struct{ db *sql.DB }
func NewStore(db *sql.DB) *Store
func (s *Store) Settings(ctx context.Context) (Settings, error)
func (s *Store) SaveSettings(ctx context.Context, v Settings) error        // writes enabled, after_deploy, daily, daily_minute, scanner_image only
func (s *Store) SetDB(ctx context.Context, version string, at time.Time, errText string) error // errText != "" keeps version/at unchanged
func (s *Store) SetLastDaily(ctx context.Context, at time.Time) error

type TaskKind string
const (KindDBUpdate TaskKind = "db_update"; KindImage TaskKind = "image")
type Reason string
const (ReasonDeploy Reason = "deploy"; ReasonDaily = "daily"; ReasonManual = "manual"; ReasonSetup = "setup")
type Task struct {
    ID       int64
    Kind     TaskKind
    ImageID  string
    Reason   Reason
    QueuedAt time.Time
    Started  bool
}
func (s *Store) Enqueue(ctx context.Context, kind TaskKind, imageID string, reason Reason, now time.Time) (bool, error) // false = already queued; a queued 'manual' upgrades an automatic reason
func (s *Store) Next(ctx context.Context) (Task, bool, error)   // db_update first, then queued_at, id
func (s *Store) Start(ctx context.Context, id int64, now time.Time) error
func (s *Store) Done(ctx context.Context, id int64) error        // deletes the row
func (s *Store) ClearQueue(ctx context.Context) error
func (s *Store) Unstart(ctx context.Context) (int, error)        // started_at = NULL for all
func (s *Store) QueueState(ctx context.Context) (running *Task, queued int, err error)
```

- [ ] **Step 1: Write failing tests** in `store_test.go` (package `vulnscan_test`, using the repo's existing test DB helper from `internal/database`):
  - `TestSettingsDefaults`: a fresh DB gives `Enabled == false`, `DailyMinute == 180`, and `EffectiveImage() == DefaultScannerImage`.
  - `TestSettingsRoundTrip`
  - `TestSetDBErrorKeepsVersion`
  - `TestEnqueueDedupes`: the same image twice gives `(true, nil)` then `(false, nil)`, and `QueueState` reports `queued == 1`.
  - `TestEnqueueManualUpgradesReason`
  - `TestNextOrder`: an image queued before a `db_update` still comes out second.
  - `TestUnstart`
- [ ] **Step 2:** Run `go test ./internal/vulnscan/ ./internal/database/`. Expected: FAIL (package missing).
- [ ] **Step 3:** Implement the migration and the two files.
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: add vulnerability scan tables, settings and queue store`.

### Task 2: Results store, ingest normalization and read queries

**Files:**
- Create: `internal/vulnscan/results.go` (writes and normalization)
- Create: `internal/vulnscan/queries.go` (reads)
- Create: `internal/vulnscan/results_test.go`

**Interfaces:**
- Consumes: `Store` (Task 1).
- Produces:

```go
type Severity string // "CRITICAL","HIGH","MEDIUM","LOW","UNKNOWN"
type Finding struct {
    VulnID, PkgName, PkgPath, InstalledVersion, FixedVersion string
    Severity                                                 Severity
    Title, URL                                               string
}
type Report struct {
    OS       string
    Findings []Finding
}

var ignoredPackages = map[string]bool{"linux-libc-dev": true}
func normalizeFindings(in []Finding) []Finding // drops ignored packages, collapses duplicate primary keys (first wins), maps an unknown severity to UNKNOWN

func (s *Store) RecordScan(ctx context.Context, imageID, imageRef string, r Report, dbVersion string, now time.Time) error // one tx: replace findings, set scanned_at/db_version/error='', INSERT OR IGNORE first_seen
func (s *Store) RecordFailure(ctx context.Context, imageID, imageRef, reason string, now time.Time) error              // upsert row; only error and attempted_at change on an existing row
func (s *Store) ScanState(ctx context.Context, imageID string) (dbVersion, errText string, found bool, err error)
func (s *Store) PruneExcept(ctx context.Context, keep []string) (int, error) // deletes image_scans not in keep; findings cascade; never touches first_seen

type Counts struct{ Critical, High, Medium, Low, Unknown, Fixable int }
type ImageResult struct {
    ImageID, ImageRef, OS, Error string
    ScannedAt, AttemptedAt       *time.Time
    Counts                       Counts
}
func (s *Store) ImageResults(ctx context.Context, imageIDs []string) (map[string]ImageResult, error)
func (s *Store) Counts(ctx context.Context, imageIDs []string) (Counts, error) // unique vuln_id per severity across the given images; Fixable = unique vuln_id with any fixed_version != ''

type FindingRow struct {
    Finding
    FirstSeenAt time.Time
    New         bool
}
func (s *Store) Findings(ctx context.Context, imageID string, now time.Time) ([]FindingRow, error) // ordered by severity rank, then vuln_id

type CVEQuery struct {
    ImageIDs         []string // scope; the HTTP layer resolves projectId/composeId to image IDs
    Severities       []Severity
    FixableOnly, NewOnly bool
    Q                string // case-insensitive match on vuln_id, pkg_name or title
    Page, PageSize   int
}
type CVERow struct {
    VulnID      string
    Severity    Severity // highest across matched findings
    Title       string
    Fixable     bool
    FirstSeenAt time.Time
    New         bool
    ImageIDs    []string
}
func (s *Store) CVEs(ctx context.Context, q CVEQuery, now time.Time) ([]CVERow, int, error)
```

- [ ] **Step 1: Write failing tests:**
  - `TestNormalizeDropsLibcDevAndCollapses`
  - `TestRecordScanReplacesFindings`: the second scan has only 1 finding, so only 1 row remains.
  - `TestRecordFailureKeepsFindings`: `error` is set and the findings count is unchanged.
  - `TestFirstSeenSurvivesPrune`: record a scan, `PruneExcept(nil)`, then record a scan of a different image with the same CVE. `first_seen_at` keeps the first timestamp.
  - `TestCountsUniqueAcrossImages`: CVE-1 CRITICAL in two images counts 1. Fixable counts 1 if either finding has a fix.
  - `TestFindingsNewFlag`: first seen 6 days ago gives `New`, 8 days ago does not.
  - `TestCVEsFiltersAndPagination`: severity, fixable, new, `Q`, and page 2 of size 2 over 5 CVEs, with total 5.
  - `TestRecordScanLarge`: 6,000 generated findings record in under 2 s.
- [ ] **Step 2:** Run `go test ./internal/vulnscan/`. Expected: FAIL.
- [ ] **Step 3:** Implement. `RecordScan` calls `normalizeFindings` itself. Use one prepared `INSERT` inside the transaction.
- [ ] **Step 4:** Run `go test ./internal/vulnscan/`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: store vulnerability scan results and first-seen dates`.

### Task 3: Trivy translation: parser, argv builders, reference validator

**Files:**
- Create: `internal/dockerexec/trivy.go` (pure functions only)
- Create: `internal/dockerexec/trivy_test.go`
- Create: `internal/dockerexec/testdata/trivy/{os-alpine.json,go-binary.json,node.json,debian-libc.json,clean.json}`. These are small hand-trimmed Trivy 0.76.1 outputs with 2–5 entries each, in the real field layout. `clean.json` has `Results` without `Vulnerabilities`.

**Interfaces:**
- Consumes: `vulnscan.Report` and `vulnscan.Finding` (Task 2).
- Produces:

```go
func ParseTrivy(r io.Reader) (vulnscan.Report, error) // json.Decoder over stdout; maps Results[].Vulnerabilities[] fields per the spec; OS from Metadata.OS "Family Name"
func ValidScannerRef(ref string) bool                  // the Global Constraints regexp
func scanArgv(ref, imageHex, tarPath string) []string  // the spec's `docker run` for a scan, without the leading "docker"
func dbUpdateArgv(ref string) []string
func chownArgv(ref string) []string                    // one-off root run: chown 65532:65532 on the cache volume mounted at /v
```

- [ ] **Step 1: Write failing tests:**
  - `TestParseTrivyFixtures`: table-driven over the five fixtures, with expected `OS` and counts. `go-binary.json` keeps `PkgPath`. `debian-libc.json` still contains `linux-libc-dev`, because filtering is vulnscan's job.
  - `TestParseTrivyMalformed` returns an error.
  - `TestValidRef`:
    - accepted: `aquasec/trivy@sha256:<64>`, `ghcr.io/aquasecurity/trivy:0.76.1@sha256:<64>`
    - rejected: no digest, a 63-hex digest, an uppercase hex digest, `latest`
  - `TestScanArgvLockdown` asserts the argv contains:
    - `--network none`, `--read-only`, `--cap-drop ALL`, `--security-opt no-new-privileges`, `--user 65532:65532`, `--cpus 1`, `--memory 1g`
    - the tmpfs spec verbatim, `-v relist-trivy-cache:/cache`
    - `-v <tarPath>:/work/image.tar:ro`, `--label relist.vulnscan=1`, `--name relist-vulnscan-<hex12>`
    - `--skip-db-update`, `--skip-java-db-update`, `--offline-scan`, `--scanners vuln`, `-f json`, `-q`

    It also asserts that no element contains `docker.sock` and none is `--privileged`.
  - `TestDBUpdateArgv`: there is no `--network none`, no tar mount and no `docker.sock`. The other lockdown flags are present, plus `--download-db-only`.
- [ ] **Step 2:** Run `go test ./internal/dockerexec/ -run 'Trivy|ValidRef|Argv'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: translate trivy output and build locked-down scanner commands`.

### Task 4: `TrivyScanner` adapter

**Files:**
- Create: `internal/dockerexec/trivy_scanner.go`
- Create: `internal/dockerexec/trivy_scanner_test.go`
- Modify: `internal/vulnscan/ports.go` (create), which declares the ports.

**Interfaces:**
- Produces in `vulnscan/ports.go`:

```go
type DBInfo struct{ Version string } // metadata.json UpdatedAt, RFC 3339
var ErrNoDB = errors.New("vulnerability DB not downloaded yet")
var ErrImageGone = errors.New("image no longer exists")
type Scanner interface {
    EnsureImage(ctx context.Context, ref string) error
    UpdateDB(ctx context.Context, ref string) (DBInfo, error)
    DBInfo(ctx context.Context, ref string) (DBInfo, error) // ErrNoDB when the volume has no metadata.json
    Scan(ctx context.Context, ref, imageID string) (Report, error)
    ImageID(ctx context.Context, ref string) (string, error) // the local image ID of the Scanner image
    RemoveImage(ctx context.Context, ref string) error
    DeleteCache(ctx context.Context) error
    CacheSize(ctx context.Context) (int64, error)
    Cleanup(ctx context.Context) error
}
```

- Produces in `dockerexec`:

```go
type TrivyScanner struct { /* socket client, dataDir, runDocker func(ctx, argv, stdin, stdout, stderr) error, freeBytes func(path string) (uint64, error) */ }
func NewTrivyScanner(socketPath, dataDir string) *TrivyScanner
var _ vulnscan.Scanner = (*TrivyScanner)(nil)
```

- Behaviour:
  - **EnsureImage:**
    1. `docker image inspect ref`.
    2. If it is missing, `docker pull ref`.
    3. Run `--version` with the lockdown flags.
    4. Create the volume if it is missing, and chown it once with `chownArgv`.
  - **DBInfo:** read `/cache/db/metadata.json` by running the Scanner image with `--entrypoint cat` and the lockdown flags.
  - **Scan:**
    1. `GET /images/{id}/json`. A 404 returns `ErrImageGone`.
    2. Check free space with `freeBytes`. Fail with `not enough disk space for export` if it is short.
    3. Stream `GET /images/{id}/get` to `.scan-work/<hex>.tar`, then chmod 0644.
    4. Run `scanArgv`, then `ParseTrivy` on stdout.
    5. Remove the tar in a `defer`.
    6. An exit status of 137 becomes `scanner ran out of memory (1 GB limit)`.
  - **Cleanup:** remove the containers listed by `docker ps -aq --filter label=relist.vulnscan=1`, then delete `.scan-work/*`.
  - **DeleteCache:** `docker volume rm relist-trivy-cache`.

- [ ] **Step 1: Write failing tests** with `runDocker`, `freeBytes` and an `httptest` Unix-socket Engine faked. Follow the existing `dockerexec` test style.
  - `TestScanRemovesTarOnFailure`
  - `TestScanImageGone`
  - `TestScanDiskShort`: no tar is written.
  - `TestScanOOMMessage`
  - `TestEnsureImagePullsOnlyWhenMissing`
  - `TestCleanupRemovesLabelledAndWorkFiles`
  - `TestTrivyScannerIntegration` runs a real `traefik/whoami` scan. It is skipped unless `RELIST_DOCKER_TESTS=1` and `docker info` succeeds. It asserts at least one finding and that no tar remains.
- [ ] **Step 2:** Run `go test ./internal/dockerexec/ -run 'Scan|Ensure|Cleanup'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the unit tests. Expected: PASS. Then run `RELIST_DOCKER_TESTS=1 go test ./internal/dockerexec/ -run Integration` once on the dev host. Expected: PASS. Record the digest re-check result in the commit body.
- [ ] **Step 5:** Commit: `feat: run trivy in a locked-down container over an exported image tar`.

### Task 5: `InUse` adapter and `DeployGate`

**Files:**
- Modify: `internal/vulnscan/ports.go`
- Create: `internal/dockerexec/inuse.go`
- Create: `internal/dockerexec/inuse_test.go`
- Modify: `internal/deployments/queue.go`: add `Busy()`.
- Modify: `internal/deployments/queue_test.go`

**Interfaces:**
- Produces in `vulnscan/ports.go`:

```go
type UserKind string
const (UserService UserKind = "service"; UserPanel = "panel"; UserTraefik = "traefik"; UserScanner = "scanner")
type ImageUser struct {
    Kind      UserKind
    ProjectID, EnvironmentID, ComposeID int64 // service only
    Service   string                          // service only
}
type UsedImage struct {
    ImageID string
    Refs    []string
    Users   []ImageUser
}
type InUse interface{ Images(ctx context.Context) ([]UsedImage, error) }
type DeployGate interface{ Busy() bool }
```

- Produces in `dockerexec`:

```go
type ComposeIdentities func(ctx context.Context) (map[string]vulnscan.ImageUser, error) // deploy project label -> IDs (Service empty)
func NewInUse(socketPath string, identities ComposeIdentities, hostname func() (string, error)) *InUse
```

  It reads `GET /containers/json` (running only), gets each container's `ImageID`, and classifies it per "Decisions made while planning". It reads `RepoTags` with `GET /images/{id}/json`, once per image. The Scanner image is not added here; the worker adds it.
- Produces in `deployments`: `func (q *Queue) Busy() bool`, which is true while a Deployment runs or `isHeld()`.

- [ ] **Step 1: Write failing tests:**
  - `TestInUseClassifies`: two Services share one image (one `UsedImage` with two users); the panel and Traefik are found via the hostname container's project label; an unknown container is ignored.
  - `TestInUseHostnameMissing`: the panel and Traefik are omitted and Services still work.
  - `TestQueueBusy`: held gives true; idle gives false; during a running fake Deployment, true.
- [ ] **Step 2:** Run `go test ./internal/dockerexec/ ./internal/deployments/ -run 'InUse|Busy'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: classify in-use images and expose deployment queue busy state`.

### Task 6: Worker and service operations

**Files:**
- Create: `internal/vulnscan/service.go`
- Create: `internal/vulnscan/worker.go`
- Create: `internal/vulnscan/worker_test.go`
- Create: `internal/vulnscan/fakes_test.go` (fake Scanner, InUse and DeployGate shared by the package tests)

**Interfaces:**
- Consumes: Tasks 1, 2, 4 and 5.
- Produces:

```go
type Deps struct {
    Store   *Store
    Scanner Scanner
    InUse   InUse
    Gate    DeployGate
    Now     func() time.Time
    Tick    time.Duration // gate re-check; 2*time.Second in production
}
type Service struct{ /* deps, wake chan struct{} */ }
func New(d Deps) *Service
func (s *Service) Run(ctx context.Context)                                     // Cleanup, Unstart, then the loop until ctx is done
func (s *Service) Enable(ctx context.Context) error                            // EnsureImage, then save Enabled=true, enqueue db_update + all images (ReasonSetup)
func (s *Service) Disable(ctx context.Context) error                           // save Enabled=false, ClearQueue
func (s *Service) SetScannerImage(ctx context.Context, ref string) error       // "" = default; EnsureImage(new) before saving; then RemoveImage(old) best-effort and enqueue the new image's scan
func (s *Service) ScanAll(ctx context.Context, reason Reason) (int, error)     // all InUse images + the Scanner image
func (s *Service) ScanImage(ctx context.Context, imageID string, reason Reason) error
func (s *Service) ScanCompose(ctx context.Context, composeID int64) error      // deploy hook body; no-op unless Enabled && AfterDeploy
func (s *Service) UsedImages(ctx context.Context) ([]UsedImage, error)         // InUse plus the Scanner image as UserScanner
```

- Worker loop:
  1. `Next`.
  2. While `Gate.Busy()`, wait `Tick` or for ctx.
  3. `Start`.
  4. Do the task:
     - `db_update`: `UpdateDB`, then `SetDB`. On error, `SetDB(.., err)`.
     - `image`: skip when `ScanState` shows an equal DB version with no error and the reason is not `manual`. Otherwise `DBInfo` (`ErrNoDB` gives `RecordFailure`), then `Scan`, then `RecordScan` or `RecordFailure`.
  5. `Done`.
  6. Wake on `Enqueue`. Back off for 5 s when the store itself fails.
- `UsedImages` adds the Scanner image as `UserScanner`, using `Scanner.ImageID`. If that fails, it is left out and logged.

- [ ] **Step 1: Write failing tests:**
  - `TestWorkerYieldsAndDedupes`: Busy is true for 3 ticks, the same image is enqueued 3 times, `Scan` is called once and only after Busy turns false.
  - `TestWorkerSkipsSameDB`: a manual scan does not skip.
  - `TestWorkerNoDB`: the row error is `vulnerability DB not downloaded yet`.
  - `TestWorkerImageGone`: the error is set and the next task still runs.
  - `TestWorkerRestartCleansAndRequeues`: a started row before `Run` means `Cleanup` is called before the first `Scan`, and the task runs once.
  - `TestEnableFailsKeepsOff`
  - `TestDisableClearsQueue`
  - `TestSetScannerImageFailureKeepsOld`
  - `TestScanComposeRespectsSwitches`
- [ ] **Step 2:** Run `go test ./internal/vulnscan/ -run 'Worker|Enable|Disable|SetScanner|ScanCompose'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/vulnscan/ ./internal/dockerexec/`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: add the vulnerability scan worker`.

### Task 7: Daily scheduler and deploy hook

**Files:**
- Create: `internal/vulnscan/scheduler.go`
- Create: `internal/vulnscan/scheduler_test.go`
- Modify: `internal/deployments/queue.go`: `Deps.OnSucceeded func(composeID int64)`, called as described under "Deploy hook placement".
- Modify: `internal/deployments/queue_test.go`

**Interfaces:**
- Produces:

```go
func (s *Service) RunScheduler(ctx context.Context)   // checks every minute via Now; see dueDaily
func dueDaily(now time.Time, st Settings) bool        // Enabled && Daily && now >= today(DailyMinute in now.Location()) && (LastDailyAt == nil || LastDailyAt < that moment)
func (s *Service) runDaily(ctx context.Context) error // enqueue db_update + ScanAll(ReasonDaily), PruneExcept(used + scanner), SetLastDaily(now)
func NextDaily(now time.Time, st Settings) *time.Time // for /status; nil unless Enabled && Daily
```

- [ ] **Step 1: Write failing tests:**
  - `TestDueDaily`: table cases are before the time, at the time, after it with the last run today, after it with the last run yesterday, daily off, master off, and a non-UTC `time.Location`.
  - `TestRunDailyPrunesAndEnqueues`
  - `TestNextDaily`
  - `TestOnSucceededCalledOnlyOnSuccess`: in deployments, a failed or canceled Deployment never calls it, and a succeeded one calls it once with the Compose ID.
- [ ] **Step 2:** Run `go test ./internal/vulnscan/ ./internal/deployments/ -run 'Daily|OnSucceeded'`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same command. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: schedule daily scans and scan after successful deployments`.

### Task 8: HTTP API and Docker Hub tag list

**Files:**
- Create: `internal/vulnscan/http.go`
- Create: `internal/vulnscan/hubtags.go`
- Create: `internal/vulnscan/http_test.go` (external package `vulnscan_test`, against `httptest`)
- Create: `internal/vulnscan/hubtags_test.go`

**Interfaces:**
- Consumes: `Service` (Tasks 6 and 7).
- Produces:

```go
func (s *Service) RegisterRoutes(mux *http.ServeMux)
func (s *Service) WithHub(baseURL string, client *http.Client) *Service // default https://hub.docker.com, 10 s timeout
type ScannerTag struct {
    Tag      string    `json:"tag"`
    Digest   string    `json:"digest"`
    PushedAt time.Time `json:"pushedAt"`
    AgeDays  int       `json:"ageDays"`
    Young    bool      `json:"young"`
}
func pickTags(raw []hubTag, now time.Time) []ScannerTag // semver only (vX.Y.Z or X.Y.Z, no pre-release), last 14 days newest first + newest older one
```

- Routes and shapes follow the spec's API table exactly, under `/api/vulnerabilities`.
- **Scope resolution:** `projectId` or `composeId` is turned into image IDs through `UsedImages`, by filtering the users. `services` in `/summary?composeId=` is `[{service, imageId, counts}]`.
- **`/status`** also returns `timeZone` (`time.Local.String()`) and `cacheBytes` (`Scanner.CacheSize`, `0` if the volume is missing).
- **Errors:** use the repo's JSON error shape (`{"error": "..."}`).
- **`PUT /settings`:**
  1. Validate first (`ValidScannerRef` or `""`, and `0 <= dailyMinute < 1440`, from `dailyTime` `"HH:MM"`).
  2. Apply the image change.
  3. Apply enable or disable.
  4. Save the switches.
  5. Run everything with a 2-minute context.

- [ ] **Step 1: Write failing tests:**
  - `TestGetSettingsDefaults`
  - `TestPutSettingsBadImageKeepsOld` (422)
  - `TestPutSettingsInvalidTime` (422)
  - `TestScansWhileOff` (409, copy verbatim)
  - `TestDeleteCacheWhileOn` (409)
  - `TestImagesListUsers`
  - `TestSummaryCompose`: the per-Service breakdown, and the CVE in two Services counts 1 in the total.
  - `TestSummaryOffHidesCounts`: `enabled:false`.
  - `TestCVEsQuery`
  - `TestStatusDBStale`
  - `TestPickTags`: filters `latest`, `canary` and `0.77.0-rc1`; the 14-day window plus one older tag; `young` flags.
  - `TestScannerTagsNonHub` (400)
  - `TestScannerTagsHubDown` (502)
- [ ] **Step 2:** Run `go test ./internal/vulnscan/`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/vulnscan/`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: add the vulnerabilities API`.

### Task 9: Wiring, ownership, migration skip, and docs

**Files:**
- Modify: `internal/httpapi/httpapi.go`:
  - `Deps` gains `VulnScanner vulnscan.Scanner` and `VulnInUse vulnscan.InUse`. Both nil disables the feature (fakes at the seam).
  - Construct `vulnscan.New` with `queue` as the `Gate`.
  - Pass `OnSucceeded: func(id int64) { go svc.ScanCompose(context.WithoutCancel(ctx), id) }`.
  - Start `svc.Run` and `svc.RunScheduler` in the same `worker` WaitGroup.
  - Register routes.
  - Extend `owners` with the effective Scanner image reference and `relist-trivy-cache` under `Vulnerability scanner`.
- Modify: `main.go`: build `dockerexec.NewTrivyScanner(dockerexec.DefaultSocketPath, cfg.DataDir)` and `dockerexec.NewInUse(...)`, with `ComposeIdentities` from the same query as `knownComposeProjects`, extended with IDs.
- Modify: `internal/migration/manifest.go`: add `".scan-work"` to `skipInDataDir`, plus a test in `manifest_test.go`.
- Modify: `internal/httpapi/*_test.go`: a seam test that `/api/vulnerabilities/settings` answers 200 with fakes, and 503 during the Migration lock (it is already gated because `allowedWhileLocked` excludes it; assert that).
- Create: `docs/adr/0021-vulnerability-scanning.md`: the spec's "Decisions and evidence", the rejected alternatives, and these measurements (dev host, 2026-10-27):

  | | Trivy 0.69.3 (socket) | Grype v0.121.0 (socket) | Trivy 0.76.1 (`--input`, locked down) |
  |---|---|---|---|
  | Scanner image | 245 MB | 129 MB | 263 MB |
  | DB on disk | 1.5 GB | 3.1 GB | 1.5 GB |
  | node:24 cold scan | 10.3 s / 664 MiB | 20.7 s / 2219 MiB | 3.65 s / 267 MiB (+4.5 s export, 428 MB tar) |
  | Warm rescan | 0.5–1.2 s | equal to cold | 0.27–1.1 s |
  | EPSS/KEV | no | yes | no |

  The `--input` scan matched the socket scan's findings on all 5 images. On node:24, Trivy's extra ~2,956 IDs were all `linux-libc-dev`.
- Modify: `CONTEXT.md`: **Image** exception, and new **Scanner**, **Vulnerability scan** and **Finding** entries, verbatim from the spec.

- [ ] **Step 1: Write failing tests:** `TestSkipInDataDirScanWork` and `TestVulnerabilitiesRoutesWired`/`TestVulnerabilitiesLocked` in httpapi.
- [ ] **Step 2:** Run `go test ./internal/httpapi/ ./internal/migration/`. Expected: FAIL.
- [ ] **Step 3:** Implement the wiring and write the docs.
- [ ] **Step 4:** Run `go test ./...` and `go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: wire vulnerability scanning into the panel` (the docs can go in the same commit, or a following `docs:` commit).

### Task 10: Frontend data layer and view model

**Files:**
- Modify: `web/src/data/types.ts`: the API shapes from Task 8, camelCase as returned.
- Modify: `web/src/data/api.ts`: add these to `api`:
  - `vulnSettings()`, `saveVulnSettings(body)`, `vulnStatus()`, `vulnScannerTags()`
  - `vulnScan(imageId?)`, `vulnDeleteCache()`
  - `vulnImages()`, `vulnFindings(imageId)`, `vulnCves(query)`, `vulnSummary(scope)`
- Create: `web/src/pages/vulnerabilities-view.ts`
- Create: `web/src/pages/vulnerabilities-view.test.ts`

**Interfaces:**
- Produces:

```ts
export type SummaryBadge = { critical: number; high: number; fixable: number } | null; // null = hide
export function summaryBadge(s: VulnSummary | undefined): SummaryBadge;            // null when !enabled or undefined
export function usedByLabel(u: VulnImageUser, names: NameLookup): string;          // "web (shop / prod / api)", "Panel", "Traefik", "Scanner (Trivy)"
export function dbFreshness(status: VulnStatus, now: Date): 'ok' | 'stale' | 'missing';
export function cveQueryFromSearch(params: URLSearchParams): VulnCveQuery;         // projectId/composeId/severity/fixable/new/q/page
export function scanRowState(r: VulnImage): 'ok' | 'never' | 'failed' | 'failedWithResults';
export function tagChoiceNeedsConfirm(t: ScannerTag): boolean;                     // t.young
export function parseDailyTime(v: string): number | null;                          // "03:00" -> 180
```

- [ ] **Step 1: Write failing tests** for each function, including:
  - `summaryBadge` hides when off;
  - all four `scanRowState` cases;
  - `dbFreshness` at the 7-day boundary;
  - `cveQueryFromSearch` ignores junk numbers.
- [ ] **Step 2:** Run `pnpm --dir web test vulnerabilities-view`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `pnpm --dir web test` and `pnpm --dir web typecheck`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: add vulnerabilities data layer and view model`.

### Task 11: Vulnerabilities page, settings drawer, nav and route

**Files:**
- Create: `web/src/pages/VulnerabilitiesPage.tsx`
- Create: `web/src/pages/VulnerabilitySettingsDrawer.tsx`
- Create: `web/src/pages/VulnerabilitiesPage.test.tsx`
- Modify: `web/src/app/paths.ts`: `vulnerabilities: '/vulnerabilities'` and a `routePaths` entry.
- Modify: `web/src/app/routes.tsx`: `'vulnerabilities': <VulnerabilitiesPage />`.
- Modify: `web/src/app/AppShell.tsx`: a nav item after `images`, key `vulnerabilities`, `SafetyOutlined`, with a red `Badge` of the host Critical count from `vulnSummary({})`. It is hidden when the badge is null or 0. Poll it on the same cadence the queue badge uses.

**Interfaces:**
- Consumes: Task 10.
- UI per the spec's UI section:
  - **Status bar:** an `Alert` or `Descriptions` row, with **Scan now** and **Settings**.
  - **Tabs:** `Images` (an expandable `Table`) and `CVEs` (server-paginated, with filters bound to the URL search params).
  - **Polling:** poll `/status` every 5 s while `running` or `queued > 0`, otherwise every 60 s.
- Drawer:
  - **Switches:** the master switch, and the two sub-switches (disabled while master is off). The `TimePicker` uses `format="HH:mm"` and shows `timeZone`.
  - **Scanner image:** the input, **Use default**, and **Pick from recent tags**, which opens a modal table. Picking a `young` tag asks for confirmation with the copy `This tag is less than 14 days old. Use it anyway?`
  - **Save:** the button shows a loading state while saving. It shows the `422` error inline, under the image field.
  - **Delete scanner cache:** disabled while on. The confirmation shows the volume size from `/status` `cacheBytes`.

- [ ] **Step 1: Write failing tests:**
  - `shows Scanning is off with a settings link`
  - `filters CVEs from the URL`
  - `expands an image to its findings`
  - `young tag asks for confirmation`
  - `save error shows under the image field`
  - `nav badge hidden when off`
- [ ] **Step 2:** Run `pnpm --dir web test VulnerabilitiesPage AppShell`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `pnpm --dir web test` and `pnpm --dir web typecheck`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: add the vulnerabilities page and scan settings`.

### Task 12: Badges on Overview, Project and Compose, and the embedded build

**Files:**
- Create: `web/src/components/VulnBadges.tsx`: renders a `SummaryBadge` as `Tag`s (`N Critical` red, `N High` orange, `N fixable` default) wrapped in a link. It renders nothing for `null`.
- Modify: `web/src/pages/OverviewPage.tsx`: a Vulnerabilities card next to the existing `StatCard`s, hidden when off.
- Modify: `web/src/pages/ProjectPage.tsx`: header badges, linking `?projectId=`.
- Modify: `web/src/pages/ComposePage.tsx`: header badges, plus a Vulnerabilities column in the Services table from `summary.services`, linking `?composeId=`.
- Modify: the tests of the three pages: assert the badges render and are absent when off.
- Modify: `internal/webassets/dist/*`, via `task build`.

- [ ] **Step 1: Write failing tests** in the existing page test files:
  - `renders vulnerability badges`
  - `hides vulnerability badges when scanning is off`
  - `compose services table shows per-service counts`
- [ ] **Step 2:** Run `pnpm --dir web test`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `pnpm --dir web test`, `pnpm --dir web typecheck` and `task test`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat: show vulnerability counts on overview, project and compose`. Then a separate commit: `chore: rebuild the embedded frontend`.

### Task 13: End-to-end check on the dev host

**Files:** none, unless a defect is found.

- [ ] **Step 1:** Run `task build`. Run the binary as in the README's "Local development", with a throwaway `RELIST_DATA_DIR` and a free port. **Do not touch the operator's live install container.** Then enable scanning in the drawer.
- [ ] **Step 2: Verify:**
  - the Scanner image is pulled;
  - `db_update` runs, then every in-use image plus the panel, Traefik and the Scanner image are scanned;
  - `docker ps` never shows a scanner container with the socket mounted (`docker inspect` the running `relist-vulnscan-*` while it runs: `HostConfig.NetworkMode == "none"`, `CapDrop == ["ALL"]`, `ReadonlyRootfs == true`);
  - `.scan-work` is empty afterwards.
- [ ] **Step 3:** Deploy a Compose. Verify its images are rescanned only after the Deployment finishes, and that the Compose page badges update.
- [ ] **Step 4:** Disable scanning. Verify the queue is cleared, badges hide, and the results page shows "scanning is off". Delete the cache and verify `relist-trivy-cache` is gone.
- [ ] **Step 5:** Mark the spec `Status: ready-for-human` for final review, and commit: `chore: close vulnerability scanning plan`.
