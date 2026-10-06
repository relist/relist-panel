# Image upload — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level Images page that lists every Docker image on the host and loads one `docker save` archive (up to 10 GiB, optionally compressed). The archive is streamed through the panel into the Engine API.

**Architecture:** A new domain package `internal/images` owns `GET`/`PUT /api/images` behind one consumer-declared seam, `images.Images`. Its real adapter, `internal/dockerexec/images.go`, calls `GET /images/json`, `GET /containers/json?all=1` and `POST /images/load?quiet=1` over the Docker socket client `newSocketClient`. The page loads the list with SWR and uploads with an `XMLHttpRequest` helper, so it can show progress.

**Tech Stack:** Go 1.25 stdlib `net/http`; React 19, antd 6.5.1, `swr` 2.5.1 (new), dayjs; vitest.

**Spec:** [.scratch/image-upload/spec.md](spec.md)

## Global Constraints

- Commits: Conventional Commits, no scope. **No** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md).
- Upload cap: `10 << 30` bytes. It is hard-coded, with no env knob, on the server (authoritative) and in the client (pre-check).
- Accepted extensions (client): `.tar`, `.tar.gz`, `.tgz`, `.tar.zst`, `.tar.xz`, `.tar.bz2`.
- Copy, verbatim from the spec:
  - `upload requires a declared length (Content-Length)` (411)
  - `upload exceeds the 10 GiB cap` (413)
  - `Docker is not reachable` (503)
  - `Upload cancelled`, `Upload failed — connection lost`, `Loading into Docker…`
  - Explainer: `Load an image built with ` + `docker save` + ` onto this host, then reference it by name in a Compose file.`
  - The daemon's own load error text is passed through unchanged (422).
- Routes live under `/api/images`, outside `/api/projects`. Do not change `internal/httpapi/coordination.go`.
- Domain words come from CONTEXT.md: Image, Compose, Container. Never "stack", "build" or "artifact" for an Image.
- The panel stores nothing about images. No migration, no SQLite table.
- Before any edit under `web/`, read `https://ant.design/llms.txt` and the pages for the components you use, read `docs/GUIDELINES.md`, and check props against antd 6.5.1 (AGENTS.md).
- Behaviour that needs a real Docker daemon is verified manually (Task 7), never through fakes.

## Review Focus

- **A daemon stream that reports `Loaded image: …` and then an `errorDetail`** must fail the whole load (422), not report the names it saw first. Covered in Task 2.
- **A daemon JSON line longer than 64 KiB** (a long error, or many tags) must not break parsing. Read the stream with `json.Decoder`, never `bufio.Scanner`'s default buffer. Covered in Task 2.
- **A client that disconnects mid-upload** must cancel the socket request, so `Load` returns promptly with the context error and no goroutine is left blocked on the body. Covered in Task 2.
- **A body shorter than its declared `Content-Length`** (connection cut) must reach the operator as a failure, never as `200` with no images. Any non-sentinel error from `Load` answers `500` with its message. Covered in Task 1.
- **An image with several tags, or with none** must show every tag and appear in the search results under each one. An untagged image shows `<none>` and matches no non-empty search. Covered in Tasks 2 and 5.

## File Structure

