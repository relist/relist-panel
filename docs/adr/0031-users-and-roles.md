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

- **A User who is not an Admin sees only what their Grants reach** (see Roles below); with no Grant they see an empty Projects list.
- **Recovery stays on the host.** `create-admin` still makes only the first User; `reset-password`, `change-email` and `reset-2fa` take `--email`, required once there is more than one User.
- **Secrets, SSH keys and certificates are still not a boundary against Admins.** ADR-0016, ADR-0020 and ADR-0022 now say "an Admin owns the host".
- **A Migration moves Users and their sessions** with `panel.db`, like everything else (ADR-0018).
- Spec: `.scratch/multi-user/spec.md`. Plans: `.scratch/multi-user/plan-1-users.md`, `.scratch/multi-user/plan-2-roles.md`.

## Roles (phase 2, 2026-11-04)

A User who is not an Admin gets a **Role** per Project or per Environment through a **Grant**: Viewer (read everything in it, secret values excepted), Operator (also deploy, restart, stop, cancel), Editor (also change the Compose, its Variables, Domains and settings; Preview, Terminal, Volume Explorer, secret values). An Environment Grant wins over the Project Grant and may be **No access**, which hides that Environment. A **Project Editor** holds `editor` on the Project itself and may also edit the Project's details and add or delete its Environments. Grants live in `grants` (migration `00032`); only Admins read or change them.

`internal/authz` holds every route's level in one table. For a scoped route it aligns the path with the ServeMux pattern to find the Project, Environment and Compose ids, checks in the database that each belongs to the one before it, loads the User's Grants, and only then runs the handler. A non-admin who cannot see the target, or who forged the path (an unknown, non-numeric or mismatched id), gets **404**, so a hidden Project is indistinguishable from a missing one; one who can see it but whose Role is too low gets **403**; a database error is a **500**, never a pass. Admins skip every check. Lists (Projects, Environments, Deployment history) are filtered to what the User can see; the Deployment queue keeps other rows with their names and ids blanked, so a member sees why theirs waits.

A secret variable's value is sent as `""` to anyone below Editor, from every list that carries it (Environment and Compose Variables, Variable Sets, effective variables). For an Editor and an Admin the secret flag stays a display tier.

`docker compose` and `docker buildx` no longer inherit the panel's environment: they get only `PATH`, `HOME`, `TMPDIR`, the XDG and Buildx paths and `DOCKER_*`, so `${RELIST_ADMIN_PASSWORD}` in a Compose file interpolates as empty. An open Terminal re-checks its User's access every 5 seconds and closes when it is gone.

Rulings where the spec was silent:

1. An Editor of an Environment writes its Environment Variables; reading keys is Viewer.
2. Deploying with `freshVolumes` needs Editor, since it deletes data; a plain Deployment is Operator.
3. Hidden wins for a Project Editor: an Environment set to No access answers 404 to them too.
4. Deployment history is filtered in SQL so totals and pages are right; only the queue keeps other rows, redacted.
5. The Members tab writes with `PUT /api/projects/{id}/grants`, which replaces that Project's Grants in one transaction.
6. `POST /api/users` takes no Grants; an Admin invites, then sets Grants on the User page.
7. A member's `/` redirects to `/projects`; Overview, Monitoring and every host-wide page stay admin-only.
8. This ADR is amended rather than a new one written; ADR-0032 is kept for the Compose policy.
9. A Project's `environmentCount` counts only the Environments the User can see.

**Until the Compose policy ships, an Editor can take the host**: a Compose that mounts the Docker socket or runs privileged, then the Terminal. Grant Editor only to people you would give root.
