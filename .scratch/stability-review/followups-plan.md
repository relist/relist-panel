# Stability follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** close the stability review's remaining tickets: 15 (Prune skips panel data by default), 14 (minor stalls) and the four items parked during the first round (17–20).

**Architecture:** Prune of volumes and images stops calling Docker's host-wide prune endpoints: the panel lists the unused candidates, drops the ones a panel Compose owns or names, and deletes the rest one by one, unforced. The other items are local fixes in deployments, auth, sources, composes, migration and the Volume helper.

**Tech Stack:** Go 1.26, modernc sqlite, Docker Engine API over the socket, React 19 + antd 6.5.1 + Vitest.

**Spec:** `.scratch/stability-review/spec.md` (findings B6, C5), tickets `.scratch/stability-review/issues/14`, `15`, `17`–`20`.

## Global Constraints

- Work only in `/home/agent/projects/relist/relist-panel-followups` (branch `fix/stability-followups`). Never touch `/home/agent/projects/relist/relist-panel` or the live install's containers, volumes or images.
- **Never call a real prune or delete against this machine's Docker**: volume and image removal is host-wide and the live install's stopped volumes would go. Tests use fakes and command stand-ins only.
- Commits: Conventional Commits, no scope, full-sentence subjects; no `Co-Authored-By`/`Claude-Session` trailers. Each task ends with a separate `chore: close ticket NN <slug>` commit flipping `**Status:** ready-for-agent` → `**Status:** closed` (ticket 14 closes at the end of Task 5).
- Never edit a committed migration; no new Go modules or npm packages.
- Frontend: read https://ant.design/llms.txt and the doc page of each antd component used; antd 6.5.1; follow `docs/GUIDELINES.md`.
- Go verification: `go vet ./... && go test ./...`, plus `go test -race` on touched packages (httpapi ~8 min; iterate with `-run`).
- Web verification (no Node on the host): `docker run --rm -v "$PWD/web":/w -w /w -e CI=true node:24 sh -c 'corepack enable >/dev/null 2>&1; pnpm install --frozen-lockfile >/dev/null && pnpm typecheck && pnpm test'`. Baseline: 55 files / 580 tests.

## Review Focus

- An `external: true` volume a panel Compose names (hand-made, unlabelled) must be skipped by "all unused volumes".
- A volume or image that becomes in use between plan and delete: the unforced delete refuses; that is a skip, never an error that blocks Docker.
- A transport failure halfway through the delete loop is uncertain: block Docker operations as a failed prune does today.
- Login lockout must still hold against parallel wrong guesses once bcrypt runs outside the transaction.
- Boot cleanup must never delete the only copy of a Source.

---

### Task 1: Prune skips panel-owned volumes and images (ticket 15, backend)

**Files:** `internal/dockerhost/dockerhost.go`, `internal/dockerhost/api.go`, new `internal/dockerhost/plan.go` (+ test), `internal/dockerexec/dockerhost.go` (+ test), new `internal/composes/ownership.go` (+ test), `internal/httpapi/httpapi.go`.

**Interfaces (produces):**
```go
// package dockerhost
type Ownership struct {
    Projects map[string]string // deploy project name -> Compose name
    Volumes  map[string]string // volume name a Compose's YAML names -> Compose name
    Images   map[string]string // literal image reference -> Compose name
}
type Candidate struct { ID, Name string; Labels map[string]string; Tags []string; Size *uint64 }
type PlanEntry struct { Name string `json:"name"`; Size *uint64 `json:"size"` }
type SkipEntry struct { Name string `json:"name"`; Compose string `json:"compose,omitempty"`; Reason string `json:"reason"` }
type PrunePlan struct { Kind Kind `json:"kind"`; Delete []PlanEntry `json:"delete"`; Skip []SkipEntry `json:"skip"` }
// Engine gains (the four kinds Volumes, AllVolumes, Images, UnusedImages use these, not Prune):
Candidates(ctx context.Context, kind Kind) ([]Candidate, error)
Remove(ctx context.Context, kind Kind, c Candidate) (removed bool, refusal string, err error) // refusal != "" = Docker said no (in use, conflict); err = transport/unknown
func Plan(kind Kind, candidates []Candidate, owners Ownership, includePanel bool) PrunePlan
func NewAPI(engine Engine, guard *Guard, pending func(context.Context) (bool, error), owners func(context.Context) (Ownership, error)) *API
// PruneResult gains: Skipped []SkipEntry `json:"skipped"`
// package composes
func (s *Service) DockerOwnership(ctx context.Context) (projects, volumes, images map[string]string, err error)
```

