# Spec — Basic auth groups, picked per Domain, served by Traefik's file provider

Date: 2026-11-08.
Status: open — design approved in chat, spec awaiting review

## Problem

A Domain has no way to ask for a password. Today an operator protects a staging or
preview host by writing a Traefik `basicauth` middleware in the Compose labels and
naming it in the Domain's Middlewares. Only an Admin may write the Compose file
(ADR-0032), the bcrypt hash must be escaped (`$` → `$$`), the same credentials are
copied into every Compose that needs them, and changing a password means editing and
redeploying each one. The operator also often has to look a password up again to send
it to a client or teammate, and a hash cannot give it back.

## Goal and success criteria

- A new host-wide **Basic auth** menu, Admin only, lists **Basic auth groups**. A group
  has a name and any number of users (username + password).
- An Admin adds, renames and deletes groups, adds, edits and removes users, and can
  reveal and copy any user's password later.
- On a Domain, anyone who may edit Domains (Editor and up) picks **one** group or
  None. A Domain with a group answers `401` with a basic auth challenge until the
  browser sends a username and password of that group.
- Adding, removing or re-passwording a user in a group applies **at once**, to every
  Domain using the group, without a redeploy.
- Picking or changing a Domain's group applies on the next deployment, like every
  other Domain change (ADR-0013).
- A group in use by a Domain cannot be deleted; the error names those Domains.
- Revealing a password is recorded in the Audit log; the password itself never is.

## Out of scope

- Groups scoped to a Project or Environment, or groups an Editor manages. One
  host-wide list owned by Admins.
- More than one group on a Domain. Chained `basicAuth` middlewares each demand their
  own credentials while the browser sends one `Authorization` header, so a second group
  would never let anyone else in. Combine users into a new group instead.
- Basic auth on the panel's own host, on the Registry, or on hand-written routers.
  Hand-written routers can already name `relist-auth-<id>@file` in their own labels;
  the panel does not advertise or validate that.
- Forward auth, OAuth, IP allow-lists, or any other access control.
- Encrypting the stored passwords. Like SSH keys (ADR-0020) and Certificate private
  keys (ADR-0022), they sit in `panel.db` in the clear; an Admin owns the host.
- Expiring users, login rate limiting, or per-user access logs.

## Domain changes

`CONTEXT.md` gains:

> **Basic auth group**:
> A named list of usernames and passwords, host-wide and managed by Admins on the Basic
> auth page. A Domain may use one group; Traefik then asks for a username and password
> of that group before passing a request on. Group edits apply at once; picking a
> group for a Domain applies on deploy (ADR-0036).
> _Avoid_: access list, htpasswd file.

The **Domain** entry gains one sentence: "It may require the username and password of
one Basic auth group."

## Data model

Migration `internal/migrations/00040_basic_auth_groups.sql` (renumber if another
branch takes 00040 first; never edit a committed one):

```sql
CREATE TABLE auth_groups (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

CREATE TABLE auth_group_users (
    id         INTEGER PRIMARY KEY,
    group_id   INTEGER NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    username   TEXT    NOT NULL,
    password   TEXT    NOT NULL,
    hash       TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE (group_id, username)
);

ALTER TABLE domains ADD COLUMN auth_group_id INTEGER REFERENCES auth_groups(id);
```

- `hash` is bcrypt at `bcrypt.DefaultCost`, made once when the password is set, so
  writing the file never re-hashes.
- `domains.auth_group_id` is `NULL` for "None". Deleting a group in use is refused in
  the handler (see API), so the foreign key never dangles in practice.

## Validation (`internal/authgroups`)

- Group name: trimmed, 1–64 characters, unique ignoring case (`409` with a field error
  on `name`).
- Username: 1–64 characters, printable ASCII, no `:` and no whitespace (the htpasswd
  separator and what browsers send cleanly). Unique within the group (`409`, field
  `username`).
- Password: 8–128 characters. The UI offers a generated one (24 characters, letters and
  digits) but any value in range is accepted.
- `domains.auth_group_id` on a Domain create or update: `null` or the id of an existing
  group, else `400` with a field error on `authGroupId`.

