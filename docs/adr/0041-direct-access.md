# Direct access: a timed, Admin-opened way into one container's port

A Database has no host port by design (ADR-0039): other Composes reach it on `relist-network`, and Traefik routes HTTP only. An operator still needs DBeaver or TablePlus on a Database, and the next targets (a Compose Service without its Domain's auth) will want the same shape. This amends ADR-0039's "no host port is opened for any Database" for one timed, Admin-opened case (`.scratch/databases/spec-2.md`, "Phase 5: Direct access").

**Decision:** A **Direct access** has a **target**, a **channel** and an **expiry**, and the panel runs one small proxy container for it. Admin only.
- **Generic model.** One table, `direct_accesses` (migration `00048`): `target_kind`, `target_id`, `target_port`, `channel`, `channel_config` (JSON), `expires_at`, `opened_by`, `opened_at`. Phase 5 ships target `database` and channel `host_port` (`{"bind","hostPort"}`). A Compose Service target and `tailscale` or `cloudflare` channels are later values, with no new table. There is no foreign key (target kinds live in different tables) and no unique constraint on the target: one access per Database is a service rule.
- **The proxy.** `relist-proxy`, a separate binary in the panel's own image, resolved as the Volume helper's image is. It is not a `relist-volhelper` subcommand: the Volume helper has no network by design (ADR-0010). The container is named `relist-access-<access id>`, joins the target's network, and has `AutoRemove`, restart policy `no`, 32 MiB of memory, a PID limit, no capabilities, `no-new-privileges` and a read-only root filesystem with a 1 MiB `/tmp`. It forwards every connection to one fixed target, at most 64 at once.
- **The panel enforces nothing on the channel.** Whoever can reach the channel reaches the target as if on its network. The channel's own control is the guard: SSH to the host for `host_port`.
- **`host_port` is `127.0.0.1` by default and always expires.** 15 minutes, 1, 4 or 8 hours. **All interfaces** (`0.0.0.0`) is an explicit tick with a warning.
- **The deadline is enforced by the proxy itself.** At the deadline it closes its listener and every open connection and exits, so the access closes on time even when the panel is down. **Extend** runs `relist-proxy extend` in the container, which writes a new deadline to a file the proxy rereads every 2 seconds: no restart, no dropped connection.
- **The table is the truth.** Extend cannot change a container's labels, so `relist.access.expires` is informational only. Reconcile (at start and every minute) reads `expires_at` from the table: a container with no row, or a row past its expiry, loses its container; a row with no running container is deleted.
- **Lifecycle.** Deleting a Database closes its Direct accesses first. Stop, Restart, Update image, Repair and restore leave them alone: the target name is stable. Opening is refused while a server Migration holds its lock; a Migration does not carry Direct accesses.
- **Audit.** Open, Extend and Close are audited under `direct_access.open`, `.extend` and `.close`, naming the target. Expiry and reconcile are not.

## Considered options

**A `relist-volhelper` subcommand.** Rejected: the Volume helper has no network, by design.

**Panel-side timer only.** Rejected: if the panel is down at the deadline, the port would stay open.

**A host-wide TCP gateway in the panel.** Rejected: a long-lived listener inside the panel would widen its attack surface; a short-lived container does not.

## Consequences

- Docker-published ports bypass host firewalls such as UFW: with **All interfaces**, a Database is on the internet with only its password. The card says so in red.
- A Migration carries no Direct accesses; the old host's proxies expire there.
- A failed port bind answers 409 naming the port, and leaves no container or row.
- Future channels and kinds need no new table; their own access control decides whether they may be permanent.
