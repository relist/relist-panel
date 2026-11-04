# Multi-user Phase 1 (Users) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Several people sign in to one panel with their own accounts, created from invite links. Admins
manage them, `users.id = 1` is the untouchable Owner, and a non-admin can reach only their Account until
Phase 2 adds roles.

**Architecture:** One new migration replaces `admin_credentials` with `users`, rebuilds `sessions` on
`user_id` and adds `user_tokens`. `internal/auth` keeps owning credentials and sessions, now for many
users. It puts the signed-in `auth.User` in the request context and gains the Users and Invite routes. A
new `internal/authz` middleware between auth and the mux lets non-admins through only on the Signed-in
routes; every other route stays admin-only by default. The web app gets an Invite page outside the gate,
a Users page, and a shell that shows a non-admin only Account.

**Tech Stack:** Go 1.25 `net/http` ServeMux patterns, SQLite (modernc) + goose, bcrypt; React 19,
antd 6.5.1, react-router 8, SWR, vitest + Testing Library.

**Spec:** `.scratch/multi-user/spec.md` (sections Users, Data model `users`/`sessions`/`user_tokens`,
Permissions "Signed-in" row, Phases 1).

## Global Constraints

- Migration file is `internal/migrations/00031_users.sql`. Never edit a committed migration.
- `foreign_keys` is ON inside goose's transaction: copy every row into new tables before dropping old
  ones (pattern: `00015_compose_owned_variable_sets.sql`).
- The Owner is `users.id = 1`. It is an admin whatever `is_admin` says. No web request may disable,
  delete, demote, or reset the Owner. The one exception: the Owner may reset itself.
- Invite token is 32 random bytes, base64url without padding, and is returned once. Only its SHA-256
  (lower-case hex) is stored.
- Invite lifetime is 7 days, reset lifetime 24 hours. A User has at most one unused token, and issuing a
  new one deletes the old.
- An expired, used, unknown token, or a token of a disabled User, answers
  `404 {"error":"This link is no longer valid"}`.
- A disabled or invited User signing in gets exactly the wrong-password answer:
  `401 {"error":"invalid email or password"}`. The failure counter does not move, because the account
  has no password to guess.
- Password rules are unchanged: `validatePassword`, 8 runes to 72 bytes.
- Commit messages are Conventional Commits with no scope and no trailers (`docs/agents/commit-conventions.md`).
- Frontend work reads `https://ant.design/llms.txt` and the component pages it uses (Table, Modal, Form,
  Tag, Switch, Popconfirm, Result, Typography.Paragraph copyable), checked against antd 6.5.1, and
  follows `docs/GUIDELINES.md`.

## Review Focus

1. **Upgrading a live install with a signed-in admin who has two-factor.** The same cookie, email,
   password and TOTP keep working after `00031`. Pinned in Task 1 (`TestUsersMigrationKeepsAdminAndSessions`).
2. **Inviting an email that already exists in another case** (`Bob@X.com` vs `bob@x.com`). Expect 409
   and no second row. Pinned in Task 4 (`TestCreateUserRejectsDuplicateEmailAnyCase`).
3. **An admin resetting the Owner to take over id 1.** Expect 403 unless the actor is id 1. Pinned in
   Task 4 (`TestOwnerCannotBeResetByAnotherAdmin`).
4. **An admin is demoted or disabled while signed in.** The very next request sees it: 403 on
   `/api/users` after demotion, 401 after disable, with nothing cached. Pinned in Task 3
   (`TestDemotionTakesEffectOnNextRequest`) and Task 4 (`TestDisableEndsSessions`).
5. **An invite link opened after the User was disabled, or opened twice.** Both answer the same 404 and
   leave no session. Pinned in Task 5 (`TestInviteRefusedForDisabledUser`, `TestInviteCannotBeReused`).

---

### Task 1: Migration `00031_users.sql`

