# Spec — Volume files: browse, edit, and download what a Compose mounts

## Problem

The panel deploys Compose stacks but gives the operator no way to look inside the data those stacks
leave behind. A named volume or bind mount holding a database, an uploaded corpus, or a botched
config file is only reachable by SSHing in and running `docker run -v ... alpine tar`. The Compose
delete action (compose-panel ticket 11) can *remove* a Compose's volumes; nothing can read them.

The operator wants: on a Compose's page, a tab listing **only the Volumes that Compose mounts**; a
file manager over one of them — browse, edit, upload, rename, delete; and download of a directory as
**tar.gz or zip**. It must match the panel's Ant Design theme, and it must not make the panel heavy.
The reference point is Dokploy, whose panel idles at 1–2 GB RSS, which is what makes small VPSs
painful.

Vocabulary is `CONTEXT.md`'s: a **Volume** is one entry in a Compose's `volumes:` list (named volume
or bind mount — the panel treats both alike), a **Volume Handle** is the panel's temporary grant of
access to one Volume, and the **Volume Explorer** is the screen.

## Decision

Build the file manager ourselves, in two pieces:

- A **thin, network-less helper container** that mounts exactly one Volume and speaks a small
  argv/stdio protocol. No HTTP server, no listening socket.
- The **UI inside the panel's existing SPA** — Ant Design components and the Monaco editor the panel
  already ships (`web/src/monaco.ts`).

The panel drives the helper with `docker exec`, one exec per operation, streaming file bytes over
the exec's stdin/stdout. See **ADR-0008** for the architectural decision and its rejected
alternatives.

### Why build it rather than embed an off-the-shelf file manager

1. **Nothing off the shelf does tar.gz.** Measured: `sigoden/dufs` serves folder archives as zip
   only — `?zip` returns `content-type: application/zip` and
   `content-disposition: attachment; filename="data.zip"`, while `?tar` and `?tgz` fall through to
   the HTML listing. `filebrowser` ships zip. tar.gz is a stated requirement.
2. **Any off-the-shelf file manager is a second HTTP server with a second auth system** — a login
   session the panel's logout does not end, and a port that has to be reachable from the panel.
   That reachability is the single risk worth engineering away, and it is the one thing a
   third-party file manager cannot avoid.
3. **Weight.** The lightest measured option is still about as large as the entire panel.

### Measured (this host, 2026-09-24)

| | RSS idle | tar.gz | zip | Network surface | New dependency |
| --- | --- | --- | --- | --- | --- |
| `filebrowser/filebrowser:latest` | 14.73 MiB | ✗ | ✓ | HTTP server, must be proxied | yes |
| `sigoden/dufs:latest` | 1.65 MiB | ✗ (zip only) | ✓ | HTTP server, must be proxied | yes |
| Panel binary, idle → after one request | 14.35 MB → 15.42 MB | — | — | — | — |
| Minimal Go helper (stdin loop, no HTTP) | **2.21 MB** | ✓ stdlib | ✓ stdlib | **none** | none |

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

- **The persistent-container model is settled by measurement, not preference.** One exec is 33 ms;
  one `docker run` is 365–594 ms, with a 2.1 s outlier — a 10x penalty on every directory listing,
  and an outlier that would read as a hang. The hardening flags (`--cap-drop ALL`, `--read-only`,
  `--security-opt no-new-privileges`) and the Volume mount add nothing measurable (0.365 s vs
  0.373 s), so they cost nothing to include.
- **The helper must die on SIGTERM without trapping it.** `docker stop` costs 10.1 s when pid1
  ignores SIGTERM and 1.1 s when it handles it. A Go binary as pid1 dies on SIGTERM by default, so
  the rule is: install no signal handler, and Stop stays fast.
- **`docker compose config` is cheap enough to run per page load** (30 ms) — no caching needed.

### Mounting creates whatever is missing

