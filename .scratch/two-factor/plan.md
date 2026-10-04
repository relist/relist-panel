# Two-factor Sign-in Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in TOTP second factor to the single admin account. The plan covers a per-account
login lock, a host-side `reset-2fa` recovery command, a sign-in code step and an Account page.

**Architecture:**
- All server logic stays in `internal/auth`, split into focused files:
  - `lock.go`: failure counting
  - `twofactor.go`: TOTP enrolment and verification
  - `reset.go`: recovery
- `main.go` gains one subcommand dispatch, which lives in `cli.go`.
- The frontend gets:
  - a discriminated `api.login` result
  - a code step on the existing sign-in page
  - a new `/account` route inside the shell

**Tech Stack:** Go 1.25, SQLite (modernc), goose migrations, `github.com/pquerna/otp` v1.5.0,
React + antd 6.5.1 (`QRCode`, `Input.OTP`, `Steps`, `Modal`), vitest.

**Spec:** `.scratch/two-factor/spec.md`. Read it before starting any task.

## Global Constraints

- TOTP: SHA-1, 30-second period, 6 digits, ±1 step skew, issuer `Relist Panel`, account name =
  the admin's email.
- Lock: 5 consecutive failures, of password or code, set `locked_until = now + 60s` and reset the
  counter to 0. While locked, every credential check returns `429` with `Retry-After: <seconds>`,
  rounded up and at least 1.
- A code whose step is at or before `totp_last_step` is refused.
- Enabling or disabling deletes every session of the account except the current one.
  `reset-2fa` deletes all of them.
- The session lifetime stays 30 days. No new public routes: every new `/api/auth/*` route stays
  behind `Middleware`, so `publicPath` is not changed.
- Code identifiers use `twoFactor` / `totp`, never `mfa`/`otp`/`2fa`, except in URL paths, which are
  `/api/auth/2fa/...`, and in the command name `reset-2fa` (CONTEXT.md).
- Error copy:
  - `invalid email or password` (unchanged)
  - `invalid code`
  - `two-factor code required`
  - `too many attempts, try again in N seconds`
- Commits follow Conventional Commits with no scope and end with the
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` trailer.
- Before any `web/` change, read `https://ant.design/llms.txt` and the `qr-code`, `input` (OTP),
  `steps`, `modal` and `form` pages, and follow `docs/GUIDELINES.md`.
- Go checks: `go test ./...`, `go vet ./...` and `gofmt -l .` (which must print nothing).
- Web checks: `pnpm --dir web typecheck`, `pnpm --dir web test` and `pnpm --dir web build`.
  Run `pnpm --dir web install` once per worktree before the first web task.

## Review Focus

1. **Two concurrent logins with the same valid code.** Only one may get a session. The verify and
   the `totp_last_step` write share one transaction (Task 2 test
   `TestConcurrentLoginsWithOneCodeYieldOneSession`).
2. **`reset-2fa` while the panel is running.** It must wait for the busy database instead of
   failing with `SQLITE_BUSY` (Task 3 test `TestOpenAndMigrateWaitsForABusyWriter`).
3. **A pasted code with spaces, like `123 456`.** Authenticator apps display codes this way. The
   server strips ASCII spaces before verifying, and any remaining character that is not a digit is
   just an invalid code, never a `400` that skips failure counting (Task 2 test
   `TestLoginAcceptsACodeWithSpaces`).
4. **Pressing Back on the code step, then signing in as a different email.** No password or code
   from the first attempt may leak into the second (Task 4 test `resets held credentials on back`).
5. **The Account page after a `reset-2fa`.** The admin's session is gone, so `/api/auth/account`
   returns `401` and the existing gate sends them to sign-in. The page must not show a stale `On`
   (Task 5, manual check in Step 6).

---

### Task 1: Per-account login lock

**Files:**
- Create: `internal/migrations/00012_two_factor.sql`
- Create: `internal/auth/lock.go`
- Modify: `internal/auth/auth.go`: the `login` handler and the `admin` struct
- Test: `internal/httpapi/auth_twofactor_test.go` (new) and `internal/auth/lock_test.go` (new,
  `package auth`)

