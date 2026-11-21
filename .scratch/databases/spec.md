# Spec: Databases, backups and query panel

Date: 2026-11-14.
Status: ready-for-human

## Problem

Every Compose that needs Postgres, MySQL, MariaDB or Redis today carries its own
database Service. Whenever the operator wants to back up, restore, run a query
or just look at data, they have to:

- install pgAdmin or phpMyAdmin as yet another Compose;
- give it a Domain, a password and a Basic auth group;
- keep updating it whenever a vulnerability comes out.

That is tiring and adds attack surface. Backups have no schedule and no
rotation unless the operator scripts them by hand.

Easypanel and Dokploy solve this with templates. A template writes a Compose
once and the panel then forgets what it made, so it cannot offer tools. Relist
will instead make a database a typed, panel-managed entity. Because the panel
knows the engine, the credentials and the volume, the tools work without any
setup.

## Goal and success criteria

- **Databases.** An Admin creates a Postgres, MySQL, MariaDB or Redis
  **Database** from the new **Data → Databases** page by picking a name, engine
  and version. Seconds later it is running. No YAML is involved.
- **Connection.** The Database page shows host, port, user, password and a
  ready-to-paste connection URL plus a Compose snippet. A Service in any Compose
  that joins `relist-network` can connect with them unchanged.
- **Backup and restore** are for the SQL engines only; Redis has none (see
  Decisions). One click backs up either the whole instance or the
  selected logical databases. The file can be downloaded, restored, or
  uploaded and restored. A restore never runs without first taking a backup of
  the current state.
- **Schedule and rotation.** Each Database can back up on a schedule, copy each
  scheduled backup to a **Backup destination** (S3-compatible first), and rotate
  scheduled backups locally and remotely by count and/or age. Manual backups are
  never deleted automatically.
- **Query panel.** An Admin runs SQL (or Redis commands) in the browser,
  read-only by default, with results as a table, and every query is audited.
  pgAdmin and phpMyAdmin are no longer needed.
- No host port is opened for any Database.

## Out of scope (later, in this order of likely value)

- **Temporary access.** A short-lived TCP proxy container (built from the panel's
  own image) that forwards a host port to a Database, binds to `127.0.0.1` by
  default, expires after a set time, and never restarts the Database. This is
  for DBeaver/TablePlus over an SSH tunnel.
- **Redis backup and restore** (RDB via `redis-cli --rdb`). Possibly with a
  Cache / Persistent choice at create, so only persistent Redis offers it.
- **SFTP and WebDAV** Backup destinations. SFTP will reuse the SSH key library.
- **Attach.** Pointing the same tools at a database Service that already runs
  inside a Compose.
- **Logical database and user management** (create a database and user per app
  from the UI). Until then, run `CREATE DATABASE` / `CREATE USER` in the Query
  panel.
- **Password rotation.** Changing the superuser password would break every
  consumer.
- **GFS rotation** (daily / weekly / monthly tiers), **backup encryption**,
  **failure notifications**, point-in-time recovery.
- **Editing data in a grid** and schema-editing UI (phpMyAdmin style).
- **Per-Database grants for non-Admin roles.**
- **Major version upgrades in place.** The path is backup, then create a new
  Database on the new major, then restore.
- More engines (MongoDB, ClickHouse, …). The Engine driver interface is designed
  so that each one is a new driver.

## Decisions

### Scope change

- **ADR-0001's non-goal "No database-as-a-service" is withdrawn** by a new ADR.
  `CONTEXT.md` is updated:
  - its opening paragraph drops "no database-as-a-service";
  - **Language** gains the terms Database, Engine, Backup, Backup scope, Backup
    schedule, Rotation and Backup destination.
- **Host-wide, Admin only.** Databases and Backup destinations belong to the
  host, not to a Project or Environment, like the Registry and Certificates. Only
  Admins can open, query, back up or restore them (ADR-0031 roles). Other roles
  see neither menu entry.
- **One Database = one container.** Each Database has its own container, volume,
  password, version and backups. Grouping several apps into one server is left
  to the operator, by creating logical databases inside it.

### Runtime: panel-managed container, the Registry pattern

A Database is **not** a Compose. The rejected alternative was a hidden Compose
(`kind=database`). Every Compose must belong to an Environment, so it would
have needed a fake system Project. The Compose, Policy and Deployment pages
would also have needed guards to hide it. And editable YAML could drift from
what the panel believes the credentials and image are.

