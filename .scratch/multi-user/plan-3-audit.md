# Multi-user Phase 3 (Audit log) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every change made through the panel, every sensitive read and every sign-in is recorded with
who, when, what and where; admins read it all on an Audit log page, Editors read their Project's events
on an Activity tab, and old events expire after the retention period.

**Architecture:** Migration `00033` adds `audit_events` and `audit_settings`. A new `internal/audit`
package owns the table (record, list, prune, settings) and the per-request context hooks
(`audit.Detail`, `audit.Target`, `audit.Emit`). `internal/authz` gains an `Actions` table (pattern →
action) and records, after the handler, every actioned request that ended 2xx (`ok`) and every one it
refused (`denied`), with the scope's names read before the handler runs. Handlers add detail and emit
the reads the middleware cannot see (secret values, Terminal open/close). `internal/auth` and the CLI
record sign-ins and host commands. A new `internal/auditapi` serves the lists and settings and runs the
daily retention. The web app gets an Audit log page and a Project Activity tab.

**Tech Stack:** Go 1.26 `net/http`, SQLite (modernc) + goose; React 19, antd 6.5.1, react-router 8, SWR,
vitest + Testing Library.

**Spec:** `.scratch/multi-user/spec.md` (Decision "Full Audit log", Data model `audit_events`,
Authorization middleware, CLI, Audit log, UI, Phases 3, Testing). Phase 2 plan:
`.scratch/multi-user/plan-2-roles.md`. Branch `claude/multi-user-audit`, stacked on
`claude/multi-user-roles` (PR #47).

## Global Constraints

- Migration file is `internal/migrations/00033_audit.sql`. Never edit a committed migration (`00032` included).
- `audit_events` columns, the `outcome` check and the three indexes are exactly the spec's (Data model →
  `audit_events`). `audit_settings` is one row `id = 1`, `retention_days INTEGER NOT NULL DEFAULT 90
  CHECK (retention_days >= 0)`; 0 means forever.
- `at` is unix **milliseconds**. `actor` is the User's email at the time, or `cli`. `ip` is the host of
  `r.RemoteAddr` (the TCP peer), never a forwarded header.
- **No variable value ever lands in `detail` or `target`**, secret or not. Keys, Domain hosts, roles and
  names may.
- `actor_user_id`, `project_id`, `environment_id`, `compose_id` have no foreign keys: events outlive what
  they name.
- The database has one connection. Audit writes happen after the handler returns (middleware) or after
  the handler's own reads and writes finished (`Emit`), never inside an open transaction.
- An audit write that fails is logged (`log.Printf("audit: ...")`) and never changes the response.
- Work the panel starts by itself (scheduled scans, Panel update scheduler, queue workers) is not recorded.
- Public routes (login, invites, migration pull) have no action; `internal/auth` records sign-in and
  invite use itself.
- Commit messages are Conventional Commits with no scope and no trailers.
- Frontend work reads `https://ant.design/llms.txt` and the pages for Table, DatePicker (RangePicker),
  Select, Tabs, InputNumber, checked against antd 6.5.1, and follows `docs/GUIDELINES.md`.
- Every frontend change ends with `go generate ./internal/webassets/...` and a separate
  `chore: rebuild the embedded frontend` commit.

## Rulings this plan makes (the spec is silent or loose)

1. **Actions are an explicit `authz.Actions` map** (pattern → action), separate from `Routes`, because
   admin-only routes are not in `Routes`. Every registered non-GET, non-Public pattern must have one;
   GETs have one only for the spec's sensitive reads. Names are `<resource>.<verb>`.
