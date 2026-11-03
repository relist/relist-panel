# Build resource limits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator define resource-limited Builders (CPU, memory, max parallel steps), pick a panel default, and choose a Builder per Compose. Every build of that Compose then runs on the chosen Builder.

**Architecture:**
- A new `internal/builders` package owns:
  - three tables;
  - the Builder rules: validation, delete guards, and the busy count that blocks re-create and prune;
  - `/api/builders`.
- It drives the host through one port, `builders.Engine`, implemented in `internal/dockerexec/buildx.go` with the `docker buildx` CLI plus the Engine API.
- `composes` resolves the Compose's Builder at Capture time, into `ExecutionSnapshot.BuilderID`. It serves the per-Compose choice at `.../composes/{id}/builder`.
- `deployments` acquires the Builder for the whole build phase, logs it, and sets `RunRequest.Builder`.
- `dockerexec` turns `RunRequest.Builder` into `BUILDX_BUILDER=<name>` on calls that can build.

**Tech Stack:** Go (stdlib `net/http`, `database/sql`, `modernc.org/sqlite`, goose), the `docker buildx` CLI (buildx 0.38.1 in the panel image), and BuildKit v0.34.2. React + antd 6.5.1, vitest + Testing Library.

**Spec:** `.scratch/build-resource-limits/spec.md`

## Global Constraints

- **Migrations:** never edit committed migrations. The only schema change is the new `internal/migrations/00030_builders.sql`.
- **Default BuildKit image:** `moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe`.
  - It goes in `builders.DefaultBuildKitImage`.
  - The panel-wide setting `builder_settings.buildkit_image` overrides it. Empty means the default.
  - A custom value must match `^[a-z0-9][a-z0-9._/:-]*(:[A-Za-z0-9._-]+)?@sha256:[0-9a-f]{64}$`, the same pattern as `vulnscan.ValidScannerRef`. Anything else is `422 BuildKit image must be repo[:tag]@sha256:<64 hex digits>`.
  - Saving pulls it with a 2-minute timeout. A failure is `502 Could not pull <ref>: <err>`, and the old value is kept.
  - Re-check it before Task 2 is committed: `docker buildx imagetools inspect moby/buildkit:v0.34.2`.
  - Every `buildx create` passes the effective image as `--driver-opt image=…`. An unpinned create pulls `buildx-stable-1`, which was probed.
- **Host names:**
  - builder `relist-builder-<id>`;
  - container `buildx_buildkit_relist-builder-<id>0`;
  - state volume `buildx_buildkit_relist-builder-<id>0_state`.
- **Limits on the container:**
  - `cpu-period=100000`, and `cpu-quota=cpus_milli*100`;
  - `memory` and `memory-swap` both `memory_mb` MiB;
  - `[worker.oci] max-parallelism = N` in a temp `buildkitd.toml`, only when N > 0.
- **Validation:**
  - name: trimmed, 1–40 characters, unique case-insensitively, and not `Docker default`;
  - CPU: `cpus_milli` in steps of 100, from 100 up to the host's `NCPU*1000`;
  - memory: from 256 MB up to the host's `MemTotal` in MiB;
  - max parallel steps: 0 (meaning the BuildKit default) or 1–64.
- **Defaults:** the panel default is Docker default, `default_builder_id` NULL. A Compose with no `compose_builders` row uses the panel default. A snapshot without `BuilderID` (0) builds on Docker default.
- **Copy, verbatim:**
  - log line: `Builder: <name> (<cpus> CPU, <memory>[, <n> parallel steps])`, or `Builder: Docker default (no limits)`.
    - `<cpus>` is `%g` of `cpus_milli/1000`.
    - `<memory>` is `N GB` when `memory_mb` is a multiple of 1024, else `N MB`.
  - start failure: `Could not start Builder <name>: <docker error>`
  - busy: `409 A build is running on this Builder; try again when it finishes`
  - delete the default: `409 This Builder is the panel default; pick another default first`
  - delete in use: `409 These Composes use this Builder: <a>, <b>`
  - delete while queued: `409 A queued or running Deployment uses this Builder`
  - OOM hint: `The build ran out of memory on Builder <name> (<memory>). Raise its memory or pick a larger Builder for this Compose.`
  - local image hint: `<image> exists on this host, but Builders cannot use local images. Pick Docker default for this Compose, or push the image to a registry.`
  - Docker default description: `No CPU or memory limit. When a build uses up the host's memory, the kernel may kill a running app instead of the build. It can build FROM images that exist only on this host.`
  - CPU help: `Tools inside the build still see every host CPU (nproc); the limit slows them down rather than shrinking their thread pools.`
  - image switch: `Builder <name>: switching BuildKit image to <ref>`
  - image switch deferred: `Builder <name> still runs <old ref>; it switches to <ref> once its other builds finish`
