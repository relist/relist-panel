# 02 — Parent slug uniqueness (Project global, Environment within Project)

**What to build:** Creating or renaming a Project/Environment whose *slug* collides with a sibling
returns 409, so two Composes can never resolve to the same deploy identity across parents
(ADR-0005). Project slug uniqueness is global; Environment slug uniqueness is within its Project
(the data-dir path is `<project-slug>/<env-slug>/<compose-slug>`).

**Blocked by:** —

**Status:** open

- [ ] Project create and update reject a name whose `slugify` collides with any other Project's slug → 409 "a project with a conflicting name already exists"
- [ ] Environment create and update reject a slug collision within the same Project → 409 "an environment with a conflicting name already exists"
- [ ] Enforced in Go (mirroring the existing compose sibling-slug check at composes.go:170), not by a new DB index — legacy rows are backfilled by ticket 01's startup step
- [ ] HTTP-seam tests: "My App" then "my-app" → 409 at both levels; rename into a collision → 409; rename to a non-colliding name still succeeds
- [ ] Covered by the full suite: `make test`, `go test -race ./...`, `go vet ./...`

## Comments
