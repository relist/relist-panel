# Spec — Two-factor sign-in for the admin account

## Problem

The panel is reachable from the internet through Traefik, and a password is the only thing between
a stranger and the Docker socket. A leaked or guessed password is a full host compromise. Nothing
slows guessing either: `POST /api/auth/login` has no rate limit.

## Decision

Settled in brainstorming on 2026-10-04:

- **TOTP, opt-in.** The admin turns two-factor on from a new Account page by scanning a QR code
  and confirming one code. It is off by default, so upgrading an existing install locks nobody out.
- **The host is the recovery path.** The admin can lose their authenticator. Anyone who can run
  `docker compose exec panel relist-panel reset-2fa` already owns the host, so there are no
  recovery codes.
- **One login request, with an optional code.** `POST /api/auth/login` accepts
  `{email, password, code?}`. A correct password without a needed code gets
  `401 {"codeRequired": true}`, and the client sends all three again. There is no half-signed-in
  token or session state.
- **Lock the account after failures.** Five consecutive failures, password or code, lock the
  account for one minute. The lock is per account, not per IP, because client IPs behind Traefik
  can't be trusted. Accepting someone could lock the admin out a minute at a time is a deliberate
  trade.
- **Codes are single-use.** A code at or before the last accepted 30-second step is refused.
- **Changing two-factor ends other sessions.** Turning it on or off deletes every session except
  the current one. `reset-2fa` deletes all of them. Session lifetime stays 30 days, and the code
  is asked only at login.
- **Changing two-factor needs the password.** A stolen session cookie can't turn two-factor off,
  and can't turn it on with the thief's phone to lock the owner out.
- **Library, not hand-rolled.** `github.com/pquerna/otp` v1.5.0 does the TOTP math (RFC 6238,
  SHA-1, 30 s, 6 digits, ±1 step skew). The frontend draws the QR code with antd's `QRCode`, so
  the backend only returns the `otpauth://` URI. Replay protection and the rate limit are this
  repo's code, and its tests.
- **The secret is stored as plain text in SQLite.** Anyone who can read the database already
  holds the recovery path, so encrypting it would add no protection.
- **The design is ready for more accounts but adds none.** Two-factor state lives on the
  `admin_credentials` row, and `reset-2fa` takes `--email`. Multi-user support overturns
  ADR-0001 and gets its own spec and PR.

## Schema

Migration `00012_two_factor.sql` adds these columns to `admin_credentials`:

| column | meaning |
|---|---|
| `totp_secret TEXT NULL` | base32 secret; `NULL` means two-factor is off |
| `totp_pending_secret TEXT NULL` | secret issued by setup and not yet confirmed |
| `totp_last_step INTEGER NULL` | 30-second step of the last accepted code (replay guard) |
| `failed_attempts INTEGER NOT NULL DEFAULT 0` | consecutive failures since the last success or lock |
| `locked_until INTEGER NULL` | unix seconds; while in the future, every credential check is refused |

`database.OpenAndMigrate` adds `_pragma=busy_timeout(5000)` to the DSN. Without it, `reset-2fa`
fails with `SQLITE_BUSY` whenever the running panel is writing.

## Login rules

`POST /api/auth/login` with `{email, password, code?}`. All reads and writes of the account's
counters happen in one transaction, so two requests sending the same code can't both succeed.

1. No account with that email: `401 invalid email or password`, as today. There is no row to
   count against.
2. `locked_until` is in the future: `429` with `Retry-After: <seconds>`. The password is not
   checked.
3. Wrong password: count a failure, then `401 invalid email or password`.
4. Correct password, two-factor on, no `code`: `401 {"error": "...", "codeRequired": true}`. This
   is **not** a failure, because it is the normal first step.
5. `code` is wrong or replayed: count a failure, then `401 invalid code`.
6. Two-factor off: any `code` sent is ignored.
7. Success: set `failed_attempts = 0`, record `totp_last_step` when a code was used, and create a
   30-day session as today.

Counting a failure: increment `failed_attempts`. When it reaches 5, set
`locked_until = now + 60s` and `failed_attempts = 0`.

## API

These routes require a session and are registered by `auth.Service`:

| route | body | behaviour |
|---|---|---|
| `GET /api/auth/account` | — | `{email, twoFactorEnabled}` |
| `POST /api/auth/2fa/setup` | `{password}` | `409` if two-factor is already on. Otherwise writes a new `totp_pending_secret`, replacing any earlier one, and returns `{secret, otpauthUri}` with issuer `Relist Panel` and the email as the account name. |
| `POST /api/auth/2fa/enable` | `{code}` | `409` if no pending secret. Checks `code` against the pending secret. On success, moves it to `totp_secret`, clears the pending secret, records `totp_last_step` and deletes the account's other sessions. Wrong code: `400 invalid code`. |
| `POST /api/auth/2fa/disable` | `{password, code}` | `409` if two-factor is off. Checks both. On success, clears all TOTP columns and deletes the account's other sessions. |

A wrong password or code at `setup`, `enable` or `disable` counts as a failure under the same lock
as login, and a locked account gets `429` here too. Otherwise these routes would be a way to guess
around the login limit.

## `reset-2fa` command

`main.go` checks `os.Args[1]` first. For `reset-2fa`, it loads config, opens and migrates the
database, calls `auth.ResetTwoFactor(ctx, db, email)` and exits. It does not start the HTTP server,
the Docker network check or the metrics loop.

```bash
docker compose exec panel relist-panel reset-2fa [--email admin@example.com]
```

In one transaction, the command:

- clears `totp_secret`, `totp_pending_secret` and `totp_last_step`
- sets `failed_attempts = 0` and `locked_until = NULL`
- deletes every session of the account

Account selection:

- Without `--email`, it uses the only account.
- Without `--email` and with more than one account, it fails and asks for `--email`.
- An email that doesn't exist exits non-zero.

It also works when two-factor is already off, which makes it the way to clear a lock. It prints
what it did.

## UI

These follow `docs/GUIDELINES.md` and use antd 6 `QRCode`, `Input.OTP`, `Steps` and `Modal`.

**Account page, `/account`.**
- The disabled "Admin account" item in the header's account dropdown becomes a link to this page.
- The page has one `PageHeader` and two cards.
  - *Sign-in*: the email, read-only.
  - *Two-factor authentication*: an `On`/`Off` tag, the action button, and a permanent note giving
    the `reset-2fa` command for a lost authenticator.

**Turn on.** A modal with three `Steps`:
1. *Password*: calls `setup`.
2. *Scan*: the `QRCode` of `otpauthUri`, plus the secret as copyable text for manual entry.
3. *Verify*: `Input.OTP` with length 6. A complete code submits to `enable`.

On success the modal closes with the message "Two-factor turned on. Other sessions were signed
out." Closing the modal midway leaves a pending secret that the next setup replaces.

**Turn off.** A danger button opens one modal with the password and an `Input.OTP`, then calls
`disable`.

**Sign in.**
- When login answers `codeRequired`, the form switches to a code step.
  - Email and password stay in component state only, never in the store or storage.
  - The step shows an autofocused `Input.OTP` with `autoComplete="one-time-code"`. A complete code
    submits.
  - **Back** returns to the password step and clears the held password.
- Errors:
  - Invalid code: shown under the input, and the input is cleared.
  - `429` at either step: "Too many attempts. Try again in N seconds.", with N from `Retry-After`.

**Data layer.** `api.login` returns a discriminated result: signed in, code required, or rate
limited with seconds. The UI branches on that type, never on error strings. The step logic for
sign-in and for the turn-on modal is pure functions in `*-view.ts` modules.

## Testing

**HTTP seam** (`internal/httpapi`, following `TestAuthSetupAndLogin`). Tests generate codes with
`totp.GenerateCode`.
- Enable round trip: setup, enable, logout, then login without a code gets `codeRequired`, and
  login with a code gets a session.
- Rejections:
  - setup with a wrong password
  - setup while two-factor is on gets `409`
  - enable with a wrong code
  - disable without a correct password and code
- Enable and disable delete other sessions and keep the current one.
- A replayed code in the same step is refused.
- Five failures of mixed kinds give `429` with `Retry-After`. While locked, a correct password is
  refused too.
- An account without two-factor logs in exactly as before.