**Interfaces:**
- Produces:
  - `const maxFailedAttempts = 5` and `const lockDuration = time.Minute`
  - `func (s *Service) lockedFor(ctx context.Context, tx *sql.Tx, adminID int64) (time.Duration, error)`:
    the remaining lock time, or 0 when unlocked
  - `func (s *Service) recordFailure(ctx context.Context, tx *sql.Tx, adminID int64) error`
  - `func (s *Service) recordSuccess(ctx context.Context, tx *sql.Tx, adminID int64) error`: sets
    `failed_attempts = 0`
  - `func writeLocked(w http.ResponseWriter, remaining time.Duration)`: writes the `429` and the
    `Retry-After` header
  - `func newServiceWithClock(db *sql.DB, now func() time.Time) *Service`: unexported, for tests
    in the package

- [ ] **Step 1: Write the migration.** Add all five columns from the spec's Schema table now, so
  later tasks don't need another migration:
  - `totp_secret TEXT NULL`
  - `totp_pending_secret TEXT NULL`
  - `totp_last_step INTEGER NULL`
  - `failed_attempts INTEGER NOT NULL DEFAULT 0`
  - `locked_until INTEGER NULL`

  The Down migration drops them with `ALTER TABLE ... DROP COLUMN`.

- [ ] **Step 2: Write the failing HTTP tests** in `internal/httpapi/auth_twofactor_test.go`, using
  the existing `newTestServer`, `postJSON` and `setupAndLogin` helpers:
  - `TestFiveFailedLoginsLockTheAccount`: post a wrong password 4 times and assert `401` each
    time. The 5th wrong attempt returns `401`. The 6th attempt, with the **correct** password,
    returns `429`, `Retry-After` parses as an integer from 1 to 60, and the body's `error` starts
    with `too many attempts`.
  - `TestSuccessfulLoginResetsTheFailureCount`: 4 wrong, 1 right, then 4 wrong again. None of
    these returns `429`.
  - `TestUnknownEmailIsNotCounted`: 6 attempts with `nobody@example.com` all return `401`, and the
    real admin still logs in with `200`.

- [ ] **Step 3: Write the failing clock test** `TestLockExpiresAfterOneMinute` in
  `internal/auth/lock_test.go`:
  - Build a `*Service` with `newServiceWithClock` and a settable clock over
    `database.OpenAndMigrate(t.TempDir()+"/db.sqlite")`, then serve it with `httptest`.
  - Lock the account. At +59s the correct password gets `429`. At +60s it gets `200`.

- [ ] **Step 4: Run** `go test ./internal/auth/ ./internal/httpapi/ -run 'Lock|Failed|Failure|Unknown'`
  and confirm all four tests fail.

- [ ] **Step 5: Implement `lock.go` and rewire `login`.**
  - Move the account lookup, the lock check, bcrypt and the session insert into one `BeginTx`.
  - Follow the spec's Login rules, steps 1–3 and 7. Steps 4–6 come in Task 2.
  - `New(db)` calls `newServiceWithClock(db, time.Now)`.

- [ ] **Step 6: Run** `go test ./internal/auth/ ./internal/httpapi/` and confirm it passes,
  including the existing `TestAuthSetupAndLogin`.

- [ ] **Step 7: Commit**
  ```bash
  git add internal/migrations/00012_two_factor.sql internal/auth internal/httpapi/auth_twofactor_test.go
  git commit -m "feat: lock the admin account for a minute after five failed sign-ins"
  ```

---

### Task 2: TOTP enrolment, verification and code-gated login

**Files:**
- Modify: `go.mod` and `go.sum`, via `go get github.com/pquerna/otp@v1.5.0`
- Create: `internal/auth/twofactor.go`
- Modify: `internal/auth/auth.go`: `RegisterRoutes`, `login`, `credentials` (add
  `Code string \`json:"code,omitempty"\``), `admin` (add `totpSecret sql.NullString` and
  `totpLastStep sql.NullInt64`)
- Test: `internal/httpapi/auth_twofactor_test.go` and `internal/auth/twofactor_test.go`

**Interfaces:**
- Consumes: everything Task 1 produced.
- Produces:
  - Routes:
    - `GET /api/auth/account` returns `{"email": string, "twoFactorEnabled": bool}`
    - `POST /api/auth/2fa/setup` takes `{password}` and returns `200 {"secret", "otpauthUri"}`
    - `POST /api/auth/2fa/enable` takes `{code}` and returns `204`
    - `POST /api/auth/2fa/disable` takes `{password, code}` and returns `204`
    - `POST /api/auth/login` returns `401 {"error": "two-factor code required", "codeRequired": true}`
      when a code is needed
  - `func verifyCode(secret, code string, now time.Time, lastStep sql.NullInt64) (step int64, ok bool)`
  - `func (s *Service) currentSession(r *http.Request) (adminID int64, token string, err error)`
  - `func revokeOtherSessions(ctx context.Context, tx *sql.Tx, adminID int64, keepToken string) error`

