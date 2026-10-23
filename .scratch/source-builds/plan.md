# Sources and the deployment queue — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Compose can have a Git or zip Source whose Dockerfiles it builds, and every deploy of every Compose runs as a Deployment in one host-wide queue, one at a time, with steps, a log and a history of 20.

**Architecture:** Two new packages:
- `internal/sources` owns the `compose_sources` row, the Deploy key, git fetch, zip extraction, Dockerfile detection and the `/source` routes.
- `internal/deployments` owns the `deployments` row, the queue worker, the logs and the `/deployments` routes.

`internal/composes` keeps the resolve-and-`up` logic, which moves out of the HTTP handlers into `RunDeploy`, and gains `PrepareBuild` for the staging files. The worker takes the Project gate through an exported `Acquire` on the `httpapi` coordinator, and only for the Deploy step. Import direction: `deployments → sources, composes`, `sources → composes`, and `composes` imports neither. `composes` reaches the queue only through a `DeploymentCanceler` it declares.

**Tech Stack:** Go 1.25 stdlib `net/http`, `os/exec`, `archive/zip`, `golang.org/x/crypto/ssh` (already in go.mod at v0.38.0), `gopkg.in/yaml.v3`, goose migrations, SQLite. React 19, antd 6.5.1, swr, vitest, and `yaml@2.9.1` (added as a direct dependency, the version monaco-yaml already installs).

**Spec:** [.scratch/source-builds/spec.md](spec.md). ADR: [docs/adr/0016-sources-and-deployment-queue.md](../../docs/adr/0016-sources-and-deployment-queue.md).

## Global Constraints

- Commits: Conventional Commits, no scope. **No** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md).
- **Never edit a committed migration.** The user runs feature branches on the live install. All schema for this feature is in `00016_sources_and_deployments.sql`, written once in Task 1. A later schema change needs `00017`.
- Domain words come from CONTEXT.md: Compose, Service, Source, Deploy key, Deployment. Never "stack", "app", "job" or "build" as an entity name.
- Paths inside a Compose directory:

  | Path | Mode |
  |---|---|
  | `src/` | — |
  | `src/.relist-source` (the marker) | — |
  | `.src.new/` | — |
  | `.build-compose.yml` | 0644 |
  | `.build.env` | 0600 |
  | `.deployments/<number>.log` | 0600 |
  | `.ssh/known_hosts` | — |
  | `.upload-*.zip` | temp file |

  The Compose directory is always `filepath.Dir(req.ComposeFilePath)`. Never rebuild it from names.
- Limits:

  | Limit | Value |
  |---|---|
  | Zip upload | 500 MiB compressed (`500 << 20`) |
  | Zip extraction | 10,000 entries and 4 GiB uncompressed (`volfs.DefaultExtractLimits()`) |
  | History | 20 Deployments per Compose |
  | Deployment timeout | 60 minutes |
  | Log read | at most 1 MiB per call |
  | Dockerfile detection | depth 4, skipping `.git`, `node_modules` and `vendor` |
  | `repo_url` | at most 512 chars |
  | `branch` | at most 255 chars |

- Every external command runs with `exec.CommandContext` and an argv, never a shell. Values that reach an argv must not start with `-`.
- Copy, verbatim:
  - `src/ already exists in this Compose's directory, move it first`
  - `A deployment is running`
  - `confirmName must match the compose name`
  - `no Service has build:`
  - Restart failure reason: `panel restarted`
  - Timeout reason: `timeout`
  - Key hint: `Repository → Settings → Deploy keys → Add deploy key, leave write access off`
  - Empty Source card: `Services use images named in the YAML`
  - Zip help: `Replaces all code in the Source`
- Before any edit under `web/`, read `https://ant.design/llms.txt` and the pages for `Segmented`, `Upload`, `Steps`, `Typography` (copyable), `Select`, `Table`, `Alert`, `Dropdown`, `Modal` and `List`. Also read `docs/GUIDELINES.md`, and check props against antd 6.5.1 (AGENTS.md).
- After any `web/` change that ships, rebuild the embedded frontend (`internal/webassets/dist`) in its own `chore: rebuild the embedded frontend with …` commit, as the repo does.
- Real git runs in tests against `file://` bare repositories. Real ssh, GitHub and Docker are verified manually (Task 12), never through fakes.

## Review Focus

- **A double-click on Build & Deploy.** Two enqueues race the `deployments_one_queued` unique index. The loser must get the winner's Deployment back (`200`), not a `500`. Test in Task 1.
- **A branch name with slashes** (`feature/checkout-v2`). It validates, `ls-remote` lists it, and the fetch checks it out. Test in Tasks 4 and 5.
- **A zip made on macOS.** It carries `__MACOSX/` and `.DS_Store` beside the real top-level folder. A person expects the single folder to be stripped anyway, so those two names are ignored before the single-root check and never extracted. Test in Task 4.
- **A Dockerfile at the root of a Compose named `My Shop`.** The suggested Service name must be a valid Compose service name (`my-shop`), not the display name. Test in Task 4.
- **A log read whose byte offset lands inside a multi-byte UTF-8 character** (Thai output from a build). The server returns text that ends on a rune boundary and an offset that matches it, so the browser never shows `�`. Test in Task 7.

---

## File Structure

**Go: create**

- `internal/migrations/00016_sources_and_deployments.sql`: both tables, exactly as the spec's Data model, plus `head_sha TEXT NOT NULL DEFAULT ''` on `compose_sources`. `head_sha` is the branch head from the last test, which the card compares with the built SHA. The UI shows a "New commits" tag, not a count, because a count needs a fetch.
- `internal/sources/store.go`: `Source`, `Store`.
- `internal/sources/keys.go`: `GenerateKey`.
- `internal/sources/validate.go`: `ValidateRepoURL`, `ValidateBranch`.
- `internal/sources/srcdir.go`: the marker, `EnsureOwnedSrc` and `ReplaceSrc`.
- `internal/sources/zip.go`: `ExtractZip`.
- `internal/sources/detect.go`: `DetectDockerfiles`.
- `internal/sources/git.go`: `Git`, `Fetch` and `ListBranches`.
- `internal/sources/http.go`: the `/source` routes.
- `internal/deployments/store.go`: `Deployment`, `Store`.
- `internal/deployments/queue.go`: `Queue`, the worker, cancel and recovery.
- `internal/deployments/logfile.go`: log paths and `ReadLog`.
- `internal/deployments/http.go`: the `/deployments` routes and `/api/deployments/queue`.

**Go: modify**

