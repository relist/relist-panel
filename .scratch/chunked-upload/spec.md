# Spec — Chunked uploads (Volume files up to 10 GiB through Cloudflare)

Status: needs review

## Problem

Behind a Cloudflare proxy (orange cloud), large uploads through the panel fail. Each request body is
limited by the zone's plan, and a request whose origin does not answer in time ends in a `524`.
Cloudflare's documentation, read 2026-11-20:

| Limit | Value | Adjustable |
|---|---|---|
| Request body, Free and Pro | 100 MB | no |
| Request body, Business | 200 MB | no |
| Request body, Enterprise | up to 5 GB (more through the account team) | yes |
| Proxy Read Timeout (origin must answer) | 125 s | Enterprise only, up to 6,000 s |
| Proxy Write Timeout | 30 s | no |

Sources: Cloudflare docs "Error 413", "Error 524", "Connection limits".

Every panel endpoint that takes a file in one request is affected:

| Endpoint | Panel cap today | Through a Free/Pro zone |
|---|---|---|
| Volume upload, `PUT …/volumes/content` | 1 GiB (`internal/volumeapi/content.go`) | fails above ~100 MB |
| Volume restore, `PUT …/volumes/restore` | 1 GiB compressed | fails above ~100 MB |
| Images upload, `PUT /api/images` | 10 GiB | fails above ~100 MB |
| Source zip, `POST …/source/upload` | 500 MB | fails above ~100 MB |

Raising a cap does not help, and no plan carries a 10 GiB body. Routing uploads through a separate
DNS-only hostname would work, but it exposes the origin IP, which is what Cloudflare is there to hide.

## Goal and success criteria

- One upload mechanism, `internal/chunkupload`, that any endpoint taking a large file can use. A
  client sends the file as a sequence of small requests (chunks), so **no request body exceeds
  64 MiB** and no request takes longer than a chunk's transfer.
- This spec delivers it for **Volume upload** only, with a **10 GiB** cap. Volume restore, Images
  and Source zip move onto it in later specs (Out of scope).
- A 10 GiB file uploads into a Volume through a Free-plan Cloudflare zone.
- An upload interrupted by a network drop, a closed tab, a reboot of the user's machine or a panel
  restart resumes from the last stored chunk once the user picks the same file again.
- Writing the finished file into the Volume happens in the background. The request that starts it
  answers at once, so it never meets the 125 s timeout.
- The direct `PUT …/volumes/content` stays as it is (1 GiB): the editor's save and curl keep using it.

## Out of scope

- Volume restore, Images upload and Source zip on the new mechanism. Each gets its own spec once
  this one ships; the package is shaped for them (see Package and seam).
- Several files at once, folders, drag-and-drop of many files.
- Parallel chunks. The user's uplink is the bottleneck; sequential appends keep the server simple.
- Per-chunk or whole-file checksums (see Decisions, Integrity).
- An env knob for the cap, the chunk size or the expiry.
- The tus protocol and its clients (see Decisions, Protocol).

## Decisions

### Protocol: hand-rolled, tus-shaped, sequential

The protocol borrows tus's offset model (`Upload-Offset`, a status read for the current offset, a `409`
that carries the real offset) without the rest of tus 1.0. No new Go or web dependency.

Rejected:
- **tus 1.0 through tusd and tus-js-client.** Two new dependencies and protocol surface the panel
  does not use (extensions, metadata encoding, its own storage and locking). tusd's handler
  also owns its routes, which does not fit `authz.Routes`' per-pattern classification.
- **S3-style parallel parts.** Needs an assembly step and per-part state, for throughput the user's
  uplink does not have.

### Staging on the panel's disk

