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
  Decisions). One click backs up every logical database, or the selected ones,
  as one file per logical database. A file can be downloaded, or restored
  (from the list or uploaded) into a logical database the Admin picks. A
  restore that replaces a database never runs without first backing that
  database up.
- **Schedule and rotation.** Each Database can back up on a schedule, copy each
  scheduled backup to a **Backup destination** (S3-compatible first), and rotate
  scheduled backups locally and remotely by count and/or age. Manual backups are
  never deleted automatically.
- **Query panel.** An Admin runs SQL (or Redis commands) in the browser,
  read-only by default, with results as a table, and every query is audited.
  pgAdmin and phpMyAdmin are no longer needed.
- No host port is opened for any Database.

## Out of scope

Planned in `.scratch/databases/spec-2.md` (2026-11-15): Direct access, SFTP
Backup destinations, logical database and user management,
superuser password reset, per-Database access for non-Admins, GFS Rotation,
backup encryption and changing the major version.

Dropped: Redis backup and restore, Attach, and (2026-11-16) WebDAV Backup
destinations.

Still out of scope: failure notifications, point-in-time recovery, editing data
in a grid and schema-editing UI, more engines.

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
- list logical databases (for choosing what to back up, the restore target and the Query panel's selector);
- dump command for one logical database, and restore command into one
  logical database (SQL engines only; the Redis driver reports that it has no
  backup support);
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
- **The Query panel connects directly with Go drivers** (`pgx` v5.12.0 and
  `go-sql-driver/mysql` v1.10.1; Redis through a small RESP2 client in the
  panel, phase 4 decision: no new dependency, and every reply is read with a
  size cap) over `relist-network`. The panel is already
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
  old container is removed, and the next Start creates the new one. From phase 2 on, a "Back up first" checkbox is on by default; it is unticked and disabled while the Database is not running, since a Backup needs it running. With it ticked, Update image is a job (phase 2 decision): the route answers 202, it takes a `pre_update` Backup first, and a failed Backup stops it before the pull.
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
  - The tombstone's `deleted_at` is set, its container and volume are removed, and its name becomes `<name> (deleted #<id>)` with slug `deleted-<id>`, so the name is free again (phase 2 decision).

### Backups

- **SQL engines only.** Redis Databases have no Backups tab, no schedule and no
  restore. The operator's Redis use is cache-like, and Redis restore (stop,
  swap `dump.rdb`, handle AOF) was the costliest part of the backup work for
  the least value.
  - A Redis Database keeps the image's default persistence (RDB snapshots in
    its volume), so its data survives restarts and Update image.
  - Redis backup was dropped for good on 2026-11-15 (`spec-2.md`).
- **One Backup is one logical database in one file** (phase 2 decision,
  replacing whole-instance Backups). It is gzipped by the panel while
  streaming, and its sha256 is computed at the same time.
  - Local path: `<data dir>/.databases/<id>/backups/<UTC timestamp>-<backup id>.sql.gz`.
  - Directory mode `0700`, file mode `0600`.
  - A run that fails deletes its partial file.
  - The file is plain SQL with no `CREATE DATABASE`, `\connect` or `USE`, so
    it restores into any database, under any name, with the standard tools.
- **Users and roles are never backed up.** No Postgres role and no MySQL user
  is in any Backup, so a restore can never change a password or drop a role
  another database uses. An app on the panel's `postgres` / `root` credentials
  is unaffected. A restore into an instance that lacks a role the dump's
  objects are owned by fails with `role "x" does not exist`; the Admin
  creates the role and restores again.
- **What to back up** is chosen per manual run and per schedule:
  - **All databases** (the default): every logical database that exists when
    the run starts, so one created later is included;
  - **Selected databases**: a fixed list. A selected database that no longer
    exists fails its Backup and names it. It is never skipped silently.

  A run makes one Backup per logical database, one after another. One that
  fails does not stop the others.

  | Engine | Dump of one logical database `db` |
  |---|---|
  | postgres | `pg_dump -U postgres`, with `PGDATABASE=db` in the env |
  | mysql / mariadb | `mysqldump` / `mariadb-dump -uroot --single-transaction --routines --events --triggers db` |

  The logical databases listed are every non-template database for Postgres,
  **including `postgres`**, the database the panel's connection URL points at.
  MySQL and MariaDB hide `information_schema`, `performance_schema`, `mysql`
  and `sys`. A name that starts with `-` is refused, so it can never reach a
  tool as an option.
