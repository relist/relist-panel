# Volume file access is one `docker run --rm` per operation

Browsing, editing, backing up and restoring the files of a Compose's Volumes needs a process that can
*see* those files. The panel cannot: it runs in a container that mounts only the Docker socket and the
data directory, so a Volume's contents are unreachable to it. Every route to those bytes goes through
the Docker socket, which means a container (ADR-0008). This ADR revises how long that container lives.

**Decision:** every operation — list, stat, read, write, delete, archive, extract — is its own
`docker run --rm` invocation, with the helper binary (`relist-volhelper`) as the container's
entrypoint, driven over argv/stdin/stdout (`-i`, never `-t`), mounting exactly one Volume — the
declared source at `/data` — with `--user` passed per operation, `:ro` for read operations and `:rw`
for write operations. The container exits and removes itself with the operation. There is no
persistent helper and no Volume Handle lifecycle to build: no start/stop controls, no idle timeout,
no dashboard banner or badge, no labels, no reconciliation at panel start, no one-handle-per-volume
rule, and no ordering against Compose delete.

Everything else in ADR-0008's container spec carries over unchanged: `--network none`, `--cap-drop
ALL`, `--security-opt no-new-privileges`, `--read-only` rootfs, `--tmpfs /tmp`, exactly one mount
(never the Docker socket, never another host path), an explicit `--user`, `os.Root` path containment
in the helper, the helper shipped inside the panel's own image (no registry, no pull, no version
skew), and image resolution via `docker inspect $(hostname)` → `.Config.Image` with `RELIST_IMAGE` as
the escape hatch.

**Restore is now in scope**, where ADR-0008 had it out of scope. A tar.gz is streamed on stdin into
`extract`, and each entry is validated before it is written: relative names only, no `..`, no
absolute paths; symlink and hardlink entries are refused. Partial extraction is possible if a stream
fails mid-way and is disclosed, not hidden. Caps: compressed ≤ 1 GiB, uncompressed ≤ 4 GiB, ≤ 10k
entries. **tar.gz is the only format, in both directions** — zip is dropped from restore and from
download, because a zip restore needs random access while this design streams.

## Considered options

**A long-lived helper container per Volume Handle, driven with `docker exec`.** ADR-0008's choice,
and the measurement it rejected the per-operation run on still stands: an exec in an already-running
container costs 33 ms, against 365–594 ms for `docker run --rm`, with a 2.11 s outlier. This ADR
reverses that call by accepting the measurement rather than disputing it, because the accepted
workload changed. This feature is rare operations — backup, an occasional config edit, restore — not
interactive file management, so 0.4–0.6 s per operation is an acceptable price for zero idle cost, no
standing state, a strictly smaller exposure window, no upgrade version-skew, and a much smaller
ticket surface. Every operation cleans itself up (`--rm`).

**Keeping zip in either direction.** Rejected. A zip restore needs random access (its central
directory is at the end of the stream), so it cannot be fed from stdin the way `extract` is; keeping
zip for download only would leave two archive formats, one with no restore story.

## Consequences

- **ADR-0002's boundary still widens.** The panel still creates plain containers of its own, outside
  any Compose project and invisible to `docker compose down` on the install stack — but they are
  ephemeral and self-removing, so the panel's only lifecycle duty is killing an in-flight child when
  the HTTP request is cancelled, not stopping containers left over from an earlier run.
- **ADR-0003's "exactly one port/adapter pair in the whole system" becomes three.** Two new seams
  are faked in tests, as `ComposeRunner` is: `volumes.CLI` (resolution through `docker compose
  config` and `docker volume inspect`) and `volumes.Runner` (the `docker run --rm` invocations).
- **Tickets 04 and 10 of `.scratch/volume-files/` are closed by this decision.** Ticket 04 (handle
  lifecycle and the container run spec) has no lifecycle left to build, and ticket 10's banner has
  nothing to list. Ticket 10's audit rows are deferred: single admin, no viewer screen yet; additive
  later.