- [ ] **Step 1: Write the failing unit tests** in `internal/auth/twofactor_test.go`. Generate
  codes with `totp.GenerateCodeCustom(secret, t, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})`.
  - `TestVerifyCodeAcceptsOneStepOfSkew`: codes for `now-30s`, `now` and `now+30s` are each
    accepted with no last step. Codes for `now-60s` and `now+60s` are refused.
  - `TestVerifyCodeRefusesReplayedSteps`: a code for `now` is refused when `lastStep` equals that
    code's step, and also when `lastStep` is one step later.
  - `TestVerifyCodeReturnsTheMatchedStep`: a code for `now-30s` returns `now.Unix()/30 - 1`.

- [ ] **Step 2: Write the failing HTTP tests** in `internal/httpapi/auth_twofactor_test.go`. Add a
  helper `enableTwoFactor(t, server) (secret string)` that calls setup with the correct password,
  then enable with a fresh code.
  - `TestTwoFactorRoundTrip`:
    1. Account reports `twoFactorEnabled: false`.
    2. Enable 2FA, then account reports `true`.
    3. Log out. Login without a code gets `401` with `codeRequired: true`, and that attempt does
       not count toward the lock.
    4. Login with a code from the *next* step gets `200`. Use `time.Now().Add(30*time.Second)` so
       the step differs from the one enable consumed.
  - `TestTwoFactorSetupRequiresThePassword`: a wrong password gets `401`. Setup while 2FA is
    enabled gets `409`.
  - `TestTwoFactorEnableRejectsAWrongCode`: gets `400` with `invalid code`. Enable without a
    prior setup gets `409`.
  - `TestTwoFactorEnableAndDisableRevokeOtherSessions`:
    - Log in twice. Copy the first session's cookie into a second `testServer` cookie map
      sharing the same handler, or send it with `server.serve`.
    - Enable from session B: session A's `GET /api/projects` gets `401` and session B's gets
      `200`.
    - Repeat the check for disable.
  - `TestTwoFactorDisableNeedsPasswordAndCode`: password only gets `401`. A wrong code gets `401`.
    Both correct gets `204`, and login then needs no code.
  - `TestReplayedCodeIsRefusedAtLogin`: log in with code X and get `200`. Log out. The same X
    gets `401` with `invalid code`.
  - `TestWrongCodesCountTowardTheLock`: 3 wrong passwords plus 2 wrong codes, then the correct
    password and code get `429`.
  - `TestLoginAcceptsACodeWithSpaces`: a valid code sent as `"123 456"` gets `200`. A code of
    `"12345a"` gets `401` and counts as a failure.
  - `TestConcurrentLoginsWithOneCodeYieldOneSession`: 8 goroutines post the same valid code.
    Exactly one gets `200` and the rest get `401`. Run it with `-race`.
  - `TestAccountWithoutTwoFactorIgnoresACode`: login with `code: "000000"` while 2FA is off gets
    `200`.

- [ ] **Step 3: Run** `go test ./internal/auth/ ./internal/httpapi/ -run 'TwoFactor|VerifyCode|Replayed|WrongCodes|Spaces|Concurrent|IgnoresACode'`
  and confirm the tests fail to compile or fail.

- [ ] **Step 4: Implement `twofactor.go`.**
  - `verifyCode`:
    - Strip ASCII spaces from the code. Anything left that isn't exactly 6 digits fails.
    - Check steps `s-1`, `s` and `s+1`, where `s = now.Unix()/30`, using
      `totp.GenerateCodeCustom` and `subtle.ConstantTimeCompare`.
    - Return the matched step only if it is greater than `lastStep`.
  - Setup generates the secret with
    `totp.Generate(totp.GenerateOpts{Issuer: "Relist Panel", AccountName: email})`, then returns
    `key.Secret()` and `key.URL()`.
  - Every route that checks a password or code runs in one transaction:
    1. Check `lockedFor`; if locked, call `writeLocked`.
    2. Verify.
    3. Call `recordFailure` or `recordSuccess`.
  - Extend `login` with the spec's steps 4–6. On success with a code, write `totp_last_step` in
    the same transaction as the session insert.

- [ ] **Step 5: Run** `go test -race ./internal/auth/ ./internal/httpapi/` and `go vet ./...`, and
  confirm both pass.

