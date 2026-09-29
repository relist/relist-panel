# 12 — Restore: streamed tar.gz extraction

**What to build:** Restoring an uploaded tar.gz into a directory that already exists, streamed end
to end with nothing staged on the panel's disk. Upload and extract are one operation; extraction is
the sharpest edge in the feature, so this lands last.

**Blocked by:** 02, 03, 13, 05

**Status:** ready-for-human

- [x] `internal/volfs` gains `extract`: every entry is validated **before it is written** — names relative and free of `..` segments, no absolute paths, symlink and hardlink entries refused outright — and a refusal aborts the rest of the stream
- [x] The helper gains `extract -- <relpath>`, consuming the tar.gz on stdin; the target is a directory relative to `/data` that must already exist, directories inside the archive are created, existing files are overwritten and directories merge
- [x] One HTTP endpoint streams the request body straight into the run's stdin with the Volume mounted `:rw` — upload and extract are one operation, and nothing is staged on the panel's disk
- [x] Caps enforced end to end: compressed ≤ 1 GiB on the request body, uncompressed ≤ 4 GiB and ≤ 10k entries counted by the helper as it streams
- [x] The UI discloses, before the operator runs it, that a stream that fails mid-way leaves the entries already written in place — extraction is partial, there is no transaction across a stream — and names the target directory
- [x] Tests against malicious archives: `..` entries, absolute entries, symlink and hardlink entries, and the entry-count and uncompressed-size caps

## Comments

**session 1/3:** `internal/volfs/extract.go` landed — the `volfs` box and the tests that pin it.
`func (fs *FS) Extract(target string, src io.Reader, limits ...ExtractLimits) error` streams a tar.gz
from `src` into `target`, a directory relative to the root that must already exist: a missing or
non-directory target is an error, never a create, and it is checked before a byte of `src` is read.
Every header is validated before anything is written for it, in this order: the entry count (10k),
then the name — relative and slash-separated, no empty or `..` segment under either separator, no
leading slash, no Windows drive/UNC shape — then the type, where only `TypeReg`/`TypeRegA` and
`TypeDir` pass, and then the body, counted against the uncompressed cap as bytes are actually
written (an entry that ends exactly at the cap is allowed). Directories go through
`os.Root.MkdirAll`, created owner-writable with the header mode applied once the stream ends so a
read-only directory does not lock out its own children; files go through `os.Root.Create`,
overwritten and truncated, then chmod'd to the header's permission bits. Nothing is ever deleted:
entries already written stay on a later refusal, a file cut off mid-body stays truncated, and the
error path never rolls back. Refusals abort the stream and are classifiable with `errors.Is`:
`ErrUnsafeArchive` (name or type) and `ErrArchiveTooLarge` (entry or byte cap), each wrapped with
the offending entry; every other failure — gzip header, truncated body, failed write — stays an
ordinary error. Caps are injectable: `ExtractLimits{MaxEntries, MaxBytes}`, with
`DefaultExtractLimits()` = 10,000 / 4 GiB; the 1 GiB compressed cap stays the HTTP layer's, on the
request body.

Evidence under `golang:1.27-alpine`: `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` all
green; `internal/volfs` gains 12 extract tests (28 sub-cases) — an `Archive`→`Extract` round trip of
a 1 MB binary, nested files and a newline-bearing name into a second root, content-identical with
modes and an empty directory intact; overwrite/truncate and merge with siblings and outsiders
untouched; 15 unsafe names (`../escape`, `/etc/passwd`, `a//b`, `..\..\escape`, `C:\escape`,
`\\host\share`, empty and rooted names) each refused with the Volume tree unchanged;
symlink/hardlink/FIFO/character/block entries each refused and unwritten; entry cap and byte cap
with tiny injected limits (the file cut at the cap is left as written); a 1 GiB-declared header with
a 13-byte body treated as an ordinary `unexpected EOF`, not a size refusal; missing,
non-directory, `..` and absolute targets; empty, garbage and gzip-but-not-tar streams; and the
partial case where entry 2 is unsafe while entry 1 stays. Still open: the helper's
`extract -- <relpath>` (session 2/3) and the streaming endpoint plus the UI disclosure (session 3/3).