- Create `internal/images/images.go`: the seam (`Images`, `Image`, sentinel errors), `Service`, `New`, `RegisterRoutes`, the two handlers, and package-local `writeJSON`/`writeError`.
- Create `internal/images/faketest/faketest.go`: the scripted fake.
- Create `internal/httpapi/images_test.go`: the HTTP-seam tests. They live here so they can reuse `newTestServer` and `setupAndLogin`.
- Modify `internal/httpapi/httpapi.go`: `NewHandler` gains a final `imageStore images.Images` parameter. Nil means the routes are not registered.
- Modify `internal/httpapi/httpapi_test.go`, `legacy_identity_test.go`, `lifecycle_regression_test.go`: pass the new argument.
- Create `internal/dockerexec/images.go` and `images_test.go`: the Engine API adapter.
- Modify `main.go`: wire `dockerexec.NewImages(dockerexec.DefaultSocketPath)`.
- Modify `docker-compose.yml` and `README.md`: the Traefik read timeout.
- Modify `web/package.json` and `pnpm-lock.yaml`: add `swr` 2.5.1.
- Modify `web/src/data/types.ts` and `web/src/data/api.ts` (and `api.test.ts`): `HostImage`, `listImages`, `uploadImage`.
- Create `web/src/pages/images-view.ts` and `images-view.test.ts`: the pure helpers.
- Create `web/src/pages/ImagesPage.tsx`. Modify `web/src/app/paths.ts`, `paths.test.ts`, `routes.tsx` and `AppShell.tsx`.
- Modify `CONTEXT.md`, `docs/GUIDELINES.md` and `docs/adr/0001-v1-scope-and-stack.md`.

---

### Task 1: `internal/images` — seam, fake and HTTP handlers

**Files:**
- Create: `internal/images/images.go`, `internal/images/faketest/faketest.go`
- Create: `internal/httpapi/images_test.go`
- Modify: `internal/httpapi/httpapi.go:32-55`; every `httpapi.NewHandler(` call in `internal/httpapi/*_test.go`

**Interfaces:**
- Produces (package `images`):
  ```go
  var ErrLoadFailed = errors.New("images: load failed")   // wrap with the daemon's message: fmt.Errorf("%w: %s", ErrLoadFailed, msg)
  var ErrUnavailable = errors.New("images: docker is not reachable")
  type Image struct { ID string; Tags []string; Size int64; Created time.Time; InUse bool }
  type Images interface {
      List(ctx context.Context) ([]Image, error)
      Load(ctx context.Context, archive io.Reader, size int64) ([]string, error)
  }
  const UploadCap int64 = 10 << 30
  func New(store Images) *Service
  func (s *Service) RegisterRoutes(mux *http.ServeMux)
  ```
  The spec's `Load(ctx, archive)` gains `size` so the adapter can set the outgoing `ContentLength` instead of sending the body chunked.
- Produces (package `faketest`): `type Images struct { Listed []images.Image; ListErr error; Names []string; LoadErr error; Loaded [][]byte; mu sync.Mutex }`. `Load` reads the whole archive into `Loaded` and then returns `Names, LoadErr`. If reading fails, it returns that read error. `List` returns `Listed, ListErr`. Use getters (`LoadedArchives() [][]byte`) when the tests need them under the mutex.
- Wire format: `GET` returns `{"images":[{"id","tags","size","created","inUse"}]}`. `tags` is never null, and the list is sorted by `created`, newest first, with ties broken by `id`. `PUT` returns `{"images":["name",...]}`. Errors use `{"error":"..."}`.

- [ ] **Step 1: Write the failing tests** in `internal/httpapi/images_test.go`. Add a `images *imagesfake.Images` field to `testServer` and pass it to `NewHandler`. Each test calls `setupAndLogin` first, except the auth test.
  - `TestImagesListSortsNewestFirstWithTagsNeverNull`: the fake lists `{ID:"sha256:a", Created:t0}` and `{ID:"sha256:b", Tags:["web:1"], Created:t0+1h, InUse:true}`. Expect `200`, the order `b` then `a`, `a.tags` as JSON `[]` (assert on the raw body: `"tags":[]`), and `b.inUse == true`.
  - `TestImagesListUnavailableAnswers503`: `ListErr = images.ErrUnavailable`. Expect `503` with `error == "Docker is not reachable"`.
  - `TestImagesUploadRequiresContentLength`: `sendUpload(..., http.MethodPut, "/api/images", body, -1)`. Expect `411` and the exact copy, and that the fake was never called.
  - `TestImagesUploadOverCapAnswers413BeforeDocker`: declared length `images.UploadCap+1`. Expect `413` and `upload exceeds the 10 GiB cap`, with zero `Loaded`. At exactly `UploadCap` with a tiny real body, the request is not 413.
  - `TestImagesUploadStreamsBytesAndReturnsNames`: body `[]byte("archive-bytes")` with `Names = ["web:1","sha256:abc"]`. Expect `200` with `{"images":["web:1","sha256:abc"]}`, and `LoadedArchives()[0]` equal to the body.
  - `TestImagesUploadLoadFailedAnswers422WithDaemonMessage`: `LoadErr = fmt.Errorf("%w: %s", images.ErrLoadFailed, "invalid tar header")`. Expect `422` and `error == "invalid tar header"`. The `ErrLoadFailed` prefix is not shown.
  - `TestImagesUploadUnavailableAnswers503`.
  - `TestImagesUploadOtherErrorAnswers500` (Review Focus): `LoadErr = io.ErrUnexpectedEOF`. Expect `500` and a non-empty `error`.
  - `TestImagesRoutesRequireAuth`: without login, `GET` and `PUT /api/images` both answer `401`.
  - `TestImagesRoutesAbsentWithoutStore`: `httpapi.NewHandler(..., nil)` with a final nil. After login, `GET /api/images` is not a JSON `200` (it falls through to the web assets or a 404/405). Assert `Content-Type` is not `application/json`.

