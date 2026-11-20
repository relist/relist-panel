# Backups keep their database's state

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

**Considered and rejected:**

- **A schema-only or data-only restore, as in pgAdmin.** Backups are plain
  SQL, so either one would mean filtering `COPY` or `INSERT` blocks out of
  the stream as it is restored. That is fragile, and nobody has asked for it.
- **Restoring the ACL as it was.** A Backup restored under a new name is a
  copy, and opening the copy to the original's grantees is not what
  "restore" says. The new database stays restricted. Recreate restores the
  users' access on purpose.

**Consequences:** each Backup runs one more admin query. Backups taken
before `00055` restore into a new database owned by `postgres`, as before.
