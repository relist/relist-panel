# 05 — Environments

**What to build:** Within a Project, an operator can create and delete Environments, and Project creation automatically provisions a default "production" Environment.

**Blocked by:** 04

**Status:** closed

- [x] A migration creates the `environments` table (id, project_id FK, name, created_at), unique on (project_id, name), per `docs/sa.md`
- [x] Creating a Project also creates a default Environment named "production" (spec user story 12) — this extends the Project-creation handler from ticket 04
- [x] The Project detail page shows Environments as Tabs (per `ProjectDetail.dc.html`), defaulting to "production"
- [x] An operator can create an additional Environment with a name
- [x] An operator can delete an Environment
- [x] Covered by HTTP-seam tests per the spec's Testing Decisions

## Comments

Implemented in `205ee21` with `internal/migrations/00004_environments.sql` and the new `internal/environments` domain package. The migration uses integer IDs consistent with the existing schema, a project foreign key with cascade behavior, case-insensitive uniqueness scoped to `(project_id, name)`, and a creation timestamp. `docs/sa.md` was updated to match the current Project and Environment ID types.

Project creation now inserts the default `production` Environment in the same transaction as the Project. Project responses and cards report the live Environment count. Authenticated nested HTTP routes support Environment list, create, and delete operations; deletion intentionally permits removing the last Environment. Environment deletion remains plain for this ticket; cascading later Composes and Docker containers belongs to the future Compose/runner work.

The Project detail screen now loads Environments as Ant Design `Tabs`, selects `production` by default, and provides create/delete controls. The referenced `ProjectDetail.dc.html` mockup was not present, so the UI follows the existing Ant Design and `DESIGN.md` tokens. The package path `internal/environments` follows this ticket's explicit handoff even though ADR-0003 uses singular examples.

HTTP-seam tests use real SQLite migrations and `httptest` requests. Verified with `pnpm --dir web typecheck`, `make test`, `go test -race ./...`, `go vet ./...`, `go build -o /tmp/relist-panel-ticket05 .`, and `git diff --check`. The implementation and closure are kept in separate local commits; no remote was configured or changed.
