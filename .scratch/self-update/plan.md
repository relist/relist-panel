# Plan — Panel update (execution order for tickets 01–07)

Spec (binding): `.scratch/self-update/spec.md`. ADR: `docs/adr/0024-panel-self-update.md`.
Tickets: `.scratch/self-update/issues/NN-*.md`. Each task below names the ticket and spec
sections it implements; the ticket and spec text are the requirements. Where this plan adds
a decision the spec does not make, it is marked **Decision**.

Ticket 08 (switch the Install file to the published image) is **not** in this plan: it needs
a first release on GHCR (ticket 02's human part) and changes how the owner rebuilds the live
install. Ticket 02's human part (signing key, package visibility) is also not in this plan.

## Global Constraints

- **Never touch the live install.** This checkout is also the owner's live panel: Compose
  project `relist-panel` (containers `relist-panel-panel-1`, `relist-panel-traefik-1`),
  data dir `/var/lib/relist-panel`. Never run `docker compose up/down/restart/pull` in the
  repo root, never stop, recreate or exec into those containers, never write under
  `/var/lib/relist-panel`. Any real-Docker check uses a throwaway project in a temp
  directory with its own project name (prefix `su-test-`) and is torn down afterwards
  (`docker compose -p su-test-… down -v`, remove temp dirs and test images you built).
- Vocabulary from `CONTEXT.md`: Install, Install file, Install override, Panel update,
  Updater, Update manifest, Install invariant. "Migration" means a server Migration only;
  SQL changes are "schema migrations".
- Never edit an existing schema migration under `internal/migrations/`. The new one is
  `00025_panel_updates.sql`.
- Commits: Conventional Commits, no scope (`feat: …`, `fix: …`, `test: …`, `docs: …`),
  imperative, lowercase after the colon. **No `Co-Authored-By` or `Claude-Session`
  trailers.** Commit on the current branch `feat/panel-self-update`. Never push.
- Verification before reporting done: `go vet ./...` and `go test ./...` green; for web
  changes also `pnpm --dir web typecheck` and `pnpm --dir web test`.
- Match the surrounding code: package layout per ADR-0003 (one adapter package, one port
  per consumer), comment density and naming of neighbouring files, table tests with fakes
  as in `internal/vulnscan/*_test.go` and `internal/migration/*_test.go`.
- Frontend (`web/`): read https://ant.design/llms.txt first, then only the linked
  component pages you use; installed `antd` is 6.5.1. Follow `docs/GUIDELINES.md`.
- Go standard library only for crypto (ed25519). No new Go module dependencies unless a
  task says so.

## Codebase facts the tasks rely on

- Subcommands are dispatched at the top of `main()` in `main.go` (`reset-2fa`,
  `create-admin`, `migration`, `maintenance-page`). The server is wired further down in
  `main.go` (`httpapi.NewHandler(ctx, httpapi.Deps{…})`).
- `go:embed` cannot reach parent directories. `docker-compose.yml` sits in the repo root,
  so the embed directive lives in package `main` (root) and the bytes are passed into
  internal packages. The `Dockerfile` go-builder stage copies only `*.go`, `internal/`,
  `cmd/`: it must also `COPY docker-compose.yml ./`.
- Deployment queue: `internal/deployments/queue.go` has `Hold()`, `Release()` (a single
  bool, not a counter), `WaitIdle(ctx)` and `Busy()`. `Busy()` is true while held, and the
  scan worker's `vulnscan.DeployGate.Busy()` is backed by it — so holding the queue also
  gates the scan queue between tasks.
- Migration lock: `internal/migration/lock.go` — the middleware and `allowedWhileLocked`
  allowlist are the shape to copy for the update lock.
- `VACUUM INTO`: `internal/migration/items.go` (`writeDatabase`).
- One-run helper containers: `internal/dockerexec/volume_runner.go` (ADR-0008/0010).
  Engine API client: `internal/dockerexec/engine.go`.
- Prune ownership: `internal/dockerhost/plan.go` (`Ownership`, `owner`).
- Auth status route: `internal/auth/auth.go` (`GET /api/auth/status`, `s.status`).
- Web: routes `web/src/app/routes.tsx`, paths `web/src/app/paths.ts`, shell/sider
  `web/src/app/AppShell.tsx`, pages under `web/src/pages/`, API data hooks under
  `web/src/data/`.

---

## Task 1: Version stamp, named image, embedded Install file

Implements ticket `issues/01-version-and-install-file.md` and spec "Install file and image
pin" (phase "Until releases exist").

- **Decision:** the embedded copy is exposed from package `main` (`//go:embed
  docker-compose.yml` into a `[]byte`) and handed to later packages as a value; the
  ticket's "`internal/install` package exports `EmbeddedInstallFile()`" becomes "package
  `main` holds the embed; tasks 3–4 receive it as a dependency". The test that the
  embedded bytes equal the repo file lives in package `main`.
- **Decision:** `GET /api/auth/status` gains `"version": "<version>"`; thread the version
  into `auth` through its existing constructor/options rather than a global.
- Update `Dockerfile` (`ARG VERSION=dev`, `-ldflags "-X main.version=${VERSION}"`,
  `COPY docker-compose.yml ./` in go-builder) and `Taskfile.yml` build (`-ldflags` with
  `VERSION` defaulting to `dev`).
- `docker-compose.yml`: keep `build: .`, add `image: ghcr.io/relist/relist-panel:local`
  and `pull_policy: build`, and the comment header from the spec. Add
  `docker-compose.override.example.yml`.
- Do not run `docker compose` against the repo root (Global Constraints). Validate the
  file with `docker compose -f docker-compose.yml config -q` only.

## Task 2: Update check, settings, status API

Implements ticket `issues/03-update-check.md` and spec "Update manifest", "Update check",
"API" (the `status`, `check`, `settings` rows only).

- New package `internal/panelupdate`.
- **Decision:** the release public key is a package-level `var releasePublicKey = "<base64
  placeholder>"` in `key.go`, settable at build time with `-ldflags -X
  relist-panel/internal/panelupdate.releasePublicKey=<base64>` (used for end-to-end tests
  with a test key). Production builds use the committed value. Unit tests inject keys
  directly and never read the variable.
- **Decision:** manifest location is
  `https://github.com/relist/relist-panel/releases/latest/download/update-manifest.json`
  (and `.sig`), overridable with env `RELIST_UPDATE_MANIFEST_URL` (the `.sig` URL is the
  manifest URL + `.sig`). The signature is still verified, so the override is safe.
- Version compare: semver `MAJOR.MINOR.PATCH` with optional leading `v`; anything else
  (including `dev`) is never offered an update — status says "Development build: updates
  are not offered".
- Schema migration `00025_panel_updates.sql` creates both tables from the spec. Write it
  so a later task can add columns with a new migration; this task owns only the columns
  it uses plus the `panel_updates` run columns listed in the spec (id, from_version,
  to_version, state, error, started_at, finished_at).
- Routes registered through `httpapi.Deps` the way `vulnscan` registers its routes;
  admin-only like the other `/api/*` routes.
- The daily check must skip writing while the update lock is on — the lock arrives in
  Task 5; leave a small `func() bool` hook (default false) that Task 5 wires.

## Task 3: Install discovery and reading Install files

Implements the "The Install, found from labels" section of the spec and the first two
bullets of ticket `issues/04-install-discovery-and-preflight.md`.

- Discovery: inspect the panel's own Container (`os.Hostname()` → Engine API container
  inspect) and read `com.docker.compose.project`, `com.docker.compose.project.working_dir`,
  `com.docker.compose.project.config_files` (comma-separated). Return
  `Install{Project, Dir string; ConfigFiles []string}` or an Unavailable reason with the
  spec's wording.
