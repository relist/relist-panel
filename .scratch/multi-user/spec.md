# Spec — Multiple users, per-Project roles, Audit log and Compose policy

## Problem

The panel has one account. `admin_credentials` is pinned to one row by `CHECK (id = 1)`, and
ADR-0001 rules out users, teams and roles. A team that shares a panel shares one password: nobody
can tell who changed what, one person cannot be cut off without changing everyone's password, and
everyone can do everything on every Project, including the host-wide pages.

A second limit sits under the first: anyone who can edit and deploy a Compose can take the host
(mount `/var/run/docker.sock` or `/`, run `privileged`, then open a Terminal). Roles alone would only
stop mistakes, not someone who means harm.

## Decision

Settled in brainstorming on 2026-11-04:

- **A trusted team, each with their own login.** Every person signs in as themselves, sees only
  the Projects and Environments they were granted, and can do there only what their role allows.
- **Three fixed roles: Viewer, Operator, Editor.** No custom permission sets.
- **Grants at Project and at Environment level.** An Environment grant overrides the Project
  grant, up or down. An Environment grant may also be **No access**, which hides it.
- **Admins see everything** and alone create and delete Projects, manage users and grants, and
  use the host-wide pages.
- **The Owner is `users.id = 1`, fixed.** The Owner is always an admin whatever the row says, and
  no web request can disable, delete or demote it. There is no ownership transfer: a new owner
  takes over the id 1 account (its email and password). The host is the recovery path, as for
  two-factor: `relist-panel create-admin` resets id 1.
- **Invite links, no email.** An admin creates a single-use link and hands it over; the person
  sets their own password. A reset link works the same way.
- **One central authorization middleware with a route table, default deny.** A route missing from
  the table is admin-only, and a test fails when any registered route is missing.
- **Full Audit log.** Every change and every sensitive read, kept 90 days by default. Admins see
  all of it, Editors see the events of what they edit.
- **Compose policy.** A Compose whose resolved config could take the host deploys only after an
  approver approves that exact config. The approver is every admin by default, or only the Owner
  when `RELIST_POLICY_APPROVER=owner`. Host paths outside the Compose directory that may be bound
  without approval are listed in `RELIST_POLICY_BIND_ROOTS`.
- **Phased delivery.** One spec, four phases that merge separately: Users, Roles, Audit log,
  Compose policy. Implementation starts on `main` after #38–#42 merged (`ed460c6`), so the
  migration numbers and the route table are final from the start.

## Goal and success criteria

- A second person signs in with their own email and password, through an invite link.
- A user with no grant in a Project does not see it in the list, and its URLs answer 404.
- A Viewer cannot restart, an Operator cannot edit the Compose file, an Editor of `staging` with
  Viewer on `production` cannot deploy `production`, and an Environment on No access is invisible.
- Viewers and Operators never receive a secret variable's value from any endpoint.
- The existing admin keeps signing in with the same email, password, two-factor and session.
- Every change made through the panel shows in the Audit log with who made it.
- A non-approver cannot deploy a Compose that mounts the Docker socket, any path outside its
  allowed roots, another Compose's volume, or runs privileged, however the file spells it, and
  cannot open such a path in the Volume Explorer. The race listed under "What it does not stop"
  remains.

## Out of scope / follow-up

- Custom roles or per-permission checkboxes.
- Email delivery of invites, SSO/OIDC, API tokens.
- Requiring two-factor for every user.
- Shipping the Audit log off the host (tamper-evidence against admins).
- Limiting what an admin who is not the Owner can do on the host-wide pages.

## Domain changes (CONTEXT.md)

Add:

- **User**: one person who signs in to the panel, with their own email, password and two-factor.
  Is **invited**, **active** or **disabled**. _Avoid_: Account (the Account page is where a User
  edits their own sign-in), Member.
- **Owner**: the User with id 1. Always an admin; the web cannot disable, delete or demote it.
- **Admin**: a User who sees every Project and may use the host-wide pages and manage Users.
- **Role**: Viewer, Operator or Editor, what a User may do in an Environment. _Avoid_: Permission
  (a Role is the unit; there are no separate permissions).