- [ ] **Step 6: Commit**
  ```bash
  git add go.mod go.sum internal/auth internal/httpapi/auth_twofactor_test.go
  git commit -m "feat: let the admin turn on TOTP two-factor and require it at sign-in"
  ```

---

### Task 3: `reset-2fa` recovery command and SQLite busy timeout

**Files:**
- Create: `internal/auth/reset.go` and `internal/auth/reset_test.go`
- Create: `cli.go` and `cli_test.go` (package `main`, at the repo root)
- Modify: `main.go`: dispatch before `config.Load()`
- Modify: `internal/database/database.go` and `internal/database/database_test.go`

**Interfaces:**
- Consumes: the Task 1 columns.
- Produces:
  - `type ResetResult struct { Email string; WasEnabled bool; SessionsRevoked int64 }`
  - `var ErrAccountNotFound` and `var ErrEmailRequired`
  - `func ResetTwoFactor(ctx context.Context, db *sql.DB, email string) (ResetResult, error)`.
    An empty `email` means the only account.
  - `func runResetTwoFactor(args []string, stdout, stderr io.Writer) int`, which returns the exit
    code

- [ ] **Step 1: Write the failing database test** `TestOpenAndMigrateWaitsForABusyWriter`:
  - Open the same file twice with `OpenAndMigrate`.
  - On handle A, `BEGIN IMMEDIATE` and insert a row into `admin_credentials`, then commit after
    300ms in a goroutine.
  - On handle B, an `UPDATE admin_credentials ...` must succeed instead of failing with
    `database is locked`.

- [ ] **Step 2: Write the failing reset tests** in `internal/auth/reset_test.go`. Seed rows with
  SQL directly: an admin with `totp_secret`, `totp_pending_secret`, `totp_last_step`,
  `failed_attempts = 3`, a future `locked_until` and two sessions.
  - `TestResetTwoFactorClearsStateAndSessions`: every TOTP column is `NULL`,
    `failed_attempts = 0`, `locked_until` is `NULL`, there are 0 sessions, and the result is
    `{Email: "admin@example.com", WasEnabled: true, SessionsRevoked: 2}`.
  - `TestResetTwoFactorPicksTheOnlyAccount`: an empty email works.
  - `TestResetTwoFactorUnknownEmail`: `errors.Is(err, ErrAccountNotFound)`.
  - `TestResetTwoFactorWhenNotEnabledStillUnlocks`: `WasEnabled: false` and the lock is cleared.

  `ErrEmailRequired` can't be reached while `admin_credentials` has `CHECK (id = 1)`. Implement
  the branch anyway and leave it untested. The multi-user spec will test it.

- [ ] **Step 3: Write the failing CLI tests** in `cli_test.go`. Use `t.Setenv("RELIST_DATA_DIR", t.TempDir())`
  and seed the database through `database.OpenAndMigrate(config...DatabasePath())`.
  - `TestResetTwoFactorCommandSucceeds`: exits 0 and stdout mentions the email and the revoked
    session count.
  - `TestResetTwoFactorCommandUnknownEmail`: `--email nobody@example.com` exits 1 and stderr names
    the email.
  - `TestResetTwoFactorCommandRejectsStrayArgs`: an argument `extra` exits 2 and stderr shows the
    usage line `usage: relist-panel reset-2fa [--email ADDRESS]`.

- [ ] **Step 4: Run** `go test ./internal/database/ ./internal/auth/ . -run 'Busy|Reset'` and
  confirm the tests fail.

- [ ] **Step 5: Implement.**
  - DSN: `"file:" + databasePath + "?_pragma=busy_timeout(5000)"`.
  - `ResetTwoFactor` does one transaction: an update, then
    `DELETE FROM sessions WHERE admin_id = ?`.
  - `runResetTwoFactor` parses its arguments with a `flag.FlagSet` using `ContinueOnError`, then
    runs `config.Load` → `database.OpenAndMigrate` → `auth.ResetTwoFactor`.
  - In `main.go`, before anything else:
    `if len(os.Args) > 1 && os.Args[1] == "reset-2fa" { os.Exit(runResetTwoFactor(os.Args[2:], os.Stdout, os.Stderr)) }`.

- [ ] **Step 6: Run** `go test ./...`, `go vet ./...` and `gofmt -l .`, and confirm all pass.

- [ ] **Step 7: Commit**
  ```bash
  git add main.go cli.go cli_test.go internal/auth internal/database
  git commit -m "feat: add a reset-2fa command that clears two-factor and the sign-in lock"
  ```

---

### Task 4: Sign-in code step

