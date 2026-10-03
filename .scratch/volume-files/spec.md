# Spec — Volume files: browse, edit, and download what a Compose mounts

## Problem

The panel deploys Compose stacks but gives the operator no way to look inside the data those stacks
leave behind. A named volume or bind mount holding a database, an uploaded corpus, or a botched
config file is only reachable by SSHing in and running `docker run -v ... alpine tar`. The Compose
delete action (compose-panel ticket 11) can *remove* a Compose's volumes; nothing can read them.

The operator wants: on a Compose's page, a tab listing **only the Volumes that Compose mounts**; a
file manager over one of them — browse, edit, upload, delete; download of a directory as **tar.gz**,
and restore of an uploaded tar.gz. It must match the panel's Ant Design theme, and it must not make
the panel heavy.
The reference point is Dokploy, whose panel idles at 1–2 GB RSS, which is what makes small VPSs
painful.

Vocabulary is `CONTEXT.md`'s: a **Volume** is one entry in a Compose's `volumes:` list (named volume
or bind mount — the panel treats both alike), and the **Volume Explorer** is the screen.

## Decision

Build the file manager ourselves, in two pieces:

- A **thin, network-less helper container** that mounts exactly one Volume and speaks a small
  argv/stdio protocol. No HTTP server, no listening socket.
- The **UI inside the panel's existing SPA** — Ant Design components and the Monaco editor the panel
  already ships (`web/src/monaco.ts`).

The panel drives the helper with **one `docker run --rm` per operation**, streaming file bytes over
the container's stdin/stdout. No container outlives the operation it serves, and nothing is ever
sent to a container started earlier. See **ADR-0010**, which revises ADR-0008's container lifetime;
ADR-0008's measurements, hardening flags and helper design carry over.

### Why build it rather than embed an off-the-shelf file manager

1. **Nothing off the shelf does tar.gz.** Measured: `sigoden/dufs` has no tar.gz folder archive —
   `?tar` and `?tgz` fall through to its HTML listing — and `filebrowser` ships no tar.gz either.
   tar.gz is a stated requirement.
2. **Any off-the-shelf file manager is a second HTTP server with a second auth system** — a login
   session the panel's logout does not end, and a port that has to be reachable from the panel.
   That reachability is the single risk worth engineering away, and it is the one thing a
   third-party file manager cannot avoid.
3. **Weight.** The lightest measured option is still about as large as the entire panel.

### Measured (this host, 2026-09-24)

| | RSS idle | tar.gz | Network surface | New dependency |
| --- | --- | --- | --- | --- |
| `filebrowser/filebrowser:latest` | 14.73 MiB | ✗ | HTTP server, must be proxied | yes |
| `sigoden/dufs:latest` | 1.65 MiB | ✗ | HTTP server, must be proxied | yes |
| Panel binary, idle → after one request | 14.35 MB → 15.42 MB | — | — | — |
| Minimal Go helper (stdin loop, no HTTP) | **2.21 MB** | ✓ stdlib | **none** | none |

Two corrections this table forces:

- `host-monitoring/spec.md` records the panel idling at **3.86 MB** RSS. Measured it is **14.35 MB**
  (`VmRSS`; `VmHWM` agrees). The 1.87 GB `VmSize` is Go's virtual reservation, not memory in use.
  That figure needs re-measuring before it is relied on again.
- The 2.21 MB helper is a *minimal* binary. The real helper must be its own small binary
  (`cmd/relist-volhelper`), not a subcommand of the panel binary, so it does not link
  `modernc.org/sqlite` and `net/http` and pay their init cost. Re-measure once built.

### Execution costs (Docker 29.8.0, n=11 per target, warm-up discarded)

| Operation | mean | max |
| --- | --- | --- |
| `docker exec` in a running container | **33 ms** | 40 ms |
| `docker run --rm` (full ephemeral lifecycle) | **365–594 ms** | 2110 ms |
| `docker create` | 109 ms | 120 ms |
| `docker start` | 148 ms | 160 ms |
| `docker stop`, pid1 traps SIGTERM | 1092 ms | 1320 ms |
| `docker stop`, pid1 ignores SIGTERM | **10123 ms** | 10130 ms |
| `docker rm` | 55 ms | 60 ms |
| `docker inspect` / `docker volume inspect` | 10 ms | 14 ms |
| `docker compose config --format json`, 3 services / 3 volumes | 30 ms | 30 ms |
| `docker exec` streaming 100 MB out of a container | 92 ms (**1089 MB/s**) | 100 ms |