- **Trigger** is one of `manual`, `scheduled`, `pre_restore` or `pre_update`
  (phase 2 adds `pre_update`, the Backups "Back up first" takes of every
  logical database before Update image; a Database with none updates
  straight away).
- **Concurrency.** Only one job runs per Database at a time. A job is a Backup, a restore, or an Update image with "Back up first". While one runs, Start, Stop, Restart, Update image, Apply settings, Repair and Delete answer 409; Rename and reads still work (phase 2 decision).
  Scheduled runs queue host-wide and run one at a time. They do not use the
  Deployment queue.
- **Panel restart.** A graceful shutdown waits for a running job, until Docker's stop timeout kills the panel. Either way a `running` row is made `failed` ("the panel stopped during this run") at the next start, and its `.partial` and upload files are removed (phase 2).
- **Panel update and Migration.** Backups and restores behave like Registry
  cleanup. A run that has not started waits. A Panel update's quiesce, and the
  Migration lock, wait for a run already in progress.
- **Download** streams the file to the browser. Every download is audited.
- **Pin** marks a scheduled Backup so Rotation never deletes it. Pin moves to phase 3: with no Rotation, nothing could delete a Backup that Pin protects.

### Restore

- **Source:** a Backup from the list (its sha256 is checked first), or an
  uploaded `.sql` or `.sql.gz` file (gzip is told by its content).
- **Target:** every restore goes into one logical database the Admin picks
  (phase 2 decision). Nothing in the file decides where it goes.
  - **Replace an existing database:** the default for a Backup whose database
    still exists. A `pre_restore` Backup of that database runs first; if it
    fails, nothing changes.
  - **Restore into a new database:** the Admin types a name (letters, digits,
    `_` and `-`, at most 63 characters, not starting with `-`). The default
    for a Backup whose database is gone. No `pre_restore` Backup is needed.
  - A Backup can be restored under another name, which copies a database.
  - System and template databases (`template0`, `template1`, and the MySQL
    ones above) are never a target.
- **Replacing** drops and recreates the target, then streams the file into
  it. The Database keeps running.
  - **Postgres:** `DROP DATABASE … WITH (FORCE)`, which ends the sessions on
    it in the same step, then `CREATE DATABASE … TEMPLATE template0 OWNER
    <its owner before>`. A new database is owned by `postgres`.
  - **MySQL / MariaDB:** `DROP DATABASE`, then `CREATE DATABASE` with its
    default character set and collation from before.
  - The file then runs connected to the target: `psql -v ON_ERROR_STOP=1`, or
    `mysql` / `mariadb --database=<target>`. Every engine stops at the first
    error, and that fails the restore.
  - Apps connected to the target lose their connections. The confirmation
    dialog says so.
- **Uploads are checked before anything runs.** A file is refused, naming the
  line, when a line (outside Postgres `COPY` data, and after a MySQL `/*!NNNNN`
  prefix) starts with:
  - a psql meta-command other than `\restrict` / `\unrestrict` (so
    `\connect`, `\c`, `\!` are refused);
  - `CREATE`, `ALTER` or `DROP` followed by `DATABASE`, `ROLE` or `USER`, or
    `SET PASSWORD`;
  - for MySQL and MariaDB also `USE`, and `CREATE` / `DROP SCHEMA`.

  Such a file (`pg_dumpall`, `mysqldump --all-databases` or `--databases`)
  would leave the target or change users. The error says to dump one database
  with `pg_dump db` or `mysqldump db`.
- **Confirmation.** The Admin types the Database name. The dialog names the
  target and whether it is replaced or created.
- A failed restore shows its log and links to the `pre_restore` Backup. Restores are recorded in `database_restores` (phase 2 decision), with their target, so the log has a home.

### Backup schedule and Rotation