Verified two ways: `docker run -v <nonexistent-volume>:/data` **creates the volume**, and
`docker run -v <nonexistent-host-path>:/data` **creates that directory as root:root 0755**. Opening
a browse view would therefore mutate the host unless something stops it. See **Pre-flight** below.

## The helper

### Run spec

```
docker run -d --rm \
  --name relist-vol-<handle> \
  --label relist.handle=<handle> --label relist.compose=<compose-id> \
  --network none \
  --user <uid>:<gid> \
  --read-only --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -v <resolved source>:/data[:ro] \
  --entrypoint /usr/local/bin/relist-volhelper \
  <panel image> idle
```

Every flag is load-bearing:

- `--network none` — no ingress, no egress. This is why the feature needs no proxy, no shared token,
  no new Docker network, and no change to the ADR-0002 bootstrap.
- **Exactly one mount**, the declared source, at `/data`. Never the Docker socket, never another
  host path.
- `--user` always explicit. Root is a deliberate opt-in, not a default (see UID/GID).
- `:ro` unless the handle was explicitly opened for writing.
- `--read-only` rootfs plus `--tmpfs /tmp` — so nothing can stage a copy of the data, including the
  helper itself.
- The labels are how every live handle is found on panel start (see Lifecycle).

The image is the panel's own, so the host already has it from `docker compose build` — no registry,
no runtime pull, no version skew between panel and helper. The panel resolves its own image name
with `docker inspect $(hostname)` → `.Config.Image` (Docker sets hostname to the container ID by
default), falling back to a `RELIST_IMAGE` env var.

### Protocol

One `docker exec` per operation, path as an argv element, `--` before any path so a name starting
with `-` cannot be read as a flag:

```
docker exec -i relist-vol-<handle> relist-volhelper list    -- <relpath>
docker exec -i relist-vol-<handle> relist-volhelper stat    -- <relpath>
docker exec -i relist-vol-<handle> relist-volhelper read    -- <relpath>     # bytes on stdout
docker exec -i relist-vol-<handle> relist-volhelper write   -- <relpath>     # bytes on stdin
docker exec -i relist-vol-<handle> relist-volhelper mkdir   -- <relpath>
docker exec -i relist-vol-<handle> relist-volhelper rename  -- <relpath> <relpath>
docker exec -i relist-vol-<handle> relist-volhelper delete  -- <relpath>
docker exec -i relist-vol-<handle> relist-volhelper archive --format tar.gz|zip -- <relpath>
```

Paths are **relative to `/data`**, always. The helper rejects absolute paths outright. Metadata
operations return JSON on stdout; `read` and `archive` return raw bytes.

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

`docker exec` is the **transport**; the helper binary is the **implementation**. These are two
layers, not two competing designs. The alternative — each exec running `ls`, `cat`, `cp`, or `tar`
inside the container, the way a hand-rolled `docker run alpine tar` recipe does — was tested against
this image and rejected on three counts:

- **It cannot produce zip at all.** The panel's `docker:27-cli` base is Alpine: `tar` is **busybox
  1.37.0** and `zip` is **not installed** (only `unzip` is). Shelling out cannot meet the
  requirement.
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

Because mounting creates what is missing, the handle is refused before it can mutate the host:

- **Named volume**: refuse unless `docker volume inspect` finds it (10 ms, exact).
- **Bind source inside the data directory**: the panel can `os.Stat` it directly (identical-path
  mount, ADR-0002), so refuse if absent.
- **Bind source outside the data directory**: the panel cannot stat it, and probing it with a mount
  would create it. The start dialog states plainly that the path does not exist or cannot be
  checked, and that continuing will create it as `root:root 0755`, and the operator decides.

## UID/GID

Default resolution order, first hit wins:

1. The resolved `user:` for a service the operator picks, from `docker compose config` (verified
   present in that output).
2. `docker inspect` the service's container `.Config.User`.
3. `stat -c '%u:%g' /data` from a root helper run — the Volume root's owner.
4. `0:0`, opt-in only, with a warning.