- [ ] **Step 2: Run them and watch them fail.**
  Run: `go test ./internal/httpapi/ -run Images`
  Expected: build failure (`images` package / `NewHandler` arity).

- [ ] **Step 3: Implement `internal/images/images.go` and `faketest`, and add the `NewHandler` parameter.**
  - `PUT` checks `r.ContentLength < 0` → 411, then `> UploadCap` → 413. It wraps `r.Body` in `http.MaxBytesReader(w, r.Body, r.ContentLength)` and calls `Load(r.Context(), body, r.ContentLength)`.
  - `errors.Is(err, ErrLoadFailed)` → 422, with the message after the `"images: load failed: "` prefix (use `strings.TrimPrefix` on `err.Error()`). `ErrUnavailable` → 503 with the fixed copy. Any other error → 500 with `err.Error()`. Log 500s with `log.Printf`, as the other packages do.
  - `RegisterRoutes` registers `GET /api/images` and `PUT /api/images`.
  - In `httpapi.go`, if `imageStore != nil`, call `images.New(imageStore).RegisterRoutes(mux)`.
  - Update every other `NewHandler(` call site in the tests to pass `nil`, or the fake in `newTestServer`.
  - Add a package doc comment in the style of `internal/terminal/terminal.go`'s: what the package owns, and the seam per ADR-0003.

