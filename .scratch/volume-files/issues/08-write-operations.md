# 08 — Write operations — upload, edit, delete (API and UI)

**What to build:** Everything that changes a Volume's contents, plus the warning that has to carry
the safety story for it.

**Blocked by:** 02, 03, 13, 05

**Status:** ready-for-agent

- [x] Upload a file into the selected directory: 1 GiB cap, and refused before the stream starts when it exceeds the Volume's free space — the helper checks `statfs`. A full disk is a host-wide failure, not a panel one
- [ ] Edit a text file and save, with Monaco reused from the Compose YAML screen; a file above 1 MB is not opened in the editor and the Explorer says so
- [ ] Delete a file or a whole directory tree, permanent and confirmed by a `Popconfirm` naming the path (`docs/GUIDELINES.md` §6). There is no trash: a hidden trash directory inside the Volume would be visible to the application using it
- [x] Every write is POST, PUT or DELETE — never GET — so the `SameSite=Lax` session cookie keeps them CSRF-safe
- [ ] When any Service in the Compose is running, the Explorer states that writing can corrupt a live stack, naming the consequence and the affected Services. The write is deliberately **not** gated behind an acknowledgement, so this banner is the only control here: it must name the consequence rather than say "be careful"
- [ ] Deliberately not in v1: create an empty file, mkdir, rename or move, copy/duplicate, a chmod/chown editor, search, previews, multi-select, drag-and-drop, symlink creation
- [ ] HTTP-seam tests with a faked runner asserting the argv for each write, and that the running-stack banner's condition is computed from the Compose's own status

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