## Handing groups to Traefik

Same pattern as Certificates (ADR-0022): `panel.db` is the source of truth, Traefik's
watched file-provider directory `<data dir>/.traefik/dynamic/` is derived from it.

### Dynamic files

One file per group, `auth-<id>.yml`, mode `0600`, written atomically (temp file +
rename):

```yaml
# Written by relist-panel from the auth_groups table. Edits are overwritten.
http:
  middlewares:
    relist-auth-<id>:
      basicAuth:
        realm: <group name>
        removeHeader: true
        users:
          - "alice:$2a$10$..."
          - "bob:$2a$10$..."
```

- `removeHeader: true`: the app behind never sees the group's credentials.
- A group with no users gets a single user whose username and password are random
  32-byte values, generated on each write and never stored, so a Domain on an empty
  group is closed to everyone rather than open or broken.

### Reconcile

`Reconcile(ctx)` writes every group's file and removes every `auth-*.yml` whose id is
not in the table, leaving other files alone. It runs on panel startup after migrations
and after every group or user change, inside the same request. A database write that
succeeds while the file write fails returns `500`; the next Reconcile repairs it.
Startup logs a Reconcile failure and continues.

## Domain labels (`internal/domains/labels.go`)

- `Domain` gains `AuthGroupID *int64` (`json:"authGroupId"`, `null` for None), and the
  store reads and writes the column.
- `addDomain`: when `AuthGroupID` is set, `relist-auth-<id>@file` goes **first** in the
  router's middleware chain, before strip, prefix, the Domain's own Middlewares and the
  zero-downtime retry. A rejected request then never reaches the others.
- The HTTP→HTTPS redirect router (`<router>-http`) does **not** get it: the browser is
  redirected first and asked for credentials over HTTPS only.
- `Generated` derives from `addDomain`, so the Compose policy (ADR-0032) accepts the
  new middleware value with no other change.

## API (`internal/authgroups`, registered in `internal/httpapi/httpapi.go`)