Step 2 is deliberately `docker inspect` rather than "exec into the container and run `id`". Inspect
needs no `exec` binary in the target, works on distroless and scratch images, works when the
container is stopped, and cannot be blocked by the target's own security policy. The exec-based
button is strictly worse on every axis.

The UI shows the resolved value and lets the operator override it. Choosing root carries a visible
warning: files it creates become root-owned, and a stack that runs as uid 999 may then fail to
start.

## Archives

Both formats are **stdlib only** — `archive/tar`, `archive/zip`, `compress/gzip`, no external
module. Both write to a non-seekable writer, verified end to end: a tree of three files (a 1 MB
random binary, a 4 KB nested file, a text file) was streamed through an `io.Pipe` — the same shape as
an HTTP response body — and read back out of that pipe, all three entries byte-identical by sha256 in
both formats. Nested paths survive as relative, slash-separated names (`sub/nested.bin`).

The archive is produced by the helper and piped straight through the exec's stdout into the HTTP
response. Nothing is staged on the panel's disk, so a 50 GB Volume never becomes a 50 GB file in the
data directory.

Rules:

- **Entry names are set explicitly.** `tar.FileInfoHeader` fills in only the base name; it must be
  overwritten with the path relative to the Volume root, slash-separated, never absolute. A leading
  `/` or `..` in an entry name is how a *download* becomes a zip-slip write on whoever extracts it.
- **Symlinks are stored as symlinks**, faithfully. The alternative silently produces an archive that
  differs from the Volume, and a restore from it loses data without saying so. Because the tree is
  walked anyway, the Explorer can state before the download how many symlinks the archive will hold
  and how many of them point outside the Volume — the extraction-time hazard is disclosed rather
  than hidden. The fix for that hazard belongs at extraction, not in whether we record the data
  honestly.
- **Hardlinks are written as regular files**, never as hardlink entries. That sidesteps the hardlink
  tar-slip vector entirely at the cost of a larger archive, and Go's `FileInfoHeader` cannot express
  a hardlink target anyway.
- **`Content-Disposition` follows RFC 6266**: control characters stripped, and `filename*` with
  percent-encoding for any non-ASCII name.
- A single file and a directory are both downloadable; a single file is served as a plain download,
  not wrapped in an archive.

Both formats must be fully received before extraction (zip's central directory is at the end; gzip
must be decompressed sequentially), so neither can show a size-based progress bar. That is inherent
to streaming, not a bug. Resumable downloads would need a staged file with `Range` support, which
costs disk and is out of scope.

## Lifecycle

- **Start is explicit.** Opening the Volume Explorer does not auto-start a handle; the operator
  presses Start.
- **Stop is explicit** — a button on the Compose's Volumes tab and in the dashboard list.
- **Idle auto-stop**: on by default at **30 minutes**, measured from that handle's last HTTP
  request, configurable via `RELIST_VOLUME_IDLE_TIMEOUT`, `0` disables it. Relying on the operator to
  remember is not a control, and this is what actually delivers "does not eat resources". The
  dashboard shows the remaining time counting down so a handle never vanishes unannounced. Closing
  the browser tab does **not** count as idle: the panel has no websocket and no heartbeat by scope,
  so a closed tab is indistinguishable from an idle one.
- **Dashboard warning.** A banner listing live handles, plus a badge on the Volumes tab. The warning
  is about exposure as much as resources: a live handle is a container holding a Volume that exists
  because someone opened it and did not close it.
- **Panel start stops every handle.** Any container carrying the `relist.handle` label is stopped on
  startup; nothing is adopted. Adopting would have to reason about a handle created by the *previous*
  build — after an upgrade the panel could exec an operation the older helper does not implement,
  which is a bug that only ever appears during an upgrade. Stopping is one unambiguous rule, and
  reopening costs one click.
- **Compose delete stops its handles first.** A live helper holding the Volume makes `down -v` fail
  or silently leave the Volume behind. Order: stop handles → `down -v` → delete files.