- **Commits:** Conventional Commits with no scope, and no `Co-Authored-By` or `Claude-Session` trailers (`docs/agents/commit-conventions.md`).
- **Frontend:** before touching `web/`, read https://ant.design/llms.txt and the pages for Table, Form, InputNumber, Select, Modal, Popconfirm, Tag, Alert and Descriptions. Check them against antd 6.5.1. Follow `docs/GUIDELINES.md`.
- **Tests:** `go test ./...`, `pnpm --dir web test` and `pnpm --dir web typecheck`. `task build` rebuilds `internal/webassets/dist`, which is committed.

## Decisions made while planning (sharpen the spec)

- **Choice storage:** in `compose_builders(compose_id PK, builder_id NULL)`:
  - a row with NULL `builder_id` means Docker default;
  - no row means the panel default.

  The wire form is `{"mode": "default" | "docker" | "builder", "builderId": n}`.
- **The snapshot records the resolved Builder** (`ExecutionSnapshot.BuilderID`, 0 = Docker default). A later panel-default change does not move a queued Deployment.
- **The busy count lives in `builders.Service`,** not in the queue. `Acquire` increments it, and the returned release decrements it. Re-create, prune and delete check it. This is what keeps future concurrent builds safe.
- **Acquire only when the Deployment builds:** `len(snapshot.Services) > 0`, and the plan has a `kindBuild` step or an apply step with `build=true`. A Compose without `build:` never creates a Builder container.
- **Disk usage is the state volume's size** from `GET /system/df?type=volume`. `buildx du` prints humanized strings per record and was not used.
- **There is no Stop action.** **Start** runs Ensure.
- **Image switch happens in Acquire and Start, never on save.**
  - Saving the setting only pulls the image and stores it.
  - The next Acquire or Start of each Builder inspects the container. If its image differs from the effective one and no other build holds the Builder (busy is 0 before this Acquire), it re-creates the Builder with `--keep-state`. Otherwise it runs on the old image and logs the deferred line.
  - A panel update that bumps `DefaultBuildKitImage` takes the same path.
- **"Error" status** comes from `builders.last_error`. A failed Ensure sets it, and a successful one clears it.

## Review Focus

1. **A panel rebuild wipes `~/.docker`.** The first build afterwards must attach to the existing container and keep its cache. Task 3 has `TestAcquireEnsuresEveryTime`, and Task 2 has the manual probe.
2. **A Builder container removed by hand, or by the Docker page's container prune.** The next Acquire must re-create it. Task 2: `Ensure` treats a missing container as "create".
3. **Old queued snapshots without `BuilderID`.** They must build on Docker default and never fail decode. Task 5 has `TestOldSnapshotBuildsOnDockerDefault`.
4. **Lowering memory while a build runs.** If Docker refuses, the stored row must stay unchanged and the error must surface. Task 3 has `TestUpdateKeepsRowWhenDockerRefuses`.
5. **A panel update bumps the default image while Builders exist.** Each one must switch on its next build, keep its cache, and never switch under another running build. Task 3 has `TestAcquireSwitchesImageWhenIdle` and `TestAcquireDefersImageSwitchWhenBusy`.
6. **A Compose with no `build:` Service.** It must never touch the Builder, even if its choice names one. Task 5 has `TestNoBuildServicesNeverAcquires`.

---

### Task 1: Builder tables and store

**Files:**
- Create: `internal/migrations/00030_builders.sql`
- Create: `internal/builders/store.go`
- Test: `internal/builders/store_test.go`

**Interfaces:**
- Produces:
  - `type Builder struct { ID int64; Name string; CPUsMilli int; MemoryMB int; MaxParallelism int; LastError string; CreatedAt, UpdatedAt time.Time }`
  - `type Choice struct { Mode string; BuilderID int64 }`, where Mode is `"default"`, `"docker"` or `"builder"`
  - `const DockerDefault int64 = 0`
  - `NewStore(db *sql.DB) *Store`
  - `(*Store).List(ctx) ([]Builder, error)`, ordered by name
  - `Get(ctx, id) (Builder, error)`, which returns `sql.ErrNoRows` when missing
  - `Create(ctx, b Builder, now) (Builder, error)`
  - `Update(ctx, b Builder, now) error`
  - `Delete(ctx, id) error`
  - `SetLastError(ctx, id, msg string) error`
  - `DefaultID(ctx) (int64, error)`, which returns 0 for Docker default
  - `SetDefault(ctx, id int64) error`, where 0 means Docker default
  - `BuildKitImage(ctx) (string, error)`, where `''` means the default
  - `SetBuildKitImage(ctx, ref string) error`
  - `ComposeChoice(ctx, composeID) (Choice, error)`
  - `SetComposeChoice(ctx, composeID, c Choice) error`, which returns `ErrUnknownBuilder` for a missing builder or an unknown mode
  - `Resolve(ctx, composeID) (int64, error)`
  - `UsedBy(ctx, id) ([]string, error)`: Compose names, ordered