So the panel runs the container itself through the Engine API, as it does the
Registry (ADR-0033) and Builders (ADR-0028):

- **Container** `relist-db-<id>` and **named volume** `relist-db-<id>`, both from
  the database ID, so a rename never changes them (as ADR-0011).
- The container runs with `restart: unless-stopped`, has **no published port**,
  and sits on `relist-network` with two aliases:
  - stable: `relist-db-<id>`;
  - readable: `<slug>.db.internal`.

  The readable alias follows renames and is skipped with a warning when the
  slug is not DNS-safe, as ADR-0014 does. A name with no ASCII letters or
  digits has the fallback slug `x`, gets no readable alias, and may share that
  slug with other Databases.
- Optional **memory and CPU limits**.
- **Healthcheck** by driver: `pg_isready`, `mysqladmin ping` / `mariadb-admin
  ping`, `redis-cli ping`.
- **Drift and Repair.** A `relist.database.hash` label covers image, data path,
  env, command, healthcheck and limits, but not the alias. On panel start and
  before every Database operation, the panel compares the container to the
  stored record and recreates it on drift, as `registry.Service.Reconcile`
  does. The UI shows Drift, Stopped and Error, and each offers **Repair**.
- **Applied settings: a Panel update never restarts a Database.**
  - Each Database stores the container spec it was last created with
    (`applied_spec`, migration `00043`). Reconcile compares the container
    with that spec, not with what the running panel's driver would choose
    today, and recreates from it after real drift.
  - When a newer panel's driver would create the container differently, the
    Database page says "New container settings are available". **Apply
    settings**, or **Update image**, applies them. A stopped Database gets
    them on its next Start.
  - This mirrors Deployments freezing their inputs when queued (ADR-0017).
- **A missing volume is never silently recreated.** Reconcile sets the status
  to `volume_missing` and stops. An empty new volume would look like data loss
  that happened silently. The Admin either restores from a backup into a fresh
  volume (an explicit action) or deletes the Database.
- **Reserved names.** The Compose policy refuses a Service that answers on
  `relist-network` to `relist-db-<n>` or to any `*.db.internal` name. Such a
  Service would receive connections, and passwords, meant for a Database. The
  check sits beside the existing `relist-registry` check.
- **Docker cleanup** treats Database volumes and images as owned by the panel,
  so it never offers to remove them.
- **Image pull.** The panel pulls the engine image the Admin picked. This is the
  fifth exception to "the panel never pulls an image itself", after the Scanner
  (ADR-0021), the panel image (ADR-0024), BuildKit (ADR-0028) and `registry:3`
  (ADR-0033). Images are pinned by **tag** (`postgres:17`), not digest, because
  **Update image** is meant to pick up patch releases. They are Docker Hub
  official images, pulled over the Engine API with no registry login.
- **The superuser password** is 32 random bytes, base64url without padding (43
  characters), generated by the panel and stored in the panel database. The panel reads it back to show it,
  run dumps and run queries.

### Engine drivers

`internal/databases` defines an **Engine** driver interface. Each engine
implements:

- allowed versions (image + tag list shown in the create form);
- container env, data path, port and healthcheck;
- connection URL and env snippet;
- list logical databases (for Backup scope and the Query panel's selector);
- dump command for a given scope, and restore command (SQL engines only;
  the Redis driver reports that it has no backup support);
- open a query session.

Phase 1 ships `postgres`, `mysql`, `mariadb` and `redis`. The version lists are
fixed in code: postgres 18 (default), 17 and 16; mysql 8.4; mariadb 11.8
(default) and 11.4; redis 8 (default) and 7.4. Postgres 18's image keeps its
data under `/var/lib/postgresql`, while 17 and below use
`/var/lib/postgresql/data`, so the data path depends on the version.

### How tools reach the database

- **Backup and restore run `docker exec` in the Database's own container**
  (`pg_dump`, `mysqldump`, `mariadb-dump`). The output streams back
  to the panel.
  - The dump tool always matches the server version. This avoids the classic
    `pg_dump` version mismatch.
  - No extra image needs pulling or patching.
- **The Query panel connects directly with Go drivers** (`pgx`,
  `go-sql-driver/mysql`, `go-redis`) over `relist-network`. The panel is already
  on that network; it reaches the Registry at `relist-registry:5000`. A direct
  connection gives typed columns, server-side statement timeouts, cancellation
  through context, and row limits. Parsing CLI output cannot give these
  reliably.

### Lifecycle

- **Create** has fields for name, engine, version and optional limits. The
  panel pulls the image, then creates the volume and container, then waits for
  the healthcheck. Status moves through `creating`, then `running` or `error`
  (with the error message).
- **Start / Stop / Restart.**
- **Update image** pulls the same tag again, applies the panel's current
  container settings, and recreates the container on the same volume only if
  the image or the settings changed. A stopped Database is not started: its
  old container is removed, and the next Start creates the new one. From phase 2 on, a "Back up first" checkbox is on by default.
  Changing the major version is not offered.
- **Rename** changes the display name and the readable alias. The alias is
  swapped by disconnecting and reconnecting `relist-network`, and the Database
  never restarts; the alias is not part of the hash. The Connection tab
  recommends the stable host.
- **Logs** show the last lines of the container's log with a Refresh button, as
  Compose logs do.
- **Delete** requires typing the name. It removes the container and the volume.
  In phase 1, with no Backups yet, the record is removed too. From phase 2, a
  checkbox **"Also delete backups"** is off by default.
  - If it stays off, the record stays as status `deleted` and shows only the
    Backups tab, for downloading or deleting files. Remote copies stay listed
    too.
  - When the last backup is deleted, the record disappears.

### Backups

- **SQL engines only.** Redis Databases have no Backups tab, no schedule and no
  restore. The operator's Redis use is cache-like, and Redis restore (stop,
  swap `dump.rdb`, handle AOF) was the costliest part of the backup work for
  the least value.
  - A Redis Database keeps the image's default persistence (RDB snapshots in
    its volume), so its data survives restarts and Update image.
  - Redis backup is listed under Out of scope and can be added later as a
    driver capability.
- **One Backup is one file.** It is gzipped by the panel while streaming, and
  its sha256 is computed at the same time.
  - Local path: `<data dir>/.databases/<id>/backups/<UTC timestamp>-<backup id>.sql.gz`.
  - Directory mode `0700`, file mode `0600`.
  - A run that fails deletes its partial file.
- **Backup scope** is either the whole instance (the default) or selected
  logical databases. It is chosen per manual run and per schedule, and recorded
  on each Backup.

  | Engine | Whole instance | Selected databases |
  |---|---|---|
  | postgres | `pg_dumpall --clean --if-exists` | `pg_dumpall --globals-only`, then `pg_dump --create --clean` per database, concatenated into one `.sql.gz` |
  | mysql / mariadb | `--all-databases --single-transaction --routines --events` | `--databases a b --add-drop-database --single-transaction --routines --events` |

  A selected database that no longer exists fails the run and names it. It is
  never skipped silently.
- **Trigger** is one of `manual`, `scheduled` or `pre_restore`.
- **Concurrency.** Only one backup or restore runs per Database at a time.
  Scheduled runs queue host-wide and run one at a time. They do not use the
  Deployment queue.
- **Panel update and Migration.** Backups and restores behave like Registry
  cleanup. A run that has not started waits. A Panel update's quiesce, and the
  Migration lock, wait for a run already in progress.
- **Download** streams the file to the browser. Every download is audited.
- **Pin** marks a scheduled Backup so Rotation never deletes it.

### Restore

- **Source:** a Backup from the list (its sha256 is checked first), or an
  uploaded `.sql` or `.sql.gz` file.
- **Confirmation.**
  - The Admin types the Database name.
  - For a selected-scope Backup, the dialog lists the logical databases that
    will be dropped and recreated. Others in the instance are untouched.
  - For a whole-instance Backup or an upload, the dialog says the whole
    instance is affected.
- **A `pre_restore` Backup of the whole instance always runs first.** If it
  fails, the restore does not start.
- **SQL engines** stream the file into `psql` / `mysql` / `mariadb` inside the
  container. The Database keeps running.
  - The client stops at the first error (`ON_ERROR_STOP=1` for `psql`).
  - **Postgres** first terminates other sessions on the affected databases
    (`pg_terminate_backend`), so `DROP DATABASE` can proceed. It runs connected
    to `postgres`, or to `template1` when `postgres` is itself in scope. For
    this reason a whole-instance dump uses `pg_dumpall --clean --if-exists`.
  - Apps connected to an affected database lose their connections during the
    restore. The confirmation dialog says so.
- A failed restore shows its log and links to the `pre_restore` Backup.

### Backup schedule and Rotation

