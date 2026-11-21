# Session Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sign-in sessions end after 24 idle hours or 7 days, whichever comes first. They are
stored as SHA-256 hashes, and dead rows are pruned.

**Architecture:**
- All logic stays in `internal/auth`.
- One new migration renames `sessions.token` to `token_hash`, adds `last_seen_at`, and deletes
  every existing row.
- One lookup, `liveSession`, replaces the two places that read `sessions` today:
  `authenticated` in `auth.go` and `currentSession` in `twofactor.go`. It also does the throttled
  touch.
- The new `RunSessionCleanup` loop is started from `httpapi.go` next to `auditLog.RunRetention`.

**Tech Stack:** Go 1.25, SQLite (modernc, `SetMaxOpenConns(1)`), goose v3 migrations.

**Spec:** `.scratch/session-hardening/spec.md`. Read it before starting any task.

## Global Constraints

- Constants in `internal/auth/auth.go` replace `sessionLifetime`:
  - `sessionMaxLifetime = 7 * 24 * time.Hour`
  - `sessionIdleTimeout = 24 * time.Hour`
  - `sessionTouchInterval = 10 * time.Minute`
  - `sessionCleanupInterval = time.Hour`
- Liveness: `expires_at > now AND last_seen_at > now - 86400` (Unix seconds, both strict).
- Touch when `now - last_seen_at >= 600` seconds. Never touch otherwise.
- Hash = `hashToken` from `internal/auth/tokens.go` (hex SHA-256). The cookie value is never stored.
- **Migration and ADR numbers:**
  - The Databases stack merged first (main `28f7fce` holds migrations up to `00055` and ADRs up
    to `0050`).
  - This branch uses migration `00056_session_hardening.sql` and ADR `0051`.
  - Never edit a committed migration.
- Branch from `origin/main` (`claude/session-hardening`).
- Conventional Commits, no scope.

## Review Focus

1. **A busy panel at the 7-day cap.** A user active every few hours is still signed out at
   `created_at + 7d`. Test: `TestSessionEndsAtMaxLifetimeDespiteActivity` (Task 2).
2. **A touch that fails** (database busy or locked). The request is still served and the
   failure is only logged. Test: `TestFailedTouchStillServesRequest` (Task 2).
3. **The clock steps backwards** (`last_seen_at` is in the future). The session stays live and
   nothing is written. Test: `TestFutureLastSeenIsNotTouched` (Task 2).
4. **Two-factor enable or disable, and password change, keep the current session.** The kept
   value is now a hash, so a raw-token comparison would revoke the caller too. The existing
   `internal/httpapi/auth_twofactor_test.go` "session rows = 2" check covers it once its seed is
   updated (Task 1).
5. **First load after the upgrade.** Every session is gone, so the open tab must land on sign-in,
   not on a broken page. This is the manual live check in Task 4.

---

### Task 1: Store session tokens as hashes

**Files:**
- Create: `internal/migrations/00056_session_hardening.sql`
- Create: `internal/auth/session.go`, which holds the session code moved out of `auth.go` along
  with the new helpers
- Modify: `internal/auth/auth.go`: `startSession`, `logout`, `authenticated`, `setSessionCookie`
- Modify: `internal/auth/twofactor.go`: `currentSession`, `revokeOtherSessions`
- Modify: `internal/auth/password.go`, which keeps the current session by hash
- Test: `internal/auth/session_test.go` (new) and `internal/database/database_test.go`
- Fix seeds that insert raw tokens:
  - `internal/auth/user_context_test.go:83` (`addSession`)
  - `internal/auth/reset_test.go:42` (`seedResetSession`)
  - `internal/auth/invites_test.go:125`
  - `internal/httpapi/auth_twofactor_test.go:394`
  - `internal/httpapi/authz_test.go:23`
  - `cli_test.go:38` (uses the old `token` column; seeds two rows for the revoke counts)
  - `internal/database/database_test.go:627`

**Interfaces:**
- Produces:
  - `func HashSessionToken(token string) string`. It is exported for `internal/httpapi` tests and
    returns `hashToken(token)`.
  - `sessions` columns `token_hash TEXT PRIMARY KEY, user_id, expires_at, created_at,
    last_seen_at INTEGER NOT NULL`.
  - `startSession` writes `last_seen_at = created_at`.
  - `currentSession` returns `(userID int64, keepHash string, err error)`, and
    `revokeOtherSessions(ctx, tx, userID, keepHash)` compares `token_hash <> ?`.