- [ ] **Step 1: Write the migration**

  It creates three tables:
  - `builders`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `name TEXT NOT NULL UNIQUE COLLATE NOCASE`, `cpus_milli INTEGER NOT NULL`, `memory_mb INTEGER NOT NULL`, `max_parallelism INTEGER NOT NULL DEFAULT 0`, `last_error TEXT NOT NULL DEFAULT ''`, `created_at INTEGER NOT NULL` and `updated_at INTEGER NOT NULL`.
  - `builder_settings`: `id INTEGER PRIMARY KEY CHECK (id = 1)`, `default_builder_id INTEGER REFERENCES builders(id) ON DELETE RESTRICT` and `buildkit_image TEXT NOT NULL DEFAULT ''`. The migration inserts row 1 with NULL and `''`.
  - `compose_builders`: `compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE` and `builder_id INTEGER REFERENCES builders(id) ON DELETE RESTRICT`.

  The Down drops all three. A header comment points at the spec, in the style of `00029_access_log_settings.sql`.

- [ ] **Step 2: Write the failing tests**

  Use the in-memory DB helper the other store tests use.
  - `TestResolveFallsBackToPanelDefault`:
    - no row and default NULL → `0`;
    - `SetDefault(b.ID)` → `b.ID`;
    - `SetComposeChoice({Mode:"docker"})` → `0` while the default is still `b.ID`;
    - `{Mode:"builder", BuilderID: c.ID}` → `c.ID`.
  - `TestComposeChoiceRoundTrip`: all three modes come back as set, and no row reads as `{Mode:"default"}`.
  - `TestUsedByListsComposeNames`: two Composes choose `b`, and the result is their names sorted.
  - `TestDeleteRestrictedByForeignKey`: `Delete` of a Builder a Compose uses returns an error, which is the RESTRICT backstop.
  - `TestComposeDeleteCascadesChoice`.
  - `TestSetComposeChoiceUnknownBuilder`: returns `ErrUnknownBuilder`.
  - `TestBuildKitImageRoundTrip`: it starts as `''`, and a set then a get round-trips.

- [ ] **Step 3: Run the tests to verify they fail**

  Run: `go test ./internal/builders/ -run 'Resolve|Choice|UsedBy|Delete|Cascade' -v`
  Expected: FAIL (package does not compile)

- [ ] **Step 4: Implement `store.go`**

  Times are Unix seconds, as in the other stores.

- [ ] **Step 5: Run the tests to verify they pass**

  Run: `go test ./internal/builders/ ./internal/migrations/ -v`
  Expected: PASS

- [ ] **Step 6: Commit** `feat: add Builder tables and store`

### Task 2: buildx engine in dockerexec

**Files:**
- Create: `internal/builders/engine.go`, which holds the port and `HostSpec`
- Create: `internal/dockerexec/buildx.go`
- Test: `internal/dockerexec/buildx_test.go`

**Interfaces:**
- Produces, in `internal/builders/engine.go`:
  - `const DefaultBuildKitImage = "moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe"`
  - `func ValidImageRef(ref string) bool`
  - `func EffectiveImage(setting string) string`, which returns `DefaultBuildKitImage` when the setting is `''`
  - `type HostSpec struct { Name, Image string; CPUQuota, CPUPeriod, MemoryBytes int64; MaxParallelism int }`
  - `func SpecFor(b Builder, image string) HostSpec`, which uses `HostName(b.ID)`, period 100000, quota `CPUsMilli*100` and memory `MemoryMB<<20`
  - `func HostName(id int64) string`, which returns `relist-builder-<id>`
  - `type ContainerState string`, with the constants `StateMissing = "missing"`, `StateRunning = "running"` and `StateStopped = "stopped"`
  - `type Inspection struct { State ContainerState; Image string }`, where `Image` is the container's `Config.Image` and is empty when the container is missing
  - the `Engine` interface:
    - `Ensure(ctx, HostSpec) error`
    - `Recreate(ctx, HostSpec) error`
    - `Remove(ctx, name string) error`
    - `Inspect(ctx, name string) (Inspection, error)`
    - `Pull(ctx, ref string) error`
    - `DiskUsage(ctx, name string) (int64, error)`
    - `Prune(ctx, name string) error`
    - `Capacity(ctx) (cpus int, memoryBytes int64, err error)`
    - `ImageExists(ctx, ref string) (bool, error)`
- Produces, in `dockerexec`: `NewBuildx(socketPath string) *Buildx`, which implements `builders.Engine`.

