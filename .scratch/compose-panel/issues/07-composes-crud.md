# 07 — Composes: CRUD of YAML

**What to build:** Within an Environment, an operator can create and edit Composes by writing/pasting a `docker-compose.yml` in a Monaco editor. Composes are listed but not yet deployable — that's ticket 08.

**Blocked by:** 05

**Status:** closed

- [x] A migration creates the `composes` table (id, environment_id FK, name, yaml_content, created_at, updated_at), unique on (environment_id, name), per `docs/sa.md`
- [x] The Composes sub-tab (per `ProjectDetail.dc.html`) lists all Composes in the active Environment, with status shown as a placeholder (e.g. "not deployed") since deploy execution doesn't exist yet
- [x] An operator can create a Compose with a name and YAML content
- [x] The Compose detail page's General tab (per `ComposeGeneral.dc.html`) embeds a Monaco editor with YAML syntax highlighting for editing and saving the YAML
- [x] Covered by HTTP-seam tests per the spec's Testing Decisions

## Comments

Implemented in `internal/composes` (`internal/migrations/00006_composes.sql` +
`internal/composes/composes.go`), following the plural-package and
Service/repository precedent from `internal/environmentvariables`. Composes
use integer ids, belong to an Environment with `ON DELETE CASCADE`, and
enforce a case-insensitive unique name per Environment (`COLLATE NOCASE`,
matching Project/Environment name uniqueness). Authenticated routes support
list, create, get, and update at the nested Project → Environment → Compose
path; update only accepts `yamlContent` (the ticket's own wording — "editing
and saving the YAML" — the name is fixed at creation, consistent with no
rename requirement in this ticket's scope). Delete, deploy, status, and logs
are explicitly out of scope here (tickets 08/11/12).

The Project detail UI adds a third "Composes" sub-tab (alongside Overview
and Environment Variables) listing Name / a static "Not deployed" status Tag
/ Last updated, with row-click navigation into a Compose detail view. The
detail view has a single "General" tab (the eventual "Logs" tab is ticket
12's job) embedding a locally-bundled Monaco editor (no CDN — self-hosted
consistent with the panel's own design) scoped to just the `yaml` basic
language to avoid dragging in Monaco's ~70 other bundled languages. The
`New Compose` modal also embeds Monaco so an operator can paste YAML at
creation time, per spec user story 17. `EnvironmentVariables.dc.html` /
`ComposeGeneral.dc.html` mockups were not present, so the UI follows the
existing Ant Design + `DESIGN.md` tokens (code font stack, border tokens).

HTTP-seam tests use real SQLite migrations and `httptest`, covering create,
list, get, update, duplicate/case-insensitive name conflicts, missing-name
validation, missing-compose-on-update, and parent scoping (missing project,
missing environment, environment under another project, and — added after
`/code-review` flagged the gap — a Compose fetched through a sibling
Environment in the same Project). Verified with `pnpm --dir web typecheck`,
`pnpm --dir web build`, `go build ./...`, `go vet ./...`, `go test -race
./...`, and `git diff --check`. Also smoke-tested the full flow in a real
browser (admin setup → login → create Project → create Compose with pasted
YAML → edit and save YAML → verify the list reflects the update) with no
console errors.

`/code-review` against `408df92` found no hard violations on either axis.
Standards flagged only accepted judgment calls (duplicated parent-existence
helpers, consistent with ADR-0003's explicit rejection of a shared layer at
this app's size, already duplicated across `environments`/
`environmentvariables`). Spec flagged one test-coverage gap (see above,
since fixed) and confirmed no scope creep — no delete/rename/deploy
plumbing was added.