- **Schedule per Database:** enabled, days (`daily` or weekdays), time of day in
  the host's timezone, Backup scope, and optionally one Backup destination.
  - The schedule rules (`dayMoment`, `listed`, `Due`) move out of
    `internal/registry/schedule.go` into a shared package, which both the
    Registry and Databases use.
  - A run the panel missed while it was down is made up once.
  - A schedule that has never run is armed, not fired.
- **Rotation** applies only to `scheduled` Backups that are not pinned. It is
  configured separately for **Local** and for **Destination**, each with:
  - **keep last N** (blank = no limit);
  - **keep at most X days** (blank = no limit).

  A Backup is deleted when it breaks either rule. **The newest successful
  Backup is always kept**, so a long run of failures can never empty the set.
  The defaults are Local: keep last 7, and Destination: keep last 30.
- **`manual` and `pre_restore` Backups are never deleted automatically.** The
  Admin deletes them by hand.
- Rotation runs after every successful scheduled Backup and on each scheduler
  tick, so the age rule applies even when no new Backups happen.
- Rotation is a pure function: (backups, settings, now) → IDs to delete.

### Backup destinations

- New page **Data → Backup destinations**, a host-wide library like SSH keys
  and External registries.
- A **Backup destination** has a name, a `type` and that type's settings. Phase
  3 ships `s3`:
  - endpoint;
  - region;
  - bucket;
  - prefix;
  - access key ID;
  - secret access key;
  - path-style toggle.

  This covers AWS S3, Cloudflare R2, Backblaze B2 and MinIO. Secrets are stored
  in the panel database and never sent back to the browser; editing leaves the
  secret blank to keep it.
- **Test** writes a small object under the prefix, reads it back and deletes it.
- **Upload** happens after the local Backup succeeds. The object key is
  `<prefix>/<db slug>-<db id>/<file name>`.
  - Upload status is recorded per Backup.
  - A failed upload leaves the local Backup successful and offers **Retry
    upload**.
- **Remote deletion** (Rotation, manual delete) only deletes objects the panel
  recorded uploading. It never lists the prefix and deletes what it finds.
- A Backup destination that a schedule still uses cannot be deleted. The error
  names the Databases.

### Connection tab

- Every field has a copy button:
  - host;
  - port;
  - user;
  - password (hidden until revealed);
  - default database;
  - connection URL (password percent-encoded).
- **Both hosts are shown**, with the stable `relist-db-<id>` recommended for
  config. The readable `<slug>.db.internal` follows renames.
- **Compose snippet**, for example:

  ```yaml
  environment:
    DATABASE_URL: postgres://postgres:<password>@relist-db-12:5432/postgres
  networks: [relist-network]
  ```

  It has a note that the consuming Service must be on `relist-network`
  (ADR-0014 and its 2026-11-06 amendment). Revealing or copying the password is
  audited.

### Query panel

**SQL engines (postgres, mysql, mariadb):**

- **Layout.**
  - A logical database selector at the top.
  - A schema → table tree on the left. Clicking a table inserts and runs
    `SELECT * FROM <table> LIMIT 100`.
  - A Monaco editor (already lazy-loaded) on the right.
- **Run.** Runs the selection if there is one, otherwise the whole editor.
  Each statement's result appears in its own tab, either as a table or as
  affected rows.
- **Read-only by default.** Statements run in a `READ ONLY` transaction. The
  **Allow writes** switch is per page view and resets on reload.
- **Limits.**
  - Timeout of 30 s (server-side statement timeout plus context).
  - At most 1,000 rows returned per result, with a "truncated" flag.
  - Cell values are cut at 64 KB for display.
- **Cancel** aborts the HTTP request. The request context is cancelled, which
  makes the driver cancel the query on the server.
- **CSV export** of the displayed result happens in the browser. Bulk export is
  what Backups are for.
- **History** of the last 50 queries per Database is kept in the browser's
  `localStorage` (a per-viewer convenience).

**Redis:**

- **Console** that splits arguments the way `redis-cli` does, quotes included.
- **Key browser** that uses `SCAN` with a pattern. It shows type and TTL, and
  shows the value by type (string, hash, list, set, zset), capped at 1,000
  elements.
- **Read-only by default.** Commands whose `COMMAND INFO` flags include `write`
  are refused unless Allow writes is on.
- **Dangerous commands** need a second confirmation even when writes are
  allowed: `FLUSHALL`, `FLUSHDB`, `CONFIG SET`, `SHUTDOWN`, `DEBUG`, `SCRIPT
  FLUSH`, `FUNCTION FLUSH`.