Three consequences:

- **The gap between the two models is real and accepted.** One exec is 33 ms; one `docker run` is
  365–594 ms, with a 2.11 s outlier — a 10x penalty on every directory listing, and an outlier that
  would read as a hang. This feature's operations are not interactive file management: a backup, an
  occasional config edit, a restore. Paying 0.4–0.6 s per operation buys zero idle cost, no standing
  state, and a strictly smaller exposure window. The hardening flags (`--cap-drop ALL`,
  `--read-only`, `--security-opt no-new-privileges`) and the Volume mount add nothing measurable
  (0.365 s vs 0.373 s), so they cost nothing to include.
- **The helper inherits the fast-stop property.** `docker stop` costs 10.1 s when pid1 ignores
  SIGTERM and 1.1 s when it does not. A Go binary as pid1 dies on SIGTERM by default, and the helper
  installs no signal handler, so any stop stays fast. Nothing in this design issues `docker stop`
  (see No standing state); the property is kept deliberately.
- **`docker compose config` is cheap enough to run per page load** (30 ms) — no caching needed.

### Mounting creates whatever is missing

Verified two ways: `docker run -v <nonexistent-volume>:/data` **creates the volume**, and
`docker run -v <nonexistent-host-path>:/data` **creates that directory as root:root 0755**. Opening
a browse view would therefore mutate the host unless something stops it. See **Pre-flight** below.

## The helper

### Run spec

```
docker run --rm -i \
  --network none \
  --user <uid>:<gid> \
  --read-only --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -v <resolved source>:/data[:ro] \
  --entrypoint /usr/local/bin/relist-volhelper \
  <panel image> <op> -- <relpath>
```

Every flag is load-bearing:

- `--rm` — the container removes itself when the operation ends. Nothing to reap at panel start,
  nothing to adopt across an upgrade.
- `-i` — stdin is the pipe that carries `write` and `extract` bytes in, and its close is how a
  helper that is mid-operation learns the caller is gone. Never `-t`: a TTY rewrites the stream
  (line discipline, no reliable EOF) and breaks binary both ways.
- `--network none` — no ingress, no egress. This is why the feature needs no proxy, no shared token,
  no new Docker network, and no change to the ADR-0002 bootstrap.
- **Exactly one mount**, the declared source, at `/data`. Never the Docker socket, never another
  host path.
- `--user` always explicit. Root is a deliberate opt-in, not a default (see UID/GID).
- `:ro` unless the operation writes.
- `--read-only` rootfs plus `--tmpfs /tmp` — so nothing can stage a copy of the data, including the
  helper itself.
- `--entrypoint ... <op> -- <relpath>` — the helper's argv is the operation and its path, and
  nothing else.

The image is the panel's own, so the host already has it from `docker compose build` — no registry,
no runtime pull, no version skew between panel and helper. The panel resolves its own image name
with `docker inspect $(hostname)` → `.Config.Image` (Docker sets hostname to the container ID by
default), falling back to a `RELIST_IMAGE` env var.

A cancelled HTTP request kills the `docker` CLI child the panel started; the helper's pipe closes
and it dies on `EPIPE`/`EOF`; `--rm` then removes the container. Nothing lingers.

### Protocol

One `docker run --rm` per operation, path as an argv element, `--` before any path so a name starting
with `-` cannot be read as a flag. The container's command is the helper invocation:

```
relist-volhelper list    -- <relpath>
relist-volhelper stat    -- <relpath>
relist-volhelper read    -- <relpath>                 # bytes on stdout
relist-volhelper write   [--expect <bytes>] -- <relpath> # bytes on stdin
relist-volhelper delete  -- <relpath>                 # file or whole tree
relist-volhelper mkdir   -- <relpath>                 # one new directory
relist-volhelper create  -- <relpath>                 # one new empty file
relist-volhelper archive --format tar.gz -- <relpath> # bytes on stdout
relist-volhelper extract -- <relpath>                 # tar.gz on stdin
```