- [ ] **Step 4: Run the tests.**
  Run: `go test ./internal/httpapi/ ./internal/images/...` then `go vet ./...`
  Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/images internal/httpapi
  git commit -m "feat: serve the host's images and load an uploaded archive"
  ```

### Task 2: `dockerexec.Images` — the Engine API adapter

**Files:**
- Create: `internal/dockerexec/images.go`, `internal/dockerexec/images_test.go`
- Modify: `main.go:88-92`

**Interfaces:**
- Consumes: `images.Images`, `images.Image`, `images.ErrLoadFailed`, `images.ErrUnavailable` (Task 1); `newSocketClient(socketPath string) *http.Client` (`containersource.go`).
- Produces: `func NewImages(socketPath string) *Images`, which satisfies `images.Images`.

- [ ] **Step 1: Write the failing tests** in `images_test.go`. Use the existing `newTerminalDaemon(t, handler) string` and `writeTerminalJSON` helpers from `terminal_test.go` (same package) to serve a fake daemon on a real unix socket.
  - `TestImagesListJoinsContainersForInUse`: `/images/json` answers two images. One has `RepoTags: ["web:1","web:latest"]`, `Size: 100` and `Created: 1700000000`. The other has `RepoTags: ["<none>:<none>"]`. `/containers/json` (assert the query `all=1`) answers one container whose `ImageID` is the first image's `Id`. Expect the first image to have both tags, `InUse` true, and `Created == time.Unix(1700000000,0)`. Expect the second to have `Tags` as an empty, non-nil slice and `InUse` false.
  - `TestImagesLoadPostsTheBodyAndParsesNames`: the handler asserts `POST /images/load`, query `quiet=1`, `Content-Type: application/x-tar`, `r.ContentLength == len(body)`, and a body equal to what was sent. It answers with the lines `{"stream":"Loaded image: web:1\n"}` and `{"stream":"Loaded image ID: sha256:abc\n"}`. Expect `["web:1","sha256:abc"]`.
  - `TestImagesLoadErrorAfterNamesFails` (Review Focus): the stream is `Loaded image: web:1`, then `{"errorDetail":{"message":"unexpected EOF"},"error":"unexpected EOF"}`. Expect `errors.Is(err, images.ErrLoadFailed)` and a message containing `unexpected EOF`. No names are returned.
  - `TestImagesLoadEmptyStreamFails`: the stream is a 200 with no lines. Expect `ErrLoadFailed` with `archive contained no images`.
  - `TestImagesLoadReadsLongLines` (Review Focus): one `errorDetail` line whose message is 100 KiB of `x`. Expect `ErrLoadFailed` and the full message.
  - `TestImagesLoadNon2xxFails`: the daemon answers `500 {"message":"boom"}`. Expect `ErrLoadFailed` with `boom`.
  - `TestImagesUnreachableSocketIsUnavailable`: point at a socket path that does not exist. Both `List` and `Load` return `errors.Is(err, images.ErrUnavailable)`.
  - `TestImagesLoadStopsWhenTheContextIsCancelled` (Review Focus): the handler blocks reading the body. The test feeds an `io.Pipe` reader that never closes, cancels the ctx after 50 ms, and asserts `Load` returns within 2 s with an error.

- [ ] **Step 2: Run them and watch them fail.**
  Run: `go test ./internal/dockerexec/ -run Images`
  Expected: build failure (`NewImages` undefined).

- [ ] **Step 3: Implement `internal/dockerexec/images.go`.**
  - Decode only the JSON fields you use: `Id`, `RepoTags`, `Size`, `Created` from images, and `ImageID` from containers.
  - Map a dial failure to `ErrUnavailable`. Detect it with `errors.As(err, *net.OpError)` on the `client.Do` error, but only when `ctx.Err() == nil`. A cancelled request returns the context error unchanged.
  - Decode the load stream with a `json.Decoder` loop until `io.EOF`. Names are the trimmed text after `Loaded image: ` or `Loaded image ID: ` in `stream`. A non-empty `error` or `errorDetail.message` returns immediately with `fmt.Errorf("%w: %s", images.ErrLoadFailed, msg)`.
  - Set `req.ContentLength = size`.
  - Write a short doc comment that ties the adapter to ADR-0006's socket path, in the style of `ContainerSource`'s.

- [ ] **Step 4: Wire it into `main.go`.** Pass `dockerexec.NewImages(dockerexec.DefaultSocketPath)` as `NewHandler`'s new last argument.

- [ ] **Step 5: Run the tests.**
  Run: `go test ./... && go vet ./...`
  Expected: PASS.

- [ ] **Step 6: Commit**
  ```bash
  git add internal/dockerexec/images.go internal/dockerexec/images_test.go main.go
  git commit -m "feat: list and load images through the docker engine api"
  ```

### Task 3: Install — Traefik read timeout and the README

**Files:**
- Modify: `docker-compose.yml` (traefik `command:`), `README.md`

- [ ] **Step 1: Add the Traefik flag.** Add `- --entrypoints.web.transport.respondingTimeouts.readTimeout=3600s` under the traefik `command:`. Above it, add a comment saying that Traefik v3 defaults to 60s for the whole request body, and that the Images upload needs up to 10 GiB, so the limit is one hour for every app on `web`.
- [ ] **Step 2: Document the limit.** In `README.md`'s install or usage section, add a short paragraph: image uploads through the domain must finish within one hour (10 GiB needs roughly 25 Mbit/s sustained). A slower link can upload through the panel's direct port (`RELIST_PORT`, default host port 8888), which bypasses Traefik.
- [ ] **Step 3: Verify the compose file still parses.**
  Run: `docker compose -f docker-compose.yml config --quiet`
  Expected: exit 0. Warnings about unset variables are fine.
- [ ] **Step 4: Commit**
  ```bash
  git add docker-compose.yml README.md
  git commit -m "feat: give traefik an hour to read an image upload"
  ```

### Task 4: Web data layer — `swr`, `HostImage`, `listImages` and `uploadImage`

**Files:**
- Modify: `web/package.json`, `web/pnpm-lock.yaml`, `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/data/api.test.ts`

**Interfaces:**
- Produces:
  ```ts
  // types.ts
  export type HostImage = { id: string; tags: string[]; size: number; created: string; inUse: boolean };
  // api.ts
  export const IMAGES_URL = '/api/images';
  export function listImages(): Promise<HostImage[]>;            // GET, unwraps { images }
  export function uploadImage(
    file: Blob,
    onProgress: (sent: number, total: number) => void,
    signal?: AbortSignal,
  ): Promise<string[]>;                                           // PUT, unwraps { images }
  ```
  `listImages` uses the existing `request<T>()`. `uploadImage` uses `XMLHttpRequest` (`xhr.upload.onprogress`). Non-2xx rejects with `ApiError(status, <body.error or "Request failed (status)">)`, the same message rule as `responseError`. `onerror` rejects with `ApiError(0, 'Upload failed — connection lost')`. Abort, whether from the signal or from an already-aborted signal, rejects with `ApiError(0, 'Upload cancelled')`.

- [ ] **Step 1: Add the dependency.**
  Run: `pnpm --dir web add swr@2.5.1 --save-exact`
  Expected: `"swr": "2.5.1"` in `dependencies`.
- [ ] **Step 2: Write the failing tests** in `api.test.ts`, in a new `describe('uploadImage')` block. Stub `XMLHttpRequest` with a minimal fake class via `vi.stubGlobal`, and unstub in `afterEach`. The fake records `method`, `url` and the sent body, and exposes `upload.onprogress`, `onload`, `onerror` and `abort()`.
  - `PUTs the file to /api/images and resolves with the loaded names`: `status 200` with `responseText '{"images":["web:1"]}'` resolves to `['web:1']`. Assert `method === 'PUT'`, `url === '/api/images'`, and that the body is the blob.
  - `reports progress`: firing `upload.onprogress({loaded: 5, total: 10, lengthComputable: true})` calls `onProgress(5, 10)`.
  - `rejects with the server's error message`: `status 422`, `responseText '{"error":"invalid tar header"}'` → `ApiError` with `status 422` and message `invalid tar header`.
  - `rejects a dropped connection`: `onerror()` → message `Upload failed — connection lost`.
  - `rejects when cancelled`: aborting the controller calls `xhr.abort()` and rejects with `Upload cancelled`.
  - `listImages unwraps the list`: stub `fetch` to return `{images:[...]}` and expect the array.
