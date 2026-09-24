# Spec — Volume files (browse, edit, download the volumes a Compose mounts)

## Problem

The panel deploys Compose stacks but gives the operator no way to look inside the data those
stacks leave behind. A named volume or bind mount holding a database, an uploaded corpus, or a
botched config file is only reachable by SSHing in and running `docker run -v ... alpine tar`.
Ticket 11 can *delete* a Compose's volumes; nothing can read them.

The operator wants: on a Compose's page, a tab listing **only the volumes that Compose mounts**;
a file manager over one of them (browse, edit, upload, rename, delete); and download of a
directory as **tar.gz or zip**. It must match the panel's Ant Design theme, and it must not make
the panel heavy — the reference point is Dokploy, whose panel idles at 1–2 GB RSS, which is what
makes small VPSs painful.

## Decision

Build the file manager ourselves, in two pieces:

- A **thin, network-less helper container** that mounts exactly one volume and speaks a small
  argv/stdio protocol. No HTTP server, no listening socket.
- The **UI inside the panel's existing SPA** — Ant Design components and the Monaco editor the
  panel already ships (`web/src/monaco.ts`).

The panel drives the helper with `docker exec`, one exec per operation, streaming file bytes over
the exec's stdin/stdout.

### Why not embed an off-the-shelf file manager

Three reasons, in order of weight:

1. **Nothing off the shelf does tar.gz.** Measured 2026-09-24: `sigoden/dufs` serves folder
   archives as zip only — `?zip` returns `content-type: application/zip` and
   `content-disposition: attachment; filename="data.zip"`, while `?tar` and `?tgz` fall through to
   the HTML listing. `filebrowser` ships zip. tar.gz is a stated requirement.
2. **Any off-the-shelf file manager is a second HTTP server with a second auth system** — a
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
| `docker exec` round trip (10 runs) | 31 ms mean, 40 ms max | | | | |

Two corrections this table forces:

- `host-monitoring/spec.md` records the panel idling at **3.86 MB** RSS. Measured today it is
  **14.35 MB** (`VmRSS`; `VmHWM` agrees). The 1.87 GB `VmSize` is Go's virtual reservation, not
  memory in use. That figure needs re-measuring before it is relied on again.
- The 2.21 MB helper is a *minimal* binary. The real helper must be built as its own small binary
  (`cmd/relist-volhelper`) rather than as a subcommand of the panel binary, so it does not link
  `modernc.org/sqlite` and `net/http` and pay their init cost. Re-measure once built.

## The helper

### Run spec

```
docker run -d --rm \
  --name relist-vol-<session> \
  --label relist.session=<session> --label relist.compose=<compose-id> \
  --network none \
  --user <uid>:<gid> \
  --read-only --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -v <resolved source>:/data[:ro] \
  --entrypoint /usr/local/bin/relist-volhelper \
  <panel image> idle
```

Every flag there is load-bearing:

- `--network none` — no ingress, no egress. This is why the feature needs no proxy, no shared
  token, no new Docker network, and no change to the ADR-0002 bootstrap. It is the strongest
  isolation available and it costs nothing.
- **Exactly one mount**, the declared source, at `/data`. Never the Docker socket, never any other
  host path.
- `--user` always explicit. Root is a deliberate opt-in, not a default (see UID/GID below).
- `:ro` unless the session was explicitly opened for writing.
- `--read-only` rootfs plus `--tmpfs /tmp` — so nothing can stage a copy of the data, including the
  helper itself.
- The labels are how orphaned sessions are found after a panel restart (see Lifecycle).

The image is the panel's own image, so the host already has it from `docker compose build` — no
registry, no runtime pull, no version skew between panel and helper. The panel resolves its own
image name with `docker inspect $(hostname)` → `.Config.Image` (Docker sets hostname to the
container ID by default), falling back to a `RELIST_IMAGE` env var.

### Protocol