**Files:**
- Create: `internal/migrations/00031_users.sql`
- Modify: `internal/database/database.go` (`checkSchema`, if it names `admin_credentials` or `admin_id`)
- Test: `internal/database/database_test.go`

**Interfaces:**
- Produces tables:
  - `users(id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0, disabled_at INTEGER NULL, last_login_at INTEGER NULL,
    created_at INTEGER NOT NULL, totp_secret TEXT NULL, totp_pending_secret TEXT NULL,
    totp_last_step INTEGER NULL, failed_attempts INTEGER NOT NULL DEFAULT 0, locked_until INTEGER NULL,
    password_from_env INTEGER NOT NULL DEFAULT 0)`
  - `sessions(token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)`, plus `sessions_expires_at_idx` and
    `sessions_user_id_idx`
  - `user_tokens(id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('invite','reset')), token_hash TEXT NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL, created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    used_at INTEGER NULL)`, plus a unique index `user_tokens_one_open_idx ON user_tokens(user_id) WHERE used_at IS NULL`
- `admin_credentials` no longer exists.

- [ ] **Step 1: Write the failing test `TestUsersMigrationKeepsAdminAndSessions`**

Follow `TestScopedVariablesMigrationUpgradesVersion13`: run `goose.UpTo(db, ".", 30)`. Insert an
`admin_credentials` row with id 1, email `admin@example.com`, a hash, `totp_secret 'JBSWY3DPEHPK3PXP'`,
`totp_last_step 7`, `failed_attempts 2` and `password_from_env 1`. Insert one session `tok` for
`admin_id 1`. Then run `Migrate(db)`. Assert:
- `SELECT id, email, password_hash, is_admin, totp_secret, totp_last_step, failed_attempts, password_from_env FROM users`
  returns exactly that row, with `is_admin = 1`.
- `SELECT user_id FROM sessions WHERE token='tok'` is `1`.
- `SELECT COUNT(*) FROM sqlite_master WHERE name='admin_credentials'` is `0`.
- `ForeignKeyViolations` is 0.
- Inserting a second `user_tokens` row with `used_at IS NULL` for the same user fails with a unique
  constraint error.

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/database/ -run TestUsersMigrationKeepsAdminAndSessions -v`
Expected: FAIL, `no such table: users`.

- [ ] **Step 3: Write `00031_users.sql`**

Order matters, because a drop cascades:
1. Create `users` and copy every row from `admin_credentials` with `is_admin = 1`.
2. Create `sessions_new` referencing `users` and copy `sessions` with `admin_id AS user_id`.
3. `DROP TABLE sessions`, then `DROP TABLE admin_credentials`.
4. `ALTER TABLE sessions_new RENAME TO sessions` and recreate both indexes.
5. Create `user_tokens` and its index.

Header comment in the file's style: one paragraph on why it rebuilds. The `-- +goose Down` section
reverses it, keeping only `id = 1`.

- [ ] **Step 4: Run the test and the whole package**

Run: `go test ./internal/database/ -v`
Expected: PASS, including `TestMigrateIsIdempotent`.

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/00031_users.sql internal/database/
git commit -m "feat: replace the single admin row with a users table"
```

---

### Task 2: `internal/auth` on `users`, with the signed-in User in context

**Files:**
- Modify: `internal/auth/auth.go`, `lock.go`, `password.go`, `reset.go`, `twofactor.go`
- Modify: `cli.go`, `dispatch.go` (help text only: "admin" becomes "user" where the command now takes any user)
- Modify the fixtures that write `admin_credentials` or `admin_id`: `cli_test.go`, `internal/auth/*_test.go`,
  `internal/httpapi/httpapi_test.go`, `auth_password_test.go`, `auth_twofactor_test.go`,
  `internal/migration/preflight_test.go`, `internal/database/database_test.go` (the busy-writer test)
- Test: `internal/auth/user_context_test.go`

