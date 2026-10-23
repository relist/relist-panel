# 02 — Remove env seeding and the web setup screen

Status: closed
Blocked by: 01

Spec: [../spec.md](../spec.md)

## Backend

- `internal/auth/auth.go`
  - Remove the `POST /api/auth/setup` route, the `setup` handler, and
    `createAdmin` (`insertAdmin` stays; `CreateAdmin` from ticket 01 uses it).
  - Remove `setupBlocked`, `SetSetupBlocker`, and their comments.
  - Remove `"/api/auth/setup"` from `publicPath`.
  - `status`: respond `{"authenticated": bool}` only. Keep skipping the
    session check and clearing the cookie when no admin exists.
  - `login`: when `findAdmin` finds no row, answer
    `401 "invalid email or password"` whether or not any admin exists. Drop
    the `adminExists` fallback there and its comment about the single pooled
    connection.
  - Keep `adminExists` only if something still calls it after the above.
- `internal/auth/password.go`: delete `AdminEmailEnv`, `AdminPasswordEnv`,
  `SeedAdmin`, `markPasswordFromEnv`, `flagPasswordFromEnv`. Add
  `AdminExists(ctx, db) (bool, error)` if `main.go` needs it for the boot log
  (or reuse an existing helper).
- `internal/auth/twofactor.go`: keep `passwordFromEnv` in the account
  response; reword the comment to "The password is still the one
  RELIST_ADMIN_PASSWORD seeded, on installs from before create-admin."
- `internal/config/config.go`: remove `AdminEmail`, `AdminPassword`, their
  comment, and the `os.Getenv` reads. `internal/config/config_test.go`
  `TestLoadAdminSeedAndMigrationSettings`: drop the admin env and assertions;
  rename to `TestLoadMigrationSettings`.
- `main.go`: replace the `auth.SeedAdmin` call and its comment with: if no
  admin exists, `log.Println(`auth: no admin account exists; run "relist-panel create-admin" in the panel container`)`.
  Remove `SeededAdmin: cfg.AdminEmail` from `migration.Options`.
- `internal/migration/options.go`: remove `SeededAdmin`.
- `internal/httpapi/httpapi.go`: remove `SeededAdmin:` from the `LocalHost`
  literal (~line 150) and `authService.SetSetupBlocker(target.InProgress)`
  (~line 168).
- `internal/migration/target.go`: remove `InProgress` and its comment if
  nothing else calls it (`grep -rn "InProgress()" internal`).
- `internal/migration/preflight.go`: remove `LocalHost.SeededAdmin`, the
  admin query, and the "already has an admin account" block, with its
  comment. Remove the `auth` import if unused.

Do not edit anything under `internal/migrations/` (committed SQL).

## Frontend (`web/`)

Read `AGENTS.md` → "Frontend and Ant Design" first.

- `web/src/pages/AuthPages.tsx`: delete `SetupPage`; in `SignInPage` remove
  the `entryScreen(auth) === 'setup'` redirect and its comment, and the
  "First run? Create the admin account" footer.
- `web/src/app/routes.tsx`: remove the `setup` route and the `SetupPage`
  import.
- `web/src/app/paths.ts`: remove `setup: '/setup'` and `/setup` from the
  comment near line 49.
- `web/src/app/entry.ts`: `entryScreen` returns `'app' | 'signin'` only;
  update the doc comment.
- `web/src/data/api.ts`: drop `setupRequired` from the status type; remove
  `setup`.
- `web/src/data/store.tsx`: drop `setupRequired` from `AuthState` and every
  `setAuth(...)`; remove `signUp` (type, implementation, context value).
- Tests: remove the `SetupPage` suite in `AuthPages.test.tsx`; update
  `entry.test.ts`, `store.test.tsx`, `RequireAuth.test.tsx`,
  `ComposePage.test.tsx`, `AppShell.test.tsx` to drop `setupRequired`.
- Rebuild the embedded bundle: `go generate ./internal/webassets` (runs
  `pnpm --dir ../../web build`), and commit `internal/webassets/dist`.

## Go tests

- Wherever a test creates the admin with `POST /api/auth/setup`
  (`internal/httpapi/httpapi_test.go` ~62, ~2223, ~2732;
  `internal/auth/lock_test.go` ~54), create it with
  `auth.CreateAdmin(ctx, db, email, password, time.Now)` on the test's DB
  instead. Drop the second-setup-returns-409 assertion (~86) and the
  `SetupRequired` field (~2359).