- [ ] **Step 1: Write the failing argv tests**

  Use the package's `command` stand-in, as in `dockerexec_test.go:63`, to record argv.
  - `TestEnsureCreateArgs`: for `HostSpec{Name:"relist-builder-3", Image:DefaultBuildKitImage, CPUQuota:150000, CPUPeriod:100000, MemoryBytes:2<<30, MaxParallelism:4}`, Ensure runs:
    1. `buildx create --name relist-builder-3 --driver docker-container --driver-opt image=<spec.Image>,memory=2147483648,memory-swap=2147483648,cpu-quota=150000,cpu-period=100000 --buildkitd-config <tmp>`;
    2. then `buildx inspect --bootstrap relist-builder-3`.

    The temp toml contains `max-parallelism = 4`, and it is removed afterwards.
  - `TestEnsureNoConfigWithoutParallelism`: with `MaxParallelism: 0`, no `--buildkitd-config` is passed.
  - `TestEnsureUpdatesDriftedLimits`: when the Engine API inspect of container `buildx_buildkit_relist-builder-30` reports a different `Memory` or `CpuQuota`, Ensure runs `update --cpu-quota 150000 --cpu-period 100000 --memory 2147483648 --memory-swap 2147483648 buildx_buildkit_relist-builder-30`. When they match, no `update` runs. Serve inspect from an `httptest` unix-socket server, the way the other socket-backed tests do.
  - `TestRecreateKeepsState`: runs `buildx rm --keep-state relist-builder-3` before the create.
  - `TestRemoveDropsState`: runs `buildx create …` (to attach), then `buildx rm relist-builder-3` without `--keep-state`. If the container and the instance are both absent, it returns nil.
  - `TestDiskUsageReadsStateVolume`: `/system/df?type=volume` returns volume `buildx_buildkit_relist-builder-30_state` with `UsageData.Size` 4172000000, and `DiskUsage` returns 4172000000. A missing volume returns 0.
  - `TestInspect`: 404 → `StateMissing` with an empty Image. `Running:true` → `StateRunning`, otherwise `StateStopped`. Image is `Config.Image`.
  - `TestPullArgs`: runs `pull <ref>`.

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `go test ./internal/dockerexec/ -run 'Ensure|Recreate|Remove|DiskUsage|Inspect|Pull' -v`
  Expected: FAIL

- [ ] **Step 3: Implement `buildx.go`**

  - Run CLI calls with the same `command` var and the stderr-tail error style as `run` in `dockerexec.go`.
  - Engine API calls (inspect, `/info` for `NCPU`/`MemTotal`, `/system/df`, `/images/{ref}/json` for `ImageExists`) go over `socketPath`, the same way `NewDockerHost` does.
  - `Prune` runs `buildx prune -af --builder <name>`.

- [ ] **Step 4: Run the tests to verify they pass.** Same command. Expected: PASS.

- [ ] **Step 5: Manual probe against the dev Docker**

  1. Write a throwaway `main` under the session scratchpad, not the repo, that calls `NewBuildx(DefaultSocketPath)`. Run Ensure on `relist-builder-999` with 1 CPU and 1 GB.
  2. Check `docker inspect buildx_buildkit_relist-builder-9990 --format '{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}} {{.Config.Image}}'`. Expected: `1073741824 1073741824 moby/buildkit:v0.34.2@sha256:c5f8…`. Then call `Recreate` with another valid digest of `moby/buildkit` (for example `v0.34.1`) and check that the image changed and a cached step still hits.
  3. `docker rm -f` the container, run Ensure again, and confirm that it is running again.
  4. Run `Remove` and confirm that the container and the volume are gone.

- [ ] **Step 6: Commit** `feat: drive buildx Builders through the Docker CLI`

### Task 3: Builder service and API

**Files:**
- Create: `internal/builders/service.go`, `internal/builders/http.go`, `internal/builders/hints.go`
- Test: `internal/builders/service_test.go`, `internal/builders/http_test.go`, `internal/builders/hints_test.go`, and `internal/builders/fakes_test.go` (a fake `Engine` that records calls and can be made to fail)

**Interfaces:**
- Consumes: Task 1 `Store`, and Task 2 `Engine`/`HostSpec`/`SpecFor`.
- Produces:
  - `type Deployments interface { UsesBuilder(ctx context.Context, id int64) (bool, error) }`, implemented in Task 5
  - `New(Deps{Store *Store; Engine Engine; Deployments Deployments; Now func() time.Time}) *Service`
  - `(*Service).Acquire(ctx context.Context, id int64, out io.Writer) (hostName string, release func(), err error)`:
    - id 0 writes the Docker default log line and returns `""` with a no-op release;
    - otherwise it waits while a re-create of that Builder is in progress (ctx-aware), notes whether busy was 0, and increments busy;
    - it calls `Inspect`. If the container exists with an image other than `EffectiveImage(setting)`: with busy 0 before, it writes the switch line and calls `Recreate`; otherwise it writes the deferred line and calls `Ensure` with the container's current image. In every other case it calls `Ensure`;
    - it writes the log line, and records or clears `last_error`;
    - on an Ensure error it decrements busy and returns `Could not start Builder <name>: <err>`.
  - `(*Service).SetBuildKitImage(ctx, ref string) error`:
    - `''` stores the default without pulling;
    - an invalid ref returns `ErrInvalidImage`;
    - otherwise it calls `Pull` with a 2-minute timeout and stores the ref only if the pull succeeds. A failure returns `*PullError`.
  - `(*Service).FailureHint(ctx context.Context, id int64, buildErr error) string`, which returns `""` for id 0 or when no hint applies
  - `(*Service).RegisterRoutes(mux *http.ServeMux)`

