# Basic Auth Groups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admins manage host-wide Basic auth groups. A Domain picks one group, and Traefik then asks for a username and password of that group.

**Architecture:** A new Go package, `internal/authgroups`, holds validation, the SQLite store, the Traefik file writer and the HTTP service. It is modelled on `internal/certificates`. `Reconcile` rewrites `<data dir>/.traefik/dynamic/auth-<id>.yml` on startup and after every write. `internal/domains` gains `AuthGroupID` and puts `relist-auth-<id>@file` first in the router chain. The frontend adds a Basic auth page, a group detail page and a group Select on the Domain form.

**Tech Stack:** Go (`golang.org/x/crypto/bcrypt`, `gopkg.in/yaml.v3`, goose SQL migrations), React + antd 6.5.1 + SWR + vitest.

**Spec:** `.scratch/basic-auth/spec.md` · ADR: `docs/adr/0036-basic-auth-groups-through-the-file-provider.md`

## Global Constraints

- Never edit a committed migration. The new one is `internal/migrations/00040_basic_auth_groups.sql`; if `00040` is taken on `main` when you start, use the next free number. Timestamps are `INTEGER` unix seconds, like every other table.
- Responses never carry `password` or `hash`, except `GET /api/auth-groups/{id}/users/{uid}/password`.
- Group name: trimmed, 1–64 characters, unique ignoring case (`409`, field `name`).
- Username: 1–64 characters of printable ASCII (`0x21`–`0x7E`) except `:`; unique per group (`409`, field `username`).
- Password: 8–72 bytes (72 is bcrypt's limit in Go); outside the range → `400` field `password`.
- Generated password: 24 characters from `A-Za-z0-9`, made in the browser with `crypto.getRandomValues`.
- bcrypt cost: `bcrypt.DefaultCost`, hashed once when the password is set.
- Error body shape: `{"error": "...", "field": "..."}`, with `field` omitted when there is none. A delete in use returns `409 {"error": "...", "domains": [...]}`.
- Dynamic files: `auth-<id>.yml` in `certificates.DynamicDir(dataDir)`. Each file is mode `0600` and written as a temp file + rename. Reconcile removes only `auth-*.yml` files with no row.
- Middleware name: `relist-auth-<id>`. A Domain references it as `relist-auth-<id>@file`.
- Commit messages follow Conventional Commits with no scope (`docs/agents/commit-conventions.md`) and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Before any UI work, read `https://ant.design/llms.txt` and the Table, Modal, Form, Select, Input (Password), Popconfirm, Tag and Typography (editable, copyable) pages, and follow `docs/GUIDELINES.md`.
- UI copy fixed by the spec is copied verbatim: the intro line, the help text and the empty "Used by" text.

## Review Focus

1. **Passwords containing `$`, quotes, `#` or spaces.** These are what a password manager produces. The written hash must verify against the stored password, and the YAML must parse. Test in Task 2.
2. **Group names with YAML-special characters** (`a"b: c #x`), used as the `realm`. The file must be produced by `yaml.Marshal`, never by string formatting. Test in Task 2.
3. **Renaming a user without a new password.** The hash stays valid, and the file's line changes to the new username. Test in Task 1 (store) and Task 2 (file).
4. **Deleting a Compose or Domain that used a group.** The group must become deletable afterwards. `domains` rows cascade from `composes`, so counting must read the live rows. Test in Task 3.
5. **An Editor's Domain PUT from a client that omits `authGroupId`.** The value decodes as `null`, which means None. The UI always sends the field, so this is acceptable, but the test pins the behaviour so it is deliberate. Test in Task 4.

---

### Task 1: Migration, validation and store

**Files:**
- Create: `internal/migrations/00040_basic_auth_groups.sql` (SQL from the spec's Data model, timestamps `INTEGER`, plus `+goose Down` that drops the column (SQLite ≥ 3.35 `ALTER TABLE DROP COLUMN`) and both tables)
- Create: `internal/authgroups/store.go`
- Test: `internal/authgroups/store_test.go`

**Interfaces:**
- Produces:
  ```go
  type FieldError struct{ Field, Message string } // Error() = Field + ": " + Message
  var ErrNotFound = errors.New("not found")
  type ConflictError struct{ Field, Message string }

  type Group struct {
      ID          int64     `json:"id"`
      Name        string    `json:"name"`
      UserCount   int       `json:"userCount"`
      DomainCount int       `json:"domainCount"`
      CreatedAt   time.Time `json:"createdAt"`
      UpdatedAt   time.Time `json:"updatedAt"`
  }
  type User struct {
      ID        int64     `json:"id"`
      GroupID   int64     `json:"-"`
      Username  string    `json:"username"`
      Password  string    `json:"-"`
      Hash      string    `json:"-"`
      CreatedAt time.Time `json:"createdAt"`
      UpdatedAt time.Time `json:"updatedAt"`
  }
  type UsedBy struct { // one Domain using a group
      ID int64 `json:"id"`; Host string `json:"host"`; Path string `json:"path"`
      ProjectID int64 `json:"projectId"`; EnvironmentID int64 `json:"environmentId"`; ComposeID int64 `json:"composeId"`
      ProjectName string `json:"projectName"`; EnvironmentName string `json:"environmentName"`; ComposeName string `json:"composeName"`
  }
  type UserInput struct{ Username, Password string } // json "username","password"

  func CleanName(name string) (string, *FieldError)
  func CheckUsername(u string) *FieldError
  func CheckPassword(p string) *FieldError
  func NewStore(db *sql.DB) *Store
  func (s *Store) List(ctx) ([]Group, error)                  // by name, with counts
  func (s *Store) Get(ctx, id) (Group, error)                  // ErrNotFound
  func (s *Store) Create(ctx, name string, users []UserInput) (Group, error) // one tx
  func (s *Store) Rename(ctx, id int64, name string) (Group, error)
  func (s *Store) Delete(ctx, id int64) ([]UsedBy, error)      // non-empty UsedBy = refused, nothing deleted
  func (s *Store) Users(ctx, groupID int64) ([]User, error)    // by username, Password/Hash filled
  func (s *Store) AddUser(ctx, groupID int64, in UserInput) (User, error)
  func (s *Store) UpdateUser(ctx, groupID, userID int64, username *string, password *string) (User, error)
  func (s *Store) DeleteUser(ctx, groupID, userID int64) error
  func (s *Store) UsedBy(ctx, groupID int64) ([]UsedBy, error) // by host, path
  func (s *Store) Exists(ctx, id int64) (bool, error)
  func (s *Store) Options(ctx) ([]struct{ID int64; Name string}, error) // json id,name; by name
  ```
  Duplicate name and duplicate username come back as `*ConflictError` with the field set. Any change to a user also bumps the group's `updated_at`.

- [ ] **Step 1: Write failing tests.** Use the repo's existing test DB helper, the same one `internal/certificates/store_test.go` uses (migrated, `foreign_keys = ON`). Tests:
  - `TestCleanName`: `"  team  "` → `"team"`; `""` and a 65-character name are `*FieldError{Field:"name"}`.
  - `TestCheckUsername`: `alice` ok; `""`, `a:b`, `a b`, `ä`, and 65 characters are refused, all with `Field:"username"`.
  - `TestCheckPassword`: 7 bytes refused, 8 ok, 72 ok, 73 refused, all with `Field:"password"`.
  - `TestCreateWithUsersAndCounts`: `Create("Staging", [{alice, pw1…}])` → `List` gives `UserCount 1, DomainCount 0`. `Users` gives a `Hash` for which `bcrypt.CompareHashAndPassword(hash, pw)` is nil.
  - `TestDuplicateNameIgnoresCase`: `Create("staging")` after `"Staging"` returns a `*ConflictError` with `Field "name"`.
  - `TestDuplicateUsername`: adding `alice` twice returns a `*ConflictError` with `Field "username"`.
  - `TestUpdateUserUsernameOnlyKeepsHash`: rename `alice`→`al` with a nil password → `Hash` is unchanged and `UpdatedAt` moves.
  - `TestUpdateUserPasswordRehashes`: a new password gives a new `Hash` that verifies against it.
  - `TestDeleteRefusedWhileUsed`: insert a project, environment, compose and a domain with `auth_group_id` set. `Delete` returns 1 `UsedBy` with all the names, and the group still exists. Delete the compose row; `Delete` now returns nil and the group's users are gone (cascade).
- [ ] **Step 2: Run `go test ./internal/authgroups/...`.** Expected: FAIL, the package does not build.
- [ ] **Step 3: Write the migration and `store.go`.** Map the UNIQUE-constraint error to `ConflictError` the way `internal/domains/store.go` `explain` detects it (`strings.Contains(lower(err), "unique constraint")`).
- [ ] **Step 4: Run `go test ./internal/authgroups/... ./internal/database/...`.** Expected: PASS. The database tests check the migration count and version; update their expected numbers if they pin them.
- [ ] **Step 5: Commit** `feat: store basic auth groups and their users`.

### Task 2: Traefik dynamic files and startup reconcile

**Files:**
- Create: `internal/authgroups/traefik.go`
- Modify: `main.go` (next to the `certificates.Reconcile` call at about line 131)
- Test: `internal/authgroups/traefik_test.go`

**Interfaces:**
- Consumes: `Store.List`, `Store.Users` (Task 1); `certificates.DynamicDir(dataDir string) string`.
- Produces:
  ```go
  func MiddlewareName(groupID int64) string            // "relist-auth-<id>"
  func WriteDir(dir string, groups []GroupUsers) error // GroupUsers{Group; Users []User}
  func Reconcile(ctx context.Context, store *Store, dataDir string) error
  ```
  File content: the header line `# Written by relist-panel from the auth_groups table. Edits are overwritten.`, then `http.middlewares.relist-auth-<id>.basicAuth` with `realm: <name>`, `removeHeader: true` and `users: ["<username>:<hash>", …]` in username order. A group with no users gets one entry, `<random hex 32 bytes>:<bcrypt of random hex 32 bytes>`, generated on every write.

- [ ] **Step 1: Write failing tests:**
  - `TestWriteDirFile`: a group "Staging" with users `alice` (password `pa$$ "w#rd x`) and `bob`. Unmarshal the file with yaml.v3. Check `realm == "Staging"`, `removeHeader == true` and two users in order, and that the hash after the first `:` verifies against `alice`'s password.
  - `TestWriteDirYAMLSpecialName`: the name `a"b: c #x` round-trips as the realm.
  - `TestWriteDirEmptyGroupLocked`: a group with no users → exactly 1 user whose username is 64 hex characters. Writing twice gives different usernames.
  - `TestWriteDirRemovesStaleOnly`: pre-create `auth-99.yml`, `cert-1.yml` and `mine.yml`. After writing group 1, `auth-99.yml` is gone, the other two remain, and `auth-1.yml` exists with mode `0600`. No `*.tmp` files are left.
  - `TestReconcileCreatesMissingDir`: a data dir without `.traefik/dynamic` → no error, and the file exists.
- [ ] **Step 2: Run `go test ./internal/authgroups/ -run 'WriteDir|Reconcile'`.** Expected: FAIL, undefined.
- [ ] **Step 3: Implement `traefik.go`, modelled on `internal/certificates/traefik.go`** (with its own file pattern `^auth-\d+\.yml$`). In `main.go`, call `authgroups.Reconcile(ctx, authgroups.NewStore(db), cfg.DataDir)` after the certificates call. On failure, log it the same way and continue.
- [ ] **Step 4: Run `go test ./internal/authgroups/... && go build ./...`.** Expected: PASS.
- [ ] **Step 5: Commit** `feat: write basic auth groups to Traefik's file provider`.

### Task 3: HTTP service, authorization and audit

**Files:**
- Create: `internal/authgroups/http.go`
- Modify: `internal/httpapi/httpapi.go` (register next to `certificates.New(db, dataDir).RegisterRoutes(mux)`)
- Modify: `internal/authz/routes.go`, `internal/authz/actions.go`, `internal/authz/record.go` (`hostTables`), `internal/authz/routescan_test.go` (`adminOnly`)
- Test: `internal/authgroups/http_test.go`, `internal/authz/record_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–2; `authz.Member(ctx) (grants.Access, bool)`, `grants.Access.AllVisibleEnvironments()`, `grants.Access.EnvironmentRole(id) grants.Role`, `grants.RoleEditor`.
- Produces: `func New(db *sql.DB, dataDir string) *Service`, `func (s *Service) RegisterRoutes(mux *http.ServeMux)`, with the routes and bodies in the spec's API table. Group detail: `{…Group, users: [User], domains: [UsedBy]}`. The reveal route returns `{"password": "..."}`.
- Authz: only `GET /api/auth-group-options` goes in `Routes`, as `LevelSignedIn`. Its handler returns `[]` to a Member with no Environment where the role is `>= RoleEditor`. All the other auth-group patterns go in `adminOnly` in `routescan_test.go`.
- Actions (`actions.go`, "Host-wide (admin)" block):
  - `POST /api/auth-groups` → `auth_group.create`
  - `PATCH /api/auth-groups/{id}` → `auth_group.update`
  - `DELETE /api/auth-groups/{id}` → `auth_group.delete`
  - `POST /api/auth-groups/{id}/users` → `auth_group_user.create`
  - `PATCH /api/auth-groups/{id}/users/{uid}` → `auth_group_user.update`
  - `DELETE /api/auth-groups/{id}/users/{uid}` → `auth_group_user.delete`
  - `GET /api/auth-groups/{id}/users/{uid}/password` → `auth_group_user.password_read`

  `hostTables` gets `"auth-groups": "auth_groups"`, so the target is the group's name. Check that `hostTarget` handles patterns longer than 3 segments (`/api/auth-groups/{id}/users/{uid}`). It only looks at segments 0–2, so it should. The test proves it.

- [ ] **Step 1: Write failing tests** (`http_test.go`, calling the handler directly the way `internal/certificates/http_test.go` does):
  - `TestCreateGroupWithFirstUser`: POST `{name, users:[{username,password}]}` → `201`. The body has no `password` or `hash` key. `auth-<id>.yml` exists.
  - `TestFieldErrors`: a bad name → `400 field name`; a duplicate username → `409 field username`; a 7-character password → `400 field password`.
  - `TestGroupDetail`: GET returns users without passwords and a `domains` list.
  - `TestRevealPassword`: GET `…/password` → `{"password": original}`.
  - `TestPatchUserWithoutPasswordKeepsIt`: PATCH `{username:"al"}`, then reveal gives the original password.
  - `TestDeleteInUse`: a domain uses the group → `409` with `domains[0].host`. The file still exists.
  - `TestDeleteRemovesFile`: no domain uses the group → `204`, and the file is gone.
  - `TestOptionsForMember`: a context with an Access that is Editor in one environment → `[{id,name}]`; Viewer-only → `[]`.
  - `record_test.go`: `DELETE /api/auth-groups/6/users/2` and `GET /api/auth-groups/6/users/2/password` both record the target `"Staging"`.
- [ ] **Step 2: Run `go test ./internal/authgroups/... ./internal/authz/...`.** Expected: FAIL.
- [ ] **Step 3: Implement `http.go`.** Every write calls `Reconcile` before it responds (`500` when the file write fails, as `certificates.reconcileAndWrite` does). Register it in `httpapi.go`, then add the authz entries above.
- [ ] **Step 4: Run `go test ./internal/... `.** Expected: PASS, including `TestEveryRouteIsClassified` and `TestEveryChangeHasAnAction`.
- [ ] **Step 5: Commit** `feat: basic auth groups API for admins`.

### Task 4: Domains use a group

**Files:**
- Modify: `internal/domains/domains.go` (`Domain`, `Input`), `internal/domains/store.go` (columns, scan, create/update, list queries LEFT JOIN `auth_groups`), `internal/domains/labels.go` (`addDomain`)
- Test: `internal/domains/labels_test.go`, `internal/domains/domains_test.go` (or the existing HTTP test file in the package)

**Interfaces:**
- Consumes: the `domains.auth_group_id` column (Task 1); `authgroups.MiddlewareName` (Task 2). `domains` must not import `authgroups` if that creates a cycle; if it would, inline `fmt.Sprintf("relist-auth-%d@file", id)` in a `domains.authMiddleware(id int64) string` helper, with a test pinning the string.
- Produces: `Domain.AuthGroupID *int64` (`json:"authGroupId"`), `Domain.AuthGroupName string` (`json:"authGroupName"`, empty when there is none), `Input.AuthGroupID *int64` (`json:"authGroupId"`).

- [ ] **Step 1: Write failing tests:**
  - `TestAddDomainAuthGroupFirst` (labels): a domain with group 3, strip path, a user middleware `x@file` and zero-downtime → `…middlewares == "relist-auth-3@file,<n>-strip,x@file,<n>-retry"`.
  - `TestAddDomainAuthGroupNotOnRedirect`: HTTPS + default entrypoint + group → the `<router>-http.middlewares` label is exactly `<n>-redirect`.
  - `TestAddDomainNoAuthGroupUnchanged`: the existing expected label set is unchanged when `AuthGroupID` is nil.
  - `TestGeneratedIncludesAuthGroup`: `Generated(...)` contains the new middleware value.
  - HTTP: PUT with `authGroupId: 999` (no such group) → `400` with `field "authGroupId"`. With an existing group → `200`, and the body has `authGroupName`. With the field omitted → `authGroupId: null` (Review Focus 5).
- [ ] **Step 2: Run `go test ./internal/domains/...`.** Expected: FAIL.
- [ ] **Step 3: Implement it.** The existence check goes in `Service.readInput` after `Validate`: `SELECT 1 FROM auth_groups WHERE id = ?`, and `400 {"error":"basic auth group does not exist","field":"authGroupId"}` when the row is missing. In `addDomain`, prepend to `chain` before the strip middleware.
- [ ] **Step 4: Run `go test ./internal/... `.** Expected: PASS (compose policy tests included).
- [ ] **Step 5: Commit** `feat: protect a domain with a basic auth group`.

### Task 5: Frontend data layer

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`
- Create: `web/src/pages/basic-auth-view.ts`, `web/src/pages/basic-auth-view.test.ts`

**Interfaces:**
- Produces (types): `AuthGroup {id,name,userCount,domainCount,createdAt,updatedAt}`, `AuthGroupUser {id,username,createdAt,updatedAt}`, `AuthGroupDomain` (the `UsedBy` fields), `AuthGroupDetail = AuthGroup & {users: AuthGroupUser[]; domains: AuthGroupDomain[]}`, `AuthGroupOption {id,name}`. `DomainInput` gains `authGroupId: number | null`; `Domain` gains `authGroupName: string`.
- Produces (api): `listAuthGroups`, `getAuthGroup(id)`, `createAuthGroup({name, users?})`, `renameAuthGroup(id, name)`, `deleteAuthGroup(id)`, `addAuthGroupUser(id, {username,password})`, `updateAuthGroupUser(id, uid, {username?, password?})`, `deleteAuthGroupUser(id, uid)`, `revealAuthGroupPassword(id, uid): Promise<string>`, `listAuthGroupOptions()`.
- Produces (view): `generatePassword(length = 24): string`, `usedByLabel(d: AuthGroupDomain): string` (`"host/path — Project / Environment / Compose"`, where path `/` shows as an empty string), `inUseDomains(error: ApiError): AuthGroupDomain[]` (reads `domains` from a 409 body; `[]` otherwise).
- `domains-view.ts`: `domainFormValues` defaults `authGroupId: null` and copies it on edit; `domainInput` passes it through.

- [ ] **Step 1: Write failing tests** in `basic-auth-view.test.ts`: `generatePassword()` has length 24 and matches `/^[A-Za-z0-9]+$/`, and 50 calls are all distinct. `usedByLabel` covers path `/` and `/api`. `inUseDomains` returns the list from a 409 and `[]` from a 500. Extend `domains-view.test.ts` for `authGroupId` defaults and pass-through. Check that `ApiError` keeps the response body; if it does not, add a `body?: unknown` to it and test that.
- [ ] **Step 2: Run `cd web && npx vitest run src/pages/basic-auth-view.test.ts src/pages/domains-view.test.ts`.** Expected: FAIL.
- [ ] **Step 3: Implement it.**
- [ ] **Step 4: Run the same command plus `npx tsc --noEmit`.** Expected: PASS.
- [ ] **Step 5: Commit** `feat: frontend types and API for basic auth groups`.

### Task 6: Basic auth pages and menu

**Files:**
- Create: `web/src/pages/BasicAuthPage.tsx`, `web/src/pages/BasicAuthGroupPage.tsx`, `web/src/components/AuthGroupUserModal.tsx`, plus `.test.tsx` for both pages
- Modify: `web/src/app/paths.ts` (`basicAuth: '/basic-auth'`, `basicAuthGroup: (id) => '/basic-auth/' + id`, patterns `basic-auth` and `basic-auth/:groupId`), `web/src/app/routes.tsx`, `web/src/app/AppShell.tsx` (menu item after Certificates, `LockOutlined`, admin-only list; breadcrumbs Overview › Basic auth › <group name>)

**Interfaces:**
- Consumes: Task 5 api, view helpers and types; `useIsAdmin` is not needed (the admin menu already gates it).
- Produces: `AuthGroupUserModal({open, user?, saving, error?, onCancel, onSubmit(input: {username: string; password?: string})})`. When adding, the password is required. When editing, it is optional and empty means unchanged. A **Generate** button fills the field with `generatePassword()` and shows it. Task 7 reuses this modal for the first user of a new group.

- [ ] **Step 1: Write failing tests:**
  - `BasicAuthPage.test.tsx`: renders rows with user and domain counts; **New group** creates a group (calls `createAuthGroup` with the name); delete in use shows each `usedByLabel` in the error.
  - `BasicAuthGroupPage.test.tsx`: passwords render as `••••••••`; clicking the eye calls `revealAuthGroupPassword` and shows the value; **Add user** with **Generate** submits a 24-character password; **Edit** with an empty password sends no `password` key; the "Used by" list links to the Compose's Domains tab (use the existing compose path helper in `paths.ts`); with no domains it shows "No domain uses this group yet."
- [ ] **Step 2: Run `cd web && npx vitest run src/pages/BasicAuth`.** Expected: FAIL.
- [ ] **Step 3: Implement it, modelled on `CertificatesPage.tsx`** for the page shell, `PageHeader`, `EmptyState` and the error handling. The intro line is copied verbatim from the spec. The name is edited inline with `Typography.Title editable`, and saving it calls `renameAuthGroup`. Copy uses `navigator.clipboard.writeText` after the reveal call, followed by `message.success('Copied')`.
- [ ] **Step 4: Run `npx vitest run && npx tsc --noEmit`.** Expected: PASS, including `AppShell.test.tsx` and `paths.test.ts` (update them for the new entries).
- [ ] **Step 5: Commit** `feat: Basic auth page for managing groups`.

### Task 7: Domain form and Domains table

**Files:**
- Modify: `web/src/components/DomainFormModal.tsx`, `web/src/components/ComposeDomainsTab.tsx`
- Create: `web/src/components/NewAuthGroupModal.tsx`
- Test: `web/src/components/DomainFormModal.test.tsx` and the `ComposeDomainsTab` test, if one exists (otherwise add the tag check to `DomainFormModal.test.tsx`'s neighbour test)

**Interfaces:**
- Consumes: `listAuthGroupOptions`, `createAuthGroup` (Task 5), `AuthGroupUserModal` field layout (Task 6), `useIsAdmin()`.
- Produces: `NewAuthGroupModal({open, onCancel, onCreated(group: AuthGroupOption)})`. It holds the group name, the username and the password with Generate, and posts one `createAuthGroup({name, users:[{username,password}]})`.

- [ ] **Step 1: Write failing tests:**
  - Picking a group submits `authGroupId: <id>`; **None** submits `null`.
  - The **+ New group** footer is visible for an admin and absent for a non-admin.
  - Creating a group through the footer selects it in the form.
  - The help text reads exactly "Visitors must sign in with a user of this group. Applies on the next deploy; edits to the group apply immediately."
  - The Domains table shows a Tag with the group name when `authGroupName` is set, and none otherwise.
- [ ] **Step 2: Run `cd web && npx vitest run src/components/DomainFormModal.test.tsx`.** Expected: FAIL.
- [ ] **Step 3: Implement it.** The Select goes below Middlewares, with options from `useSWR('/api/auth-group-options')` and the footer built with `popupRender`/`dropdownRender`. Pick whichever the installed antd 6.5.1 documents as current, and avoid the deprecated one. Add `'authGroupId'` to `FIELDS` so a `400 field authGroupId` lands on the field.
- [ ] **Step 4: Run `npx vitest run && npx tsc --noEmit && npm run build`.** Expected: PASS.
- [ ] **Step 5: Commit** `feat: pick a basic auth group on the domain form`.

### Task 8: Docs and live check

**Files:**
- Modify: `CONTEXT.md` (the **Basic auth group** term and the Domain sentence, verbatim from the spec), `README.md` (a Basic auth section near Certificates: what a group is, that user edits apply at once and the Domain pick on deploy, and that it needs the file-provider flags from the Certificates upgrade note)

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run the full suite: `go test ./... && go vet ./... && (cd web && npx vitest run && npx tsc --noEmit && npm run build)`.** Expected: all PASS.
- [ ] **Step 3: Browser and curl check on a dev panel.** Create group `Staging` with `alice` and put it on an HTTPS Domain, then deploy. Then:
  - `curl -sk -o /dev/null -w '%{http_code}' https://<host>/` gives `401`.
  - `-u alice:<pw>` gives the app's status.
  - A wrong password gives `401`.
  - Change alice's password in the UI and check, without redeploying, that the old password is refused and the new one works within a few seconds.
  - `http://<host>/` gives a `301`/`308` to HTTPS with no `401`.
  - The reveal shows in the Audit log as `auth_group_user.password_read`.
- [ ] **Step 4: Commit** `docs: basic auth groups in CONTEXT and README`.