- **Schedule per Database:** enabled, days (`daily` or weekdays), time of day in
  the host's timezone, all or selected databases, and optionally one Backup
  destination.
  - The schedule rules (`dayMoment`, `listed`, `Due`) move out of
    `internal/registry/schedule.go` into a shared package, which both the
    Registry and Databases use.
  - A run the panel missed while it was down is made up once.
  - A schedule that has never run is armed, not fired. Turning a schedule on
    arms it too (phase 3 decision), so enabling at 10:00 a 03:00 schedule runs
    tomorrow, not now.
  - **At run time** (phase 3 decision):
    - the Database is running a job, or a Migration or Panel update holds its
      lock: the run waits and is tried again each minute;
    - the Database is not running: the day's run is skipped and the schedule
      shows "skipped: the Database was not running";
    - a selected database is gone: its Backup fails, naming it, and the others
      run;
    - the panel stops mid-run: the running rows fail at the next start, as in
      phase 2, and the run is not repeated until the next due time.
- **Rotation** applies only to `scheduled` Backups that are not pinned. It is
  configured separately for **Local** and for **Destination**, and counted
  **per logical database**, each with:
  - **keep last N** (blank = no limit);
  - **keep at most X days** (blank = no limit).

  A Backup is deleted when it breaks either rule. **The newest successful
  Backup of each logical database is always kept**, so a long run of failures
  can never empty its set.
  The defaults are Local: keep last 7, and Destination: keep last 30.
- **`manual` and `pre_restore` Backups are never deleted automatically.** The
  Admin deletes them by hand.
- Rotation runs after every successful scheduled Backup and on each scheduler
  tick, so the age rule applies even when no new Backups happen.
- Rotation is a pure function: (backups, settings, now) → IDs to delete.
- Rotation runs only for an enabled schedule. Remote Rotation covers the copies
  in the schedule's current destination; copies in a destination the schedule
  used before are kept until deleted by hand (phase 3 decision).
- **Local and remote copies** (phase 3 decision). Local Rotation deletes the
  local file. A Backup whose file is gone but whose copy is still in a
  destination stays listed as **Remote only**: it cannot be downloaded or
  restored from the panel; the Admin fetches it from the bucket. A Backup with
  neither a file nor a copy is removed. A deleted Database's Backups view keeps
  Remote only Backups until they are deleted.

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
  secret blank to keep it. A kept secret stays with its endpoint: changing the
  endpoint asks for the secret again, as External registries do (phase 3
  decision).
- **Client library:** `github.com/minio/minio-go/v7` (phase 3 decision, 18
  modules, against about 130 packages for `aws-sdk-go-v2`).
- **Test** writes a small object under the prefix, reads it back and deletes it.
- **Upload** happens after the local Backup succeeds. The object key is
  `<prefix>/<db slug>-<db id>/<logical database>/<file name>` (phase 3
  decision: the logical database is in the key, so a person browsing the
  bucket can tell the files apart). The panel sends the file's size with the
  upload and checks the object's size afterwards.
  - Upload status is recorded per Backup.
  - A failed upload leaves the local Backup successful and offers **Retry
    upload**.
- **Remote deletion** (Rotation, manual delete) only deletes objects the panel
  recorded uploading. It never lists the prefix and deletes what it finds.
  - Deleting a Backup by hand, or a Database with its Backups, deletes the
    remote copies first. If one cannot be deleted, nothing else is deleted and
    the error names the destination (phase 3 decision), so no copy is left
    behind untracked.
- A Backup destination that a schedule still uses cannot be deleted. The error
  names the Databases. A destination that only holds copies can be deleted
  after a confirmation that says how many: the copies stay in the bucket and
  the panel stops tracking them (phase 3 decision).
- **Restoring from a destination** is not offered in phase 3 (phase 3
  decision): download the object from the bucket and use Restore from file.

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
- **Read-only by default.** The session starts read-only
  (`default_transaction_read_only=on` for Postgres, `transaction_read_only=ON`
  for MySQL and MariaDB). The **Allow writes** switch is per page view and
  resets on reload.
  - Probed in phase 4 planning on pg 18, mysql 8.4 and mariadb 11.8: INSERT,
    CREATE/ALTER/DROP/TRUNCATE TABLE, CREATE/DROP DATABASE, CREATE USER, GRANT,
    `nextval`, `DO` blocks, functions and procedures that write, and
    `COMMIT; INSERT …` are all refused. `VACUUM` runs.
  - It is a guard against accidents, not a security boundary (phase 4
    decision): an Admin is a superuser and can type `BEGIN READ WRITE`
    (Postgres) or `SET SESSION transaction_read_only=OFF` (MySQL).