**Interfaces:**
- Consumes: Task 1 tables.
- Produces:
  - `type User struct { ID int64; Email string; IsAdmin bool }`, where `IsAdmin` is true for `ID == 1`
    or `is_admin = 1`
  - `func (u User) IsOwner() bool` returns `u.ID == 1`
  - `func UserFrom(ctx context.Context) (User, bool)`
  - `func WithUser(ctx context.Context, u User) context.Context`, exported for tests in other packages
  - `Middleware` now loads the session's User with
    `JOIN users ON users.id = sessions.user_id WHERE users.disabled_at IS NULL`, and passes a request
    carrying `WithUser`
  - `GET /api/auth/status` adds `"user": {"id","email","isAdmin","isOwner"}` when authenticated
  - `const OwnerID int64 = 1`
  - `CreateAdmin` inserts `users(id 1, is_admin 1)` and still returns `ErrAdminAlreadyExists` when any
    user exists. `AdminExists` becomes "any user exists"; keep the name, update its comment.

- [ ] **Step 1: Write the failing tests in `user_context_test.go`**

- `TestMiddlewarePutsUserInContext`: an admin signs in through `login`. A handler behind `Middleware`
  reads `UserFrom` and gets `{ID:1, Email:"admin@example.com", IsAdmin:true}`, and `IsOwner()` is true.
- `TestDisabledUserSessionIsRejected`: insert user 2 with a password and a session. Set `disabled_at`.
  The request answers 401 `authentication required`.
- `TestLoginRefusesInvitedAndDisabledLikeAWrongPassword`: user 2 has `password_hash NULL`, and user 3 has
  a hash and `disabled_at` set. Both logins answer `401 {"error":"invalid email or password"}`, and
  `failed_attempts` stays 0 for both.
- `TestStatusReportsUser`: signed in as user 2 (`is_admin 0`), status JSON has
  `user == {id:2,email:"bob@example.com",isAdmin:false,isOwner:false}`.
- `TestLoginRecordsLastLogin`: after a successful login, `last_login_at` equals the service clock.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/auth/ -run 'UserInContext|DisabledUser|InvitedAndDisabled|StatusReportsUser|LastLogin' -v`
Expected: FAIL to compile, because `UserFrom` is undefined.

- [ ] **Step 3: Implement**

- Rename the type `admin` to `account`.
- In SQL, `admin_credentials` becomes `users` and `admin_id` becomes `user_id`.
- Scan `password_hash` into `sql.NullString`. A NULL hash, or a non-NULL `disabled_at`, is
  `sql.ErrNoRows` to the login path, so the existing wrong-password branch answers it.
- `completeLogin` sets `last_login_at`.
- Update the package doc: `// Package auth owns the panel's users, their credentials and sessions.`
- The CLI error strings `no admin account matches` and `more than one admin account exists` become
  `no user matches` and `more than one user exists`.

- [ ] **Step 4: Run the whole Go suite**

Run: `go test ./...`
Expected: PASS. Every fixture now writes `users` and `user_id`.

- [ ] **Step 5: Commit**

```bash
git add -A internal/auth cli.go cli_test.go dispatch.go internal/httpapi internal/migration internal/database
git commit -m "feat: sign users in from the users table and carry the user in the request"
```

---

### Task 3: `internal/authz`, non-admins only on Signed-in routes

**Files:**
- Create: `internal/authz/authz.go`, `internal/authz/routes.go`
- Modify: `internal/httpapi/httpapi.go` (wrap `inner`)
- Test: `internal/authz/authz_test.go`, `internal/httpapi/authz_test.go`

**Interfaces:**
- Consumes: `auth.UserFrom`, `auth.WithUser`.
- Produces:
  - `type Level int` with `const ( LevelAdmin Level = iota; LevelSignedIn )`. Phase 2 adds Viewer,
    Operator, Editor and ProjectEditor between them.
  - `type Rule struct { Level Level }`
  - `var Routes = map[string]Rule{...}`, keyed by the exact ServeMux pattern string
  - `func Middleware(mux *http.ServeMux, next http.Handler) http.Handler`
