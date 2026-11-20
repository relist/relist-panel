# Compose status without the Compose CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Loading the panel never starts a `docker compose ps` process. Compose status (running/total containers and Service names) is read through the Engine API, one Docker call per list. This stops the panel container being OOM-killed on a host with many Composes.

**Why (measured 2026-11-15 in the e2e sandbox):**
- **Every page load fans out.** The web store loads the whole Project tree (`web/src/data/store.tsx` `loadProjects`). It requests `GET …/environments/{id}/composes` for every Environment at once, through `Promise.all`.
- **Every list request runs one CLI call per Compose.** It runs `docker compose ps --all --format json` for each Compose in turn (`composes.withStatuses` → `runner.Status`).
- **That adds up.** With about 36 Composes, about 20 `docker` + `docker-compose` CLI pairs ran at once, each about 6 MB anon.
- **The kernel kills the panel.** `dmesg` showed `Memory cgroup out of memory: Killed process … (relist-panel)` at the 256 MiB `mem_limit`. The panel restarted mid-request, and Traefik answered 502.
- **More tabs or users make it worse.**

**Architecture:**
- `internal/dockerexec.Engine` gains `ComposeContainers(ctx)`: one `GET /containers/json?all=1` filtered to non-one-off Compose containers, with each container's project.
- `internal/composes` computes `ComposeStatus` from Engine containers with a pure function:
  - `withStatuses` reads one shared **snapshot** of all Compose containers.
    - The snapshot is one Engine call. List requests that arrive at once share it (singleflight), and it is reused for 2 s.
    - So a page load that lists 20 Environments makes one Docker call, not 20.
    - There is no background loop: nothing runs while nobody is looking. Host monitoring already reads the Engine on its own sampler (`internal/hostmetrics`), and Compose status follows the same rule of no CLI.
  - `currentStatus`, `withRefreshedStatus` and `ServiceNames` call `ProjectContainers`, which always reads fresh. A deploy, stop or restart also drops the snapshot.
- `Runner.Status` and `parseStatus` are deleted, so no code path can shell out for status again.

**Delivery:** one PR, branch `claude/compose-status-from-engine` on `main` (the plan commit e3e83ed is its base). It is independent of the Databases stack.

**Tech Stack:** Go only. No new dependency, no migration, no frontend change.

**Spec:** this file. There is no separate spec; the problem statement above is the requirement.

## Global Constraints

- **Status semantics unchanged:**
  - `Total` is every non-one-off container of the project, in any state;
  - `Running` counts `State == "running"`;
  - `Services` is the distinct `com.docker.compose.service` labels, **sorted**.
- **Which containers count:**
  - A container belongs to a Compose when its `com.docker.compose.project` label equals the Compose's `RunRequest.ProjectName` **exactly**.
  - One-off containers (`com.docker.compose.oneoff=True`, from `compose run`) never count; filter `com.docker.compose.oneoff=False` as `ProjectContainers` does.
- **Undeployed and unknown Composes are unchanged.**
  - A Compose with no resolved compose file on disk is still a zero status, with no Docker call for it.
  - `requireUniqueIdentity` failing is still a zero status.
- **Best-effort as today.** An Engine error, or a nil engine, gives a zero status and never fails the request.
- **The snapshot.**
  - **Sharing.** List requests within 2 s of each other share one Engine call, whatever the number of Composes or of concurrent requests. Use `snapshotTTL = 2 * time.Second` and no new dependency; hand-roll it with a mutex and a done channel.
  - **Errors are not cached.** The next request tries again.
  - **Caller contexts.**
    - The shared call runs on `context.WithoutCancel(ctx)` with a 10 s timeout, so one cancelled request never fails the others.
    - Each waiting caller still returns when its own `ctx` ends.
  - **Invalidation.** After a runner action (Up, Down, DownWithVolumes, Restart, Scale), the snapshot is dropped. The list then shows the outcome at once, not up to 2 s later.