Chunks append to one staged file under the data directory, `<data>/.uploads/<id>`. Once every byte
has arrived, the staged file is streamed into the existing consumer (the volume helper's `write`)
exactly as a request body is today.

This reverses the no-staging decision of `.scratch/image-upload/spec.md` for the chunked path, and
is recorded as ADR-0053. Staging is what makes resume across a panel restart possible. It costs disk
space equal to the file while the upload is open; see Disk space.

ADR-0010 still holds: the consume step is one `docker run --rm` of the helper, and so is the new
`free` check at start.

Rejected: **feeding chunks into a consumer process held open across requests.** No staging, but
resume dies with the panel or the process, chunks must arrive strictly in order into a live pipe,
and an abandoned upload leaves a process to reap.

### Integrity

No checksums. TLS protects bytes in transit. `Upload-Offset` rejects a chunk at the wrong place.
`complete` refuses unless the staged length equals the declared size. A per-chunk SHA-256 in the
browser needs `crypto.subtle`, which exists only in a secure context, so it would break the panel on
`http://<ip>:<port>`.

Accepted risk: resuming with a different file that has the same name, size and `lastModified` is not
detected.

### Locking and coordination

The coordination middleware (`internal/httpapi/coordination.go`) treats any non-GET under
`…/volumes` as a Volume write: it takes the Project's mutation gate and its exclusive volume lock
for the length of the request. That is wrong for chunks. A chunk touches only the staging file, so
holding the gate would stall the Deployment queue for every chunk of a 10 GiB upload.

- `volumeClass` gets a new class, `volumesStagingOnly`, for every route under `…/volumes/uploads`.
  Those requests take only the host maintenance guard: no mutation gate, no volume lock. The
  existing classes keep their meaning.
- The consume job takes the host guard, the mutation gate and the exclusive volume lock itself,
  through a narrow interface the coordinator implements, in the usual lock order. It holds them
  only while the helper runs. As with today's direct upload, a deploy of that Project waits while a
  large file is written.

### Audit

`authz.Actions` records every 2xx change. A chunk is a change, but recording ~320 of them per
10 GiB file would flood the Audit log. `authz` gains an explicit `Unrecorded` set, with a reason per
pattern, holding only the chunk `PATCH`. `TestEveryChangeHasAnAction` skips patterns in it, and a
new check fails when a pattern in `Unrecorded` also has an action.

| Pattern | Action |
|---|---|
| `POST …/volumes/uploads` | `volume.upload_start` |
| `PATCH …/volumes/uploads/{uploadID}` | unrecorded |
| `POST …/volumes/uploads/{uploadID}/complete` | `volume.upload` |
| `DELETE …/volumes/uploads/{uploadID}` | `volume.upload_cancel` |

`volume.upload` records that the write was accepted and started (`202`). A later failure of the job
shows in the upload's state, not in the Audit log.

All four routes are `LevelEditor`, like the other Volume writes.

## Package and seam

New package `internal/chunkupload` owns sessions, staging, offsets, expiry and the background job.
It knows nothing about Volumes. A feature package registers a **purpose**:

```go
// Purpose is what a feature plugs into chunkupload.
type Purpose struct {
    Name string // "volume", later "restore", "image", "source"
    Cap  int64  // largest declared size accepted, 10 << 30 for "volume"
    // Consume reads the complete staged file. It runs in the background,
    // after the HTTP request that started it has answered. It must be safe
    // to call again after a failure or a panel restart.
    Consume func(ctx context.Context, s Session, staged io.Reader) error
}
```

`chunkupload.Service` exposes the operations the feature's handlers call (`Create`, `Append`,
`Offset`, `Complete`, `Status`, `Cancel`, `ListOpen`) plus `Sweep` for expiry. The feature package
owns its routes under its own path, so authz stays per-pattern and the URL names the target.

`internal/volumeapi` registers the `"volume"` purpose and the routes below. Its `Consume` acquires
the Project's locks (see Locking), runs the helper's `write --expect <size>` with the staged file as
stdin, and maps the result the way `writeRunFailure` does today.

## Data model

New migration `00058_uploads.sql` (never edit an existing one):