- File reads go through a one-run helper Container of the panel's current image (image ID
  from the same inspect), Install dir mounted `:ro` at the same path, following
  `volume_runner.go`'s pattern. It returns the bytes of each config file and `.env`
  (missing `.env` → empty, not an error).
- Put the Docker-facing code in `internal/dockerexec` (adapter) behind a port interface
  declared in `internal/panelupdate`, per ADR-0003.
- `GET /api/panel-update/status` (from Task 2) adds `install: {available, reason}`.
- Unit tests with fakes; one real-Docker test is optional and, if written, must be skipped
  unless an env var like `RELIST_DOCKER_TESTS=1` is set and must use a `su-test-` project.

## Task 4: Preflight

Implements spec "Preflight (no writes)" steps 1–7, the "Install invariant kinds" table,
and the rest of ticket `issues/04-install-discovery-and-preflight.md`, plus
`POST /api/panel-update/preflight`.

- Inputs: the Install (Task 3), the verified newest manifest (Task 2), the embedded
  Install file (Task 1).
- Pull by digest through the Engine API (`POST /images/create?fromImage=…`), as the
  Scanner image pull does in `internal/dockerexec/trivy*.go`.
- `git merge-file -p <ours> <base> <theirs>`: exit 0 merged, 1–127 conflicts, 255 error.
  Run it in the panel process (git is in the image) on temp files under the scratch dir.