Admin only unless noted. Responses never carry `password` or `hash` except the reveal
route.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/auth-groups` | `[{id, name, userCount, domainCount, createdAt, updatedAt}]`, by name |
| POST | `/api/auth-groups` | `{name, users?: [{username, password}]}` → `201` group. Lets the Domain form create a group and its first user in one call |
| GET | `/api/auth-groups/{id}` | group + `users: [{id, username, createdAt, updatedAt}]` + `domains: [{id, host, path, composeId, composeName, environmentName, projectName}]` |
| PATCH | `/api/auth-groups/{id}` | `{name}` |
| DELETE | `/api/auth-groups/{id}` | `204`, or `409 {error, domains: [...]}` when any Domain uses it |
| POST | `/api/auth-groups/{id}/users` | `{username, password}` → `201` user |
| PATCH | `/api/auth-groups/{id}/users/{uid}` | `{username?, password?}` |
| DELETE | `/api/auth-groups/{id}/users/{uid}` | `204` |
| GET | `/api/auth-groups/{id}/users/{uid}/password` | `{password}`; audited as `auth_group_user.password_read` |
| GET | `/api/auth-group-options` | `[{id, name}]` — any signed-in User who may edit a Domain somewhere (Admin, or Editor on any Environment) |

## Permissions

- **Admin**: everything above.
- **Editor** (of the Domain's Environment) and **Project Editor**: sets a Domain's
  `authGroupId` through the existing Domain API; reads group names through
  `/api/auth-group-options`. Never sees users or passwords.
- **Operator, Viewer**: see the group's name on the Domains table (the Domain response
  carries `authGroupName` alongside `authGroupId`).

## Audit

Through the existing authz recording: each new route gets an entry in
`internal/authz/actions.go` (so `TestEveryChangeHasAnAction` and the route scan in
`routescan_test.go` cover it), and `internal/authz/record.go` resolves the group's name
as the target, like `certificates`:

- `POST/PATCH/DELETE /api/auth-groups…` → `auth_group.create`, `auth_group.update`,
  `auth_group.delete`;
- user routes → `auth_group_user.create`, `auth_group_user.update`,
  `auth_group_user.delete`. No password value is ever recorded;
- the reveal `GET` is one of the few audited reads →
  `auth_group_user.password_read`, target group name, detail `{username}`.

A Domain's group change is already recorded as `domain.update`.

## UI (antd 6.5.1)

Before writing UI, read Ant Design's `llms.txt` and the component pages used (Table,
Modal, Form, Select, Input.Password, Popconfirm, Tag, Typography copyable), per
AGENTS.md, and follow `docs/GUIDELINES.md`.

### Basic auth page

- Sidebar entry **Basic auth** after **Certificates**, icon `LockOutlined`, route
  `/basic-auth`, shown to Admins only, wired in `AppShell.tsx` and `routes.tsx` like
  Certificates; breadcrumb Overview › Basic auth.
- Intro line: "Traefik asks for a username and password of the group before serving a
  domain that uses it. Changes to users apply immediately."
- Table: **Name**, **Users** (count), **Domains** (count). Row click opens the group.
  Row action **Delete** (Popconfirm). A `409` shows the Domains that use it.
- **New group** button in the page header and the empty state.
- Non-UI logic in `pages/basic-auth-view.ts` with tests.

### Group detail (`/basic-auth/:id`)

- Name editable inline (Typography editable).
- Users table: **Username**, **Password** shown as `••••••••` with an eye button that
  fetches and shows it and a copy button that fetches and copies it, **Updated**. Row
  actions **Edit** (username, new password optional) and **Remove** (Popconfirm).
- **Add user** modal: Username, Password (Input.Password) with a **Generate** button
  that fills a 24-character random password.
- **Used by** list: each Domain as `host/path` linking to its Compose's Domains tab,
  with Project / Environment / Compose names. Empty: "No domain uses this group yet."

### Domain form and table

- `DomainFormModal.tsx`: a **Basic auth** Select below Middlewares, options "None" plus
  every group from `/api/auth-group-options`. For Admins the dropdown footer has
  **+ New group**, opening a modal with group name, username and password (Generate
  button); on success the new group is selected.
- Help text: "Visitors must sign in with a user of this group. Applies on the next
  deploy; edits to the group apply immediately."
- Domains table: a `LockOutlined` Tag with the group name on Domains that use one.

## Docs

- `docs/adr/0036-basic-auth-groups-through-the-file-provider.md` (written with this
  spec): the decision, the considered options (credentials per Domain; labels instead
  of the file provider; hash only), consequences.
- `README.md`: a Basic auth section.
- `CONTEXT.md`: the terms above.

## Testing

Go:

- Validation: name, username and password bounds; `:` and whitespace in username;
  duplicate name and username (`409`).
- Store: group and user CRUD; cascading user delete; `userCount` and `domainCount`;
  password changes refresh `hash` and `updated_at`.
- Reconcile: one file per group with the expected YAML; bcrypt hashes verify against
  the stored passwords; empty group writes one random user; stale `auth-*.yml`
  removed, `cert-*.yml` and other files left alone; mode `0600`.
- Labels: no group → unchanged output; group → `relist-auth-<id>@file` first in the
  chain, ahead of strip, prefix, own middlewares and retry; redirect router without it;
  `Generated` includes it.
- HTTP: Admin-only routes refuse Editor, Operator and Viewer (`403`); options route
  allowed for an Editor; delete in use → `409` with Domains; no `password` or `hash`
  in any response but the reveal; reveal is audited as `auth_group_user.password_read`; Domain
  update with an unknown `authGroupId` → `400`.

Frontend:

- `basic-auth-view.ts`: password generator length and alphabet; `409` Domain list
  formatting.
- Basic auth page: list renders, create, delete-in-use shows Domains.
- Group detail: reveal fetches and shows, add user with Generate, edit without a new
  password sends none.
- `DomainFormModal.test.tsx`: group select sends `authGroupId`; None sends `null`;
  **+ New group** only for Admins.

Manual (browser): a Domain with a group answers `401` and accepts a group user;
a wrong password is refused; changing that user's password takes effect without a
redeploy; the HTTP URL redirects to HTTPS before asking.
