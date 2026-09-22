# 02 — Parent slug uniqueness (Project global, Environment within Project)

**What to build:** Creating or renaming a Project/Environment whose *slug* collides with a sibling
returns 409, so two Composes can never resolve to the same deploy identity across parents
(ADR-0005). Project slug uniqueness is global; Environment slug uniqueness is within its Project
(the data-dir path is `<project-slug>/<env-slug>/<compose-slug>`).

**Blocked by:** —

**Status:** closed

- [x] Project create and update reject a name whose `slugify` collides with any other Project's slug → 409 "a project with a conflicting name already exists"
- [x] Environment create rejects a slug collision within the same Project → 409 "an environment with a conflicting name already exists" — ~~and update~~: Environments have no update/rename endpoint (list/create/delete only), so create is the only write to enforce
- [x] Enforced in Go (mirroring the existing compose sibling-slug check at composes.go:170), not by a new DB index — legacy rows are backfilled by ticket 01's startup step
- [x] HTTP-seam tests: "My App" then "my-app" → 409 at both levels; rename into a collision → 409; rename to a non-colliding name still succeeds
- [x] Covered by the full suite: `make test`, `go test -race ./...`, `go vet ./...`

## Comments

Implemented on `deepen-deploy-identity`. `slugify` moved verbatim to a new `internal/slug` package
(`slug.Slugify`) so projects/environments consume it without importing composes (ADR-0003); the
compose call sites were switched over with identical behavior. Project create and rename now list
the projects and 409 when any *other* project's slug equals the candidate's (global scope; the
rename check excludes the project's own row, so "My App" → "my-app" succeeds). Environment create
does the same against its project's environments only. Both mirror the compose sibling-slug
loop — no new index or migration; legacy Compose rows stay covered by ticket 01's backfill.

One test adaptation: since every Project is provisioned with a default `production` environment,
the literal "Production" then "production" sequence would hit the pre-existing NOCASE *name*
constraint (different 409 message), and "production" in a second project can never return 201.
The environment test therefore isolates the slug path with slug-only variants — "Production!!!"
vs the default, "Staging" then "staging!!!" (409, new message asserted), "staging!!!" in another
project (201, scope asserted). Project tests cover create "My App"/"my-app" → 409, "???" then
"!!!???" → 409 (the `"x"` fallback), rename into a collision → 409, rename to a free name → 200,
and rename to the project's own slug variant → 200.

Verified TDD-first: all three new tests failed with the pre-change statuses (201/200) and pass
after. Full suite green: `make test`, `go test -race ./...` (42 tests, 0 failures),
`go vet ./...`, `go build`, `pnpm --dir web typecheck`, `git diff --check`. Commits:
`44c9cd1` (refactor: extract slugify to internal/slug), `7e4e6d4` (feat: enforce slug uniqueness
for projects and environments).