- **Grant**: a Role given to one User on one Project, or on one Environment (which may also be
  No access). The Environment Grant wins over the Project Grant.
- **Invite link**: a single-use link with which a User sets a password, for a new User (7 days) or
  a reset (24 hours).
- **Audit event**: one recorded action: who, when, what, where.
- **Compose policy**: the rules a resolved Compose config must meet to deploy without approval.
- **Approval**: an approver's consent to deploy one resolved config of one Compose, keyed by its
  hash.

Change:

- **Environment Variable**: the secret flag stops being only a display tier. A Viewer or Operator
  never receives the value. For an Editor and an admin it stays a display tier.
- **Two-factor**: per User, not per admin account.

## Data model

Each phase adds its own migration, from `00031` on: `00031_users.sql` (phase 1), then
`grants`, `audit_events`/`audit_settings` and `compose_approvals` in the phases that use them.
Never edit a committed migration.

### `users` (replaces `admin_credentials`)

SQLite cannot drop the `CHECK (id = 1)`, so the migration creates `users`, copies the admin row
with its id, rebuilds `sessions` and drops `admin_credentials`.

| column | meaning |
|---|---|
| `id INTEGER PRIMARY KEY` | id 1 is the Owner |
| `email TEXT NOT NULL UNIQUE COLLATE NOCASE` | |
| `password_hash TEXT NULL` | `NULL` while invited |
| `is_admin INTEGER NOT NULL DEFAULT 0` | ignored for id 1, which is always admin |
| `disabled_at INTEGER NULL` | unix seconds |
| `last_login_at INTEGER NULL` | |
| `created_at INTEGER NOT NULL` | |
| `totp_secret`, `totp_pending_secret`, `totp_last_step`, `failed_attempts`, `locked_until`, `password_from_env` | moved unchanged from `admin_credentials` |

The copied row gets `is_admin = 1`.

### `sessions`

`admin_id` becomes `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE`. Existing
sessions are copied, so nobody is signed out by the upgrade. A User's Grants are read on every
request, so a Grant change takes effect on the next request. Disabling a User deletes their
sessions.

### `user_tokens`

| column | meaning |
|---|---|
| `id INTEGER PRIMARY KEY` | |
| `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE` | |
| `kind TEXT NOT NULL CHECK (kind IN ('invite','reset'))` | |
| `token_hash TEXT NOT NULL UNIQUE` | SHA-256 of the token; the token is shown once |
| `expires_at INTEGER NOT NULL` | invite +7 days, reset +24 hours |
| `created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL` | |
| `used_at INTEGER NULL` | |

A User has at most one unused token: creating a new one deletes the old.

### `grants`

| column | meaning |
|---|---|
| `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE` | |
| `project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE` | |
| `environment_id INTEGER NULL REFERENCES environments(id) ON DELETE CASCADE` | `NULL` = Project Grant |
| `role TEXT NOT NULL CHECK (role IN ('none','viewer','operator','editor'))` | |
| `CHECK (environment_id IS NOT NULL OR role <> 'none')` | No access only on an Environment |

Two partial unique indexes: `(user_id, project_id) WHERE environment_id IS NULL` and
`(user_id, environment_id) WHERE environment_id IS NOT NULL`. Writing a Grant checks that the
Environment belongs to the Project.

### `audit_events`

| column | meaning |
|---|---|
| `id INTEGER PRIMARY KEY` | also the pagination cursor |
| `at INTEGER NOT NULL` | unix milliseconds |
| `actor_user_id INTEGER NULL` | no foreign key: the event outlives the User |
| `actor TEXT NOT NULL` | the User's email at the time, or `cli` |
| `action TEXT NOT NULL` | e.g. `compose.update`, `deployment.create`, `grant.set`, `auth.login_failed` |
| `outcome TEXT NOT NULL CHECK (outcome IN ('ok','denied'))` | |
| `project_id`, `environment_id`, `compose_id INTEGER NULL` | no foreign keys, for the same reason |
| `target TEXT NOT NULL` | readable names at the time, e.g. `my-saas / production / api` |
| `detail TEXT NULL` | JSON; never a variable's value |
| `ip TEXT NULL` | the TCP peer, as for the Access log |