- **Docker.** Only through the sandbox `DOCKER_HOST` from `.data/e2e/env`.
- **Go tests** run as `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **ADR.** Add `docs/adr/0043-compose-status-from-the-engine.md`. 0040–0042 are taken on the open Databases branches; renumber if they are not merged first.

## Review Focus

0. **Twenty Environments listed at once** by the web store's `Promise.all`. They make exactly one Engine call. Test in Task 2: `TestSnapshotSharedAcrossConcurrentLists`.
1. **Project names that are prefixes of each other** (`relist-c1` and `relist-c19`). Grouping is by exact label value, never by prefix or filter substring. Test in Task 2: `TestStatusesGroupByExactProject`.
2. **One-off containers** from `docker compose run`. They are not counted, as `compose ps --all` leaves them out. Test in Task 1: `TestComposeContainersSkipsOneOff`.
3. **Docker unreachable.** The list still answers 200 with zero statuses, and makes one failed Engine call, not one per Compose. Test in Task 2: `TestStatusesEngineDownIsZero`.
4. **An orphan container** of a service removed from the file. Its status must match what `docker compose ps --all` showed before.
   - Verify in the sandbox in Task 3 and record the result in the ADR.
   - If they differ, keep the CLI's numbers by also filtering to the Services in `docker compose config`. Only do that if the check shows a difference, and only for the single-Compose paths.
5. **Many Composes on one page load.** With 30 deployed Composes in the sandbox, a fresh sign-in starts zero `docker compose ps` processes in the panel container, and its `memory.peak` stays well under the limit. This is the Task 3 live check.

---

### Task 1: `Engine.ComposeContainers`

**Files:**
- Modify:
  - `internal/composes/engine.go`: `EngineContainer` gains `Project string`, and `ContainerEngine` gains the method;
  - `internal/dockerexec/engine.go`: `ProjectContainers` also fills `Project`.
- Test: `internal/dockerexec/engine_test.go`. Follow its existing fake-socket pattern.

**Interfaces:**
- Produces:
  ```go
  // ComposeContainers lists every Compose-managed container in every state,
  // one-off ones (compose run) left out, each with its project.
  ComposeContainers(ctx context.Context) ([]EngineContainer, error)
  ```
  Filter: `{"label":["com.docker.compose.project","com.docker.compose.oneoff=False"]}`, where the bare key means "has the label".

- [ ] **Step 1: Write the failing tests**
  - `TestComposeContainers`:
    - the fake daemon checks that the request is `GET /containers/json` with `all=1` and exactly that filter JSON;
    - it answers 3 containers across projects `relist-c1` and `relist-c19`;
    - the result carries `Project`, `Service`, `Running` and `Name`.
  - `TestComposeContainersSkipsOneOff`: the request's filter includes `com.docker.compose.oneoff=False`.
  - Extend the existing `ProjectContainers` test so that `Project` is filled.
- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/dockerexec/ -run ComposeContainers`
  Expected: build failure.
- [ ] **Step 3: Implement it**

  Share the decode with `ProjectContainers`. Fix every fake `ContainerEngine` in tests (`grep -rn "ProjectContainers(ctx" --include=*_test.go internal`).
- [ ] **Step 4: Run the tests and see them pass**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/dockerexec/ ./internal/composes/`
  Expected: PASS.
- [ ] **Step 5: Commit**: `feat: list compose containers through the engine`

---

### Task 2: Status from the Engine; delete `Runner.Status`

**Files:**
- Modify:
  - `internal/composes/composes.go`: `withStatuses`, `currentStatus`, `withRefreshedStatus`, `ServiceNames`, and the `Runner` interface;
  - `internal/dockerexec/dockerexec.go`: delete `Status` and `parseStatus` and their tests.
- Create: `internal/composes/status.go`
- Test: `internal/composes/status_test.go`; update the fakes in `internal/composes/*_test.go`.

**Interfaces:**
- Consumes: `ContainerEngine.ProjectContainers` and `ContainerEngine.ComposeContainers` (Task 1).
- Produces:
  ```go
  // statusOf is the ComposeStatus of one project's containers.
  func statusOf(containers []EngineContainer) ComposeStatus
  // snapshot shares one ComposeContainers call between list requests (see Global Constraints).
  type snapshot struct{ /* mu, at, byProject, inflight chan, now func() time.Time */ }
  func (n *snapshot) get(ctx context.Context, engine ContainerEngine) (map[string][]EngineContainer, error)
  func (n *snapshot) drop()
  ```
  `Service` gains `status *snapshot`, created in `New` with `now = time.Now`; tests set the clock.
- **`withStatuses(ctx, composes)`:**
  - keep the per-Compose `withDeployIdentity`, the file check and `requireUniqueIdentity`;
  - then call `s.status.get` once, and only if at least one Compose is deployed;
  - group by `Project`, and give each deployed Compose `statusOf(group[req.ProjectName])`.
- **The single-Compose paths** (`currentStatus`, `withRefreshedStatus`, `ServiceNames`) use `ProjectContainers(ctx, req.ProjectName)`. `ServiceNames` keeps returning the error, as it does today. `withRefreshedStatus` calls `s.status.drop()` first.

- [ ] **Step 1: Write the failing tests**
  - `TestStatusOf`: containers `{web running}`, `{web exited}`, `{db running}` give `{Running:2, Total:3, Services:["db","web"]}`. An empty input gives a zero value with `Services` nil.
  - `TestStatusesGroupByExactProject`:
    - two deployed Composes, projects `relist-c1` and `relist-c19`, and a fake engine that returns containers of both;
    - each Compose gets only its own counts.
  - `TestStatusesOneEngineCall`:
    - a list of 5 deployed Composes gives `ComposeContainers` called exactly once and `ProjectContainers` 0 times;
    - the fake runner's methods fail the test if a status-like CLI call is made. After deletion this is a compile-time guarantee; keep a call counter on the fake engine.
  - `TestSnapshotSharedAcrossConcurrentLists`: the fake engine blocks until released. 20 goroutines list at once, the engine is released, and there is exactly 1 `ComposeContainers` call. Every list has the right statuses.
  - `TestSnapshotExpires`: with a fake clock, a list at t=0 and one at t=1.9 s make 1 call; one at t=2.1 s makes a 2nd call.
  - `TestSnapshotErrorNotCached`: the 1st call errors and the next list makes a new call.
  - `TestSnapshotCallerCancel`:
    - caller A's ctx is cancelled while the shared call is blocked; A returns `ctx.Err()` promptly;
    - caller B, waiting on the same call, gets the result once it is released.
  - `TestRefreshedStatusDropsSnapshot`: list (1 call), then a deploy action through `withRefreshedStatus`, then list within 2 s: a 2nd `ComposeContainers` call.
  - `TestStatusesUndeployedNoCall`: an all-undeployed list makes 0 Engine calls.
  - `TestStatusesEngineDownIsZero`: `ComposeContainers` returns an error. The list handler answers 200, every status is zero, and there is 1 call.
  - `TestServiceNamesFromEngine`: `ServiceNames` returns the sorted services of the project, and the Engine's error is passed through.
- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/composes/ -run 'StatusOf|Statuses|ServiceNames'`
  Expected: FAIL.
