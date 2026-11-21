# Spec: Databases, part 2

Date: 2026-11-15.
Status: ready-for-human

Follows `.scratch/databases/spec.md` (phases 1–4, PRs #80–#86). Everything
there still holds unless a section below says it changes. Each phase is one
implementation plan and one PR, stacked on the one before.

## Problem

Phases 1–4 give an Admin Databases, Backups, S3 copies and a Query panel. What
is still missing, in the operator's order of value:

- desktop tools (DBeaver, TablePlus) cannot reach a Database, because no host
  port is ever opened;
- the only off-site destination is S3, while many small operators have an SFTP
  or WebDAV box (Hetzner Storage Box, Synology, Nextcloud);
- every app connects as the superuser, because the panel cannot create a
  database and a user for it, and the superuser password can never change;
- only Admins can see a Database at all;
- Rotation is "last N / N days" only, copies leave the server unencrypted, and
  a new major version means doing backup, create and restore by hand.

## Decisions taken while planning (2026-11-15)

- **Dropped for good:** Redis backup and restore, and Attach (tools for a
  database Service inside a Compose). Both leave `spec.md`'s Out of scope.
- **Still out of scope:** failure notifications (the panel has no notification
  system; that needs its own spec covering Deployments and Certificates too),
  point-in-time recovery, grid editing and schema UI, more engines.
- **Added to phase 8 (2026-11-15):** a **Preview** (dry-run) of
  data-changing statements in the Query panel.

## Phases

5. Direct access (a Database on a host port first).
6. SFTP (6a) and WebDAV (6b) Backup destinations.
7. Logical databases and users, and superuser password reset.
8. Per-Database access for non-Admins.
9. GFS Rotation (9a) and backup encryption (9b).
10. Change major version.

---

## Phase 5: Direct access

A **Direct access** is a way into one container's port that skips Traefik:
a small proxy the panel runs, with a **target**, a **channel** and an
optional expiry. The panel checks nothing on the channel itself; whoever can
reach the channel can use the target as if they were on its network. The
channel's own control (SSH to the host, a tailnet ACL, a Cloudflare Access
policy) is the guard.

Phase 5 ships one target kind and one channel: a Database, published on a host
port for desktop tools over an SSH tunnel. The model is built so later phases
add kinds and channels without a new table. This withdraws `spec.md`'s
success criterion "No host port is opened for any Database" for this one
timed, Admin-opened case; ADR-0041 amends ADR-0039.

### The model

- **Target** (`target_kind`, `target_id`, `target_port`):
  - `database` (phase 5): `relist-db-<id>` on `relist-network`, at the
    engine's port.
  - Later: `service`, a Service in a Compose, reached by joining that
    Compose's network (for example an API without its Domain's auth).
- **Channel** (`channel`, `channel_config` JSON):
  - `host_port` (phase 5): `{"bind": "127.0.0.1" | "0.0.0.0", "hostPort": n}`.
  - Later: `tailscale` (the proxy joins a tailnet; no host port) and
    `cloudflare` (through `cloudflared`, for WARP or Zero Trust).
- **Expiry** (`expires_at`, nullable). A `host_port` channel always has one;
  only later channels with their own access control may be permanent.
- **Table** `direct_accesses`: `id`, `target_kind`, `target_id`,
  `target_port`, `channel`, `channel_config`, `expires_at`, `opened_by` (user
  ID), `opened_at`. It is the panel's truth, because Extend cannot change a
  container's labels.
- **Names** come from the access's own ID, so one target may have several
  accesses later: container `relist-access-<id>`, labels
  `relist.access.id=<id>`, `relist.access.target=<kind>:<id>` and
  `relist.access.expires=<unix>` (informational).
  The program is `relist-proxy`.

### Phase 5 rules (target `database`, channel `host_port`)