Indexes on `at`, `(project_id, at)` and `(actor_user_id, at)`. Retention lives in a one-row
`audit_settings` table (`retention_days`, default 90, at least 1, or 0 = forever), like
`access_log_settings`.

### `compose_approvals`

| column | meaning |
|---|---|
| `compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE` | |
| `config_hash TEXT NOT NULL` | SHA-256 of the resolved config |
| `approved_by INTEGER NULL` | |
| `approved_at INTEGER NOT NULL` | |

One approval per Compose; a new one replaces it.

## Effective role

```
role(user, environment):
  if user.id == 1 or user.is_admin: admin
  if a Grant on environment exists:  that role (none hides it)
  if a Grant on its Project exists:  that role
  else:                              none
```

A Project is visible when at least one of its Environments has a role other than none, or when it
has no Environments and the User holds a Project Grant. No access on every Environment hides the
Project. A Project-level Editor is a User whose Project Grant is `editor`.

## Permissions

Each level includes the ones above it.

| Level | May |
|---|---|
| **Signed-in** | any User with a session: `GET /api/auth/status`, the Account routes (`/api/auth/account`, password, email, two-factor), logout |
| **Viewer** | read Projects, Environments, Composes, the Compose file, Services, logs, metrics, Deployments and their logs, Domains, Source settings, Zero-downtime settings, Builder settings, variable keys and non-secret values, Variable Set keys and non-secret values, Bindings |
| **Operator** | create and cancel Deployments, restart, stop |
| **Editor** | update the Compose, its Variables, Variable Sets, Bindings, Domains, Source (including upload, test, known-hosts reset, cache delete), Zero-downtime and Builder settings; Preview; Terminal; Volume Explorer (read, write, archive, orphans); removal plans; read secret values; create and delete Composes |
| **Project Editor** | create and delete Environments, update the Project's name and description |
| **Admin** | create and delete Projects; Users and Grants; Audit log page; every host-wide route: `/api/images`, `/api/docker/*`, `/api/certificates*`, `/api/ssh-keys*` writes, `/api/builders*` writes, `/api/traefik/*` (static config included), `/api/vulnerabilities/*`, `/api/panel-update/*`, `/api/host/metrics`, `/api/migration/*` |

Preview is Editor because its output has variables interpolated, secrets included. The Volume
Explorer is Editor because a Volume may hold a database.

**`docker compose` stops inheriting the panel's environment** (phase 2). Today
`dockerexec` runs it with `os.Environ()`, so `${RELIST_ADMIN_PASSWORD}` in a Compose file would
interpolate the panel's own variable into Preview and into containers. The CLI gets only `PATH`,
`HOME` and the `DOCKER_*` variables it needs, plus the variables the panel passes on purpose.

Lists filter instead of refusing:

- `GET /api/projects`, `GET /api/projects/{projectID}/environments`: only visible ones.
- `GET /api/deployments` and `/api/deployments/queue`: full rows for visible Composes; other rows
  keep their status and position but have no Project, Environment or Compose name or id, so a
  user can see why their Deployment waits.
- `GET /api/ssh-keys`: a non-admin gets only `id` and `name`, to choose a key for a Source.
- `GET /api/builders`: a non-admin gets only `id`, `name` and which is the default, to choose a
  Builder on the Compose's Builder setting.
- `GET /api/auth/status`: returns the User (`id`, `email`, `isAdmin`, `isOwner`) and the effective
  role per visible Environment.

## Authorization middleware (`internal/authz`)

- Sits inside `auth.Middleware`, outside `panelUpdates.Middleware`, `gate.middleware` and
  `oldHost.Middleware`, so a request without the right is refused before any lock sees it.
- Finds the matched pattern with `mux.Handler(r)` and looks it up in one table:
  `pattern → {level, action, audit}`. No `RegisterRoutes` signature changes.
- Takes `projectID`, `environmentID` (or `{id}` under `environments/`) and `composeID` from the path
  by aligning it with the pattern, then **reads the parents from the database**: the Environment's
  Project and the Compose's Environment must be the ones in the path, otherwise 404. A forged
  `projectID` cannot raise a role.
