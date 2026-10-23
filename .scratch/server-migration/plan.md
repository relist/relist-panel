# Server migration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An empty panel on a new host pulls everything from an old panel over HTTPS, item by item, while the old panel sits under a persisted Migration lock. The Composes that were running come back up on the new host.

**Architecture:** One new package, `internal/migration`, holds both roles.
- **Source** is the old host: tokens and session, the lock sequence, the manifest, item streams and the `/api/migration/*` routes.
- **Target** is the new host: claim, preflight evaluation, the pull loop with `state.json`, restore per item kind, finalize, and the `/api/migration/pull/*` routes the setup UI uses.

Docker access goes through one port, `migration.Docker`, implemented in `internal/dockerexec/migration.go`. The Volume helper gains `pack`, `unpack`, `size` and `free`, backed by new `volfs` functions. Import direction: `migration → composes, deployments, domains, sources, volumes, volfs, database, config`. Nothing imports `migration` except `httpapi` and `main`.

**Tech Stack:** Go 1.25 stdlib (`net/http`, `archive/tar`, `crypto/sha256`, `crypto/rand`, `os/exec`, `syscall.Statfs`), goose, SQLite (modernc). React 19, antd 6.5.1, swr 2.5.1, vitest.

**Spec:** [.scratch/server-migration/spec.md](spec.md). ADR: [docs/adr/0018-server-migration.md](../../docs/adr/0018-server-migration.md).

## Global Constraints

- Commits: Conventional Commits, no scope. **No** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md). Commit backend, frontend and the rebuilt embedded assets (`chore: rebuild the embedded frontend with …`) separately, as earlier features did.
- **Never edit a committed migration.** All schema for this feature is in `internal/migrations/00018_server_migration.sql`, written once in Task 1. A later change needs `00019`.
- Domain words come from CONTEXT.md: Migration, Migration token, Migration lock, Migrated, Compose, Service, Volume, Image, Deployment. Never "backup", "restore" or "transfer" as the feature name in identifiers or copy. Function names like `restoreVolume` for one item kind are fine.
- Times are stored as Unix seconds in `INTEGER` columns, like `deployments.queued_at`.
- Token lifetimes: exactly `1h`, `24h` (default) and `168h`. Any other value gets `400`, `lifetime must be 1h, 24h or 168h`.
- Tokens and session keys are 32 bytes from `crypto/rand`, base64url without padding. Only their SHA-256 hex is stored.
- Claim rate limit: 5 consecutive wrong tokens make every `claim` return `429` for one minute. The count is global.
- Protocol version: `1`.
- Every external command runs with `exec.CommandContext` and an argv, never a shell. A value that reaches an argv must not start with `-`.
- Paths on the new host: `<data dir>/.migration/` (dir 0700), `.migration/state.json` (0600, it holds the session key), `.migration/staging/`, `.migration/finalize` (marker), `.migration/summary.json` (0600).
- Copy, verbatim:
  - Lock `503` body: `{"error":"This server is being migrated"}`
  - Migrated `503` body: `{"error":"This server was migrated to <url>"}`
  - Data dir mismatch: `RELIST_DATA_DIR differs: set it to <old value> on this host`
  - Not empty: `This panel already has data. Migrate into a freshly installed panel`
  - Schema: `This panel is older than the source. Upgrade it first`
  - Volume exists: `Volume <name> already exists on this host`
  - Host path busy: `<path> exists on this host and is not empty`
  - Space: `Not enough free space in <path>: need <n>, have <m>`
  - Protocol: `The two panels speak different migration protocols. Use the same panel version on both`
  - HTTP refused: `The source must use https://`
  - Resume confirm mismatch: `type RESUME to confirm`
  - Cancel reason for Deployments: `server migration`
  - Maintenance page title: `Down for maintenance`
- Before any edit under `web/`, read `https://ant.design/llms.txt` and the pages for `Steps`, `Progress`, `Table`, `Checkbox`, `Alert`, `Result`, `Form`, `Input`, `Segmented`, `Typography` (copyable) and `Modal`. Also read `docs/GUIDELINES.md`, and check props against antd 6.5.1 (AGENTS.md).

## Review Focus