- [ ] **Step 1: Write the failing tests**

  In `internal/auth/session_test.go`, using `newUserFixture`:
  - `TestSessionTokenStoredAsHash`: log in, read the `relist_session` cookie value `v`.
    - `SELECT COUNT(*) FROM sessions WHERE token_hash = ?` with `HashSessionToken(v)` is 1.
    - The same count with `v` itself is 0.
  - `TestRawTokenRowDoesNotAuthenticate`: insert a row whose `token_hash` is the literal
    `"raw-token"`. `GET /api/whoami` with cookie `raw-token` → 401.
  - `TestLogoutDeletesHashedRow`: log in, then `POST /api/auth/logout` with the cookie. The
    `sessions` count goes back to 0.

  In `internal/database/database_test.go`:
  - `TestSessionHardeningMigrationEndsSessions`:
    1. `goose.UpTo(db, ".", 55)`, then insert a user and one session through the old `token` column.
    2. Run `Migrate(db)`. Then:
       - `SELECT COUNT(*) FROM sessions` is 0.
       - `PRAGMA table_info(sessions)` lists `token_hash` and `last_seen_at`, and no `token`.

- [ ] **Step 2: Run them and see them fail**

  Run: `go test ./internal/auth/ ./internal/database/ -run 'Session|Hardening' -v`
  Expected: FAIL. `HashSessionToken` is undefined and the `token_hash` column does not exist.

- [ ] **Step 3: Write the migration**

  - `-- +goose Up`:
    1. `DELETE FROM sessions;`
    2. `ALTER TABLE sessions RENAME COLUMN token TO token_hash;`
    3. `ALTER TABLE sessions ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;`
  - `-- +goose Down`: the same three statements reversed:
    1. delete the rows
    2. `DROP COLUMN last_seen_at`
    3. rename `token_hash` back to `token`
  - Start the file with a header comment naming the spec and ADR-0051, as `00040` does.

- [ ] **Step 4: Switch every `sessions` read and write to the hash**

  - Every place in `auth.go`, `twofactor.go` and `password.go` that puts the cookie value into
    SQL now passes `hashToken(cookie.Value)`.
  - `startSession` still returns the raw token for the cookie.
  - Move `startSession`, `newSessionToken`, `setSessionCookie`, `clearSessionCookie`,
    `clearSessionCookieIfPresent`, `currentSession` and `revokeOtherSessions` into `session.go`.
  - Update the seven seeds listed under Files so they store `auth.HashSessionToken(token)` (or
    `HashSessionToken` inside package `auth`).
  - Seeds set `last_seen_at` to the test's current time (`f.now` or `time.Now()`), not to
    `created_at`. `seedResetSession` uses `created_at = 1`, so copying `created_at` would make
    its row idle-dead once Task 2 lands.
  - In `database_test.go:627`, the old assertion reads the row back by `token`. Replace it with
    `SELECT COUNT(*) FROM sessions` = 0, since the new migration ends every session.

- [ ] **Step 5: Run the whole suite**

  Run: `go test ./...`
  Expected: PASS, including `auth_twofactor_test.go` "session rows = 2".

- [ ] **Step 6: Commit**

  `fix: store session tokens as SHA-256 hashes`

---

### Task 2: Idle timeout, 7-day cap, throttled touch

**Files:**
- Modify: `internal/auth/session.go` and `internal/auth/auth.go` (constants, `authenticated`)
- Modify: `internal/auth/twofactor.go` (`currentSession` goes through `liveSession`)
- Test: `internal/auth/session_test.go`

**Interfaces:**
- Consumes: Task 1's `HashSessionToken` and the `token_hash`/`last_seen_at` columns.
- Produces:
  - `func (s *Service) liveSession(ctx context.Context, token string) (liveSessionRow, bool, error)`
  - `type liveSessionRow struct { hash string; user User; lastSeen time.Time }`
  - The lookup joins `users` and keeps the `disabled_at IS NULL` and Owner-is-admin rules of
    today's `authenticated`.
  - It touches the row per Global Constraints. `authenticated` and `currentSession` both call it.
  - **It must never run while the caller holds an open `*sql.Tx`.** The panel has one database
    connection (`SetMaxOpenConns(1)`), so a touch through `s.db` inside a transaction waits
    forever.
    - On `origin/main`, every `currentSession` call in `password.go` and `twofactor.go` comes
      before its `BeginTx`.
    - `Middleware` runs before any handler.
    - The terminal's re-check (`authz.StillAllowed` → `CurrentUser`) holds no transaction.
    - Keep that order.

- [ ] **Step 1: Write the failing tests**

  All tests use the fixture clock `f.now`. `t0` is the sign-in time.
  - `TestSessionEndsAfterIdleTimeout`: sign in at `t0`. At `t0+24h`, `GET /api/whoami` → 401.
  - `TestActivityExtendsIdleWindow`: sign in at `t0`.
    - Requests at `t0+23h` and `t0+46h` → 200.
    - A request at `t0+46h+24h` → 401.
  - `TestSessionEndsAtMaxLifetimeDespiteActivity`: sign in at `t0`.
    - A request every 12h up to `t0+7d-1s` → all 200.
    - At `t0+7d` → 401.
  - `TestTouchIsThrottled`: sign in at `t0`.
    - After a request at `t0+9m59s`, `last_seen_at` = `t0`.
    - After a request at `t0+10m`, `last_seen_at` = `t0+10m`.
  - `TestFutureLastSeenIsNotTouched`: set the row's `last_seen_at` to `t0+1h`, then make a
    request at `t0` → 200, and `last_seen_at` is still `t0+1h`.
  - `TestFailedTouchStillServesRequest`:
    1. Add `CREATE TRIGGER block_touch BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'no'); END;`.
    2. Make a request at `t0+11m` → 200.
  - `TestTwoFactorRoutesHonourIdleTimeout`: sign in at `t0`. At `t0+24h`,
    `GET /api/auth/account` → 401.
  - `TestSessionCookieLifetime`: the login response's cookie has `MaxAge == 604800` and
    `Expires` equal to the row's `expires_at`.

