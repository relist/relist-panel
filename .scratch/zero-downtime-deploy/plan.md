# Zero-downtime Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plan every Deployment before running it (Part 0, no behaviour change), then let a Service with a Domain opt into a rollout that replaces its Container with no downtime (Part 1).

**Architecture:** Part 0 adds a pure `planDeployment` in `internal/deployments`. The plan has two lists: `prepare` steps, which run outside the Project gate and can be canceled, and `apply` steps, which run inside the gate and are shielded. The worker only loops over them. Part 1 adds a `zerodowntime` package (store and pure checks over `docker compose config --format json`) and a `ContainerEngine` port implemented over the Docker socket. It also adds `composes.RunRollout` and a `rollout:<service>` apply step. Labels are injected per Domain, and a transport file goes into Traefik's file provider.

**Tech Stack:** Go 1.x stdlib + `gopkg.in/yaml.v3`, SQLite with goose migrations, Docker Compose v5.5.1 CLI, Docker Engine API over `/var/run/docker.sock`, Traefik v3.7. Web: React, antd 6.5.1, vitest.

**Spec:** `.scratch/zero-downtime-deploy/spec.md`. Read it before any task. This plan argues from it and does not repeat its rationale.

## Global Constraints

- Commits: Conventional Commits with no scope (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`), and **no** `Co-Authored-By` or `Claude-Session` trailers (`docs/agents/commit-conventions.md`).
- Never edit a committed migration. The new one is `internal/migrations/00024_zero_downtime_services.sql`.
- Part 0 is its own PR and must not change behaviour. Its only visible difference is that the full step list is stored from the start.
- Before touching `web/`, read `https://ant.design/llms.txt` and the Switch, Modal, Alert and Typography pages. Check every prop against antd **6.5.1** (`web/package.json`). Follow `docs/GUIDELINES.md`.
- Retry middleware: `traefik.http.middlewares.<n>-retry.retry.attempts=4`, placed **last** in the router chain. It never goes on the `-http` redirect router. `retryNonIdempotentMethod` is never set.
- Transport label: `traefik.http.services.<n>.loadbalancer.serversTransport=relist-fastdial@file`.
- Transport file: `<data dir>/.traefik/dynamic/relist-transports.yml` with `dialTimeout: 1s`. It is written at startup and at every Deploy step that injects the label, written atomically (temp file + rename), and never deleted.
- Wait limit: `start_period + (interval + timeout) × retries + 10s`. Docker defaults: interval 30s, timeout 30s, retries 3, start_period 0. It is capped by the Deploy step's deadline.
- Overlap wait before stopping OLD: 3s, and none in drift mode. Health is polled every 1s.
- Cleanup Engine calls (remove NEW, stop and remove OLD, orphan removal) run on `context.WithoutCancel` with their own timeout: 30s, or `StopTimeout + 30s` for a stop.
- Stopping OLD sends no `t`. Removing OLD uses `v=false`. Removing NEW uses `force=true&v=true`.
- Anonymous volume = a mount of type `volume` whose volume carries the label `com.docker.volume.anonymous`. This was verified on Docker 29.8.0.
- Only a `healthcheck` in the YAML counts. An image `HEALTHCHECK` with none in the YAML is refused, as the spec's table says.
- The exact reason strings are listed in Task 4. The exact UI copy is listed in Tasks 12–14.

## Review Focus

1. **Deadline reached during the health wait**: NEW must still be removed and OLD keep serving. Test in Task 8 (`TestRolloutDeadlineStillRemovesNew`).
2. **A row left for a Service that was renamed or lost its Domain**: the deploy behaves as if the switch were off. No labels, no rollout, no 422. Tests in Task 7 (`TestCaptureIgnoresRowWithoutDomain`) and Task 4.
3. **`compose config` fails at enqueue** (bad interpolation, missing env): the request must answer 400 with Compose's first error line, not 500 and not 422. Test in Task 9 (`TestEnqueueConfigFailureIs400`).
4. **Two OLD Containers after a crash**: scale to 3 and remove both. Test in Task 8 (`TestRolloutTwoOldContainers`).
5. **A Project deploy where one Compose is refused**: the others still queue. Test in Task 14 (`ProjectPage deploy continues after a zero-downtime refusal`).

---

# Part 0 — Plan the Deployment, then run it (PR 1)

### Task 1: `planDeployment`

**Files:**
- Create: `internal/deployments/plan.go`
- Test: `internal/deployments/plan_test.go`

**Interfaces:**
- Produces:
  ```go
  type stepKind int
  const (
      kindFetch stepKind = iota
      kindBuild
      kindDeploy
  )
  type step struct {
      kind         stepKind
      name         string // "fetch", "build:<svc>", "deploy"
      service      string // kindBuild
      noCache      bool   // kindBuild
      build        bool   // kindDeploy: --build
      freshVolumes bool   // kindDeploy
      note         string // logged right after "==> <name>"
  }
  type plan struct {
      fail          *outcome // settle with this; steps (if any) are still stored first
      usesSource    bool     // prepare holds the Source lock, records the Source and materialization
      prepare       []step
      afterPrepare  string   // logged after the last prepare step ("no Service has build:")
      apply         []step
  }
  type planInput struct {
      mode          Mode
      noCache       bool
      freshVolumes  bool
      source        *sources.Source
      buildServices []string
  }
  func planDeployment(in planInput) plan
  func (p plan) stepNames() []string // prepare names then apply names
  ```

- [ ] **Step 1: Write the table test `TestPlanDeployment`.** It has one row per line of the spec table "planDeployment must reproduce today's behaviour" and compares with `reflect.DeepEqual` on the whole `plan`. Rows that need exact values:
  - `deploy`, no Source → `apply: [{kind: kindDeploy, name: "deploy", build: true}]`, no prepare, `usesSource: false`.
  - `deploy`, Git Source → `apply: [{kindDeploy, "deploy", build: false, note: "compose has a source; deploying without building"}]`.
  - `skip-build`, no Source → `fail: &outcome{status: StatusFailed, reason: reasonNoSource}`, no steps.
  - `build`, no Source → the same `fail` as above.
  - `build`, Git with `Branch: ""` → `fail: failed("fetch", reasonNoBranch)`, `prepare: [fetch]`, `apply: [deploy]`. The stored steps stay `[fetch, deploy]` as today.
  - `build`, Git with no `PrivateKey` → `fail: failed("fetch", reasonNoKey)`, same steps.
  - `build`, Git, services `[web, api]`, `noCache` → `usesSource`, `prepare: [fetch, build:web{noCache}, build:api{noCache}]`, `apply: [deploy{build:false}]`.
  - `build`, zip, `[web]` → `prepare: [build:web]` and no fetch.
  - `build`, Git, no build services → `prepare: [fetch]`, `afterPrepare: "no Service has build:"`.
  - Any row plus `freshVolumes` → the apply `deploy` has `freshVolumes: true`.
  - `stepNames()` of the Git build row → `["fetch", "build:web", "build:api", "deploy"]`.
- [ ] **Step 2: Run it.** `go test ./internal/deployments/ -run TestPlanDeployment` must FAIL (undefined).
- [ ] **Step 3: Implement `planDeployment` in `plan.go`.** It is pure. `stepNames` concatenates the prepare and apply names.
- [ ] **Step 4: Run it.** The same command must PASS.
- [ ] **Step 5: Commit.** `refactor: decide a deployment's steps in one pure function`

### Task 2: Run the plan in the worker

**Files:**
- Modify: `internal/deployments/queue.go`: `process` (≈557–601), `job` (≈620–636), `runSteps`/`fetchAndBuild`/`deploy` (≈724–990)
- Test: `internal/deployments/queue_test.go` (existing)

**Interfaces:**
- Consumes: `planDeployment`, `plan`, `step` (Task 1).
- Produces (later tasks extend these):
  ```go
  func (j *job) run(p plan) outcome
  func (j *job) runPrepare(p plan) (outcome, bool) // today's fetchAndBuild, driven by p
  func (j *job) runApply(steps []step) outcome     // today's deploy(): gate (+volumes hold if any step.freshVolumes) once, phaseDeploying, shielded ctx, then a switch per step
  func (j *job) runDeploy(dctx context.Context, s step) ([]string, error) // calls Composes.RunDeploy
  ```

- [ ] **Step 1: Write the failing test.** Add `TestGitBuildStoresAllStepsBeforeFetching` to `queue_test.go`. Block the fetch with `h.fetch.onFetch` waiting on a channel. While it is blocked, `h.store.Get(...)` must return `Steps == []string{"fetch", "build:web", "build:api", "deploy"}`. Today it returns `[fetch, deploy]`.
- [ ] **Step 2: Run it.** `go test ./internal/deployments/ -run TestGitBuildStoresAllStepsBeforeFetching` must FAIL.
- [ ] **Step 3: Rewire.**
  - In `process`, build `planInput` from `snapshot.Source` and `snapshot.Files.Services`, call `planDeployment`, and replace `j.pre` with the result.
  - `runSteps` becomes `run(p)`:
    1. If `p` has steps, `SetSteps(p.stepNames())` once.
    2. Return `*p.fail` if it is set.
    3. If `p.usesSource || len(p.prepare) > 0`, call `runPrepare`.
    4. Call `runApply(p.apply)`.
  - Move the bodies, don't rewrite them:
    - fetch and `SetSource` go to the `kindFetch` case.
    - `Builder.Build` goes to the `kindBuild` case.
    - `PrepareBuild`, its `Cleanup` and `recordMaterialization` stay in `runPrepare`, around the step loop.
    - The zip `SetSource` stays at the start of `runPrepare`.
    - The gate code goes to `runApply`.
  - Delete the two progressive `SetSteps` calls inside `fetchAndBuild`.
  - Keep the phases, `interrupted()` checks and `stepFailed` exactly where they were relative to each step.
- [ ] **Step 4: Run everything.** Run `go test ./internal/deployments/...` and then `task test`. All must PASS. If an existing test asserted a progressive step list, update it to the full list and name it in the commit body. No other test may change.
- [ ] **Step 5: Commit.** `refactor: run each deployment from its plan`
- [ ] **Step 6: When the user asks, open PR 1** (`refactor: plan each deployment before running it`) from a branch cut at this commit. The spec commits on `feat/zero-downtime-deploy` come along. Part 1 continues on `feat/zero-downtime-deploy`.

---

# Part 1 — Zero-downtime deploy (PR 2)

### Task 3: Table and store

**Files:**
- Create: `internal/migrations/00024_zero_downtime_services.sql` (the DDL in spec §Data model, goose Up/Down like `00023_certificates.sql`)
- Create: `internal/zerodowntime/store.go`
- Test: `internal/zerodowntime/store_test.go`

**Interfaces:**
- Produces:
  ```go
  package zerodowntime
  type Store struct{ /* db */ }
  func NewStore(db *sql.DB) *Store
  func (s *Store) Enabled(ctx context.Context, composeID int64) ([]string, error) // sorted service names
  func (s *Store) Set(ctx context.Context, composeID int64, service string, on bool, now time.Time) error // on: INSERT OR IGNORE; off: DELETE
  ```

- [ ] **Step 1: Write the failing tests.** Use the repo's test DB helper, as in `internal/certificates/store_test.go`.
  - `TestSetAndEnabled`: Set `web` and `api` on, then `Enabled` returns `["api","web"]`. Set `web` off, then `["api"]`. Setting `api` on twice is not an error.
  - `TestRowsGoWithCompose`: deleting the `composes` row leaves `Enabled` returning `[]`.
- [ ] **Step 2: Run them.** `go test ./internal/zerodowntime/` must FAIL.
- [ ] **Step 3: Implement** the migration and the store.
- [ ] **Step 4: Run them.** The same command must PASS, and `go test ./internal/migrations/...` must PASS.
Server Migration copies `panel.db` whole (`internal/migration/finalize.go`), so the new table moves with it. No Migration change is needed.

- [ ] **Step 5: Commit.** `feat: store which services deploy with zero downtime`

### Task 4: Checks over `compose config`

**Files:**
- Create: `internal/zerodowntime/check.go`
- Test: `internal/zerodowntime/check_test.go` (fixtures are inline JSON in the shape `docker compose config --format json` prints. Capture one real output first with `docker compose -f <fixture> config --format json` and keep it as `testdata/config.json`)

**Interfaces:**
- Produces:
  ```go
  type Problem struct {
      Service string   `json:"service"`
      Reasons []string `json:"reasons"`
  }
  // Check returns, for each of services, why it can't roll out; services with no reason are left out.
  func Check(configJSON []byte, services []string) ([]Problem, error)
  // TraefikLabels returns service's labels whose key starts with "traefik.".
  func TraefikLabels(configJSON []byte, service string) (map[string]string, error)
  // WaitLimit is start_period + (interval + timeout) × retries + 10s, Docker defaults for unset fields.
  func WaitLimit(configJSON []byte, service string) (time.Duration, error)
  ```
- The reason strings are exact:
  - `has no healthcheck`
  - `sets container_name; two Containers can't share a name`
  - `publishes host port %s; two Containers can't bind it`
  - `runs more than one replica; rollout supports one`
  - `has an anonymous volume at %s; mount a named volume there`

- [ ] **Step 1: Write the failing table test `TestCheck`.** It has one row per spec reason:
  - no `healthcheck`; `healthcheck.disable: true`; `test: ["NONE"]`
  - `container_name`
  - `"8080:80"` → reason with `8080`; long syntax `published: "9000"` → `9000`; `"80"` alone → no reason
  - `deploy.replicas: 2`; `scale: 2`; `deploy.replicas: 1` → no reason
  - `volumes: ["/data"]` → reason with `/data`; a named volume and a bind mount → no reason
  - a service with two problems → both reasons, in the table's order
  - a name in `services` that isn't in the config → no Problem, because a renamed Service's row is ignored
  - a `${PORT}` value: the fixture is already-interpolated JSON with `published: "8080"`, which proves the check reads config output, not raw YAML

  Also write `TestTraefikLabels` (non-`traefik.` labels dropped) and `TestWaitLimit`:
  - `interval 2s, timeout 2s, retries 3, start_period 15s` → `37s`
  - all unset → `3m10s`
- [ ] **Step 2: Run them.** `go test ./internal/zerodowntime/ -run 'TestCheck|TestTraefikLabels|TestWaitLimit'` must FAIL.
- [ ] **Step 3: Implement** with `encoding/json` into the minimal structs. Parse durations with `time.ParseDuration`.
- [ ] **Step 4: Run them.** The same command must PASS.
- [ ] **Step 5: Commit.** `feat: check which services can roll out from compose config`

### Task 5: Labels and the transport file

**Files:**
- Modify: `internal/domains/labels.go` (`Inject`, `addDomain`)
- Create: `internal/domains/transport.go`
- Modify: `main.go` (after `certificates.Reconcile`, ≈134)
- Modify: every `domains.Inject` caller: `internal/composes/composes.go:1177` and its tests
- Test: `internal/domains/labels_test.go`, `internal/domains/transport_test.go`, `internal/certificates/traefik_test.go`

**Interfaces:**
- Produces:
  ```go
  func Inject(services map[string]any, composeID int64, domains []Domain, network string, zeroDowntime map[string]bool) ([]string, error)
  const TransportFileName = "relist-transports.yml"
  func WriteTransportFile(dir string) error // dir = certificates.DynamicDir(dataDir); MkdirAll 0700, temp+rename, mode 0600
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestInjectZeroDowntime`: a Domain on `web` with strip path, `internal_path` and user middleware `auth@file`, HTTPS on `web` entrypoint default. With `zeroDowntime{"web": true}`:
    - `relist-c1-d2.middlewares == "relist-c1-d2-strip,relist-c1-d2-prefix,auth@file,relist-c1-d2-retry"`
    - `traefik.http.middlewares.relist-c1-d2-retry.retry.attempts == "4"`
    - `traefik.http.services.relist-c1-d2.loadbalancer.serversTransport == "relist-fastdial@file"`
    - `relist-c1-d2-http.middlewares == "relist-c1-d2-redirect"` (unchanged)
  - `TestInjectWithoutZeroDowntimeUnchanged`: every existing `TestInject*` passes `nil` and its expected labels stay byte-identical. This is the golden guard.
  - `TestWriteTransportFile`: the content parses to `http.serversTransports.relist-fastdial.forwardingTimeouts.dialTimeout == "1s"`, the mode is `0600`, and a second write leaves no temp files.
  - In `internal/certificates/traefik_test.go`: `TestWriteDirKeepsTransportFile`. Put `relist-transports.yml` in the dir, `WriteDir(dir, nil)`, and the file must still exist.
- [ ] **Step 2: Run them.** `go test ./internal/domains/ ./internal/certificates/` must FAIL.
- [ ] **Step 3: Implement.**
  - `addDomain` takes `zeroDowntime bool`. It appends `n+"-retry"` after `d.Middlewares` and sets the two labels.
  - Update the callers to pass `nil` for now. Task 7 passes the real set.
  - In `main.go`, call `domains.WriteTransportFile(certificates.DynamicDir(cfg.DataDir))`. A failure is logged with `log.Printf("write traefik transport file: %v", err)` and is never fatal.
- [ ] **Step 4: Run them.** `go test ./internal/domains/ ./internal/certificates/ ./internal/composes/` must PASS.
- [ ] **Step 5: Commit.** `feat: give zero-downtime domains a retry and a fast-dial transport`

### Task 6: Runner and Engine ports, and their Docker adapters

**Files:**
- Modify: `internal/composes/composes.go` (`ComposeRunner`, `UpOptions`)
- Create: `internal/composes/engine.go`
- Modify: `internal/dockerexec/dockerexec.go` (`upArgs`, `Up`, new `Config`, `Scale`)
- Create: `internal/dockerexec/engine.go`
- Test: `internal/dockerexec/dockerexec_test.go`, `internal/dockerexec/engine_test.go` (use `newFakeDaemon` from `containersource_test.go`)
- Modify: every fake `ComposeRunner` in tests (`grep -rn "func (.*) Restart(ctx context.Context, req composes.RunRequest)" --include=*_test.go`). Add no-op `Config` and `Scale`.

**Interfaces:**
- Produces:
  ```go
  // composes
  type UpOptions struct { Build bool; Services []string; Out io.Writer } // Services non-empty → --no-deps + names
  type ScaleOptions struct { Service string; Replicas int; Build bool; Out io.Writer }
  // ComposeRunner gains:
  Config(ctx context.Context, req RunRequest) ([]byte, error)          // config --format json
  Scale(ctx context.Context, req RunRequest, opts ScaleOptions) error   // up -d --build|--no-build --no-deps --no-recreate --scale <svc>=<n> <svc>

  type EngineContainer struct { ID, Name, Service string; Running bool }
  type EngineMount struct { Destination string; Anonymous bool }
  type EngineState struct {
      Running          bool
      Health           string // "", "starting", "healthy", "unhealthy"
      LastHealthOutput string
      Labels           map[string]string
      Mounts           []EngineMount // type volume only
  }
  type ContainerEngine interface {
      ProjectContainers(ctx context.Context, project string) ([]EngineContainer, error) // all states, oneoff excluded
      Inspect(ctx context.Context, id string) (EngineState, error)
      Stop(ctx context.Context, id string) error
      Remove(ctx context.Context, id string, force, volumes bool) error
  }
  // dockerexec
  func NewEngine(socketPath string) *Engine
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestUpArgs`:
    - `upArgs(UpOptions{Build: false})` stays `["up","-d","--no-build","--remove-orphans"]`.
    - With `Services: ["worker","db"]` → `["up","-d","--no-build","--no-deps","--remove-orphans","worker","db"]`.
  - `TestScaleArgs`: `{Service:"web", Replicas:2}` → `["up","-d","--no-build","--no-deps","--no-recreate","--scale","web=2","web"]`. The `Build: true` variant uses `--build`.
  - `TestEngineProjectContainers`: the fake daemon must receive `GET /containers/json?all=1&filters=` with `label=com.docker.compose.project=p` and `label=com.docker.compose.oneoff=False`. `Service` must map from the `com.docker.compose.service` label.
  - `TestEngineInspect`: `State.Health.Log` last `Output` → `LastHealthOutput`. A volume mount whose `GET /volumes/{name}` has `com.docker.volume.anonymous` → `Anonymous: true`. One without that label → `false`. Bind mounts are omitted.
  - `TestEngineStopAndRemove`:
    - `POST /containers/x/stop` with **no** `t` query.
    - `DELETE /containers/x?force=1&v=1`, and `force=0&v=0` for the other variant.
    - A 404 on remove is not an error.
- [ ] **Step 2: Run them.** `go test ./internal/dockerexec/` must FAIL.
- [ ] **Step 3: Implement** the adapters. `upArgs` now takes `UpOptions`. Reuse `newSocketClient`. `Config` returns `[]byte(content)` from `run(ctx, req, nil, "config", "--format", "json")`.
- [ ] **Step 4: Run them.** `go test ./...` must PASS (fakes updated).
- [ ] **Step 5: Commit.** `feat: add compose config, scale and container engine adapters`

### Task 7: Snapshot, status and the Deploy step's subset

**Files:**
- Modify: `internal/composes/composes.go` (`Service` struct, `New`, `resolveCompose` signature)
- Modify: `internal/composes/snapshot.go` (`ExecutionSnapshot`, `Capture`, extract `stageSnapshot` from `prepareSnapshotBuild`)
- Modify: `internal/composes/deploy.go` (`DeployOptions`, `RunDeploy`, `prepareDeployFiles`, `resolveForDeploy`)
- Create: `internal/composes/zerodowntime.go`
- Modify: `internal/httpapi/httpapi.go` (`Deps.Engine`, wiring), `main.go` (`Engine: dockerexec.NewEngine(dockerexec.DefaultSocketPath)`)
- Test: `internal/composes/zerodowntime_test.go`, `internal/composes/deploy_test.go`

**Interfaces:**
- Consumes: Tasks 3–6.
- Produces:
  ```go
  func (s *Service) WithEngine(e ContainerEngine) *Service
  // ExecutionSnapshot gains ZeroDowntime []string: switched-on Services that have ≥1 Domain, in YAML order.
  type DeployOptions struct { FreshVolumes, Build bool; Exclude []string; Out io.Writer }
  // Exclude non-empty: up --no-deps --remove-orphans <active services minus Exclude>, or, if none remain,
  // remove orphans through the engine; no markDeployed (the caller calls MarkDeployed).
  // "Active services" are the keys of `services` in Config() of the written files, which drops
  // services whose profile is not enabled. Naming one on the up command line would start it
  // (verified on Compose v5.5.1).
  func (s *Service) MarkDeployed(ctx context.Context, t DeployTarget) error
  type ZeroDowntimeStatus struct { Service string `json:"service"`; Enabled bool `json:"enabled"`; Reasons []string `json:"reasons"` }
  // ZeroDowntimeProblems checks t.Snapshot.ZeroDowntime against compose config of the snapshot's files,
  // staged in the Compose dir under unique names (see stageCheck).
  func (s *Service) ZeroDowntimeProblems(ctx context.Context, t DeployTarget) ([]zerodowntime.Problem, error)
  // ZeroDowntimeServices captures t from the current YAML, then lists every Service with a Domain.
  func (s *Service) ZeroDowntimeServices(ctx context.Context, t DeployTarget) ([]ZeroDowntimeStatus, error)
  func serviceNames(yamlContent string) ([]string, error) // every service key, file order: used only to tell orphans apart
  // stageCheck writes the snapshot's YAML, env file and set files into the Compose dir as
  // .zd-check-<random>.yml, .zd-check-<random>.env and .zd-check-<random>-sets/. Relative env_file,
  // include and extends paths then resolve as they do on deploy, and a running build's
  // .build-* files are never touched. cleanup removes all three.
  func stageCheck(t DeployTarget) (req RunRequest, cleanup func(), err error)
  ```

- [ ] **Step 1: Write the failing tests** with a fake runner and fake engine.
  - `TestCaptureRecordsZeroDowntime`: rows `web` (has a Domain) and `worker` (no Domain) → `Snapshot.ZeroDowntime == ["web"]`, and the snapshot YAML has `web`'s retry label.
  - `TestCaptureIgnoresRowWithoutDomain`: the row is `old` and `old` is not in the YAML → `ZeroDowntime == []`, and there is no retry label anywhere.
  - `TestZeroDowntimeProblemsUsesConfig`: the fake `Config` returns JSON where `web` has no healthcheck → `[{web, ["has no healthcheck"]}]`. The fake must have been called with a `RunRequest` whose files are in the Compose dir, are named `.zd-check-*`, exist during the call and are gone after it.
  - `TestStageCheckLeavesBuildFilesAlone`: pre-create `.build-compose.yml`, `.build.env` and `.build-sets/x.env` in the Compose dir, then run `ZeroDowntimeProblems`. All three must be unchanged afterwards.
  - `TestStageCheckResolvesRelativeEnvFile` (needs Docker; skip when `docker` is absent, as the other dockerexec integration tests do): the YAML has `env_file: ./app.env`, and `app.env` exists in the Compose dir. A real `Runner.Config` on the staged request succeeds.
  - `TestRunDeployExcludeRunsSubset`: services `web, worker, db` plus `tool` with `profiles: [tools]`. The fake `Config` lists only `web, worker, db` as active. With `Exclude: ["web"]` → one `Up` with `Services: ["worker","db"]` (no `tool`), and `markDeployed` is not called.
  - `TestRunDeployExcludeAllRemovesOrphansViaEngine`: `Exclude` covers every service. The engine lists `p-web-1` (service `web`) and `p-old-1` (service `old`). Expect no `Up`, and `Stop`+`Remove(force=false, volumes=false)` on `p-old-1` only.
  - `TestRunDeployWritesTransportFile`: `Snapshot.ZeroDowntime` is non-empty → the transport file exists after `RunDeploy`. When the write fails (make the dir read-only), it fails with message `could not write Traefik transport file` and the runner is untouched.
- [ ] **Step 2: Run them.** `go test ./internal/composes/ -run 'ZeroDowntime|Exclude|TransportFile'` must FAIL.
- [ ] **Step 3: Implement.**
  - Read the switch set **once** per capture. `prepareDeployFiles` loads `zerodowntime.Store.Enabled` once, passes it to `resolveCompose` → `domains.Inject`, and returns it in `deployFiles`. `Capture` fills `ZeroDowntime` from that same value: the enabled names that have a Domain, in YAML order. A toggle between two reads could otherwise roll out a Service without its retry label. Preview goes through the same path, so it shows the labels too.
  - Add `TestCaptureReadsSwitchesOnce`: a store wrapper counts `Enabled` calls → exactly 1 per `Capture`.
  - Extract the file-writing part of `prepareSnapshotBuild` into a helper that takes the target names. Builds keep the `.build-*` names, and `stageCheck` uses its `.zd-check-<random>` names.
- [ ] **Step 4: Run them.** `go test ./internal/composes/ ./internal/httpapi/` must PASS.
- [ ] **Step 5: Commit.** `feat: snapshot zero-downtime services and deploy the rest as a subset`

### Task 8: `RunRollout`

**Files:**
- Create: `internal/composes/rollout.go`
- Test: `internal/composes/rollout_test.go`

**Interfaces:**
- Consumes: `ContainerEngine`, `ComposeRunner.Scale/Config/Up`, `zerodowntime.TraefikLabels/WaitLimit` (Tasks 4, 6).
- Produces:
  ```go
  type RolloutOptions struct { Build bool; Deadline time.Time; Out io.Writer }
  // RunRollout runs spec §Deploy sequence step 3 for one service. It returns warnings and a
  // *DeployFailure (FailureRunner) naming the service on any failure.
  func (s *Service) RunRollout(ctx context.Context, t DeployTarget, service string, opts RolloutOptions) ([]string, error)
  var (
      rolloutPoll    = time.Second     // tests shorten
      rolloutOverlap = 3 * time.Second // tests shorten
  )
  ```
- The exact log and warning copy (`Out` lines and returned warnings):
  - `web had no running container; deployed without rollout` (warning)
  - `web's Traefik labels changed (Domain edited or zero-downtime just enabled); this deploy has a short outage` (warning)
  - `web did not become healthy in 37s; kept the previous container` (failure reason. The duration is the computed limit, `time.Duration.String()`)
  - `web has an anonymous volume at /var/lib/app (from the image's VOLUME); mount a named volume there or turn off zero-downtime deploy` (failure reason)
  - `could not identify the new container` (failure reason)

- [ ] **Step 1: Write the failing tests.** The fake engine records calls in order, and a fake clock is not needed because the vars are shortened.
  - `TestRolloutHealthy`: OLD=`a`, after Scale NEW=`b`, health `starting`→`healthy` → the call order is `Scale(web,2)`, `Inspect(b)`×2, `Stop(a)`, `Remove(a,false,false)`. The overlap wait elapsed, and there are no warnings.
  - `TestRolloutDrift`: OLD labels ≠ config labels → no overlap wait, and the drift warning is returned.
  - `TestRolloutUnhealthy`: NEW goes `unhealthy` → `Remove(b,true,true)`, OLD is never stopped, and the error is the "did not become healthy" one. The log contains the health output, truncated to 2048 bytes.
  - `TestRolloutNewExited`: NEW `Running: false` → same as unhealthy.
  - `TestRolloutDeadlineStillRemovesNew`: `Deadline` is already passed and the ctx is canceled → `Remove(b,true,true)` still happens.
  - `TestRolloutNoOldContainer`: OLD is empty → `Up{Services:["web"]}`, no Scale, and the warning is returned.
  - `TestRolloutAnonymousVolume`: an OLD mount is `Anonymous` → no Scale, and the error is the anonymous-volume one.
  - `TestRolloutTwoOldContainers`: OLD is `a,c` → `Scale(web,3)`, then both stopped and removed.
  - `TestRolloutCannotIdentifyNew`: after Scale the list has no new ID → the error, and every non-OLD `web` container is removed.
  - `TestRolloutRemoveNewFails`: the remove error → the failure names the leftover container ID instead of "kept the previous container".
- [ ] **Step 2: Run them.** `go test ./internal/composes/ -run TestRollout` must FAIL.
- [ ] **Step 3: Implement** with the cleanup calls on `context.WithoutCancel(ctx)` + `context.WithTimeout` per the Global Constraints. The wait limit is `min(WaitLimit, time.Until(opts.Deadline))`.
- [ ] **Step 4: Run them.** The same command must PASS.
- [ ] **Step 5: Commit.** `feat: roll out a service by scaling, waiting for health and removing the old container`

### Task 9: Plan rollouts and refuse at enqueue

**Files:**
- Modify: `internal/deployments/plan.go`, `plan_test.go` (Task 1)
- Modify: `internal/deployments/queue.go` (`Composes` interface, `Enqueue`, `runApply`)
- Modify: `internal/deployments/http.go` (`create`)
- Test: `internal/deployments/queue_test.go`, `internal/deployments/http_test.go` (or wherever `create` is tested: `grep -rn "func Test.*Create\|/deployments\"" internal/deployments/*_test.go`)

**Interfaces:**
- Consumes: `composes.RunRollout`, `MarkDeployed`, `ZeroDowntimeProblems`, `DeployOptions.Exclude` (Tasks 7–8).
- Produces:
  ```go
  // plan.go
  const kindRollout stepKind = 3       // after kindDeploy
  // step gains: exclude []string (kindDeploy)
  // planInput gains: zeroDowntime []string
  // queue.go
  type ZeroDowntimeError struct{ Problems []zerodowntime.Problem }
  func (e *ZeroDowntimeError) Error() string // "zero-downtime deploy is on for services that can't be rolled out"
  ```

- [ ] **Step 1: Write the failing tests.**
  - Extend `TestPlanDeployment` with these rows:
    - `deploy`, `zeroDowntime:[web]` → `apply: [deploy{build:true, exclude:[web]}, rollout:web{build:true}]`
    - `zeroDowntime:[web, api]` (YAML order, as Task 7 records it) → `[deploy{exclude:[web, api]}, rollout:web, rollout:api]`.
    - `freshVolumes` + `zeroDowntime:[web]` → `[deploy{freshVolumes}]` and no rollout.
  - `TestRolloutStepsRunInGateAndRecordWarnings` (queue): a fake `Composes` returns a warning from `RunRollout`. Expect steps `["deploy","rollout:web"]`, status succeeded, the warning in `Deployment.Warnings`, `MarkDeployed` called once after the rollout, and the gate held across both steps.
  - `TestRolloutFailureFailsDeployment`: with `zeroDowntime:[web, api]`, `RunRollout(web)` errors → `FailedStep == "rollout:web"`, `RunRollout(api)` is never called, the log contains `not deployed: api`, and `MarkDeployed` is not called.
  - `TestCancelDuringRolloutRefused`: the same as `TestCancelDuringDeployRefused`, at the rollout step.
  - `TestEnqueueRefusesZeroDowntimeProblems`: the fake returns `[{web, ["has no healthcheck"]}]` → `*ZeroDowntimeError`, and no row is inserted.
  - `TestEnqueueFreshVolumesSkipsZeroDowntimeCheck`: the fake is never called.
  - `TestCreateZeroDowntime422` (http): status 422, body `{"error":"zero-downtime deploy is on for services that can't be rolled out","zeroDowntime":[{"service":"web","reasons":["has no healthcheck"]}]}`.
  - `TestEnqueueConfigFailureIs400`: `ZeroDowntimeProblems` returns a plain error `docker compose ... : variable X not set` → HTTP 400 with the first line.
- [ ] **Step 2: Run them.** `go test ./internal/deployments/` must FAIL.
- [ ] **Step 3: Implement.**
  - `process` passes `snapshot.Files.ZeroDowntime`.
  - `runApply` handles `kindRollout` by calling `RunRollout` with `Deadline` taken from `j.tctx`. It collects warnings across steps and calls `MarkDeployed` once after the last step when any rollout ran.
  - `Enqueue` runs `ZeroDowntimeProblems` after `capture` and before the insert when `!r.FreshVolumes && len(snapshot.Files.ZeroDowntime) > 0`, and discards the captured snapshot on refusal.
  - `create` maps `*ZeroDowntimeError` → 422 with that body, and other check errors → 400.
- [ ] **Step 4: Run them.** Run `go test ./internal/deployments/ ./internal/composes/` and then `task test`. All must PASS.
- [ ] **Step 5: Commit.** `feat: plan rollout steps and refuse services that can't roll out`

### Task 10: Zero-downtime API

**Files:**
- Modify: `internal/composes/composes.go` (`RegisterRoutes`)
- Create: `internal/composes/zerodowntime_http.go`
- Test: `internal/composes/zerodowntime_http_test.go`

**Interfaces:**
- Produces:
  - `GET  /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/zero-downtime` → `200 [ZeroDowntimeStatus]`
  - `PUT  …/zero-downtime/{service}` with body `{"enabled": bool}` → `204`. It returns `404 compose not found`, and `422 {"error":"web has no domain"}` when the Service has no Domain in the current YAML.

- [ ] **Step 1: Write the failing tests.**
  - GET lists only Services with a Domain, with `enabled` and `reasons` (fake runner `Config`).
  - PUT on then GET → `enabled: true`.
  - PUT on a Service without a Domain → 422 with that body.
  - PUT on an unknown Compose → 404.
  - Turning on while `reasons` is non-empty → 204, because enabling is allowed.
- [ ] **Step 2: Run them.** `go test ./internal/composes/ -run ZeroDowntimeHTTP` must FAIL.
- [ ] **Step 3: Implement** using the handler helpers that already exist in `composes.go` (`writeJSON`, `writeError`, path parsing).
- [ ] **Step 4: Run them.** The same command must PASS.
- [ ] **Step 5: Commit.** `feat: add the zero-downtime switch API`

### Task 11: Web data layer

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/data/store.tsx` (expose `loadZeroDowntime`, `setZeroDowntime`), `web/src/data/deployments-view.ts`
- Test: `web/src/data/api.test.ts` (or the existing api test file), `web/src/data/deployments-view.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export type ZeroDowntimeService = { service: string; enabled: boolean; reasons: string[] };
  export class ZeroDowntimeRefusedError extends ApiError { readonly problems: { service: string; reasons: string[] }[] }
  api.listZeroDowntime(location: ComposePath): Promise<ZeroDowntimeService[]>
  api.setZeroDowntime(location: ComposePath, service: string, enabled: boolean): Promise<void>
  // api.enqueueDeployment throws ZeroDowntimeRefusedError on a 422 whose body has `zeroDowntime`.
  ```

- [ ] **Step 1: Write the failing tests.**
  - A mocked 422 with a `zeroDowntime` body → `ZeroDowntimeRefusedError` with `problems`. A 422 without it → plain `ApiError`.
  - `stepLabel('rollout:web') === 'Rollout web'`, and a running Deployment on `rollout:web` → headline `Rolling out web`.
- [ ] **Step 2: Run them.** `pnpm --dir web test -- api deployments-view` must FAIL.
- [ ] **Step 3: Implement.** In `enqueueDeployment`, read the body once and branch on `zeroDowntime` before throwing. Add the `rollout:` case next to the `build:` case in `deploymentHeadline` and `activeStepWord` (`rolling out`).
- [ ] **Step 4: Run them.** The same command and `task typecheck` must PASS.
- [ ] **Step 5: Commit.** `feat: zero-downtime types, API calls and rollout step labels`

### Task 12: Requirements list, switch card and confirm modal

**Files:**
- Create: `web/src/components/ZeroDowntimeRequirements.tsx` (the one list, used by the modal and the editor note)
- Create: `web/src/components/ZeroDowntimeCard.tsx`
- Modify: `web/src/components/ComposeDomainsTab.tsx` (render the card under the domains table)
- Test: `web/src/components/ZeroDowntimeCard.test.tsx`

**Interfaces:**
- Produces:
  ```tsx
  export const ZERO_DOWNTIME_REQUIREMENTS: readonly string[]; // the seven lines below, in order
  export function ZeroDowntimeRequirements(): JSX.Element;     // <ul> of them
  export function ZeroDowntimeCard(props: { compose: Compose }): JSX.Element;
  ```
- The requirement lines are exact, in this order:
  1. `A healthcheck that passes only once the app can serve requests`
  2. `No container_name`
  3. `No fixed host port in ports: ("80" is fine, "8080:80" is not)`
  4. `One replica (no deploy.replicas or scale above 1)`
  5. `No anonymous volumes: mount a named volume over every path the app writes to, including paths the image declares with VOLUME`
  6. `The app finishes in-flight requests when it gets SIGTERM, within stop_grace_period`
  7. `The app tolerates two copies running for a few seconds (no SQLite file shared through a named volume, no migrations that lock out the old version)`
- Card help text is exact: `New containers take traffic only once their healthcheck passes; the old one is then stopped. See the requirements under the YAML editor. Changing this Service's Domains makes the next deploy briefly unavailable.`
- Modal copy is exact:
  - title `Turn on zero-downtime deploy for {service}?`
  - buttons `Cancel` and primary `Accept and turn on`
  - the reasons Alert line `Deploys will be refused until these are fixed`

- [ ] **Step 1: Write the failing tests** (`ZeroDowntimeCard.test.tsx`, mocked store):
  - Clicking an off switch opens the modal, sends no request, and the switch stays unchecked.
  - Cancel and Esc → modal closed, still unchecked, no request.
  - Accept → `setZeroDowntime(…, 'web', true)` once. The button shows loading until it resolves, then the switch is checked and the modal closed.
  - Accept rejects → the modal stays open with the error message.
  - A service with `reasons` → the modal shows the warning Alert with the reasons and the line above.
  - Clicking an on switch → `setZeroDowntime(…, false)` immediately, with no modal.
  - On and `reasons` non-empty → a warning Alert under the row with an `Open editor` link.
- [ ] **Step 2: Run them.** `pnpm --dir web test -- ZeroDowntimeCard` must FAIL.
- [ ] **Step 3: Implement** with antd `Switch`, `Modal` (`confirmLoading`, `onCancel`) and `Alert type="warning"`. Load via `loadZeroDowntime` on mount and after each change.
- [ ] **Step 4: Run them.** The same command and `task typecheck` must PASS.
- [ ] **Step 5: Commit.** `feat: zero-downtime switch with a confirm modal on the Domains tab`

### Task 13: Editor note

**Files:**
- Modify: `web/src/components/ComposeEditorGuide.tsx` (new optional prop), `web/src/pages/ComposePage.tsx` (≈794: pass the statuses, reload after save)
- Test: `web/src/components/ComposeEditorGuide.test.tsx`

**Interfaces:**
- Consumes: `ZeroDowntimeRequirements`, `ZeroDowntimeService` (Tasks 11–12).
- Produces: `ComposeEditorGuide({ zeroDowntime?: ZeroDowntimeService[] })`.

- [ ] **Step 1: Write the failing tests.**
  - No prop, or no service enabled → the output equals today's three bullets exactly.
  - With `web` enabled and no reasons → a "Zero-downtime deploy" block, the seven requirements, and `web — ready`.
  - With `api` reasons `["has no healthcheck","publishes host port 8080; two Containers can't bind it"]` → `api — has no healthcheck; publishes host port 8080; two Containers can't bind it`, rendered as `Text type="warning"`.
  - No `Alert` in the note.
- [ ] **Step 2: Run them.** `pnpm --dir web test -- ComposeEditorGuide` must FAIL.
- [ ] **Step 3: Implement.** In `ComposePage`, reload `listZeroDowntime` after each successful save. Keep the note's existing style object, and make sure long lines wrap at 320px (`overflowWrap: 'anywhere'` is already set).
- [ ] **Step 4: Run them.** The same command and `task typecheck` must PASS.
- [ ] **Step 5: Commit.** `feat: list zero-downtime requirements and status under the YAML editor`

### Task 14: Refusal modal and Fresh volumes line

**Files:**
- Create: `web/src/components/ZeroDowntimeRefusalModal.tsx`
- Modify: `web/src/pages/ComposePage.tsx` (`runDeploy` catch ≈503; the Fresh volumes modal ≈1240–1275), `web/src/pages/ProjectPage.tsx` (`lifecycle` catch ≈198)
- Test: `web/src/components/ZeroDowntimeRefusalModal.test.tsx`, `web/src/pages/ComposePage.test.tsx`, `web/src/pages/ProjectPage.test.tsx` (create the last one if absent, following `ComposePage.test.tsx`)

**Interfaces:**
- Produces: `ZeroDowntimeRefusalModal({ problems, onOpenEditor?, onOpenDomains?, onClose })`.
- Copy is exact:
  - title `Can't deploy with zero downtime`
  - one row per reason, `{service} — {reason}`
  - buttons `Open editor`, `Open domains`, `Close`
  - Fresh volumes line `Zero-downtime deploy doesn't apply to this deploy.`

- [ ] **Step 1: Write the failing tests.**
  - The modal lists each `service — reason`, and the buttons call their handlers.
  - `ComposePage`: `deployCompose` rejects with `ZeroDowntimeRefusedError` → the modal opens and the `actionError` Alert does **not** show. A plain error still shows the Alert. `Open editor` switches to the Editor tab, and `Open domains` to the Domains tab.
  - `ProjectPage deploy continues after a zero-downtime refusal`: deploying Compose A is refused and opens the modal for A (without the editor and domains buttons; it links to A's page instead), and Compose B's deploy then queues normally.
  - The Fresh volumes modal shows the line only when any Service has the switch on.
- [ ] **Step 2: Run them.** `pnpm --dir web test -- ZeroDowntimeRefusalModal ComposePage ProjectPage` must FAIL.
- [ ] **Step 3: Implement** with `instanceof ZeroDowntimeRefusedError` in the two catch blocks before `fail(reason)`.
- [ ] **Step 4: Run them.** Run the same command, `task typecheck`, then `pnpm --dir web test`. All must PASS.
- [ ] **Step 5: Commit.** `feat: explain a zero-downtime refusal wherever a deploy starts`

### Task 15: ADR, glossary, README and the live check

**Files:**
- Create: `docs/adr/0023-zero-downtime-rollout.md`
- Modify: `CONTEXT.md` (add **Zero-downtime deploy** and **Rollout** after **Deployment**, with the spec's exact text)
- Modify: `README.md` § "Upgrading an existing install" (≈162)
- Modify: `.scratch/zero-downtime-deploy/spec.md` (`Status:`)

- [ ] **Step 1: Write the ADR** in the shape of `0022-uploaded-certificates.md`: context, **Decision**, considered options, consequences. Its content is spec §Domain changes (decisions and rejected options) and §Decisions and evidence (cite the spike numbers). State that it extends ADR-0013.
- [ ] **Step 2: Add the README note.** Zero-downtime deploy needs Traefik's file provider (`--providers.file.directory=/letsencrypt/dynamic`, from ADR-0022). An install file older than that turns a switched-on Service's Domains into 404 after deploy.
- [ ] **Step 3: Check.** Run `task test`, `task typecheck` and `pnpm --dir web test`. All must PASS.
- [ ] **Step 4: Commit.** `docs: record zero-downtime rollout (ADR-0023) and its terms`
- [ ] **Step 5: Manual live check** (spec §Testing, last item). The user runs it on the live install; record the results in the PR body.
  1. Pick a Compose whose `web` has a Domain and a healthcheck, and turn the switch on.
  2. Deploy once. Expect the drift warning, because the switch was just turned on.
  3. Run `while true; do curl -s -o /dev/null -w '%{http_code}\n' https://<domain>/; sleep 0.05; done | sort | uniq -c` during a code-only deploy. Expect only `200`.
  4. Edit the Domain's path and deploy. Expect the warning and about 2s of `404`.
- [ ] **Step 6: When the user asks, open PR 2** (`feat: zero-downtime deploy for services with a domain`) with the live-check results.