- [ ] **Step 1: Write the failing service tests**

  - `TestAcquireEnsuresEveryTime`: two Acquires call `Ensure` twice with `SpecFor(b, DefaultBuildKitImage)`. Both write `Builder: large (2 CPU, 4 GB, 4 parallel steps)`.
  - `TestAcquireSwitchesImageWhenIdle`: `Inspect` reports an old image. Acquire calls `Recreate` with the new image and logs `Builder large: switching BuildKit image to <ref>`.
  - `TestAcquireDefersImageSwitchWhenBusy`: a second Acquire while the first is held sees the old image. It calls `Ensure` with the old image, not `Recreate`, and logs the deferred line.
  - `TestAcquireMissingContainerCreatesWithSetting`: `StateMissing` → `Ensure` with the effective image.
  - `TestSetBuildKitImage`: an invalid ref → `ErrInvalidImage` and no pull. A failed pull → `PullError`, and the setting is unchanged. `''` → stored with no pull.
  - `TestAcquireDockerDefault`: no engine calls, and the line is `Builder: Docker default (no limits)`.
  - `TestLogLineMemoryUnits`: 1536 MB → `1536 MB`, 2048 → `2 GB`, 0.5 CPU → `0.5 CPU`. MaxParallelism 0 leaves out the parallel part.
  - `TestAcquireFailureRecordsLastError`: Ensure fails with `boom`. The error is `Could not start Builder large: boom`, `last_error` is `boom`, and busy is back to 0. A later successful Acquire clears `last_error`.
  - `TestUpdateKeepsRowWhenDockerRefuses`: `Inspect` says the container exists, the edit changes memory, `Ensure` returns an error, and the row in the store is unchanged. Update applies limits before it saves the row, and only when `State` is not `missing`.
  - `TestUpdateOfUncreatedBuilderOnlySaves`: when `Inspect` reports `StateMissing`, the Update calls neither Ensure nor Recreate.
  - `TestParallelismEditRefusedWhileBusy`: during an Acquire, an Update that changes MaxParallelism returns `ErrBusy`. After release, it calls `Recreate`.
  - `TestCPUMemoryEditAllowedWhileBusy`: calls Ensure, not Recreate.
  - `TestDeleteGuards`: in order, the default, used-by (with the names) and `Deployments.UsesBuilder` each refuse. Otherwise it calls `Engine.Remove(HostName(id))` and then `Store.Delete`.
  - `TestPruneRefusedWhileBusy`.
  - `TestValidate`: one case per bound in Global Constraints (name length, `Docker default` in any case, duplicate name, cpus 50/1550/over NCPU, memory 255/over MemTotal, parallelism -1/65). Capacity comes from the fake Engine.

- [ ] **Step 2: Write the failing hint tests (`hints_test.go`)**

  - `TestOOMHint`: an error containing `cannot allocate memory`, or `exit code: 137`, gives the OOM hint copy with `(4 GB)`.
  - `TestLocalImageHint`: the error is `failed to solve: relist-exp-base:local: failed to resolve source metadata for docker.io/library/relist-exp-base:local: pull access denied`.
    - With `ImageExists("docker.io/library/relist-exp-base:local")` true, the result is the local image copy with `<image>` = `relist-exp-base:local`. Strip `docker.io/library/` for display.
    - With false, the result is `""`.
  - `TestNoHintForDockerDefault`.

- [ ] **Step 3: Run the tests to verify they fail**

  Run: `go test ./internal/builders/ -v`
  Expected: FAIL

- [ ] **Step 4: Implement `service.go` and `hints.go`**

  - Keep `mu sync.Mutex` and `state map[int64]*runtime{busy int; changing chan struct{}}`. Recreate sets `changing`, and Acquire waits on it.
  - The image regexp is `failed to resolve source metadata for (\S+?):\s`.
  - Export `ErrBusy`, `ErrIsDefault`, `ErrQueued` and `type InUseError struct{ Composes []string }` for the handler to map.

- [ ] **Step 5: Write the failing HTTP tests (`http_test.go`)**

  The routes:
  - `GET /api/builders` returns `{"defaultBuilderId":0,"buildkitImage":"","defaultBuildkitImage":"moby/buildkit:v0.34.2@sha256:…","capacity":{"cpus":16,"memoryMb":31900},"builders":[{"id":1,"name":"large","cpus":2,"memoryMb":4096,"maxParallelism":4,"state":"running","lastError":"","usedBy":["web"]}]}`.
  - `POST /api/builders` → 201 with the builder.
  - `PUT /api/builders/{id}` → 200.
  - `DELETE /api/builders/{id}` → 204.
  - `PUT /api/builders/default` with `{"builderId":0|n}` → 204. An unknown id is 422.
  - `PUT /api/builders/settings` with `{"buildkitImage":"…"}` → 204. `ErrInvalidImage` → 422, and `PullError` → 502, both with the Global Constraints copy.
  - `POST /api/builders/{id}/start` → 200 with the builder, or 502 with `Could not start Builder …`.
  - `GET /api/builders/{id}/disk-usage` → `{"bytes":n}`.
  - `POST /api/builders/{id}/prune` → 204.

  The errors:
  - validation → 422 `{"error": "...", "field": "name|cpus|memoryMb|maxParallelism"}`;
  - `ErrBusy`/`ErrIsDefault`/`ErrQueued`/`InUseError` → 409 with the Global Constraints copy;
  - unknown id → 404.

  Wire `cpus` is a number (`cpus_milli/1000`). The handler rounds `cpus*1000` and rejects a value that is not a multiple of 100.