**Audit.** Every run records:

- who;
- which Database and logical database;
- the statement text (cut at 4 KB);
- whether writes were allowed;
- duration, row count and error.

Create, delete, start, stop, update, repair, backup, download, restore, pin,
Backup destination changes and password reveal are audited too.

### Server migration

Server migration (ADR-0018) carries Databases:
- Database volumes are always items in the manifest, with no tick box.
  `<data dir>/.databases/` moves with the data directory.
- The old host stops every Database after stopping the Composes, because a
  running database's files copy inconsistently. It stops them again on a
  reboot during the lock, and starts the wanted ones on Resume.
- The new host starts the wanted Databases before queuing any Deployment.

## Data model (sketch)

New tables in new migrations. Committed migrations are never edited. The
numbers are picked at implementation time.

- `databases`:
  - `id`, `name`, `slug`, `engine`, `version`, `image`;
  - `password`, `memory_limit`, `cpu_limit`;
  - `status`, `status_error`, `created_at`, `deleted_at`.
- `database_backups`:
  - `id`, `database_id`, `trigger`, `scope` (JSON: `all` or a list of names);
  - `status`, `error`, `log`, `file_name`, `size`, `sha256`, `pinned`;
  - `started_at`, `finished_at`.
- `database_backup_schedules` (one per Database):
  - `enabled`, `days`, `minute`, `scope`, `destination_id`;
  - `local_keep_count`, `local_keep_days`, `remote_keep_count`,
    `remote_keep_days`;
  - `last_run_at`.
- `database_backup_uploads`:
  - `backup_id`, `destination_id`, `object_key`;
  - `status`, `error`, `uploaded_at`, `deleted_at`.
- `backup_destinations`:
  - `id`, `name`, `type`, `config` (JSON, without secrets), `secret`;
  - `created_at`.

## Error handling

| Situation | Behaviour |
|---|---|
| Image pull fails | Status `error` with the pull error; **Repair** retries |
| Healthcheck never passes | Status `error`; logs available |
| Container gone | Reconcile recreates it on the same volume |
| Volume gone | Status `volume_missing`; no automatic action |
| Disk full during backup | Run fails, partial file deleted, error shown |
| Selected logical database missing | Run fails, naming the database |
| Upload fails | Local Backup succeeds, upload marked failed, **Retry upload** |
| Pre-restore backup fails | Restore does not start |
| Restore fails | Log shown, link to the `pre_restore` Backup |
| Query error | Server message shown, with position when the engine gives it |
| Database stopped | Query and backup buttons disabled with the reason |

## Testing

- **Unit.**
  - Each driver's dump and restore command for each scope.
  - Connection URL encoding (passwords with `@ : / ? # %`).
  - Rotation as a table test: count, age, both, newest-successful kept, pinned,
    manual, pre_restore.
  - The shared schedule package (existing Registry tests move with it).
  - The Redis read-only filter and the dangerous-command list.
  - The Redis argument splitter.
- **Integration in the dind sandbox** (`task e2e:serve`), for each SQL engine:
  1. create;
  2. write data;
  3. back up the whole instance and a selected scope;
  4. delete data;
  5. restore;
  6. check the data is back;
  7. check that read-only refuses an `INSERT`.

  For Redis: create, `SET` with writes allowed, check that read-only refuses a
  `SET`, then restart and check the key survived.

  For S3, run against a MinIO container in the sandbox.
- **Playwright** (`e2e/`):
  1. create a Postgres;
  2. copy the URL;
  3. run a query;
  4. back up;
  5. restore;
  6. delete.
- A manual live check per phase, following `docs/agents/live-checks.md`.

## Phases (one implementation plan and PR each)

1. **Core.**
   - The ADR and the `CONTEXT.md` update.
   - `internal/databases` with the 4 drivers.
   - Create, start, stop, restart, rename, update image, repair and delete.
   - Reconcile on start.
   - Connection tab and Logs.
   - The **Data** group in the sider.
   - Server migration handling.
2. **Manual backup and restore.**
   - Backup scope.
   - Download and upload.
   - Pin.
   - `pre_restore` Backups.
   - The deleted-Database Backups view.
3. **Schedule, Rotation and Backup destinations (S3).**
   - Moving the schedule rules into a shared package.
   - Upload and retry.
   - Remote rotation.
4. **Query panel.** SQL and Redis, plus audit.