- Not visible (none, or no Grant) → **404**. Visible but too low → **403**. Database error →
  **500**, never allowed through.
- Puts `{user, level, projectID, environmentID, composeID}` in the request context for handlers
  and the Audit log.
- A pattern missing from the table is admin-only. `TestEveryRouteIsClassified` walks every pattern
  that `NewHandler` registers and fails on any missing one.
- Routes outside the session (`/healthz`, auth, the migration pull's self-authenticated paths, web
  assets) stay as they are.

## Users

### Invite and reset

- `POST /api/users {email, isAdmin, grants?}` (admin) creates an invited User and returns
  `{user, link}`. The link is `https://<panel>/invite/<token>`, a 32-byte random token, shown once.
- `POST /api/users/{id}/reset` (admin) returns a reset link and deletes the User's sessions now.
- `GET /api/invites/{token}` (public) returns `{email, kind}` or 404.
- `POST /api/invites/{token} {password}` (public) sets the password under the current rules,
  marks the token used, and signs the User in. A reset also turns two-factor off, which is how an
  admin recovers a User who lost their phone; `reset-2fa` stays for the host.
- Expired, used and unknown tokens give the same 404 "This link is no longer valid". Token lookup
  is by hash. The invite routes share the login rate limit.

### Managing Users (admin)

- `GET /api/users`, `GET /api/users/{id}`, `PATCH /api/users/{id} {isAdmin?, disabled?}`,
  `DELETE /api/users/{id}`.
- Any of these that would disable, delete or demote id 1 → 403.
- Disable deletes sessions and keeps Grants. Delete removes the User, its tokens and Grants.
- `GET /api/users/{id}/grants`, `PUT /api/users/{id}/grants` (replace all of one User's Grants in
  one transaction), and `GET /api/projects/{id}/grants` for the Project's Members tab.

### Sign-in and Account

- Login is unchanged: `{email, password, code?}`, five failures lock that User for one minute.
- A disabled or invited User gets the same answer as a wrong password.
- The Account page (password, email, two-factor) works on the signed-in User.

### CLI

- `relist-panel create-admin`: creates id 1 when there are no Users; otherwise sets id 1's email
  and password. `RELIST_ADMIN_PASSWORD` still seeds id 1.
- `relist-panel reset-2fa [--email]`: `--email` is required when there is more than one User.
- Both write an Audit event with actor `cli`.

## Audit log

### Recording

- The middleware records every non-GET request on a classified route that ends in 2xx, as the
  route's `action`, with `outcome = ok`. A request the middleware refuses is recorded with
  `outcome = denied`.
- Handlers may add `detail` through the context (`audit.Detail(ctx, key, value)`): variable keys,
  Domain hosts, the changed role. A variable's value never goes in `detail`, secret or not.
- These GETs are recorded too: opening a Terminal Session (and its duration when it closes),
  reading a secret value, downloading a Volume file or archive, downloading an Access log file.
- Auth: login ok and failed (the email typed and the IP), logout, invite and reset created and
  used, two-factor on and off, password and email changes.
- Work the panel starts by itself (scheduled scans, the Panel update scheduler) is not recorded.

### Reading

- `GET /api/audit?cursor&actor&action&from&to` (admin): all events, newest first, 100 per page.
- `GET /api/projects/{id}/audit` with the same filters: an admin, or a User who is Editor on at
  least one Environment of the Project; they see the events of the Environments they edit and the
  Project's own events.
- `GET/PUT /api/audit/settings` (admin): retention.
- Retention runs once a day in the existing background workers.

### Limits

The Audit log has no delete in the web and cannot be changed by Viewers or Operators. It is not
tamper-proof against an admin or an approver, who can reach `panel.db` from a container.

## Compose policy

### What is checked

The check runs on the **resolved** config, the output of `docker compose config` with every
profile enabled, so interpolation, `include` and `extends` cannot hide a value. It checks the
config as the user wrote it, before the panel injects its own keys (Domain labels,
`relist-network`, Internal hostnames, logging, the Builder); injected keys are the panel's and are
trusted. The rules are an allowlist: a key the checker does not know is a violation.