- [ ] **Step 6: Implement `http.go`**

  Use local `writeJSON`/`writeError`, as `vulnscan/http.go:39` does.

- [ ] **Step 7: Run all builders tests**

  Run: `go test ./internal/builders/ -v`
  Expected: PASS

- [ ] **Step 8: Commit** `feat: manage Builders with limits, guards and an API`

### Task 4: Builder choice in composes and BUILDX_BUILDER in dockerexec

**Files:**
- Modify: `internal/composes/composes.go`:
  - add `RunRequest.Builder string`, commented as "buildx builder name; empty is Docker default; only build-capable calls use it";
  - add the `builderChoices` field and `WithBuilders`;
  - register the routes near line 194.
- Create: `internal/composes/builder_http.go`
- Modify: `internal/composes/snapshot.go`:
  - add `ExecutionSnapshot.BuilderID int64`;
  - Capture resolves it;
  - `stageSnapshot` at line 67 copies `t.Request.Builder` into the staged request.
- Modify: `internal/dockerexec/dockerexec.go`: `run` gains the builder env. `Build` always passes `req.Builder`, and `Up`/`Scale` pass it only when `opts.Build`.
- Test: `internal/composes/builder_http_test.go`, `internal/composes/snapshot_test.go`, `internal/dockerexec/dockerexec_test.go`

**Interfaces:**
- Consumes: Task 1 `builders.Choice`, `(*builders.Store).ComposeChoice/SetComposeChoice/Resolve/Get`.
- Produces:
  - `type builderChoices interface { ComposeChoice(ctx, int64) (builders.Choice, error); SetComposeChoice(ctx, int64, builders.Choice) error; Resolve(ctx, int64) (int64, error) }`
  - `(*Service).WithBuilders(b builderChoices) *Service`. When it is nil, Capture leaves `BuilderID` 0 and the routes return 404.
  - `GET` and `PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/builder`, with body `{"mode":"default|docker|builder","builderId":n}`. PUT returns 204, and 422 for an unknown builder or mode.
  - `ExecutionSnapshot.BuilderID`
  - `RunRequest.Builder`

- [ ] **Step 1: Write the failing tests**

  - `TestCaptureRecordsResolvedBuilder`: a fake choices with `Resolve` → 7 gives `t.Snapshot.BuilderID == 7`.
  - `TestStageSnapshotCarriesBuilder`: `prepareSnapshotBuild` with `t.Request.Builder = "relist-builder-7"` returns `Request.Builder == "relist-builder-7"`.
  - `TestComposeBuilderRoutes`: a GET with no row returns `{"mode":"default","builderId":0}`. A PUT followed by a GET round-trips. A PUT with an unknown builder is 422. An unknown Compose is 404.
  - In `dockerexec_test.go`, `TestBuilderEnvOnlyOnBuildCapableCalls`: the stand-in records `cmd.Env`.
    - `Build` with `Builder:"relist-builder-7"` includes `BUILDX_BUILDER=relist-builder-7`.
    - `Up` with `Build:true` includes it.
    - `Up` with `Build:false` and `Status` do not.
    - `Build` with `Builder:""` does not.

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `go test ./internal/composes/ ./internal/dockerexec/ -run 'Builder|StageSnapshot' -v`
  Expected: FAIL

- [ ] **Step 3: Implement**

  `run` takes the builder name as a new parameter after `out`. When it is non-empty, set `cmd.Env = append(os.Environ(), "BUILDX_BUILDER="+name)`.

- [ ] **Step 4: Run the tests to verify they pass**

  Run: `go test ./internal/composes/ ./internal/dockerexec/`
  Expected: PASS

- [ ] **Step 5: Commit** `feat: record each Compose's Builder and pass it to buildx`

### Task 5: Acquire the Builder in Deployments, and wire it up

**Files:**
- Modify: `internal/deployments/queue.go`:
  - `Deps.Builders BuilderPool`;
  - acquire in `runSteps` (line 753);
  - append the hint in the build `stepFailed` path at line 870, and on deploy/rollout failures when `build=true`.
- Modify: `internal/deployments/store.go`: add `UsesBuilder`.
- Modify: `internal/httpapi/httpapi.go`:
  - add `Deps.Buildx builders.Engine`;
  - build `builders.New` when it is set;
  - `composesService.WithBuilders(store)`;
  - `RegisterRoutes`.
