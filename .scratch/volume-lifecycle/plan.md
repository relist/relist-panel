# Volume lifecycle on deploy — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plain deploy removes orphan containers, a separate action redeploys with freshly wiped named volumes, and the Volumes tab lists and deletes a Compose's orphan volumes.

**Architecture:** A is one argv change. B is a new `deploy-fresh` route that checks the confirmation, runs `DownWithVolumes` against the files already on disk, then rewrites the files and runs `Up`. C extends the existing `volumes.CLI` port with list/remove, puts the "is this an orphan" authority in `volumes.Resolver`, and exposes it through two `volumeapi` routes.

**Tech Stack:** Go (`net/http` mux, SQLite, fakes at the `composes.ComposeRunner` and `volumes.CLI` seams), React + `antd` 6.5.1 + vitest in `web/`.

**Spec:** [.scratch/volume-lifecycle/spec.md](spec.md)

## Global Constraints

- Commits: Conventional Commits, no scope (`feat: …`, `fix: …`, `docs: …`); **no** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md).
- Copy says **Compose**, never "stack".
- Plain deploy never deletes a volume. Bind mounts are never deleted by any new code.
- `docker volume rm` is never run with `-f`.
- Deletion authority is re-derived server side on every request; the client's list is never trusted (spec §C).
- No new port (ADR-0003): only `volumes.CLI` and its fake grow.
- Before any edit under `web/`: read `https://ant.design/llms.txt` and the linked pages for the components used, read `docs/GUIDELINES.md`, and check props against `antd` 6.5.1 (AGENTS.md).
- Behaviour needing a real Docker daemon is verified manually in Task 6, not by fakes.

## Review Focus

- A refused `deploy-fresh` (`confirmName` `"Web "` or `"WEB"`) must run nothing **and leave the compose file on disk byte-identical** (Task 2).
- `down -v` must run against the files already on disk, before the new YAML is written, so the volumes deleted are the ones the dialog listed (Task 2).
- A volume whose label is another project's, or an explicitly named (`name:`) volume another Compose may share, must never be reported as an orphan (Task 3).
- A `DELETE …/volumes/orphans/{name}` for a name in the current YAML or a path-like name (`../x`) must remove nothing (Task 5).
- A volume still in use must come back as 409 with Docker's message and never be retried forcibly (Task 5).

## File Structure

- Modify `internal/dockerexec/dockerexec.go` — `upArgs()`, `Up` uses it.
- Modify `internal/composes/composes.go` — split `deploy` into `loadDeployable` / `writeDeployFiles` / `completeDeploy`, add `deployFresh`, route.
- Modify `internal/composes/teardown.go` — `ErrAmbiguousIdentity`.
- Modify `internal/volumes/resolve.go` — `ProjectVolume`, label consts, `CLI` gains two methods.
- Create `internal/volumes/orphans.go` (+ `orphans_test.go`) — `Resolver.ListOrphans`, `Resolver.RemoveOrphan`, errors.
- Modify `internal/volumes/faketest/cli.go` — scripts the two new methods.
- Modify `internal/dockerexec/volume_resolver.go` (+ test) — real `ListProjectVolumes`, `RemoveVolume`.
- Create `internal/volumeapi/orphans.go` — two handlers; register in `volumeapi.go`.
- Modify `web/src/data/{types,api,store}.ts(x)`, `web/src/pages/ComposePage.tsx`, `web/src/components/ComposeVolumesTab.tsx`, `web/src/components/volumes-view.ts` (+ tests).
- Modify `CONTEXT.md`, `README.md`.

---

### Task 1: Plain deploy removes orphan containers (A)

**Files:**
- Modify: `internal/dockerexec/dockerexec.go:28-32`
- Test: `internal/dockerexec/dockerexec_test.go` (create)

**Interfaces:**
- Produces: `func upArgs() []string` returning `["up","-d","--build","--remove-orphans"]`.

