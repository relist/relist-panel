# Chunked Volume Uploads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Volume files up to 10 GiB upload through a Cloudflare proxy as ≤ 64 MiB chunks that resume after any interruption, and are written into the Volume by a background job.

**Architecture:** A new package `internal/chunkupload` owns upload sessions (SQLite table `uploads`), the staged file under `<data>/.uploads/<id>`, offsets, the background job and expiry. It knows nothing about Volumes. `internal/volumeapi` mounts six routes under `…/volumes/uploads`, runs the same checks a direct write runs, and hands `chunkupload` a consume closure that runs the helper's `write`. The web client drives the chunks from a pure, `fetch`-injected module.

**Tech Stack:** Go 1.x stdlib + `modernc.org/sqlite` + goose migrations; React 19 + antd 6.5.1 + Vitest; Playwright for e2e.

**Spec:** `.scratch/chunked-upload/spec.md` (and `docs/adr/0053-chunked-uploads-stage-on-the-panel-disk.md`). Read both before any task.

## Global Constraints

- Volume upload cap: **10 GiB** (`10 << 30`). The direct `PUT …/volumes/content` keeps its 1 GiB cap untouched.
- Chunk size the server tells the client: **32 MiB** (`33554432`). Largest accepted chunk body: **64 MiB** (`64 << 20`).
- Staging dir: `<DataDir>/.uploads/<id>`; `id` is 32 lowercase hex chars from `crypto/rand`.
- Staging margin: **2 GiB** must stay free after reservations. Volume check: `volumeFree ≥ 2 × size`.
- At most **3** open uploads (`uploading` or `failed`) per User, across all Composes.
- Expiry: **24 h** since `updated_at`; **1 h** when `received = 0`. Sweep at start and **hourly**.
- `complete` waits up to **10 s** for the job before answering `202`.
- Client: chunk retry backoff **1, 2, 4, 8, 16 s**; status poll every **2 s**.
- No new Go or web dependency. Never edit a committed migration; the new one is `00058`.
- Commits: Conventional Commits, no scope (`feat: …`, `test: …`), each ending with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Before any `web/` change: read https://ant.design/llms.txt and the docs of each antd component used (`Modal`, `Progress`, `Card`, `Alert`, `Button`), checked against antd `6.5.1`; follow `docs/GUIDELINES.md`.

## Deviations from the spec (decided while planning)

1. **No `Consume` in `Purpose`.** `Purpose` is `{Name, Cap}`. The consume step needs a `volumes.Request` resolved under the caller's permissions, which only exists inside the `complete` request, so `Complete` takes the consume closure as a parameter. Behaviour is the spec's.
2. **`POST B` takes the target in the query string**, exactly the query the direct write takes (`source`, `path`, `service`, `user`, `rootConfirmed`, `bindConfirmed`), and only `{name, size, lastModified}` in the JSON body. The raw query is stored as `target`; `complete` replays it through the same `contentOperation` checks. This reuses the existing validation instead of re-implementing it.
3. **The helper's `free` op already exists** (`cmd/relist-volhelper/main.go`, used by the migration). Only `volumes.OpFree` and its runner argv are new.

Update the spec's Data model note (`target` JSON) and Package and seam section to match these three, in Task 7's commit.

## Review Focus

1. **A chunk retried after the server stored it but the response was lost.** The client resends at the old offset, gets `409` with the new offset, and must continue, not fail and not duplicate bytes. → Task 2 test `TestAppendStaleOffsetAnswersStored`, Task 5 test `follows a 409 offset forward`.
2. **The Compose is deleted, or the user loses Editor rights, while an upload is open.** `complete` must refuse (404/403 from the existing checks) and the job must never run. → Task 4 test `TestUploadCompleteRechecksAccess`.
3. **Two tabs resume the same upload at once.** One `PATCH` wins; the other gets `409`, re-reads the offset and must not corrupt the file. → Task 2 test `TestAppendConcurrentIsBusy`.
4. **Panel shut down while a job is writing.** Shutdown cancels the job; the row must end `failed/interrupted` with the staged file kept, not stuck in `consuming`. → Task 3 test `TestJobCancelledByShutdownIsInterrupted`.
5. **A file of size not a multiple of 32 MiB, and a 1-byte file.** The last chunk is short. A 1-byte file is one chunk and completes inline with no poll. → Task 5 tests `last chunk is short` and `small file completes without polling`.