- **A Compose edit that removes a Volume** stops that Volume's handle, since its source no longer
  exists in the resolved list.
- **One handle per Volume.** Two writers on one Volume is a corruption path. Pressing Open on a
  Volume that already has a live handle focuses that handle rather than creating a second.
- **Warn when the stack is running and allow the write anyway.** Editing files under a live database
  can corrupt it, and the Compose's status is already known on that page. The write is deliberately
  **not** gated behind an acknowledgement, so the banner is the only control here and must be built
  to be read rather than dismissed: it names the consequence and the affected Services, not "be
  careful". The legitimate case it protects is real — fixing a mounted config file while the stack
  runs, then restarting it.

## v1 operation set

Read: list a directory (name, size, mtime, owner uid/gid, mode, kind); `stat` one entry; download a
single file; download a directory as an archive.

Write: upload a file into the current directory; create an empty text file; edit a text file and
save (Monaco); create a directory; rename or move an entry; delete a file or a whole directory tree.

Deliberately **not** in v1: copy/duplicate, a chmod/chown editor, search, image or media preview,
multi-select bulk operations, drag-and-drop move, symlink creation, and downloading a *selection* as
an archive (v1 archives whole directories only). These are all additions to a per-operation API
rather than rework, so adding one later is additive.

Delete is permanent, with a `Popconfirm` naming the path — no trash. A hidden trash directory inside
the Volume would be visible to the application using it, which is worse than the operator having to
mean it.

## Caps

Hard-coded, no env knobs. They exist to protect the small VPS this feature is sized for, and a knob
is an invitation to turn them off:

| | limit |
| --- | --- |
| upload | 1 GB, and refused if it exceeds the free space on the Volume's filesystem (the helper checks `statfs`) |
| editable text file | 1 MB |
| archive | 10 minute timeout on the stream |

The free-space check is the part no OS default provides, and a full disk is a host-wide failure, not
a panel one.

## What the Explorer shows

The Explorer shows resolved paths — the host path for a bind source, and
`/var/lib/docker/volumes/<real>/_data` for a named Volume. The operator owns the host.

## Screen rules

- The Volumes tab lists only the Volumes that Compose mounts, with kind, source, target, and the
  container-side path each Service uses.
- The Volume Explorer opens in a new browser tab, as its own route in the panel SPA, behind the
  panel's existing session auth. It is not a separate web app and has no nav entry of its own —
  a Volume is meaningless without its Compose as context.
- Document flow, no nested scrollers (`web-design/GUIDELINES.md` §6): the file list, the editor, and
  the upload panel all run long on the page.
- Monaco is the editor, reused from the Compose YAML screen — not a second editor dependency.
- An absolute bind source outside the data directory gets a confirmation before its first handle,
  even though the operator declared it.
- **The design pass comes first and is the visual contract.** Ticket 01 extends
  `web-design/src/pages/ComposePage.tsx` (currently two tabs, General and Logs) to three, adds the
  Volume Explorer page and its route, adds the handle surfaces to the Overview, and renders every
  state this feature can be in — including the ones that carry the safety story, like the
  running-stack write banner and the archive's symlink disclosure. Implementation follows the
  approved mock screen by screen rather than reinterpreting it.

## Security decisions

1. `--network none` — no ingress, no egress. No proxy, no token, no new network, no ADR-0002 change.
2. `--cap-drop ALL`, `--security-opt no-new-privileges`, `--read-only` rootfs, `--tmpfs /tmp`.
3. Exactly one mount: the declared source. Never the Docker socket, never another host path.
4. `--user` always explicit; root opt-in with a warning.
5. `:ro` unless the handle was explicitly opened for writing. Enforced at the mount flag, not in the
   UI — the kernel is the enforcer.
6. Paths as argv with `--`, relative-only, plus `os.Root` in the helper as defense in depth.
7. The caps above, and the free-space check before an upload is accepted.
8. **Audit rows only, no viewer in v1.** Start, stop, write, delete and archive each append a row:
   who, when, handle, path, bytes. Not logging loses that history irrecoverably, while not displaying
   it only defers a read of data already held — so the rows go in now and the screen comes if anyone
   asks. The live view is the dashboard banner.