- [ ] **Step 1: Write the failing test** `TestUpArgsRemoveOrphanContainersButNeverVolumes` asserting `reflect.DeepEqual(upArgs(), []string{"up","-d","--build","--remove-orphans"})` and that no element equals `-v` or `--volumes`.
- [ ] **Step 2: Run** `go test ./internal/dockerexec -run TestUpArgs -v`. Expected: FAIL, `undefined: upArgs`.
- [ ] **Step 3: Implement** `upArgs()` and change `Runner.Up` to `run(ctx, req, upArgs()...)`. (`run` execs the real CLI and has no injection seam, so the argv is tested through this helper.)
- [ ] **Step 4: Run** `go test ./internal/dockerexec ./internal/httpapi`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: remove orphan containers on deploy`.

---

### Task 2: Deploy with fresh volumes (B)

**Files:**
- Modify: `internal/composes/composes.go` (`deploy` at ~line 308, `RegisterRoutes` at line 122)
- Test: `internal/httpapi/httpapi_test.go` (new tests beside `TestComposeDeployRecordsDeployedAtOnSuccessOnly`); helper `composeDeployFreshPath` next to `composeDeployPath` (line 2728)

**Interfaces:**
- Produces (unexported, `*Service`):
  - `loadDeployable(w http.ResponseWriter, r *http.Request) (Compose, RunRequest, bool)` — route ids, `requireEnvironment`, load the compose, empty-YAML 400, `requireUniqueIdentity` 409. Writes nothing to disk. Writes its own error response; `ok=false` means return.
  - `writeDeployFiles(w http.ResponseWriter, compose Compose, req RunRequest) bool` — `MkdirAll`, `resolveCompose`, write the compose file and the env file (the variables lookup stays here). Writes its own error response.
  - `completeDeploy(w http.ResponseWriter, r *http.Request, compose Compose, req RunRequest)` — `markDeployed`, reload, `writeJSON(200, withRefreshedStatus(...))`.
  - `deployFresh(w, r)` — route `POST …/composes/{composeID}/deploy-fresh`, body `{"confirmName": string}`.
- Existing `deploy` becomes `loadDeployable` → `writeDeployFiles` → `s.runner.Up` (502 `deploy failed: …`) → `completeDeploy`, behaviour unchanged.
- `deployFresh` order: `loadDeployable` → check `confirmName` → if `req.ComposeFilePath` exists, `s.runner.DownWithVolumes` against the files **already on disk** → `writeDeployFiles` → `s.runner.Up` → `completeDeploy`. A missing compose file skips the down step, as Compose delete does.
- No coordination change: `coordinatedProject` in `internal/httpapi/coordination.go` already gates every non-GET `/api/projects/{id}/…` request.

- [ ] **Step 1: Write failing tests** (each uses `newTestServer`, `setupAndLogin`, a Compose with YAML, and `server.runner.calls`):
  - `TestComposeDeployFreshRunsDownWithVolumesThenUp`: deploy once, then `confirmName` equal to the Compose name → 200; the `method` sequence of calls after the first deploy, ignoring `status`, is exactly `["down-with-volumes","up"]`; `lastDeployedAt` advances.
  - `TestComposeDeployFreshDownRunsAgainstTheFilesAlreadyOnDisk`: deploy, update the YAML, then deploy-fresh; the fake records the compose file's content at the moment of `down-with-volumes` (add an optional hook to `fakeComposeRunner` that reads `req.ComposeFilePath`) and it is the **old** YAML; afterwards the file holds the new YAML.
  - `TestComposeDeployFreshOnANeverDeployedComposeSkipsDown`: no prior deploy → calls are `["up"]` only.
  - `TestComposeDeployFreshRefusesWrongConfirmName`: table `""`, `"Web "`, `"WEB"`, missing body, malformed JSON → 400, `server.runner.calls` is empty, and the on-disk compose file is byte-identical to its content before the request (update the YAML first so a premature write would show).
  - `TestComposeDeployFreshDoesNotUpWhenDownFails`: `downWithVolumesErr` set → 502, body error contains `delete volumes failed`, no `up` call, file unchanged, `lastDeployedAt` unchanged.
  - `TestComposeDeployFreshReportsRemovedVolumesWhenUpFails`: `upErr` set → 502, body error contains `volumes were removed` and `stopped`, `lastDeployedAt` unchanged.
  - `TestComposeDeployFreshReportsRemovedVolumesWhenWritingFilesFails`: make the Compose directory unwritable after the down step (hook) → 500, body error contains `volumes were removed`, no `up` call.
  - `TestComposeDeployFreshRefusesAmbiguousLegacyIdentity` in `legacy_identity_test.go`: colliding legacy composes → 409, no runner calls.
- [ ] **Step 2: Run** `go test ./internal/httpapi -run DeployFresh -v`. Expected: FAIL (404 route not found).
- [ ] **Step 3: Refactor** `deploy` into `loadDeployable` / `writeDeployFiles` / `completeDeploy` with the signatures above, keeping every existing status code and message.
- [ ] **Step 4: Run** `go test ./internal/httpapi -run 'Deploy' -v`. Expected: all existing deploy tests PASS (pure refactor).
- [ ] **Step 5: Implement `deployFresh`** and register the route. Decode the body with `json.NewDecoder`; reject with 400 unless `input.ConfirmName == compose.Name` (exact, no trimming), checked **after** `loadDeployable` so the Compose exists, **before** any runner call or file write. Error strings: down failure `"delete volumes failed: "+err`; write-files failure and up failure both end `"…after volumes were removed: "+err+". The Compose is stopped; fix the cause and deploy again."` (the write-files failure keeps its 500 status).
- [ ] **Step 6: Run** `go test ./internal/...`. Expected: PASS.
- [ ] **Step 7: Commit** `feat: add deploy with fresh volumes`.

---

### Task 3: Orphan volume discovery in the volumes package (C, domain)

**Files:**
- Modify: `internal/volumes/resolve.go` (`CLI` interface at line 27), `internal/volumes/faketest/cli.go`
- Create: `internal/volumes/orphans.go`, `internal/volumes/orphans_test.go`

**Interfaces:**
- Produces in `volumes`:
  - `const ComposeProjectLabel = "com.docker.compose.project"` and `const ComposeVolumeLabel = "com.docker.compose.volume"`
  - `type ProjectVolume struct { Name string; CreatedAt string; Labels map[string]string }`
  - `CLI` gains `ListProjectVolumes(ctx context.Context, project string) ([]ProjectVolume, error)` and `RemoveVolume(ctx context.Context, name string) error`
  - `var ErrNotOrphan = errors.New(...)`, `var ErrVolumeInUse = errors.New(...)`
  - `func (r *Resolver) ListOrphans(ctx context.Context, ref ComposeRef) ([]ProjectVolume, error)` — name-sorted, never nil.
  - `func (r *Resolver) RemoveOrphan(ctx context.Context, ref ComposeRef, name string) error` — returns `ErrNotOrphan` unless `name` is in the freshly derived `ListOrphans`; passes `ErrVolumeInUse` through.
- `faketest.CLI` gains `ProjectVolumes []volumes.ProjectVolume`, `ListProjectVolumesErr error`, `ListedProjects []string`, `RemoveVolumeErr error`, `RemovedVolumes []string`.
- Rule: an orphan is a listed volume with `Labels[ComposeProjectLabel] == ref.ProjectName`, a non-empty `Labels[ComposeVolumeLabel]`, `Name == ref.ProjectName+"_"+Labels[ComposeVolumeLabel]`, and a `Name` that is not the `Source` of any `VolumeNamed` volume in the current resolution.

- [ ] **Step 1: Write failing tests** with `faketest.CLI` and a `ComposeConfigOutput` declaring one named volume `app_data`:
  - `TestListOrphansExcludesVolumesInTheCurrentYAML`: listed `app_data` and `app_old` → only `app_old`.
  - `TestListOrphansIgnoresVolumesWhoseLabelIsAnotherProject`: listed `other_x` labelled `other` → empty slice, `len==0` and non-nil.
  - `TestListOrphansIgnoresExplicitlyNamedAndUnlabelledVolumes`: a volume named `shared_data` with the project label and `com.docker.compose.volume=data`, and one with the project label but no volume label → neither is listed.
  - `TestListOrphansIsSortedByName`.
  - `TestListOrphansSurfacesTheCLIFailure`: `ComposeConfigErr` → error returned, nothing listed or removed; `ListProjectVolumesErr` likewise.
  - `TestRemoveOrphanRefusesWhatIsNotAnOrphan`: names `app_data` (current), `other_x` (other project), `../x`, `missing` → each `errors.Is(err, ErrNotOrphan)` and `RemovedVolumes` empty.
  - `TestRemoveOrphanRemovesOnlyTheNamedOrphan` → `RemovedVolumes == ["app_old"]`.
  - `TestRemoveOrphanPassesTheInUseErrorThrough`: `RemoveVolumeErr = ErrVolumeInUse` → `errors.Is(err, ErrVolumeInUse)`.
- [ ] **Step 2: Run** `go test ./internal/volumes -run Orphan -v`. Expected: FAIL (does not compile).
- [ ] **Step 3: Implement** the types, `CLI` methods, fake and the two `Resolver` methods; the filter lives in one unexported pure function `orphansOf(project string, listed []ProjectVolume, current []Volume) []ProjectVolume`.
- [ ] **Step 4: Run** `go test ./internal/volumes/...`. Expected: PASS. The whole module builds again after Task 4 adds the real adapter, so Task 4 follows immediately and nothing is committed to the branch in between.
- [ ] **Step 5: Do not commit yet.** `dockerexec.VolumeResolver` no longer satisfies `volumes.CLI`; Task 4 restores the build and the two tasks share one commit.

---

### Task 4: Real Docker adapter for list and remove (C, adapter)

**Files:**
- Modify: `internal/dockerexec/volume_resolver.go`, test `internal/dockerexec/volume_resolver_test.go`

**Interfaces:**
- Consumes: `volumes.ProjectVolume`, `volumes.ComposeProjectLabel`, `volumes.ErrVolumeInUse`.
- Produces on `*VolumeResolver`: `ListProjectVolumes` and `RemoveVolume` (same `runDocker` seam, argv only).
- Calls: `docker volume ls -q --filter label=com.docker.compose.project=<project>` then, when names exist, `docker volume inspect <names…>` whose JSON array supplies `Name`, `CreatedAt`, `Labels`. Remove: `docker volume rm <name>`, stderr containing `volume is in use` maps to `ErrVolumeInUse` wrapping Docker's message.

- [ ] **Step 1: Write failing tests** with a scripted `runDocker` (pattern at the top of `volume_resolver_test.go`):
  - `TestVolumeResolverListProjectVolumesRunsLsThenInspect` — asserts both exact argv, and the parsed `[]ProjectVolume`.
  - `TestVolumeResolverListProjectVolumesSkipsInspectWhenNothingMatches` — only one docker call, empty non-nil slice.
  - `TestVolumeResolverListProjectVolumesFoldsStderrIntoTheError`.
  - `TestVolumeResolverRemoveVolumeRunsTheExactArgv` — `["docker","volume","rm","app_old"]`, no `-f`.
  - `TestVolumeResolverRemoveVolumeMapsInUseToErrVolumeInUse` — and the Docker message is still in `err.Error()`.
- [ ] **Step 2: Run** `go test ./internal/dockerexec -run VolumeResolver -v`. Expected: FAIL.
- [ ] **Step 3: Implement** both methods; this restores the build broken by Task 3.
- [ ] **Step 4: Run** `go build ./... && go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** Tasks 3 and 4 together as `feat: derive and remove a Compose's orphan volumes`.

