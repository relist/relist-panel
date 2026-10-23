# CLI admin

The first admin is created only by `relist-panel create-admin`, run in the
panel container. The web setup screen (`/setup`, `POST /api/auth/setup`) and
env seeding (`RELIST_ADMIN_EMAIL` / `RELIST_ADMIN_PASSWORD`) are both removed.

## Why

Today there are two ways to create the first admin:

1. Env seeding at boot (`auth.SeedAdmin`). The password stays readable in
   `.env` and `docker inspect` until the operator removes it, which is why the
   password-from-env banner exists.
2. The web setup screen, open to whoever reaches a fresh panel first. During a
   server migration it needs its own guard (`SetSetupBlocker` /
   `Target.InProgress`).

A CLI command needs shell access to the host, so it has no race, and the
password never sits in config. It follows the existing `reset-2fa` command
(`cli.go`), which already opens the database while the panel runs.

## The command

```
relist-panel create-admin [--email ADDRESS] [--generate | --password-stdin]
```

Run as `docker compose exec panel relist-panel create-admin` (or
`docker exec -it <container> relist-panel create-admin`).

- **Interactive** (no password flag, stdin is a terminal):
  1. `Email: ` — read a line, unless `--email` was given.
  2. `Password (leave empty to generate one): ` — read without echo.
  3. If non-empty: `Confirm password: ` — read without echo; on mismatch print
     `create-admin: passwords do not match` and exit 1 (no retry loop).
  4. If empty: generate one, as `--generate` does.
- **`--generate`**: generate the password; no password prompt.
- **`--password-stdin`**: read the password as the first line of stdin
  (trailing `\r\n`/`\n` stripped). Requires `--email`. For scripts.
- `--generate` and `--password-stdin` together → usage error.
- No password flag and stdin is not a terminal → exit 2 with
  `create-admin: stdin is not a terminal; pass --password-stdin or --generate`.
  An `--email`-less non-terminal run fails the same way.

**Generated password**: 18 bytes from `crypto/rand`, `base64.RawURLEncoding`
(24 characters). Printed once.

**Validation**: the existing `validateEmail` and `validatePassword` in
`internal/auth`. Errors print as `create-admin: <message>` and exit 1.

**Output on success** (stdout):

```
Created admin admin@example.com.
```

and, when generated, a second line:

```
Password: <password>
```

Prompts and the hint `Sign in, then change it in Account.` (after a generated
password) go to stderr, so stdout holds only the lines above and a script can
pick the password up from it.

**Exit codes**: 0 created; 1 an admin already exists, invalid input,
mismatch, or a database error; 2 usage error. With an admin already present:
`create-admin: an admin account already exists` (exit 1); nothing is changed.

The new admin has `password_from_env = 0`.

## Panel behaviour

- **No admin**: the panel starts normally and logs once at boot:
  `auth: no admin account exists; run "relist-panel create-admin" in the panel container`.
  The web UI shows the ordinary sign-in screen; any sign-in answers
  `401 invalid email or password`, the same as a wrong password. No special
  screen, no setup link.
- **Admin exists**: unchanged.
- **`RELIST_ADMIN_EMAIL` / `RELIST_ADMIN_PASSWORD`**: no longer read.
  Existing installs keep their admin. An admin already flagged
  `password_from_env` keeps the flag, so the banner still asks them to change
  the password; nothing sets the flag any more. The column stays (never edit a
  committed migration; dropping it is not worth a new one).
- **Server migration**: a fresh new host may have no admin or one made with
  `create-admin`; the old host's admin replaces it either way. Preflight drops
  its admin check entirely (creating an admin now needs a shell on the host,
  so an admin is no longer a sign that a stranger set the panel up); the
  existing "this panel already has data" checks remain.

## API changes

- `POST /api/auth/setup` removed (route, handler, `publicPath` entry).
- `GET /api/auth/status` returns `{"authenticated": bool}`; `setupRequired`
  is dropped.
- `POST /api/auth/login` with no admin answers
  `401 {"error": "invalid email or password"}` instead of
  `409 admin account setup is required`.
- `GET /api/auth/account` keeps `passwordFromEnv` (legacy installs).

## Out of scope

- `reset-password` or any other admin-recovery command.
- An installer that runs `create-admin` for the operator.
- Removing the `password_from_env` column, its banner, or its API field.

## Docs

- README: remove the `RELIST_ADMIN_*` entry. Add a "Create the admin" step
  after install with the three command forms above. Note under upgrading that
  `RELIST_ADMIN_*` is no longer read and can be removed from `.env`.
  Migration step 3: replace the `RELIST_ADMIN_EMAIL` sentence with "an admin
  made with `create-admin` on the new panel is fine; the old host's admin
  replaces it".
- `docker-compose.yml`: remove `RELIST_ADMIN_EMAIL`, `RELIST_ADMIN_PASSWORD`
  and their comment.
- `docs/design-states.md`: remove the `auth-setup` state.
- `web/src/components/PasswordFromEnvBanner.tsx` /
  `ChangePasswordCard.tsx`: wording may keep mentioning
  `RELIST_ADMIN_PASSWORD`; it is still true for the installs that see it.

## Tickets

1. [issues/01-create-admin-command.md](issues/01-create-admin-command.md) — add the command.
2. [issues/02-remove-env-seed-and-web-setup.md](issues/02-remove-env-seed-and-web-setup.md) — remove the other two ways. Blocked by 01.