- [ ] **Step 3: Implement it, then delete `Runner.Status` and `parseStatus`**

  Remove `Status` from the `Runner` interface and from every fake that implements it.
- [ ] **Step 4: Run all Go checks**

  Run: `go vet ./... && env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`
  Expected: PASS. `grep -rn '"ps", "--all"' internal` prints nothing.
- [ ] **Step 5: Commit**: `fix: read compose status from the engine, not docker compose ps`

---

### Task 3: Live check, ADR-0043

**Files:**
- Create: `docs/adr/0043-compose-status-from-the-engine.md`
- Possibly modify: `e2e/` (see Step 3)

- [ ] **Step 1: Check orphan parity** (Review Focus 4)

  In the sandbox, deploy a Compose with services `a` and `b`. Then remove `b` from the YAML and deploy again. Then compare:
  - `docker compose -p <project> -f <file> ps --all --format json`, the old numbers;
  - the panel's `GET …/composes` status, the new numbers.

  Record the result in the ADR. If they differ, apply the fallback named in Review Focus 4 and add a test.

- [ ] **Step 2: Check the fan-out is gone** (Review Focus 5)
  1. Create 30 Composes through the API (one `alpine` service with `sleep infinity`), spread over 10 Environments, and deploy them.
  2. While a Playwright or `curl` sign-in loads `/`, sample `docker exec relist-panel ps -o args` every 0.3 s for 20 s.
  3. Expected: no line contains `compose` and `ps`, and `/sys/fs/cgroup/memory.peak` in the panel container stays below 100 MiB.
  4. Then delete the 30 Composes.

- [ ] **Step 3: Run the full e2e suite**

  Run: `task e2e:reload && task e2e`
  Expected: every spec passes, and `docker inspect relist-panel --format '{{.RestartCount}}'` is unchanged across the run.

- [ ] **Step 4: Write ADR-0043**

  It records:
  - **Decision:** status comes from the Engine API, one call per list, with the exact project label match and one-offs excluded;
  - **Why:** the OOM measurement above;
  - **Considered and rejected:**
    - a semaphore around CLI calls (it still forks, and it serialises page loads behind slow `compose ps`);
    - a background loop running `docker compose ps` into a central cache. It still forks 2 processes per Compose every round, rounds slow down as Composes grow, it works while nobody looks, and it is stale after deploys. The shared 2 s Engine snapshot gives the same "one source" without any of that.
  - **Consequence:** the orphan parity result from Step 1.

- [ ] **Step 5: Commit**: `docs: record compose status from the engine`