- `internal/composes/composes.go`:
  - The runner interface.
  - `RunDeploy`, `Target`, `CheckDeployable` and `PrepareBuild`.
  - Remove the `/deploy` and `/deploy-fresh` routes and handlers.
  - Delete calls the canceler.
- `internal/composes/teardown.go`: teardown calls the canceler.
- `internal/dockerexec/dockerexec.go`: `Up` with options, `Build`, and streaming output.
- `internal/httpapi/coordination.go`: exported `Acquire`, plus the new gate exemptions.
- `internal/httpapi/httpapi.go`: `NewHandler(ctx, …)`, the wiring, starting the queue.
- `main.go`: passes its signal context to `NewHandler`.
- `internal/httpapi/httpapi_test.go`, `domains_test.go`, `preview_test.go`, `lifecycle_regression_test.go`:
  - The fake runner gains a mutex, `Build` and `UpOptions`.
  - A `deployAndWait` helper replaces every `/deploy` and `/deploy-fresh` call.

**Web: create**

- `web/src/data/deployments-view.ts` (+ test): step labels, status text and queue text.
- `web/src/data/source-view.ts` (+ test): the card state.
- `web/src/data/build-insert.ts` (+ test): `insertBuildBlocks`.
- `web/src/components/ComposeDeploymentsTab.tsx`
- `web/src/components/SourceCard.tsx`
- `web/src/components/SourceModal.tsx`
- `web/src/components/DetectDockerfilesModal.tsx`
- `web/src/components/BuildSnippetsModal.tsx`

**Web: modify**

- `web/src/data/types.ts`, `web/src/data/api.ts` (+ `api.test.ts`) and `web/src/data/store.tsx`: enqueue in place of deploy.
- `web/src/pages/ComposePage.tsx`: header button, new tab and card.
- `web/src/pages/ProjectPage.tsx`: list deploy enqueues.
- `web/package.json`: `"yaml": "2.9.1"`.

**Docs:** `docs/sa.md` and `README.md` (Task 12).

---

### Task 1: Migration and the two stores

**Files:**
- Create: `internal/migrations/00016_sources_and_deployments.sql`, `internal/sources/store.go`, `internal/deployments/store.go`
- Test: `internal/sources/store_test.go`, `internal/deployments/store_test.go`

**Interfaces:**
- Produces (`sources`):
  ```go
  type Kind string // "git", "zip"
  type Source struct {
      ComposeID int64; Kind Kind; RepoURL, Branch string; Submodules bool
      PrivateKey, PublicKey, HeadSHA string; LastTestAt *time.Time; LastTestOK *bool
      ZipName string; ZipSize int64; ZipFiles int; UploadedAt *time.Time
      CreatedAt, UpdatedAt time.Time
  }
  func NewStore(db *sql.DB) *Store
  func (s *Store) Get(ctx context.Context, composeID int64) (Source, error) // sql.ErrNoRows when none
  func (s *Store) Save(ctx context.Context, src Source) error               // insert or replace by compose_id
  func (s *Store) Delete(ctx context.Context, composeID int64) error
  func (s *Store) RecordTest(ctx context.Context, composeID int64, ok bool, headSHA string, at time.Time) error
  ```
- Produces (`deployments`):
  ```go
  type Mode string   // "deploy", "build", "skip-build"
  type Status string // "queued", "running", "succeeded", "failed", "canceled"
  type Deployment struct {
      ID, ComposeID int64; Number int; Mode Mode; NoCache, FreshVolumes bool
      Status Status; Step string; Steps []string; FailedStep, Reason string
      SourceRef, SourceSubject string; Warnings []string
      QueuedAt time.Time; StartedAt, FinishedAt *time.Time
  }
  type Active struct{ ComposeID int64; ComposeName string; Number int; Status Status; Step string }
  func NewStore(db *sql.DB) *Store
  func (s *Store) Enqueue(ctx context.Context, composeID int64, mode Mode, noCache, fresh bool, now time.Time) (d Deployment, created bool, err error)
  func (s *Store) NextQueued(ctx context.Context) (Deployment, bool, error)
  func (s *Store) Start(ctx context.Context, id int64, steps []string, now time.Time) error
  func (s *Store) SetStep(ctx context.Context, id int64, step string) error
  func (s *Store) SetSource(ctx context.Context, id int64, ref, subject string) error
  func (s *Store) Finish(ctx context.Context, id int64, status Status, failedStep, reason string, warnings []string, now time.Time) error
  func (s *Store) FailRunning(ctx context.Context, reason string, now time.Time) (int, error)
  func (s *Store) CancelQueued(ctx context.Context, composeID int64, now time.Time) (bool, error)
  func (s *Store) Get(ctx context.Context, composeID int64, number int) (Deployment, error)
  func (s *Store) List(ctx context.Context, composeID int64, limit int) ([]Deployment, error) // newest first
  func (s *Store) Ahead(ctx context.Context, d Deployment) (int, error) // queued with (queued_at, id) before d, plus 1 if one is running
  func (s *Store) Prune(ctx context.Context, composeID int64, keep int) ([]int, error) // returns the removed numbers
  func (s *Store) Active(ctx context.Context) ([]Active, error)
  ```

