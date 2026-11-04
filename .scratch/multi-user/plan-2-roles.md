# Multi-user Phase 2 (Roles) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A User who is not an Admin sees only the Projects and Environments they were granted, and can do
there only what their Role (Viewer, Operator, Editor) allows; Viewers and Operators never receive a secret
variable's value.

**Architecture:** Migration `00032` adds `grants`. A new `internal/grants` package owns the table, the
effective-role rule (`Access`) and the Grants routes. `internal/authz` grows from two levels to the full
table: it reads the pattern, aligns the path with it to get the Project, Environment and Compose ids,
checks the parents in the database, loads the User's `Access`, answers 404 / 403 or passes, and puts the
`Access` and a re-check function in the context. Handlers use the context only to filter lists and strip
secret values. `docker compose` stops inheriting the panel's environment. The web app gets `useCan`, a
member's menu, the Grant tree on a User page and a Members tab.

**Tech Stack:** Go 1.26 `net/http` ServeMux patterns, `go/ast` for the route scan, SQLite (modernc) +
goose; React 19, antd 6.5.1, react-router 8, SWR, vitest + Testing Library.

**Spec:** `.scratch/multi-user/spec.md` (Decision, Data model `grants`, Effective role, Permissions,
Authorization middleware, Users "Managing Users" grants routes, UI, Phases 2, Testing). Phase 1 plan:
`.scratch/multi-user/plan-1-users.md`.

## Global Constraints

- Migration file is `internal/migrations/00032_grants.sql`. Never edit a committed migration (`00031` included).
- `grants` columns, checks and the two partial unique indexes are exactly the spec's (Data model → `grants`).
- Effective role, verbatim from the spec: admin if `user.id == 1 or user.is_admin`; else the Environment
  Grant if one exists (`none` hides it); else the Project Grant; else none. A Project is visible when at
  least one of its Environments has a role other than none, or when it has no Environments and the User
  holds a Project Grant. A Project Editor is a User whose Project Grant is `editor`.
- `authz.Level`'s zero value stays `LevelAdmin`: a pattern missing from `authz.Routes` is admin-only.
- For a non-admin: not visible (no role, `none`, unknown id, non-numeric id, forged parent) → **404**
  `{"error":"not found"}`; visible but too low → **403** `{"error":"You don't have permission to do this"}`;
  database error → **500** `{"error":"authorization is temporarily unavailable"}`, never passed through.
- Admins (and the Owner) skip every scope check in `authz`; their requests behave exactly as today.
- The database has one connection (`SetMaxOpenConns(1)`). `authz` does all its reads before the handler
  runs. Handlers filter from the `Access` already in the context and never query grants themselves.
- A stripped secret is sent as `"value": ""` with `"secret": true`. Every other field is unchanged.
- `authz.CanReadSecrets(ctx, environmentID)` is false when the context carries no `Access` (fail closed).
- Commit messages are Conventional Commits with no scope and no trailers (`docs/agents/commit-conventions.md`).
- Frontend work reads `https://ant.design/llms.txt` and the component pages it uses (Tree or Table with
  Select, Tabs, Result, Tag, Tooltip), checked against antd 6.5.1, and follows `docs/GUIDELINES.md`.
- Every frontend change ends with `go generate ./internal/webassets/...` and a separate
  `chore: rebuild the embedded frontend` commit.

## Rulings this plan makes (the spec is silent or loose)

