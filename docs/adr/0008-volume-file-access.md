# Volume file access runs in a panel-made helper container, off every network

**Revised by ADR-0010 (2026-09-29).** The decision below — a long-lived helper container per Volume
Handle, driven by one `docker exec` per operation — is replaced by one `docker run --rm` per
operation. This ADR's measurements and rejected options stay as written: they are the evidence base
the revision argues from, and the container hardening and helper design carry over unchanged.

Browsing, editing, and downloading the files of a Compose's Volumes needs a process that can *see*
those files. The panel cannot: it runs in a container that mounts only the Docker socket and the
data directory, so a volume's contents at `/var/lib/docker/volumes/<name>/_data` on the host are
unreachable to it. Every route to those bytes goes through the Docker socket, which means a
container.

**Decision:** the panel creates that container itself, one per open **Volume Handle**, and drives
it with one `docker exec` per operation. The container mounts exactly one Volume and nothing else,
runs `--network none` with `--cap-drop ALL`, `--read-only` rootfs and an explicit `--user`, and
runs a second binary built from the panel's own image (`relist-volhelper`). The helper does the
filesystem work itself — `os.Root` for path containment, `os.ReadDir` for names, `archive/tar`,
`archive/zip` and `compress/gzip` for archives, all stdlib — rather than shelling out to the
image's own `ls`/`cp`/`tar`.

`--network none` is the load-bearing choice. Because the helper has no ingress and no egress, this
feature needs no reverse proxy, no shared token, no second authentication system, and no new Docker
network — so ADR-0002's install bootstrap is unchanged. It also means a path that somehow escapes
`os.Root` lands in a throwaway, read-only container filesystem rather than on the host.

## Considered options

**An off-the-shelf file manager** (filebrowser, dufs), proxied through the panel. Measured
2026-09-24: `filebrowser` idles at 14.73 MiB RSS — as much as the whole panel (14.35 MB) — and
`dufs` at 1.65 MiB. Rejected because neither produces tar.gz at all (`dufs` serves folder archives
as zip only; `filebrowser` ships zip), because each is a second HTTP server with a second session
whose logout the panel does not control, and because each needs a network path *to* it — the one
risk this design removes.

**One `docker run --rm` per operation.** Rejected on measurement: 365–594 ms per operation against
33 ms for an exec in an already-running container, with a 2.11 s outlier that would read as a hang.
The hardening flags and the volume mount add nothing measurable.

**Shelling out to the image's own tools.** Rejected on three verified counts: the panel's
`docker:27-cli` base is Alpine, where `tar` is busybox and **`zip` is not installed at all**, so the
requirement cannot be met; `tar -czf - -C /data -- /etc/passwd` exits **0** while archiving the
*container's* `/etc/passwd`, returning a file the operator did not ask for and reporting success;
and `ls` renders a file genuinely named `a\nb.txt` as `a?b.txt`, a name that does not exist.

**`docker cp`.** Rejected as not a shortcut: it needs a container with the volume mounted anyway,
copies only whole trees (no list, stat, edit, mkdir, or rename), emits its own tar, and carries a
history of symlink-race vulnerabilities from resolving paths in the daemon.

## Consequences

- **ADR-0002's boundary widens.** That ADR says the panel drives the host by shelling out to the
  `docker compose` CLI plugin. It now also creates and manages plain containers of its own, outside
  any Compose project. Those are invisible to `docker compose down` on the install stack, so the
  panel owns their whole lifecycle — including stopping any that outlive a panel restart.
- **ADR-0003's "exactly one port/adapter pair in the whole system" becomes two.** The helper's
  operations are the second thing worth faking in tests.
- **The panel needs its own image name** to start a helper. It resolves it from its own container
  (`docker inspect $(hostname)` → `.Config.Image`), with `RELIST_IMAGE` as the escape hatch, and
  the feature is unavailable when the panel runs as a bare binary.
- **One image, two binaries.** Shipping the helper inside the panel's image means the host already
  has it after `docker compose build` — no registry, no runtime pull, and no version skew between
  panel and helper (which matters because they speak a private argv protocol).
