# The superuser never runs a database owner's code

**Amended by ADR-0049:** the owner-run Admin console, Run as superuser and
the MySQL / MariaDB root opt-in were removed with phase 8. An Admin's Query
run is the superuser again, and what an owner planted runs with its rights:
an accepted risk. The other decisions below stand.

ADR-0045 kept grantees off superuser sessions. A security review of
phases 1–10 (2026-11-18) found that the panel's *own* superuser sessions broke
the same boundary from the other side. A logical database's owner, which an
app (or a grantee, while phase 8 lasted) may hold, can put code in its
database that Postgres or MariaDB runs with the caller's rights. The panel
then called that code as the superuser on an Admin's routine action:

- **Settings replayed after a restore or Change version.** A role can store a
  list setting with `SET search_path FROM CURRENT`, which keeps the
  session's raw text unquoted. The settings query spliced it into the
  statements `psql -U postgres` ran.
- **The Users tab's Save and the Query tab's table tree** opened superuser
  sessions inside the database. Those sessions took the owner's
  `ALTER DATABASE … SET search_path = evil, pg_catalog`, so an operator the
  catalog queries used was shadowed.
- **The Admin's Query console** ran as the superuser everywhere, and a click
  in the tree ran `SELECT *`. A view's function runs with the reader's rights,
  and `COPY … TO PROGRAM` works in a read-only transaction.

All three reproduced on real servers (`e2e/tests/databases-security.spec.ts`).

**Decision:**

- **Stored values go back as literals.** The settings query returns the raw
  `setconfig` elements, hex-encoded. Go builds the statements: names go
  through `pgIdent`, values through `pgLit`. List settings are split the way
  pg_dump's `SplitGUCList` splits them, so `"$user", public` stays two names.
- **Every superuser catalog session starts on `search_path = pg_catalog,
  pg_temp`.** It is set as a startup parameter, which outranks database and
  role settings. This covers the pgx admin path, the table tree, and the psql
  admin sessions (`PGOPTIONS`). `public` is left out.
- **(Removed by ADR-0049.) An Admin's Postgres run inside a database a non-superuser owns connects as
  that owner**, through its managed login. Whatever the owner planted then
  runs with the owner's rights.
  - An owner the panel has no password for is refused (409), pointing to
    Reset password.
  - **Run as superuser** is an explicit switch with a warning. That run also
    starts on the catalog `search_path`, so names must be `schema.table`.
  - A database a superuser owns, `postgres` included, is unchanged: on
    PG 15 and later nobody else can create objects or settings there.
- **(Removed by ADR-0049.) MySQL and MariaDB have no owner to step down to.** Inside a database that
  other users have Full access to, root needs Run as superuser (409
  otherwise). Those users can create `SQL SECURITY INVOKER` views over
  functions, and MariaDB has no binlog to stop a non-SUPER user from creating
  functions. This also applies to read-only runs, so the rule does not lean on
  `transaction_read_only`.
- **A click in the table tree only fills the editor**, and views are marked.
- (Removed by ADR-0049.) The response and the audit named the database user
  a run used, and whether the Admin chose the superuser.

**Considered and rejected:**

- **`SET ROLE <owner>` on the superuser's connection.** A function can
  `RESET ROLE` and get the superuser back, because the session user stays
  the superuser.
- **Pinning `search_path` on the Admin's normal runs.** Unqualified table
  names would stop resolving. Running as the owner removed the need; since
  ADR-0049 an Admin's run is the superuser on the database's own
  `search_path`, which is the accepted risk.

**Consequences** (of the removed owner-run console, until ADR-0049):
- In a database another role owns, an Admin's run sees what the owner sees,
  and objects it creates belong to the owner.
- Extensions and other superuser-only work need the switch.
- A MySQL or MariaDB database with Full users needs the switch for every
  run.

**Accepted in ADR-0049:** Restore and Change version replay an owner's dump
as the superuser through psql. A CHECK or domain constraint, or a generated
column, that calls an owner-defined function would run during `COPY` with
superuser rights (not reproduced). Closing it means restoring as the owner.