**Files:**
- Modify: `web/src/data/api.ts` and `web/src/data/api.test.ts`
- Modify: `web/src/data/store.tsx`: `signIn`
- Create: `web/src/pages/signin-view.ts` and `web/src/pages/signin-view.test.ts`
- Modify: `web/src/pages/AuthPages.tsx`: `SignInPage`

**Interfaces:**
- Consumes: the Task 2 login wire format and the Task 1 `429` with `Retry-After`.
- Produces:
  - `export type LoginInput = Credentials & { code?: string }`
  - `export type LoginResult = { kind: 'signedIn' } | { kind: 'codeRequired' } | { kind: 'rateLimited'; retryAfterSeconds: number }`
  - `api.login(input: LoginInput): Promise<LoginResult>`. It still throws `ApiError` for every
    other non-2xx response, such as a wrong password or code.
  - `signIn(email: string, password: string, code?: string): Promise<LoginResult>` in `PanelValue`.
    It sets `authenticated` only on `signedIn`.
  - `signin-view.ts`:
    - `type SignInStep = { step: 'password' } | { step: 'code'; email: string; password: string }`
    - `nextStep(current: SignInStep, submitted: { email: string; password: string }, result: LoginResult): SignInStep`
    - `backToPassword(): SignInStep`
    - `rateLimitMessage(seconds: number): string`, which returns
      `` `Too many attempts. Try again in ${seconds} seconds.` ``

- [ ] **Step 1: Write the failing tests.**
  - In `api.test.ts`, under `describe('login')`, stub `fetch` and check four cases:
    - a `200` response maps to `{kind: 'signedIn'}`
    - `401 {"codeRequired": true}` maps to `{kind: 'codeRequired'}`
    - `429` with `Retry-After: 42` maps to `{kind: 'rateLimited', retryAfterSeconds: 42}`
    - `401 {"error": "invalid code"}` rejects with an `ApiError` whose message is `invalid code`
  - In `signin-view.test.ts`, test that:
    - `codeRequired` moves the password step to the code step and holds the submitted email and
      password
    - `signedIn` and `rateLimited` keep the current step
    - the case named `resets held credentials on back`: `backToPassword()` returns
      `{step: 'password'}` with no credential fields
    - `rateLimitMessage(42)` returns the exact copy

- [ ] **Step 2: Run** `pnpm --dir web test` and confirm the new tests fail.

- [ ] **Step 3: Implement.**
  - Write `api.login` with its own `fetch` instead of `request`, because `401`/`429` are results
    here and not errors.
  - Rewrite `SignInPage` around the `SignInStep` state:
    - The password step keeps the current form.
    - The code step shows `Input.OTP` with `length={6}`, `autoFocus` and
      `autoComplete="one-time-code"`, and submits from `onChange` once 6 characters are entered.
    - On `invalid code`, the error shows under the input and the input is cleared.
    - The code step has a link button **Back**.
    - `rateLimited` shows `rateLimitMessage` in the existing `AuthError` slot at either step.

- [ ] **Step 4: Run** `pnpm --dir web typecheck` and `pnpm --dir web test`, and confirm both pass.

- [ ] **Step 5: Commit**
  ```bash
  git add web/src/data web/src/pages/signin-view.ts web/src/pages/signin-view.test.ts web/src/pages/AuthPages.tsx
  git commit -m "feat: ask for the two-factor code as a second sign-in step"
  ```

---

### Task 5: Account page with turn-on and turn-off flows

**Files:**
- Modify: `web/src/data/api.ts` and `web/src/data/api.test.ts`
- Modify: `web/src/app/paths.ts` (add `account: '/account'` and `{ path: 'account' }` to
  `routePaths`), `web/src/app/routes.tsx` and `web/src/app/paths.test.ts`
- Modify: `web/src/app/AppShell.tsx`: the account dropdown item
- Create: `web/src/pages/AccountPage.tsx`, `web/src/pages/account-view.ts` and
  `web/src/pages/account-view.test.ts`
- Modify: `docs/GUIDELINES.md`: add `### 4.10 Account (/account)` and one sentence on the code
  step in §4.8

**Interfaces:**
- Consumes: the Task 2 routes and `rateLimitMessage` from Task 4.
- Produces:
  - `export type Account = { email: string; twoFactorEnabled: boolean }`
  - `export type TwoFactorSetup = { secret: string; otpauthUri: string }`
  - `api.account(): Promise<Account>`
  - `api.twoFactorSetup(password: string): Promise<TwoFactorSetup>`
  - `api.twoFactorEnable(code: string): Promise<void>`
  - `api.twoFactorDisable(password: string, code: string): Promise<void>`
  - `account-view.ts`:
    - `type EnableStep = 'password' | 'scan' | 'verify'`
    - `enableStepIndex(step: EnableStep): 0 | 1 | 2`
    - `RESET_COMMAND = 'docker compose exec panel relist-panel reset-2fa'`