- [ ] **Step 1: failing tests.**
  - `dockerhost` `TestPlanSkipsPanelOwned`: a volume labelled with a panel project → skip, `compose` set, reason `used by Compose <name>`; an unlabelled volume a Compose names (external) → skip; a foreign volume → delete; `includePanel` → everything in delete, skip empty. Images: tag matching an owned reference, or a panel project label → skip.
  - API `TestPrunePlanRoute` (`GET /api/docker/prune-plan?kind=all-volumes` → the plan; containers/networks/build-cache kinds answer `{"kind","delete":[],"skip":[]}` because they keep the old path); `TestPruneIncludePanelNeedsStrongerWord` (`includePanel: true` with `DELETE VOLUMES` → 400; with `DELETE PANEL DATA` → runs); `TestPruneRefusalIsSkippedNotBlocked` (Remove refusal → 200, name in `skipped`, guard not blocked); `TestPruneTransportErrorBlocks` (Remove err mid-loop → 502, guard blocked, earlier deletions in the message's context); owners error → 503 and nothing deleted.
  - `dockerexec`: `Candidates` for volumes reads `GET /volumes?filters={"dangling":["true"]}` (Volumes kind: only those labelled `com.docker.volume.anonymous`; AllVolumes: all), images reads `/images/json` and `/containers/json?all=1` and keeps images no container uses (Images kind: only untagged/dangling); `Remove` sends `DELETE /volumes/<name>` and `DELETE /images/<id>` without `force`, maps 409 to a refusal. Use an httptest server on a unix socket or the existing test seam in that file.
  - `composes` `TestDockerOwnership`: projects = every Compose's `DeployProjectName()`; volumes include `name:` values, `external: true` names (key name, `name:`, and old `external: {name: x}`), and `<project>_<key>` defaults; images = literal `image:` values; anything containing `$` skipped.
- [ ] **Step 2: run, expect FAIL.**
- [ ] **Step 3: implement.** Sizes for `Delete` entries come from the candidate (volumes: `UsageData` when present, else nil). `SpaceReclaimed` sums the sizes of what was removed. The prune handler builds the plan fresh at POST time from `owners` + `Candidates` and deletes `plan.Delete` only. Update the `DockerHost` comment ("No arbitrary command, path, filter or force-delete option reaches Docker") to describe the list-then-delete path. `httpapi.NewHandler` passes a closure over `composesService.DockerOwnership`.
- [ ] **Step 4: Go verification.**
- [ ] **Step 5: commit** `fix: skip volumes and images that panel Composes use when pruning, unless the operator includes them` (ticket 15 closes in Task 2).

### Task 2: Prune confirmation lists what goes and what is kept (ticket 15, web)

**Files:** `web/src/pages/DockerPage.tsx` (+ test), `web/src/data/api.ts`, `web/src/data/types.ts`.

**Interfaces:** `api.dockerPrunePlan(kind): Promise<DockerPrunePlan>`; `api.pruneDocker(kind, confirm, includePanel?: boolean)`; types mirror Task 1.

- [ ] **Step 1: failing tests** (`DockerPage.test.tsx`): opening the all-volumes confirmation fetches the plan and shows heading `Deleted (N)` with the names and `Kept because a Compose uses them (M)` with `name — Compose <compose>`; ticking `Also delete the ones panel Composes use` changes the confirm word to `DELETE PANEL DATA` and the POST carries `includePanel: true`; a result with `skipped` shows `Skipped: <name> (<reason>)`; containers/networks/build-cache confirmations are unchanged.
- [ ] **Step 2: run, expect FAIL.**
- [ ] **Step 3: implement** with antd `Checkbox` and the existing DangerModal; a failed plan fetch shows the error and disables confirm.
- [ ] **Step 4: web verification.**
- [ ] **Step 5: commit** `feat: show which volumes and images a prune deletes and which it keeps for panel Composes`; close ticket 15 (`chore: close ticket 15 prune names relist volumes`).

### Task 3: A blocked Docker guard answers 503 and logs once (ticket 14, item 1)

**Files:** `internal/deployments/*` (find where the guard's error becomes a generic 500 and where the worker logs every second), tests beside them.

- [ ] **Step 1: failing tests:** with the host guard blocked, `POST …/deployments` → 503 with `ErrMaintenanceBlocked`'s message (check `errors.Is` survives the wrapping); the worker logs the blocked state once per blocked period (a hook counts log lines over several polls; after `Recover` and a new block it logs again); queued Deployments are not failed while blocked.
- [ ] **Step 2–4:** run (FAIL), implement, verify.
- [ ] **Step 5: commit** `fix: answer 503 with the reason while Docker operations are paused, and log the pause once`.

### Task 4: Login checks the password without holding the database (ticket 14, item 2)

**Files:** `internal/auth/auth.go` (+ tests).

- [ ] **Step 1: failing tests:** a test hook replaces the password comparison with one that blocks; while it blocks, an unrelated query on the same `*sql.DB` completes; N parallel wrong logins (N > the lockout threshold) record at most the threshold before every later attempt answers locked; a password change between the read and the write phase makes the attempt fail.
- [ ] **Step 2: run, expect FAIL.**
- [ ] **Step 3: implement:** an in-process mutex serializes login handling (one admin); short read tx (find admin, lockout) → commit; bcrypt outside any tx; write tx re-reads the account, refuses if the hash changed or the lockout started, then records the failure or runs the TOTP/replay/session steps as today. Rewrite the comment above the old tx to describe the new split and why the mutex replaces the connection hold.
- [ ] **Step 4–5:** verify; commit `fix: check the login password outside the database transaction so other requests are not stalled`.

### Task 5: Boot removes upload leftovers safely (ticket 14, item 3)

**Files:** `internal/sources/srcdir.go` (new `CleanStaging(composeDir string) error` + test), wiring before the server serves (main.go or `httpapi.NewHandler`, for every Compose directory).

- [ ] **Step 1: failing tests** for `CleanStaging`: `src` present → `.upload-*.zip`, `.src.new-*` and `.src.old` removed; `src` missing and `.src.old` present → `.src.old` renamed back to `src`, then the rest removed; `src` missing and no `.src.old` → nothing removed, a log line names the directory; a symlink named like a leftover is removed as a link, never followed.
- [ ] **Step 2–4:** run (FAIL), implement, verify.
- [ ] **Step 5: commit** `fix: clear upload leftovers at boot and put back a Source a crash left half-swapped`; close ticket 14 (`chore: close ticket 14 minor stalls`).

### Task 6: Delete keeps a volume Compose did not create (ticket 17)

**Files:** `internal/composes/volumeplan.go`, `internal/httpapi/volume_plan_test.go`.

- [ ] Flip the `unlabelled` case in `TestDeleteRemovesOwnedVolumesWithDownV` deliberately into a new `TestDeleteKeepsUnlabelledVolume` (kept, reason `not created by Docker Compose`, `DownAndRemoveVolumes` without it); run (FAIL); add the keep rule (`Exists && Project == ""`); verify; commit `fix: keep a named volume Docker Compose did not create when deleting a Compose`; close ticket 17.

### Task 7: Boot snapshot collection runs in the background (ticket 18)

**Files:** `internal/deployments/queue.go`, `internal/deployments/snapshot_gc_test.go`.

- [ ] Adjust `TestRunCollectsSnapshotsAtStart` and add `TestBootCollectionDoesNotDelayFirstDeployment` (a `MountedSources` that blocks until released; a queued Deployment still runs); run (FAIL); start `collectAllSnapshots` in a goroutine after `FailRunning`, inside `safely.Do`, honouring ctx, tracked so `Run` returns only after it ends; verify with `-race`; commit `fix: collect leftover snapshots in the background at boot so the first deploy does not wait for Docker`; close ticket 18.

### Task 8: Migration target sections release the lock on panic (ticket 19)

**Files:** `internal/migration/target.go` (+ `target_test.go`).

- [ ] Add `TestPanicInsideLockedSectionDoesNotDeadlock` (a hook that panics inside a section the run goroutine locks; status returns within 2 s and shows the error); run (FAIL or hang with timeout); add `func (t *Target) locked(fn func())` (Lock, defer Unlock, fn) and route every non-deferred `t.mu.Lock()` section through it or through `defer t.mu.Unlock()`; verify with `-race ./internal/migration`; commit `fix: release the migration lock when a step panics so status keeps answering`; close ticket 19.

### Task 9: Explorer pages past 10,000 entries (ticket 20)

**Files:** `internal/volfs/volfs.go` (`ListFirst` gains `after string`), `cmd/relist-volhelper/main.go` (+ usage/protocol), `internal/volumes` request type and `internal/dockerexec/volume_runner.go` argv, `internal/volumeapi` (`after` query), web `api.ts`, `types.ts`, `store.tsx`, `VolumeExplorerPage.tsx` (+ tests).

**Interfaces:** `func (fs *FS) ListFirst(name, after string, limit int) (entries []Entry, truncated bool, err error)`; helper output `{"entries","truncated","next"}` (`next` = last returned name when truncated, else omitted); `GET …/volumes/list?…&after=<name>`; web `VolumeListing` gains `next?: string`.

- [ ] **Step 1: failing tests:** `ListFirst` with `after` returns the names sorting strictly after it; helper validates `after` like a name (no `/`, not `..`); the volumeapi passes it through; Explorer: truncated listing shows `Showing entries 1–10,000.` and a `Next 10,000` button; on page two it shows `Showing entries 10,001–20,000.` and `Back to the start`.
- [ ] **Step 2–4:** run (FAIL), implement, Go + web verification.
- [ ] **Step 5: commit** `feat: page the Volume Explorer past the first 10,000 entries`; close ticket 20.

## After the last task
Full verification (`go vet ./... && go test -race ./...`, web), rebuild the embedded frontend (`internal/webassets/dist`), whole-branch review by the main session, then ask the operator before push/PR/merge.