- Signed-in patterns in `routes.go`:
  - `GET /api/auth/status`
  - `POST /api/auth/logout`
  - `GET /api/auth/account`
  - `POST /api/auth/password`
  - `POST /api/auth/2fa/setup`
  - `POST /api/auth/2fa/enable`
  - `POST /api/auth/2fa/disable`
  - `GET /api/migration/status`, which the lock page polls for everyone
- Rules:
  - No User in context: pass through. auth already refused or allowed the request as public.
  - Admin: pass.
  - Pattern found with `LevelSignedIn`: pass.
  - Anything else: `403 {"error":"You don't have permission to do this"}`.
  - Requests the mux would not route (empty pattern): pass, so 404 and 405 stay as they are.
- Chain in `NewHandler`: `authService.Middleware(authz.Middleware(mux, inner))`

- [ ] **Step 1: Write the failing tests**

In `authz_test.go`, use a mux with `GET /api/auth/account`, `GET /api/projects` and
`GET /api/projects/{id}`.

`TestNonAdminReachesOnlySignedInRoutes`: table over
`{admin, non-admin} × {GET /api/auth/account, GET /api/projects, GET /api/projects/3}`. Expected:

| | `/api/auth/account` | `/api/projects` | `/api/projects/3` |
|---|---|---|---|
| admin | 200 | 200 | 200 |
| non-admin | 200 | 403 | 403 |

`TestUnroutedRequestPassesThrough`: `GET /api/nope` as a non-admin is 404, not 403.

In `internal/httpapi/authz_test.go`:
- `TestSignedInRoutesAreRegistered`: every key of `authz.Routes` is a pattern `NewHandler` registers.
  Compare against the pattern `mux.Handler` returns for a synthetic request per key, so a renamed route
  fails the test.