- [ ] **Step 3: Run them and watch them fail.**
  Run: `pnpm --dir web test -- api`
  Expected: FAIL (`uploadImage` not exported).
- [ ] **Step 4: Implement** `HostImage`, `IMAGES_URL`, `listImages` and `uploadImage`.
- [ ] **Step 5: Run the tests and the typecheck.**
  Run: `pnpm --dir web test && pnpm --dir web typecheck`
  Expected: PASS.
- [ ] **Step 6: Commit**
  ```bash
  git add web/package.json web/pnpm-lock.yaml web/src/data
  git commit -m "feat: list images and upload an archive from the web client"
  ```

### Task 5: Pure view helpers — `images-view.ts`

**Files:**
- Create: `web/src/pages/images-view.ts`, `web/src/pages/images-view.test.ts`

**Interfaces:**
- Consumes: `HostImage` (Task 4).
- Produces:
  ```ts
  export const IMAGE_UPLOAD_CAP = 10 * 2 ** 30;
  export const IMAGE_ARCHIVE_EXTENSIONS = ['.tar', '.tar.gz', '.tgz', '.tar.zst', '.tar.xz', '.tar.bz2'] as const;
  export function shortImageId(id: string): string;                  // 'sha256:0123456789abcdef…' → '0123456789ab'
  export function filterImages(images: HostImage[], query: string): HostImage[];
  export function checkImageArchive(file: { name: string; size: number }): string | undefined; // error copy, or undefined if acceptable
  ```