Violations:

- A bind mount (in `volumes:` long or short form, or `configs`/`secrets`/`build.secrets` with
  `file:`) whose source is not inside an **allowed root**, after resolving symlinks. Any path
  component that is a symlink pointing outside is a violation, including symlinks inside a Source.
- A bind source inside or equal to another bind source of the same Compose (a container that can
  write the outer one could swap the inner one for a symlink before the next `up`).
- `env_file` or `label_file` outside an allowed root. To verify before implementing: whether
  `docker compose config` keeps these paths or inlines their contents. If it inlines them, the
  checker reads them from the Compose file before resolving.
- A secret or config with an `environment:` source, and `build.ssh`.
- A named volume with `driver_opts` (a `local` volume can bind any host path), a non-default
  `driver`, `external: true`, or a `name:` other than this Compose's `<project>_<logical>`. Any of
  these lets one Compose open another Compose's volume, another Project's database included.
- `volumes_from` naming anything outside this Compose.
- `privileged`, `cap_add`, `devices`, `device_cgroup_rules`, `security_opt`, `sysctls`,
  `cgroup_parent`, `runtime`, `userns_mode: host`, and `pid`, `ipc`, `uts` or `network_mode` set to
  `host` or to `container:`/`service:` naming anything outside this Compose.
- `build.context` or `build.dockerfile` outside the Compose directory, `build.network: host`,
  `build.privileged`, `build.entitlements`, `build.additional_contexts` outside the directory.
- `include` or `extends` of a file outside the Compose directory.
- A `traefik.*` label whose value names the panel's own domain.

The **Compose directory** is `<data dir>/<compose dir>`, which holds the Compose file and its
Source. The **allowed roots** are the Compose directory plus the host paths listed in
`RELIST_POLICY_BIND_ROOTS` (comma-separated, empty by default), for installs that keep data in
places like `/srv`. Like the approver, the list is read from the environment only. Mount sources
are compared as the panel sees them and as the host sees them (ADR-0002); both must be inside.

### Volume Explorer

The Volume Explorer resolves Volumes from the saved file, not from the last Deployment, so saving
`/:/host` would otherwise open the host root in it without any deploy. It opens a Volume only when
that Volume's source passes the policy, or when the user is an approver and the Compose's current
config is approved. Orphan removal is unaffected: it only ever touches this Compose's named
volumes.

### Approval

- `config_hash` is SHA-256 of the resolved config. A non-approver may queue a Deployment only when
  the config passes, or when `compose_approvals.config_hash` equals the current hash. Otherwise
  `POST …/deployments` answers 409 `{policy: {violations: […], approvalRequired: true}}`.
- The check runs again in the Deployment, on the inputs frozen when it was queued (ADR-0017),
  before `up`, so a change between queueing and running cannot slip through.
- An approver queueing a Deployment of a violating config approves it as a side effect (and the
  Audit event says so), but only with the `configHash` the approver was shown:
  `POST …/deployments {configHash}`. `POST …/composes/{composeID}/approval {configHash}` approves
  without deploying. Either answers 409 when the hash no longer matches, so an edit saved between
  looking and clicking is never approved unseen.
- Saving a Compose that violates is allowed and returns the violations, so the editor sees them.
- `GET …/composes/{composeID}/policy` (Viewer) returns `{violations, configHash, approved}` for the
  Compose page.
- Restart and stop do not run the check: they do not change the config.
- Composes that already run are not touched by the upgrade; their next deploy is checked.

### Approver

`RELIST_POLICY_APPROVER` is `admins` (default) or `owner`. Any other value stops the panel at
startup with an error. It is read from the environment only, so a stolen admin session cannot
widen it.

### What it does not stop

- A kernel exploit from an ordinary container.
- A symlink race across Deployments: a container still running from the previous Deployment that
  can write a directory the new config binds could swap a path between the check and `up`. The
  nesting rule closes it within one config; the check runs as late as possible before `up` to
  shrink the window, but does not close it.
- An admin who is not the Owner still uses the host-wide pages, among them Docker host and
  Migration serve, which hands out `panel.db`: admins remain fully trusted.

