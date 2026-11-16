# Per-Database access for non-Admins, and Preview

Through phase 7 only Admins saw a Database at all. Phase 8
(`.scratch/databases/plan-phase-8.md`) lets an Admin give a panel user who is
not an Admin access to one Database, worked in its Query panel or Redis
console, and adds **Preview**, a dry run of data-changing SQL, for Admins and
grantees alike.

**Decision:**

- **The grant model.** One row per panel user per Database
  (`database_grants`, migration `00051`): on a SQL engine the grant names a
  **managed user** from phase 7 ("Alice uses `shop` as `shop_ro`"), on Redis
  it holds a **level**, Read or Read-write. No hidden database user is
  created. The row goes away with the panel user, the managed user or the
  Database; a password reset of the managed user keeps it. Only non-Admins
  can hold one: an Admin already reaches everything.
- **The database's own permissions are the boundary, not statement
  filtering.** Filtering cannot stop a superuser session (`DO … EXECUTE`
  built from strings, `SELECT pg_read_file(…)`, `PREPARE … FROM CONCAT(…)`),
  so a non-Admin never gets one: every SQL run of a grantee connects as its
  managed user with the stored password, and no grantee path ever builds a
  connection with the superuser password. authz admits the request
  (`LevelDatabase` reads `database_grants`), and the handler turns the grant
  into the caller; a non-Admin without a grant is 404, never the superuser.
- **The server-wide check.** A managed user with powers beyond its own
  databases cannot back a grant: Postgres superuser, `CREATEROLE`, or
  membership in `pg_read_server_files`, `pg_write_server_files` or
  `pg_execute_server_program`; MySQL / MariaDB any `SHOW GRANTS` line other
  than `USAGE ON *.*` or a one-schema grant, or any `WITH GRANT OPTION`.
  The check runs at grant time (422) over the admin path, and again on the
  run's own session before the first statement, because privileges change
  later. The Postgres query reads `session_user`, never `current_user`:
  phase 7's per-database `SET role` makes `current_user` the owner from the
  first statement on, which would let a superuser login pass. The grant-time
  check needs the Database running; that is why a SQL grant does.
- **The Redis console for grantees is stricter than its level.** On top of
  the writes classifier, everything Dangerous, flagged `admin`, or in the
  `@admin` or `@dangerous` ACL categories is refused outright, with no
  confirmation: `CONFIG GET` answers the superuser password, and the spec's
  Read allowlist would have allowed it. This also takes `INFO`, `KEYS`,
  `CLIENT LIST`, `SLOWLOG` and `LATENCY` from grantees; the key browser uses
  `SCAN` and still works. A Redis grantee has no Connection tab: the only
  Redis credential is the superuser's.
- **Scripts are refused for everyone, Admins included.** `EVAL` and friends,
  `FCALL`, `FUNCTION`, `SCRIPT` and `MODULE` join the never-in-the-console
  list: a script is a built-up command, so `EVAL "redis.call('FLUSHALL')"`
  got around the dangerous list.
- **Preview** runs a batch between `BEGIN` / `START TRANSACTION` and a
  `ROLLBACK` that always runs, reports each statement's verb, table and
  affected rows, and keeps nothing. Only plain `INSERT`, `UPDATE`, `DELETE`
  (and MySQL / MariaDB `REPLACE`) statements run: DDL is out because MySQL
  commits it implicitly, `COMMIT` would make the dry run real, and `/*!` or
  `/*M!` executable comments are refused outright. On Postgres each
  statement goes alone over the extended protocol (`ExecParams`), so a
  statement the new `SplitPostgres` misread fails instead of smuggling a
  `COMMIT` through; on MySQL / MariaDB the Preview is refused while any
  visible non-system table is not InnoDB, because MyISAM and Aria changes
  would not roll back. Every Preview is audited with `outcome: preview`, and
  the page says what a rollback cannot undo (sequence values, triggers'
  outside effects).

## Considered options

**Filtering the SQL a grantee sends.** Rejected: unsound against a superuser
session, and unneeded against a properly limited one.

**A hidden per-grant database user.** Rejected: the Users tab already manages
users, and a hidden one would still show up in every engine listing.

**Redis grants connecting as a dedicated ACL user.** Rejected for now: ACL
users do not survive a container recreate without config management, and the
classifier plus the stricter grantee list is sound for Redis's one-command
protocol.

## Consequences

- A grantee sees **Data → Databases** with only their Databases: status plus
  the Query panel (SQL, as their user) or the Console (Redis, at their
  level). An SQL grantee also gets the Connection tab for their own user.
  Logs, Backups, restore, Start / Stop, users, Direct access and settings
  stay Admin-only, and their Database JSON carries no admin detail
  (`lastError`, warnings, limits).
- A grantee can change its own database user's password (`ALTER ROLE …
  PASSWORD`, `SET PASSWORD`): stopping that would need statement filtering.
  Their runs then fail to log in until an Admin uses Reset password, which
  re-stores a known password and keeps the grant.
- Deleting a managed user that backs grants asks for confirmation naming the
  panel users; confirming deletes their access too (the phase 7 ADR's open
  end).
- Admins lose `EVAL` and the other script commands in the Redis console.
- An Admin's Preview sees every schema; a grantee's non-InnoDB check only
  names tables its user can see.