---

### Task 1: `uploads` table and session creation

**Files:**
- Create: `internal/migrations/00058_uploads.sql`
- Create: `internal/chunkupload/chunkupload.go` (types, `New`, `Create`, `Get`, `ListOpen`, `Cancel`)
- Create: `internal/chunkupload/store.go` (SQL)
- Test: `internal/chunkupload/chunkupload_test.go`

**Interfaces:**
- Produces:
  ```go
  type State string     // StateUploading "uploading", StateConsuming "consuming", StateDone "done", StateFailed "failed"
  type ErrorKind string // KindNone "", KindRefused "refused", KindExists "exists", KindFailed "failed", KindInterrupted "interrupted"
  type Session struct {
      ID string; UserID, ComposeID int64; Purpose, Target, Name string
      Size, LastModified, Received int64
      State State; ErrorKind ErrorKind; Error string
      CreatedAt, UpdatedAt time.Time
  }
  type Purpose struct { Name string; Cap int64 }
  type Options struct {
      Purposes  []Purpose
      FreeBytes func(dir string) (uint64, error) // nil → statfs Bavail*Bsize
      Now       func() time.Time                 // nil → time.Now
  }
  type NewSession struct { Purpose string; UserID, ComposeID int64; Target, Name string; Size, LastModified int64 }
  type NoSpaceError struct { Need, Free uint64 }             // Error(): "need <n> bytes, <f> free"
  var ErrNotFound, ErrTooLarge, ErrTooManyOpen, ErrConsuming error
  const ChunkSize = 32 << 20; const MaxChunk = 64 << 20

  func New(ctx context.Context, db *sql.DB, dataDir string, opts Options) (*Service, error) // mkdir <dataDir>/.uploads 0700; ctx is the server lifetime (jobs, Task 3)
  func (s *Service) Create(ctx context.Context, n NewSession) (Session, error)
  func (s *Service) Get(ctx context.Context, userID int64, id string) (Session, error)       // other user's id → ErrNotFound
  func (s *Service) ListOpen(ctx context.Context, userID, composeID int64, purpose string) ([]Session, error) // every state but done, newest updated first
  func (s *Service) Cancel(ctx context.Context, userID int64, id string) error              // ErrConsuming while consuming
  func (s *Service) StagedPath(id string) string
  ```

- [ ] **Step 1: Write the migration** — the spec's `CREATE TABLE uploads` with the column named `received` (not `offset`) and the index, plus `-- +goose Down DROP TABLE uploads;`, headed by a comment naming the spec and ADR-0053 like `00057_domain_enabled.sql`.