## UI

Follow `docs/GUIDELINES.md` and the Ant Design docs for each component, checked against the
installed `antd`.

- `useCan(environmentId, level)` from `GET /api/auth/status` hides or disables actions. It is
  convenience only; the server decides. A 403 shows "You don't have permission"; a 404 shows the
  existing not-found page.
- The sider shows the host-wide group, **Users** and **Audit log** only to admins.
- **Users**: table of email, status, admin, two-factor, last login. **Invite** opens a modal
  (email, admin switch) and then shows the link with a copy button. The id 1 row has an **Owner**
  tag and no disable, delete or demote action.
- **User detail**: a Grant tree of Projects and their Environments, one role Select per row; an
  Environment row also offers "Same as Project" and "No access".
- **Project → Members** tab (admin): the same Grants seen from the Project.
- **Project → Activity** tab (Editors and admins) and the **Audit log** page (admins): filters for
  actor, action and time range; cursor paging.
- **Compose page**: an Alert listing policy violations; the Deploy button reads
  "Waiting for approval" for others and "Approve & Deploy" for approvers; a tag in the Compose list.
- **`/invite/:token`**: a sign-in style page to set a password, for invites and resets.
- **Variables**: a Viewer or Operator sees a secret as `••••` with no reveal button.

## Phases

1. **Users**: migration, `users`/`sessions`/`user_tokens`, invites, reset, Users page, Account
   per User, CLI, and `internal/authz` with a route table that holds only the Signed-in routes.
   Default deny makes every other route admin-only, so a non-admin invited in this phase can sign
   in and use the Account page and nothing else.
2. **Roles**: `grants`, the rest of the route table, list filtering, secret stripping, the
   `docker compose` environment, Members tab, `useCan`.
3. **Audit log**: `audit_events`, recording, detail hooks, pages, retention.
4. **Compose policy**: checker, approvals, Deployment hook, UI, `RELIST_POLICY_APPROVER`.

## Testing

- `TestEveryRouteIsClassified` over the patterns `NewHandler` registers.
- A matrix test: every classified route × {no Grant, none, Viewer, Operator, Editor on the
  Environment, Editor on the Project with Viewer on the Environment, admin, Owner} → 2xx, 403 or
  404 as the table says.