- `TestDemotionTakesEffectOnNextRequest`: user 2 is an admin and signed in, and `GET /api/projects` is
  200. Then `UPDATE users SET is_admin=0 WHERE id=2`, and the next `GET /api/projects` is 403.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/authz/ ./internal/httpapi/ -run 'SignedIn|Unrouted|Demotion' -v`
Expected: FAIL, because package `authz` doesn't exist.

- [ ] **Step 3: Implement `Middleware` and `routes.go`; wire it in `NewHandler`**

`_, pattern := mux.Handler(r)` gives the key. Package doc: default deny, and Phase 2 will add scoped
levels.

- [ ] **Step 4: Run**

Run: `go test ./internal/authz/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz internal/httpapi
git commit -m "feat: let non-admin users reach only their own account routes"
```

---

### Task 4: Users API (admin)

**Files:**
- Create: `internal/auth/users.go`, `internal/auth/tokens.go`
- Modify: `internal/auth/auth.go` (`RegisterRoutes`)
- Test: `internal/auth/users_test.go`, `internal/httpapi/authz_test.go`

**Interfaces:**
- Consumes: `UserFrom`, `OwnerID`. These routes are not in `authz.Routes`, so they are admin-only by
  default.
- Produces:
  - JSON `UserView`:
    ```
    { "id", "email", "isAdmin", "isOwner",
      "status": "invited"|"active"|"disabled",
      "twoFactorEnabled",
      "lastLoginAt": RFC3339|null,
      "createdAt": RFC3339 }
    ```
    `status` is `disabled` when `disabled_at` is set, otherwise `invited` when `password_hash` is NULL,
    otherwise `active`.
  - `GET /api/users` → `200 [UserView]`, ordered by id.
  - `GET /api/users/{id}` → `200 UserView` | 404.
  - `POST /api/users {email, isAdmin}` → `201 {"user": UserView, "token", "expiresAt"}`. Duplicate email
    in any case → `409 {"error":"A user with this email already exists"}`. Invalid email → 400 with
    `validateEmail`'s message.
  - `PATCH /api/users/{id} {isAdmin?: bool, disabled?: bool}` → `200 UserView`.
    - Target is id 1 with `isAdmin:false` or `disabled:true` →
      `403 {"error":"The owner cannot be disabled or demoted"}`.
    - Disabling deletes the target's sessions in the same transaction.
    - Re-enabling clears `disabled_at`.
  - `DELETE /api/users/{id}` → 204. Target id 1 → `403 {"error":"The owner cannot be deleted"}`.
  - `POST /api/users/{id}/reset` → `200 {"token","expiresAt","kind"}`.
    - An invited User gets a new `invite` token (7 days).
    - An active User gets a `reset` token (24 hours), and their sessions are deleted now.
    - A disabled User → `409 {"error":"Enable this user before sending a link"}`.
    - Target id 1 and actor not id 1 → `403 {"error":"Only the owner can reset the owner"}`.
  - `tokens.go`:
    - `func issueToken(ctx context.Context, tx *sql.Tx, userID int64, kind string, createdBy int64, now time.Time) (token string, expiresAt time.Time, err error)`
      deletes the user's unused token first.
    - `func hashToken(token string) string`

- [ ] **Step 1: Write the failing tests in `users_test.go`**

Use a helper `serveAs(t, svc, user User, method, path, body) *httptest.ResponseRecorder` that calls
the mux with `WithUser`.

- `TestCreateUserReturnsInviteOnce`:
  - POST `{email:" Bob@Example.com ",isAdmin:false}` → 201.
  - `user.email == "bob@example.com"` and `status == "invited"`.
  - The token is 43 characters.
  - The `user_tokens` row has `kind='invite'`, `token_hash == hashToken(token)` and
    `expires_at == now+7d`.
  - No column holds the raw token.
- `TestCreateUserRejectsDuplicateEmailAnyCase`: `BOB@example.com` after `bob@example.com` → 409, and
  `COUNT(*) FROM users` is 2.
- `TestListAndGetUsers`: statuses `active` (id 1), `invited`, `disabled`, and `isOwner` true only for
  id 1.
- `TestOwnerCannotBeDisabledDemotedOrDeleted`: PATCH `{disabled:true}`, PATCH `{isAdmin:false}` and
  DELETE on id 1 as admin user 2 → each 403 with the exact messages. The row is unchanged.
- `TestDisableEndsSessions`: user 3 has 2 sessions. PATCH `{disabled:true}` → 200, and
  `COUNT(*) FROM sessions WHERE user_id=3` is 0.
- `TestResetActiveUserEndsSessionsAndIssuesResetToken`: `kind == "reset"`,
  `expiresAt == now+24h`, sessions 0, and exactly one unused token row.
- `TestResetInvitedUserReissuesInvite`: the old token's hash is gone, and the new token has
  `kind == "invite"`.
- `TestOwnerCannotBeResetByAnotherAdmin`: user 2 (admin) → 403. As id 1 → 200.
- `TestDeleteUserCascades`: tokens and sessions are gone.
- `TestUsersRoutesAreAdminOnly`, in `internal/httpapi/authz_test.go` through the full `NewHandler`: a
  signed-in non-admin gets 403 on all six routes. The handlers do not check admin themselves;
  `authz` does.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/auth/ -run 'User|Owner|Reset|Disable' -v`
Expected: FAIL, 404 from the mux.

- [ ] **Step 3: Implement `users.go` and `tokens.go`; register the six routes**

- [ ] **Step 4: Run**

Run: `go test ./internal/auth/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth
git commit -m "feat: let admins invite, disable, reset and delete users"
```

---

### Task 5: Invite and reset links (public)