Paths are **relative to `/data`**, always, and are refused before any run if they are absolute,
carry a `..` segment, or resolve to the root without naming it: `os.Root.RemoveAll("sub/..")`
empties the root before it reports `invalid argument` (Go 1.27.1), so the panel's path rule and
`volfs.Delete` both refuse the shape — the root alone does not. Metadata
operations return JSON on stdout; `read` and `archive` return raw bytes and `extract` consumes them.
Read operations mount `:ro`; `write`, `delete`, `extract`, `mkdir` and `create` mount `:rw`.

The helper exits `0` on success, `1` on an operation failure and `2` on a malformed invocation. A
`write` also exits **3** when it is refused for want of space, naming need vs available: `--expect`'s
declared size cannot fit the Volume — refused before any stdin is read — or the stream overran the
free space measured when it began, in which case the partial file is removed best-effort. An
`extract` exits **4** when the archive was refused — an unsafe entry, a cap exceeded, or not a
usable tar.gz — so the panel answers 4xx instead of 502: the body is the client's, not the Volume's.
A `mkdir` or `create` exits **5** when the name is already taken — neither ever merges an existing
directory nor replaces an existing file — so the panel answers 409 Conflict rather than 502.

Every filesystem call goes through **`os.Root`** (`os.OpenRoot("/data")`), which refuses to resolve a
name outside the root and rejects absolute symlinks. This is defense in depth *behind* the panel's
own validation, and it is what makes a symlink planted inside a Volume harmless.

`os.Root` in Go 1.27.1 has the methods this needs — `Open`, `Create`, `OpenFile`, `ReadFile`,
`WriteFile`, `Stat`, `Lstat`, `Mkdir`, `MkdirAll`, `Remove`, `RemoveAll`, `Rename`, `Link`,
`Symlink`, `Readlink`, `Chmod`, `Chown`, `Lchown`, `Chtimes` — **but no `ReadDir`**. Listing goes
`root.Open(".")` → `*os.File.ReadDir(-1)`.

**`go.mod` must move off `go 1.23.0`.** Verified: with a `go 1.23.0` directive, `go build` accepts
`os.Root` but `go vet` and `go test` both fail with `os.OpenRoot requires go1.24 or later`. Since the
HTTP seam is the repo's only test seam, this would break the suite. `go 1.24` minimum, `1.25`
preferred for the wider `Root` surface. The Dockerfile already builds with `golang:1.27-alpine`.

### Names, symlinks, hardlinks

Raw names come from `ReadDir`, never from parsing a listing, because a filename may contain a
newline — busybox `ls -1` renders a file genuinely named `a\nb.txt` as `a?b.txt`, a name that does
not exist.

Symlinks are **shown, described, and followed only when they stay inside the Volume**:

| | `ReadDir` | `Lstat` + `Readlink` | `Open` |
| --- | --- | --- | --- |
| `inside/real.txt` | listed | regular file | opens |
| `link-inside` → `inside/real.txt` | listed, type `L` | target reported | **opens** |
| `link-abs-outside` → `/tmp/secret.txt` | listed, type `L` | target reported | **refused** — `path escapes from parent` |
| `link-rel-outside` → `../../secret.txt` | listed, type `L` | target reported | **refused** |
| `link-etc` → `/etc/passwd` | listed, type `L` | target reported | **refused** |

So the Explorer shows every symlink with its target and follows the ones `os.Root` permits, refusing
the rest with a clear message. Hiding symlinks would misrepresent the Volume — a symlink is part of
the data, and an operator who cannot see it cannot understand why a path exists. Symlink *creation*
is out of scope.

### Why the helper does the work in Go, not by shelling out

`docker run` is the **transport**; the helper binary is the **implementation**. These are two
layers, not two competing designs. The alternative — each run executing `ls`, `cat`, `cp`, or `tar`
inside the container, the way a hand-rolled `docker run alpine tar` recipe does — was tested against
this image and rejected on two counts:

