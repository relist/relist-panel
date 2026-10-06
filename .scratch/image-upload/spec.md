# Spec — Image upload (load a `docker save` tar onto the host)

## Problem

The operator builds an image on their own machine and wants to deploy it through the panel without
running a registry. Today that means leaving the panel: copy the tar to the host with `scp`, SSH in,
and run `docker load` by hand. The panel has no notion of images at all — ADR-0001 says users bring
pre-built images in their Compose file, and nothing in `internal/` lists or loads one.

Current behaviour, verified in code: deploy runs `docker compose up -d --build --remove-orphans`
(`internal/dockerexec/dockerexec.go`) with no `--pull` flag, so Compose's default pull policy
(`missing`) applies. An image already present on the host under the name a Compose file references is
used as-is and never pulled. Deploy therefore needs no change: once an image is loaded, `image:
myapp:1.2` in a Compose file deploys it.

## Goal and success criteria

- A top-level **Images** page lists every image on the host and accepts one image tar upload at a time.
- The operator runs `docker save myapp:1.2 -o myapp.tar` (optionally compressed) on their machine,
  drops the file on the page, sees upload progress, then sees `myapp:1.2` named as loaded, with a
  copy button.
- A Compose file that references `image: myapp:1.2` then deploys with no registry involved.
- Uploads up to 10 GiB work without staging the file on the panel's disk or buffering it in memory.
- A failed load (corrupt tar, not an image archive, daemon unreachable, over the cap) shows the
  reason on the page.

## Out of scope

- Deleting or pruning images; tagging or retagging.
- Pulling from or pushing to a registry.
- Resumable uploads; several files in one upload.
- Remembering upload history — the panel stores nothing about images in SQLite.
- Linking an image to the Composes that reference it.

## Decisions

### Transport: Engine API, streamed

`PUT /api/images` streams the request body straight into the Engine API's `POST /images/load?quiet=1`
over `/var/run/docker.sock`, reusing `newSocketClient` (`internal/dockerexec/containersource.go`,
ADR-0006). That client has no timeout, which a multi-minute load needs.

Rejected: piping the body into a `docker load` CLI process. That costs one CLI process per upload on a
256 MB panel and yields plain text to parse instead of a JSON stream. Also rejected: staging the
upload to a file under the data dir first. That would make resume possible, but it doubles disk use
and needs temp-file cleanup, and the use case does not need resume.

Docker itself accepts gzip, bzip2, xz and zstd compressed archives, so the panel does nothing to
support them.

### Package and seam

New domain package `internal/images` owns the HTTP surface. Its seam is a consumer-side interface,
as `terminal.Exec` and `volumes.Runner` are:

```go
type Images interface {
    List(ctx context.Context) ([]Image, error)
    Load(ctx context.Context, archive io.Reader) ([]string, error) // names loaded
}

type Image struct {
    ID      string    // "sha256:…", full
    Tags    []string  // RepoTags; empty for an untagged image
    Size    int64     // bytes
    Created time.Time
    InUse   bool      // some container, running or stopped, uses it
}
```

`dockerexec.Images` implements it; `internal/images/faketest` fakes it.

- **List** is `GET /images/json` plus `GET /containers/json?all=1`. `InUse` is true when any
  container's `ImageID` equals the image's `Id`. `/images/json`'s own `Containers` field is `-1`
  unless the daemon is asked to compute shared size, so it is not used. `RepoTags` of `["<none>:<none>"]`
  (older daemons) is normalised to empty.
- **Load** posts the body to `POST /images/load?quiet=1` with `Content-Type: application/x-tar`. The
  daemon answers `200` even when the load fails, and reports the failure inside its JSON-lines stream,
  so Load reads the stream line by line:
  - `{"stream":"Loaded image: myapp:1.2\n"}` → name `myapp:1.2`.
  - `{"stream":"Loaded image ID: sha256:…\n"}` (an untagged image) → name `sha256:…`.
  - Any line with `errorDetail` / `error` → `ErrLoadFailed` wrapping the daemon's message.
  - Stream ends with no names and no error → `ErrLoadFailed` ("archive contained no images").
  - Dial failure → `ErrUnavailable`.

### Routes and auth

`internal/images` registers both routes behind the existing auth middleware. They live under
`/api/images`, not `/api/projects/…`, so the per-project coordinator (`internal/httpapi/coordination.go`)
does not hold them. A load does not touch any Compose. The upload is `PUT`, never `GET`, so the
SameSite=Lax session cookie keeps it CSRF-safe, as the volume writes are.

`NewHandler` gains an `images.Images` parameter. Passing nil leaves the routes unregistered, the same
convention the terminal and metrics routes follow.

## HTTP protocol

### `GET /api/images`

`200` with:

```json
{ "images": [ { "id": "sha256:…", "tags": ["myapp:1.2"], "size": 123456789,
                "created": "2026-10-06T09:00:00Z", "inUse": false } ] }
```

Sorted by `created`, newest first. `tags` is `[]`, never null.

### `PUT /api/images`

Body: the archive bytes, sent raw (no multipart form).

| Condition | Answer |
|---|---|
| No `Content-Length` (chunked) | `411` "upload requires a declared length (Content-Length)" |
| `Content-Length` > 10 GiB (`10 << 30`) | `413` "upload exceeds the 10 GiB cap" — checked before Docker is touched |
| Success | `200 {"images": ["myapp:1.2"]}` |
| `ErrLoadFailed` | `422 {"error": "<daemon message>"}` |
| `ErrUnavailable` | `503 {"error": "Docker is not reachable"}` |