- [ ] **Step 2: Run them and see them fail**

  Run: `go test ./internal/auth/ -run 'Session|Touch|Idle|Lifetime' -v`
  Expected: FAIL. With today's 30-day `expires_at`, the idle and 7-day tests return 200.

- [ ] **Step 3: Implement `liveSession` and switch the constants**

  - Pass `now` and `now - 86400` to the SQL, as Global Constraints says.
  - A failed touch is reported with `log.Printf("auth: touch session: %v", err)`, and the
    session is still returned as live.
  - Delete `sessionLifetime`. `startSession` sets `expires_at = now + sessionMaxLifetime`, and
    the cookie's `MaxAge` is `int(sessionMaxLifetime / time.Second)`.

- [ ] **Step 4: Run the whole suite**

  Run: `go test ./...`
  Expected: PASS.

- [ ] **Step 5: Commit**

  `feat: end sessions after 24 idle hours or 7 days`

---

### Task 3: Prune dead sessions

**Files:**
- Modify: `internal/auth/session.go`
- Modify: `internal/httpapi/httpapi.go`. Start the loop next to `auditLog.RunRetention`, at about
  line 326, with the same `worker.Add(1)` / `defer worker.Done()` shape.
- Test: `internal/auth/session_test.go`

**Interfaces:**
- Produces:
  - `func (s *Service) PruneSessions(ctx context.Context) (int64, error)`. It runs
    `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?` with `now` and
    `now - 86400`, and returns the number of rows deleted.
  - `func (s *Service) RunSessionCleanup(ctx context.Context)`. It prunes at once, then every
    `sessionCleanupInterval`, until `ctx` is done.
  - It logs `auth: prune sessions: %v` on error and `auth: pruned %d dead sessions` when n > 0.

- [ ] **Step 1: Write the failing tests**
  - `TestPruneSessionsDeletesOnlyDeadRows`: seed four rows at fixture time `now`:
    - live
    - idle-dead (`last_seen_at = now-24h`)
    - capped-dead (`expires_at = now`)
    - live at the edge (`last_seen_at = now-24h+1s`, `expires_at = now+1s`)

    `PruneSessions` returns 2, and the two live rows remain.
  - `TestRunSessionCleanupPrunesOnStart`: seed one idle-dead row and call `RunSessionCleanup`
    with an already-cancelled context. It returns, and the row is gone.

- [ ] **Step 2: Run them and see them fail**

  Run: `go test ./internal/auth/ -run 'Prune|Cleanup' -v`
  Expected: FAIL (undefined).

- [ ] **Step 3: Implement both functions and start the loop in `httpapi.go`**

- [ ] **Step 4: Run the whole suite**

  Run: `go test ./...`
  Expected: PASS.

- [ ] **Step 5: Commit**

  `feat: prune dead sessions every hour`

---

### Task 4: ADR and live check

**Files:**
- Create: `docs/adr/0051-session-idle-timeout-and-hashed-tokens.md`, in the format of the
  existing ADRs
- Modify: `e2e/`. Add the passing checks from Step 2 as a Playwright spec.

- [ ] **Step 1: Write the ADR**

  - **Context:** the panel is root on the host, sessions lasted 30 days, and raw tokens were in
    `panel.db`.
  - **Decision:** the values table from the spec.
  - **Consequences:**
    - polling tabs stay signed in up to the 7-day cap
    - a session can end up to 10 min early
    - every user signs in once after the upgrade
    - the server enforces the idle rule, not the cookie

- [ ] **Step 2: Manual live check**

  Follow "Running a manual check" in `docs/agents/live-checks.md`. Use `task e2e:serve`, and read
  ports and credentials from `.data/e2e/env`. Check that:
  1. Signing in sets `relist_session` with `Max-Age=604800`.
  2. In the sandbox's `panel.db`, `sessions.token_hash` is 64 hex characters and is not the
     cookie value.
  3. After `DELETE FROM sessions` (the same state as an upgrade), reloading the open tab lands on
     the sign-in page with no error screen.

- [ ] **Step 3: Add checks 1 and 3 to `e2e/` and run `task e2e`**

  Expected: PASS.

- [ ] **Step 4: Commit**

  `docs: record the session timeout and token hashing decision`