- [ ] **Step 1: Write the failing tests.**
  - `shortImageId`: `'sha256:0123456789abcdef'` → `'0123456789ab'`. An id without the prefix is still cut to 12 characters.
  - `filterImages`: the query is trimmed and case-insensitive (`' WEB '` matches `web:1`). An image matches if any of its tags contains the query. An empty query returns the list unchanged, in the same order. An untagged image matches no non-empty query (Review Focus). An image tagged `['web:1','api:2']` matches both `web` and `api`.
  - `checkImageArchive`: `app.tar`, `app.TAR.GZ` (case-insensitive) and `app.tgz` → `undefined`. `app.zip` → `Choose a .tar, .tar.gz, .tgz, .tar.zst, .tar.xz or .tar.bz2 archive`. A size of `IMAGE_UPLOAD_CAP + 1` → `upload exceeds the 10 GiB cap`. A size exactly at the cap → `undefined`.
- [ ] **Step 2: Run them and watch them fail.**
  Run: `pnpm --dir web test -- images-view`
  Expected: FAIL (module not found).
- [ ] **Step 3: Implement the three functions.**
- [ ] **Step 4: Run them.**
  Run: `pnpm --dir web test -- images-view`
  Expected: PASS.
- [ ] **Step 5: Commit**
  ```bash
  git add web/src/pages/images-view.ts web/src/pages/images-view.test.ts
  git commit -m "feat: filter images and check an archive before upload"
  ```

### Task 6: The Images page, route, sider entry and docs

**Files:**
- Create: `web/src/pages/ImagesPage.tsx`
- Modify: `web/src/app/paths.ts`, `web/src/app/paths.test.ts`, `web/src/app/routes.tsx`, `web/src/app/AppShell.tsx` (menu items around :96, `onMenuClick` around :120, breadcrumb around :133, and the selected-key logic that colours the sider)
- Modify: `docs/GUIDELINES.md` (new §4.11 after Account), `CONTEXT.md`, `docs/adr/0001-v1-scope-and-stack.md`
- Regenerate: the embedded frontend (`task build-web`)

**Interfaces:**
- Consumes: `IMAGES_URL`, `listImages`, `uploadImage`, `ApiError` (Task 4); `shortImageId`, `filterImages`, `checkImageArchive` (Task 5); `RelativeTime` (`components/primitives.tsx`); `formatBytes` (`components/MetricChart.tsx`).
- Produces: `paths.images === '/images'`; `routePaths` entry `{ path: 'images' }`; `export function ImagesPage()`.

- [ ] **Step 1: Read the docs.** Read `https://ant.design/llms.txt`, then the pages for Upload (Dragger), Progress, Table, Alert, Typography (`copyable`), Input and Tag. Check every prop you use against antd 6.5.1. Read `docs/GUIDELINES.md` §6–§9 for layout, empty, loading and error states.
- [ ] **Step 2: Write the failing route test** in `paths.test.ts`: `resolvePath('/images')?.pattern === 'images'`, and `paths.images === '/images'`.
  Run: `pnpm --dir web test -- paths`
  Expected: FAIL.