9. The README must state that this feature assumes TLS on the panel's domain. A volume file manager
   over plain HTTP is a data-exfiltration channel.
10. The panel must be running in a container for this feature (it needs the panel image on the host
    and the Docker socket). Hide the tab when running as a bare binary.

## Out of scope

- **Uploading or restoring an archive.** tar extraction is the sharpest edge in the feature: `../`
  entries, symlinks, and hardlinks can all write outside the destination. It needs its own threat
  model. There is also a second, independent reason it is harder: reading a zip needs random access,
  so the restore direction cannot stream the way the download direction does.
- **Deleting a Volume.** Irreversible, and the Compose delete action already covers teardown.
- **A permissions editor** (chmod/chown). Note also that `os.Root.Chmod`, `Chown`, and `Chtimes` are
  documented as racy on Unix — this would need care beyond what `os.Root` gives.
- Search, image/media previews, bulk multi-select, drag-and-drop move.
- Handles on any path not declared in a Compose — forbidden by the invariant above.
- Resumable or `Range` downloads.
- Multi-host.

## Tickets

`.scratch/volume-files/issues/`, each with its blocking edges:

| | ticket | blocked by |
| --- | --- | --- |
| 01 | Design pass: Volumes tab, Volume Explorer, handle surfaces | — |
| 02 | `volfs` package and the `relist-volhelper` binary | 01 |
| 03 | Resolve a Compose's Volumes, and the UID/GID chain | 01 |
| 04 | Handle lifecycle and the container run spec | 01, 02 |
| 05 | Read endpoints over `docker exec` | 01, 03, 04 |
| 06 | Archive download | 01, 05 |
| 07 | Volumes tab and Volume Explorer — the read-only slice | 01, 03, 05, 06 |
| 08 | Write operations | 01, 05 |
| 09 | Caps and the free-space check | 01, 08 |
| 10 | Dashboard banner and audit rows | 01, 04, 08 |
| 11 | Docs: README and ADR cross-references | 01, 07 |

**The work starts with the design pass, and nothing else starts until it is approved.** Every ticket
after 01 carries `01` as a blocker, and that edge is a *process* gate rather than a technical one —
matching how `web-design/` already works ("meant to be reviewed screen by screen, agreed on, and then
implemented in `../web` as a separate piece of work"). A design review regularly changes decisions
that cascade back into this spec, and finding that out on a mock costs a fraction of finding it out
in Go. The consequences worth naming: tickets 05 and 08 both depend on 03/04 for real technical
reasons, so those edges stay regardless. But the four tickets that touch no UI at all (02, 03, 04,
05) do not technically depend on the visual design at all — if the review is going to take a while,
dropping `01` from those four edges lets the backend run in parallel.

Ticket 07 is the tracer bullet: it is the first point at which an operator can see and download their
data end to end. Everything risky lives at or before it — whether the panel can start a helper from
its own image, whether the argv/stdio protocol carries bytes both ways, whether `docker compose
config` holds up on real Composes, whether the archive streams. After 07 the remaining work is
ordinary Go and React.

## Testing decisions

The repo's only test seam is the HTTP API, and it stays that way here.

- The panel's side of the protocol is tested through HTTP requests, with exec calls faked the same
  way `ComposeRunner` is faked today — the fake records the argv it was asked to run.
- `internal/volfs` is a real package with real tests against a real temp directory: traversal (`..`,
  absolute, encoded), symlinks pointing out of the root, symlinks pointing inside it, newline-bearing
  filenames, and the archive entry names.
- Archive tests round-trip: produce with our writer, read back with `archive/tar` and `archive/zip`,
  assert every entry name is relative and contains no `..` segment, and assert a symlink survives as
  a symlink.
- No real Docker in tests.