```sql
CREATE TABLE uploads (
    id            TEXT    PRIMARY KEY,           -- 32 hex chars, crypto/rand
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose       TEXT    NOT NULL,              -- "volume"
    compose_id    INTEGER REFERENCES composes(id) ON DELETE CASCADE,  -- NULL for non-Compose purposes
    target        TEXT    NOT NULL,              -- purpose-owned JSON
    name          TEXT    NOT NULL,              -- the file's name, as picked
    size          INTEGER NOT NULL,              -- declared bytes
    last_modified INTEGER NOT NULL,              -- the browser File's lastModified, ms
    received      INTEGER NOT NULL DEFAULT 0,    -- bytes stored and fsynced (OFFSET is an SQL keyword)
    state         TEXT    NOT NULL,              -- uploading | consuming | done | failed
    error_kind    TEXT    NOT NULL DEFAULT '',   -- refused | exists | failed | interrupted
    error         TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL               -- last chunk or state change; drives expiry
);
CREATE INDEX uploads_user_compose ON uploads(user_id, compose_id, state);
```

For `"volume"`, `target` is `{"source": "...", "path": "...", "user": "uid:gid"}`: the Volume, the
file's full path inside it, and the confirmed operation user the upload was started with.

A `CASCADE` delete (User or Compose gone) leaves the staged file behind; `Sweep` removes staged files
that have no row.

## HTTP protocol

Base: `B = /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/uploads`.
Every route answers `404` for an upload ID that does not exist **or belongs to another User**.
Errors use the existing `{"error": "…"}` body.

### `POST B` — start

Body (JSON, ≤ 4 KiB): `{"source", "path", "user", "name", "size", "lastModified"}`.
`path` is the full target path, `user` the confirmed operation user query value the page already
sends for writes.

The handler runs the same checks the direct write runs today (availability, Volume resolution,
Compose policy, path rule), then:

| Condition | Answer |
|---|---|
| `size` < 1 | `400` |
| `size` > 10 GiB | `413` "upload exceeds the 10 GiB cap" |
| This User already has 3 open uploads (any Compose) | `429` "finish or discard an open upload first" |
| Staging disk or the Volume lacks the space (see Disk space) | `507` naming need and free |
| OK | `201 {"id", "chunkSize": 33554432, "offset": 0}` |

### `GET B` — this User's open uploads for this Compose

`200 {"uploads": [{"id", "source", "path", "name", "size", "lastModified", "offset", "state",
"errorKind", "error", "updatedAt"}]}`, every state except `done`. The page matches a picked file by
`path` + `name` + `size` + `lastModified`.

### `PATCH B/{uploadID}` — append a chunk

Headers: `Upload-Offset: <n>` and `Content-Length`. Body: raw bytes, read through
`http.MaxBytesReader` at 64 MiB.

| Condition | Answer |
|---|---|
| state is not `uploading` | `409` "upload is <state>" |
| `Upload-Offset` ≠ stored offset | `409` with `Upload-Offset: <stored>` |
| offset + length > size | `400` |
| body over 64 MiB | `413` |
| body ends short of `Content-Length` | staged file truncated back to the stored offset, `400` |
| OK | `204` with `Upload-Offset: <new>` |

Append order: write at the stored offset, `fsync`, then update `uploads.received`. One `PATCH` per
upload at a time: a second concurrent one gets `409` (in-process mutex per ID).

### `POST B/{uploadID}/complete` — write it into the Volume

| Condition | Answer |
|---|---|
| offset ≠ size | `409` "upload is incomplete" with `Upload-Offset` |
| state is `consuming` or `done` | `409` |
| state is `uploading` or `failed` | re-run the start checks (resolution, policy, path), set `consuming`, start the job, then wait up to 10 s for it: `200` with the status object if it finished (`done` or `failed`), otherwise `202 {"state": "consuming"}` |

Retrying a `failed` upload is this same call; nothing is re-sent.

### `GET B/{uploadID}` — status