- [ ] **Step 2: Write the failing tests** (open a test DB the way other packages' tests do — search for a `database.Open` helper in `internal/*/..._test.go` and reuse it; seed one user and one compose row)

  - `TestCreateStagesAnEmptyFile`: `Create` returns `Received == 0`, `State == StateUploading`, a 32-hex `ID`; `StagedPath(id)` exists with size 0.
  - `TestCreateRefusesOverCap`: `Purpose{"volume", 10 << 30}`, `Size: 10<<30 + 1` → `ErrTooLarge`; `Size: 0` → error.
  - `TestCreateRefusesFourthOpenUpload`: 3 creates OK (different composes), 4th → `ErrTooManyOpen`; after `Cancel` of one, a create succeeds.
  - `TestCreateReservesSpace`: `FreeBytes` returns `10 GiB`; create `Size: 6 GiB` OK; second `Size: 3 GiB` → `*NoSpaceError` (10 − 6 − 3 < 2 GiB). Received bytes reduce the reservation: set the first row's `received` to 6 GiB via SQL and drop `FreeBytes` to 4 GiB → `Size: 2 GiB` OK.
  - `TestGetHidesOtherUsers`: user 2 `Get`s user 1's id → `ErrNotFound`.
  - `TestCancelRemovesRowAndFile`; `TestCancelWhileConsumingRefused` (set state via SQL).
  - `TestListOpenExcludesDone`.

- [ ] **Step 3: Run** `go test ./internal/chunkupload/` — expect compile failure / FAIL.

- [ ] **Step 4: Implement.** Reservation formula from the spec's Disk space: `free − Σ(size − received) over this-and-other uploading rows − size ≥ 2 GiB`. Create inserts the row and creates the file with `O_CREATE|O_EXCL`, mode 0600; if the file step fails, delete the row.

- [ ] **Step 5: Run** `go test ./internal/chunkupload/ ./internal/database/...` — PASS.

- [ ] **Step 6: Commit** `feat: add the uploads table and chunk upload sessions`

### Task 2: Appending chunks

**Files:**
- Modify: `internal/chunkupload/chunkupload.go`
- Create: `internal/chunkupload/append.go`
- Test: `internal/chunkupload/append_test.go`

**Interfaces:**
- Consumes: Task 1's `Service`, `Session`.
- Produces:
  ```go
  type OffsetError struct { Received int64 } // Error(): "upload is at offset <n>"
  type StateError struct { State State }     // Error(): "upload is <state>"
  var ErrBusy, ErrPastEnd, ErrShortBody error
  func (s *Service) Append(ctx context.Context, userID int64, id string, offset int64, body io.Reader, length int64) (int64, error)
  ```

- [ ] **Step 1: Write the failing tests**
  - `TestAppendWritesInOrder`: two appends of 3 and 2 bytes → returns 3 then 5; staged file content equals the concatenation; `Get().Received == 5`; `UpdatedAt` advanced.
  - `TestAppendStaleOffsetAnswersStored`: after appending 3 bytes, `Append(offset 0)` → `*OffsetError{Received: 3}`, file unchanged.
  - `TestAppendPastEnd`: `Size 4`, `Append(0, 5 bytes)` → `ErrPastEnd`.
  - `TestAppendShortBodyTruncatesBack`: `length 4`, body yields 2 bytes → `ErrShortBody`; file size and `Received` still 0.
  - `TestAppendTruncatesLeftoverBytes`: write junk past `received` into the staged file directly, then `Append(0, "ab")` → file is exactly `"ab"`.
  - `TestAppendConcurrentIsBusy`: a body that blocks on a channel holds one `Append`; a second on the same id → `ErrBusy`; release, first returns OK.
  - `TestAppendOnlyWhileUploading`: state `failed` via SQL → `*StateError{StateFailed}`.

- [ ] **Step 2: Run** `go test ./internal/chunkupload/ -run Append` — FAIL.

- [ ] **Step 3: Implement.** Per-id `sync.Mutex` held in a map under the Service's mutex, taken with `TryLock` (`ErrBusy` when held). Order: check state and offset → open `O_WRONLY` → `Truncate(received)` → `Seek(received)` → `io.CopyN(f, body, length)` → on short copy `Truncate(received)` and `ErrShortBody` → `Sync` → `UPDATE uploads SET received=?, updated_at=?`.

- [ ] **Step 4: Run** — PASS. **Step 5: Commit** `feat: append chunks to a staged upload at a declared offset`

### Task 3: Complete, background job, recovery and sweep

**Files:**
- Create: `internal/chunkupload/job.go`, `internal/chunkupload/sweep.go`
- Test: `internal/chunkupload/job_test.go`, `internal/chunkupload/sweep_test.go`

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces:
  ```go
  type ConsumeFunc func(ctx context.Context, staged io.Reader) error
  type KindError struct { Kind ErrorKind; Err error } // Consume wraps refusals in it; Unwrap returns Err
  var ErrIncomplete error // wrap with the received offset via *OffsetError when offset != size
  func (s *Service) Complete(ctx context.Context, userID int64, id string, consume ConsumeFunc, wait time.Duration) (Session, error)
  func (s *Service) Recover(ctx context.Context) error
  func (s *Service) Sweep(ctx context.Context) error
  func (s *Service) RunSweeper(ctx context.Context) // Sweep every hour until ctx is done
  func (s *Service) Wait()                           // blocks until every job has finished
  ```

- [ ] **Step 1: Write the failing tests**
  - `TestCompleteBeforeLastByte`: `Received < Size` → `*OffsetError`.
  - `TestCompleteFastJobAnswersDone`: consume reads all bytes and returns nil → returned `State == StateDone`, staged file gone, consume saw exactly the staged bytes.
  - `TestCompleteSlowJobAnswersConsuming`: consume blocks on a channel, `wait = 50ms` → returned `StateConsuming`; a second `Complete` → `*StateError{StateConsuming}`; release → after `Wait()`, `Get` is `StateDone`.
  - `TestJobFailureKinds`: consume returns `&KindError{KindRefused, errors.New("need 5, 1 free")}` → `failed/refused`, `Error` contains `need 5`; plain error → `failed/failed`; staged file kept in both.
  - `TestCompleteRetriesFailed`: from `failed`, `Complete` again runs consume once more and ends `done`.
  - `TestJobCancelledByShutdownIsInterrupted`: `New` with a cancellable ctx; consume blocks until `ctx.Done()` then returns `ctx.Err()`; cancel → after `Wait()`, `failed/interrupted`, staged file kept.
  - `TestRecoverInterruptsConsumingAndTruncates`: rows set by SQL — one `consuming` → `failed/interrupted`; one `uploading` with `received 2` and a 5-byte file → file truncated to 2.
  - `TestSweep`: with `Now` injected — a `failed` row 25 h old removed with its file; an `uploading` row 2 h old with `received 0` removed; same row with `received 1` kept; a `consuming` row 48 h old kept; an `uploading` row whose file is missing removed; a stray file `.uploads/abc` with no row removed; a `done` row 25 h old removed.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement.** `Complete` sets `consuming` with a conditional `UPDATE … WHERE state IN ('uploading','failed') AND received = size` (zero rows → re-read and return the right error), starts the goroutine (tracked by a `sync.WaitGroup`, ctx from `New`), and waits on the job's done channel up to `wait`, returning the fresh `Get`. Job errors: `errors.As(*KindError)` → its kind; `ctx.Err() != nil` from the server ctx → `KindInterrupted` with the spec's message "the panel restarted while writing this file; retry"; else `KindFailed`.

- [ ] **Step 4: Run** `go test ./internal/chunkupload/ -race` — PASS. **Step 5: Commit** `feat: write completed uploads in the background, recover and sweep them`

### Task 4: Volume upload routes

**Files:**
- Modify: `internal/volumes/volumes.go` (add `OpFree Op = "free"`)
- Modify: `internal/dockerexec/volume_runner.go` (`OpFree` argv: read-only mount, path `.`) and its test
- Create: `internal/volumeapi/uploads.go`
- Modify: `internal/volumeapi/volumeapi.go` (`New` signature, `RegisterRoutes`)
- Modify: `internal/volumeapi/content.go` (add `consumeError(err error) error`: `writeRunFailure`'s mapping, returning a `*chunkupload.KindError` with `KindRefused` / `KindExists` / `KindFailed`)
- Modify: `internal/authz/routes.go`, `internal/authz/actions.go`, `internal/authz/actions_test.go`
- Modify: `internal/httpapi/coordination.go`, `internal/httpapi/httpapi.go`
- Modify: `internal/migration/manifest.go` (`.uploads` in `skipInDataDir`) and its test
- Test: `internal/httpapi/volume_uploads_test.go`, `internal/httpapi/coordination_test.go` (or the existing file that tests `volumeClass`)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  ```go
  // volumeapi
  type VolumeLocker interface {
      Acquire(ctx context.Context, projectID int64) (func(), error)        // host guard + mutation gate
      AcquireVolumes(ctx context.Context, projectID int64) (func(), error) // exclusive volume lock, taken second
  }
  func New(composes *composes.Service, resolver *volumes.Resolver, runner volumes.Runner, uploads *chunkupload.Service, locks VolumeLocker) *Service
  const uploadCapChunked = 10 << 30
  // authz
  var Unrecorded map[string]string // pattern → reason
  ```
- HTTP (base `B = compose + "/volumes/uploads"`): `POST B`, `GET B`, `GET B/{uploadID}`, `PATCH B/{uploadID}`, `POST B/{uploadID}/complete`, `DELETE B/{uploadID}`. JSON object for an upload: `{"id","source","path","name","size","lastModified","offset","state","errorKind","error","updatedAt"}` (`source`/`path` parsed from the stored query; `offset` is `received`). `POST B` answers `201 {"id","chunkSize","offset"}`.

- [ ] **Step 1: Write the failing tests** in `volume_uploads_test.go`, reusing `setupExplorerCompose` / `volumeOpPath` and the fake runner from `volumeops_test.go`:
  - `TestUploadRoundTrip`: start (`size 5`) → `201`, `chunkSize 33554432`; `PATCH` 3 bytes with `Upload-Offset: 0` → `204`, `Upload-Offset: 3`; `PATCH` 2 bytes at 3 → `5`; `complete` → `200` state `done`; the fake runner's last call is `OpWrite` with `DeclaredBytes 5`, the stored operation user, stdin `"hello"`.
  - `TestUploadStartChecks`: size `10<<30+1` → `413` "upload exceeds the 10 GiB cap"; fake runner `free` stdout `{"bytes": 9}` with size 5 → `507` (needs 10); unknown `source` → `404` (existing check); 4th open → `429`.
  - `TestUploadPatchErrors`: wrong offset → `409` with header `Upload-Offset`; no `Content-Length` → `411`; `Content-Length` over 64 MiB → `413`.
  - `TestUploadCompleteMapsRefusal`: fake runner `Err = &volumes.RefusalError{Message: "need 5"}` → `complete` answers `200` with `state failed`, `errorKind refused`; `complete` again after clearing `Err` → `done`.
  - `TestUploadCompleteRechecksAccess`: delete the Compose (or downgrade the user to viewer) after the last chunk → `complete` is refused with the existing status and the runner saw no `OpWrite`.
  - `TestUploadsAreUserScoped`: a second user's `GET B/{id}` → `404`; `GET B` lists only own uploads, not `done` ones.
  - `TestUploadRoutesSkipTheProjectGate`: hold the Project's gate the way `volume_gate_test.go` does (a blocked archive or Deployment); `PATCH` still answers; `complete` answers `202` and the runner sees no `OpWrite` until the hold is released, then the upload ends `done`.
  - authz: add the six patterns to `Routes` as `LevelEditor`; `Actions` for `POST B` `volume.upload_start`, `POST B/{uploadID}/complete` `volume.upload`, `DELETE B/{uploadID}` `volume.upload_cancel`; `Unrecorded["PATCH "+B+"/{uploadID}"] = "one entry per chunk would flood the Audit log; start, complete and cancel are recorded"`. In `TestEveryChangeHasAnAction` skip patterns in `Unrecorded`, and add `TestUnrecordedHasNoAction` failing when a pattern is in both maps or in `Unrecorded` but not registered.
  - `skipInDataDir(".uploads")` is true (migration manifest test).
  - runner argv test: `OpFree` → `… -v <src>:/data:ro … free -- .`.

- [ ] **Step 2: Run** `go test ./internal/httpapi/ ./internal/authz/ ./internal/dockerexec/ ./internal/migration/` — FAIL.

- [ ] **Step 3: Implement.**
  - `volumeClass`: a new class `volumesStagingOnly` for any method when `parts[7]=="volumes" && len(parts)>=9 && parts[8]=="uploads"`, checked first; `acquireFor` returns only the host guard for it.
  - `startUpload`: `contentOperation(w, r, volumes.OpWrite)` (same checks as the direct write), decode body ≤ 4 KiB, cap check, run `OpFree` with the resolved `Source`/`User` and require `bytes ≥ 2×size` (`507` naming need and free), then `uploads.Create` with `Target = r.URL.RawQuery`. Map `ErrTooLarge` 413, `ErrTooManyOpen` 429, `*NoSpaceError` 507.
  - `appendChunk`: require `Content-Length` (411) and ≤ `MaxChunk` (413), parse `Upload-Offset`, `http.MaxBytesReader(w, r.Body, MaxChunk)`, call `Append`; map `*OffsetError` 409 + header, `*StateError` 409, `ErrBusy` 409, `ErrPastEnd`/`ErrShortBody` 400, `ErrNotFound` 404.
  - `completeUpload`: `Get`; clone `r` with `URL.RawQuery = session.Target` and call `contentOperation` (re-checks policy and path); build the closure: `locks.Acquire(projectID)` then `locks.AcquireVolumes(projectID)`, then `runner.Run(ctx, req with DeclaredBytes = size, staged, io.Discard)`, errors through `consumeError`. Call `Complete(…, 10*time.Second)`; `StateConsuming` → `202`, else `200`.
  - `httpapi.go`: build `chunkupload.New(ctx, db, d.DataDir, Options{Purposes: []Purpose{{"volume", 10 << 30}}})`, call `Recover` then `Sweep` before registering routes (fail `NewHandler` setup the way other setup errors are handled there), start `RunSweeper` under `worker`, and make the returned `wait` also call `uploads.Wait()`. Pass `gate` as the `VolumeLocker`.

- [ ] **Step 4: Run** `go test ./... -race` — PASS. **Step 5: Commit** `feat: chunked Volume upload routes up to 10 GiB`

### Task 5: Web client driver

**Files:**
- Modify: `web/src/data/types.ts` (`VolumeUpload`)
- Modify: `web/src/data/api.ts` (six calls + `volumeUploadsUrl`)
- Create: `web/src/data/chunked-upload.ts`
- Test: `web/src/data/chunked-upload.test.ts`, `web/src/data/api.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export type VolumeUploadState = 'uploading' | 'consuming' | 'done' | 'failed';
  export type VolumeUpload = { id: string; source: string; path: string; name: string; size: number;
    lastModified: number; offset: number; state: VolumeUploadState; errorKind: '' | 'refused' | 'exists' | 'failed' | 'interrupted';
    error: string; updatedAt: string };
  // api
  startVolumeUpload(location: ComposePath, source: string, path: string, file: { name: string; size: number; lastModified: number }, access?: VolumeAccess): Promise<{ id: string; chunkSize: number; offset: number }>
  listVolumeUploads(location: ComposePath): Promise<VolumeUpload[]>
  volumeUploadStatus(location: ComposePath, id: string): Promise<VolumeUpload>
  appendVolumeUploadChunk(location: ComposePath, id: string, offset: number, chunk: Blob, signal?: AbortSignal): Promise<{ offset: number; conflict: boolean }> // 409 with Upload-Offset → conflict:true
  completeVolumeUpload(location: ComposePath, id: string): Promise<VolumeUpload | { state: 'consuming' }>
  cancelVolumeUpload(location: ComposePath, id: string): Promise<void>
  // chunked-upload.ts
  export type UploadTransport = { append(offset: number, chunk: Blob, signal: AbortSignal): Promise<{ offset: number; conflict: boolean }>;
    complete(): Promise<VolumeUpload | { state: 'consuming' }>; status(): Promise<VolumeUpload> };
  export class UploadPausedError extends Error { constructor(readonly offset: number, readonly cause: unknown) }
  export async function runChunkedUpload(opts: { file: Blob; chunkSize: number; offset: number; transport: UploadTransport;
    signal: AbortSignal; onProgress: (sent: number) => void; onPhase: (phase: 'uploading' | 'offline' | 'writing') => void;
    sleep?: (ms: number) => Promise<void>; waitOnline?: () => Promise<void>; isOnline?: () => boolean }): Promise<VolumeUpload>
  ```

- [ ] **Step 1: Write the failing tests** (`chunked-upload.test.ts`, fake transport, `sleep` resolves immediately and records delays):
  - `sends 80 MiB as 32, 32, 16` — appends at offsets 0, 33554432, 67108864; last chunk is short; `onProgress` ends at the file size.
  - `resumes from a non-zero offset` — starts at `offset 33554432`, first append at that offset.
  - `follows a 409 offset forward` — first append answers `{conflict: true, offset: 33554432}`; next append is at that offset, no bytes resent before it.
  - `retries with backoff then pauses` — append always rejects `TypeError('network')`: delays recorded `[1000, 2000, 4000, 8000, 16000]`, then rejects `UploadPausedError` with the current offset.
  - `waits while offline` — `isOnline` false once: `onPhase('offline')`, `waitOnline` awaited, then continues.
  - `small file completes without polling` — `complete` returns a `done` upload; `status` never called.
  - `polls every 2 s while writing` — `complete` returns `{state:'consuming'}`, `status` returns consuming, then done; sleeps `[2000]`, `onPhase('writing')`.
  - `abort stops sending` — abort after first append; rejects with `AbortError`; no further append.
  - `api.test.ts`: `appendVolumeUploadChunk` sends `PATCH` with `Upload-Offset` and reads the response header; a `409` returns `conflict: true` with the header's offset; other errors throw `ApiError`.

- [ ] **Step 2: Run** `cd web && pnpm test -- chunked-upload api` — FAIL.
- [ ] **Step 3: Implement.** `failed` from `complete`/`status` resolves (the page shows it), it does not throw.
- [ ] **Step 4: Run** `cd web && pnpm test && pnpm typecheck` — PASS. **Step 5: Commit** `feat: web driver for chunked Volume uploads`

### Task 6: Volume Explorer UI

**Files:**
- Modify: `web/src/pages/volume-explorer-view.ts`, `web/src/pages/volume-explorer-view.test.ts`
- Modify: `web/src/pages/VolumeExplorerPage.tsx`, `web/src/pages/VolumeExplorerPage.test.tsx`
- Modify: `docs/GUIDELINES.md:388` ("1 GiB per upload" → "10 GiB per upload, sent in resumable 32 MiB chunks; 1 GiB per editor save")

**Interfaces:**
- Consumes: Task 5.
- Produces (view helpers):
  ```ts
  export const UPLOAD_MAX_BYTES = 10 * (1 << 30);        // was 1 << 30; the editor save keeps its own 1 GiB wording
  export function matchOpenUpload(uploads: VolumeUpload[], file: { name: string; size: number; lastModified: number }, path: string): VolumeUpload | undefined
  export function uploadProgressText(sent: number, total: number): string // "4.2 GiB of 10 GiB"
  export function uploadEta(sentBytes: number, elapsedMs: number, total: number): string | undefined
  export function volumeUploadFailure(upload: VolumeUpload): VolumeFailure // refused → "Not enough space in the Volume", interrupted → "The panel restarted while writing", else "The upload could not be written"; description carries upload.error
  ```

- [ ] **Step 1: Write the failing tests**
  - view: `uploadTooLarge` refuses `10 GiB + 1` with "Upload exceeds the 10 GiB cap" and passes `1 GiB + 1`; update the existing 1 GiB upload assertions (lines ~280–291) to 10 GiB, keep the editor-save ones (~462–465) at 1 GiB; `matchOpenUpload` matches only on path + name + size + lastModified and ignores `done`; `uploadProgressText(4.2 GiB, 10 GiB)`; `volumeUploadFailure` per kind.
  - page: picking a file matching an open upload shows the Resume / Start over `Modal`; Start over cancels the old upload first; an open upload shows in the "Unfinished uploads" `Alert` with **Discard**; during upload the card shows progress and **Cancel**, and the Upload button is disabled; a `failed` result shows **Retry** and **Discard**.
- [ ] **Step 2: Run** `cd web && pnpm test -- volume-explorer VolumeExplorerPage` — FAIL.
- [ ] **Step 3: Implement** the spec's User interface states 1–5 and the Unfinished uploads `Alert`, `beforeunload` while uploading or writing. The toolbar tooltip: "Uploads a file of up to 10 GiB into this directory, in pieces that resume if the connection drops. The Volume's free space is checked before anything is sent."
- [ ] **Step 4: Run** `cd web && pnpm test && pnpm typecheck && pnpm build` — PASS. **Step 5: Commit** `feat: resumable uploads in the Volume Explorer`

### Task 7: e2e and spec touch-up

**Files:**
- Create: `e2e/tests/volume-upload.spec.ts`
- Modify: `.scratch/chunked-upload/spec.md` (the three deviations above)

- [ ] **Step 1: Write the spec file.** Set up a Compose with a named volume the way `editor.spec.ts` sets up its Compose, deploy it, open the Volume Explorer.
  - `uploads 80 MiB in three chunks`: write an 80 MiB random temp file on disk, `setInputFiles(path)`, wait for success, download it back via `volumes/read` and compare size and SHA-256; record the `PATCH` requests and assert there are 3.
  - `resumes after an interruption`: `page.route` aborts the second `PATCH`; once the UI shows "Upload paused", reload, pick the same file path again, choose **Resume**, and compare the SHA-256. Use a file on disk, never a buffer (a buffer gets a new `lastModified`).
- [ ] **Step 2: Run** `task e2e -- volume-upload` (reads ports from `.data/e2e/env`; never guess them) — PASS.
- [ ] **Step 3: Update the spec** for the three deviations.
- [ ] **Step 4: Commit** `test: e2e for chunked Volume uploads`

### Live check (after merge approval, by the user or with them)

Upload a ~2 GiB file through the real domain behind Cloudflare; close the tab halfway, reopen, resume. Not automatable here.