- **Batches** (phase 4 decision):
  - **Postgres** sends the text as one simple-protocol query. The server splits
    it, and runs it as one transaction unless it holds its own `BEGIN` /
    `COMMIT`, so an error stops the batch and undoes it.
  - **MySQL and MariaDB** are split by the panel the way the `mysql` client
    does: quotes, backticks, `--`/`#`/`/* */` comments, and `DELIMITER` lines.
    Each statement commits on its own; an error stops the batch, and the
    statements before it stay.
- **Limits.**
  - Timeout of 30 s for the whole batch: the context, plus `statement_timeout`
    (Postgres), `max_statement_time` (MariaDB) or `max_execution_time` (MySQL,
    which covers SELECT only; the rest relies on the context and `KILL QUERY`).
  - At most 1,000 rows returned per result, with a "truncated" flag.
  - Cell values are cut at 64 KB for display.
  - At most 20 results and 16 MB of results per batch (phase 4 decision).
  - The request body is at most 1 MB.
  - **Reaching a limit stops the batch** (phase 4 decision): the panel cancels
    the statement on the server and closes the connection, rather than reading
    millions of rows it would throw away. The response says why it stopped. On
    Postgres with writes allowed, that also undoes the batch's earlier
    statements, and the response says so.
- **Cells** are strings or null, never JSON numbers, so 64-bit integers and
  numerics keep every digit. A value that is not valid UTF-8 is sent as hex
  and shown with a marker (phase 4 decision).
- **Errors** show the server's message. Postgres gives a position in the text;
  MySQL gives a line inside one statement, so the response names the statement
  and where it starts, and the editor marks it.
- **Cancel** aborts the HTTP request. The request context is cancelled, and
  the panel cancels the query on the server: pgx's cancel-request context
  handler for Postgres, `KILL QUERY <connection id>` on a second connection for
  MySQL and MariaDB (phase 4 decision: neither driver does it by default).
- **CSV export** of the displayed result happens in the browser. Bulk export is
  what Backups are for.
- **History** of the last 50 queries per Database is kept in the browser's
  `localStorage` (a per-viewer convenience).

**Redis:**

- **Console** that splits arguments the way `redis-cli` does, quotes included.
- **Key browser** that uses `SCAN` with a pattern. It shows type and TTL, and
  shows the value by type (string, hash, list, set, zset), capped at 1,000
  elements.
- **Read-only by default** (phase 4 decision, after probing `COMMAND INFO` on
  Redis 8 and 7.4: `EVAL`, `FCALL`, `CONFIG`, `SCRIPT`, `FUNCTION` and `ACL`
  carry no `write` flag). With writes off, only a command or subcommand whose
  flags include `readonly` runs, plus `PING`, `ECHO`, `INFO`, `TIME`,
  `COMMAND …`, `CONFIG GET`, `CLIENT LIST`, `CLIENT INFO`, `SLOWLOG GET`,
  `SLOWLOG LEN`, `MEMORY USAGE`, `MEMORY STATS` and `LATENCY LATEST`.
  Anything the server does not know is refused.
- **Never in the console**, writes or not: `SUBSCRIBE` and the other pub/sub
  subscriptions, `MONITOR`, `SYNC`, `PSYNC`, `MULTI`, `EXEC`, `DISCARD`,
  `WATCH`, `UNWATCH`, `SELECT` (use the database selector), `AUTH`, `HELLO`,
  `RESET`, `QUIT` and `CLIENT REPLY`.