- [ ] **Step 3: Add the route.** Add `paths.images` and the `routePaths` entry, map `'images': <ImagesPage />` in `routes.tsx`, add a sider item `{ key: 'images', icon: <ContainerOutlined />, label: 'Images' }` after Monitoring, add navigation and the breadcrumb (`Overview › Images`), and make the sider select `images` on `/images`.
- [ ] **Step 4: Build `ImagesPage.tsx` to the spec's §User interface.**
  - `const { data, error, isLoading, mutate } = useSWR(IMAGES_URL, listImages)`.
  - Upload state is a discriminated union in the page: `idle | uploading {name, sent, total} | loading {name} | loaded {names} | failed {message}`. It switches from `uploading` to `loading` when `sent === total`.
  - Cancel is an `AbortController` held in a ref, shown only in `uploading`.
  - `beforeunload` is registered only while `uploading` or `loading`, and removed on cleanup.
  - Use `Upload.Dragger` with `beforeUpload` returning `false` (no antd auto-upload), `maxCount={1}` and `showUploadList={false}`, and disable it while busy. The client check comes from `checkImageArchive`. It shows the copy inline and sends nothing.
  - On success, call `mutate()` and keep the loaded names to highlight their rows. A row is highlighted when one of its tags, or its id, is in the names (`rowClassName`, with the colour from the theme token, never a hard-coded hex).
  - Table columns follow the spec: Image (every tag `Text copyable`, untagged `<none>` in `type="secondary"`), ID (`shortImageId`, code font), Size (`formatBytes`), Created (`RelativeTime`) and In use (`Tag`). Use `rowKey="id"`. The search `Input` feeds `filterImages`.
- [ ] **Step 5: Update the docs.**
  - `docs/GUIDELINES.md` §4.11 Images (`/images`): a short paragraph in the style of §4.10 covering the upload states, the list columns, and the one-hour Traefik window.
  - `CONTEXT.md` gets **Image** after **Container**: "A Docker image present on the host, listed and loaded from the Images page. A Compose references one by name in its file; the panel never builds or pulls one itself." _Avoid_: Build, Artifact.
  - ADR-0001 gets a "Widened since" note, worded like ADR-0002's: the panel now lists the host's images and loads `docker save` archives (`.scratch/image-upload/spec.md`), while git builds and registries remain out of scope.
- [ ] **Step 6: Verify.**
  Run: `pnpm --dir web test && pnpm --dir web typecheck && task build-web && go test ./...`
  Expected: all PASS, and the embedded assets are regenerated.
- [ ] **Step 7: Commit (two commits, following the repo's pattern)**
  ```bash
  git add web/src CONTEXT.md docs
  git commit -m "feat: add an images page to load and list host images"
  git add internal/webassets
  git commit -m "chore: rebuild the embedded frontend with the images page"
  ```

### Task 7: Manual verification against a real daemon

No code. Record the results under `## Manual verification` at the bottom of `spec.md`.

- [ ] **Step 1:** Start the panel (`task` / the `run` skill) and sign in.
- [ ] **Step 2:** `docker pull alpine:3.20 && docker tag alpine:3.20 relist-test:1 && docker save relist-test:1 -o /tmp/relist-test.tar && docker rmi relist-test:1`. Upload it on `/images`. Expect `relist-test:1` in the success alert, highlighted in the list.
- [ ] **Step 3:** Create a Compose with `image: relist-test:1` and `command: sleep 3600`, deploy it, and confirm the Container runs and the list shows **In use**.
- [ ] **Step 4:** Repeat Step 2 with `.tgz` (`gzip -c`), `.tar.zst` (`zstd`), `.tar.xz` and `.tar.bz2`. Remove from `IMAGE_ARCHIVE_EXTENSIONS` (and from the spec) any extension the host's daemon refuses, and commit that as `fix: accept only archives the daemon loads`.
- [ ] **Step 5:** Upload a tar of a random directory (`tar cf /tmp/not-image.tar web/src`). Expect a red alert with the daemon's message.
- [ ] **Step 6:** Start a large upload (more than 500 MB), cancel it mid-way, and expect `Upload cancelled`. Confirm `docker images` shows nothing new.
- [ ] **Step 7:** Commit the notes: `git commit -m "docs: record the image upload manual run"`.