- [ ] **Step 1: Write failing store tests.** Use `database.OpenAndMigrate` on a temp file and insert a project, an environment and a Compose with SQL.
  - `TestSourceSaveGetRoundTrip`: a git Source round-trips, including `Submodules` and a nil `LastTestAt`. `Get` on another Compose returns `sql.ErrNoRows`.
  - `TestSourceCascadesWithCompose`: deleting the Compose row deletes the Source.
  - `TestEnqueueNumbersPerCompose`: three enqueues across Finish calls get numbers 1, 2, 3. A second Compose starts at 1.
  - `TestEnqueueReturnsExistingQueued`: a second `Enqueue` while one is queued returns `created == false` with the same `Number`, and its options stay those of the first call.
  - `TestEnqueueRaceReturnsWinner` (Review Focus): 10 goroutines `Enqueue` the same Compose. Exactly one has `created == true`, every result has the same `Number`, and no error is returned.
  - `TestNextQueuedIsFIFOAcrossComposes`
  - `TestFailRunningMarksOnlyRunning`: with `reason == "panel restarted"`, queued rows are untouched.
  - `TestAheadCountsQueuedBeforeAndRunning`
  - `TestPruneKeepsNewest`: after 25 finished Deployments, `Prune(…, 20)` returns numbers 1–5.
  - `TestActiveListsQueuedAndRunningWithComposeName`
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/sources/ ./internal/deployments/`. Expected: FAIL, packages missing.
- [ ] **Step 3: Write the migration.** Use the spec's SQL plus `head_sha`, with a `-- +goose Down` that drops both tables.
- [ ] **Step 4: Implement both stores.**
  - `Steps` and `Warnings` are JSON TEXT columns. Times are Unix seconds, as `composes` stores them.
  - The DB has a single connection (`SetMaxOpenConns(1)` in `internal/database/database.go`), so transactions are serialised. Inside a transaction, use only `tx`: a query on `db` would wait for the one connection and deadlock.
  - `Enqueue` runs in one transaction: `SELECT … WHERE compose_id=? AND status='queued'`, then `MAX(number)+1`, then INSERT. On a unique-constraint error (`isUniqueConstraint`, the same check `composes` uses) it re-reads the queued row and returns it with `created == false`.
- [ ] **Step 5: Run the tests and confirm they pass.** Run `go test -race ./internal/sources/ ./internal/deployments/ ./internal/database/`. Expected: PASS, including the migration test in `database`.
- [ ] **Step 6: Commit.** `feat: add the sources and deployments tables and stores`

### Task 2: Runner: options on `Up`, a `Build` step and streamed output

**Files:**
- Modify: `internal/composes/composes.go:63-70` (interface), `internal/dockerexec/dockerexec.go`, every `runner.Up(` call site in `composes`, and the fake in `internal/httpapi/httpapi_test.go`
- Test: `internal/dockerexec/dockerexec_test.go`

**Interfaces:**
- Produces (`composes`):
  ```go
  type UpOptions struct{ Build bool; Out io.Writer } // Build: --build, else --no-build. Out nil: no streaming.
  type BuildOptions struct{ Service string; NoCache bool; Out io.Writer }
  // ComposeRunner changes:
  Up(ctx context.Context, req RunRequest, opts UpOptions) error
  Build(ctx context.Context, req RunRequest, opts BuildOptions) error
  ```
- Produces (`dockerexec`, unexported, tested): `upArgs(build bool) []string`, `buildArgs(service string, noCache bool) []string`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpArgs`: `upArgs(true)` is `["up","-d","--build","--remove-orphans"]`, and `upArgs(false)` is `["up","-d","--no-build","--remove-orphans"]`.
  - `TestBuildArgs`: `buildArgs("api", false)` is `["build","--progress","plain","api"]`, and with `true` it is `["build","--progress","plain","--no-cache","api"]`.
  - `TestRunStreamsToOut`: point the runner's command at `sh -c 'echo out; echo err >&2; exit 3'` through the package's existing test seam, or add an unexported `command` variable if there is none. Both lines must reach `Out`, and the error must contain `exit status 3`.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/dockerexec/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - With `Out` set, `run` writes both stdout and stderr to `Out` through an `io.MultiWriter` and also keeps the last 4 KiB of stderr for the error text.
  - With `Out` nil, it behaves as today.
  - Today's call sites pass `UpOptions{Build: true}`, so their behaviour does not change.
  - The fake runner in `httpapi_test.go` gains a `sync.Mutex` around `calls` and `statuses`, because the worker will call it from another goroutine. It also records `"build:<service>"` calls, has a `buildErr map[string]error`, and records `UpOptions.Build` in a new `fakeComposeRun.build bool`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./internal/dockerexec/ ./internal/composes/ ./internal/httpapi/`. Expected: PASS, and existing tests are unchanged.
- [ ] **Step 5: Commit.** `feat: stream compose output and build one service at a time in the runner`

### Task 3: Composes: `RunDeploy`, `PrepareBuild` and the canceler hook

**Files:**
- Modify: `internal/composes/composes.go`, `internal/composes/teardown.go`
- Create: `internal/composes/deploy.go` for the new exported API, moved out of the handlers
- Test: `internal/composes/deploy_test.go`

**Interfaces:**
- Consumes: Task 2's `UpOptions`.
- Produces:
  ```go
  type DeployTarget struct{ ProjectID int64; Compose Compose; Request RunRequest }
  type DeployOptions struct{ FreshVolumes, Build bool; Out io.Writer }
  type BuildPlan struct {
      Request  RunRequest // ProjectName as deploy; ComposeFilePath .build-compose.yml; EnvFilePath .build.env
      Services []string   // Services with build:, in YAML order
      Cleanup  func()     // removes both staging files
  }
  type DeploymentCanceler interface{ CancelForComposes(ctx context.Context, composeIDs []int64) }
  func (s *Service) Target(ctx context.Context, composeID int64) (DeployTarget, error) // sql.ErrNoRows when gone
  func (s *Service) CheckDeployable(ctx context.Context, t DeployTarget) error       // blank YAML, ambiguous identity
  func (s *Service) RunDeploy(ctx context.Context, t DeployTarget, opts DeployOptions) (warnings []string, err error)
  func (s *Service) PrepareBuild(ctx context.Context, t DeployTarget) (BuildPlan, error)
  func (s *Service) SetDeploymentCanceler(c DeploymentCanceler)
  func ComposeDir(req RunRequest) string
  ```

- [ ] **Step 1: Write the failing tests.** Use an in-package test with a temp data dir and a minimal recording runner.
  - `TestRunDeployWritesFilesAndUps`: the resolved file and `.env` exist, `Up` is called with `Build == opts.Build`, the warnings match today's, and `deployed_at` is set.
  - `TestRunDeployFreshDownsBeforeWrite`: same order as today's `deployFresh`. With an existing compose file, `DownWithVolumes` runs before the write.
  - `TestRunDeployDomainTargetErrorWritesNothing`: the error text is today's refusal copy. The previously written file is unchanged and `Up` is not called.
  - `TestPrepareBuildWritesUnresolvedStaging`:
    - `.build-compose.yml` equals the stored YAML byte for byte.
    - `.build.env` holds the merged Environment and Compose Variables, with mode 0600.
    - `Services` is `["web","api"]` for YAML that declares `web` (build), `db` (image) and `api` (build), in that order.
    - `Cleanup` removes both files.
  - `TestPrepareBuildOnNewCompose`: it works with no resolved file on disk yet. This is the advisor's first-deploy case.
  - `TestDeleteCallsCanceler` and `TestTeardownCallsCanceler`: a recording canceler receives the Compose IDs before `DownWithVolumes`.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/composes/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Move the body of `deploy` and `deployFresh` (`composes.go:329-399`) into `RunDeploy`, returning errors instead of writing responses. `prepareDeployFiles` and `resolveForDeploy` lose their `http.ResponseWriter` parameter and return errors. `preview` maps those errors to the same statuses as today.
  - When `Out` is set, `RunDeploy` writes each warning to `Out` as `warning: <text>\n`.
  - `PrepareBuild` reads Service order from a `yaml.Node` decode, because a map loses order.
  - Keep the `/deploy` and `/deploy-fresh` handlers for now as thin wrappers over `RunDeploy`, so the HTTP suite stays green until Task 7 removes them.
  - The canceler is optional: nil means no call.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./internal/composes/ ./internal/httpapi/`. Expected: PASS.
- [ ] **Step 5: Commit.** `refactor: move deploy into RunDeploy and add build staging files`

### Task 4: Sources: key, validation, `src/` ownership, zip and detection

**Files:**
- Create: `internal/sources/keys.go`, `validate.go`, `srcdir.go`, `zip.go`, `detect.go`
- Test: one `_test.go` for each

**Interfaces:**
- Produces:
  ```go
  func GenerateKey(comment string) (privatePEM, authorizedKey string, err error) // ed25519; authorizedKey ends " <comment>"
  func ValidateRepoURL(v string) (string, error) // trimmed
  func ValidateBranch(v string) (string, error)  // "" allowed
  const MarkerName = ".relist-source"
  var ErrForeignSrc = errors.New("src/ already exists in this Compose's directory, move it first")
  func EnsureOwnedSrc(composeDir string) error             // ok when src/ is absent or carries the marker
  func ReplaceSrc(composeDir, newDir string) error         // writes the marker into newDir, swaps it in with renames, removes the old one
  func ExtractZip(zipPath, destDir string, limits volfs.ExtractLimits) (files int, err error)
  var ErrUnsafeZip = errors.New("unsafe zip entry")
  type Dockerfile struct{ Path, Service, Context, Dockerfile string } // Path relative to src; Context like "./src/api"
  func DetectDockerfiles(srcDir, composeName string) ([]Dockerfile, error)
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestGenerateKey`:
    - The key parses with `ssh.ParsePrivateKey`.
    - The public half matches `ssh.ParseAuthorizedKey`, is of type `ssh-ed25519`, and has the comment `relist-c42`.
    - Two calls give different keys.
  - `TestValidateRepoURL`:
    - Accepts `git@github.com:acme/shop.git`, `ssh://git@gitlab.example.com:2222/acme/shop.git` and `git@gitea.local:a/b`.
    - Rejects `https://github.com/acme/shop.git`, `-oProxyCommand=x`, `git@github.com:acme/shop.git x` and a 513-char value.
  - `TestValidateBranch` (Review Focus):
    - Accepts `""`, `main` and `feature/checkout-v2`.
    - Rejects `-x`, `a..b`, `a b`, `a~1`, `a^`, `a:b`, `a?`, `a*`, `a[`, `a\b`, `a/`, `a.lock`, `a@{1}`, `a//b` and `.a`.
  - `TestEnsureOwnedSrc`: passes when `src/` is absent and when it is marked. Returns `ErrForeignSrc` for an unmarked `src/`.
  - `TestReplaceSrcSwapsAndMarks`: the old content is gone, the new content and the marker are present, and `.src.new` is gone.
  - `TestExtractZip`:
    - Strips a single root `shop-main/`.
    - Ignores `__MACOSX/` and `.DS_Store` before the single-root check (Review Focus).
    - Keeps mode `0755` from the external attributes.
    - Allows a relative symlink inside the tree.
    - Refuses `../x`, `/abs`, a symlink to `../../etc` and a symlink to `/etc`, each with `errors.Is(err, ErrUnsafeZip)`.
    - Refuses more than `MaxEntries` and more than `MaxBytes`.
    - Build test zips in memory with `archive/zip`, setting `SetMode` for executables and symlinks.
  - `TestDetectDockerfiles`:
    - For a tree with `api/Dockerfile`, `web/Dockerfile.prod`, `tools/dev.Dockerfile`, `node_modules/x/Dockerfile`, `a/b/c/d/e/Dockerfile` (depth 5) and a root `Dockerfile`, the result is:
      - `api` → `./src/api`
      - `prod` → `./src/web` with Dockerfile `Dockerfile.prod`
      - `dev` → `./src/tools`
      - `my-shop` → `./src` for composeName `My Shop` (Review Focus)
    - `node_modules` and the depth-5 file are absent.
    - Two `api/` names in different directories become `api` and `api-2`.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/sources/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - The key uses `ssh.MarshalPrivateKey` to produce OpenSSH PEM.
  - Service names go through `internal/slug` and are then restricted to `[a-z0-9_-]`.
  - `ExtractZip` checks every entry before writing any, then writes them. It resolves symlink targets with `filepath.Join(filepath.Dir(name), target)` and requires the result to stay under the root.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./internal/sources/`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: add deploy keys, zip extraction and Dockerfile detection for sources`

### Task 5: Sources: git fetch and branch listing

**Files:**
- Create: `internal/sources/git.go`
- Test: `internal/sources/git_test.go`, which skips when `git` is not on `PATH`

**Interfaces:**
- Consumes: `Source`, `ReplaceSrc`, `MarkerName`.
- Produces:
  ```go
  type Ref struct{ SHA, Subject string }
  type Branch struct{ Name, SHA string }
  type Git struct{ Bin string } // "" means "git"
  func (g Git) Fetch(ctx context.Context, src Source, composeDir string, out io.Writer) (Ref, error)
  func (g Git) ListBranches(ctx context.Context, src Source, composeDir string) ([]Branch, error)
  func sshCommand(keyPath, knownHosts string) string // unexported, tested
  ```

- [ ] **Step 1: Write the failing tests.**
  - Each test builds a bare repository in a temp dir: `git init --bare`, plus a worktree that commits and pushes. Both use `file://` URLs. `ValidateRepoURL` is not called here, so file URLs are allowed in tests only.
  - `TestFetchFirstClone`:
    - `src/` holds the files.
    - The marker exists.
    - `Ref.SHA` equals the pushed commit and `Subject` is its message.
    - Output went to `out`.
  - `TestFetchAfterForcePush`: an untracked file left in `src/` is removed, the marker survives, and the new SHA is returned.
  - `TestFetchBranchWithSlash` (Review Focus): checks out `feature/checkout-v2`.
  - `TestFetchSubmodules`: with `Submodules` on, the submodule's file exists. With it off, the file is absent.
  - `TestFetchRefusesForeignSrc`: an unmarked `src/` returns `ErrForeignSrc` and is left untouched.
  - `TestListBranches`: returns `main` and `feature/checkout-v2` with their SHAs.
  - `TestSSHCommand`: the result contains `-i <key>`, `IdentitiesOnly=yes`, `StrictHostKeyChecking=accept-new` and `UserKnownHostsFile=<path>`.
  - `TestKeyFileRemoved`: after `Fetch`, no temp key file is left in `os.TempDir()` with the prefix `relist-key-`.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/sources/ -run 'Fetch|ListBranches|SSHCommand|KeyFile'`. Expected: FAIL.
- [ ] **Step 3: Implement `Fetch`.**
  - Call `EnsureOwnedSrc`, then create `src/` with the marker when it is absent.
  - Run the spec's sequence (`init`, `remote add|set-url`, `fetch --depth 1 origin <branch>`, `checkout --force FETCH_HEAD`, `clean -ffdx -e .relist-source`, and the optional `submodule update --init --recursive --depth 1`), then `log -1 --format=%H%n%s`.
  - Every command gets `GIT_SSH_COMMAND` (temp key 0600, prefix `relist-key-`, removed by `defer`) and `GIT_TERMINAL_PROMPT=0`.
  - `-c protocol.file.allow=always` goes on the submodule command only, and only when `src.RepoURL` starts with `file://`. Tests need it, and a validated production URL never starts with `file://`.
  - `ListBranches` runs `ls-remote --heads <url>`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./internal/sources/`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: fetch git sources over ssh with a per-compose deploy key`

### Task 6: The deployment queue

**Files:**
- Create: `internal/deployments/queue.go`, `internal/deployments/logfile.go`
- Test: `internal/deployments/queue_test.go`

**Interfaces:**
- Consumes: Task 1's `Store`, Task 3's `DeployTarget`/`DeployOptions`/`BuildPlan`, Task 2's `BuildOptions`, Task 5's `Ref`.
- Produces:
  ```go
  type Gate interface{ Acquire(ctx context.Context, projectID int64) (release func(), err error) }
  type Composes interface {
      Target(ctx context.Context, composeID int64) (composes.DeployTarget, error)
      CheckDeployable(ctx context.Context, t composes.DeployTarget) error
      PrepareBuild(ctx context.Context, t composes.DeployTarget) (composes.BuildPlan, error)
      RunDeploy(ctx context.Context, t composes.DeployTarget, opts composes.DeployOptions) ([]string, error)
  }
  type Fetcher interface{ Fetch(ctx context.Context, src sources.Source, composeDir string, out io.Writer) (sources.Ref, error) }
  type Builder interface{ Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error }
  type Deps struct {
      Store *Store; Sources *sources.Store; Composes Composes; Fetcher Fetcher; Builder Builder; Gate Gate
      Timeout time.Duration // 60 * time.Minute in production
      Keep int              // 20
      Now func() time.Time
  }
  type Request struct{ Mode Mode; NoCache, FreshVolumes bool }
  var ErrNotCancelable = errors.New("deployment can no longer be canceled")
  func NewQueue(d Deps) *Queue
  func (q *Queue) Run(ctx context.Context)  // FailRunning("panel restarted"), then loops until ctx is done; returns after any Deploy step in progress finishes
  func (q *Queue) Enqueue(ctx context.Context, composeID int64, r Request) (Deployment, bool, error)
  func (q *Queue) Cancel(ctx context.Context, composeID int64, number int) error
  func (q *Queue) CancelForComposes(ctx context.Context, composeIDs []int64) // satisfies composes.DeploymentCanceler
  func (q *Queue) IsRunning(composeID int64) bool
  func (q *Queue) TryLockSource(composeID int64) (unlock func(), ok bool)
  func LogPath(composeDir string, number int) string // <dir>/.deployments/<n>.log
  ```

- [ ] **Step 1: Write the failing tests.** Use fakes for `Composes`, `Fetcher`, `Builder` and `Gate`. Each fake can block on a channel so the test controls timing. The test runs `Run` in a goroutine with a cancelable context.
  - `TestRunsOneAtATimeFIFO`: enqueue A then B while A's build blocks. B stays `queued`. After A is released, B runs.
  - `TestBuildModeSteps`:
    - `Steps` is `["fetch","build:web","build:api","deploy"]`.
    - The builder sees `web` then `api` with the plan's `Request`.
    - `RunDeploy` gets `Build:false`.
    - `SourceRef` and `SourceSubject` come from the fetcher.
    - `Cleanup` is called.
  - `TestDeployModeUsesBuildTrue`: there is no fetch, `RunDeploy` gets `Build:true`, and `Steps` is `["deploy"]`.
  - `TestSkipBuildMode`: `Steps` is `["deploy"]` with `Build:false`.
  - `TestZipSourceRecordsZipName`: there is no fetcher call, and `SourceRef` is the zip name.
  - `TestNoBuildServicesLogsAndDeploys`: the log contains `no Service has build:`, and `Steps` is `["fetch","deploy"]`.
  - `TestStopsAtFirstFailedBuild`: `build:web` fails, so `api` is never built and `RunDeploy` is never called. The result is `failed` with `failed_step == "build:web"`.
  - `TestGateOnlyForDeployStep`: the fake gate records acquisitions. It has none during fetch and build, and exactly one around `RunDeploy`.
  - `TestCancelQueued`, and `TestCancelWhileBuilding`: the builder's ctx is done, the status is `canceled`, and the gate is never acquired.
  - `TestCancelDuringDeployRefused`: `ErrNotCancelable`.
  - `TestCancelWhileWaitingForGate`: the fake gate blocks, then `CancelForComposes` runs while the worker waits on it. The result is `canceled`, and `RunDeploy` is never called.
  - `TestComposeGoneAfterGate`: the row is deleted while the worker waits on the gate, then the gate is released. `RunDeploy` is never called.
  - `TestDeployStepSurvivesShutdown`: cancel `Run`'s ctx while `RunDeploy` blocks. `RunDeploy`'s ctx is not done, and `Run` returns only after it finishes.
  - `TestTryLockSourceFailsDuringBuild`
  - `TestCancelForComposesWaitsForExit`: it returns only after the building fake has returned. During the Deploy step it does not wait, and that Deployment finishes.
  - `TestTimeout`: with `Timeout: 50ms`, the result is `failed` with reason `timeout`.
  - `TestRestartRecovery`: seed one `running` and one `queued` row. `Run` marks the first `failed` with `panel restarted`, then runs the queued one.
  - `TestSnapshotAtStart`: the YAML changes after enqueue but before start, and `PrepareBuild` sees the new YAML.
  - `TestComposeDeletedMidRun`: `Target` returns `sql.ErrNoRows` and `Finish` affects 0 rows, yet the worker continues with the next Deployment.
  - `TestPrunesAfterFinish`: with `Keep: 2`, after 3 Deployments only 2 rows and 2 log files remain.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/deployments/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - The worker loop: `NextQueued`, then `Target` (a missing Compose finishes as `failed` with reason `compose deleted`), then `CheckDeployable`, then work out the steps, then `Start`. It runs the steps with a per-Deployment `context.WithTimeout` whose cancel function is held in `q.running` under a mutex. Then `Finish`, then `Prune` (removing log files too).
  - A wake-up channel, buffered to 1, is signalled by `Enqueue`.
  - The phase (`fetching`, `building`, `waiting-gate` or `deploying`) is kept in memory next to the cancel function, so `Cancel` and `CancelForComposes` can refuse during Deploy and wait otherwise, through a `done` channel per run.
  - **The phase becomes `deploying` only after `Gate.Acquire` returns.** While the worker waits for the gate, the Deployment can still be canceled, and `Acquire` returns on ctx cancel. After acquiring, the worker re-checks that the Compose row exists, by existence only, so the snapshot rule holds. If it is gone, it releases the gate and finishes `canceled`, reason `compose deleted`, without calling `RunDeploy`. Otherwise a Delete that held the gate would have its directory recreated and containers started for a Compose with no row.
  - **Contexts.** Fetch and build run under `ctx` (from `Run`) plus the timeout, so shutdown may cut them, and they end `failed` with reason `panel restarted` on the next start. The Deploy step runs under `context.WithoutCancel(ctx)` plus the remaining timeout, so SIGTERM never kills `docker compose up` halfway.
  - **Source lock.** `q.srcLocks` is a per-Compose `sync.Mutex`. The worker holds it from fetch through the end of the build steps. It exports `func (q *Queue) TryLockSource(composeID int64) (unlock func(), ok bool)` for Task 8.
  - The log file opens with `O_APPEND|O_CREATE`, mode 0600, and every step writes a header line `==> <step>`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./internal/deployments/`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: run deployments one at a time from a host-wide queue`

### Task 7: Deployments HTTP, gate wiring, and moving the deploy tests

**Files:**
- Create: `internal/deployments/http.go`
- Modify:
  - `internal/httpapi/coordination.go`: exported `func (c *coordinator) Acquire(ctx, projectID) (func(), error)` and the exemptions.
  - `internal/httpapi/httpapi.go`: `NewHandler(ctx context.Context, db, …) (http.Handler, func())`. It builds the queue, starts `go queue.Run(ctx)` and calls `composesService.SetDeploymentCanceler(queue)`. The returned `wait` blocks until `Run` has returned.
  - `main.go`: after `server.Shutdown`, call `wait()` within the shutdown deadline, so an in-progress Deploy step finishes before exit.
  - `internal/composes/composes.go`: remove the `deploy` and `deployFresh` routes and handlers.
  - Every httpapi test that deploys.
- Test: `internal/httpapi/deployments_test.go`, `internal/deployments/logfile_test.go`

**Interfaces:**
- Consumes: Task 6's `Queue`.
- Produces: the spec's `/deployments` routes. JSON field names are camelCase versions of the Go fields, plus `ahead` on GET one while queued. The helpers:
  ```go
  func ReadLog(path string, offset int64, max int) (text string, next int64, err error) // ends on a rune boundary
  // test helper in httpapi_test.go:
  func (s *testServer) deployAndWait(t *testing.T, composePath string, body any) deploymentResponse
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestReadLogRuneBoundary` (Review Focus): for a file holding `"สวัสดี"`, `ReadLog(path, 0, 4)` returns the first whole rune(s) only. The next offset is at a rune boundary, and repeated reads rebuild the exact string.
  - `TestReadLogCapsAt1MiB`
  - `deployments_test.go`:
    - `POST /deployments {"mode":"deploy"}` gives `202 {number:1}`. `deployAndWait` reaches `succeeded`, and the fake recorded `up` with `build == true`.
    - The same POST twice while the worker is blocked gives `200` with the same number the second time.
    - `{"mode":"build"}` on a Compose without a Source gives `400`.
    - `{"freshVolumes":true}` without `confirmName` gives `400` with `confirmName must match the compose name`.
    - Blank YAML gives `400` (`compose has no YAML content to deploy`).
    - `GET /deployments` is newest first.
    - `GET /deployments/{n}/log?offset=0` returns `{text, offset, done}`.
    - `POST …/cancel` during Deploy gives `409`.
    - `GET /api/deployments/queue` lists the running one with `composeName`.
    - Deleting a Compose while its Deployment is building gives `204` without deadlocking, and the fake builder saw its ctx canceled.
    - Deleting while in Deploy gives `204` after the Deploy finishes.
    - Cancel and `GET /log` are not gated: they answer while a Deploy holds the Project gate.
  - Migrate the existing tests: every `POST …/deploy` becomes `deployAndWait(… {"mode":"deploy"})`, and every `…/deploy-fresh` becomes `{"mode":"deploy","freshVolumes":true,"confirmName":…}`. Assertions that read `warnings` from the deploy response read them from the finished Deployment. Assertions on deploy error statuses (`400`/`502`) become `failed` with a `reason` containing the old message.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/httpapi/ ./internal/deployments/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Handlers resolve the Compose with `composes.Service.ComposeRunRequest(ctx, p, e, c)`, which checks parentage, and map `sql.ErrNoRows` to `404`.
  - `coordinatedProject` exempts:
    - `POST …/deployments/{n}/cancel`
    - `GET` under `…/deployments`
    - `POST …/source/test`
    - `GET …/source/dockerfiles`
    - `POST …/source/upload` (Task 8 gates the swap itself)
  - `newTestServer` creates its own `context.WithCancel`, and registers `t.Cleanup(func(){ cancel(); wait() })` **after** the DB cleanup, so the LIFO order stops the worker before the DB closes and `TempDir` is removed. Do not use `t.Context()`: it is canceled before cleanups run, but nothing would wait for the worker.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: enqueue every deploy as a deployment with a live log`

### Task 8: Sources HTTP

**Files:**
- Create: `internal/sources/http.go`
- Modify: `internal/httpapi/httpapi.go` (wire `sources.New(…)`)
- Test: `internal/httpapi/sources_test.go`

**Interfaces:**
- Consumes: Tasks 1, 4 and 5; `Queue.TryLockSource` through `type SourceLocker interface{ TryLockSource(composeID int64) (unlock func(), ok bool) }`.
- Produces: `type Gate interface{ Acquire(ctx context.Context, projectID int64) (func(), error) }` (declared in `sources`, same shape as Task 6's), and `func New(db *sql.DB, composes *composes.Service, git Git, locker SourceLocker, gate Gate) *Service` and `RegisterRoutes(mux)`, serving the spec's `/source` table. The response type never carries `PrivateKey`.

- [ ] **Step 1: Write the failing tests.**
  - PUT a git Source with an empty branch gives `200` with `publicKey` starting `ssh-ed25519 ` and no `privateKey` field. A second PUT keeps the same key.
  - PUT with `https://…` gives `400`, and with branch `-x` gives `400`.
  - `POST /source/key` returns a different key.
  - PUT where an unmarked `src/` exists gives `409` with `src/ already exists in this Compose's directory, move it first`.
  - Upload a small zip (multipart) gives `200` with `kind:"zip"`, `zipName` and `zipFiles`. `src/` holds the files.
  - An upload over 500 MiB is simulated by setting a smaller test cap through an unexported field and expecting `413`.
  - An upload with `../x` gives `400`, and the old `src/` is unchanged.
  - Upload, PUT, DELETE and regenerate while `TryLockSource` fails each give `409` with `A deployment is running`. Use a stub locker.
  - `GET /source/dockerfiles` returns the detected list.
  - `POST /source/known-hosts/reset` gives `204`, and `.ssh/known_hosts` is gone.
  - `DELETE /source` gives `204`. `src/` is gone, `GET /source` gives `404`, and Deployment history remains.
  - `POST /source/test` against a `file://` bare repository, set up by writing the row directly, gives `200 {ok:true, branches:[…]}` and stores `head_sha`. For a repository that does not exist it gives `200 {ok:false, error:…}`.
- [ ] **Step 2: Run them and confirm they fail.** Run `go test ./internal/httpapi/ -run Source`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - The upload uses `http.MaxBytesReader(500<<20)` and `r.MultipartReader()`, and streams the part to `<dir>/.upload-*.zip`.
  - It calls `TryLockSource` before streaming and returns `409` when that fails, then streams outside the Project gate. It takes the lock again just before `ReplaceSrc` (another `409` if that fails) and holds it through the swap. It extracts into `<dir>/.src.new` and removes the temp files on every path. PUT, DELETE and regenerate hold the lock for their whole handler, so `IsRunning` checks are never separate from the action.
  - The upload's swap step takes the Project gate by calling the coordinator. `sources` declares its own `Gate` interface, identical in shape to Task 6's, so it does not import `deployments`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: configure git and zip sources for a compose`

### Task 9: Web data layer

**Files:**
- Modify: `web/package.json` (add `"yaml": "2.9.1"`), `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/data/api.test.ts`, `web/src/data/store.tsx`
- Create: `web/src/data/deployments-view.ts`, `source-view.ts`, `build-insert.ts`, plus a `.test.ts` for each

**Interfaces:**
- Produces (`types.ts`):
  ```ts
  export type DeploymentMode = 'deploy' | 'build' | 'skip-build';
  export type DeploymentStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled';
  export interface Deployment { number: number; mode: DeploymentMode; noCache: boolean; freshVolumes: boolean; status: DeploymentStatus; step: string; steps: string[]; failedStep: string; reason: string; sourceRef: string; sourceSubject: string; warnings: string[]; queuedAt: string; startedAt: string | null; finishedAt: string | null; ahead?: number }
  export interface DeploymentLog { text: string; offset: number; done: boolean }
  export interface DeploymentRequest { mode: DeploymentMode; noCache?: boolean; freshVolumes?: boolean; confirmName?: string }
  export interface ComposeSource { kind: 'git' | 'zip'; repoUrl: string; branch: string; submodules: boolean; publicKey: string; headSha: string; lastTest: { at: string; ok: boolean } | null; zip: { name: string; size: number; files: number; uploadedAt: string } | null }
  export interface DetectedDockerfile { path: string; service: string; context: string; dockerfile: string }
  export interface BuildTarget { service: string; context: string; dockerfile: string; target?: string; replaceImage: boolean }
  ```
- Produces (`api.ts`): `enqueueDeployment(location, req) → {number}`, `listDeployments`, `getDeployment(location, n)`, `deploymentLog(location, n, offset)`, `cancelDeployment`, `deploymentQueue()`, `getSource`, `saveSource`, `deleteSource`, `regenerateKey`, `testSource(location, repoUrl?)`, `resetKnownHosts`, `uploadSource(location, file)`, `detectDockerfiles`. Remove `deployCompose` and `deployComposeFresh`.
- Produces (views):
  ```ts
  stepLabel(step: string): string // "fetch"→"Fetch", "build:api"→"Build api", "deploy"→"Deploy"
  deploymentHeadline(d: Deployment, active?: { composeName: string; number: number; step: string }): string // "Waiting — 2 ahead (shop #14 building)", "Building api", "Failed at Build web"
  isActive(d: Deployment): boolean
  sourceCardState(src: ComposeSource | null, lastBuilt: Deployment | undefined): { kind: 'none' | 'git' | 'zip'; title: string; builtLine: string; newCommits: boolean; notBuilt: boolean }
  insertBuildBlocks(draft: string, targets: BuildTarget[]): string
  ```
- `store.tsx`: `deployCompose(composeId, req)` returns `Promise<number>`, which enqueues and does not patch `warnings`. `deployComposeFresh` is removed and the fresh flow calls `deployCompose` with `freshVolumes` and `confirmName`.

- [ ] **Step 1: Write the failing tests.**
  - `api.test.ts`: `enqueueDeployment` POSTs JSON to `/api/projects/1/environments/2/composes/3/deployments`, and `uploadSource` sends `FormData` with field `file`.
  - `deployments-view.test.ts`: the exact strings above, including `Failed at Build web`, and `Canceled`.
  - `source-view.test.ts`:
    - `newCommits` is true only when `headSha` is non-empty and differs from the last succeeded build's `sourceRef`.
    - For zip, `notBuilt` is true when `uploadedAt` is later than the last build's `finishedAt`.
  - `build-insert.test.ts`:
    - A draft with comments keeps every comment line.
    - A new Service is appended with `build: {context, dockerfile}` and `restart: unless-stopped`.
    - `target` appears only when given.
    - An existing Service with `image:` keeps it unless `replaceImage`.
    - An empty draft yields a valid `services:` document.
- [ ] **Step 2: Run them and confirm they fail.** Run `cd web && pnpm install && pnpm test`. Expected: FAIL.
- [ ] **Step 3: Implement.** `insertBuildBlocks` uses `parseDocument` and `doc.setIn` / `doc.deleteIn` from `yaml`, then `doc.toString()`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `cd web && pnpm test && pnpm typecheck`. Expected: PASS. Type errors in pages are expected until Task 10, so fix only the call sites in `ComposePage.tsx` and `ProjectPage.tsx` to the new `deployCompose` signature here.
- [ ] **Step 5: Commit.** `feat: add deployment and source data to the web client`

### Task 10: Deployments tab and the header button

**Files:**
- Create: `web/src/components/ComposeDeploymentsTab.tsx`
- Modify: `web/src/pages/ComposePage.tsx` (header around lines 460–540 and the tab items around 840–1030), `web/src/pages/ProjectPage.tsx:187`, `web/src/components/RedeployBanner.tsx`

**Interfaces:**
- Consumes: Task 9.
- Produces: `<ComposeDeploymentsTab location compose selected onSelect onFinished />`. `onFinished` fires once when the shown Deployment leaves `isActive`, and ComposePage then runs today's `refreshAfterDeploy` and status refresh.

- [ ] **Step 1: Write the failing component test** `ComposeDeploymentsTab.test.tsx`, following the existing `*.test.tsx` pattern with a mocked `api`.
  - The history renders numbers and status.
  - The `Steps` current item is `Build api` for `step: "build:api"`.
  - The log appends two polled chunks in order.
  - Polling stops after `done: true`, and `onFinished` is called once.
  - Cancel is disabled when `step === "deploy"`.
  - Warnings render in an `Alert`.
- [ ] **Step 2: Run it and confirm it fails.** Run `cd web && pnpm test ComposeDeploymentsTab`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - The tab has key `deployments` and label `Deployments`, after Domains.
  - Log polling runs every 1000 ms with `setTimeout`, cleared on unmount.
  - The header:
    - With a Source, the primary button is `Build & Deploy` (`mode: "build"`). Its menu holds `Deploy without building` (`skip-build`), `Build without cache` (`build`, `noCache`), the existing fresh-volumes item and Restart, Stop and Delete.
    - Without a Source, it is `Deploy` / `Save & Deploy` (`mode: "deploy"`) as today.
    - While the latest Deployment `isActive`, the button text is `deploymentHeadline(…)`, and clicking it opens the tab.
  - Starting any deploy selects the tab and the new number.
  - ProjectPage's deploy shows `message.info("Deployment #<n> queued")`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `cd web && pnpm test && pnpm typecheck`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: show deployments with steps and a live log on the compose page`

### Task 11: Source card, Source modal, Detect dialog and build snippets

**Files:**
- Create: `web/src/components/SourceCard.tsx`, `SourceModal.tsx`, `DetectDockerfilesModal.tsx`, `BuildSnippetsModal.tsx`
- Modify: `web/src/pages/ComposePage.tsx` (right column after the Environment card, around line 800)
- Test: `SourceModal.test.tsx`, `DetectDockerfilesModal.test.tsx`

**Interfaces:**
- Consumes: Task 9 (`sourceCardState`, `insertBuildBlocks`, the API).
- Produces:
  - `<SourceCard … onConfigure onUploadNew onViewDeployments />`
  - `<SourceModal open location source onSaved(source, firstTime) onClose />`
  - `<DetectDockerfilesModal open items existingServices onInsert(targets: BuildTarget[]) onClose />`
  - `<BuildSnippetsModal open onInsert(snippet: 'args' | 'secrets') />`

  ComposePage applies `onInsert` by setting the editor draft to `insertBuildBlocks(draft, targets)`. It never saves.

- [ ] **Step 1: Write the failing tests.**
  - `SourceModal`:
    - In Git mode, a valid URL saves and shows the public key with a copy action and the hint copy.
    - Test connection fills the Branch `Select` from `branches`.
    - `ok:false` shows the error text.
    - In Zip mode, `Upload.Dragger` posts the file and shows `Replaces all code in the Source`.
    - A `409` shows the API message in an `Alert` inside the modal.
  - `DetectDockerfilesModal`:
    - Rows are pre-checked, and Service and Context are editable.
    - "replace image" appears only for names in `existingServices`.
    - `onInsert` receives the edited targets.
- [ ] **Step 2: Run them and confirm they fail.** Run `cd web && pnpm test SourceModal DetectDockerfilesModal`. Expected: FAIL.
- [ ] **Step 3: Implement** to the spec's UI section and the mockups in `.scratch/source-builds/mockups/` (`source-card.html`, `source-and-builds.html`). They predate two spec changes: the "Builds" tab is now **Deployments**, and the "2 new" tag is now **New commits**. The spec wins.
  - After `onSaved(…, firstTime=true)` or the first upload, open Detect with `detectDockerfiles`.
  - The danger area holds `Regenerate key` and `Remove source` (`Popconfirm`), and `Reset host key`.
  - The snippets are the spec's two YAML blocks, inserted with `insertBuildBlocks` for args. Secrets are a text insert at the end of the draft.
- [ ] **Step 4: Run the tests and confirm they pass.** Run `cd web && pnpm test && pnpm typecheck && pnpm build`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: configure a compose's source from the editor tab`, then rebuild `internal/webassets/dist` and commit `chore: rebuild the embedded frontend with sources and deployments`.

### Task 12: Docs and manual verification

**Files:**
- Modify: `docs/sa.md` (the deploy sequence around line 195 and the use-case table), `README.md` (new section "Building from a Source"), `.scratch/source-builds/spec.md` (`Status:` line)

- [ ] **Step 1: Update the docs.**
  - `sa.md`: the sequence becomes UI → `POST /deployments` → `202`, then worker → fetch/build → gate → `up`, then UI polling the log.
  - README:
    - The deploy-key steps.
    - `./src` contexts.
    - Build args and secrets, with the spec's snippets.
    - "Old builds stay as dangling images; `docker image prune` removes them".
    - "Deployments run one at a time on the host".
- [ ] **Step 2: Manual run on the live install.** The user runs this, because they deploy feature branches there. Record the results under `## Comments` in the spec.
  1. A private GitHub repository with two Dockerfiles. Add the deploy key, test the connection, pick a branch, Detect, Insert, then Save and Build & Deploy. Both images are built one after the other and the app answers.
  2. Push a commit. The card shows "New commits" after Test, and Build & Deploy picks the commit up.
  3. Break a Dockerfile. The Deployment ends `failed` at `build:<svc>`, and the old containers are still running.
  4. Start Build & Deploy on two Composes. The second waits with "N ahead".
  5. Upload a GitHub "Download ZIP" of the same repository to a third Compose. The single root is stripped and it builds.
  6. Build secret: a `RUN --mount=type=secret,id=npm_token` step sees a Compose Variable flagged secret.
  7. Restart the panel container mid-build. The Deployment shows `panel restarted`, and the queue continues.
- [ ] **Step 3: Commit.** `docs: document sources and the deployment queue`