- `docker compose config --format json` and `config --hash '*'` run in the read-only
  helper with `--project-directory <real Install dir>`, the scratch files as `-f` in label
  order, `--env-file <scratch>/.env` — see spec step 4 for why. The scratch dir is
  `<data dir>/.panel-update/preflight/<report id>/`; keep only the latest.
- Invariant evaluators: one function per kind, over the decoded config JSON. Unknown kind →
  report state "Needs a manual update". Blame: re-run `config` without the Install
  override; if the invariant passes there, blame the override, else the Install file (or
  `.env` for `service-image`).
- Report struct and id are kept in memory (latest only) for Task 6's apply to compare.
- Tests per the ticket, including the Traefik `command:` override case and the
  relative-path override case, using recorded `config` JSON fixtures under `testdata/`.

## Task 5: Update lock, start-locked, watcher, quiesce and backup

Implements spec "Apply" steps 1–2 and 5, and the lock/watcher/quiesce/backup bullets of
ticket `issues/05-apply-updater-rollback.md`.

- `status.json` lives in `<data dir>/.panel-update/<run id>/status.json`; "newest run" is
  the highest run id (use a sortable id, e.g. UTC timestamp `20261101T120000Z`). Writes
  are atomic (temp + rename). States: `quiescing`, `backing-up`, `starting-updater`,
  `writing-files`, `compose-up`, `waiting-health`, `succeeded`, `rolling-back`,
  `rolled-back`, `rollback-failed`, `interrupted`. Terminal: `succeeded`, `rolled-back`,
  `rollback-failed`, `interrupted`.
- Update lock = HTTP write middleware (allowlist from the spec) + `Queue.Hold()`; it is
  derived from the newest `status.json`, checked at startup **before** the HTTP server
  and workers start.
- **Decision:** `Queue.Hold/Release` is a single bool shared with the Migration lock.
  Panel update refuses to start while a Migration lock is held, and the Migration's
  `serve`/lock entry must refuse while the update lock is on (add that check where the
  Migration lock is taken, with a test). Releasing the update lock must not release a
  hold the Migration owns — with mutual exclusion this holds; document it in a comment.
- Watcher: poll the newest `status.json` every 2 s while non-terminal. Terminal → copy
  state to `panel_updates`, release the lock, remove the exited Updater Container
  (`relist-panel-updater-<run id>`). Non-terminal and the Updater Container is missing or
  exited → write `interrupted` (with a reason), release the lock.
- Quiesce + backup function (called by Task 6's apply): write `quiescing`, Hold, WaitIdle,
  wait for the scan worker's current task (expose a wait from `vulnscan` if none exists),
  then `backing-up`: `VACUUM INTO` → `panel.db`, copy every Install config file and `.env`
  into the run dir (the panel reads them via the Task 3 helper; writing the backup copies
  goes to the data dir, which the panel can write directly). Remove the previous run dir
  only after this backup is complete.
- Wire the Task 2 daily-check hook to the lock.
- Tests per the ticket (start-locked runs no Deployment, dead Updater → interrupted,
  503 while locked, allowlisted routes pass).

## Task 6: Updater subcommand, apply, rollback, prune ownership

Implements spec "Apply" steps 3–4, "API" `apply`, and the remaining bullets of ticket
`issues/05-apply-updater-rollback.md`.

- `POST /api/panel-update/apply {report_id, manual_steps_done}`: re-run Preflight; 409 if
  the report differs or a manual step is unticked; then Task 5's quiesce + backup; then
  start the Updater with the Engine API equivalent of the spec's `docker run` (no Compose
  labels; name `relist-panel-updater-<run id>`; refuse if one is still running, remove a
  stale exited one). Returns the run id.
- Subcommand `relist-panel panel-update apply --run <id>` in `main.go`, implemented in
  `internal/panelupdate` (logic) + a thin CLI file. It needs: data dir, Install dir,
  project, config files, target image digest, `RELIST_DOMAIN`. **Decision:** the panel
  writes these into `<run dir>/plan.json` before starting the Updater, so the Updater's
  only flag is `--run`; the Updater reads `RELIST_DATA_DIR` from its env (pass it in the
  `docker run` env).