**`internal/auth` with an injected clock.**
- The lock expires after 60 s.
- ±1 step of skew is accepted and ±2 is refused.
- `ResetTwoFactor` against real SQLite:
  - clears TOTP state and the lock
  - deletes all sessions
  - errors on an unknown email
  - picks the single account when no email is given
  - succeeds while a second connection holds the database (busy timeout)

**Frontend.**
- `api.login` result mapping for all three outcomes.
- Sign-in and turn-on step logic as pure tests.
- `pnpm --dir web typecheck`, `test` and `build`.

**End to end.**
- Build the image, turn on two-factor with a real authenticator app, sign in, trigger the lock,
  then recover with `docker compose exec panel relist-panel reset-2fa`.
- Check the Account page and both sign-in steps in a headless browser, in light and dark themes.

## Out of scope

- Multi-user support and roles. These need a separate spec and a new ADR superseding ADR-0001's
  single-admin scope.
- Recovery codes, WebAuthn/passkeys, and "remember this device".
- Changing the password from the Account page.
- Encrypting the TOTP secret at rest.
- Per-IP rate limiting.

## Verification

- `task build-web` (Vite) regenerated `internal/webassets/dist`, and `git status` showed only that
  directory changed.
- Full `go test -race ./...` passed, `go vet ./...` passed, and `gofmt -l .` printed nothing.
- Frontend `pnpm --dir web typecheck`, `test` (284 tests) and `build` all passed.
- Docker image built and the panel came up healthy on `:8899`, applying `00012_two_factor.sql`.
  The e2e ran against a scratch Compose project (`relist-e2e`, `RELIST_PORT=8899`,
  `RELIST_DATA_DIR=$PWD/.data-docker`); it did not touch the install running from the main
  checkout. The build exposed a real defect: the Dockerfile's `go-builder` stage copied `main.go`
  but not the `cli.go` added with `reset-2fa`, so the image failed to compile. The fix
  (`COPY main.go cli.go ./`) is part of this change. The scratch container, its data directory and
  the Chrome profile were removed afterwards; the shared `relist-network` was already present and
  was kept.
- End to end over HTTP: created the admin; signed in with the password alone (two-factor off,
  `200`); `2fa/setup` returned a secret and an `otpauth://` URI; `2fa/enable` accepted a generated
  code (`204`); `GET /api/auth/account` then reported `twoFactorEnabled: true`; after logout, login
  with the password alone answered `401 {"codeRequired":true}`; login with a later code (`200`)
  established a session; five wrong codes each answered `401 invalid code`; the sixth attempt — with
  the correct password — answered `429` with `Retry-After: 60` and
  `too many attempts, try again in 60 seconds`, confirming the lock. `docker compose exec panel
  relist-panel reset-2fa` printed `Reset two-factor for admin@example.com (two-factor was on);
  revoked 0 sessions.` (no live session existed at that moment); a second run with a live session
  present printed `revoked 2 sessions` and left that session unauthenticated. Signing in with the
  password alone then succeeded (`200`), and the account reported `twoFactorEnabled: false`.
- **Generated code, not a real authenticator.** No authenticator app was available, so codes were
  produced from the setup secret with the exact verified parameters (RFC 6238, SHA-1, 30 s,
  6 digits) using Python's stdlib `hmac`/`base64`. The generator reproduces the RFC 6238 test
  vectors. Codes are single-use, so a later-step code was minted for the second sign-in.
- Headless Chrome (152, `--headless=new`) captured nine screens, each inspected: the Account page
  with two-factor off (outlined `Off`, "Turn on two-factor") and on (green `On`, danger "Turn off
  two-factor"); the turn-on modal at Password, Scan (QR code, copyable secret, `Steps` at 2) and
  Verify (`Input.OTP` at 3); the turn-off modal (password plus `Input.OTP`, danger OK); the sign-in
  password step; the sign-in code step (`Input.OTP`, Back link); and the lock message
  ("Too many attempts. Try again in 52 seconds."). No runtime errors appeared while driving the
  flows.
- **Light theme only.** The app hardcodes `buildTheme('light')` (`web/src/main.tsx`) and has no
  theme toggle, so the plan's dark-theme screenshots are not applicable; the app was not modified
  to add dark mode. All screens above were captured in light theme.