- Modify: `main.go:177`: `Buildx: dockerexec.NewBuildx(dockerexec.DefaultSocketPath)`.
- Test: `internal/deployments/queue_test.go`, `internal/deployments/store_test.go`

**Interfaces:**
- Consumes: Task 3 `Acquire`/`FailureHint`/`Deployments`, and Task 4 `ExecutionSnapshot.BuilderID`/`RunRequest.Builder`.
- Produces:
  - `type BuilderPool interface { Acquire(ctx context.Context, id int64, out io.Writer) (string, func(), error); FailureHint(ctx context.Context, id int64, err error) string }`. A nil `Deps.Builders` behaves as Docker default with no log line, so existing tests stay unchanged.
  - `(*Store).UsesBuilder(ctx, id int64) (bool, error)`: true when a `queued` or `running` Deployment's snapshot has `json_extract(snapshot, '$.Files.BuilderID') = ?`.

- [ ] **Step 1: Write the failing tests**

  - `TestBuildRunsOnSnapshotBuilder`:
    - a Source Compose with `BuilderID` 7 and a fake pool returning `relist-builder-7`;
    - the fake Builder sees `req.Builder == "relist-builder-7"`;
    - the log contains the pool's line before `==> build:web`;
    - release is called once, after the deploy step.
  - `TestUpBuildRunsOnSnapshotBuilder`: a Compose without a Source and with a `build:` Service, in ModeDeploy. The runner's `Up` sees `Request.Builder` set.
  - `TestNoBuildServicesNeverAcquires`: the snapshot has empty `Services` and `BuilderID` 7. The pool is never called.
  - `TestOldSnapshotBuildsOnDockerDefault`: decode a stored snapshot JSON without `BuilderID`. Acquire is called with 0.
  - `TestAcquireFailureFailsDeployment`: the pool errors with `Could not start Builder large: boom`. The status is failed, and the reason is that message.
  - `TestBuildFailureAppendsHint`: the fake Builder fails, and `FailureHint` returns `X`. The log ends with `X`.
  - `TestUsesBuilder`: a queued snapshot with `BuilderID` 7 → true for 7 and false for 8. A succeeded one → false.

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `go test ./internal/deployments/ -run 'Builder|Acquire|Hint|OldSnapshot' -v`
  Expected: FAIL

- [ ] **Step 3: Implement**

  - In `runSteps`, after the `p.fail` check:
    1. if the Deployment builds (see Decisions), call `Acquire(j.tctx, snapshot.BuilderID, j.out)`;
    2. set `j.target.Request.Builder` to the returned name;
    3. `defer release()`.
  - Wire `builders.Deps{Deployments: queue.Store()}`, or pass the `*deployments.Store` that httpapi already builds.

- [ ] **Step 4: Run all Go tests**

  Run: `go test ./...`
  Expected: PASS

- [ ] **Step 5: Commit** `feat: build each Deployment on its Compose's Builder`