**Files:**
- Create: `internal/auth/invites.go`
- Modify: `internal/auth/auth.go`:
  - `publicPath` allows `GET` and `POST` under `/api/invites/`
  - extract `startSession(ctx, tx, userID) (token string, expiresAt time.Time, err error)` from
    `completeLogin`, and use it from both
- Test: `internal/auth/invites_test.go`

**Interfaces:**
- Consumes: `hashToken`, `user_tokens`.
- Produces:
  - `GET /api/invites/{token}` → `200 {"email","kind"}` | 404 `This link is no longer valid`
  - `POST /api/invites/{token} {password}`:
    - Weak password → 400 with `validatePassword`'s message; the token stays usable.
    - Otherwise, in one transaction: set `password_hash`, `password_from_env = 0`,
      `failed_attempts = 0`, `locked_until = NULL`, and set `used_at`.
    - For `kind = 'reset'`, also clear the three `totp_*` columns and delete every session of the User.
    - Then `startSession` and the session cookie, answering `200 {"authenticated":true,"email"}`.
  - Valid means: the hash exists, `used_at IS NULL`, `expires_at > now`, and the User has
    `disabled_at IS NULL`.

- [ ] **Step 1: Write the failing tests**

- `TestInviteShowsEmailAndKind`
- `TestAcceptInviteSetsPasswordAndSignsIn`: there is a cookie, a login with the new password succeeds
  afterwards, and status becomes `active`.
- `TestInviteCannotBeReused`: a second POST → 404, and a second GET → 404.
- `TestInviteExpires`: advance the clock 7d+1s → 404.
- `TestInviteRefusedForDisabledUser`: GET and POST → 404, and the sessions table is unchanged.
- `TestResetTurnsTwoFactorOffAndEndsOtherSessions`: the user had a `totp_secret` and 2 sessions. After
  POST, `totp_secret IS NULL`, and exactly 1 session remains, the new one.
- `TestWeakPasswordKeepsTokenUsable`: POST `"short"` → 400, then POST a valid password → 200.
- `TestInviteRoutesArePublic`: through `Middleware` without a cookie, GET of an unknown token → 404,
  not 401.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/auth/ -run Invite -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run**

Run: `go test ./internal/auth/ ./internal/httpapi/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth
git commit -m "feat: set a password from an invite or reset link"
```

---

### Task 6: Web data layer

**Files:**
- Modify: `web/src/data/api.ts`, `web/src/data/types.ts`, `web/src/data/store.tsx` (keep `auth.user`),
  `web/src/app/paths.ts`
- Test: `web/src/data/api.test.ts`, `web/src/app/paths.test.ts`

**Interfaces:**
- Produces:
  - `type SignedInUser = { id: number; email: string; isAdmin: boolean; isOwner: boolean }`
  - `AuthStatus` gains `user?: SignedInUser`, and `AuthState` gains `user?: SignedInUser`
  - `type UserStatus = 'invited' | 'active' | 'disabled'`
  - `type PanelUser = SignedInUser & { status: UserStatus; twoFactorEnabled: boolean; lastLoginAt: string | null; createdAt: string }`
  - `type IssuedLink = { token: string; expiresAt: string; kind: 'invite' | 'reset' }`
  - `api` methods:
    - `listUsers(): Promise<PanelUser[]>`
    - `createUser(email: string, isAdmin: boolean): Promise<{ user: PanelUser } & IssuedLink>`
    - `updateUser(id: number, patch: { isAdmin?: boolean; disabled?: boolean }): Promise<PanelUser>`
    - `deleteUser(id: number): Promise<void>`
    - `resetUser(id: number): Promise<IssuedLink>`
    - `getInvite(token: string): Promise<{ email: string; kind: 'invite' | 'reset' }>`
    - `acceptInvite(token: string, password: string): Promise<void>`
  - `paths.users = '/users'`, `paths.invite = (token: string) => \`/invite/${encodeURIComponent(token)}\``
  - `inviteUrl(token: string): string` = `window.location.origin` + Vite base (no trailing slash) +
    `paths.invite(token)`