- **The proxy.** The container runs the panel's own image, resolved the way the
  Volume helper resolves it. Its entrypoint is `relist-proxy`, not a
  `relist-volhelper` subcommand: the Volume helper has no network by design
  (ADR-0010).
  - `relist-proxy` forwards every accepted connection to one fixed target
    given in argv. It has no other mode.
  - At most 64 connections at once; the 65th is closed at accept.
  - The container joins `relist-network`, has `AutoRemove`, restart policy
    `no`, a 32 MiB memory limit, a PID limit, no capabilities and a read-only
    root filesystem.
- **One Direct access per Database** in phase 5, as a rule in the service, not
  a table constraint. Opening a second answers 409.
- **Bind.** `127.0.0.1` by default: the Admin reaches it with
  `ssh -N -L <local port>:127.0.0.1:<host port> <user>@<server>`. **All
  interfaces** (`0.0.0.0`) is a tick box with a red warning: Docker's published
  ports bypass host firewalls such as UFW, so the Database is on the internet
  with only its password.
- **Port.** Docker picks a free port by default and the panel reads it back.
  The Admin may type one (1024–65535); a port in use fails with Docker's error.
- **Duration.** 15 minutes, 1 hour (default), 4 hours or 8 hours.
  - **Extend** sets the expiry to now + the chosen duration, at most 8 hours
    from now. It does not restart the proxy or drop connections: the panel
    runs `relist-proxy extend <unix time>` in the container, which writes the
    new deadline to a file the proxy rereads every few seconds.
  - **Close now** removes the container.
  - **At expiry** the proxy closes its listener and every open connection and
    exits by itself, so it closes on time even when the panel is down.
    `AutoRemove` removes the container.
- **Who and what.** Admin only. Open (with target, channel, bind, port and
  duration), Extend and Close are audited. Expiry is not.
- **What the page shows.** A **Direct access** card on the Database's
  Connection tab: state, bind address, host port, time left, the `ssh -L`
  command (server name from the browser's address, user as a placeholder),
  and the connection URL with `127.0.0.1:<local port>`. A host-wide Direct
  access page listing every access comes with the second target kind or
  channel.
- **Lifecycle around it.**
  - Deleting a Database closes its Direct accesses first.
  - Stop, Restart, Update image, Repair and restore leave them alone; the
    target name is stable, so connections fail only while the Database is
    down.
  - Opening is refused while a server Migration holds its lock. Direct
    accesses are not carried by a Migration: the old host's proxies expire
    there.
  - At panel start and on each scheduler tick the panel reconciles both ways:
    a container with no row, or a row past `expires_at`: the container is
    removed and the row deleted; a row with no running container: the row is
    deleted.
  - Docker cleanup needs no change: the proxy runs the panel's own image, which
    is in use, and `AutoRemove` leaves no stopped container to prune.

## Phase 6: SFTP and WebDAV Backup destinations

Two more `type`s of Backup destination. Everything phase 3 decided for S3
holds for them: the object key layout `<path>/<db slug>-<db id>/<logical>/<file>`,
deleting only recorded copies, the size check after upload, the 5-minute stall
watchdog, the 1-minute timeouts on Delete and Test, and the secret rules (never
sent back; changing the host or URL asks for it again).

### 6a: SFTP

- **Settings:** host, port (default 22), user, base path, and one of:
  - an **SSH key from the SSH keys library** (recommended; the page shows its
    public key to add to `authorized_keys`), or
  - a password.

  The SSH keys page lists the destinations that use a key, and a used key
  cannot be deleted, as with Compose sources.
- **Host key: trust on first use.** The first successful Test records the
  server's host key fingerprint on the destination, and the form shows it.
  Every later connection must present the same key, or it fails with "the
  server's host key changed". **Reset host key** forgets it; the next Test
  records the new one. An upload never records a key: a destination that has
  never passed Test cannot be used by a schedule.
- **Upload** writes `<file>.part`, checks its size, then renames it to the final
  name (the `posix-rename` extension when the server has it, else remove and
  rename). A failed upload removes the `.part`. Missing directories are made.
- **Library:** `github.com/pkg/sftp` on `golang.org/x/crypto/ssh` (already a
  dependency).

### 6b: WebDAV

- **Settings:** URL (`http://` or `https://`, path allowed), user, password.
- **Client:** written on `net/http`, no dependency. It needs only `MKCOL`,
  `PUT` (with `Content-Length`), `PROPFIND` (depth 0, for the size check),
  `DELETE` and `GET` (for Test). `MKCOL` answering 405 means the collection
  exists.
- **Upload** `PUT`s to the final name, then checks the size with `PROPFIND`;
  a wrong size deletes the object and fails the upload.
- Tested against Nextcloud's and Apache's WebDAV in the sandbox (images pinned
  at plan time).

## Phase 7: Logical databases and users

SQL engines only. Redis has neither tab.

### Databases tab

- Lists the logical databases (the same list Backups use) with size, and owner
  (Postgres) or character set (MySQL / MariaDB).
- **Create database:** a name (the restore rule: letters, digits, `_` and `-`,
  at most 63, not starting with `-`) and a tick box **Create a user that owns
  it**, on by default. With the tick, one step makes the database and a user
  of the same name with a generated password and Full access to it.
  - Postgres: a new database gets `REVOKE CONNECT ON DATABASE … FROM PUBLIC`,
    so one app's user cannot connect to another app's database. So does
    Restore into a new database.
  - Postgres databases made any other way (before phase 7, in the Query panel,
    and `postgres` itself) keep Postgres's default, where every user may
    connect. The tab marks them **Any user can connect**, with a **Restrict**
    button that runs the same `REVOKE`. A user still sees no tables there
    without grants.
- **Drop database:** type its name to confirm; **Back up first** is ticked by
  default, as in Update image. System and template databases are never
  offered.

### Users tab

- Lists the engine's login users, live, minus system ones (Postgres: roles
  starting `pg_`, and `postgres`; MySQL / MariaDB: `root`, `mysql.sys`,
  `mysql.session`, `mysql.infoschema`, `mariadb.sys`, `healthcheck`).