---

### Task 5: Orphan volume API (C, HTTP)

**Files:**
- Modify: `internal/composes/teardown.go` (wrap with `%w`), `internal/volumeapi/volumeapi.go` (`RegisterRoutes`)
- Create: `internal/volumeapi/orphans.go`
- Test: `internal/httpapi/volume_orphans_test.go` (create; reuse `newTestServer`, `setupAndLogin`, `volumeCLI`)

**Interfaces:**
- Consumes: `Resolver.ListOrphans`, `Resolver.RemoveOrphan`, `ErrNotOrphan`, `ErrVolumeInUse` (Task 3).
- Produces: `var ErrAmbiguousIdentity = errors.New("ambiguous deploy identity")` in `composes`, wrapped by `requireUniqueIdentity` so its existing message text is unchanged.
- Routes: `GET …/composes/{composeID}/volumes/orphans` → 200 `{"orphans":[{"name":string,"createdAt":string}]}` (always an array); `DELETE …/volumes/orphans/{name}` → 204.
- Status mapping: unknown ids 404; `ErrAmbiguousIdentity` 409; resolution/list failure 502; `ErrNotOrphan` 404; `ErrVolumeInUse` 409 with Docker's message; other remove failure 502.

- [ ] **Step 1: Write failing tests:**
  - `TestOrphanVolumesListsOnlyThisComposesOrphans` — fake lists one current, one orphan, one other-project → response has exactly the orphan.
  - `TestOrphanVolumesListIsAnEmptyArrayWhenThereAreNone` — body contains `"orphans":[]`.
  - `TestOrphanVolumesListSurfacesTheNotDeployedError` — `ComposeConfigErr` → 502 containing the CLI text.
  - `TestDeleteOrphanVolumeRemovesIt` → 204, `volumeCLI.RemovedVolumes == ["app_old"]`.
  - `TestDeleteOrphanVolumeRefusesCurrentOtherProjectAndPathLikeNames` — 404 each, `RemovedVolumes` empty; path-like uses `%2E%2E%2Fx`.
  - `TestDeleteOrphanVolumeInUseIs409WithDockersMessage`.
  - `TestOrphanVolumesRefuseAmbiguousLegacyIdentity` (in `legacy_identity_test.go`) — both routes 409, `volumeCLI` untouched.
  - `TestOrphanVolumeRoutesNeedAuthentication` — 401 without a session.
