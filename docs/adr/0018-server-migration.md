# Server migration: an empty panel pulls everything from the old one

Amended by ADR-0019: driven from the terminal; finalize re-executes the panel.

Moving the panel to a new host meant copying the data directory, named Volumes, host paths and
locally built Images by hand, with the same `RELIST_DATA_DIR`, then redeploying every Compose.
Moving servers easily is the feature the panel was started for.

**Decision:** a **Migration** moves everything one panel manages to an **empty** panel on another
host. The new panel pulls it over HTTPS from the old one, item by item. The old panel issues a
short-lived, single-use **Migration token**. When the Migration starts, the old panel enters a
persisted **Migration lock** that refuses every change and stops every Compose. After the
Migration, the lock stays on as **Migrated** until the operator explicitly resumes that server.

- **Pull over HTTPS, not an archive file or SSH.** The operator asked for no SSH setup. A single
  archive file needs twice the disk space on the old host and two transfers. 50 GB was the size
  they named. Rejected: `export | ssh new import` streaming, and external tools (rsync daemon,
  restic) that add dependencies and ports to a single-binary panel (ADR-0001).
- **Items with their own digest, not one stream.** Each Volume, host path, Image, the database
  and the rest of the data directory are separate items. A failed item is retried alone, progress
  is per item, and a later two-phase sync or backup feature can reuse the items.
- **Everything moves, into an empty panel.** Moving the whole `panel.db` keeps every database ID,
  so `relist-c<ID>` project names and Volume names stay the same (ADR-0011). Choosing Composes to
  move would mean merging into another database and renumbering IDs. The only choice is which host
  paths outside the data directory to include.
- **The data directory path must match.** Containers mount the data directory and, since ADR-0017,
  per-Deployment source trees by absolute path (ADR-0002). Preflight refuses a different
  `RELIST_DATA_DIR`.
- **Stop, then copy once.** V1 has no live copy plus final delta. The operator accepted downtime
  for the whole copy, provided a delta pass can be added later.
- **The lock persists.** A restart of the old host mid-Migration must not reopen it for edits
  after part of the data was copied. Unlike the memory-only prune pause of the host guard, the
  lock is stored in the database.
- **Panel-managed Domains get a maintenance page** from one container run from the panel's
  image. Forwarding to the new host while DNS propagates is deferred.

## Consequences

- **The panel pulls Images, during a Migration only.** An Image with `RepoDigests` is pulled by
  digest on the new host. One without, or one whose pull fails, is copied with `docker save`.
  This is an exception to ADR-0001's "never pulls".
- **The Volume helper gains `pack` and `unpack`.** They have no size cap and keep ownership.
  `volfs.Extract`'s 4 GiB cap and setuid stripping suit uploads, not a database Volume.
- **Install settings are not copied.** `RELIST_DOMAIN`, `RELIST_ACME_EMAIL` and the Traefik ports
  live in the install's `.env`. Preflight shows the differences, and the operator copies them.
- **Hand-written Traefik routers get no maintenance page.** They return Traefik's own error
  until DNS moves.
- **Queued Deployments are canceled** on the old host at lock time, and in the copied database at
  finalize.
- **Who can reach an empty panel can start a Migration into it.** This is the same exposure as the
  first-admin setup screen today. Install, then migrate straight away.
- Spec: `.scratch/server-migration/spec.md`.