One `docker exec` per operation, path as an argv element, `--` before any path so a name starting
with `-` cannot be read as a flag:

```
docker exec -i relist-vol-<session> relist-volhelper list    -- <relpath>
docker exec -i relist-vol-<session> relist-volhelper stat    -- <relpath>
docker exec -i relist-vol-<session> relist-volhelper read    -- <relpath>     # bytes on stdout
docker exec -i relist-vol-<session> relist-volhelper write   -- <relpath>     # bytes on stdin
docker exec -i relist-vol-<session> relist-volhelper mkdir   -- <relpath>
docker exec -i relist-vol-<session> relist-volhelper rename  -- <relpath> <relpath>
docker exec -i relist-vol-<session> relist-volhelper delete  -- <relpath>
docker exec -i relist-vol-<session> relist-volhelper archive --format tar.gz|zip -- <relpath>
```

Paths are **relative to `/data`**, always. The helper rejects absolute paths outright. Metadata
operations return JSON on stdout; `read` and `archive` return raw bytes.

Every filesystem call in the helper goes through **`os.Root`** (`os.OpenRoot("/data")`), which
refuses to resolve a name outside the root and rejects absolute symlinks. This is defense in depth
*behind* the panel's own validation, and it is what makes a symlink planted inside a volume
harmless. `os.Root` in Go 1.27.1 has the full method set this needs: `Open`, `Create`, `OpenFile`,
`ReadFile`, `WriteFile`, `Stat`, `Lstat`, `Mkdir`, `MkdirAll`, `Remove`, `RemoveAll`, `Rename`,
`Link`, `Symlink`, `Readlink`, `Chmod`, `Chown`, `Lchown`, `Chtimes`.

**`go.mod` must move off `go 1.23.0`.** Verified: with a `go 1.23.0` directive, `go build` accepts
`os.Root` but `go vet` and `go test` both fail with `os.OpenRoot requires go1.24 or later`. Since
the HTTP seam is the repo's only test seam, this would break the suite. `go 1.24` minimum, `1.25`
preferred for the wider `Root` surface. The Dockerfile already builds with `golang:1.27-alpine`.

## Listing a Compose's volumes

Resolve them with the Compose CLI, not by parsing YAML:

```
docker compose -p <project> -f <compose file> --env-file <env file> config --format json
```

