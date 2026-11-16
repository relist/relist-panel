# Logical databases, users and the superuser password

Phases 1 to 6a gave every Database one superuser, and every app that used one connected as it. Phase 7 lets an Admin make a database and a user for each app (`.scratch/databases/plan-phase-7.md`), and reset the superuser password of any Database, Redis included. It amends ADR-0039, whose maintenance went through `docker exec` only.

**Decision:**
- **One admin path, over the network.** Every phase 7 statement goes through `internal/dbquery` admin functions that connect as the superuser to `relist-db-<id>` on `relist-network`, as the Query panel does. Two reasons. A password probe has to log in over the network: Postgres trusts its local socket and MariaDB's `root@localhost` accepts `unix_socket`, so a `docker exec` proves nothing about a password. And Postgres grants inside one database need a connection to that database. Backup and restore stay on `docker exec`.
- **No password in a log.** Admin connections to Postgres set `log_min_error_statement=panic` and `log_statement=none`, because a failed `CREATE ROLE … PASSWORD '…'` is otherwise written to the container log and shown on the Logs tab. Every error from an admin call is scrubbed of the superuser's password and of any password generated in that call before it is returned, stored in `LastError` or audited. Audit details carry names only.
- **Managed users.** A user the panel created or reset keeps its password in `database_users` (`database_id`, `name`, `password`, `created_at`, migration `00050`). Revealing it is audited as a secret read. A user made elsewhere shows "password unknown" until Reset password. Only Postgres roles that can log in, and MySQL / MariaDB accounts on host `%`, are listed.
- **Access levels.** Postgres Full is the database's owner, or membership in the owner role (`GRANT <owner> TO <user>`). That is refused on a database a superuser owns, because `GRANT postgres TO app` hands the user everything `postgres` owns in every database; such a database offers None and Read-only only, and Create database with an owning user is the way to get an app its own. The owning user stays at Full. Read-only is `CONNECT`, `USAGE`, `SELECT` on tables and sequences of every schema, and default privileges for tables the owner creates later. A new Postgres database has `CONNECT` revoked from `PUBLIC`; older ones keep the default and are marked **Any user can connect** with a **Restrict** button.
- **MySQL / MariaDB `\_`.** Schema grants treat `_` and `%` as wildcards, so ``GRANT … ON `app_1`.*`` would also reach `appx1`. Every schema grant escapes `_` as `\_` inside the backquotes, and the grants read back are un-escaped. Names cannot hold `%` or `\`. Dropping a database revokes every user's grants on it first, because a schema grant outlives the schema and would apply again to a new database of that name.
- **A replace-restore keeps the ACL.** `ReplaceSQL` drops and recreates the target, and a one-database `pg_dump` carries no database ACL. The restore reads `datacl` before it drops, and grants it back (`CONNECT`, `CREATE`, `TEMPORARY` per grantee, `PUBLIC` included). Restore into a new database is restricted like Create database.
- **The superuser reset is a job, in an order a crash cannot turn into a lock-out.** (1) store the new password as pending beside the current one (`databases.pending_password`); (2) set it live (Postgres `ALTER ROLE postgres`, MySQL / MariaDB one `ALTER USER` for `root@'%'` and `root@'localhost'`, Redis `CONFIG SET requirepass`) and prove it by logging in with it; (3) make it current and clear pending; (4) recreate the container with the new environment by rewriting only the `Env` of the stored applied spec, since reconcile reads the applied spec and never `specFor`. If step 2 fails and the new password does not log in, pending is cleared and nothing changed. At panel start, and when the Database is started, a Database with a pending password is probed with both: the new one working makes it current and runs step 4, the old one working drops the pending one, neither keeps it pending with a warning on the Database. The probe waits for the container, up to two minutes.
- **A pre-drop Backup.** Drop database has Back up first on by default, as Update image does: a `pre_drop` Backup, never rotated, and a failure stops the drop. A dropped database is also removed from the Backup schedule's named list, or every later run would fail with "no such database".

## Considered options

**`docker exec` for every statement.** Rejected: it cannot prove a password, and it cannot grant inside a database without a second client per database.

**Full as `GRANT postgres TO app` on superuser-owned databases.** Rejected: it makes the user a peer of the superuser.

**Setting pending after the live change.** Rejected: a crash between the two leaves a password only the server knows.

## Consequences

- The panel stores users' passwords, like the superuser's; the panel database is already the root of trust (ADR-0039).
- Every Database made before phase 7 keeps Postgres's default (any user may connect) until restricted.
- A reset restarts the Database once, briefly, and every app using the superuser password stops connecting until it is given the new one.
- Delete user does not yet ask about panel users that use it; phase 8 adds that confirmation.
