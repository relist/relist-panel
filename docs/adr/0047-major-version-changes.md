# Major version changes

Phase 10 (`.scratch/databases/plan-phase-10.md`) adds **Change version**:
an Admin moves a Database to a higher version of its engine from its page.
Downgrades are never offered or accepted.

**Decision:**

- **Postgres is dumped and restored onto a new volume.** The on-disk format
  changes between majors, so the job pulls the new image first (a failed
  pull changes nothing), then **cuts every client off** by taking the
  container off `relist-network` (`docker exec` and the healthcheck on
  127.0.0.1 keep working). It then takes a `pre_update` Backup of every
  logical database, a `pg_dumpall --roles-only` file
  (`backups/version-change-<change id>-roles.sql`, 0600, never served), and
  each database's owner, ACL and per-database settings. It stops the old
  container, starts the new version on `relist-db-<id>-v<change id>`, and
  restores the roles (without `postgres`'s own, which the new server already
  has with the Database's password), then each database. Writes made while
  the dumps run would otherwise be lost without a trace; the downtime
  starts at the backup instead of at the stop.
- **A one-database dump drops state that must come across.** `pg_dump`
  without `--create` carries no database owner, ACL or
  `ALTER ROLE … IN DATABASE … SET` / `ALTER DATABASE … SET` rows, and
  `pg_dumpall --roles-only` none of the per-database ones either. The job
  reads them on the old server and puts them back after each restore: a
  Full user's `SET role` (ADR-0044) and a restricted database's ACL survive.
  The same helper now runs in a replace Restore, which `DROP DATABASE` had
  been silently stripping of those settings since phase 7.
- **The switch is one transaction.** The Database's version, volume and
  applied settings change together with the change record, after the last
  restore. Until then the record still names the old ones: any failure
  removes the new container and volume and recreates the old container from
  its stored applied settings, and the log names the failing step. A panel
  restart in the middle heals the same way — `ReconcileAll` sees the
  container's image hash differ from the record's and recreates the old
  container on the old volume, and the half-made volume is swept after it.
  This relies on every version change changing the image: `hashSpec` does
  not include the volume, and must not, or every existing Database would be
  recreated at the next panel start.
- **`v<n>` is the change record's ID**, not a counter, so no two attempts
  ever share a volume name and a failed attempt's volume can be swept
  without being mistaken for live data.
- **The old volume is kept as Previous version data**, listed on the
  Settings tab with a Delete button. A server Migration copies it, Delete
  Database removes it, and the Docker page shows it as owned by Databases.
- **MariaDB and Redis change in place**, because both read the older
  format. MariaDB is backed up first unless the Admin unticks it (Redis has
  no Backups). The record switches just before the first start of the new
  image, and **nothing rolls back after it**: MariaDB does not support a
  downgrade after `mariadb-upgrade`, and Redis 7.4 refuses an RDB that
  Redis 8 has saved ("Can't handle RDB format version 15", checked
  2026-11-17). A failed start leaves the new version with a last error that
  names the pre-update Backups. MariaDB's first start carries
  `MARIADB_AUTO_UPGRADE=1`; once healthy, the container is recreated without
  it, so it never sits in the applied settings. A panel restart after the
  switch finishes the start at the next Startup (MariaDB still gets its
  upgrade start) instead of leaving ReconcileAll to start the new image
  plain. The last error says whether the new version ran at all: when its
  container was never created, the data is untouched and Start retries.
- **No job starts before the start-up recovery ends.** Startup fails every
  job still marked running and the sweep removes failed changes' volumes,
  so the server holds new jobs (409, "still starting") until both have run,
  and ReconcileAll leaves alone a Database whose job owns its container.
  While a change runs, its pre-update Backups cannot be deleted, and Rename
  answers 409: a rename re-aliases the container, which would put a
  Postgres server the change cut off back on `relist-network`. Every other
  job still lets Rename through.
- **Refused before anything starts:** a superuser password reset still
  pending, a tag that is not strictly higher (compared by dot-separated
  numbers, 422), an unknown tag, a stopped Database, a running job, or a
  Migration or Panel update lock. MySQL offers one tag, so it is never
  offered.
- **A role-wide list setting `pg_dumpall` cannot copy** refuses a Postgres
  change (422) before anything changes. A role can keep raw text in such a
  setting with `SET … FROM CURRENT`; when pg_dump's `SplitGUCList` cannot
  split it, or it holds no names (`''` or blanks), `pg_dumpall` writes
  `ALTER ROLE … SET … TO ;`, and the roles restore would fail every time. The refusal names the role, the setting
  and the `ALTER ROLE … RESET` that fixes it.

**Considered and rejected:**

- **`pg_upgrade`.** It needs both versions' binaries in one image and the
  old data directory mounted beside the new one; the official images ship
  one version each.
- **Rolling an in-place change back.** The new version may already have
  rewritten the data in a format the old one cannot read.
- **A counter for `v<n>`.** A crashed attempt's volume would share its name
  with the next attempt's.
- **Downgrades.** No engine supports reading a newer format.

**Consequences:** a Postgres change doubles the Database's disk use until
the previous version data is deleted; clients see the Database go away from
the first backup to the switch; a failed in-place change needs the
pre-update Backups or a manual fix, not the panel's rollback.

**A container on another volume is drift.** A panel build older than this
change reads a Database's volume from its ID (`relist-db-<id>`), so after a
Postgres change it can recreate the container on the volume the change left
behind: on the previous version data, or on a new empty volume if that was
deleted. The volume stays out of the label hash, or every Database would
restart on the Panel update that added it. Instead, reconcile compares the
volume mounted at the data path with the recorded one and recreates the
container on the recorded volume, so starting a current build again, or
Repair, puts the Database back on its data. The other volume is left as it
is, holding whatever apps wrote there meanwhile. When the recorded volume is
missing, reconcile changes nothing and the last error names both volumes:
recreating would start an empty server.