1. **A Volume owned by a non-root uid, such as Postgres data at uid 999.** It must come back with the same numeric owner and modes. The Task 3 test `TestPackUnpackKeepsOwnershipAndModes` pins this.
2. **The old host restarting mid-lock.** Composes stay stopped, the maintenance container comes back, and nothing becomes editable. The Task 7 test `TestResumeLockAtBootRecreatesMaintenance` pins this.
3. **The new host restarting mid-pull.** Done items are not pulled again, and the half-written item is cleaned before its retry. The Task 10 test `TestTargetResumesFromStateFile` pins this.
4. **A tagged Image that has no `RepoDigests`, from a Source build.** It must arrive with its tag even when its Compose is not running. The Task 6 test `TestManifestIncludesBuiltImagesOfStoppedComposes` and the Task 11 test `TestRestoreImageLoadKeepsTags` pin this.
5. **Leftover journal files beside the empty `panel.db`.** They must never be replayed onto the copied database. The Task 12 test `TestPrepareFinalizeRemovesJournalsBeforeSwap` pins this.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/migrations/00018_server_migration.sql` | Schema |
| `internal/database/version.go` | `SchemaVersion`, `KnownVersion` |
| `internal/config/config.go` | `Install` settings from env |
| `internal/volfs/pack.go` | `Pack`, `Unpack`, `Size` with ownership and no caps |
| `cmd/relist-volhelper/main.go` | `pack`, `unpack`, `size`, `free` subcommands |
| `internal/dockerexec/migration.go` | `migration.Docker` adapter |
| `internal/deployments/queue.go`, `store.go` | `CancelAllQueued`, `WaitIdle`, `CancelUnfinished` |
| `internal/domains/store.go`, `labels.go` | `ListAll`, exported `Rule`, `EntrypointFor` |
| `internal/migration/store.go` | Tokens, session, lock state rows |
| `internal/migration/manifest.go` | Manifest types and the builder |
| `internal/migration/docker.go` | The `Docker` port and its value types |
| `internal/migration/lock.go` | Lock sequence, resume, middleware |
| `internal/migration/maintenance.go` | Maintenance labels and the `maintenance-page` server |
| `internal/migration/source_http.go` | Old host routes |
| `internal/migration/items.go` | Item streams and digests (old host) |
| `internal/migration/preflight.go` | `SourceInfo` (old host) and `Evaluate` (new host) |
| `internal/migration/target.go` | Pull loop, `state.json`, retry, abort |
| `internal/migration/restore.go` | Restore per item kind |
| `internal/migration/finalize.go` | Pre-open swap, post-open cleanup, Traefik, Deployments, finish |
| `internal/migration/target_http.go` | `/api/migration/pull/*` |
| `internal/migration/integration_test.go` | Both panels in one process |
| `web/src/data/migration.ts` | Types and API calls |
| `web/src/components/ServerMigrationCard.tsx` | Old host: token card on the Docker page |
| `web/src/pages/MigrationLockPage.tsx` | Old host: locked or Migrated full page |
| `web/src/pages/MigrationPage.tsx` | New host: Connect, Preview, Progress, Done |
| `scripts/probe-migration.sh`, `Taskfile.yml` | The `probe:migration` task |

---

### Task 1: Schema, schema version and install settings

**Files:**
- Create: `internal/migrations/00018_server_migration.sql`, `internal/database/version.go`, `internal/database/version_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `docker-compose.yml`

**Interfaces:**
- Produces:
  - `database.SchemaVersion(ctx context.Context, db *sql.DB) (int64, error)`: the highest applied `goose_db_version.version_id`.
  - `database.KnownVersion() (int64, error)`: the highest version among the embedded `migrations.FS` file names.
  - `config.InstallSettings{Domain, ACMEEmail, HTTPPort, HTTPSPort string}` and `Config.Install InstallSettings`, read from `RELIST_DOMAIN`, `RELIST_ACME_EMAIL`, `TRAEFIK_HTTP_PORT` and `TRAEFIK_HTTPS_PORT`. Empty means unknown.
  - Tables:

```sql
-- +goose Up
CREATE TABLE migration_token (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    session_hash TEXT,
    claimed_by TEXT,
    claimed_at INTEGER
);
CREATE TABLE migration_claim_failures (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    failures INTEGER NOT NULL,
    locked_until INTEGER
);
CREATE TABLE migration_lock (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    step TEXT NOT NULL,
    state TEXT NOT NULL,       -- JSON migration.LockState
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE migration_lock;
DROP TABLE migration_claim_failures;
DROP TABLE migration_token;
```

- [ ] **Step 1: Write failing tests.**
  - `TestKnownVersionIsHighestEmbeddedMigration`: asserts `18`.
  - `TestSchemaVersionAfterOpen`: `OpenAndMigrate` on a temp path gives `SchemaVersion == KnownVersion`.
  - `TestLoadInstallSettings`: sets the four env vars and asserts `Config.Install`.
- [ ] **Step 2: Run them.** Run `go test ./internal/database/ ./internal/config/`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** Write the migration and both functions. `KnownVersion` parses the leading digits of each `*.sql` name in `migrations.FS`. Add `RELIST_DOMAIN: ${RELIST_DOMAIN:-panel.localhost}`, `RELIST_ACME_EMAIL: ${RELIST_ACME_EMAIL:-}`, `TRAEFIK_HTTP_PORT: ${TRAEFIK_HTTP_PORT:-80}` and `TRAEFIK_HTTPS_PORT: ${TRAEFIK_HTTPS_PORT:-443}` to the panel service's `environment:` in `docker-compose.yml`.
- [ ] **Step 4: Run `go test ./...`.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add the server migration schema and install settings`

### Task 2: Tokens, session and lock rows

**Files:**
- Create: `internal/migration/store.go`, `internal/migration/store_test.go`

**Interfaces:**
- Consumes: the tables from Task 1.
- Produces:

```go
type Step string
const (
    StepNone        Step = ""
    StepLocked      Step = "locked"      // lock persisted, queue canceled
    StepStopping    Step = "stopping"    // waiting for the running Deployment, then stopping Composes
    StepMaintenance Step = "maintenance" // maintenance container started
    StepReady       Step = "ready"       // manifest built, items may stream
    StepMigrated    Step = "migrated"
)
type LockState struct {
    RunningComposeIDs []int64   `json:"runningComposeIds"`
    HostPaths         []string  `json:"hostPaths"`
    Manifest          *Manifest `json:"manifest,omitempty"`
    ClaimedBy         string    `json:"claimedBy"`
    TargetURL         string    `json:"targetUrl"`
    LockedAt          int64     `json:"lockedAt"`
    MigratedAt        int64     `json:"migratedAt,omitempty"`
}
var ErrBadLifetime, ErrInvalidToken, ErrAlreadyClaimed, ErrClaimLocked, ErrInvalidSession, ErrStarted error

func NewStore(db *sql.DB, now func() time.Time) *Store
func (s *Store) CreateToken(ctx context.Context, lifetime time.Duration) (token string, expiresAt time.Time, err error)
func (s *Store) RevokeToken(ctx context.Context) error
func (s *Store) Claim(ctx context.Context, token, claimedBy string) (sessionKey string, err error)
func (s *Store) VerifySession(ctx context.Context, sessionKey string) error
func (s *Store) Lock(ctx context.Context) (Step, LockState, error)          // StepNone when no row
func (s *Store) SetLock(ctx context.Context, step Step, st LockState) error // upsert
func (s *Store) ClearAll(ctx context.Context) error                         // deletes all three tables' rows
func (s *Store) TokenStatus(ctx context.Context) (expiresAt *time.Time, claimedBy string, err error)
```

`Manifest` is declared in Task 6. In this task, declare `type Manifest struct{}` in `manifest.go`. Task 6 fills it in.

- [ ] **Step 1: Write failing tests**, each using `database.OpenAndMigrate` on `t.TempDir()` with a fake clock.
  - `TestCreateTokenAcceptsOnlyThreeLifetimes`: `1h`, `24h` and `168h` succeed. `2h` and `169h` give `ErrBadLifetime`.
  - `TestCreateTokenRevokesThePreviousOne`: a second create makes the first `Claim` give `ErrInvalidToken`.
  - `TestTokenExpires`: the clock advances past expiry, then `Claim` gives `ErrInvalidToken`.
  - `TestClaimIsSingleUse`: the first claim returns a key. A second claim with the same token gives `ErrAlreadyClaimed`. `VerifySession(key)` is nil, and a wrong key gives `ErrInvalidSession`.
  - `TestClaimLocksAfterFiveWrongTokens`: five wrong claims, then a sixth with the right token gives `ErrClaimLocked`. 61 seconds later, the right token succeeds and the counter is reset.
  - `TestOnlyHashesAreStored`: reads `migration_token` raw and asserts neither the token nor the key appears.
  - `TestRevokeAfterStartIsRefused`: `SetLock(StepLocked, …)`, then `RevokeToken` gives `ErrStarted`.
  - `TestLockRoundTrips`: `SetLock` then `Lock` returns an equal `LockState`. `ClearAll` gives `StepNone`.
- [ ] **Step 2: Run `go test ./internal/migration/`.** Expected: FAIL.
- [ ] **Step 3: Implement.** Compare hashes with `subtle.ConstantTimeCompare`. A failed lookup and a wrong hash return the same `ErrInvalidToken`.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add migration tokens, sessions and lock state`

### Task 3: Pack and unpack with ownership in volfs and the helper

**Files:**
- Create: `internal/volfs/pack.go`, `internal/volfs/pack_test.go`
- Modify: `cmd/relist-volhelper/main.go`, `cmd/relist-volhelper/main_test.go`

**Interfaces:**
- Produces:
  - `func (fs *FS) Pack(name string, dst io.Writer) error`: an uncompressed tar of `name` (`.` for the whole root). It keeps numeric `Uid`/`Gid`, the full mode including setuid/setgid/sticky, mtime, symlinks (as links, never followed) and empty dirs. Entry names are relative to `name`'s parent, so a file `name` packs as one entry.
  - `func (fs *FS) Unpack(src io.Reader) error`: extracts into the root with no entry or byte cap. It applies `Lchown`, the mode and mtime. Absolute names, `..` and any link whose target resolves outside the root are refused with `ErrArchiveRefused`, using the existing error the helper maps to exit 4. Device nodes and FIFOs are refused.
  - `func (fs *FS) Size(name string) (int64, error)`: the sum of regular file sizes under `name`.
  - Helper argv:
    - `relist-volhelper pack -- <name>` writes the tar to stdout.
    - `relist-volhelper unpack -- .` reads it from stdin.
    - `relist-volhelper size -- <name>` and `relist-volhelper free -- .` print `{"bytes":N}`.

- [ ] **Step 1: Write failing tests.** They run as root where possible, using `t.Skip` when `os.Geteuid() != 0` for the chown assertions.
  - `TestPackUnpackKeepsOwnershipAndModes`: files owned `999:999` with mode `0700`, a setgid dir and a relative symlink round-trip identically into a second root.
  - `TestPackHasNoSizeCap`: a 5 GiB sparse file round-trips. Use `t.Skip` under `-short`.
  - `TestUnpackRefusesEscapes`: `../x`, `/etc/x` and a symlink `a -> ../../etc` followed by entry `a/passwd` each give `ErrArchiveRefused`, and nothing is written outside the root.
  - `TestPackSingleFile`: `Pack("conf.yml")` gives one entry `conf.yml`.
  - Helper: `TestRunPackUnpackSizeFree` drives `run` with the four argvs and asserts the exit codes and the JSON shape.
- [ ] **Step 2: Run `go test ./internal/volfs/ ./cmd/relist-volhelper/`.** Expected: FAIL.
- [ ] **Step 3: Implement.** Do it through the existing `os.Root`. Use `root.Lchown` and `root.Chmod`. Use `os.Chtimes` for files and `unix.Lutimes` for symlinks. `free` uses the existing `FreeBytes`.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add ownership-keeping pack and unpack to the volume helper`

### Task 4: The Docker port and its adapter

**Files:**
- Create: `internal/migration/docker.go`, `internal/dockerexec/migration.go`, `internal/dockerexec/migration_test.go`, `internal/migration/fakedocker_test.go`

**Interfaces:**
- Produces, in `internal/migration/docker.go`:

```go
type VolumeInfo struct {
    Name, Driver string
    Labels, Options map[string]string
}
type ImageInfo struct {
    ID          string
    Tags        []string
    RepoDigests []string
    Size        int64
}
type Docker interface {
    ComposeStop(ctx context.Context, req composes.RunRequest) error  // docker compose -p … -f … --env-file … stop
    ComposeStart(ctx context.Context, req composes.RunRequest) error // … start
    ProjectContainerImages(ctx context.Context, project string) ([]string, error) // image IDs of the project's containers, any state
    InspectVolume(ctx context.Context, name string) (VolumeInfo, bool, error)
    CreateVolume(ctx context.Context, v VolumeInfo) error
    RemoveVolume(ctx context.Context, name string) error
    InspectImage(ctx context.Context, ref string) (ImageInfo, bool, error)
    PullImage(ctx context.Context, ref string) error
    TagImage(ctx context.Context, source, target string) error
    SaveImages(ctx context.Context, refs []string, w io.Writer) error
    LoadImages(ctx context.Context, r io.Reader) error
    RemoveImage(ctx context.Context, ref string) error
    // Helper runs out of the panel's image. mountSource is a volume name or an
    // absolute host path, mounted at /data. Pack mounts it read-only.
    Pack(ctx context.Context, mountSource, name string, w io.Writer) error
    Unpack(ctx context.Context, mountSource string, r io.Reader) error
    Size(ctx context.Context, mountSource, name string) (int64, error)
    Free(ctx context.Context, mountSource string) (int64, error)
    DockerRootDir(ctx context.Context) (string, error)
    RunMaintenance(ctx context.Context, labels map[string]string) error // replaces any existing one
    RemoveMaintenance(ctx context.Context) error                       // no error when absent
    MaintenanceRunning(ctx context.Context) (bool, error)
    StopTraefik(ctx context.Context) error
    StartTraefik(ctx context.Context) error
}
```

- Produces, in `dockerexec`: `NewMigrationDocker(socketPath string) *MigrationDocker`, which satisfies `migration.Docker`.
- Produces, in `fakedocker_test.go`: `type fakeDocker` in package `migration`. It keeps in-memory volumes, images, host paths and calls, and later tasks' tests use it.

The helper argv differs from `volumeArgv`:
- pack, size and free: `docker run --rm -i --network none --user 0:0 --read-only --tmpfs /tmp --cap-drop ALL --cap-add DAC_READ_SEARCH --security-opt no-new-privileges -v <src>:/data:ro --entrypoint /usr/local/bin/relist-volhelper <image> <op> -- <name>`
- unpack: the same without `:ro`, with `--cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER` replacing `DAC_READ_SEARCH`.

Maintenance container: `docker run -d --name relist-maintenance --restart unless-stopped --network relist-network --label relist.maintenance=1 --label k=v … --entrypoint relist-panel <image> maintenance-page`. The panel image comes from the existing `panelImage`. Traefik is found with `docker ps -a --filter label=com.docker.compose.project=<panel's project> --filter label=com.docker.compose.service=traefik`, where the panel's project is read from `docker inspect $(hostname)`.

- [ ] **Step 1: Write failing argv tests** in `dockerexec/migration_test.go`, in the style of `volume_runner_test.go`: `TestPackArgvIsReadOnlyWithDacReadSearch`, `TestUnpackArgvAddsChownCaps`, `TestMaintenanceArgvCarriesLabels`, `TestComposeStopArgv`. Each asserts the whole argv slice.
- [ ] **Step 2: Run `go test ./internal/dockerexec/`.** Expected: FAIL.
- [ ] **Step 3: Implement** the adapter. Volumes, images, save/load and pull go through the Engine API on the socket, as `images.go` does. Compose and helper runs go through `execDocker`.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add the docker adapter for server migration`

### Task 5: Queue and Domain hooks

**Files:**
- Modify: `internal/deployments/queue.go`, `internal/deployments/store.go`, `internal/deployments/queue_test.go`, `internal/domains/store.go`, `internal/domains/labels.go`, `internal/domains/labels_test.go`

**Interfaces:**
- Produces:
  - `func (q *Queue) CancelAllQueued(ctx context.Context, reason string) (int, error)`: every queued Deployment of every Compose becomes `canceled` with `reason`, and its snapshot is discarded as in `CancelForComposes`.
  - `func (q *Queue) WaitIdle(ctx context.Context) error`: returns once no Deployment is running.
  - `func (q *Queue) Running() (composeID int64, number int, ok bool)`
  - `func (s *Store) CancelUnfinished(ctx context.Context, reason string, now time.Time) (int, error)`: queued and running become `canceled`. It runs on the copied database before the worker starts.
  - `func (s *Store) ListAll(ctx context.Context) ([]Domain, error)` in `domains`, ordered by `id`.
  - `func Rule(d Domain) string` and `func EntrypointFor(d Domain) string` in `domains`, extracted from the existing label builder without changing its output.
- [ ] **Step 1: Write failing tests.**
  - `TestCancelAllQueuedCancelsEveryCompose`: queued Deployments on two Composes are both `canceled` with reason `server migration`.
  - `TestWaitIdleReturnsAfterTheRunningDeployment`: uses the existing fake runner blocked in Build.
  - `TestCancelUnfinished`: queued and running rows become canceled.
  - `TestRuleAndEntrypointMatchGeneratedLabels`: for the existing label test cases, `Rule(d)` and `EntrypointFor(d)` equal the generated router's rule and entrypoints.
- [ ] **Step 2: Run `go test ./internal/deployments/ ./internal/domains/`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...`.** Expected: PASS, with the existing label tests unchanged.
- [ ] **Step 5: Commit.** `feat: let a migration cancel and drain the deployment queue`

### Task 6: The manifest builder

**Files:**
- Modify: `internal/migration/manifest.go`
- Create: `internal/migration/manifest_test.go`

**Interfaces:**
- Consumes: `Docker` (Task 4), `volumes.Resolver.Resolve` and `ListOrphans` with `ComposeRef{AllProfiles: true}`, `composes.Service.Target`.
- Produces:

```go
const ProtocolVersion = 1
type ItemKind string
const (
    KindDatabase ItemKind = "database"
    KindDataDir  ItemKind = "data-dir"
    KindVolume   ItemKind = "volume"
    KindHostPath ItemKind = "host-path"
    KindImage    ItemKind = "image"
)
type Item struct {
    ID       string      `json:"id"` // "database", "data-dir", "volume-<name>", "host-path-<12 hex of sha256(path)>", "image-<12 hex of the image id>"
    Kind     ItemKind    `json:"kind"`
    Size     int64       `json:"size"`
    Volume   *VolumeInfo `json:"volume,omitempty"`
    HostPath string      `json:"hostPath,omitempty"`
    IsFile   bool        `json:"isFile,omitempty"`
    Image    *ImageInfo  `json:"image,omitempty"`
}
type HostPathCandidate struct {
    Path     string `json:"path"`
    IsFile   bool   `json:"isFile"`
    System   bool   `json:"system"` // unticked by default
    Size     int64  `json:"size"`
    Composes []string `json:"composes"`
}
type Manifest struct {
    Protocol          int      `json:"protocol"`
    SchemaVersion     int64    `json:"schemaVersion"`
    DataDir           string   `json:"dataDir"`
    RunningComposeIDs []int64  `json:"runningComposeIds"`
    Items             []Item   `json:"items"`
    Warnings          []string `json:"warnings"`
}
type ComposeTargets interface {
    Target(ctx context.Context, composeID int64) (composes.DeployTarget, error)
}
type Builder struct {
    DB *sql.DB; Composes ComposeTargets; Volumes *volumes.Resolver; Docker Docker; DataDir string
}
func (b Builder) HostPathCandidates(ctx context.Context) ([]HostPathCandidate, []string, error) // candidates, warnings
func (b Builder) Build(ctx context.Context, running []int64, hostPaths []string) (Manifest, error)
func IsSystemPath(path, dataDir string) bool
```

Rules, copied from the spec:
- **Volumes** are every named Volume of every Compose resolved with every profile, plus its Orphan Volumes, deduplicated by name. A Volume whose driver is not `local`, or that has `Options`, becomes the warning `Volume <name> uses driver <d> and is not moved` and gets no item.
- **Host paths** are every `VolumeBind` source outside `DataDir`. `IsSystemPath` is true for anything under `/etc`, `/proc`, `/sys`, `/dev`, `/run`, `/var/run` or `/var/lib/docker`, for the data directory, and for a path whose name ends in `.sock`. Only paths passed in `hostPaths` become items. A path that is not a candidate is an error.
- **Images** are the union of:
  - every Service's `image:` from `docker compose config` (all profiles);
  - `<project>-<service>` for each Service with `build:` and no `image:`;
  - `ProjectContainerImages` for each Compose.

  Deduplicate by `ImageInfo.ID`. A name that `InspectImage` cannot find becomes the warning `Image <ref> is not on this host; its Compose will pull or build it on deploy`.
- **Never-deployed Composes:** a Compose whose resolved compose file (`DeployTarget.Request.ComposeFilePath`) does not exist is skipped for volume, host path and image resolution, the same check `composes.ServiceNames` uses. It adds the warning `<compose> was never deployed; only its files and database rows move`. Any other resolution error fails `Build`. `Source.Info` (Task 8) runs the same resolution, so that error appears in preflight before anything is locked.
- **Items are ordered** `database`, `data-dir`, volumes, host paths, then images.
- **Hand-written routers**: a Compose whose stored YAML has `traefik.http.routers.` labels (`domains.HasTraefikLabels`) adds the warning `<compose>: hand-written Traefik routers get no maintenance page`.

The images' `docker compose config` output comes from the same `ComposeConfig` call `volumes.Resolver` uses. Add `func (r *Resolver) Images(ctx context.Context, ref ComposeRef) (images []string, built []string, err error)` in `internal/volumes/resolve.go`, which parses `services.*.image` and `services.*.build`.

- [ ] **Step 1: Write failing tests** with `fakeDocker` and a fake `volumes.CLI`.
  - `TestManifestVolumesIncludeProfilesExternalAndOrphans`
  - `TestManifestSkipsNonLocalDriversWithWarning`
  - `TestHostPathCandidatesMarkSystemPaths`: `/etc/localtime`, `/var/run/docker.sock` and `/srv/x.sock` are system paths. `/srv/uploads` is not.
  - `TestManifestIncludesBuiltImagesOfStoppedComposes`: a Compose with `build:` and no Containers yields an image item for `relist-c7-api`, keeping its tag and an empty `RepoDigests`.
  - `TestManifestDedupesImagesByID`
  - `TestManifestItemOrder`
  - `TestManifestRefusesUnknownHostPath`
  - `TestManifestSkipsNeverDeployedCompose`: no compose file on disk, so the Compose is skipped with the warning, and `Build` succeeds.
- [ ] **Step 2: Run `go test ./internal/migration/ ./internal/volumes/`.** Expected: FAIL.
- [ ] **Step 3: Implement.** List Compose IDs with `SELECT id FROM composes ORDER BY id`. Size the data dir by walking it in process, skipping `panel.db*` and `.migration`.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: build the server migration manifest`

### Task 7: The lock sequence, Resume and the lock middleware

**Files:**
- Create: `internal/migration/lock.go`, `internal/migration/lock_test.go`, `internal/migration/maintenance.go`, `internal/migration/maintenance_test.go`
- Modify: `main.go` (the `maintenance-page` subcommand)

**Interfaces:**
- Consumes: `Store` (Task 2), `Docker` (Task 4), `Queue` hooks and `domains.ListAll`/`Rule`/`EntrypointFor` (Task 5), `Builder` (Task 6), `composes.ComposeRunner.Status`.
- Produces:

```go
type QueueControl interface {
    CancelAllQueued(ctx context.Context, reason string) (int, error)
    WaitIdle(ctx context.Context) error
    Running() (composeID int64, number int, ok bool)
}
type SourceDeps struct {
    Store    *Store
    Builder  Builder
    Queue    QueueControl
    Docker   Docker
    Runner   composes.ComposeRunner // Status
    Composes ComposeTargets
    Domains  *domains.Store
    Now      func() time.Time
}
func NewSource(d SourceDeps) *Source
func (s *Source) Start(ctx context.Context, hostPaths []string) (Manifest, error) // idempotent, returns the stored manifest once ready
func (s *Source) ResumeLock(ctx context.Context) error                           // at boot: continues from the stored step
func (s *Source) Resume(ctx context.Context) error                               // Cancel migration / Resume this server
func (s *Source) Finish(ctx context.Context, targetURL string) error
func (s *Source) Middleware(next http.Handler) http.Handler
func MaintenanceLabels(ds []domains.Domain) map[string]string
func ServeMaintenancePage(ctx context.Context, addr string) error // ":8080", answers 503 with the page
```

`Start` runs these steps, persisting the step **before** acting so `ResumeLock` can redo it:
1. `StepLocked`: `Queue.CancelAllQueued(ctx, "server migration")`.
2. `StepStopping`: `Queue.WaitIdle`. Then, for each Compose, a `Runner.Status` with `Running > 0` adds its ID to `RunningComposeIDs`. The IDs are saved first, then `Docker.ComposeStop` runs on each.
3. `StepMaintenance`: `Docker.RunMaintenance(MaintenanceLabels(domains.ListAll()))`. No Domains means no container.
4. `StepReady`: `Builder.Build(running, hostPaths)` is stored in `LockState.Manifest`.

`ResumeLock` at `StepLocked` or `StepStopping` reruns from step 1, because each step is idempotent. At `StepMaintenance` or later, it recreates the maintenance container when `MaintenanceRunning` is false and stops any Compose in `RunningComposeIDs` that has started again. At `StepMigrated`, it does the same.

`Resume` runs `RemoveMaintenance`, then `ComposeStart` for each ID in `RunningComposeIDs`, then `Store.ClearAll`.

`MaintenanceLabels` uses router `relist-maint-d<domainID>` with `Rule(d)` and `EntrypointFor(d)`. When `d.HTTPS` is set it adds `tls=true` and the same certresolver as `labels.go`. When `d.HTTPS && d.Entrypoint == ""` it also adds router `relist-maint-d<id>-http` on `web`, with no redirect. It sets `traefik.enable=true` and `traefik.docker.network=relist-network`, and points every router at service `relist-maint` on port 8080.

The `Middleware` allowlist is `/healthz`, `/api/auth/status`, `/api/auth/login`, `/api/auth/logout`, every `/api/migration/` path and every non-`/api` path (the SPA shell must load to render the lock page). Everything else gets `503` with the copy from Global Constraints. Terminal Sessions end because their Containers stop, and new ones are refused by this middleware.

- [ ] **Step 1: Write failing tests.**
  - `TestStartRecordsRunningThenStops`: only the Composes with running Containers are in `RunningComposeIDs`, and `ComposeStop` is called for each.
  - `TestStartCancelsQueuedAndWaitsForRunning`
  - `TestStartIsIdempotent`: a second `Start` returns the same manifest with no new stops.
  - `TestResumeLockAtBootRecreatesMaintenance`: at `StepReady`, with the maintenance container gone, `ResumeLock` runs it again and nothing is started.
  - `TestResumeStartsWhatWasRunningAndClears`
  - `TestMiddlewareAllowlist`: a table of paths and methods with their expected status under `StepLocked`. `StepMigrated` gives the Migrated body with the URL.
  - `TestMaintenanceLabelsHTTPSDomain`: exact label map for an HTTPS letsencrypt Domain with path `/api`.
  - `TestMaintenancePageAnswers503`: `httptest` against the handler that `ServeMaintenancePage` uses; the body contains `Down for maintenance`.
- [ ] **Step 2: Run `go test ./internal/migration/`.** Expected: FAIL.
- [ ] **Step 3: Implement.** Add `if len(os.Args) > 1 && os.Args[1] == "maintenance-page"` to `main.go` next to `reset-2fa`. It calls `migration.ServeMaintenancePage(ctx, ":8080")`.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: lock the old host for a migration with a maintenance page`

### Task 8: Old host routes and item streams

**Files:**
- Create: `internal/migration/source_http.go`, `internal/migration/items.go`, `internal/migration/preflight.go`, `internal/migration/source_http_test.go`
- Modify: `internal/auth/auth.go` (`publicPath`), `internal/httpapi/httpapi.go`, `main.go`

**Interfaces:**
- Consumes: Tasks 2, 4, 6 and 7, `database.SchemaVersion`, `config.Install`.
- Produces:

```go
type SourceInfo struct {
    Protocol      int                   `json:"protocol"`
    SchemaVersion int64                 `json:"schemaVersion"`
    DataDir       string                `json:"dataDir"`
    Install       config.InstallSettings `json:"install"`
    HostPaths     []HostPathCandidate   `json:"hostPaths"`
    Volumes       []Item                `json:"volumes"` // kind volume, with size
    Images        []Item                `json:"images"`
    DataDirSize   int64                 `json:"dataDirSize"`
    DatabaseSize  int64                 `json:"databaseSize"`
    ActiveDeployment string             `json:"activeDeployment,omitempty"` // "<compose> #<n>"
    Warnings      []string              `json:"warnings"`
}
// SourceDeps (Task 7) gains: DB *sql.DB; DataDir string; ReportedDataDir string
// (cfg.DataDir in production, overridden only by the Task 14 test); Install config.InstallSettings.
func (s *Source) Info(ctx context.Context) (SourceInfo, error)
func (s *Source) StreamItem(ctx context.Context, id string, w io.Writer) error // only at StepReady or StepMigrated
func (s *Source) Digest(id string) (string, bool)                            // hex sha256 of the last complete stream
func (s *Source) RegisterRoutes(mux *http.ServeMux)
```

Routes (session = `Authorization: Bearer <key>` checked with `Store.VerifySession`; admin = the normal cookie):

| Route | Auth | Response |
|---|---|---|
| `POST /api/migration/claim` `{"token","claimedBy"}` | none | `200 {"sessionKey"}`; `401` invalid; `409` claimed; `429` locked |
| `GET /api/migration/preflight` | session | `SourceInfo` |
| `POST /api/migration/start` `{"hostPaths":[]}` | session | `200 Manifest` once ready; `202 {"step"}` while waiting |
| `GET /api/migration/items/{id}` | session | `application/octet-stream` stream |
| `GET /api/migration/items/{id}/digest` | session | `{"sha256"}`; `404` before a complete stream |
| `POST /api/migration/abort` | session | `204`, same as `Resume` |
| `POST /api/migration/finish` `{"targetUrl"}` | session | `204` |
| `POST /api/migration/tokens` `{"lifetime":"24h"}` | admin | `201 {"token","expiresAt"}` |
| `DELETE /api/migration/tokens/current` | admin | `204`; `409` after start |
| `GET /api/migration/status` | admin | `{"step","claimedBy","targetUrl","lockedAt","migratedAt","tokenExpiresAt"}` |
| `POST /api/migration/resume` `{"confirm":"RESUME"}` | admin | `204`; `400 type RESUME to confirm` |

`Start` runs in a goroutine detached from the request (`context.WithoutCancel`). The route returns `202` until `StepReady`, and the client polls. Item streams by kind:
- `database`: `VACUUM INTO '<dataDir>/.migration-out/panel.db'`, stream the file, then delete it.
- `data-dir`: an in-process `volfs.Open(dataDir)` with `Pack(".")`, skipping `panel.db`, `panel.db-journal`, `panel.db-wal`, `panel.db-shm`, `.migration` and `.migration-out`. Add an exclude option: `func (fs *FS) PackExcluding(name string, skip func(rel string) bool, dst io.Writer) error` in `volfs/pack.go`.
- `volume`: `Docker.Pack(name, ".")`.
- `host-path`: `Docker.Pack(parent, base)`.
- `image`: `Docker.SaveImages(Tags)`, or `[ID]` when there are no tags.

`publicPath` gains `/api/migration/claim`, `/api/migration/preflight`, `/api/migration/start`, `/api/migration/abort`, `/api/migration/finish` and the prefix `/api/migration/items/`. Those handlers check the session themselves. Also add the prefix `/api/migration/pull/` for Task 13. In `httpapi.NewHandler`, wrap as `authService.Middleware(source.Middleware(gate.middleware(mux)))`. `main.go` calls `source.ResumeLock` in the background after the handler is built, and logs failures.

- [ ] **Step 1: Write failing tests** through `httptest` with `fakeDocker`.
  - `TestClaimRoute`, covering `200`/`401`/`409`/`429`.
  - `TestSessionRoutesRejectMissingOrWrongBearer`
  - `TestStartReturns202ThenManifest`
  - `TestItemStreamDigestMatchesBody`: reads the body, computes SHA-256 and compares it with `/digest`.
  - `TestItemStreamRefusedBeforeReady`
  - `TestDataDirItemSkipsDatabaseAndMigrationDirs`
  - `TestResumeRequiresConfirm`
  - `TestTokensRoutesRequireAdmin`
- [ ] **Step 2: Run `go test ./internal/migration/ ./internal/httpapi/`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...`.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: serve the migration source api and item streams`

### Task 9: Preflight evaluation on the new host

**Files:**
- Modify: `internal/migration/preflight.go`
- Create: `internal/migration/preflight_test.go`

**Interfaces:**
- Consumes: `SourceInfo` (Task 8), `Docker` (Task 4).
- Produces:

```go
type Check struct {
    Blocking bool   `json:"blocking"`
    Message  string `json:"message"`
}
type LocalHost struct {
    DB *sql.DB; Docker Docker; DataDir string; Install config.InstallSettings
}
func Evaluate(ctx context.Context, local LocalHost, src SourceInfo, selected []string) ([]Check, error)
```

Blocking checks, using the Global Constraints copy:
- protocol mismatch;
- the local `KnownVersion` is lower than `src.SchemaVersion`;
- `DataDir` differs;
- the panel is not empty (an `admin_credentials` row or a `projects` row);
- a Volume exists (`InspectVolume`);
- a selected host path exists and is not an empty dir (`Docker.Size` on the parent, or `os.Stat` when the path is under the data dir);
- free space: `statfs` on `DataDir` needs `(DataDirSize + DatabaseSize) * 1.1`, and `Docker.Free(DockerRootDir)` needs `(sum of volumes + images) * 1.1`. Host paths are checked against `Free` of their parent.

Warnings:
- each differing non-empty `Install` field, as `<ENV> is <old> on the source and <new> here; copy it into this host's .env`;
- every `src.Warnings` entry;
- `ActiveDeployment` set, as `<it> is running on the source; queued Deployments there will be canceled`.

- [ ] **Step 1: Write failing tests,** one table-driven test, `TestEvaluate`, with one row per check. Each row asserts the exact message and `Blocking`.
- [ ] **Step 2: Run `go test ./internal/migration/ -run TestEvaluate`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the test.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: evaluate migration preflight on the new host`

### Task 10: The pull loop with state.json, retry and abort

**Files:**
- Create: `internal/migration/target.go`, `internal/migration/client.go`, `internal/migration/target_test.go`

**Interfaces:**
- Consumes: Tasks 8 and 9. `restore` is a seam filled by Task 11.
- Produces:

```go
type Phase string
const (
    PhaseIdle       Phase = ""
    PhaseConnected  Phase = "connected"  // claimed, preflight cached
    PhaseLocking    Phase = "locking"    // start sent, polling 202
    PhasePulling    Phase = "pulling"
    PhasePaused     Phase = "paused"     // an item failed 3 times
    PhaseFinalizing Phase = "finalizing"
    PhaseDone       Phase = "done"
)
type ItemProgress struct {
    ID string; Kind ItemKind; Size, Done int64; State string; Attempts int; Error string // State: pending|running|done|failed
}
type Progress struct {
    Phase Phase; SourceURL string; LockStep Step; Items []ItemProgress; Checks []Check; Info *SourceInfo; Error string
}
type TargetDeps struct {
    Local     LocalHost
    HTTP      *http.Client
    Insecure  bool             // --insecure-migration-source
    SelfURL   func() string    // the URL the operator reaches this panel on, from the request's Host
    Restore   Restorer         // Task 11
    Finalizer Finalizer        // Task 12
    Backoff   func(attempt int) time.Duration // 2s, 8s, 30s in production
}
type Restorer interface {
    Restore(ctx context.Context, item Item, body io.Reader) error // writes the item
    Cleanup(ctx context.Context, item Item) error                 // removes what a failed attempt wrote
    Discard(ctx context.Context, items []Item) error              // abort: removes everything restored
}
type Finalizer interface { Begin(ctx context.Context, m Manifest) error } // Task 12: stage, marker, exit
func NewTarget(d TargetDeps) *Target
func (t *Target) Connect(ctx context.Context, sourceURL, token string) (Progress, error)
func (t *Target) Recheck(ctx context.Context, selected []string) (Progress, error)
func (t *Target) Start(ctx context.Context, selected []string) error // refuses while any blocking Check exists
func (t *Target) Retry(ctx context.Context) error
func (t *Target) Abort(ctx context.Context) error
func (t *Target) Progress() Progress
func (t *Target) ResumeIfPending(ctx context.Context) error // boot: loads state.json, continues
```

`state.json` holds `{phase, sourceUrl, sessionKey, selected, manifest, items:{id:{state,attempts,error}}}`. It is written atomically (temp file and rename) after every state change, with mode 0600.

For each item, the loop runs:
1. Mark it `running` and call `Cleanup`.
2. `GET items/{id}` through a `sha256` tee into `Restore`.
3. `GET items/{id}/digest` and compare.
4. Mark it `done`.

On any error it calls `Cleanup`, increments `attempts`, and sleeps `Backoff(attempts)`. After 3 failed attempts it moves to `PhasePaused`. `Retry` resets `attempts` of failed items and continues. `Abort` calls `POST abort` on the source, which is best effort, then `Discard` and removes `.migration/`. After every item it calls `Finalizer.Begin`.

`Connect` refuses a non-`https://` URL unless `Insecure` is set (`The source must use https://`) and sends `claimedBy = SelfURL()`. `Images` are restored through the Task 11 image path, which may skip the stream.

- [ ] **Step 1: Write failing tests,** each with an `httptest` source that serves canned items and a recording fake `Restorer`.
  - `TestConnectRefusesHTTP`
  - `TestStartRefusedWithBlockingCheck`
  - `TestPullRetriesThenSucceeds`: the item body is cut on the first attempt. `Cleanup` is called, and the second attempt is `done`.
  - `TestDigestMismatchRetries`
  - `TestPausesAfterThreeFailures`, with `Retry` then continuing.
  - `TestTargetResumesFromStateFile`: stop after item 2 of 4. A new `Target` with `ResumeIfPending` pulls only items 3 and 4 and calls `Cleanup` on the one marked `running`.
  - `TestAbortCallsSourceAndDiscards`
  - `TestStateFileMode0600`
- [ ] **Step 2: Run `go test ./internal/migration/ -run 'Test(Connect|Start|Pull|Digest|Pauses|Target|Abort|State)'`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: pull migration items with resume, retry and abort`

### Task 11: Restore per item kind

**Files:**
- Create: `internal/migration/restore.go`, `internal/migration/restore_test.go`

**Interfaces:**
- Consumes: `Docker` (Task 4), `volfs.Unpack` (Task 3), the source client from Task 10 for the image fallback.
- Produces: `func NewRestorer(local LocalHost, fetch func(ctx context.Context, id string) (io.ReadCloser, func() (string, error), error)) Restorer`, where `fetch` returns the body and a digest getter. It also implements `ImageRestorer`:

```go
type ImageRestorer interface {
    RestoreImage(ctx context.Context, item Item) (warning string, err error) // pull or load, then verify the ID
}
```

When `Restore` sees `KindImage`, the loop in Task 10 calls `RestoreImage` instead of `GET items/{id}`. `RestoreImage` uses `fetch` only on fallback.

Per kind:
- `database`: write to `.migration/staging/panel.db`.
- `data-dir`: `volfs.Open(.migration/staging/data)` and `Unpack`. `Cleanup` removes that directory.
- `volume`: `CreateVolume(item.Volume)` (same name, driver and labels), then `Unpack(name)`. `Cleanup` and `Discard` run `RemoveVolume`.
- `host-path`: run `Docker.Unpack(parent, body)`. Docker creates a missing bind source directory, and the stream's single top entry is the base name. `Cleanup` and `Discard` remove the base through the existing helper op `delete -- <base>`, run against the parent.
- `image`:
  1. For each `RepoDigests` entry `repo@sha`, run `PullImage`, then `TagImage` for every tag.
  2. If there are no digests or every pull failed, `fetch` the stream into `LoadImages`.
  3. An untagged Image is tagged `relist-migrated/<12 hex>` after load.
  4. Compare `InspectImage` of the first tag (or the ID) with `item.Image.ID`. A pulled Image that differs falls back to step 2. A loaded one that differs returns the warning `Image <ref> has a different ID on this host (image store differs); content was loaded from the source`.
  5. `Discard` runs `RemoveImage` on the tags it created.

- [ ] **Step 1: Write failing tests** with `fakeDocker`.
  - `TestRestoreVolumeKeepsNameDriverLabels`
  - `TestRestoreHostPathFileAndDir`
  - `TestRestoreImagePullsByDigestAndTags`
  - `TestRestoreImagePullFailureFallsBackToLoad`
  - `TestRestoreImageLoadKeepsTags`: no digests, tag `relist-c7-api:latest`, loaded with its tag.
  - `TestRestoreUntaggedImageGetsMigratedTag`
  - `TestLoadedImageIDMismatchIsWarning`
  - `TestCleanupAndDiscard`
- [ ] **Step 2: Run `go test ./internal/migration/ -run TestRestore`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: restore migration items on the new host`

### Task 12: Finalize

**Files:**
- Create: `internal/migration/finalize.go`, `internal/migration/finalize_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `deployments.Store.CancelUnfinished` and `Queue.Enqueue` (Task 5), `sources.Store.Get`, `Store.ClearAll` (Task 2), `Docker.StopTraefik`/`StartTraefik`.
- Produces:

```go
func NewFinalizer(local LocalHost, exit func()) Finalizer  // Begin
func PrepareFinalize(dataDir string) (pending bool, err error) // before OpenAndMigrate
func ClearCopiedState(ctx context.Context, db *sql.DB, now time.Time) error // after OpenAndMigrate when pending
type Enqueuer interface {
    Enqueue(ctx context.Context, composeID int64, r deployments.Request) (deployments.Deployment, bool, error)
}
type CompleteDeps struct {
    Docker Docker; Queue Enqueuer; Sources *sources.Store; DataDir string; Finish func(ctx context.Context) error
}
func CompleteFinalize(ctx context.Context, d CompleteDeps) (Summary, error) // after the handler exists
type Summary struct {
    Deployments []struct{ ComposeID int64; Number int; Error string } `json:"deployments"`
    Warnings    []string `json:"warnings"`
    DNS         []string `json:"dns"` // every Domain host, deduplicated
}
```

Order, from the spec:
1. `Begin`: `StopTraefik`. Then move each entry of `.migration/staging/data/` into `DataDir`, replacing it. Leave `staging/panel.db`. Write `.migration/finalize` with the manifest's `RunningComposeIDs` as JSON, then call `exit()` (`os.Exit(0)` in production).
2. `PrepareFinalize`: when the marker exists, remove `panel.db`, `panel.db-journal`, `panel.db-wal` and `panel.db-shm`, then rename `staging/panel.db` to `panel.db`. If the staged file is already gone and `panel.db` exists, skip the swap, so a crash after the rename is safe. `pending` is true whenever the marker exists, swap or no swap, so `ClearCopiedState` always runs before the copied `migration_lock` row could reach `source.ResumeLock`. `ClearCopiedState` is idempotent.
3. `ClearCopiedState`: `DELETE FROM migration_lock`, `migration_token` and `migration_claim_failures`, then `CancelUnfinished(ctx, "server migration", now)`.
4. `CompleteFinalize`: `StartTraefik`. For each running ID, enqueue `ModeSkipBuild` when `sources.Store.Get` finds a Source, otherwise `ModeDeploy`. Call `Finish` (`POST finish` with `SelfURL`, retried 3 times; a failure becomes a warning). Write `.migration/summary.json`, then remove the marker and the staging dir.

`main.go` wiring:

```go
pending, err := migration.PrepareFinalize(cfg.DataDir)  // before OpenAndMigrate
db, err := database.OpenAndMigrate(cfg.DatabasePath())
if pending { migration.ClearCopiedState(ctx, db, time.Now()) }
// handler built (Task 13 returns the Target), then in the background:
target.ResumeIfPending(ctx) // which calls CompleteFinalize at PhaseFinalizing
```

- [ ] **Step 1: Write failing tests.**
  - `TestPrepareFinalizeRemovesJournalsBeforeSwap`: create an empty `panel.db`, a `panel.db-journal` and a staged database with one marker row. After `PrepareFinalize` and `OpenAndMigrate`, the marker row is present and no journal file remains.
  - `TestPrepareFinalizeIsIdempotent`
  - `TestPrepareFinalizeAfterRenameStillPending`: marker present, staged file already renamed. `pending` is true, and after `ClearCopiedState` the `migration_lock` table is empty.
  - `TestClearCopiedStateClearsLockAndCancels`
  - `TestCompleteFinalizeEnqueuesModesPerSource`: Compose 1 has a Source (`skip-build`) and Compose 2 has none (`deploy`). A Compose not in the marker is not enqueued.
  - `TestBeginStopsTraefikBeforeMovingDataDir`: `fakeDocker` call order.
- [ ] **Step 2: Run `go test ./internal/migration/ -run 'Test(Prepare|Clear|Complete|Begin)'`.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./...`.** Expected: PASS.
- [ ] **Step 5: Commit.** `feat: finalize a migration on the new host`

### Task 13: New host routes and wiring

**Files:**
- Create: `internal/migration/target_http.go`, `internal/migration/target_http_test.go`
- Modify: `internal/auth/auth.go`, `internal/httpapi/httpapi.go`, `main.go`

**Interfaces:**
- Consumes: Tasks 10–12.
- Produces:
  - `func (s *auth.Service) AdminExists(ctx context.Context) (bool, error)` and `func (s *auth.Service) Authenticated(r *http.Request) (bool, error)`, exported wrappers of the existing helpers.
  - `func (t *Target) RegisterRoutes(mux *http.ServeMux, allowed func(r *http.Request) (bool, error))`. `allowed` is true when no admin exists, or when the request is authenticated.

| Route | Body | Response |
|---|---|---|
| `POST /api/migration/pull/connect` | `{"sourceUrl","token"}` | `Progress` |
| `POST /api/migration/pull/recheck` | `{"hostPaths":[]}` | `Progress` |
| `POST /api/migration/pull/start` | `{"hostPaths":[]}` | `202 Progress`; `409` with the first blocking message |
| `GET /api/migration/pull/progress` | | `Progress`, including `Summary` once done |
| `POST /api/migration/pull/retry` | | `202 Progress` |
| `POST /api/migration/pull/abort` | | `204` |

  - `NewHandler` gains a trailing `migration.Options` argument:

```go
type Options struct {
    Docker          Docker
    Install         config.InstallSettings
    Insecure        bool
    ReportedDataDir string                          // "" means dataDir
    HTTP            *http.Client                    // nil means a default client with no overall timeout
    Backoff         func(attempt int) time.Duration // nil means 2s, 8s, 30s
    Exit            func()                          // nil means os.Exit(0)
}
```

    These pass through to `SourceDeps`, `TargetDeps` and `NewFinalizer`. It returns the `*migration.Target` too, so `main.go` can call `ResumeIfPending`. `--insecure-migration-source` is parsed in `main.go` from `os.Args`.
  - After the restart, `Progress()` rebuilds `PhaseDone` from `.migration/summary.json` when `state.json` is gone. `GET progress` then requires the admin cookie, because an admin now exists.
  - While `/api/auth/setup` would succeed and the Target's phase is past `connected`, `/api/auth/setup` answers `409` with `A migration is in progress`. This is wired through an `auth.Service.SetSetupBlocker(func() bool)`.
- [ ] **Step 1: Write failing tests.**
  - `TestPullRoutesOpenOnlyBeforeAdminExists`: with no admin, `200`. With an admin and no cookie, `401`. With an admin and a cookie, `200`.
  - `TestSetupBlockedDuringMigration`
  - `TestStartConflictCarriesBlockingMessage`
  - `TestProgressAfterRestartReadsSummary`: there is no `state.json` and there is a `summary.json`. With the cookie, `phase` is `done` with the summary. Without it, `401`.
- [ ] **Step 2: Run `go test ./internal/migration/ ./internal/auth/ ./internal/httpapi/`.** Expected: FAIL.
- [ ] **Step 3: Implement** and wire `main.go`: `PrepareFinalize`, `OpenAndMigrate`, `ClearCopiedState`, `NewHandler`, then go `source.ResumeLock` and go `target.ResumeIfPending`.
- [ ] **Step 4: Run `go test ./...` and `task build`.** Expected: PASS, and the binary builds.
- [ ] **Step 5: Commit.** `feat: expose the migration pull api to the setup screen`

### Task 14: Integration test with both panels in one process

**Files:**
- Create: `internal/migration/integration_test.go`

**Interfaces:**
- Consumes: everything above. Each panel is a full `httpapi.NewHandler` over its own `t.TempDir()` data dir, with its own `fakeDocker`, and the two share nothing. `Insecure: true`. The identical-path check compares `SourceInfo.DataDir` with the new panel's `DataDir`. Because two panels in one process cannot share one directory, the old panel's `SourceDeps.ReportedDataDir` is set to the new panel's path (Task 8).
- The test sets `Options.Exit` to cancel the first new-host handler's ctx, `Options.Backoff` to return 0 and `Options.HTTP` to a client with a test `RoundTripper`. Before building the second new-host handler on the same data dir, it waits on the first one's worker (the `wait` func `NewHandler` returns).
- [ ] **Step 1: Write the tests.**
  - `TestMigrationHappyPath`: seed the old panel with an admin, a Project, two Composes (one with a Source and one running), a Domain, a Volume with files owned `999:999`, a host path and a digest-less Image. Then run `connect`, `start`, poll until done, and restart the new handler: `PrepareFinalize`, `OpenAndMigrate`, `ClearCopiedState`, `NewHandler` and `ResumeIfPending`. Assert:
    - the new database has the same Composes, Domains and admin;
    - the new `fakeDocker` has the Volume with the same labels and files;
    - the Image has its tag;
    - one `skip-build` Deployment is queued for the running Compose;
    - the old `GET /api/migration/status` step is `migrated` with the target URL;
    - any other old route is `503`.
  - `TestMigrationConnectionCutMidItem`: a `RoundTripper` drops the volume stream once, and the migration completes.
  - `TestMigrationAbortRestoresOldHost`: abort after the first item. The old host's Composes are started, the maintenance container is removed, `status.step` is `""`, and the new host's `.migration/` is gone.
  - `TestResumeOnOldHostAfterMigrated`
- [ ] **Step 2: Run `go test ./internal/migration/ -run TestMigration -v`.** Expected: PASS. Fix the owning task's code for any failure, then rerun.
- [ ] **Step 3: Commit.** `test: cover a whole migration between two panels`

### Task 15: Web UI

**Files:**
- Create: `web/src/data/migration.ts`, `web/src/data/migration.test.ts`, `web/src/components/ServerMigrationCard.tsx`, `web/src/components/ServerMigrationCard.test.tsx`, `web/src/pages/MigrationLockPage.tsx`, `web/src/pages/MigrationLockPage.test.tsx`, `web/src/pages/MigrationPage.tsx`, `web/src/pages/MigrationPage.test.tsx`
- Modify: `web/src/pages/DockerPage.tsx`, `web/src/pages/AuthPages.tsx` (`SetupPage`), `web/src/app/routes.tsx` (`{ path: 'migration', element: <MigrationPage /> }` outside the gate), `web/src/app/paths.ts`, `web/src/app/RequireAuth.tsx`

**Interfaces:**
- Consumes: the routes of Tasks 8 and 13. TS types mirror `Progress`, `ItemProgress`, `Check`, `SourceInfo`, `HostPathCandidate` and `Summary` in camelCase.
- Produces:
  - `ServerMigrationCard` on `/docker`:
    - a `Segmented` lifetime picker (`1 hour`, `24 hours`, `7 days`, default `24 hours`) and **Create migration token**;
    - the token in copyable `Typography.Text`, shown once, with `Shown once. Anyone with it can copy this server.`;
    - the expiry and **Revoke**.
  - `RequireAuth`: after auth settles to `app`, `GET /api/migration/status`. When `step` is set, render `MigrationLockPage` instead of the `Outlet`.
  - `MigrationLockPage`:
    - when locked, a `Steps` view of the lock step, "Being copied by `<claimedBy>`", and **Cancel migration**, which opens a `Modal` that needs `RESUME` typed;
    - when Migrated, a `Result` with `Migrated to <targetUrl> on <time>` and **Resume this server**, which uses the same modal with a warning `Alert`: `Data written on the new server after the migration is not here.`
  - `SetupPage` adds a link, **Migrate from another panel**, to `/migration`.
  - `MigrationPage` stages, driven by `Progress.phase`:
    - **Connect**: `Form` with `sourceUrl` and `token`.
    - **Preview**:
      - sizes per kind;
      - an Images `Table` with a "pull" or "copy" tag;
      - a host path `Table` with `Checkbox`es, system paths unticked by default, calling `recheck` on change;
      - every `Check` as an `Alert` (`error` when blocking, otherwise `warning`);
      - **Start migration**, disabled while any check is blocking.
    - **Progress**: poll `progress` every 2s. One `Table` row per item with a `Progress` bar, state and error. **Retry** and **Abort** show when paused.
    - While `phase` is `finalizing`, connection errors and `502`/`503` from `progress` are expected, because the panel exits and restarts. Show "Restarting the panel…" and keep polling.
    - A `401` from `progress` redirects to `/signin` with `state.from = '/migration'`, with the copy `Sign in with the admin account of the old server`.
    - **Done**: a `Result` with a Deployment list (links to each log), the warnings and the DNS hosts to point at this server. It also says to sign in with the old admin account.
- [ ] **Step 1: Read the Ant Design docs** listed in Global Constraints and `docs/GUIDELINES.md`.
- [ ] **Step 2: Write failing vitest tests.**
  - `migration.test.ts`: the API functions' URLs and bodies.
  - `ServerMigrationCard.test.tsx`: the default lifetime is 24 hours, and the token is shown once after create.
  - `MigrationLockPage.test.tsx`: Resume is disabled until `RESUME` is typed.
  - `MigrationPage.test.tsx`: Start is disabled with a blocking check, and system paths are unticked. A network error while finalizing keeps polling. A `401` redirects to sign-in with `from` set to `/migration`.
- [ ] **Step 3: Run `pnpm --dir web test`.** Expected: FAIL.
- [ ] **Step 4: Implement.**
- [ ] **Step 5: Run `pnpm --dir web test` and `pnpm --dir web typecheck`.** Expected: PASS.
- [ ] **Step 6: Commit** the web code with `feat: add the server migration screens`. Then run `task build-web` and commit the assets with `chore: rebuild the embedded frontend with server migration`.

### Task 16: Probe, README and closing docs

**Files:**
- Create: `scripts/probe-migration.sh`
- Modify: `Taskfile.yml` (task `probe:migration`), `README.md` (a "Moving to a new server" section), `.scratch/server-migration/spec.md` (probe record, `Status:`)

- [ ] **Step 1: Write the probe.** It creates `docker network create probe-mig`, then two `docker:27-dind` containers (`mig-old` and `mig-new`, privileged) on that network. In each, it builds and runs the panel install with `RELIST_DATA_DIR=/var/lib/relist-panel`. On `mig-old` it creates the admin and seeds the three Composes from the spec through the API:
  1. `postgres:16` with a named Volume and 1,000 rows;
  2. a zip Source building a `busybox` httpd image;
  3. `alpine` bind-mounting `/srv/probe` with a 5 GiB file, to cross the old 4 GiB cap.

  It then creates a token, drives `mig-new`'s `/api/migration/pull/*` with `--insecure-migration-source` over the `probe-mig` network, waits for `done`, and asserts that:
  - `SELECT count(*)` is 1000 on `mig-new`;
  - `sha256sum /srv/probe/*` matches;
  - `acme.json` matches;
  - every Compose has a running container;
  - `mig-old` answers `503` on `/api/projects`.

  It exits non-zero on the first failure and cleans up on exit.
- [ ] **Step 2: Run `task probe:migration`.** Expected: `probe: migration ok`. Record the date, Docker version and result under `## Probe record` in the spec.
- [ ] **Step 3: Write the README section.**
  - Steps: install the new panel with the same `RELIST_DATA_DIR`, copy `.env`, create the token, connect, check the preview, start, change DNS, then turn off the old server.
  - What is not moved: install `.env` settings, non-local Volume drivers, and unticked host paths.
  - Hand-written Traefik routers get no maintenance page.
- [ ] **Step 4: Set `Status: ready-for-human`** in the spec, then commit with `docs: document server migration and record its probe`.
