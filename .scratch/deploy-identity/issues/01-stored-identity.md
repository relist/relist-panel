# 01 — Store deploy identity on the Compose row

**What to build:** A Compose's deploy identity (its data-dir path and `docker compose -p` project
name, ADR-0002) is persisted on the Compose row at creation and read by every lifecycle action,
instead of being recomputed from the *current* Project/Environment names each time. Renames of
Project or Environment must never change an existing Compose's identity (ADR-0005).

**Blocked by: —**

**Status:** closed

- [x] Migration `00007` adds `project_slug TEXT NOT NULL DEFAULT ''` and `env_slug TEXT NOT NULL DEFAULT ''` to `composes`; an idempotent Go backfill at startup fills any empty slugs from current parent names (legacy rows); fresh creates always set them
- [x] Compose create resolves the parent slugs once (via the existing parent join) and stores them on the row
- [x] `runRequestFor` derives identity from the row's stored slugs only; the deploy-time parent-names join (`runRequestForCompose`) is no longer needed by lifecycle actions
- [x] `deploy`, `stop`, `restart`, `delete`, `logs`, `currentStatus`/`withStatus(es)` all act on the stored identity
- [x] HTTP-seam test: deploy → rename Project → `stop`, `restart`, `logs` act on the original identity; `delete` calls `DownWithVolumes` with the original identity (no orphaned skip); status reflects the original path
- [x] HTTP-seam test: same via Environment rename
- [x] Covered by the full suite: `make test`, `go test -race ./...`, `go vet ./...`, `pnpm --dir web typecheck`

## Comments

Implemented on `deepen-deploy-identity`. Migration `00007_compose_identity.sql` adds the two slug
columns (Up/Down verified). `composes.BackfillLegacyIdentities` is the idempotent Go backfill — it
fills empty slugs from the parents' current names and never re-derives filled ones — wired into
`main.go` right after `database.OpenAndMigrate`. Compose create freezes `projectSlug`/`envSlug` at
insertion (the `parentNames` join's only remaining use); `runRequestForCompose` is deleted and
`runRequestFor(dataDir, compose)` derives everything from the row, so deploy/stop/restart/delete/
logs and the status attachers (`withStatus`, `withStatuses`, `currentStatus`) all act on stored
identity. The `Compose` model gained `projectSlug`/`envSlug` JSON fields (nothing removed).

Verified TDD-first at the HTTP seam in `internal/httpapi/httpapi_test.go`: both rename-safety tests
(Project rename via `PUT /api/projects/{id}`; Environment rename written directly to the DB because
Environments have no rename endpoint yet) failed before the fix — status degraded to zero and stop
would 400 — and pass after, asserting every recorded `fakeComposeRunner` request (up, status, down,
restart, logs, down-with-volumes) targets `rename-co-production-web` / `rename-env-co-production-web`
and the original file paths. `TestComposeCreateStoresDeployIdentity` probes the stored slugs in
SQLite; `TestBackfillLegacyIdentitiesFreezesCurrentParentSlugs` covers backfill and its idempotence.
Full suite green: `make test`, `go test -race ./...` (42 tests), `go vet ./...`,
`go build`, `pnpm --dir web typecheck`, `git diff --check`. Also bumped the latest-migration
assertion in `internal/database/database_test.go` from 6 to 7.
