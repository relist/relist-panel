# 08 — Write operations — upload, edit, delete (API and UI)

**What to build:** Everything that changes a Volume's contents, plus the warning that has to carry
the safety story for it.

**Blocked by:** 02, 03, 13, 05

**Status:** ready-for-human

- [x] Upload a file into the selected directory: 1 GiB cap, and refused before the stream starts when it exceeds the Volume's free space — the helper checks `statfs`. A full disk is a host-wide failure, not a panel one
- [x] Edit a text file and save, with Monaco reused from the Compose YAML screen; a file above 1 MB is not opened in the editor and the Explorer says so
- [x] Delete a file or a whole directory tree, permanent and confirmed by a `Popconfirm` naming the path (`docs/GUIDELINES.md` §6). There is no trash: a hidden trash directory inside the Volume would be visible to the application using it
- [x] Every write is POST, PUT or DELETE — never GET — so the `SameSite=Lax` session cookie keeps them CSRF-safe
- [x] When any Service in the Compose is running, the Explorer states that writing can corrupt a live stack, naming the consequence and the affected Services. The write is deliberately **not** gated behind an acknowledgement, so this banner is the only control here: it must name the consequence rather than say "be careful"
- [x] After the plan: New folder (`mkdir`) and New file (`create`) were added at the operator's request — the spec, HTTP surface, helper and Explorer dialog shipped together in `dbfd35b` — leaving rename or move, copy/duplicate, a chmod/chown editor, search, previews, multi-select, drag-and-drop and symlink creation deliberately out of v1
- [x] HTTP-seam tests with a faked runner asserting the argv for each write, and that the running-stack banner's condition is computed from the Compose's own status

## Comments

**session 1 (API side, helper pre-flight and stream guard):** `volfs.FS` gained `FreeBytes()` —
`syscall.Statfs` on the root, `Bavail × Bsize` — the only new filesystem capability. `relist-volhelper
write` now accepts an optional `--expect <bytes>` (`write [--expect <bytes>] -- <path>`), parsed as a
non-negative integer (anything else is usage, exit 2). With `--expect` the helper measures free space
*before* it reads any stdin or opens the target and refuses a size that cannot fit with exit 3 — a new
`exitRefused`, distinct from 0/1/2 — naming need vs available; without `--expect` the pre-check is
skipped. Either way the copy is wrapped in a `spaceGuard` that delivers no more than the free space
measured at the start; a guarded abort removes the partial file best-effort (the write truncates its
target, so leaving a truncated file would be worse) and exits 3, while any other `volfs.Write`
failure stays exit 1. `volumes.Request` gained `DeclaredBytes int64`, and `volumeArgv` appends
`--expect <n>` for a `write` with a positive declared size — before `--`, alongside
`archive --format tar.gz`. The spec's Protocol and Caps sections were amended in the same commit.
The helper takes its free-space lookup as a `run` parameter (`(*volfs.FS).FreeBytes` in `main`), so
tests inject a tiny budget against a `t.TempDir()` and never fill a disk.

Evidence: `gofmt -l . && go vet ./... && go test ./...` under `golang:1.27-alpine` — gofmt silent,
vet clean, full suite green. New tests: `--expect` over the injected free space exits 3, leaves an
existing target untouched and a new one uncreated, with a reader that fails if stdin is touched; a
fitting declared size (including a stream ending exactly at the budget) and a write without
`--expect` succeed; an overrunning stream aborts with exit 3 and the partial file removed, both with
and without `--expect`; a malformed `--expect` is exit 2; a failed lookup stays exit 1; `FreeBytes`
answers for a real temp dir; the runner argv table asserts both `write` and `write --expect
1048576`. Still open on this ticket: the write HTTP endpoints (a later session sets `DeclaredBytes`)
and the UI.

**session 2 (API side, write and delete endpoints):** `volumes.RefusalError` is the typed
"the helper refused before doing the work": `dockerexec.VolumeRunner` maps a child exit code of 3
via an `exitCoder` (`*exec.ExitError` in production) to it, carrying the helper's own stderr
message; exits 1 and 2 stay ordinary `docker …: exit status N: <stderr>` failures.
`volumeapi` gained `PUT …/volumes/content` for an upload or an edit and `DELETE …/volumes/content`
for a file or a whole tree, both through the read pipeline refactored as `operationOf` — availability
503, unknown source 404, missing pre-flight 409, user validation 400 — with the write family
read-write and `operationContentPath` refusing the Volume root (absent, empty or `.`) as a 400
before any run. The PUT body is `r.Body` fed straight to the runner's stdin; a declared
Content-Length is required (chunked/unknown → 411) and must not exceed the hard-coded 1 GiB
(`> uploadCap` → 413), then it rides on `Request.DeclaredBytes` so the runner builds
`write --expect <n>`; success is 204. A `*volumes.RefusalError` answers **507 Insufficient
Storage** with the helper's need-vs-available message, everything else stays 502; the helper's
own partial-file clean-up is unchanged and only reported. Routes are registered as PUT and DELETE
only, so no write is reachable by GET; auth middleware unchanged.