- Updater steps per spec 4.1–4.5; it shells out to `docker compose` (as the panel already
  does) and uses the Engine API for container lookups. Health: find Traefik by labels
  (`com.docker.compose.project=<project>`, `com.docker.compose.service=traefik`), its IP on
  `relist-network`, `GET https://<ip>/healthz` with Host + SNI `RELIST_DOMAIN`,
  `InsecureSkipVerify`; plus panel Container image ID == target. 180 s, every 2 s. Make the
  durations injectable for tests.
- Rollback restores Install files and `.env` from the run dir, then stop panel → copy
  backup `panel.db` over `<data dir>/panel.db` (remove `-wal`/`-shm` siblings) → `up -d` →
  health against the old digest.
- Prune: `internal/dockerhost` ownership lists the previous panel image (from the last
  `succeeded` run's from-image) and never offers `.panel-update/` content; label the owner
  "Panel update".
- Tests with fakes per the ticket. Then one manual end-to-end check on a **throwaway
  sandbox Install** (never the live one): a temp dir with a copy of the Install file
  renamed project `su-test-update`, its own data dir under the temp dir, two locally built
  panel images tagged `su-test/panel:a` and `:b` (built with
  `-ldflags -X …releasePublicKey=<test key>`), a manifest signed with the test key served
  from a local HTTP server via `RELIST_UPDATE_MANIFEST_URL`. If ports 80/443 are taken by
  the live Traefik, override the sandbox Traefik ports in its override file. Record what
  you ran and saw in the report; tear everything down. If the sandbox cannot be made to
  work in reasonable time, report DONE_WITH_CONCERNS with what blocked it rather than
  touching the live install.

## Task 7: Web — Panel update page

Implements ticket `issues/06-web-panel-update-page.md` and spec "Web".

- Route `/panel-update` (add to `paths.ts`, `routes.tsx`), sider entry below Docker, header
  `Update available` tag beside the version (version from `/api/auth/status`).
- All states from the spec's "Page states" table, including Interrupted; polling rules from
  "Polling"; confirm text from "Confirmation"; Preview with read-only Monaco diff editor,
  invariant list with blame, recreated Services with the Traefik warning, manual-step
  checkboxes.
- Component tests in the style of the neighbouring `*.test.tsx` for: each top-level state
  render, Restarting on network error/5xx, 401 → sign-in, reload once on target version.
- Rebuild the embedded frontend (`task build-web`) and commit it as its own
  `chore: rebuild the embedded frontend` commit, as earlier features did.

## Task 8: Release tooling and workflow

Implements the "Agent" part of ticket `issues/02-release-workflow-and-key.md` and the
`release/*.json` bullet of ticket `issues/07-docs.md`.

- `cmd/relist-release` with `manifest`, `sign`, `verify`, and **Decision:** a `keygen`
  subcommand (prints a new base64 private and public key) so the owner can do the human
  step with repo tooling.
- `release/invariants.json`: convert every note under README "Upgrading an existing
  install" into invariants of the kinds Task 4 implements; `service-image` for `panel`.
  `release/manual-steps.json`: anything that cannot be an invariant (e.g. opening port
  443). The `manifest` subcommand validates kinds against the same list Task 4 uses
  (import it; do not duplicate it).
- `.github/workflows/release.yml` per the ticket: tag `v*`, buildx amd64+arm64, push to
  GHCR with `GITHUB_TOKEN` (`permissions: packages: write, contents: write`), read the
  index digest, manifest, sign with secret `RELIST_RELEASE_KEY`, create the GitHub Release
  with both files. Pin third-party actions by full commit SHA with a version comment.
- Tests: manifest round-trip, sign/verify, a tampered byte fails verify, an unknown
  invariant kind fails `manifest`.

## Task 9: Docs

Implements the rest of ticket `issues/07-docs.md`.

- README: the Install file vs override and `.env` rule; how Panel update works and what it
  does not promise (no downgrade, Traefik restart drops Domains briefly, `--env-file`
  Installs refused, unavailable until releases exist); the one-time manual upgrade
  (spec section); shorten "Upgrading an existing install" to a pointer to
  `release/invariants.json` / `release/manual-steps.json` while keeping the notes
  operators of today's `build:` installs still need.
- `docs/GUIDELINES.md`: add section 4.15 *Panel update* (route, placement, states, polling,
  confirmation) matching what Task 7 built.
- Set `Status: closed` on tickets 01, 03, 04, 05, 06, 07 (and leave 02 `ready-for-human`
  with a note of what remains: key, secret, package visibility; and 08 open).