The body is also wrapped in `http.MaxBytesReader` at the declared length, so a client sending more than
it declared is cut off. The cap is hard-coded, with no env knob, matching the volume upload cap.

If the client disconnects, the request context is cancelled, the socket request is aborted, and the
daemon discards the partial load. The panel holds no state to clean up.

Error bodies use the same `{"error": "…"}` shape as the other packages, through a package-local
`writeError`/`writeJSON` pair like theirs.

## User interface

Before writing any UI code, read Ant Design's `llms.txt` and check every component used against
`antd` 6.5.1, per `AGENTS.md`. Follow `docs/GUIDELINES.md`.

### Route and navigation

- Path `/images`, added to `web/src/app/paths.ts` and `routes.tsx`.
- Sider item **Images** with `ContainerOutlined`, after Monitoring.
- Breadcrumb: Overview › Images.

### Files

- `web/src/pages/ImagesPage.tsx` holds the page.
- `web/src/pages/images-view.ts` holds the pure helpers: sorting, the search filter, display names
  (untagged → `<none>`), short ID, the accepted-extension check, and the size check. Tests go in
  `images-view.test.ts`.
- `web/src/data/api.ts` gets `listImages()` and `uploadImage(file, onProgress, signal)`. The upload
  uses `XMLHttpRequest`, because `fetch` cannot report upload progress. `signal` aborts the XHR.
- `web/src/data/types.ts` gets the `HostImage` type.

Data loading follows the existing page-local pattern: `useEffect` and state in the page, as in
`VolumeExplorerPage`. There is no global store entry.

### Upload area

- One-line explainer: "Load an image built with `docker save` onto this host, then reference it by
  name in a Compose file."
- `Upload.Dragger`, single file. It accepts `.tar`, `.tar.gz`, `.tgz`, `.tar.zst`, `.tar.xz` and
  `.tar.bz2`. The client refuses a file over 10 GiB or with another extension before sending. The
  server's checks remain authoritative.
- States:
  1. **Idle**: the dragger.
  2. **Uploading**: file name, `Progress` percent, **Cancel** button (aborts the XHR).
  3. **Loading**: all bytes sent, waiting for the daemon. Spinner and "Loading into Docker…". There is
     no cancel here: the bytes are sent, and aborting now gains nothing.
  4. **Loaded**: success `Alert` listing each loaded name as `Typography.Text copyable`. The dragger
     is available again.
  5. **Failed**: error `Alert` with the server's message (or "Upload cancelled"). The dragger is
     available again.
- While uploading or loading, the dragger is disabled and a `beforeunload` handler warns before
  leaving the page.

### Image list

- `Table` below the upload area. Columns:
  - **Image**: every tag, each copyable; untagged shows `<none>` in secondary text.
  - **ID**: the first 12 hex characters after `sha256:`.
  - **Size**: human-readable.
  - **Created**: relative time (`dayjs`), with the absolute time in a tooltip.
  - **In use**: a `Tag` when true.
- Newest first. A search `Input` filters by tag substring on the client.
- Loading, empty and error states follow `docs/GUIDELINES.md`.
- After a successful load, the list refetches and the rows for the loaded names are highlighted until
  the next upload.

## Error handling

| Situation | What the operator sees |
|---|---|
| Wrong extension / over 10 GiB (client check) | Inline error, nothing sent |
| Over cap (server) | Failed: "upload exceeds the 10 GiB cap" |
| Not an image archive / corrupt tar | Failed: the daemon's message |
| Docker socket unreachable | Failed: "Docker is not reachable"; the list shows the same as its error state |
| Network drops mid-upload | Failed: "Upload failed — connection lost" |
| Operator cancels | Failed: "Upload cancelled" |

## Testing

### Go: `internal/images` (external package `images_test`, black-box HTTP seam per ADR-0003)

- List: fake images come back sorted newest first, with `tags` as `[]` never null.
- Upload: `411` without a length; `413` over the cap, with the fake never called; `200` with the
  fake's names; `422` and `503` mapped from the two sentinel errors; the fake receives exactly the
  bytes sent.
- Unauthenticated `GET` and `PUT` are refused.
- With nil `Images`, the routes are not registered.

### Go: `internal/dockerexec`

Tests against an `httptest` server on a unix socket, as the existing adapter tests are:

- Load parses tagged and untagged `Loaded image` lines and maps an `errorDetail` line and an empty
  stream to `ErrLoadFailed`.
- List joins images with containers for `InUse` and normalises `<none>:<none>`.

### Vitest

- `images-view.test.ts`: sort, filter, display name, short ID, and the extension and size checks.
- `api.test.ts`: `uploadImage` reports progress, resolves with names, rejects with the server's
  error, and rejects on abort.

### Manual

`docker save` a small image, `docker rmi` it, upload it on the Images page, confirm it appears in the
list, deploy a Compose that references it, and confirm the container runs. Repeat with a `.tar.gz`
and with a non-image tar.

## Documentation

- `CONTEXT.md`: add **Image**: a Docker image present on the host, listed and loaded from the Images
  page. Avoid: Build, Artifact.
- `docs/GUIDELINES.md`: add a §4 entry for the Images screen.
- No new ADR. This extends ADR-0006's Engine-API-over-socket path, and the rejected options are
  recorded here.