1. **Environment Variables are written by an Editor of that Environment** (the Editor row's "its
   Variables" read as the Environment's too). Reading keys is Viewer.
2. **`freshVolumes: true` on a Deployment needs Editor.** A plain Deployment is Operator; wiping volumes
   deletes data, which the spec gives only to Editors (Volume Explorer).
3. **Hidden wins for a Project Editor.** An Environment set to No access for them answers 404 on its
   delete and removal plan, as on every other route.
4. **`GET /api/deployments` (history) shows only visible rows**, filtered in SQL so totals and pages are
   right. Only `GET /api/deployments/queue` keeps the other rows, with `composeId`, `composeName`,
   `projectId`, `projectName`, `environmentId`, `environmentName` zeroed, so a member sees why theirs waits.
5. **The Members tab writes with `PUT /api/projects/{id}/grants`**, which replaces that Project's Grants in
   one transaction, so it never read-modify-writes a User's whole list.
6. **`POST /api/users` takes no `grants`.** An Admin invites, then sets Grants on the User page.
7. **A member's `/` redirects to `/projects`.** Overview, Monitoring and every host-wide page stay admin-only.
8. **The ADR is ADR-0031, amended.** Its title already covers roles; the spec's ADR-0032 stays for the
   Compose policy.
9. **`projectsService` reports `environmentCount` as the visible count** for a non-admin.

## Review Focus

1. **A route registered only under some `Deps`** (Buildx, Terminal, Docker, metrics, Migration,
   TraefikApply) escaping the classification test. The test scans source, not a running mux. Pinned in
   Task 4 (`TestEveryRouteIsClassified`).
2. **A forged path**: Project A's Environment under Project B, a Compose under the wrong Environment, a
   non-numeric id. Expect 404 for a member who can see both. Pinned in Task 3 (`TestForgedParentIsNotFound`).
3. **A secret leaking through a second door**: effective variables, the Variable Set entries, or a
   compose-variables list read by an Operator. Pinned in Task 6 (`TestNoSecretValueReachesAViewerOrOperator`).
4. **Access removed mid-Terminal**: the Grant drops to Viewer, or the User is disabled, while a shell is
   open. Expect the socket closed within one re-check interval. Pinned in Task 8 (`TestTerminalClosesWhenAccessIsRemoved`).
5. **`${RELIST_ADMIN_PASSWORD}` in a Compose file** interpolating the panel's value in Preview or a deploy.
   Expect empty. Pinned in Task 7 (`TestComposeCLIEnvDropsPanelVariables`).

---

### Task 1: Migration `00032_grants.sql`

**Files:**
- Create: `internal/migrations/00032_grants.sql`
- Modify: `internal/database/files_test.go`, `internal/database/version_test.go`,
  `internal/migration/preflight_test.go` (every assertion that names 31 as the latest version → 32)
- Test: `internal/database/database_test.go`

**Interfaces:**
- Produces table `grants(user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, project_id
  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE, environment_id INTEGER NULL REFERENCES
  environments(id) ON DELETE CASCADE, role TEXT NOT NULL CHECK (role IN ('none','viewer','operator','editor')),
  CHECK (environment_id IS NOT NULL OR role <> 'none'))`, indexes `grants_project_idx ON grants(user_id,
  project_id) WHERE environment_id IS NULL` (UNIQUE), `grants_environment_idx ON grants(user_id,
  environment_id) WHERE environment_id IS NOT NULL` (UNIQUE), `grants_project_id_idx ON grants(project_id)`.

- [ ] **Step 1: Write the failing test** `TestGrantsMigration` in `internal/database/database_test.go`:
  insert a user 2, a project, an environment; assert a Project Grant `viewer` and an Environment Grant
  `none` insert; a Project Grant `none` fails (CHECK); a second Project Grant for the same user and project
  fails (UNIQUE); deleting the environment deletes its grant; deleting user 2 deletes the rest.
- [ ] **Step 2: Run** `go test ./internal/database/ -run TestGrantsMigration` — Expected: FAIL, no such table.
- [ ] **Step 3: Write the migration** with a `-- +goose Down` that drops the table.
- [ ] **Step 4: Bump the version assertions** (31 → 32) and run `go test ./internal/database/ ./internal/migration/` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the grants table`

### Task 2: `internal/grants`: roles, effective access, Grants routes

**Files:**
- Create: `internal/grants/access.go`, `internal/grants/store.go`, `internal/grants/http.go`
- Modify: `internal/httpapi/httpapi.go` (register `grants.New(db).RegisterRoutes(mux)`)
- Test: `internal/grants/access_test.go`, `internal/grants/http_test.go`

**Interfaces:**
- Consumes: Task 1's table.
- Produces:
  - `type Role int` with `RoleNone, RoleViewer, RoleOperator, RoleEditor` in that order;
    `ParseRole(string) (Role, bool)` (accepts `none|viewer|operator|editor`), `(Role) String() string`.
  - `type Access struct` (fields unexported) with `Admin bool` exported, and:
    - `func Load(ctx context.Context, q Querier, userID int64, isAdmin bool) (Access, error)`, where
      `type Querier interface{ QueryContext(context.Context, string, ...any) (*sql.Rows, error) }`.
      An admin returns `Access{Admin: true}` with no query.
    - `(Access) EnvironmentRole(environmentID int64) Role`. Admin → `RoleEditor`.
    - `(Access) ProjectGrant(projectID int64) Role`, the Project Grant alone. Admin → `RoleEditor`.
    - `(Access) ProjectVisible(projectID int64) bool`
    - `(Access) VisibleEnvironments(projectID int64) []int64`, ascending.
    - `(Access) AllVisibleEnvironments() []int64`, for the Deployment history filter. Admin → nil.
    - `(Access) View() *AccessView`. Admin → nil. Otherwise
      `{"projects":[{"id":1,"role":"editor"|null,"environments":[{"id":3,"role":"viewer"}]}]}`: visible
      Projects only, ordered by id; `role` is the Project Grant or null; only Environments with a role
      other than none are listed.
  - `type Grant struct { UserID int64; Email string; ProjectID int64; EnvironmentID *int64; Role Role }`
    with JSON `{"userId","email","projectId","environmentId","role"}`.
  - Routes (admin-only through `authz`'s default):
    - `GET /api/users/{id}/grants` → `[Grant]` ordered by project, then environment (null first). 404 for
      an unknown user.
    - `PUT /api/users/{id}/grants` body `[{"projectId","environmentId","role"}]` → replaces all of the
      User's Grants in one transaction, answers the new list.
    - `GET /api/projects/{id}/grants` → `[Grant]` of every User on that Project. 404 for an unknown Project.
    - `PUT /api/projects/{id}/grants` body `[{"userId","environmentId","role"}]` → replaces all Grants
      on that Project in one transaction, answers the new list.
  - Validation for both PUTs, each answered 400 with the message given:
    - unknown role: `role must be none, viewer, operator or editor`;
    - `none` without an Environment: `No access is only for an Environment`;
    - an Environment not in that Project: `environment N does not belong to project M`;
    - an unknown Project or User: `project N not found`, `user N not found`;
    - the same target twice: `grant for the same target listed twice`.

- [ ] **Step 1: Write the failing tests** in `access_test.go`, table-driven over a seeded database. One
  Project P has Environments E1 and E2, an empty Project Q, and an untouched Project R.

  | grants | expect |
  |---|---|
  | none | P, Q and R not visible; `EnvironmentRole(E1) == RoleNone` |
  | P=viewer | E1 = E2 = viewer; P visible |
  | P=viewer, E2=editor | E1 viewer, E2 editor |
  | P=editor, E1=none | E1 none, E2 editor; `ProjectGrant(P) == RoleEditor`; `VisibleEnvironments(P) == [E2]` |
  | E1=none, E2=none, P=viewer | P not visible |
  | E1=operator only | P visible, E2 none, `ProjectGrant(P) == RoleNone` |
  | Q=viewer | Q visible, with no Environments |
  | admin | `Admin`, every role editor, `View() == nil` |

  Plus `TestAccessViewListsVisibleOnly`, which checks the JSON of the `P=editor, E1=none` case.
- [ ] **Step 2: Run** `go test ./internal/grants/` — Expected: FAIL, package has no `Load`.
- [ ] **Step 3: Implement `access.go`.** Make two queries: the User's grants, then
  `SELECT id, project_id FROM environments WHERE project_id IN (…the granted projects…)`. Compute every
  Environment's effective role once into maps.
- [ ] **Step 4: Write the failing HTTP tests** in `http_test.go` over an `httptest` mux: the PUT/GET round
  trip for a User and for a Project; each 400 above; a PUT that fails validation leaves the old Grants in
  place; a project PUT leaves that User's Grants on other Projects alone.
- [ ] **Step 5: Implement `store.go` and `http.go`; register in `NewHandler`.** Run
  `go test ./internal/grants/` — Expected: PASS.
- [ ] **Step 6: Commit** `feat: store per-Project and per-Environment grants and work out each user's access`

### Task 3: `authz` core: levels, scope, Access in the context

**Files:**
- Modify: `internal/authz/authz.go`
- Create: `internal/authz/scope.go`, `internal/authz/context.go`
- Modify: `internal/auth/auth.go` (export `func (s *Service) CurrentUser(r *http.Request) (User, bool, error)`,
  the existing `authenticated`), `internal/httpapi/httpapi.go`
- Test: `internal/authz/authz_test.go`, `internal/authz/scope_test.go`

**Interfaces:**
- Consumes: `grants.Load`, `grants.Access`, `grants.Role`.
- Produces:
  - Levels, in this order: `LevelAdmin` (zero), `LevelPublic` (auth lets it through without a session;
    authz treats it as SignedIn), `LevelSignedIn`, `LevelViewer`, `LevelOperator`, `LevelEditor`,
    `LevelProjectEditor`. `Rule{Level Level}` is unchanged.
  - `type Scope struct { ProjectID, EnvironmentID, ComposeID int64 }`, and
    `func scopeOf(pattern, path string) (Scope, bool)`:
    - Aligns the pattern's path segments with the request's. It never calls `r.PathValue`, which the mux
      fills only inside `ServeHTTP`.
    - `{projectID}`, or `{id}` right after `projects`, is the Project. `{environmentID}`, or `{id}` right
      after `environments`, is the Environment. `{composeID}` is the Compose.
    - Every other wildcard is ignored. A non-positive or non-numeric id gives `ok == false`.
  - `func New(db *sql.DB, currentUser func(*http.Request) (auth.User, bool, error)) *Authorizer`
  - `func (a *Authorizer) Middleware(mux *http.ServeMux, next http.Handler) http.Handler`. The free
    function `Middleware` goes away.
  - Context helpers in `context.go`:
    - `AccessFrom(ctx) (grants.Access, bool)`;
    - `ScopeFrom(ctx) (Scope, bool)`;
    - `WithAccess(ctx, grants.Access) context.Context`, for tests and `Middleware`;
    - `CanReadSecrets(ctx, environmentID int64) bool`;
    - `StillAllowed(ctx) func(context.Context) (bool, error)`, which is nil when absent.
- Decision for a request with a signed-in User, in order:
  1. `pattern == ""` or `"/"` → pass, unchanged from phase 1.
  2. Admin → put `WithAccess(Access{Admin:true})` and pass.
  3. Load `Access` (500 on error) and put it in the context.
  4. Rule level `LevelPublic`/`LevelSignedIn` → pass. `LevelAdmin` with no scope → 403.
  5. Scope from `scopeOf`. `ok == false` → 404.
  6. Check parents in one query:
     - Compose: `SELECT e.project_id, c.environment_id FROM composes c JOIN environments e ON e.id = c.environment_id WHERE c.id = ?`.
     - Environment: `SELECT project_id FROM environments WHERE id = ?`.
     - Project: `SELECT 1 FROM projects WHERE id = ?`.
     - No row, or a parent that differs from the path → 404.
  7. Visibility:
     - With an Environment: its role must not be none.
     - Project only: `ProjectVisible`.
     - Otherwise → 404.
  8. Level:
     - `LevelViewer`/`LevelOperator`/`LevelEditor` against the Environment's role. A Project-only scope
       at `LevelViewer` passes once visible.
     - `LevelProjectEditor` needs `ProjectGrant == RoleEditor`.
     - `LevelAdmin` → 403.
     - Too low → 403.
  9. Put the Scope and a `StillAllowed` closure in the context. The closure repeats steps 2–8 for the
     same pattern and path, with a fresh `currentUser` on a clone of the request's headers. It reports
     false on 401/403/404 and returns the error on a database failure.
- A request with no User (public or self-authenticated paths) passes untouched, as today.

- [ ] **Step 1: Write the failing scope tests** in `scope_test.go`:

  | pattern | path | expected scope |
  |---|---|---|
  | `GET /api/projects/{id}` | `/api/projects/7` | `{7,0,0}` |
  | `DELETE /api/projects/{projectID}/environments/{id}` | `/api/projects/7/environments/9` | `{7,9,0}` |
  | `GET …/composes/{composeID}/services/{service}/metrics` | `…/composes/11/services/web/metrics` | `{7,9,11}` |
  | `GET /api/projects/{id}` | `/api/projects/abc` | `ok == false` |
  | `GET /api/projects/{id}` | `/api/projects/0` | `ok == false` |

- [ ] **Step 2: Write the failing middleware tests** in `authz_test.go`. Use a stub mux that registers a
  handful of patterns, each answering 204 and echoing `ScopeFrom`. Use a real migrated database with
  Projects A and B, A's Environments A1 and A2, B's Environment B1, Compose C under A1, and User 2:
  - `TestUnknownPatternIsAdminOnly`: a pattern absent from `Routes` → 403 for User 2, 204 for an admin.
  - `TestNoGrantIsNotFound`: User 2 with no Grant → 404 on `GET /api/projects/A`.
  - `TestViewerCannotRestart`: A=viewer → 204 on a Viewer route under C, 403 on `POST …/C/restart`.
  - `TestEnvironmentGrantOverridesProject`: A=editor, A2=viewer → Editor route 204 under A1, 403 under A2.
  - `TestNoAccessEnvironmentIsNotFound`: A=editor, A2=none → 404 under A2, including
    `DELETE /api/projects/A/environments/A2` (ruling 3).
  - `TestForgedParentIsNotFound`: A=editor, B=editor; A1 under B → 404; C under A2 → 404; a
    non-numeric id → 404.
  - `TestProjectEditorLevel`: A=viewer, A1=editor → 403 on `POST /api/projects/A/environments`;
    A=editor → 204.
  - `TestDatabaseErrorIsNotPassed`: database closed → 500, handler not reached.
  - `TestStillAllowedSeesRevokedGrant`: the closure returns true, then false after the Grant is deleted.
  - `TestCanReadSecretsWithoutAccessIsFalse`.
- [ ] **Step 3: Run** `go test ./internal/authz/` — Expected: FAIL to compile.
- [ ] **Step 4: Implement**, and switch `httpapi.go` to
  `authz.New(db, authService.CurrentUser).Middleware(mux, inner)`.
- [ ] **Step 5: Run** `go test ./internal/authz/ ./internal/httpapi/ ./internal/auth/` — Expected: PASS.
  Phase 1's httpapi tests still pass because the table still holds only the Signed-in routes.
- [ ] **Step 6: Commit** `feat: decide a member's access per Project, Environment and Compose in one middleware`

### Task 4: The full route table and its tests

**Files:**
- Modify: `internal/authz/routes.go`
- Create: `internal/authz/routescan_test.go`
- Modify: `internal/httpapi/authz_test.go` (delete `TestSignedInRoutesAreRegistered`; the scan replaces it.
  `TestDemotionTakesEffectOnNextRequest` probes `GET /api/users` instead of `GET /api/projects`, which
  becomes Signed-in)
- Test: `internal/authz/matrix_test.go`

**Interfaces:**
- Consumes: Task 3's levels.
- Produces: `Routes` covering every pattern the scan finds. `C` below stands for
  `/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}`, and `E` for
  `/api/projects/{projectID}/environments/{environmentID}`.

| Level | Patterns |
|---|---|
| Public | `/`, `GET /healthz`, `POST /api/auth/login`, `GET/POST /api/invites/{token}`, `POST /api/migration/claim`, `GET /api/migration/preflight`, `POST /api/migration/start`, `GET /api/migration/items/{id}`, `GET /api/migration/items/{id}/digest`, `POST /api/migration/abort`, `POST /api/migration/finish` |
| SignedIn | phase 1's eight (status, logout, account, password, 2fa ×3, migration status), `GET /api/projects`, `GET /api/deployments`, `GET /api/deployments/queue`, `GET /api/ssh-keys`, `GET /api/builders` |
| Viewer | `GET /api/projects/{id}`, `GET /api/projects/{projectID}/environments`, `GET E/variables`, `GET E/composes`, `GET C`, `GET C/builder`, `GET C/logs`, `GET C/services`, `GET C/services/{service}/effective-variables`, `GET C/services/{service}/metrics`, `GET C/zero-downtime`, `GET C/bindings`, `GET C/deployments`, `GET C/deployments/{number}`, `GET C/deployments/{number}/log`, `GET C/domains`, `GET C/source`, `GET C/source/dockerfiles`, `GET C/variable-sets`, `GET C/variables` |
| Operator | `POST C/deployments`, `POST C/deployments/{number}/cancel`, `POST C/restart`, `POST C/stop` |
| Editor | `POST/PUT/DELETE` on `E/variables…`; `POST E/composes`; `PUT C`, `DELETE C`, `GET C/removal-plan`, `PUT C/builder`, `POST C/preview`, `PUT C/zero-downtime/{service}`, `PUT C/bindings/{service}`; every write under `C/domains`, `C/source` (`PUT`, `DELETE`, `POST test`, `POST known-hosts/reset`, `POST upload`), `C/variable-sets…`, `C/variables…`; `GET C/terminal`, `GET C/terminal/services`; every `C/volumes…` route |
| ProjectEditor | `PUT /api/projects/{id}`, `POST /api/projects/{projectID}/environments`, `DELETE /api/projects/{projectID}/environments/{id}`, `GET /api/projects/{projectID}/environments/{id}/removal-plan` |
| Admin (left out of the map) | everything else, including `POST /api/projects`, `DELETE /api/projects/{id}`, `GET /api/projects/{id}/removal-plan`, the Grants and Users routes and every host-wide route |

- [ ] **Step 1: Write `TestEveryRouteIsClassified`** in `routescan_test.go`.
  - It parses every non-test `.go` file under `../` with `go/parser`.
  - In each function named `RegisterRoutes` or `NewHandler`, it collects the first argument of every
    `.HandleFunc`/`.Handle` call. It evaluates that argument as a string, following basic literals, `+`,
    parentheses and package-level or local `const` identifiers.
  - `RegisterControlRoutes` is excluded: the control socket has no session.
  - It fails on an argument it cannot evaluate, and on a scanned pattern missing from `Routes` that is not
    in an explicit `adminOnly` list in the test (the Admin row's patterns).
  - Also write `TestRoutesNameRegisteredPatterns`: every `Routes` key is among the scanned patterns.
  - Also write `TestScopedRulesHaveTheirWildcard`:
    - Viewer, Operator and Editor patterns contain `{projectID}` or `projects/{id}`.
    - Operator and Editor patterns other than the `E/variables` writes and `POST E/composes` contain
      `{composeID}`.
    - ProjectEditor patterns contain a Project wildcard.
- [ ] **Step 2: Run** `go test ./internal/authz/ -run 'Classified|RegisteredPatterns|Wildcard'` — Expected:
  FAIL, listing the unclassified patterns.
- [ ] **Step 3: Fill `Routes`** from the table above.
- [ ] **Step 4: Write `TestRoleMatrix`** in `matrix_test.go`.
  - Use a stub mux with every `Routes` key plus the `adminOnly` list, each answering 204.
  - Set wildcards to the seeded ids (`{service}`, `{number}`, `{name}`, `{token}` and the other ids take
    `web`, `1`, `x`).
  - Run it for each subject, and assert the code each pattern's level dictates:

    | subject | Viewer route | Operator route | Editor route | ProjectEditor route | Admin route |
    |---|---|---|---|---|---|
    | no Grant | 404 | 404 | 404 | 404 | 404 (scoped) / 403 (global) |
    | `none` on the Environment | 404 | 404 | 404 | 404 | 404 / 403 |
    | Viewer on the Environment | 204 | 403 | 403 | 403 | 403 |
    | Operator on the Environment | 204 | 204 | 403 | 403 | 403 |
    | Editor on the Environment | 204 | 204 | 204 | 403 | 403 |
    | Project Editor with Viewer on the Environment | 204 | 403 | 403 | 204 | 403 |
    | admin | 204 | 204 | 204 | 204 | 204 |
    | Owner | 204 | 204 | 204 | 204 | 204 |

  - Public and SignedIn routes answer 204 for every subject.
- [ ] **Step 5: Run** `go test ./internal/authz/ ./internal/httpapi/` — Expected: PASS.
- [ ] **Step 6: Commit** `feat: classify every route by role`

### Task 5: Status access, list filtering and handler-level rules

**Files:**
- Modify: `internal/auth/auth.go` (add `func (s *Service) WithAccess(fn func(context.Context, User) (any, error)) *Service`;
  `status` adds `"access": fn(…)` for a signed-in User, or `null` when fn is nil or returns nil)
- Modify: `internal/httpapi/httpapi.go` (pass `func(ctx, u) { a, err := grants.Load(ctx, db, u.ID, u.IsAdmin); return a.View(), err }`)
- Modify: `internal/projects/projects.go` (list: keep `ProjectVisible`, set `EnvironmentCount = len(VisibleEnvironments)`;
  get: same count)
- Modify: `internal/environments/environments.go` (list: keep visible Environments)
- Modify: `internal/deployments/global.go`, `internal/deployments/store.go` (`GlobalQuery.EnvironmentIDs []int64`; nil = all;
  non-nil adds `AND environments.id IN (…)`, and an empty slice returns no rows), `internal/deployments/http.go`
  (queue redaction, ruling 4; `freshVolumes` needs `EnvironmentRole ≥ RoleEditor` else 403, ruling 2)
- Modify: `internal/sshkeys/http.go` (non-admin: `[{id, name}]` only)
- Modify: `internal/builders/http.go` (non-admin: `{defaultBuilderId, builders: [{id, name}]}` only, and no Docker capacity call)
- Test: `internal/httpapi/roles_test.go` (new; uses phase 1's `signInAsUser` and a new `grant(t, server, userID, projectID, environmentID *int64, role string)`)

**Interfaces:**
- Consumes: `authz.AccessFrom`, `grants.Access`.
- Produces: `GET /api/auth/status` → `{…, "access": AccessView | null}`.

- [ ] **Step 1: Write the failing tests** in `roles_test.go`:
  - `TestStatusReportsAccess`: a member with P=editor and E1=none gets
    `access.projects[0].environments` holding only E2; an admin gets `"access": null`.
  - `TestProjectListShowsOnlyVisible`: two Projects, a Grant on one → one row with the visible
    `environmentCount`.
  - `TestEnvironmentListHidesNoAccess`.
  - `TestDeploymentHistoryShowsOnlyVisible`: Deployments on a visible and a hidden Compose → `total == 1`.
  - `TestQueueRedactsHiddenRows`: a queued hidden Deployment appears with `composeId == 0` and
    `projectName == ""`, and keeps its `status`.
  - `TestOperatorCannotDeployWithFreshVolumes`: 403, and a plain deploy is accepted.
  - `TestSSHKeysAndBuildersAreReducedForMembers`: a member gets no `publicKey` or `usedBy` and no
    `cpus`; an admin's response is unchanged.
- [ ] **Step 2: Run** `go test ./internal/httpapi/ -run 'Status|Visible|NoAccess|Queue|FreshVolumes|Reduced'` — Expected: FAIL.
- [ ] **Step 3: Implement** each change listed under Files.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: show members only what they were granted in lists and the auth status`

### Task 6: Secret values only for Editors

**Files:**
- Modify:
  - `internal/environmentvariables/environmentvariables.go` (list);
  - `internal/composevariables/composevariables.go` (list);
  - `internal/variablesets/variablesets.go` (list: entries);
  - `internal/composes/variablesets.go` (`effectiveVariables`).
- Modify the package tests that call these handlers without the middleware
  (`internal/environmentvariables/service_test.go`, `internal/composes/variablesets_test.go`, and any
  other the run finds). Wrap their mux in a handler that sets `authz.WithAccess(ctx, grants.Access{Admin: true})`.
- Test: `internal/httpapi/roles_test.go`

**Interfaces:**
- Consumes: `authz.CanReadSecrets(ctx, environmentID)`.

- [ ] **Step 1: Write `TestNoSecretValueReachesAViewerOrOperator`**.
  - Seed one secret and one plain variable at each layer: Environment, Compose, and a bound Variable Set.
  - Run the test as a Viewer, and again as an Operator.
  - Read the Environment variables, the Compose variables, the Variable Sets, and effective-variables for
    the bound Service.
  - Assert that the secret value string appears in no response body and the plain value does.
  - Assert `POST C/preview` → 403.
  - An Editor and an admin see the secret value.
- [ ] **Step 2: Run** `go test ./internal/httpapi/ -run NoSecretValue` — Expected: FAIL (value present).
- [ ] **Step 3: Strip `Value` on secret rows** when `!CanReadSecrets`. Effective variables take the
  Compose's `EnvironmentID`.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS. Any package test that breaks is fixed by the
  wrapper above, never by loosening the default.
- [ ] **Step 5: Commit** `feat: send secret variable values only to editors and admins`

### Task 7: `docker compose` without the panel's environment

**Files:**
- Create: `internal/dockerexec/cli_env.go`
- Modify:
  - `internal/dockerexec/dockerexec.go`: `command` sets `cmd.Env = cliEnv(os.Environ())`; `runOn`
    appends `BUILDX_BUILDER=` to `cmd.Env` instead of `os.Environ()`.
  - `internal/dockerexec/volume_resolver.go`: `execDocker` sets the same env.
  - `internal/dockerexec/volume_runner.go`: its `exec.CommandContext`.
  - Leave `panel_updater.go` alone: the Install's own compose needs its `RELIST_*` variables.
- Test: `internal/dockerexec/cli_env_test.go`

**Interfaces:**
- Produces: `func cliEnv(environ []string) []string`. It keeps only `PATH`, `HOME`, `TMPDIR`,
  `XDG_CONFIG_HOME`, `XDG_RUNTIME_DIR`, `BUILDX_CONFIG`, and every `DOCKER_*` variable, and drops
  everything else.

- [ ] **Step 1: Write `TestComposeCLIEnvDropsPanelVariables`**:
  - Input `PATH=/bin`, `HOME=/root`, `DOCKER_HOST=unix:///x`, `RELIST_ADMIN_PASSWORD=s3cret`,
    `RELIST_DATA_DIR=/data`, `COMPOSE_PROJECT_NAME=x`, `FOO=bar`.
  - Output: the first three only.
  - Add `TestRunOnPassesNoPanelVariables`, which replaces `command`, runs `runOn` with a builder, and asserts
    `cmd.Env` holds `BUILDX_BUILDER=b` and no `RELIST_` entry.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/ -run 'CLIEnv|PanelVariables'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/dockerexec/` — Expected: PASS.
- [ ] **Step 5: Commit** `fix: stop docker compose from reading the panel's own environment`

### Task 8: Terminal closes when access goes

**Files:**
- Modify: `internal/terminal/terminal.go` (`Options.AccessInterval time.Duration`, default 5 s),
  `internal/terminal/session.go` (`terminal` reads `authz.StillAllowed(r.Context())` and passes it to `run`;
  `run` starts `accessLoop` when it is non-nil)
- Test: `internal/terminal/access_test.go` (package-level, with the existing fake `Exec`), plus
  `TestTerminalClosesWhenAccessIsRemoved` in `internal/httpapi/terminal_test.go` through the real middleware

**Interfaces:**
- Produces: `func (s *Service) accessLoop(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc, still func(context.Context) (bool, error))`.
  On every tick it calls `still`:
  - false → `conn.Close(websocket.StatusPolicyViolation, "Your access to this Terminal was removed")`,
    then cancel;
  - an error → log it and keep the session; the next tick tries again.

- [ ] **Step 1: Write the failing tests**:
  - `TestAccessLoopClosesOnRevoke`: `still` flips to false after the first call, and the client reads
    close code 1008 with that reason within 3 intervals of 10 ms.
  - `TestAccessLoopKeepsSessionOnError`.
  - In httpapi, a member with Editor opens the terminal. Then their Grant drops to Viewer, the socket
    closes, and opening it again answers 403.
- [ ] **Step 2: Run** `go test ./internal/terminal/ ./internal/httpapi/ -run 'AccessLoop|AccessIsRemoved'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `feat: close a terminal when its user loses access`

### Task 9: Web: access in the store, `useCan`, a member's shell

**Files:**
- Modify:
  - `web/src/data/api.ts` (`AccessView`; `AuthStatus.access`; `Grant`; `listUserGrants`,
    `putUserGrants`, `listProjectGrants`, `putProjectGrants`);
  - `web/src/data/store.tsx` (`AuthState.access`);
  - `web/src/app/AppShell.tsx`, `web/src/app/routes.tsx`.
- Create: `web/src/data/useCan.ts`
- Delete: `web/src/pages/NoAccessPage.tsx`, if nothing still uses it
- Test: `web/src/data/useCan.test.tsx`, `web/src/app/AppShell.test.tsx`

**Interfaces:**
- Produces:
  - `type Level = 'viewer' | 'operator' | 'editor'`;
  - `useCan(environmentId: number | undefined, level: Level): boolean`, true for an admin;
  - `useIsProjectEditor(projectId: number | undefined): boolean`;
  - `useIsAdmin(): boolean`.
  - All three read `auth.user` and `auth.access` only, and make no request.

- [ ] **Step 1: Write the failing tests**:
  - `useCan` against an access fixture: an Environment role of viewer, operator or editor answers each
    level; an unknown Environment → false; an admin → true.
  - `useIsProjectEditor`: true only when the Project's `role` is `editor`.
  - AppShell for a member: the menu shows exactly Projects, Deployments and the account menu; no request
    goes to `/api/host/metrics`, `/api/vulnerabilities/summary` or `/api/panel-update/status`; `/` lands
    on `/projects`; `/docker` shows the 403 Result ("You don't have permission").
- [ ] **Step 2: Run** `pnpm --dir web exec vitest run src/data/useCan.test.tsx src/app/AppShell.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement.**
  - In AppShell, split phase 1's `adminView`. The project tree and the Deployment queue load for everyone.
  - Disk pressure, the vuln summary, Panel update status and the host menu group stay admin-only.
  - For a member, every admin-only route path renders `<Result status="403" title="You don't have permission" />`.
- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: give members a shell with their projects and deployments`

### Task 10: Web: actions follow the role

**Files:**
- Modify:
  - `web/src/pages/ProjectsPage.tsx`: New Project and Delete are admin only; Edit details needs Project
    Editor.
  - `web/src/pages/ProjectPage.tsx`:
    - New Environment needs Project Editor.
    - On the Compose rows, Deploy needs operator; Restart and Stop need operator; Delete needs editor.
    - New Compose needs editor.
    - Settings tab: project editing needs Project Editor; Delete Project is admin only.
  - `web/src/pages/ProjectSettingsTab.tsx`.
  - `web/src/pages/ComposePage.tsx`:
    - Deploy, Restart and Stop need operator.
    - Fresh volumes, Delete Compose and Save in the editor need editor.
    - The Terminal and Volumes tabs are hidden below editor.
    - The vuln summary and badges show for admins only.
  - The editing controls in `web/src/components/VariablesPanel.tsx`, `ComposeVariablesTab.tsx`,
    `VariableSetsCard.tsx`, `ComposeDomainsTab.tsx`, `SourceCard.tsx`, `ComposeBindingsCard.tsx`,
    `ZeroDowntimeCard.tsx` and `ComposeBuilderSelect.tsx` need editor. Hide them, or show them disabled
    with a Tooltip "Needs the Editor role", following GUIDELINES.
  - A secret row below editor shows `••••` and no reveal or copy button.
- Test: extend each page's existing `*.test.tsx` with a Viewer case and an Operator case.

- [ ] **Step 1: Write the failing tests**:
  - On ComposePage, a Viewer sees no Deploy, Restart, Stop, Terminal or Volumes.
  - An Operator sees Deploy, Restart and Stop, but no Fresh volumes and no Save.
  - On VariablesPanel, a Viewer sees a secret as `••••` with no reveal.
  - On ProjectsPage, a member sees no New Project.
  - On ProjectPage, a Project Editor sees New Environment and a plain Editor does not.
- [ ] **Step 2: Run** the touched test files — Expected: FAIL.
- [ ] **Step 3: Implement** with `useCan`, `useIsProjectEditor` and `useIsAdmin`.
- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: show each member only the actions their role allows`

### Task 11: Web: Grant tree on the User page and the Members tab

**Files:**
- Create: `web/src/pages/UserPage.tsx` (route `users/:userId`, `paths.user(id)`),
  `web/src/components/GrantTree.tsx`, `web/src/components/ProjectMembersTab.tsx`
- Modify:
  - `web/src/app/routes.tsx` and `web/src/app/paths.ts`;
  - `web/src/pages/UsersPage.tsx`: the email links to the User page; the ⋯ menu gains "Edit access".
  - `web/src/pages/ProjectPage.tsx`: a Members tab for admins, keyed `members` beside Settings.
- Test: `web/src/components/GrantTree.test.tsx`, `web/src/components/ProjectMembersTab.test.tsx`, `web/src/pages/UserPage.test.tsx`

**Interfaces:**
- `GrantTree({ value, onChange, projects })` is a controlled component over `Grant[]`.
  - One row per Project, with a Select: No grant, Viewer, Operator, Editor.
  - One row per Environment under it, with a Select: Same as Project, No access, Viewer, Operator, Editor.
  - "Same as Project" removes the Environment Grant; "No grant" removes the Project Grant.
- The User page has a Save button that calls `putUserGrants`. Owner and admin rows show a note "Admins
  see every Project" and the tree is read-only.
- The Members tab lists each User with a Grant on the Project: email, Project role, per-Environment
  override. "Add member" picks a User and a role. Save calls `putProjectGrants`.

- [ ] **Step 1: Write the failing tests**:
  - GrantTree:
    - Setting an Environment to No access emits `{environmentId, role: 'none'}`.
    - "Same as Project" drops that entry.
    - The Project Select has no No access option.
  - UserPage: Save sends the tree's value, and a 400 shows its message.
  - Members tab: adding a member and saving sends one `putProjectGrants` with the full list.
- [ ] **Step 2: Run** them — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: grant roles from the user page and a project's members tab`

### Task 12: Docs and the embedded frontend

**Files:**
- Modify:
  - `docs/adr/0031-users-and-roles.md`: a dated "Roles (phase 2)" section with the roles, the scope
    checks, 404 versus 403, secret stripping, the `docker compose` environment and rulings 1–9. Replace
    the consequence "Until roles exist…".
  - `CONTEXT.md`: add **Role** and **Grant** as the spec words them; change **User**'s last sentence;
    change **Environment Variable**'s secret flag per the spec.
  - `README.md`: a short "Users and roles" section, which states: **until the Compose policy ships, an
    Editor can take the host (a Compose that mounts the Docker socket or runs privileged, then the
    Terminal), so grant Editor only to people you would give root**.
  - `docs/adr/0016-…`, `0020-…`, `0022-…`: "the single admin owns the host" → "an Admin owns the host".
  - `.scratch/multi-user/spec.md`: add a "2026-11-04 — Phase 2 rulings" comment that lists rulings 1–9.
  - `internal/webassets/dist/**`.
- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run** `go generate ./internal/webassets/...`, `go vet ./...`, `go test ./...`, `pnpm --dir web test`,
  `pnpm --dir web typecheck` — Expected: all pass; `git status` shows only `internal/webassets/dist` changed by the generate step.
- [ ] **Step 3: Commit** `docs: record roles and grants in ADR-0031, the domain and the README`, then
  `chore: rebuild the embedded frontend`.

## Live check (after merge, on the install)

- Panel update to the release, `00032` applied, and the admin still signed in.
- A member with Viewer on one Environment sees only that Project, cannot restart, and sees secrets as `••••`.
- Raise them to Editor while their Terminal is open, then drop them back to Viewer: the Terminal closes
  within about 5 s.