- **Managed users.** A user the panel created or reset has its password stored
  in the panel database (new table `database_users`: `database_id`, `name`,
  `password`, `created_at`). Revealing or copying it is audited. A user made
  outside the panel shows "password unknown"; **Reset password** generates one
  and from then on the panel manages it.
- **Create user:** a name; the password is always generated (43 characters, as
  the superuser's). MySQL / MariaDB users are `'name'@'%'`.
- **Access** per logical database: **None**, **Read-only** or **Full**. Saving
  re-applies the grants, which also picks up schemas added since.
  - Postgres Full: the database's owner, or for a second user membership in
    the owner role (`GRANT <owner> TO <user>`). The dialog says that a member
    gets everything the owner role has, in every database it owns.
  - Postgres Read-only: `CONNECT`, `USAGE` on every schema, `SELECT` on every
    table and sequence, and `ALTER DEFAULT PRIVILEGES FOR ROLE <owner>` to grant
    `SELECT` on tables the owner creates later.
  - MySQL / MariaDB Full: `GRANT ALL ON \`db\`.*`. Read-only: `GRANT SELECT, SHOW
    VIEW ON \`db\`.*`.
- **Delete user:** refused while it owns a database or objects (Postgres), with
  the error naming them. Deleting a user that phase 8 access uses asks for
  confirmation listing those panel users.
- **The Connection tab** gains a user picker: the superuser, and every managed
  user, each with its own URL and Compose snippet.

### Superuser password reset

- On the Connection tab, behind typing the Database name. It runs as a job.
- **Order, so a crash never locks the panel out of its own Database:**
  1. store the new password as **pending** beside the current one;
  2. set it live (Postgres `ALTER ROLE postgres`; MySQL / MariaDB `ALTER
     USER` for `root@'%'` and `root@'localhost'`; Redis `CONFIG SET
     requirepass`);
  3. make it the current password and clear pending;
  4. recreate the container with the new environment.

  At panel start, a Database with a pending password is probed with both; the
  one that works becomes current, and step 4 runs if it was the new one. The MySQL healthcheck and Redis's startup read the password from
  the environment, so a recreate is needed; the Database restarts once,
  briefly.
- The dialog says that every app using the superuser password stops connecting
  until it is given the new one, and that the Database restarts.
- Audited, without the password.

### Restore and users

Unchanged from phase 2: Backups carry no users. A restore whose objects are
owned by a user the target lacks fails with `role "x" does not exist`; the
Admin creates the user on the Users tab and restores again. The restore error
links to the Users tab.

## Phase 8: Per-Database access for non-Admins

An Admin gives a panel user (not an Admin) access to one Database. Security
comes from the database's own permissions, not from filtering statements:
filtering cannot stop a superuser session (`DO … EXECUTE` built from strings,
`SELECT pg_read_file(…)`, `PREPARE … FROM CONCAT(…)`), so a non-Admin never
gets a superuser session.

- **SQL engines:** the grant names a **managed user** from phase 7, for
  example "Alice uses `shop` as `shop_ro`". Everything Alice does runs as
  `shop_ro`; what she can change is what `shop_ro` can change. No hidden user
  is created.
- **Redis:** the grant has a level, **Read** or **Read-write**. The console
  connects as the superuser, and the existing classifier is the boundary,
  which is sound for Redis: every command is one discrete name, and the only
  ways to run built-up commands (`EVAL`, `FCALL`, `FUNCTION`, `MODULE`) are
  never allowed in the console.
  - Read: only the read-only allowlist.
  - Read-write: read and write commands; the dangerous list stays refused,
    with no confirm override.
- **What a non-Admin sees:** the **Data → Databases** menu with only their
  Databases, and on each: status and the Query panel (SQL as their user;
  Redis at their level). An SQL grantee also gets the Connection tab for their
  user: the URL and the password, revealing audited. A Redis grantee gets no
  Connection tab, because the only Redis credential is the superuser's, which
  a non-Admin never sees. Logs, Backups, restore,
  Start / Stop, Update image, users, Direct access and settings stay
  Admin-only.
- **Storage:** new table `database_grants`: `database_id`, `user_id`,
  `db_user` (SQL) or `level` (Redis), `created_at`. One grant per user per
  Database.
- **Management:** an **Access** tab on the Database page (Admin only) lists the
  grants and adds or removes them. Grant changes are audited.
- When the managed user is deleted or the Database is deleted, its grants go
  with it. A password reset of the managed user needs nothing: the panel
  always connects with the stored password.
- Every Query panel run is audited as today, with the panel user and the DB
  user.

### Preview (dry-run) in the SQL Query panel

For Admins and grantees alike. It is a guard against mistakes, not a security
boundary: security is the DB user's permissions above.

- **Preview** sits beside Run when writes are allowed. It runs the batch in one
  transaction, reports each statement's affected rows ("would delete 3,412 rows
  from `orders`"), then rolls back. The person then clicks Run to do it.
- **Only when every statement starts with `INSERT`, `UPDATE`, `DELETE` or
  (MySQL / MariaDB) `REPLACE`.** Otherwise Preview is refused before anything
  runs, naming the first other statement. DDL is excluded because MySQL
  commits it implicitly.
- **MySQL / MariaDB:** refused while any non-system schema holds a table whose
  engine is not transactional (not InnoDB), listing up to five, because
  MyISAM and Aria changes would not roll back.
- **The page says what Preview cannot undo:** sequence values, and anything a
  trigger or function does outside the database's tables.
- The same limits, timeout and cancel as Run. Every Preview is audited with
  `outcome: preview`.

## Phase 9: Rotation tiers and encryption

### 9a: GFS Rotation

- Each Rotation set (Local, Destination) picks a mode:
  - **Simple:** today's keep last N / keep at most X days.
  - **GFS:** keep last N, and the newest Backup of each of the last D days,
    W weeks (ISO weeks), M months and Y years, in the host's timezone. Each
    field is optional; blank keeps nothing for that tier.
- A Backup is kept when any GFS rule keeps it. The existing rules still hold:
  only scheduled, unpinned Backups rotate, counting is per logical database,
  and the newest successful Backup is always kept.
- Failed Backups never fill a GFS slot.
- `Rotate` stays a pure function; GFS is a table test like Simple.
- New columns in a new migration; existing schedules are Simple.

### 9b: Backup encryption

- **On the Backup destination:** a tick box **Encrypt copies with a
  passphrase**. Only copies sent to that destination are encrypted; local
  Backups stay plain so they restore directly.
- **Format: age with a scrypt passphrase** (`filippo.io/age`). A copy is
  `<file>.age` and opens anywhere with `age -d`.
- **Work factor 2^15** (32 MiB of memory, about 0.1 s), not age's default
  2^18 (256 MiB): the panel container runs with `mem_limit: 256M` and
  `GOMEMLIMIT: 200MiB`, so the default would have it killed. Only one scrypt
  derivation runs at a time in the whole panel. A test asserts the memory a
  derivation adds.
- **Passphrase.** Generated by default (and may be typed instead, at least 20
  characters). Stored in the panel database so scheduled uploads work;
  revealing it is audited. The form says in bold that the panel cannot help if
  the server is lost: keep a copy elsewhere, or every copy is unreadable.
- **Encrypt then upload.** The copy is encrypted into a temporary file beside
  the Backup, uploaded with its exact size, and the temporary file removed.
- **Changing the passphrase or turning encryption off** applies to new copies
  only. The dialog says existing copies keep the old passphrase and the panel
  forgets it. Each copy records whether it is encrypted.
- **Restore from file** accepts a `.age` file (detected by its header) and asks
  for the passphrase. The decrypted stream goes through the same upload check
  as a plain file before anything runs. A file whose scrypt work factor is above 2^15 is refused
  before any derivation ("decrypt it with `age -d` first"), so an upload cannot
  make the panel allocate gigabytes.

## Phase 10: Change major version

**Change version** on the Database page, for any other tag the engine offers
that is higher than the current one. Downgrades are never offered.

- **Postgres** (on-disk format changes between majors, `16` → `18`): dump and
  restore, in place for everyone who connects.
  1. A `pre_update` Backup of every logical database, plus the roles
     (`pg_dumpall --roles-only`, kept beside them; the role of `postgres`
     itself is skipped on restore).
  2. Stop the container.
  3. Create a new container on a **new volume** `relist-db-<id>-v<n>` with the
     new image. The Database keeps its ID, name, hosts and password, so no app
     changes its config.
  4. Restore the roles, then each logical database.
  5. On success the Database records the new volume. The old volume is kept and
     listed as "previous version data" with a Delete button.
  6. On any failure the new container and volume are removed and the old
     container is started again; the Database is as before, and the log says
     which step failed.
- **MariaDB (11.4 → 11.8) and Redis (7.4 → 8):** the image changes in place
  with Update image's flow, because both read the older format; MariaDB gets
  `MARIADB_AUTO_UPGRADE=1` for that run. **Back up first** is ticked by default.
- The volume name becomes a stored column (new migration), defaulting to
  `relist-db-<id>` for existing Databases.
- Runs as a job; the Database is unavailable from step 2 to the end.

## Testing

Each phase follows `spec.md`'s Testing section: unit tests for pure parts,
integration in the dind sandbox, and a Playwright spec through the panel URL.
Phase-specific checks:

- **5:** connect through the Direct access proxy with each engine's client; Extend keeps an
  open connection; expiry closes open connections; the proxy exits on its own
  with the panel stopped; a restart of the panel removes an expired proxy.
- **6:** SFTP against an OpenSSH server container with key and with password,
  including a changed host key; WebDAV against two servers.
- **7:** a Read-only user cannot write, a Full user can, and one app's user
  cannot connect to another's database (Postgres); superuser reset keeps
  data and the URL works with the new password.
- **8:** a non-Admin cannot run `pg_read_file`, `COPY … TO PROGRAM`, `SELECT …
  INTO OUTFILE`, or reach another Database's page or API; Redis Read refuses
  `SET`.
- **9:** GFS table test; an encrypted copy opens with the `age` CLI; an upload
  with work factor 2^22 is refused without the memory spike.
- **10:** Postgres 16 → 18 with data, roles and a managed user; a forced
  failure at step 4 leaves the Database on 16 with its data.