- **busybox tar returns the wrong file with a success status.** `tar -czf - -C /data -- /etc/passwd`
  exits **0** and yields the *container's* `/etc/passwd` — busybox strips the leading `/` and
  archives it as `etc/passwd`. A download that silently returns something the operator did not ask
  for, reported as success. busybox tar 1.37.0 also has no `--one-file-system` and no
  `--dereference`, so the guards a careful implementation would use are not all available.
- **`ls` output is not a filename transport**, as above.

`os.Root` refuses the same shapes by construction, in-process, with no validate-then-use gap:
`../secret/private.txt`, `/etc/passwd`, and `dir/../../secret/private.txt` each fail with
`path escapes from parent`, while legitimate paths inside the root open normally.

**`docker cp` is not a shortcut.** It still requires a container with the Volume mounted, so it is
the same plumbing; but it copies only whole trees — no list, stat, edit, mkdir, or rename — emits its
own tar we do not control, and carries a history of symlink-race vulnerabilities from resolving
paths in the daemon. It buys nothing here.

## Resolving a Compose's Volumes

Resolve them with the Compose CLI, not by parsing YAML:

```
docker compose -p <project> -f <compose file> --env-file <env file> config --format json
```

Verified to resolve, in one call: named volumes (service-level `source: pgdata` against a top-level
`volumes.pgdata.name: demo_pgdata`), relative binds (`./rel` → the Compose file's directory),
absolute binds, `${VAR}` interpolation (`${HOSTPATH:-/tmp/vmtest/srv}` → the default), and each
service's resolved `user:`. It also prunes volumes no service references, which is exactly the set
this feature wants.

Reading `services.*.volumes[]` for `type` / `source` / `target` / `read_only` and the top-level
`volumes` map for real names gives both kinds:

| kind | shown as | mounted into the helper as |
| --- | --- | --- |
| `volume` | logical name + real name (`demo_pgdata`) | the real volume name |
| `bind` | the resolved absolute host path | that path |

**The one invariant that matters: a source path is never free text.** It comes only from the parsed
Compose output. The operator picks from a list; no input field accepts a host path. This keeps the
feature's privilege at "exactly what the operator already declared in a Compose the panel deploys"
rather than "any path on the host". It follows that a Compose binding the panel's own data directory
is allowed — the operator declared it — with a warning in the Explorer that editing `panel.db` can
break the panel.

**Before the first deploy the tab is unavailable.** Verified: `docker compose config` fails with
`couldn't find env file` when the generated env file does not exist, and the panel always passes
`--env-file` (`dockerexec.run`). Show the CLI's own error and why. Do **not** fall back to a
hand-rolled YAML preview: it would show `DATA_DIR/pgdata` unresolved while the real source resolves
elsewhere, and a plausible-looking wrong answer is worse than none. Do **not** write the env file
earlier either — the panel uses the presence of the Compose file as its "is deployed" signal
(`resolveDeployedCompose`, `currentStatus`, `delete` all `os.Stat` it), and that signal would break.

### Pre-flight

Because a `docker run` with a missing source creates it, the panel checks before it mounts:

- **Named volume**: `docker volume inspect` (10 ms, exact) runs before the first operation against a
  Volume, and again before `archive` and `extract` — so a Volume deleted underneath a browse is
  reported, not silently recreated empty.
- **Bind source inside the data directory**: the panel can `os.Stat` it directly (identical-path
  mount, ADR-0002), so it checks before every operation and refuses if it is absent.
- **Bind source outside the data directory**: the panel cannot stat it, and probing it with a mount
  would create it. The confirmation before the first operation states plainly that the path does not
  exist or cannot be checked, that continuing will create it as `root:root 0755`, and the operator
  decides.

## UID/GID

One rule, resolved before the first operation and passed to every `docker run` as `--user`:

- **The resolved `user:` for a service the operator picks**, from `docker compose config` (verified
  present in that output). The stack's files belong to that account, so files the helper writes stay
  usable by the stack.
- **Otherwise `0:0`, with a visible warning** — files it creates become root-owned, and a stack that
  runs as uid 999 may then fail to start.

Root is a deliberate opt-in, never a default. The UI shows the resolved value and lets the operator
override it. Every operation accepts `service` or a numeric `user=uid:gid`; the chosen
Service must mount the requested Volume. A Service's resolved `user:` that is not itself a numeric
uid:gid — a name such as `postgres`, or a bare uid — is refused (HTTP 400, explained in the
Explorer): `--user` resolves a name against the helper image's `/etc/passwd`, not the Service's own
image, and a bare uid's group is image-dependent too, so the operator must supply the explicit
numeric override instead. UID 0 requires `rootConfirmed=1`, and an
unverifiable bind requires `bindConfirmed=1`, checked before the helper runs.
The Explorer asks for these acknowledgements once, behind its Operation user Confirm; it is the
only place an archive starts, and nothing is listed or downloaded before the operator confirms.

## Archives

The archive is **stdlib only** — `archive/tar` and `compress/gzip`, no external module. It writes
to a non-seekable writer, verified end to end: a tree of three files (a 1 MB random binary, a 4 KB
nested file, a text file) was streamed through an `io.Pipe` — the same shape as an HTTP response
body — and read back out of that pipe, all three entries byte-identical by sha256. Nested paths
survive as relative, slash-separated names (`sub/nested.bin`).

The archive is produced by the helper and piped straight through the container's stdout into the
HTTP response. Nothing is staged on the panel's disk, so a 50 GB Volume never becomes a 50 GB file
in the data directory.

Rules:

- **Entry names are set explicitly.** `tar.FileInfoHeader` fills in only the base name; it must be
  overwritten with the path relative to the Volume root, slash-separated, never absolute. A leading
  `/` or `..` in an entry name is how a *download* becomes a write outside the destination on
  whoever extracts it.
- **Symlinks are stored as symlinks**, faithfully. The alternative silently produces an archive that
  differs from the Volume, and a restore from it loses data without saying so. The Explorer marks
  each symlink that points outside the Volume as it lists it, so the extraction-time hazard is
  disclosed where the operator acts; a pre-download count was dropped with ticket 06 — it would need
  a second full walk to know before the streaming archive's own walk does. The fix for that hazard is
  at extraction (see Restore), not in whether we record the
  data honestly.
- **Hardlinks are written as regular files**, never as hardlink entries. That sidesteps the hardlink
  tar-slip vector entirely at the cost of a larger archive, and Go's `FileInfoHeader` cannot express
  a hardlink target anyway.
- **`Content-Disposition` follows RFC 6266**: control characters stripped, and `filename*` with
  percent-encoding for any non-ASCII name.
- A single file and a directory are both downloadable; a single file is served as a plain download,
  not wrapped in an archive.

A tar.gz must be fully received before extraction — gzip decompresses sequentially — so the download
cannot show a size-based progress bar. That is inherent to streaming, not a bug. Resumable downloads
would need a staged file with `Range` support, which costs disk and is out of scope.

## Restore

A tar.gz is streamed from the HTTP request body straight into the helper's stdin for the `extract`
operation — one `docker run --rm -i` with the Volume mounted `:rw`, no temp file anywhere, nothing
staged on the panel's disk. Upload and extract are one operation: the operator does not "upload then
extract" in two steps, and the archive is never stored.

The target is a directory, relative to `/data`, that must already exist; directories inside the
archive are created as part of extraction.

Every entry is validated **before it is written**: names must be relative and contain no `..`
segment, no absolute paths, and symlink or hardlink entries are refused outright — the
extraction-time hazard ADR-0008 named. A refused entry aborts the rest of the stream.

Partial extraction must be disclosed rather than hidden: entries written before a failure stay
written (there is no transaction across a stream), and the UI must say so before the operator runs
it. Files that already exist are overwritten; directories merge; nothing else is deleted.

Caps are enforced end to end: compressed ≤ 1 GiB on the request body; uncompressed ≤ 4 GiB and
≤ 10k entries, counted by the helper as it streams.

## No standing state

A container exists only while its operation runs. Panel start has nothing to reconcile — there is no
long-lived container to find or stop, and `--rm` leaves nothing behind when an operation ends. An
upgrade has nothing to adopt for the same reason: every operation runs the helper out of the image
the panel is running, so panel and helper cannot skew. The only lifecycle duty is killing an
in-flight `docker` child when the operator's HTTP request is cancelled; the helper follows it down
(see Run spec).

## v1 operation set

Read: list a directory (name, size, mtime, owner uid/gid, mode, kind); `stat` one entry; download a
single file; download a directory as tar.gz.

Write: upload a file into the current directory; edit a text file and save (Monaco); create one
folder (`mkdir`) or one empty file (`create`, O_EXCL: an existing name is refused, never replaced);
delete a file or a whole directory tree.

Restore: extract an uploaded tar.gz into a directory that already exists.

Deliberately **not** in v1 — all of these are additive later: rename or move, copy/duplicate, a
chmod/chown editor, search, image or media preview, multi-select or drag-and-drop, symlink creation,
and zip in either direction. Each lands as another operation on the same per-operation API rather
than as rework; `mkdir` and `create` were the first, added without touching the pipeline.

Delete is permanent, with a `Popconfirm` naming the path — no trash. A hidden trash directory inside
the Volume would be visible to the application using it, which is worse than the operator having to
mean it.

## Caps

Hard-coded, no env knobs. They exist to protect the small VPS this feature is sized for, and a knob
is an invitation to turn them off:

| | limit |
| --- | --- |
| upload | 1 GiB, and refused when it exceeds the Volume's free space (the helper checks `statfs`: a declared size is checked before stdin is read, and a stream that still overruns is aborted, its partial file removed) |
| editable text file | 1 MB |
| archive stream | 10 minute timeout |
| restore, compressed | ≤ 1 GiB, enforced on the request body |
| restore, uncompressed | ≤ 4 GiB, counted by the helper as it streams |
| restore, entries | ≤ 10,000, counted by the helper as it streams |

The free-space check is the part no OS default provides, and a full disk is a host-wide failure, not
a panel one.

## What the Explorer shows

The Explorer shows resolved paths — the host path for a bind source, and
`/var/lib/docker/volumes/<real>/_data` for a named Volume. The operator owns the host.

## Screen rules

- The Volumes tab lists only the Volumes that Compose mounts, with kind, source, target, and the
  container-side path each Service uses. It is a launcher: each row opens the Explorer, and no
  archive starts from the tab.
- The Explorer's Operation user card applies an identity only when the operator presses Confirm: the
  Service (or numeric uid:gid) and its required acknowledgements are one confirmed snapshot that
  every list, read, download and write uses. Changing the form afterwards keeps the confirmed
  snapshot in force until Confirm is pressed again.
- New folder and New file are one dialog with two titles. The name is checked in the dialog against
  the listing it can see — one entry, never empty, never `.` or `..` — and the create runs as the
  confirmed operation user; when the dialog's check is raced, the API's 409 is the refusal.
- The Volume Explorer opens in a new browser tab, as its own route in the panel SPA, behind the
  panel's existing session auth. It is not a separate web app and has no nav entry of its own —
  a Volume is meaningless without its Compose as context.
- Document flow, no nested scrollers (`docs/GUIDELINES.md` §6): the file list, the editor, and
  the upload panel all run long on the page.
- Monaco is the editor, reused from the Compose YAML screen — not a second editor dependency.
- An absolute bind source outside the data directory gets a confirmation before its first
  operation, even though the operator declared it.
- **UI work happens directly in `web/`**, following `docs/GUIDELINES.md`. The clickable mock was
  retired per **ADR-0009** and lives at the annotated tag `design-mock` — the visual reference for
  the Explorer's screens and states, not a gate on implementation. Ticket 01 is closed by this
  re-plan.

## Security decisions

1. `--network none` — no ingress, no egress. No proxy, no token, no new network, no ADR-0002 change.
2. `--cap-drop ALL`, `--security-opt no-new-privileges`, `--read-only` rootfs, `--tmpfs /tmp`.
3. Exactly one mount: the declared source. Never the Docker socket, never another host path.
4. `--user` always explicit; root opt-in with a warning.
5. `:ro` unless the operation writes. Enforced at the mount flag, not in the UI — the kernel is the
   enforcer.
6. Paths as argv with `--`, relative-only, plus `os.Root` in the helper as defense in depth.
7. The caps above, and the free-space check before an upload is accepted.
8. **Audit rows, deferred.** v1 is a single admin and no viewer screen exists, so the rows — who,
   when, operation, path, bytes — have nowhere to be read yet; recording them later is additive.
9. The README must state that this feature assumes TLS on the panel's domain. A volume file manager
   over plain HTTP is a data-exfiltration channel.
10. The panel must be running in a container for this feature (it needs the panel image on the host
    and the Docker socket). Hide the tab when running as a bare binary.

## Out of scope

- **Zip, in either direction.** tar.gz is the only archive format: a zip restore needs random access
  (its central directory sits at the end of the stream), so it cannot be fed to the streaming
  `extract`, and zip for download only would leave one format with no restore story.
- **Archive upload of arbitrary non-tar formats.** Plain tar, 7z, rar: `extract` accepts a gzipped
  tar stream only.
- **Deleting a Volume.** Irreversible, and the Compose delete action already covers teardown.
- **A permissions editor** (chmod/chown). Note also that `os.Root.Chmod`, `Chown`, and `Chtimes` are
  documented as racy on Unix — this would need care beyond what `os.Root` gives.
- Search, image/media previews, bulk multi-select, drag-and-drop move.
- Any path not declared in a Compose — the mount source comes only from the parsed Compose output;
  no input field accepts a host path (the invariant above).
- Resumable or `Range` downloads.
- Multi-host.
- **An audit viewer.** The audit rows are deferred entirely in v1 (Security decisions 8).

## Tickets

`.scratch/volume-files/issues/`, each with its blocking edges:

| | ticket | blocked by |
| --- | --- | --- |
| 01 | closed — design review gate is moot after ADR-0009; review happens in web/ during implementation, against tag `design-mock` | — |
| 02 | `volfs`, the tar.gz writer, and the `relist-volhelper` binary | — |
| 03 | Resolve a Compose's Volumes, uid/gid, and pre-flight | — |
| 04 | closed — replaced by 13; no persistent container, so no lifecycle | — |
| 05 | Read endpoints and backup download, one run per operation | 02, 03, 13 |
| 06 | closed — merged into 05; tar.gz only | — |
| 07 | Volumes tab and Volume Explorer — the read-only slice | 05 |
| 08 | Write operations — upload, edit, delete (API and UI) | 02, 03, 13, 05 |
| 09 | closed — caps live in 08 (upload/free space) and 12 (restore) | — |
| 10 | closed — no dashboard in this model; audit rows deferred | — |
| 11 | Docs: README and ADR cross-references | 07, 08, 12 |
| 12 | Restore: streamed tar.gz extraction (helper, endpoint, UI) | 02, 03, 13, 05 |
| 13 | The `docker run --rm` runner seam and its test fake | — |

**Ticket 13 is the cheapest first landing** — no UI, no Docker in tests, so the runner and its fake
can land while everything else is still open. **05 + 07 together are the tracer bullet**: the first
point at which an operator can list their Compose's Volumes, browse, and download a backup end to
end. **12 is last because it is the security-heaviest** — extraction is the sharpest edge in the
feature. The old rule — nothing starts until the design pass is approved — no longer applies: ticket
01 is closed by this re-plan.

## Testing decisions

The repo's only test seam is the HTTP API, and it stays that way here.

- The panel's side of the protocol is tested through HTTP requests, with `docker` runs faked at the
  runner seam the same way `ComposeRunner` is faked today — the fake records the argv it was asked
  to run.
- `internal/volfs` is a real package with real tests against a real temp directory: traversal (`..`,
  absolute, encoded), symlinks pointing out of the root and inside it, newline-bearing filenames,
  and the archive entry names.
- Extraction validation is tested against malicious archives: `..` entries, absolute entries,
  symlink and hardlink entries, and the entry-count and uncompressed-size caps.
- Archive tests round-trip tar.gz only: produce with our writer, read back with `archive/tar` and
  `compress/gzip`, assert every entry name is relative and contains no `..` segment, and assert a
  symlink survives as a symlink.
- No real Docker in tests.