- [ ] **Step 1: Write the failing tests.**
  - `api.test.ts`: each of the four calls hits the right path and method, with the exact JSON
    body.
  - `paths.test.ts`: `resolvePath('/account')` matches the pattern `account`.
  - `account-view.test.ts`: `enableStepIndex` returns 0, 1 and 2 for `password`, `scan` and
    `verify`, and `RESET_COMMAND` equals the exact string.

- [ ] **Step 2: Run** `pnpm --dir web test` and confirm the new tests fail.

- [ ] **Step 3: Implement the API calls, route and dropdown.**
  - Add the four API calls, the route and the paths entry.
  - The dropdown item becomes `{ key: 'account', label: 'Account', onClick: () => navigate(paths.account) }`.

- [ ] **Step 4: Implement `AccountPage`** as the spec's UI section describes:
  - one `PageHeader`
  - a *Sign-in* card showing the email
  - a *Two-factor authentication* card with an `On`/`Off` tag, a button, and a permanent note
    showing `RESET_COMMAND` in `Typography.Text code copyable`

  The turn-on modal:
  - Three `Steps`: password → `twoFactorSetup` → `QRCode value={otpauthUri}` plus the
    `secret` with `copyable` → `Input.OTP` → `twoFactorEnable`.
  - On success, close the modal, refetch the account and show
    `message.success('Two-factor turned on. Other sessions were signed out.')`.

  The turn-off modal:
  - One `danger` button opens a modal with the password and an `Input.OTP`, which calls
    `twoFactorDisable`.
  - On success, show `'Two-factor turned off. Other sessions were signed out.'`.

  Error handling:
  - A `429` `ApiError` in either modal shows the server message in an `Alert` inside the modal.
  - Load the account on mount. On a load error, show the page-level error the other pages use.

- [ ] **Step 5: Run** `pnpm --dir web typecheck`, `pnpm --dir web test` and
  `pnpm --dir web build`, and confirm all three pass.

- [ ] **Step 6: Manual check against Review Focus item 5.**
  - Run the panel locally with `go run .`, using `RELIST_DATA_DIR=./.data`, and turn 2FA on.
  - In another terminal, run `RELIST_DATA_DIR=./.data go run . reset-2fa`.
  - Reload `/account`. You should land on `/signin`, and after signing in the page should show
    `Off`.

- [ ] **Step 7: Commit**
  ```bash
  git add web/src docs/GUIDELINES.md
  git commit -m "feat: add an Account page to turn two-factor on and off"
  ```

---

### Task 6: Embedded build and end-to-end verification

**Files:**
- Modify: `internal/webassets/dist/**`, regenerated
- Modify: `.scratch/two-factor/spec.md`: append a `## Verification` section like
  `system-hardening` has

- [ ] **Step 1: Regenerate the embedded frontend.** Run `task build-web` (or
  `go generate ./internal/webassets/...`) and confirm `git status` shows only
  `internal/webassets/dist` changes.

- [ ] **Step 2: Run the full checks:**
  - `go test -race ./...`
  - `go vet ./...`
  - `gofmt -l .`, which must print nothing
  - `pnpm --dir web typecheck`, `test` and `build`

  All must pass.

- [ ] **Step 3: Test end to end with Docker.**
  1. Run `docker compose up -d --build panel`.
  2. Create the admin and turn on 2FA with a real authenticator app.
  3. Sign out, then sign in with the code.
  4. Enter 5 wrong codes and confirm the lock message.
  5. Run `docker compose exec panel relist-panel reset-2fa` and confirm it prints the email and the
     revoked count.
  6. Sign in with the password alone.

- [ ] **Step 4: Check the screens in a headless browser.** Screenshot the Account page with 2FA
  off and on, the turn-on modal at each step, and the sign-in code step and lock message. Check
  both light and dark themes and inspect every screenshot.

- [ ] **Step 5: Record and commit.** Append the outcomes to the spec's `## Verification`, then
  commit:
  ```bash
  git add internal/webassets/dist .scratch/two-factor/spec.md
  git commit -m "chore: rebuild the embedded frontend with two-factor sign-in"
  ```