- [ ] **Step 2: Run** `go test ./internal/httpapi -run Orphan -v`. Expected: FAIL (404).
- [ ] **Step 3: Implement** `ErrAmbiguousIdentity`, the two handlers and route registration. The handlers resolve the Compose through `s.composes.ComposeRunRequest` themselves (not `composeRequest`) so `errors.Is(err, composes.ErrAmbiguousIdentity)` can map to 409; unknown ids and 404 behave as in `composeRequest`.
- [ ] **Step 4: Run** `go test ./internal/...`. Expected: PASS, including the unchanged legacy-identity tests.
- [ ] **Step 5: Commit** `feat: serve a Compose's orphan volumes over the api`.

---

### Task 6: Frontend (B and C)

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/data/store.tsx`, `web/src/pages/ComposePage.tsx`, `web/src/components/ComposeVolumesTab.tsx`, `web/src/components/volumes-view.ts`
- Test: `web/src/data/api.test.ts`, `web/src/components/volumes-view.test.ts`

**Interfaces:**
- Produces: `type OrphanVolume = { name: string; createdAt: string }`; `api.deployComposeFresh(location: ComposePath, confirmName: string): Promise<Compose>`; `api.listOrphanVolumes(location): Promise<{ orphans: OrphanVolume[] }>`; `api.deleteOrphanVolume(location, name): Promise<void>` (URL-encodes `name`); store action `deployComposeFresh` mirroring `deployCompose`, pending key `deploy:${compose.id}`; pure `orphansView(payload?: { orphans: OrphanVolume[] }, error?: string)` in `volumes-view.ts` returning `{ status: 'loading' | 'error' | 'empty' | 'ready', … }`.

- [ ] **Step 1: Read the docs** listed in Global Constraints; confirm `Dropdown`, `Space.Compact`, `Modal` and `Popconfirm` props against `antd` 6.5.1 (do not use the deprecated `Dropdown.Button` if 6.5.1 deprecates it).
- [ ] **Step 2: Write failing tests:** `api.test.ts` — `deployComposeFresh` POSTs `…/deploy-fresh` with JSON `{confirmName}`; `deleteOrphanVolume` encodes `a/b` as `a%2Fb`. `volumes-view.test.ts` — `orphansView` returns `loading` with no payload and no error, `error` with an error, `empty` for `[]`, `ready` with rows otherwise.
- [ ] **Step 3: Run** `cd web && npx vitest run src/data/api.test.ts src/components/volumes-view.test.ts`. Expected: FAIL.
- [ ] **Step 4: Implement** types, api methods, store action and `orphansView`.
- [ ] **Step 5: Run** the same command. Expected: PASS.
- [ ] **Step 6: Build the UI.** The dialog's confirm sends `compose.name` itself (`DangerModal.onConfirm` takes no argument and its own check trims the typed text; do not change `DangerModal` to forward the raw input — the server compares exactly). The dropdown item is disabled with a tooltip when the Compose was never deployed (the Volumes payload is an error) or the Volumes tab is hidden (`available: false`). In `ComposePage.tsx`, the Deploy button (line ~387) becomes a `Space.Compact` of the existing button and a `Dropdown` trigger with one item, **Deploy with fresh volumes**. That item opens the existing `DangerModal` (`confirmWord = compose.name`) whose `consequences` list the Compose's named volumes (from the page's already-fetched Volumes payload), say bind mounts are not deleted, and point to the Volumes tab for a tar.gz backup first; confirming runs the same save-first steps as `deploy` (line ~276, `deploySteps`) and then `deployComposeFresh`. In `ComposeVolumesTab.tsx`, add the orphan section: fetched once by the page and passed in, rows with a per-row `Popconfirm`-or-`DangerModal` delete that states the data is permanently lost, no bulk delete, an `Alert` carrying the API message on 409 or 502, and an empty state.
- [ ] **Step 7: Run** `cd web && npm run typecheck && npm test && npm run build`. Expected: all pass.
- [ ] **Step 8: Check in a browser** (`run` skill): plain Deploy unchanged; dropdown opens the dialog; confirm stays disabled until the name matches; the orphan section lists and deletes. Report anything not exercised.
- [ ] **Step 9: Commit** `feat: add fresh volume deploy and orphan cleanup to the compose page`.

---

### Task 7: Docs and verification against real Docker

**Files:**
- Modify: `CONTEXT.md`, `README.md` (Volume files section, ~line 76), 

- [ ] **Step 1: Docs.** `CONTEXT.md`: add **Orphan Volume** (a named volume labelled with this Compose's project that its YAML no longer declares; _Avoid_: dangling volume, which is Docker's term for any unreferenced volume). `README.md`: deploy now removes orphan containers; fresh volumes and orphan cleanup; bind mounts are never deleted.
- [ ] **Step 2: Verify on a real daemon** with a throwaway Compose in the panel, recording each result in the commit message body:
  1. Deploy a Compose with services `a` and `b`; remove `b` from the YAML; deploy → `docker ps -a` shows `b`'s container gone.
  2. Same step: `docker volume ls` still lists `b`'s named volume (confirms `--remove-orphans` leaves volumes alone).
  3. The Volumes tab lists that volume as an orphan; delete it → `docker volume ls` no longer lists it.
  4. Start a container mounting an orphan, try to delete it → 409 carrying Docker's "in use" text.
  5. Write a file into a named volume, run Deploy with fresh volumes → the file is gone and the Compose is running; a bind mount's files are untouched.
  6. Declare an `external: true` volume → it never appears in the orphan list.
  7. Declare a volume with an explicit `name:` shared by two Composes, drop it from the first's YAML → it never appears as an orphan of the first.
  8. `docker volume inspect` on a Compose-created named volume shows the `com.docker.compose.project` and `com.docker.compose.volume` labels, and the volume's name is `<project>_<logical>`; a Compose with an anonymous volume leaves no entry in the orphan list. If any of these differ from the spec, stop and update the spec's orphan definition before merging.
- [ ] **Step 3: Run** `go test ./... && (cd web && npm test)`. Expected: PASS.
- [ ] **Step 4: Commit** `docs: describe fresh volumes and orphan cleanup`.
