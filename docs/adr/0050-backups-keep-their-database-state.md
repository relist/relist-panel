# Backups keep their database's state, and a lost volume is recreated from them

A Backup is a one-database dump: `pg_dump` without `--create`, or
`mysqldump` of one schema. It carries no `CREATE DATABASE`, so no owner, no
ACL, and no character set. A replace Restore reads those from the live
database before it drops it (ADR-0047). A restore into a new database had
nothing to read, so Postgres made `postgres` the owner while the app's user
still owned every table. The Users tab then refused both ways out: Read-only
(409, the user owns objects there) and Full (422, a superuser owns the
database).

**Decision:**

- **Every Backup reads its database's state before the dump** and stores it
  as JSON in `database_backups.state` (migration `00055`): InspectSQL's line,
  which is the owner and ACL on Postgres, or the character set and collation
  on MySQL and MariaDB. The read is best effort. A Backup never fails for it,
  and a Backup without it restores the old way. It runs through the same
  `docker exec` admin command as the dump, so the `pre_update` Backups a
  version change takes after cutting clients off get it too.
- **Restore into a new database offers "as it was backed up"**, ticked by
  default. On Postgres the new database gets the Backup's owner, and stays
  restricted like Create database. On MySQL and MariaDB it gets the Backup's
  character set and collation. The owner is used only while the role exists
  and is no superuser, because a superuser owner would run the owner's code
  as one (ADR-0048). Otherwise `postgres` owns the database, and the
  restore's log says why. A replace Restore is unchanged: it keeps the live
  database's own state.
- **The Users tab names the fix for a database restored before this**:
  `ALTER DATABASE … OWNER TO …`, run in the Query tab.
- **The state also holds each login user's access** (`full` or `read`, the
  Users tab's rows for that database), read in the same admin session.
  A one-database dump carries no `CONNECT`, no membership in the owner role,
  and on MySQL and MariaDB no schema grant at all, so without it a
  recreated app user would exist but reach nothing.
- **Recreate and restore** is the explicit action ADR-0039 left for a
  `volume_missing` Database. Reconcile still never recreates a volume on
  its own. The Admin picks one Backup per database (only Backups still on
  disk), types the name, and a `recreate` job:
  1. makes the container on an empty volume with the Database's password,
     so its hosts and credentials stay the same, and waits for it to be
     healthy;
  2. creates again, with their stored passwords, the users the panel made
     (`database_users`);
  3. restores each Backup as it was backed up, with its owner when that
     user is back;
  4. gives each of those users the access its Backup recorded, through the
     Users tab's own code.

  Every check runs before anything is created: the volume is really gone,
  the name matches, each Backup succeeded and its file is unchanged, and no
  database is picked twice. A step that fails is kept on its Restore row and
  in the last error, and nothing is rolled back: a running server with some
  databases back beats none. Users made outside the panel cannot come back,
  because the panel has no password for them; a restore whose objects
  belong to one fails on the missing role, as any restore does. Redis keeps
  no Backups, so it is deleted instead.

**Considered and rejected:**

- **A schema-only or data-only restore, as in pgAdmin.** Backups are plain
  SQL, so either one would mean filtering `COPY` or `INSERT` blocks out of
  the stream as it is restored. That is fragile, and nobody has asked for it.
- **Restoring the ACL as it was.** A Backup restored under a new name is a
  copy, and opening the copy to the original's grantees is not what
  "restore" says. The new database stays restricted. Recreate restores the
  users' access on purpose.
- **Recreating an empty volume by itself in Reconcile.** It would look like
  the data was never there (ADR-0039).
- **Keeping a roles dump with every Backup**, as a version change does. It
  would bring back users made outside the panel too, but it holds every
  role's password hash beside each Backup, and the panel already keeps the
  passwords of the users it made.

**Consequences:** each Backup runs one more admin session (two queries). Backups taken
before `00055` restore into a new database owned by `postgres`, as before.
