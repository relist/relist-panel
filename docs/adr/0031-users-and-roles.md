# Users, an Owner, and roles decided in one place

ADR-0001 kept the panel to one admin account. A team that shares a panel then shares one password: nobody can tell who changed what, one person cannot be cut off without changing everyone's password, and everyone can do everything, the host-wide pages included.

**Decision:** The panel has **Users**, each signing in as themselves with their own password and Two-factor. `admin_credentials` becomes `users`, and the existing admin keeps id 1, sessions and all. That row is the **Owner**: always an Admin, never disabled, deleted or demoted from the web, and reset only by itself. An Admin adds a User by email and hands them an **Invite link**, a 256-bit token stored as its SHA-256, that works once within 7 days; the same kind of link, valid 24 hours, resets a password, signs the User out and turns their Two-factor off. Every request's User is read from its session on every request, so disabling or demoting someone takes effect on their next click.

What a User may reach is decided by one middleware, `internal/authz`, between `auth` and the mux. It looks up the request's ServeMux pattern in one table, and **a pattern missing from the table is admin-only**: a new route is closed until someone classifies it. The first table holds only the routes every signed-in User needs (their Account, sign-out, the Migration lock's status). The next step gives each User a role per Project or Environment — Viewer, Operator, Editor, or No access on an Environment — in the same table; then a Compose policy keeps those roles from reaching the host through a Compose file.

## Considered options

**Checks in each handler.** Rejected: nearly thirty packages, and a route whose author forgot the check is open. A central table with default deny fails closed, and a test fails when a registered route is missing from it.

**An `is_owner` flag that can move.** Rejected by the operator: id 1 is simpler to reason about, and taking over the account (its email and password) is how ownership changes. A stolen Admin session could not have been stopped by other Admins anyway: whoever can deploy a Compose owns the host until the Compose policy exists.

**Custom permission sets.** Rejected: three fixed roles answer what the team needs, and every combination of permissions is another case to test.

**Email delivery of invites.** Rejected: the panel has no mail settings, and the Admin is already talking to the person they invite.

**Rate-limiting the invite routes.** Rejected: a 256-bit token cannot be guessed, and the login lock is per User, which a link without a password has nothing to count against.

## Consequences

- **Until roles exist, a User who is not an Admin sees only their Account** and a page saying they have no access yet.
- **Recovery stays on the host.** `create-admin` still makes only the first User; `reset-password`, `change-email` and `reset-2fa` take `--email`, required once there is more than one User.
- **Secrets, SSH keys and certificates are still not a boundary against Admins.** ADR-0016, ADR-0020 and ADR-0022 say "the single admin owns the host"; with Users, read it as "an Admin owns the host".
- **A Migration moves Users and their sessions** with `panel.db`, like everything else (ADR-0018).
- Spec: `.scratch/multi-user/spec.md`. Plan: `.scratch/multi-user/plan-1-users.md`.