**session 2/3:** the helper's `extract -- <relpath>` landed, and `volfs` now classifies a body that is
not a usable tar.gz. `ErrInvalidArchive` joins `ErrUnsafeArchive` and `ErrArchiveTooLarge`:
gzip/tar parse failures and entries cut off mid-body wrap it (a marker around the tar body reader
keeps clean ends clean), while a failed write into the Volume stays an ordinary error. The helper
registers `extract` with the same `-- <relpath>` shape as every other op — relative-only, an
absolute path refused before `os.OpenRoot` — calls `volfs.Extract(target, stdin)` with the spec's
default limits, and maps a refusal to **4**, the new `exitArchiveRefused`: unsafe entry, cap
exceeded, or not a usable tar.gz, with the offending entry in the message. Targets, I/O and space
stay 1; malformed invocations stay 2. No new flags, no signal-handler changes. Tests through
`run(...)` against `t.TempDir()`: a `volfs.Archive` round trip into a subdirectory and into `.`; an
unsafe second entry exits 4 with the first left in place (partial semantics); 10,001 hand-built
entries trip the real 10k default cap with exit 4 — no injection seam needed, the helper's call
site keeps the production defaults; garbage stdin exits 4; a missing target exits 1 with stdin
proven unread; an absolute path exits 2. Spec: the op list already carried `extract` from 873200b;
the Protocol paragraph gained exit 4. Evidence under `golang:1.27-alpine`: `gofmt -l .`
silent, `go vet ./...` clean, `go test ./...` all green. Still open: the streaming endpoint and the
UI disclosure (session 3/3).

**session 3/4:** the restore endpoint landed. `PUT .../volumes/restore?source=&path=&user=&service=`
rides the existing pipeline unchanged (availability 503 → Compose 404 → membership 404 → pre-flight
`missing` 409 / `unverifiable` proceeds → user/path 400), then streams the raw request body into
exactly one read-write `extract` run's stdin — `OpExtract`, `ReadOnly: false`, nothing staged on the
panel's disk. `path` is the target directory, so unlike the write family the Volume root is legal:
absent, empty or `.` runs against `.`; a leading `/` is still a 400. The declared length is required
(chunked → **411**) and checked against the spec's 1 GiB compressed cap (**413**), both before any
run; success is **204**. `dockerexec.VolumeRunner` maps helper exit **4** to the new typed
`volumes.ArchiveRefusedError`, answered **422** with the helper's own message (unsafe entry, cap
exceeded, unusable tar.gz — the body is the client's); exit 3 stays `RefusalError` → 507 and every
other failure 502. HTTP-seam tests, no real Docker: the recorded run (`OpExtract`, `ReadOnly:
false`, target path, body as stdin) for a named volume, a bind and an unverifiable bind; the 411 and
413 refusals with no run; 422 carrying the helper's message; the 507/502 mapping; the legal root vs.
a leading-`/` 400; and the pipeline refusals (unknown Compose, undeclared source, missing
volume/bind, unavailable feature) each with no run. Evidence under `golang:1.27-alpine`:
`gofmt -l .` silent, `go vet ./...` clean, `go test ./...` all green. Still open: the UI disclosure
(session 4/4).

**session 4/4 (UI):** the Explorer's restore landed. The toolbar gains **Restore tar.gz**: a hidden
`.tar.gz`/`.tgz` input, a client-side check of the 1 GiB compressed cap, and — before anything is
sent — a confirmation modal carrying the whole disclosure, target named by its relative path (`.`
at the root): files overwritten and directories merged with nothing else deleted; upload and extract
one operation with the archive never stored; partial extraction on a failed stream, with no
transaction; and the three caps it can hit (1 GiB compressed, 4 GiB uncompressed, 10,000 entries
counted by the helper during extraction). Cancel sends nothing. Confirming streams the picked `File`
as the `PUT` body through `api.restoreVolume`; the toolbar action spins while it runs, success names
the archive and target and re-reads the directory, and failures land in the Explorer's established
alert via `restoreFailure`: 413 names the compressed cap, 422 the archive refusal with the helper's
words and the partial truth, 507 the space refusal with the helper's need-vs-available text and no
rollback claim, 409 the missing source, and 400 the API's message. The pure helpers
(`restoreTarget`, `restoreTooLarge`, `restoreDisclosure`, `restoreIntent`, `restoreFailure`) live in
`web/src/pages/volume-explorer-view.ts` with 15 new tests, and `volumeRestoreUrl` is pinned in
`web/src/data/api.test.ts`. Evidence under `node:24-alpine`: `pnpm typecheck` clean, `pnpm test` all
green (13 files, 230 tests). The malicious-archive tests box was already evidenced by sessions 1–2
(15 unsafe names; symlink/hardlink/FIFO/character/block entries refused unwritten; entry and byte
caps) and is ticked with this close.