Verified on 2026-09-24 to resolve, in one call: named volumes (service-level `source: pgdata`
against a top-level `volumes.pgdata.name: demo_pgdata`), relative binds (`./rel` →
`/tmp/vmtest/cmptest/rel`, resolved against the Compose file's directory), absolute binds, `${VAR}`
interpolation (`${HOSTPATH:-/tmp/vmtest/srv}` → the default), and each service's resolved `user:`.
It also prunes volumes no service references, which is exactly the set this feature wants.

Reading `services.*.volumes[]` for `type` / `source` / `target` / `read_only` and the top-level
`volumes` map for real names gives both kinds:

| kind | shown as | mounted into the helper as |
| --- | --- | --- |
| `volume` | logical name + real name (`demo_pgdata`) | the real volume name |
| `bind` | the resolved absolute host path | that path |

**The one invariant that matters: a source path is never free text.** It comes only from the
parsed Compose output. The operator picks from a list; there is no input field that accepts a host
path. This is what keeps the feature's privilege at "exactly what the operator already declared in
a Compose the panel deploys" rather than "any path on the host".

Cost: one CLI call per page load (~100–200 ms). Cache it for the page.

If `config` fails — most likely because the Compose was never deployed, so the generated env file
does not exist — show the tab as unavailable with the CLI's own error. Do **not** fall back to a
hand-rolled YAML parse: that would reimplement Compose's path and interpolation semantics and get
them subtly wrong, and being subtly wrong about a path is how this feature becomes a
path-traversal bug.

## UID/GID

Default resolution order, first hit wins:

1. The resolved `user:` for a service the operator picks, from `docker compose config` (verified
   present in that output).
2. `docker inspect` the service's container `.Config.User`.
3. `stat -c '%u:%g' /data` from a root helper run — the volume root's owner.
4. `0:0`, opt-in only, with a warning.

Step 2 is deliberately `docker inspect` rather than "exec into the container and run `id`". Inspect
needs no `exec` binary in the target, works on distroless and scratch images, works when the
container is stopped, and cannot be blocked by the target's own security policy. The exec-based
button is strictly worse on every axis.

The UI shows the resolved value and lets the operator override it. Choosing root carries a visible
warning: files it creates become root-owned, and a stack that runs as uid 999 may then fail to
start.

## Streaming tar.gz and zip

Both formats are **stdlib only** and both stream to a non-seekable writer. Verified by piping each
through an `io.Pipe` (the same shape as an HTTP response body): `archive/tar` + `compress/gzip`
and `archive/zip` both completed with no seek and no error. No dependency, no temp file.

The archive is produced by the helper and piped straight through the exec's stdout into the HTTP
response. Nothing is staged on the panel's disk, so a 50 GB volume never becomes a 50 GB file in
the data directory.

Two things to get right:

- **Entry names must be set explicitly.** `tar.FileInfoHeader` fills in only the base name; the
  name must be overwritten with the path relative to the volume root, slash-separated, and never
  absolute. A leading `/` or `..` in an entry name is how a *download* turns into a zip-slip write
  on whoever extracts it. This needs its own test.
- **The `Content-Disposition` filename is derived from operator-controlled text** (the volume or
  prefix name) and must be sanitized.

Both formats must be fully received before extraction (zip's central directory is at the end;
gzip must be decompressed sequentially), so neither can show a size-based progress bar. That is
inherent to streaming, not a bug. Resumable downloads would need a staged file with `Range`
support, which costs disk and is out of scope.

## Lifecycle

- **Start is explicit.** Opening the file manager page does not auto-start a session; the operator
  presses Start, per the requested design.
- **Stop is explicit** — a button on the Compose's volume tab and in the dashboard list.
- **Idle auto-stop** (default 30 min, configurable) as well. Relying on the operator to remember
  is not a control, and this is the part that actually delivers "does not eat resources".
- **Dashboard warning.** A banner listing live sessions, plus a badge on the volume tab. The
  warning is about exposure as much as resources: a live session is a container holding a volume
  that exists because someone opened it and did not close it.
- **Orphan reconciliation on panel start.** List containers carrying the `relist.session` label;
  adopt the ones with a matching DB row, stop the rest. Without this, a panel restart leaks
  containers forever.
- **Compose delete must stop its sessions first.** Ticket 11 runs `down -v`; a live helper holding
  the volume makes the volume removal fail or silently leave the volume behind. Order: stop
  sessions → `down -v` → delete files.
- **Compose edit that removes a volume** must stop that volume's session, since its source no
  longer exists in the resolved list.
- **One session per volume.** Two writers on one volume is a corruption path.
- **Warn when the stack is running.** Editing files under a live database can corrupt it; the
  Compose's status is already known on that page.

## Screen rules

- The volume tab lists only the volumes that Compose mounts, with kind, source, target, and the
  container-side path each service uses.
- The file manager opens in a new browser tab, as its own route in the panel SPA, behind the
  panel's existing session auth. It is not a separate web app.
- Document flow, no nested scrollers (per `web-design/GUIDELINES.md` §6) — the file list, the
  editor, and the upload panel all run long on the page.
- Monaco is the editor, reused from the Compose YAML screen, not a second editor dependency.
- An absolute bind source outside the data directory gets a confirmation prompt before its first
  session, even though the operator declared it.
- The design draft is `web-design/src/pages/ComposePage.tsx` (currently two tabs, General and
  Logs); it becomes three. The spec's "exactly two tabs" line changes with it.

## Security decisions

1. `--network none` — the helper has no ingress and no egress. No proxy, no token, no new network,
   no ADR-0002 bootstrap change.
2. `--cap-drop ALL`, `--security-opt no-new-privileges`, `--read-only` rootfs, `--tmpfs /tmp`.
3. Exactly one mount: the declared source. Never the Docker socket, never another host path.
4. `--user` always explicit; root opt-in with a warning.
5. `:ro` by default; `rw` only for a session explicitly opened for writing.
6. Paths as argv with `--`, relative-only, plus `os.Root` inside the helper as defense in depth.
7. Caps: max upload size, max editable file size, archive timeout, and a free-disk check before an
   upload is accepted.
8. Audit rows for start, stop, write, delete, and archive: who, when, session, path, bytes.
9. The README must state that this feature assumes TLS on the panel's domain. A volume file
   manager over plain HTTP is a data-exfiltration channel.
10. The panel must be running in a container for this feature (it needs the panel image on the
    host and the Docker socket). Hide the tab when running as a bare binary.

An ADR is required: this adds a second runner-style port beside `ComposeRunner` (ADR-0003's
"exactly one interface" line changes) and a new execution mode — containers the panel creates
itself, outside `docker compose`, which ADR-0002 does not cover. Next free number is **0006**.

## Out of scope

- **Uploading / restoring an archive.** tar extraction is the sharpest edge in the whole feature:
  `../` entries, symlinks, and hardlinks can all write outside the destination. It needs its own
  design and its own threat model.
- **Deleting a volume.** Irreversible, and ticket 11 already covers teardown.
- **A permissions editor** (chmod/chown). Note also that `os.Root.Chmod`, `Chown`, and `Chtimes`
  are documented as racy on Unix — if this is ever built, it needs care beyond what `os.Root`
  gives.
- Search, image/media previews, bulk multi-select operations.
- Sessions on any path not declared in a Compose — forbidden by the invariant above.
- Resumable or `Range` downloads.
- Multi-host.

## Tickets

1. `internal/volfs` — `os.Root`-based path-safe filesystem operations, and `cmd/relist-volhelper`
   with the argv/stdio protocol. Bump `go.mod` to 1.24+. Unit tests over the protocol.
2. Helper image and run spec: second binary in the panel image, the `docker run` flags above,
   session labels, self-image resolution.
3. Session lifecycle: SQLite table, start/stop, idle auto-stop, orphan reconciliation on startup,
   one-session-per-volume.
4. Resolve a Compose's volumes and binds via `docker compose config`, and the HTTP endpoint that
   serves them.
5. UID/GID resolution chain and the override UI.
6. File-operation HTTP endpoints, proxied to `docker exec` (list, stat, read, write, mkdir,
   rename, delete).
7. Archive download: streamed tar.gz and zip, with the entry-name and filename-sanitization tests.
8. Frontend: the volume tab on the Compose page and the file manager screen in Ant Design +
   Monaco, including the design-draft update.
9. Dashboard warning banner and the audit log.
10. ADR-0006 and the README notes (TLS requirement, containerized-panel requirement).

## Testing decisions

The repo's only test seam is the HTTP API, and it stays that way here.

- The panel's side of the protocol is tested through HTTP requests, with the exec calls faked the
  same way `ComposeRunner` is faked today — the fake records the argv it was asked to run.
- `internal/volfs` is a real package with real tests against a real temp directory: path traversal
  (`..`, absolute, encoded), symlinks pointing out of the root, symlinks pointing inside the root,
  and the archive entry names.
- Archive tests must round-trip: produce with our writer, read back with `archive/tar` and
  `archive/zip`, and assert every entry name is relative and contains no `..` segment.
- No real Docker in tests.

## Open decisions

- Idle auto-stop default (proposed 30 min) and whether it is per-install config or per-session.
- Whether write access needs a global env flag as well as a per-session toggle, or the per-session
  toggle alone is enough.
- Whether `archive` should accept a single file, or directories only.