2. **`denied` is recorded only for actioned patterns.** A refused plain GET (a stale link, a hidden
   Project's page) is not an event.
3. **Target names are read before the handler** for every actioned request, admins included, with one
   query (`project / environment / compose`), so a delete keeps the name of what it deleted. An unscoped
   route's target is what the handler sets with `audit.Target`, else the request path.
4. **A secret read is one `secret.read` event per list response that carries at least one secret
   value**, with `detail.keys`. The four lists are Environment Variables, Compose Variables, Variable
   Sets and effective variables. There is no reveal endpoint; the list is the read.
5. **The Terminal records itself** (`terminal.open`, then `terminal.close` with `detail.seconds`): the
   WebSocket upgrade answers 101 on a hijacked connection, which the middleware cannot see as 2xx.
6. **Sign-in events:** `auth.login` (ok), `auth.login_failed` (outcome `denied`, `detail.email` = the
   email typed, actor = that email or `unknown`), `auth.invite_accepted` (ok). Logout, password, two-factor,
   invite and reset creation are ordinary actioned routes.
7. **CLI events**, actor `cli`: `cli.create_admin`, `cli.reset_password`, `cli.change_email`,
   `cli.reset_2fa`, each with `detail.email`.
8. **Settings API mirrors the Access log's** `{retentionDays, keepForever}` so the web reuses one
   retention card; stored as the spec's `retention_days` (0 = forever).
9. **Filters:** `actor` matches the stored email exactly (case-insensitive) or `cli`; `action` is a
   prefix (`compose.` matches every Compose action); `from`/`to` are unix ms, inclusive.
10. **`GET /api/projects/{id}/audit` is Viewer in `authz.Routes`** (hidden Project → 404); the handler
    answers 403 unless the User is admin or Editor on at least one of its visible Environments, and then
    returns events whose `environment_id` is one they edit, or is NULL with `project_id = id`.
11. **The HTTP side lives in `internal/auditapi`**: `authz` imports `audit`, and the Project route needs
    `authz.AccessFrom`, so one package cannot hold both.

## Review Focus

1. **A delete's event naming nothing** because the names were read after the row was gone. Expect the
   event's `target` to read `alpha / production / web`. Pinned in Task 3 (`TestDeleteEventKeepsNames`).
2. **A variable value in `detail` or `target`** through a create/update body or a hook. Expect no row
   containing the value. Pinned in Task 4 (`TestNoVariableValueInAudit`).
3. **An Editor of `staging` reading `production`'s events** on the Activity tab, or a Viewer reading any.
   Expect only staging's and the Project's own events, and 403 for a Viewer. Pinned in Task 6
   (`TestProjectAuditShowsOnlyEditedEnvironments`).
4. **An audit write failing** (database error) turning a successful change into an error. Expect the
   handler's status unchanged. Pinned in Task 3 (`TestAuditWriteFailureKeepsTheResponse`).
5. **Cursor paging with many events in one millisecond.** Expect 150 events in two pages of 100 and 50,
   no duplicates, newest first. Pinned in Task 2 (`TestListPagesByCursor`).

---

### Task 1: Migration `00033_audit.sql`

**Files:**
- Create: `internal/migrations/00033_audit.sql`
- Modify: every test that names 32 as the latest version (`internal/database/database_test.go`,
  `files_test.go`, `version_test.go`; grep for `32`) → 33
- Test: `internal/database/database_test.go`

**Interfaces:**
- Produces `audit_events(id, at, actor_user_id, actor, action, outcome, project_id, environment_id,
  compose_id, target, detail, ip)` with indexes `audit_events_at_idx(at)`,
  `audit_events_project_idx(project_id, at)`, `audit_events_actor_idx(actor_user_id, at)`; and
  `audit_settings(id INTEGER PRIMARY KEY CHECK (id = 1), retention_days ...)` seeded with `(1, 90)`.

- [ ] **Step 1: Write the failing test** `TestAuditMigration`: after migrating, `SELECT retention_days FROM
  audit_settings` is 90; inserting an event with `outcome = 'maybe'` fails; one with `outcome = 'ok'` and
  `project_id = 999` (no such Project) succeeds.
- [ ] **Step 2: Run** `go test ./internal/database/` — Expected: FAIL (no such table).
- [ ] **Step 3: Write the migration and bump the version asserts.**
- [ ] **Step 4: Run** `go test ./internal/database/ ./internal/migration/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the audit events table`

### Task 2: `internal/audit` — store and context hooks

**Files:**
- Create: `internal/audit/event.go`, `internal/audit/store.go`, `internal/audit/context.go`
- Test: `internal/audit/store_test.go`, `internal/audit/context_test.go`

**Interfaces:**
- Produces:
  - `type Event struct { At time.Time; ActorUserID int64 /* 0 = none */; Actor, Action, Outcome string;
    ProjectID, EnvironmentID, ComposeID int64 /* 0 = NULL */; Target string; Detail map[string]any; IP string }`
    and `const OutcomeOK = "ok"`, `OutcomeDenied = "denied"`, `ActorCLI = "cli"`.
  - `type Execer interface { ExecContext(ctx, query string, args ...any) (sql.Result, error) }`;
    `func Record(ctx context.Context, db Execer, e Event) error` (detail marshalled to JSON, NULL when empty).
  - `type Filter struct { Cursor int64; Actor, Action string; From, To int64; ProjectID int64;
    EnvironmentIDs []int64; ProjectOwn bool }` — `ProjectID` limits to that Project; with
    `EnvironmentIDs` non-nil, also `environment_id IN (...) OR (ProjectOwn AND environment_id IS NULL)`.
  - `type Row` = the JSON shape `{id, at, actorUserId|null, actor, action, outcome, projectId|null,
    environmentId|null, composeId|null, target, detail|null, ip|null}` (`at` in ms).
  - `func List(ctx, q Querier, f Filter) (rows []Row, nextCursor int64, err error)` — newest first by
    `id DESC`, `id < Cursor` when set, 100 per page, `nextCursor` 0 on the last page.
  - `func Settings(ctx, q) (retentionDays int, err error)`, `func SetSettings(ctx, db, days int) error`
    (`days < 0` → `ErrInvalidRetention`), `func Prune(ctx, db, now time.Time) (int64, error)` (no-op when 0).
  - Context: `func Begin(ctx context.Context, base Event, resolveTarget func(context.Context) string,
    record func(context.Context, Event) error) (context.Context, *Pending)`; `func Detail(ctx, key string, value any)`; `func Target(ctx, target string)`;
    `func Emit(ctx, action string, detail map[string]any)` — records a second event from the pending base
    with that action, outcome ok, now, calling `resolveTarget` when the base has no target yet; all three
    are no-ops on a context without `Begin`.
    `(*Pending).Event() Event` returns the base merged with Detail/Target.

- [ ] **Step 1: Write the failing tests:**
  - `TestRecordAndList`: three events; `List` returns them newest first with `detail` decoded and NULL
    ids as `null` in JSON.
  - `TestListPagesByCursor`: 150 events with the same `at`; page 1 has 100 and `nextCursor`, page 2 has
    50 and `nextCursor` 0; the union has 150 distinct ids.
  - `TestListFilters`: actor (case-insensitive), action prefix `compose.` (and `compose_variable.x` not
    matched), from/to inclusive, ProjectID + EnvironmentIDs + ProjectOwn.
  - `TestPruneKeepsRecentAndForever`: retention 1 day deletes a 2-day-old event, keeps a fresh one;
    retention 0 deletes nothing.
  - `TestContextHooks`: `Detail`/`Target` show in `Pending.Event()`; `Emit` calls `record` with the base's
    actor and scope; all are no-ops on `context.Background()`.
- [ ] **Step 2: Run** `go test ./internal/audit/` — Expected: FAIL (package missing).
- [ ] **Step 3: Implement.** The action prefix uses `LIKE ? ESCAPE '\'` with `%`, `_`, `\` escaped.
- [ ] **Step 4: Run** `go test ./internal/audit/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: store audit events with cursor paging and retention`

### Task 3: `authz` records actions

**Files:**
- Create: `internal/authz/actions.go`, `internal/authz/record.go`
- Modify: `internal/authz/authz.go` (Middleware), `internal/httpapi/httpapi.go` (`authz.New` gets the recorder)
- Test: `internal/authz/actions_test.go`, `internal/authz/record_test.go`

**Interfaces:**
- Consumes: `audit.Event`, `audit.Record`, `audit.Begin`, `Scope`, `auth.User{ID, Email}`.
- Produces: `var Actions map[string]string`; `func (a *Authorizer) WithRecorder(rec func(context.Context,
  audit.Event) error) *Authorizer` (default: `audit.Record` on `a.db`).

Naming rule for `Actions`: `<resource>.<verb>`; resource is the singular of the entity changed, in
snake_case (`project`, `environment`, `environment_variable`, `compose`, `compose_variable`,
`variable_set`, `variable_set_entry`, `binding`, `domain`, `source`, `zero_downtime`, `compose_builder`,
`volume`, `deployment`, `user`, `grant`, `ssh_key`, `certificate`, `builder`, `image`, `traefik`,
`access_log`, `panel_update`, `docker`, `vulnerability`, `auth`, `audit_settings`); verb is
`create`/`update`/`delete` from POST/PUT·PATCH/DELETE unless the last static segment is a verb
(`restart`, `stop`, `cancel`, `preview`, `test`, `upload`, `reset`, `apply`, …), which is used as-is.

- [ ] **Step 1: Write the failing tests:**
  - `TestEveryChangeHasAnAction`: every registered pattern (the route scan's list) whose method is not
    GET and whose level is not Public has an `Actions` entry; every `Actions` key is registered; every
    value matches `^[a-z_0-9]+\.[a-z_0-9]+$`; GET keys are exactly `GET <compose>/volumes/read`,
    `GET <compose>/volumes/archive`, `GET /api/traefik/access-logs/{name}`.
  - `TestKeyActions`: `PUT <compose>` → `compose.update`, `POST <compose>/deployments` →
    `deployment.create`, `POST <compose>/deployments/{number}/cancel` → `deployment.cancel`,
    `POST <compose>/restart` → `compose.restart`, `PUT /api/users/{id}/grants` and
    `PUT /api/projects/{id}/grants` → `grant.set`, `POST /api/users` → `user.create`,
    `POST /api/users/{id}/reset` → `user.reset`, `PUT /api/audit/settings` → `audit_settings.update`.
  - `TestEveryActionIsRecorded`: for each `Actions` pattern, an admin request through the middleware to
    a stub handler answering 204 records exactly one event with that action, outcome ok, actor the
    admin's email, and the path's scope ids.
  - `TestNonSuccessIsNotRecorded`: the stub answers 400 → no event.
  - `TestRefusalIsRecordedDenied`: a member's `PUT <compose>` with Viewer → 403 and one `denied` event;
    a refused plain `GET <compose>` → no event.
  - `TestDeleteEventKeepsNames`: the stub deletes the Compose row, then answers 204; the event's target
    is `alpha / production / web` (fixture names).
  - `TestAuditWriteFailureKeepsTheResponse`: a recorder that returns an error; the response is still 204.
  - `TestPlainGetIsNotWrapped`: a GET without an action reaches the handler with the original
    `http.ResponseWriter` (a Hijacker in the test stays one), and `audit.Emit` from that handler records
    an event whose target is the scope's names.
- [ ] **Step 2: Run** `go test ./internal/authz/` — Expected: FAIL.
- [ ] **Step 3: Implement.** For every request with a User, call `audit.Begin` (no database read; the
  target resolves lazily for `Emit`), so handlers of plain GETs (secret lists, Terminal) can emit. For an
  actioned pattern also: read names (`LEFT JOIN` on the scope ids) and
  set them as the base target before the handler; wrap `w` in a status recorder that also implements `Flush`,
  `Hijack` and `Unwrap`; after the handler, record when the status is 2xx. On a refusal of an actioned
  pattern, record `denied` before writing the error. IP = `net.SplitHostPort(r.RemoteAddr)` host.
- [ ] **Step 4: Run** `go test ./internal/authz/ ./internal/httpapi/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: record every change and refusal in the audit log`

### Task 4: Detail, targets and the reads only handlers see

**Files:**
- Modify: `internal/environmentvariables`, `internal/composevariables`, `internal/variablesets`,
  `internal/composes/variablesets.go` (effective variables), `internal/domains`, `internal/grants/http.go`,
  `internal/auth/users.go`, `internal/projects` (create), `internal/terminal/session.go`
- Test: `internal/httpapi/audit_test.go`

**Interfaces:**
- Consumes: `audit.Detail`, `audit.Target`, `audit.Emit`.
- Detail keys: variables and set entries `key`; Variable Sets `name`; Domains `host`; Grants
  `detail.grants` = `[{projectId, environmentId, role}]` and target = the User's email (user route) or the
  Project's name (project route); Users target = email, PATCH adds `isAdmin`/`disabled` when sent;
  Project create target = its name.

- [ ] **Step 1: Write the failing tests** in `internal/httpapi/audit_test.go` (real server, `roleFixture`):
  - `TestNoVariableValueInAudit`: as admin create and update an Environment Variable, a Compose Variable
    and a Variable Set entry with value `s3cr3t-v4lue` (secret and not); then read each list as an
    Editor. No `audit_events` row has `s3cr3t-v4lue` in `target` or `detail`; the create events carry
    `detail.key`.
  - `TestSecretReadIsRecorded`: an Editor reads Environment Variables holding one secret → one
    `secret.read` with `detail.keys = ["TOKEN"]`; a Viewer's read → none; a list with no secret → none.
  - `TestGrantEventNamesTheUser`: `PUT /api/users/2/grants` → `grant.set`, target `member@example.com`.
  - `TestTerminalOpenAndCloseAreRecorded`: open and close a Terminal (existing terminal test harness) →
    `terminal.open`, then `terminal.close` with `detail.seconds` ≥ 0.
- [ ] **Step 2: Run** `go test ./internal/httpapi/ -run 'Audit|SecretRead|GrantEvent|TerminalOpen'` — Expected: FAIL.
- [ ] **Step 3: Implement the hooks.** `secret.read` is emitted only where `authz.SecretsVisible` /
  `CanReadSecrets` was true and at least one returned row is secret with a non-empty value.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add keys, names and sensitive reads to audit events`

### Task 5: Sign-in and CLI events

**Files:**
- Modify: `internal/auth/auth.go` (login), `internal/auth/invites.go` (accept), `cli.go`
- Test: `internal/auth/audit_test.go`, `cli_test.go`

**Interfaces:**
- Consumes: `audit.Record`.
- Produces: `auth.Service` records through `audit.Record(ctx, s.db, ...)`; CLI commands call it on the db
  they already open.

- [ ] **Step 1: Write the failing tests:**
  - `TestLoginIsRecorded`: a good login → `auth.login` ok with actor email and IP `192.0.2.1` (from
    `RemoteAddr`); a wrong password → `auth.login_failed`, outcome denied, `detail.email`; an unknown
    email → `auth.login_failed`, actor `unknown`. The password typed appears nowhere in the row.
  - `TestInviteAcceptIsRecorded`: accepting an invite → `auth.invite_accepted`, actor the new User.
  - `TestCLICommandsAreRecorded` (in `cli_test.go`): `create-admin`, `reset-password`, `change-email`,
    `reset-2fa` each leave one event with actor `cli` and their action.
- [ ] **Step 2: Run** `go test ./internal/auth/ . -run 'Recorded'` — Expected: FAIL.
- [ ] **Step 3: Implement.** A two-factor-code prompt (`needsCode`) is not a failure and records nothing.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: record sign-ins and host commands in the audit log`

### Task 6: `internal/auditapi` — reading, settings, retention

**Files:**
- Create: `internal/auditapi/http.go`, `internal/auditapi/retention.go`
- Modify: `internal/httpapi/httpapi.go` (routes + worker), `internal/authz/routes.go`
  (`GET /api/projects/{id}/audit` → Viewer), `internal/authz/routescan_test.go` (`GET /api/audit`,
  `GET /api/audit/settings`, `PUT /api/audit/settings` → `adminOnly`)
- Test: `internal/auditapi/http_test.go`, `internal/httpapi/audit_test.go`

**Interfaces:**
- Consumes: `audit.List/Settings/SetSettings/Prune`, `authz.AccessFrom`, `grants.Access`.
- Produces: `GET /api/audit?cursor&actor&action&from&to` → `{items: Row[], nextCursor: number|null}`;
  `GET /api/projects/{id}/audit` (same query and shape); `GET /api/audit/settings` →
  `{retentionDays, keepForever}`; `PUT /api/audit/settings` with the same body (400
  `{"error":..., "field":"retentionDays"}` when `retentionDays < 1` and not `keepForever`);
  `func (s *Service) RunRetention(ctx)` prunes at once, then every 00:00 UTC.

- [ ] **Step 1: Write the failing tests:**
  - `TestAuditListFiltersAndPages` (auditapi): query params reach `Filter`; a bad `from` → 400.
  - `TestProjectAuditShowsOnlyEditedEnvironments` (httpapi, `roleFixture`): events on alpha/production,
    alpha/staging, alpha itself (no Environment) and beta. Member Editor on staging, Viewer on
    production → staging's and alpha's own; Viewer only → 403; no Grant on beta → 404; admin → all of alpha.
  - `TestAuditSettingsRoundTrip`: PUT `{retentionDays: 30, keepForever: false}` → GET returns it;
    PUT `{retentionDays: 30, keepForever: true}` → GET returns `{retentionDays: 90, keepForever: true}`;
    PUT `{retentionDays: 0, keepForever: false}` → 400 with `field: "retentionDays"`.
  - `TestRetentionRunsOnStart`: `RunRetention` with a canceled-after-first-run context prunes old events.
  - The route scan and matrix tests pass with the new routes classified.
- [ ] **Step 2: Run** `go test ./internal/auditapi/ ./internal/httpapi/ ./internal/authz/` — Expected: FAIL.
- [ ] **Step 3: Implement.** `keepForever: true` stores `retention_days = 0`; GET answers
  `{retentionDays: 90, keepForever: true}` for 0 (the days field shows the default the operator returns
  to). The Project route builds the filter from `Access.VisibleEnvironments(projectID)` with
  `EnvironmentRole(id) >= grants.Editor`; admins get no environment filter.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: serve the audit log to admins and a project's editors`

### Task 7: Web data layer

**Files:**
- Modify: `web/src/data/api.ts`
- Test: `web/src/data/api.test.ts`

**Interfaces:**
- Produces: `type AuditEvent = { id: number; at: number; actorUserId: number | null; actor: string;
  action: string; outcome: 'ok' | 'denied'; projectId: number | null; environmentId: number | null;
  composeId: number | null; target: string; detail: Record<string, unknown> | null; ip: string | null }`;
  `type AuditQuery = { cursor?: number; actor?: string; action?: string; from?: number; to?: number }`;
  `type AuditPage = { items: AuditEvent[]; nextCursor: number | null }`;
  `api.listAudit(query)`, `api.listProjectAudit(projectId, query)`, `api.getAuditSettings()`,
  `api.putAuditSettings({retentionDays, keepForever})`.

- [ ] **Step 1: Write the failing test:** `listAudit({actor: 'a@b', action: 'compose.', from: 1, cursor: 9})`
  requests `/api/audit?cursor=9&actor=a%40b&action=compose.&from=1`; empty fields are left out;
  `listProjectAudit(3, {})` requests `/api/projects/3/audit`.
- [ ] **Step 2: Run** `pnpm --dir web exec vitest run src/data/api.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** it — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the audit calls to the web client`

### Task 8: Audit log page

**Files:**
- Create: `web/src/components/AuditTable.tsx`, `web/src/components/RetentionCard.tsx` (moved out of
  `pages/TraefikPage.tsx`, same props plus `onSave(values) => Promise<void>` and a `title`),
  `web/src/pages/AuditPage.tsx`
- Modify: `web/src/pages/TraefikPage.tsx`, `web/src/app/paths.ts` (`audit: '/audit'`, route `audit`),
  `web/src/app/routes.tsx`, `web/src/app/AppShell.tsx` (sider item `audit`, "Audit log", next to Users,
  admin only)
- Test: `web/src/components/AuditTable.test.tsx`, `web/src/pages/AuditPage.test.tsx`, existing
  `TraefikPage.test.tsx` and `AppShell.test.tsx` stay green

**Interfaces:**
- Produces: `AuditTable({ load: (query: AuditQuery) => Promise<AuditPage>, swrKey: string })` — filters
  (actor `Input`, action `Input` with placeholder `compose.`, time `DatePicker.RangePicker` with
  inclusive day bounds), a Table (Time as `RelativeTime` with the full date in its title, Actor, Action as
  `Tag`, Target, Detail as compact `key: value` text, Outcome — `denied` in red, IP), and **Load more**
  appending the next page until `nextCursor` is null.

- [ ] **Step 1: Write the failing tests:**
  - AuditTable: renders the first page; Load more calls `load` with `cursor` = the first page's
    `nextCursor` and appends; typing an action and applying calls `load` with `action` and no cursor;
    `denied` shows a red tag; a load error shows an Alert with Retry.
  - AuditPage: as admin it renders "Audit log" and the retention card, whose Save calls
    `putAuditSettings`; AppShell shows "Audit log" in the sider for an admin and not for a member.
- [ ] **Step 2: Run** them — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the audit log page`

### Task 9: Project Activity tab

**Files:**
- Modify: `web/src/pages/ProjectPage.tsx`, `web/src/pages/project-view.ts` (`activity` tab)
- Test: `web/src/pages/ProjectPage.test.tsx`, `web/src/pages/project-view.test.ts`

**Interfaces:**
- Consumes: `AuditTable`, `api.listProjectAudit`, `useAccess`.

- [ ] **Step 1: Write the failing tests:** `activeTab('activity')` is `activity`; an admin and a member
  with Editor on one Environment of the Project see an "Activity" tab whose table loads
  `listProjectAudit(1, …)`; a Viewer-only member does not see the tab, and on `.../activity` gets Composes.
- [ ] **Step 2: Run** them — Expected: FAIL.
- [ ] **Step 3: Implement.** Shown when `isAdmin` or any visible Environment of this Project has role
  `editor` in the access view.
- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: show a project's activity to its editors`

### Task 10: Docs and the embedded frontend

**Files:**
- Modify:
  - `docs/adr/0031-users-and-roles.md`: a dated "Audit log (phase 3)" section: what is recorded,
    `ok`/`denied`, names read before the handler, no variable values, who reads what, retention, rulings
    1–11, and the limit (not tamper-proof against an admin).
  - `CONTEXT.md`: **Audit event** as the spec words it.
  - `README.md` "Users and roles": one paragraph on the Audit log and its retention.
  - `.scratch/multi-user/spec.md`: a "2026-11-05 — Phase 3 rulings" comment listing rulings 1–11.
  - `internal/webassets/dist/**`.
- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run** `go generate ./internal/webassets/...`, `go vet ./...`, `go test ./...`,
  `pnpm --dir web test`, `pnpm --dir web typecheck` — Expected: all pass.
- [ ] **Step 3: Commit** `docs: record the audit log in ADR-0031, the domain and the README`, then
  `chore: rebuild the embedded frontend`.

## Live check (after merge, on the install)

- `00033` applied; the Audit log page lists your own sign-in.
- Edit a variable: the event shows its key, never its value. Delete a test Compose: its event still names it.
- A member with Editor on one Environment sees that Environment's events on the Project's Activity tab.
