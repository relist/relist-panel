# Databases are Admin-only

Phase 8 (ADR-0045) let an Admin give a non-Admin access to one Database. To
keep a grantee from becoming the superuser, it needed server-wide privilege
checks, restricted sessions, a stricter Redis classifier, and then (ADR-0048)
an owner-run Admin console with a Run as superuser switch and a MySQL opt-in.
Nobody but Admins needs a Database, and the protections cost more to review
and keep correct than they bought. So every `/api/databases` route is
Admin-only again: authz leaves them out of `Routes`, and
`TestDatabaseRoutesAreAdminOnly` holds that. Migration `00054` drops
`database_grants`.

**Kept from phase 8 and the security review:**

- **Preview**, for Admins (ADR-0045's Preview section stands).
- **Redis scripts stay refused in the console** (`EVAL`, `FCALL`,
  `FUNCTION`, `SCRIPT`, `MODULE`): a script gets around the dangerous list.
- From ADR-0048: settings replayed as literals, the catalog `search_path` on
  every superuser catalog session (Users tab, table tree, psql admin), and a
  click in the table tree that only fills the editor, with views marked.

**Removed:** grants and the Panel access tab, the grantee Database view, the
server-wide check, the grantee Redis classifier, the Postgres owner-run
console, Run as superuser, and the MySQL / MariaDB root opt-in.

**Consequences:**

- An Admin's Query run is the superuser (root on MySQL / MariaDB) again. A
  logical database's owner (an app holding the owner's credentials, or
  anyone with Full access) can plant a view, trigger, function or a
  `search_path` operator that an Admin's run then executes with superuser
  rights. That risk is accepted: the Admin chooses what to run, and the
  table tree no longer runs anything by itself.
- **Restore and Change version replay a dump as the superuser** (accepted
  2026-11-20). A CHECK or domain constraint, or a generated column, that
  calls an owner's function runs with superuser rights during `COPY`. It is
  the same kind of risk. Closing it would mean restoring as the owner: a
  session that logs in as the owner (not `SET ROLE`, which a function can
  undo), and splitting the plain-SQL dump into the parts that need the
  superuser (extensions, other roles' ownership) and the rest. That was
  judged not worth its size.
- Granting a non-Admin access later starts over from this ADR and
  ADR-0045's lessons, not from the removed code.