Evidence: `gofmt -l . && go vet ./... && go test ./...` under `golang:1.27-alpine` — gofmt silent,
vet clean, full suite green. New tests: the dockerexec mapping (exit 3 → `RefusalError` with the
helper's message; 1 and 2 stay ordinary) plus the HTTP seam — recorded `Request`s for write and
delete (`ReadOnly: false`, `DeclaredBytes` set, `OpWrite`/`OpDelete`) with the body as stdin, 411
without a declared length, 413 above the cap with no run (and exactly at the cap proceeding), 507
from a scripted refusal, 502 for ordinary write/delete failures, 400 for the root path on both
verbs, membership 404 and pre-flight 409 with no run, 503 while unavailable, 204 success, and
GET/POST of `/content` matching no route and running nothing. Still open on this ticket: the
editor (1 MB rule), the upload UI and the delete `Popconfirm`, and the running-stack banner.

**session 3 (UI, write actions and the running-stack banner):** the Explorer gains its write
slice. `api.ts` gains `ApiError`, which carries the HTTP status on every non-2xx response while
still being an `Error` the existing callers read `.message` from, plus `writeVolumeFile` (PUT with
the `File` itself as the body, so the browser declares its length — the API requires it) and
`deleteVolumeEntry` (DELETE), both built by the one `volumeContentUrl`; `store.tsx` exposes them
under the per-entry pending keys `volume-write:<compose>:<source>:<path>` and
`volume-delete:<compose>:<source>:<path>`. The toolbar gains `Upload` onto the current directory:
the hidden file input hands its `File` straight to the API, a client-side `uploadTooLarge` fast
path refuses a file over 1 GiB before any request (the API's 413 stays the enforcer), success says
`Uploaded <path>` and re-reads the directory, and a failure stays on screen as an Alert whose copy
comes from `uploadFailure`/`deleteFailure`: 413 names the 1 GiB cap, 507 names the Volume's free
space with the helper's own need-vs-available message and that a refused write removes its partial
file, 400/409/422 keep the API's words (409 adds that mounting the missing source is what would
create it), and anything else falls back to the API's message. Every row gains a danger delete
action, confirmed by a `Popconfirm` that names the full relative path — a directory adds "and
everything under it" — and states there is no trash and no undo; 204 says `Deleted <path>` and
re-reads the listing, and the action column stops the row's click so a delete never navigates.
A Compose with any Service running raises a warning banner above the write controls, derived by
the pure `runningWriteWarning(compose.status, volume)`: the condition is the Compose's own
`status.running > 0` and the names are the Services that mount this Volume, deduplicated and in
declaration order; the copy names the consequence (a live database's data directory above all) and
says outright that there is no confirmation gate. It is deliberately not an acknowledgement — the
write proceeds. The unverifiable-bind confirmation now covers reads and writes, and the screen's
footnote no longer claims to be read-only. No editor, restore, mkdir, rename or new-file control;
the editor (1 MB rule) stays for session 2.

Evidence: `pnpm typecheck && pnpm test` under `node:24-alpine` (frozen lockfile, warm pnpm store) —
tsc clean, **196/196 green** (14 new: `uploadTooLarge`'s cap boundary, `uploadFailure`/`deleteFailure`
for 413/507/400/409/422/502 and a non-`Error` rejection, `runningWriteWarning`'s condition,
deduplicated service list, no-uses fallback and consequence copy, and `volumeContentUrl`'s
encoding). No DOM testing library; the decisions stay pure helpers. No rebuilt
`internal/webassets/dist` is committed.

**session 4 (UI 2/2, the text editor):** the Explorer's editor slice. Opening is an explicit
per-row action — a file row's `Edit` button, loading on the store's new
`volume-read:<compose>:<source>:<path>` pending key — while the row click keeps selecting a file
for download, so nothing about a click silently becomes "edit". The 1 MB rule is the pure
`editorGate(entry, path)` (`EDITOR_MAX_BYTES = 1 << 20`, boundary exact): a file above it is
**not fetched**, and the screen says "Too large to edit", names the file and its size against the
1 MB limit, and offers the download as the alternative. Fetched bytes go through
`editorContent(path, bytes)`, whose documented rule refuses binary-ish content rather than
rendering mojibake: a NUL byte anywhere in the first 8 KiB, or bytes that are not valid UTF-8
(which the editor could only show as replacement characters and a save would write back) — both
refusals say so and point at the download. `readVolumeFile` was added to `api.ts` (plain `read`,
no `download` marker, bytes held whole under the gate) and to the store; `api.ts`'s URL builder
was split so `volumeReadUrl` is the same URL plus `download=1`.

The editor itself is `VolumeFileEditor`, following `FullscreenEditor`'s full screen treatment:
full-viewport modal, a 56px toolbar carrying the file's relative path as the title and the
`Modified` marker, `Discard`, `Save` and close, and the error `Alert` inside the modal so a
failure stays on screen. The page owns the draft (`original`/`draft`); `isDirty` is the
comparison, `editorSaveStatus` derives clean/dirty/saving/failed (a save in flight outranks a
previous failure), and Discard and close both ask through `modal.confirm` when there are unsaved
changes. Saving is one `PUT …/volumes/content` with the draft as a `Blob` (the browser declares
the length); success says `Saved <path>`, moves `original` to the draft so the file is never
re-read, and patches the row's size and mtime in place rather than re-listing the directory.
Failures use `editFailure`, the existing `writeFailure` mapping worded for a save: 413 the 1 GiB
write cap, 507 the helper's need-vs-available and the partial-file clean-up, 409 the missing
source, 400 the API's own refusal, 422 the helper's, anything else the API's message. Monaco is
reused through a ported `TextFileEditor` (same `MONACO_OPTIONS`/theme/`monaco.ts`; language from
the filename; model keyed `volume:<path>`), so no editor dependency was added.

Evidence: `pnpm typecheck && pnpm test` under `node:24-alpine` (frozen lockfile, warm pnpm store) —
tsc clean, **213/213 green** (17 new: `editorGate`'s exact boundary, `editorContent` for valid
UTF-8/empty/NUL-in-window/NUL-past-window/invalid UTF-8, `isDirty`, `editorSaveStatus`'s
precedence, `editFailure` for 413/507/400/409/422/502, and `volumeReadApiUrl`'s encoding). No DOM
testing library; the decisions stay pure helpers. No rebuilt `internal/webassets/dist` is
committed.