- [ ] **Step 1: Write the failing tests**

Follow the existing `api.test.ts` stub style. Test the URLs, methods and bodies of each new method, and
that `inviteUrl('a b')` ends with `/invite/a%20b`.

- [ ] **Step 2: Run them and see them fail**

Run: `cd web && npx vitest run src/data/api.test.ts src/app/paths.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run**

Run: `cd web && npx vitest run src/data src/app && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/data web/src/app/paths.ts web/src/app/paths.test.ts
git commit -m "feat: add the users and invite calls to the web client"
```

---

### Task 7: Invite page

**Files:**
- Create: `web/src/pages/InvitePage.tsx`
- Modify: `web/src/app/routes.tsx` (add `{ path: 'invite/:token', element: <InvitePage /> }` beside `signin`)
- Test: `web/src/pages/InvitePage.test.tsx`

**Interfaces:**
- Consumes: `api.getInvite`, `api.acceptInvite`, `usePanel().loadAuthStatus`.
- Behaviour, in the same card layout as `SignInPage` (`CenteredScreen`):
  - Title "Set your password" for `invite`, "Reset your password" for `reset`.
  - Read-only email, then New password and Confirm password.
  - A mismatch shows "Passwords do not match" under Confirm without calling the API.
  - A 400 shows the server message under New password.
  - Success calls `loadAuthStatus()`, then navigates to `paths.overview`.
  - A 404 from either call renders `Result status="warning"` with title "This link is no longer valid",
    subtitle "Ask an admin for a new link." and a "Go to sign in" button.

- [ ] **Step 1: Write the failing tests**

- shows the invite email and title
- shows reset title
- shows mismatch without an API call
- shows the 400 message
- signs in and navigates on success
- shows the invalid link result on 404

- [ ] **Step 2: Run them and see them fail**

Run: `cd web && npx vitest run src/pages/InvitePage.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run**

Run: `cd web && npx vitest run src/pages/InvitePage.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/InvitePage.tsx web/src/pages/InvitePage.test.tsx web/src/app/routes.tsx
git commit -m "feat: add the invite page that sets a password"
```

---

### Task 8: Users page

**Files:**
- Create: `web/src/pages/UsersPage.tsx`
- Modify: `web/src/app/routes.tsx` (`'users': <UsersPage />`), `web/src/app/paths.ts`
  (`routePaths` entry), `web/src/app/AppShell.tsx` (menu item `users` with `TeamOutlined`, placed
  after `traefik`, and `selected` mapping)
- Test: `web/src/pages/UsersPage.test.tsx`

**Interfaces:**
- Consumes: Task 6 api methods, `inviteUrl`, `usePanel().auth.user`.
- Behaviour:
  - A Table with columns Email, Status (Tag: invited = default, active = success, disabled = error),
    Role ("Owner" tag for `isOwner`, else "Admin" or "Member"), Two-factor (On/Off), Last sign-in
    (relative, or "Never"), and Actions.
  - The **Invite user** button opens a Modal with an Email input and an "Admin" Switch.
    - On success the Modal body becomes the link in `Typography.Paragraph copyable`, with the line
      "This link works once and expires on <date>. It is shown only now."
    - A 409 shows under Email.
  - Actions per row, hidden on the Owner row:
    - "Make admin" / "Remove admin"
    - "Disable" / "Enable"
    - "Send reset link" (labelled "New invite link" for invited users), which opens the same link
      Modal
    - "Delete" behind a Popconfirm
  - The Owner row also hides "Send reset link" unless the viewer is the Owner.
  - After any action, reload the list. If the viewer removed their own admin, call `loadAuthStatus()`.

- [ ] **Step 1: Write the failing tests**

- lists users with status tags and the Owner tag
- invites and shows the copyable link once
- shows 409 under Email
- the Owner row has no disable, delete or admin actions
- disables a user and reloads
- shows the reset link for an active user