### Task 6: Builders page

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts` (inside `api` at line 259)
- Create: `web/src/pages/BuildersPage.tsx`, `web/src/pages/builders-view.ts`
- Modify: `web/src/app/routes.tsx:35`, `web/src/app/paths.ts`, `web/src/app/AppShell.tsx`:
  - the menu item `{ key: 'builders', icon: <BuildOutlined />, label: 'Builders' }` goes after Images;
  - `onMenuClick`;
  - the breadcrumb.
- Test: `web/src/pages/BuildersPage.test.tsx`, `web/src/pages/builders-view.test.ts`

**Interfaces:**
- Consumes: Task 3 routes and JSON.
- Produces:
  - the type `Builder`, as in Task 3's GET (`state: 'running' | 'stopped' | 'missing'`);
  - `BuildersResponse`;
  - `api.listBuilders`, `setBuildKitImage(ref)`, `createBuilder`, `updateBuilder`, `deleteBuilder`, `setDefaultBuilder(builderId)`, `startBuilder`, `builderDiskUsage`, `pruneBuilder`;
  - in `builders-view.ts`, `formatLimits(b): string` (the same text as the log line, without `Builder: `) and `builderStatus(b): { label: string; color: string }`. A non-empty `lastError` → `Error`.

- [ ] **Step 1: Write the failing tests**

  - `builders-view.test.ts`: `formatLimits` cases matching Task 3's `TestLogLineMemoryUnits`, and `builderStatus` for each state plus lastError.
  - `BuildersPage.test.tsx`:
    - the first row is `Docker default`, has no Edit or Delete, and shows the Docker default description copy;
    - the default row carries a `Default` tag;
    - **Set as default** calls `setDefaultBuilder`;
    - a 409 from delete shows the server message in an Alert;
    - disk usage loads after the table renders (the table is visible while `builderDiskUsage` is pending);
    - the CPU field shows the CPU help copy;
    - the form rejects a CPU of 0.05 before submit;
    - the BuildKit image field shows `Default` when the field is empty. **Use default** clears it, and **Save** PUTs `buildkitImage`. A 502 shows the server message.

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `pnpm --dir web test -- builders`
  Expected: FAIL

- [ ] **Step 3: Implement the page**

  - Above the table, the **BuildKit image** Input has a placeholder of the default ref, plus **Use default** and **Save** buttons.
  - A Table with the columns Name, Limits, Status, Cache, Used by and actions (Start, Prune cache in a Popconfirm, Edit, Delete in a Popconfirm, Set as default).
  - Add and Edit open a Modal Form: Name Input, CPU `InputNumber step=0.1`, Memory `InputNumber` in MB with an addon, and Max parallel steps `InputNumber` with an "Empty = BuildKit default" placeholder.
  - Bounds come from `capacity`.

- [ ] **Step 4: Run the tests and the typecheck**

  Run: `pnpm --dir web test && pnpm --dir web typecheck`
  Expected: PASS

- [ ] **Step 5: Commit** `feat: add the Builders page`

### Task 7: Builder select on the Compose page, Docker page copy, embedded build

**Files:**
- Modify: `web/src/data/api.ts`: add `getComposeBuilder(location)` and `setComposeBuilder(location, choice)`, next to `listZeroDowntime` at line 467.
- Modify: `web/src/components/ComposeDeploymentsTab.tsx`: add a `Builder` Select above the history. It is shown only when `yamlServices(compose.yamlContent, true)` (in `web/src/data/source-view.ts:46`) is not empty.
- Modify: `web/src/pages/DockerPage.tsx:55`: the Prune build cache description adds `Covers Docker default only. Builders have their own caches on the Builders page.`, with a link to `/builders`.
- Modify: `internal/webassets/dist/*` through `task build`.
- Test: `web/src/components/ComposeDeploymentsTab.test.tsx`

**Interfaces:**
- Consumes: Task 4 routes, and Task 6 `api.listBuilders`/`Builder`.

- [ ] **Step 1: Write the failing tests**

  - The Select offers `Use panel default (<default name>)`, `Docker default` and each Builder. Choosing one PUTs `{mode:'builder', builderId}`.
  - The Select is hidden for a Compose without `build:`.
  - A failed PUT shows an error and reverts the Select.

- [ ] **Step 2: Run the tests to verify they fail**

  Run: `pnpm --dir web test -- ComposeDeploymentsTab`
  Expected: FAIL

- [ ] **Step 3: Implement the Select and the Docker page copy**

- [ ] **Step 4: Run the checks and rebuild the embedded frontend**

  Run: `pnpm --dir web test && pnpm --dir web typecheck && task build`
  Expected: PASS, and `internal/webassets/dist` changes.

- [ ] **Step 5: Commit**

  - `feat: choose a Builder per Compose`
  - then `chore: rebuild the embedded frontend`

### Task 8: Domain docs and end-to-end check

**Files:**
- Create: `docs/adr/0028-builds-on-panel-managed-builders.md`
- Modify: `CONTEXT.md`: the Image exceptions, a new **Builder** entry, and the Deployment sentence, all per the spec's Domain changes.

- [ ] **Step 1: Write the ADR**

  Follow the other ADRs' format. It records:
  - the `docker-container` driver;
  - the database as the source of truth, with ensure-on-use;
  - no per-build `docker update`, because of concurrency;
  - the pinned BuildKit image as a pull exception, with the panel-wide override and the switch on next use;
  - the privileged container, and why it adds nothing beyond the socket;
  - the local-image limitation.

  Copy the probe numbers from the spec.

- [ ] **Step 2: Update `CONTEXT.md`**

  Use the spec's wording, with _Avoid_: Runner, Worker, Build agent.

- [ ] **Step 3: End-to-end on the dev panel**

  1. Rebuild the dev panel from this branch.
  2. Create a Builder `small` with 0.5 CPU and 512 MB, and point a test Compose at it. Use a Compose with a Source whose Dockerfile runs `head -c 900m /dev/zero | tail`.
  3. Deploy. Expected:
     - the log shows `Builder: small (0.5 CPU, 512 MB)`;
     - the build fails;
     - the OOM hint appears.
  4. Switch the Compose to Docker default and deploy again. Expected: success.
  5. Delete `small`. Expected: the 409 used-by copy names the Compose.
  6. Set the BuildKit image to `moby/buildkit:v0.34.1@sha256:<digest from docker buildx imagetools inspect moby/buildkit:v0.34.1>`. Expected: the save succeeds after a pull.
  7. Deploy on a second Builder. Expected: the log shows `switching BuildKit image to …`, and an unchanged step is still `CACHED`. Then press **Use default** and save.
  8. Set the Compose back to the panel default, delete `small` again, and confirm that `docker ps -a` and `docker volume ls` show no `relist-builder-` leftovers.

- [ ] **Step 4: Commit** `docs: record Builders in the ADRs and glossary`
