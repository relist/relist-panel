# Spec — Deploy identity is stored, not recomputed

## Problem

A Compose's deploy identity — its on-disk path `dataDir/<project-slug>/<env-slug>/<compose-slug>/`
and its `docker compose -p` project name `<p>-<e>-<c>` (ADR-0002) — is currently a pure function of
the **current** Project and Environment names, recomputed by every lifecycle action
(`runRequestFor`, internal/composes/composes.go:851). Parent names are mutable, deployed containers
are not. After a Project or Environment rename:

- `delete` stats the renamed path, misses, and skips `DownWithVolumes` — orphaned containers keep
  running with no DB row (composes.go:450).
- `stop` / `restart` / `logs` return 400 "compose has not been deployed" (composes.go:551).
- `currentStatus` reports a running stack as Stopped (composes.go:601).

A second defect in the same family: slug uniqueness is enforced only at the Compose level.
Project names are unique by name (NOCASE), not by slug — "My App" and "my-app" produce the same
`dataDir/` segment and can collide across Projects. `slugify`'s `"x"` fallback widens this.

## Decision (ADR-0005)

Deploy identity is **immutable data frozen at Compose creation**. The resolved slugs are stored on
the Compose row; lifecycle actions **read** identity instead of recomputing it. Renames never
propagate to deployed stacks (propagating would require moving files and re-pointing running
containers — far outside v1 scope per ADR-0001). Parent slugs become unique
(Project: globally; Environment: within its Project) so two Composes can never share a deploy
identity across parents.

## Tickets

- `01-stored-identity.md` — store project/env slugs on the Compose row; all lifecycle actions read them. (No blockers.)
- `02-parent-slug-uniqueness.md` — 409 on slug collisions at Project (global) and Environment (within Project) create/rename. (No blockers.)

## Testing decisions

HTTP-seam tests in the external `composes_test` style per ADR-0003: real SQLite migrations,
`httptest` requests, the recording `fakeComposeRunner`. Rename-safety is the headline test:
deploy → rename parent → every lifecycle action still targets the original identity.