- [ ] **Step 2: Run them and see them fail**

Run: `cd web && npx vitest run src/pages/UsersPage.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run**

Run: `cd web && npx vitest run src/pages/UsersPage.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/UsersPage.tsx web/src/pages/UsersPage.test.tsx web/src/app
git commit -m "feat: add the users page"
```

---

### Task 9: Shell for a non-admin

**Files:**
- Modify: `web/src/app/AppShell.tsx`, `web/src/app/routes.tsx`
- Create: `web/src/pages/NoAccessPage.tsx`
- Test: `web/src/app/AppShell.test.tsx`

**Interfaces:**
- Consumes: `auth.user.isAdmin`.
- Behaviour when `auth.user && !auth.user.isAdmin`:
  - The sider menu is empty, apart from the existing account dropdown (Account, Sign out).
  - The shell skips the projects load, the deployment queue badge, vulnerability summary, disk pressure
    and panel-update hooks, so no request answers 403.
  - Every route except `account` renders `NoAccessPage`: `Result status="info"` with title
    "No access yet" and subtitle "An admin has not given you access to any Project."
- An admin sees everything as today, plus Users.

- [ ] **Step 1: Write the failing tests in `AppShell.test.tsx`**

- non-admin sees no menu items and the no-access page
- non-admin triggers no projects request
- admin sees the Users menu item

- [ ] **Step 2: Run them and see them fail**

Run: `cd web && npx vitest run src/app/AppShell.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

Guard the hooks with an `enabled` argument where they already take one, or skip the effect. Don't call
hooks conditionally.

- [ ] **Step 4: Run the whole web suite**

Run: `cd web && npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat: show a non-admin user only their account until they are given access"
```

---

### Task 10: Docs, ADR and embedded frontend

**Files:**
- Create: `docs/adr/0031-users-and-roles.md`
- Modify:
  - `docs/adr/0001-v1-scope-and-stack.md`: annotate the multi-user non-goal with "(Overturned by
    ADR-0031.)", in the file's existing "Widened since" style
  - `CONTEXT.md`: add User, Owner, Admin and Invite link from the spec's Domain changes; change
    Two-factor to "per User"
  - `.scratch/multi-user/spec.md`: append to `## Comments` the Phase 1 deviations listed below
  - `internal/webassets/` generated output
- Test: none (docs), then the full suite.

**Content of ADR-0031:** the whole multi-user decision from the spec's Decision section, plus its
Considered options:
- roles in handlers vs. the central table
- the `is_owner` flag vs. the fixed id 1
- custom permissions

Write it in the style of ADR-0027: a Decision paragraph, Considered options, Consequences, and the spec
link. Phase 2 adds no new ADR; it only updates ADR-0016, 0020 and 0022.

**Spec deviations to record in Comments:**
- Recovery for id 1 is the existing `reset-password` and `change-email`; `create-admin` stays
  create-only.
- An admin cannot reset the Owner; only the Owner can.
- The invite routes have no rate limit, because a 256-bit token cannot be guessed.

- [ ] **Step 1: Write the docs**

- [ ] **Step 2: Rebuild the embedded frontend and run everything**

Run: `task build-web && go test ./... && (cd web && npm test && npm run typecheck)`
Expected: PASS.

- [ ] **Step 3: Commit, in two commits as the repo does**

```bash
git add docs CONTEXT.md .scratch/multi-user/spec.md
git commit -m "docs: record users and roles in ADR-0031 and the domain"
git add internal/webassets
git commit -m "chore: rebuild the embedded frontend"
```

- [ ] **Step 4: Live check (user)**

After rebuilding the live panel, check:
1. The existing admin is still signed in.
2. Invite a second email, then open the link in a private window and set a password.
3. The second user sees only Account and "No access yet".
4. Disable them, and their window is signed out on the next click.
5. Run `GET /api/projects` as them: 403.