`200` with the same object as one `GET B` entry, including `done`. It is also how the client learns
the stored offset when it resumes. The page polls it every 2 s while `consuming`.

### `DELETE B/{uploadID}` — cancel

Removes the row and the staged file. `409` while `consuming`. `204` otherwise.

## Background job

- One goroutine per `complete`, with a context that is cancelled only on panel shutdown.
- It takes the Project's locks (Locking), opens the staged file and calls `Purpose.Consume`.
- Result:
  - success → `done`; the staged file is deleted at once; the row stays until `Sweep`.
  - `volumes.RefusalError` → `failed`, `error_kind = refused` (today's `507`).
  - `volumes.ExistsError` → `failed`, `error_kind = exists` (today's `409`; not expected for write).
  - anything else → `failed`, `error_kind = failed` (today's `502`).
- The staged file is kept on failure, so the user can retry without uploading again.

## Restart and expiry

- **On start**, before serving:
  - every `consuming` row becomes `failed`, `error_kind = interrupted`, "the panel restarted while
    writing this file; retry". Re-running the helper's write is safe: it writes a temp file and
    renames it over the target only once the declared size has arrived.
  - every `uploading` row's staged file is truncated to `uploads.received` (a crash mid-chunk can
    leave bytes past it).
- **`Sweep`** runs at start (after the two steps above) and hourly. It removes:
  - rows whose `updated_at` is over 24 hours old (1 hour when `received = 0`), in any state
    except `consuming`, with their staged files;
  - `uploading` or `failed` rows whose staged file is missing (see Server migration);
  - staged files with no row.

## Disk space

The staging directory lives on the data directory's disk, together with SQLite. Filling it could stop
the panel, so `POST B` refuses (`507`) unless:

```
statfs(.uploads).free − Σ (size − received) over uploading rows − size ≥ 2 GiB
```

The sum is the space promised to other open uploads but not yet written; bytes already written are
in `free` already. The margin is a constant.

**The Volume.** At consume the staged file and the helper's `.relist-write-*` temp exist together,
and an existing target stays until the rename. On a one-disk host, where `<data>` and
`/var/lib/docker/volumes` share a filesystem, a file needs about twice its size free. Checking only
at consume would refuse it after the user waited for every byte. So `POST B` also runs the helper's
new `free` op (one `docker run`, prints `{"bytes": <Bavail × Bsize>}` from `volfs.FreeBytes`) and
refuses with `507` unless:

```
volumeFree ≥ 2 × size
```

The check assumes the staging disk and the Volume share a filesystem. On a host where they do not,
it refuses some files that would fit; that is accepted for now. The helper's `write --expect` at
consume stays as the backstop, since space can run out while the upload is open.

**Reservations.** A User may hold at most 3 open uploads (`uploading` or `failed`), so an editor
cannot reserve the staging disk with sessions that never send a byte. An upload with
`received = 0` expires after 1 hour instead of 24.

## Server migration

`.uploads` joins `skipInDataDir` in `internal/migration/manifest.go`, next to `.scan-work`. Open
uploads are not carried to the new host; their rows go with `panel.db`, so after a migration they
would point at staged files that are missing. The `Sweep` at start removes those rows, so the new
host shows no unfinished uploads and the user starts them again.

## User interface

Before writing any UI code, read Ant Design's `llms.txt` and check each component used against the
installed `antd`, per `AGENTS.md`. Follow `docs/GUIDELINES.md`.

### Files

- `web/src/data/chunked-upload.ts`: the client driver. Pure, with `fetch` injected:
  `runUpload({file, start | resume, onProgress, signal, fetch})`. It sends chunks of the server's
  `chunkSize`, retries a failed chunk with backoff 1, 2, 4, 8, 16 s, follows a `409`'s
  `Upload-Offset`, waits for the browser's `online` event while offline, calls `complete`, and
  polls status until `done` or `failed` only when `complete` answered `202`. A small file is
  therefore start, one chunk, and complete, with no poll.
- `web/src/data/api.ts`: the six calls (start, list, status, append, complete, cancel), rejecting with the existing `ApiError`.
- `web/src/pages/VolumeExplorerPage.tsx` and `volume-explorer-view.ts`: the states below.

### Upload button

The toolbar's **Upload** now goes through the chunked path for every file. The client-side cap
check becomes 10 GiB. The tooltip says files up to 10 GiB, uploaded in pieces that resume.

### States

1. **Pick.** If an open upload matches the picked file (`path` + `name` + `size` + `lastModified`):
   a `Modal` "`x.bin` was uploaded to 63%. **Resume** or **Start over**". Start over deletes the
   old upload first.
2. **Uploading.** A `Card` under the toolbar: file name, `Progress` with "4.2 GiB of 10 GiB",
   speed and time left, **Cancel** (deletes the upload). While offline: "Waiting for the network…".
   After the retries run out: "Upload paused" with **Resume**.
3. **Writing.** All bytes sent: indeterminate progress, "Writing into the Volume…". No cancel.
4. **Done.** `message.success`, the card closes, the listing reloads.
5. **Failed.** `Alert` with the API's message and **Retry** (calls `complete` again; nothing is
   re-sent) and **Discard**.

While states 2–3 last, `beforeunload` warns before leaving, and the Upload button is disabled.

### Unfinished uploads

On load, the Volume Explorer lists this User's open uploads for the Compose in an `Alert` under the
toolbar: name, target path, percent, age, and **Discard**. To resume, the user picks the same file
again; a browser cannot reopen a file by itself after a reload, so the Alert says so.

## Testing

### Go

- `internal/chunkupload`: offset mismatch `409` with the stored offset; chunk over 64 MiB `413`;
  short body truncates back; concurrent `PATCH` `409`; `complete` before the last byte `409`;
  disk reservation and the `507`; the per-User cap of 3 and the `429`; `complete` answering `200`
  inline for a fast job and `202` for a slow one; restart turns `consuming` into `failed/interrupted` and truncates
  `uploading` files; `Sweep` removes expired rows, orphan files and rows whose file is gone; another
  User's ID answers `404`; a job's failure kinds.
- The helper's `free` op: prints `FreeBytes` as JSON; its argv mounts the Volume read-only.
- `internal/volumeapi`: `POST B` refuses with `507` when the fake runner's `free` is under
  2 × size; with a fake runner, consume gets the staged bytes with `DeclaredBytes =
  size` and the stored operation user; refusal becomes `failed/refused`; `complete` re-checks the
  Compose policy; the job holds the gate and exclusive volume lock and the chunk routes hold neither.
- `internal/authz`: the four patterns are classified; `Unrecorded` holds only the `PATCH`; the new
  consistency check.
- `internal/httpapi`: `volumeClass` of the upload routes.
- `internal/migration`: `skipInDataDir(".uploads")`.
- The migration applies on an existing database.

### Web (Vitest)

- `chunked-upload.ts`: chunk boundaries for a size that is not a multiple of `chunkSize`; retry and
  backoff; following a `409` offset; resume from a non-zero offset; offline wait; abort.
- `volume-explorer-view.ts` and the page: the resume `Modal` match rule, the unfinished-uploads
  `Alert`, the state transitions, the 10 GiB client check.

### e2e (Playwright, `task e2e`)

- Upload an 80 MiB file (three chunks) into a Volume, then read it back and compare its size and
  SHA-256.
- Abort the request after the first chunk with `page.route`, reload, pick the same file, Resume,
  and compare again. The test writes a real temp file and passes its path to `setInputFiles`: an
  in-memory buffer gets a new `lastModified` on each pick, so the resume match would never hit.

### Live check

The sandbox has no Cloudflare. Upload a file of about 2 GiB through a real domain behind a Free-plan
Cloudflare zone, then interrupt one upload by closing the tab and resume it.