- **Dangerous commands** need a second confirmation even when writes are
  allowed, checked by the server: `FLUSHALL`, `FLUSHDB`, `CONFIG SET`,
  `SHUTDOWN`, `DEBUG`, `SCRIPT FLUSH`, `FUNCTION FLUSH`, and (phase 4
  decision) `SWAPDB`, `REPLICAOF`, `SLAVEOF`, `FAILOVER`, `MODULE LOAD`,
  `MODULE UNLOAD`, `ACL SETUSER` and `ACL DELUSER`.

**Audit.** Every run records:

- who;
- which Database and logical database;
- the statement text (cut at 4 KB), with passwords replaced by `***` (phase 4
  decision): the quoted value after `PASSWORD` or `IDENTIFIED … BY`, and in
  Redis the arguments of `AUTH`, `CONFIG SET requirepass|masterauth`,
  `MIGRATE … AUTH|AUTH2` and `ACL SETUSER` rules starting with `>` or `#`;
- whether writes were allowed;
- duration, row count and error.

Every run is recorded, including refused, failed, stopped and cancelled ones
(phase 4 decision): a run answers 200 with its outcome in the body, so the
audit middleware records it even when the browser has gone.

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
  - `id`, `database_id`, `trigger`, `logical_name` (the one database in the
    file; `scope` is left from the first phase 2 draft and no longer read);
  - `status`, `error`, `log`, `file_name`, `size`, `sha256`, `pinned` and `local_removed_at` (phase 3);
  - `started_at`, `finished_at`.
- `database_restores` (phase 2): `id`, `database_id`, `source` (`backup` or `upload`), `backup_id`, `pre_backup_id`, `upload_name`, `target`, `status`, `error`, `log`, `started_at`, `finished_at`.
- `database_backup_schedules` (one per Database):
  - `enabled`, `days`, `minute`, `databases` (JSON: all, or a list of names),
    `destination_id`, `last_error`;
  - `local_keep_count`, `local_keep_days`, `remote_keep_count`,
    `remote_keep_days`;
  - `last_run_at`.
- `database_backup_uploads`:
  - `backup_id`, `destination_id`, `object_key`;
  - `status`, `error`, `uploaded_at`, `deleted_at`.
- `backup_destinations`:
  - `id`, `name`, `type`, `config` (JSON, without secrets), `secret`;
  - `last_test_at`, `last_test_ok`, `last_test_error`, `created_at`.

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
  - Each driver's dump and restore command for one logical database, and the upload check.
  - Connection URL encoding (passwords with `@ : / ? # %`).
  - Rotation as a table test: count, age, both, newest-successful kept, pinned,
    manual, pre_restore.
  - The shared schedule package (existing Registry tests move with it).
  - The Redis read-only filter and the dangerous-command list.
  - The Redis argument splitter.
- **Integration in the dind sandbox** (`task e2e:serve`), for each SQL engine:
  1. create;
  2. write data;
  3. back up all databases and a selected one;
  4. delete data;
  5. restore;
  6. check the data is back;
  7. check that read-only refuses an `INSERT`.

  For Redis: create, `SET` with writes allowed, check that read-only refuses a
  `SET`, then restart and check the key survived.

  For S3, run against a MinIO container in the sandbox.
  - **Images (phase 3 decision).** The upstream `minio/minio` and `minio/mc`
    images are no longer published on Docker Hub or Quay (checked 2026-11-15).
    The newest pullable releases are the Bitnami legacy mirror's:
    `bitnamilegacy/minio:2025.7.23-debian-12-r5` (MinIO
    `RELEASE.2025-07-23T15-54-02Z`) and
    `bitnamilegacy/minio-client:2025.7.21-debian-12-r3` (mc
    `RELEASE.2025-07-21T05-28-08Z`). Pinned in `e2e/tests/backup-destinations.spec.ts`.
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
   - Per-database Backups (all or selected).
   - Download and upload, with the upload check.
   - Restore into a picked target.
   - `pre_restore` and `pre_update` Backups.
   - The deleted-Database Backups view.
3. **Schedule, Rotation and Backup destinations (S3).**
   - Moving the schedule rules into a shared package.
   - Upload and retry.
   - Remote rotation.
4. **Query panel.** SQL and Redis, plus audit. Shipped as two stacked PRs:
   SQL (4a), then Redis (4b).