- A forged path (Project A's Environment under Project B, a Compose under the wrong Environment)
  answers 404.
- No secret value in any response to a Viewer or Operator: variables, effective variables,
  Variable Sets, Preview (refused), Compose detail.
- Every action in the table writes an Audit event; no variable value ever lands in `detail`.
- The migration keeps the admin's email, password, two-factor and sessions working.
- Owner rules: disable, delete and demote of id 1 all answer 403.
- Invite: expired, used and unknown tokens answer alike; a used token cannot be reused; reset
  ends sessions and two-factor.
- `${RELIST_ADMIN_PASSWORD}` in a Compose file does not interpolate to the panel's value.
- The Volume Explorer refuses a saved-but-violating bind for a non-approver.
- An approval or approving deploy with a stale `configHash` answers 409.
- Policy: one fixture per violation above, each also hidden behind a variable, plus a symlink in a
  Source and a nested bind, must fail; ordinary Composes (named volumes, relative bind mounts, Domains) must pass. An
  approval stops matching after any change to the resolved config, including a variable value.
- Frontend tests in the existing `web/` style for `useCan`, the Grant tree and the invite page.
- A live check on the install after each phase.

## Docs

- ADR-0031 superseding ADR-0001's
  single-admin non-goal: users and roles, the Owner as id 1, the central middleware with default
  deny, what the secret flag now protects, and the Audit log's limits.
- ADR-0032 for the Compose policy: allowlist on the resolved config, approval keyed by hash,
  `RELIST_POLICY_APPROVER`, and why admins remain fully trusted.
- Update ADR-0016, ADR-0020 and ADR-0022, which say "the single admin owns the host".
- CONTEXT.md changes listed above. Public docs: Users, roles and the approver variable.

## Comments

### 2026-11-04 — Phase 1 deviations

- Recovery for id 1 is the existing `reset-password` and `change-email` (both take `--email`); `create-admin` stays create-only, rather than resetting id 1.
- An Admin cannot send the Owner a reset link; only the Owner can (`403 Only the owner can reset the owner`). Otherwise any Admin could take over id 1.
- The invite routes have no rate limit: a 256-bit token cannot be guessed.
- `POST /api/users/{id}/reset` on an invited User issues a fresh invite (7 days), and on a disabled User answers `409 Enable this user before sending a link`.
- Accepting an invite or reset link signs the User in and sets their last sign-in.

### 2026-11-04 — Phase 2 rulings

1. An Editor of an Environment writes its Environment Variables; reading keys is Viewer.
2. `freshVolumes: true` on a Deployment needs Editor; a plain Deployment is Operator.
3. Hidden wins for a Project Editor: an Environment set to No access answers 404 on its delete and removal plan too.
4. `GET /api/deployments` shows only visible rows, filtered in SQL; `GET /api/deployments/queue` keeps the others with their names and ids zeroed.
5. The Members tab writes with `PUT /api/projects/{id}/grants`, replacing that Project's Grants in one transaction.
6. `POST /api/users` takes no `grants`; an Admin invites, then sets Grants on the User page.
7. A member's `/` redirects to `/projects`; Overview, Monitoring and every host-wide page stay admin-only.
8. Roles are recorded by amending ADR-0031; ADR-0032 stays for the Compose policy.
9. A Project's `environmentCount` is the visible count for a non-admin.

### 2026-11-05 — Phase 3 rulings

1. Actions are a separate `authz.Actions` map (pattern → `<resource>.<verb>`); every registered non-GET, non-Public pattern has one; GETs only for Volume read/archive and Access log downloads.
2. `denied` is recorded only for actioned patterns.
3. Target names are read before the handler, for admins too; unscoped routes use the handler's `audit.Target`, else the path.
4. `secret.read` is one event per list response that carried a secret value, with `detail.keys`.
5. The Terminal records `terminal.open` and `terminal.close` (`detail.seconds`) itself.
6. Sign-in events: `auth.login`, `auth.login_failed` (denied, `detail.email`, reason `locked`/`code`), `auth.invite_accepted` (`detail.kind`).
7. CLI events, actor `cli`: `cli.create_admin`, `cli.reset_password`, `cli.change_email`, `cli.reset_2fa`.
8. Settings API is `{retentionDays, keepForever}`, stored as `retention_days` (0 = forever).
9. Filters: actor exact (case-insensitive) or `cli`; action prefix; from/to inclusive ms.
10. `GET /api/projects/{id}/audit` is Viewer in the route table; the handler answers 403 unless the User edits one of its Environments.
11. HTTP lives in `internal/auditapi`.

### 2026-11-05 — Phase 4 rulings

1. The check runs on the Deployment's snapshot (panel keys injected), the config `up` runs; injected keys pass the rules.
2. Inside the Compose directory a path may not be the directory itself nor `.ssh` (a Source's git key).
3. `include`/`extends` paths in the saved YAML must be relative, inside the directory, without variables or URLs.
4. The same bind path used twice is not nested; only a bind strictly inside another is.
5. `RELIST_POLICY_BIND_ROOTS` refuses `/`; roots the panel container cannot see are compared as text.
6. `configHash` goes only to approvers (`GET …/policy`, the 409 body); everyone sees the violations.
7. The Volume Explorer checks the deployed file it resolves from.
8. A git Source in build mode that cannot be resolved before its fetch queues; the check after the fetch decides.
9. The Compose list tag is left out; the Compose page shows the state.
10. (Review) The hash covers the Source settings (kind, repository, branch, paths, zip upload), not each commit.
11. (Review) A bind of the panel's own files in the Compose directory (dot names, `docker-compose.yml`, `sets`) is refused, except the Deployment's own Source copy; a link the panel cannot follow is refused; include and extends are followed into the files they name.
12. `com.docker.*` labels on services, volumes and networks are refused; other labels naming the panel domain are harmless (only Traefik reads labels).
13. Secret values are masked in violation messages and evaluation errors.