- `internal/httpapi/migration_test.go` ~79, ~90: tests of setup being blocked
  during a migration — delete.
- `internal/migration/integration_test.go` `signIn(setup bool)`: create the
  admin with `auth.CreateAdmin` on that panel's DB instead of the HTTP call.
- `internal/migration/lock_test.go` ~244: remove the `/api/auth/setup` row.
- `internal/migration/preflight_test.go`: delete the "already has an admin
  account" case (~47) and the "an admin seeded from the env … is not data"
  loop (~114); add a case that an admin row alone does not block.
- `internal/auth`: delete `SeedAdmin` tests; add: no admin →
  `POST /api/auth/login` 401 `invalid email or password`;
  `GET /api/auth/status` → `{"authenticated": false}` with no
  `setupRequired` key.

## Scripts

`scripts/probe-migration.sh` and `scripts/sandbox-migration.sh`:

- Stop passing `RELIST_ADMIN_*` to `docker compose up` (the `admin=(...)`
  arrays, including the reload branch in `sandbox-migration.sh`).
- After `/healthz` is up, create the old host's admin with
  `printf '%s\n' 'correct horse battery staple' | docker exec -i "$OLD" docker exec -i install-panel-1 relist-panel create-admin --email admin@example.com --password-stdin`
  (match each script's existing `docker exec` nesting) and delete the
  `POST /api/auth/setup` → 201 line; keep the login check.
- New host: create `seed@example.com` the same way (password
  `seeded password 123`, matching `$SEED`) where the scripts relied on the
  env admin. In `probe-migration.sh` delete the
  "setup is still open on the new host" (409) assertion and its comment.

## Docs

As listed in the spec's "Docs" section: README, `docker-compose.yml`,
`docs/design-states.md`.

## Done when

- `go test ./...` and `go vet ./...` pass.
- In `web/`: `pnpm test` and `pnpm build` pass.
- `grep -rnE "auth/setup|setupRequired|SetupPage|SetSetupBlocker|signUp|SeedAdmin|SeededAdmin|AdminEmailEnv" --exclude-dir=node_modules --exclude-dir=.scratch --exclude-dir=.git .`
  returns nothing.
- `grep -rn "RELIST_ADMIN" --exclude-dir=node_modules --exclude-dir=.scratch --exclude-dir=.git .`
  matches only the README upgrade note, the banner/password-card wording,
  the `twofactor.go` comment, and `internal/migrations/00019_*.sql`.
- One commit, Conventional Commits, no scope, no trailers, e.g.
  `feat: drop the web setup screen and RELIST_ADMIN_* seeding, so create-admin is the only way to make the first admin`.

## Comments

Implemented on `feat/cli-admin` in `ba7a03c`.

- `status` keeps the no-admin short-cut through the exported
  `auth.AdminExists`; the private `adminExists` and `createAdmin` are gone.
  `login` now answers 401 for an unknown email whether or not any admin
  exists.
- `main.go` logs the create-admin line only when no admin exists.
  `RELIST_ADMIN_*` set in the env is ignored: checked against a live binary
  where signing in with the env pair gave 401, `POST /api/auth/setup` gave
  401 (no longer public), and after `create-admin` sign-in gave 200.
- Deviation: two places still name `/api/auth/setup`, on purpose, to check
  it is gone. They are `TestNoAdminRefusesSignInLikeAWrongPassword` in
  `internal/httpapi/httpapi_test.go`, and a `!= 201` check in
  `scripts/probe-migration.sh` in place of the old 409 one. So the ticket's
  first grep matches those two files and nothing else.
- Both migration scripts create their admins with `create-admin
  --password-stdin` through a `mkadmin` helper. They pass `bash -n`, but I
  did not run them; they need the Docker-in-Docker sandbox.
- `AuthPages.test.tsx` now checks that `SignInPage` offers no way to create
  an admin; the `AuthLayout` `footer` prop is gone with its only use.
- Docs also cover `docs/GUIDELINES.md` (routes line, §4.8), and
  `docs/design-states.md` drops the three `auth-setup*` states
  (105 → 102).
- Verified: `go vet ./...`, `go test ./...`, `pnpm typecheck`,
  `pnpm test` (568 passed), `go generate ./internal/webassets`.
