# 01 — The `secret` flag: column, backfill, wire

**What to build:** Migration `00009_deploy_timestamps.sql` is taken; this feature's migration is
`00010_variable_secrets.sql` (take the next free number if that one has moved): add
`secret INTEGER NOT NULL DEFAULT 0` to `environment_variables`, and backfill existing rows once
with the SQL translation of the UI's heuristic — keys matching
`secret|password|passwd|token|credential|(^|_)(api_?)?key($|_)|(^|_)private_?key`
case-insensitively — expressed as `LIKE` patterns. The create/update input
(`internal/environmentvariables`) accepts an optional `secret` boolean (absent = false), the
repository persists it, and the list/detail wire payloads return `secret` per variable. Bump
`database_test.go`'s latest-version assertion.

**Blocked by: —**

**Status:** closed

- [x] Migration follows the repo idiom and is idempotent; the backfill only ever runs in it (no
      Go-side guessing after migration)
- [x] An existing `JWT_SECRET` row comes back `secret: true` after migration; a `MONKEY_PATCH`
      row comes back `secret: false`
- [x] Create and update persist the flag; the validation (key pattern, size) is unchanged
- [x] The API never logs a variable's value — if any handler path does today, fix it and pin it
      with a test that captures the log output
- [x] `go test ./...`, `go vet ./...`, gofmt clean; repository-seam tests cover the round-trip
      and the backfill against a real migrated SQLite

## Comments

Cut 2026-09-25 from the grill: display tier only — the value stays stored and served as today;
nothing downstream (deploy interpolation, `docker compose` env) changes.

Implemented in this commit (`feat: add the per-variable secret flag`). Written test-first: the
new external-package seam tests (`internal/environmentvariables/service_test.go`, per the
ADR-0003 black-box rule) failed against the unmodified service before the migration and wire
changes landed.

- The heuristic is translated as `lower(key) GLOB …` rather than `LIKE`: GLOB treats `_` as a
  literal, which is what makes the key family's `(^|_)…($|_)` word boundaries expressible without
  ESCAPE clauses, and on `lower(key)` its case-sensitivity is exactly the pattern's `/i` over the
  ASCII-only keys validation allows. `HONEYPOT_KEY` and `APIKEY_VAULT` mark; `MONKEY`,
  `MONKEY_PATCH` and `KEYCLOAK_URL` stay plain (the ticket named LIKE patterns; GLOB is the same
  idea with exact boundaries — noted as a deliberate deviation).
- The backfill test builds the pre-flag world with `goose.UpTo(…, 8)`, seeds rows without the
  column, then `database.Migrate` applies 00010 across the deliberately skipped 00009 — so the
  gap's harmlessness is pinned, not assumed. `database_test.go` asserts latest version 10.
- Logging: no handler path logs values today (the only `log.Printf` in the API surface is
  httpapi's healthz encode error); `TestVariablesAPIDoesNotLogValues` captures the standard
  logger across create/list/update plus rejected-body and missing-parent paths, with a sentinel
  value and a guard that the create actually ran.
- Verified: `pnpm --dir web build` (dist unchanged — no frontend files in this ticket), then
  `go test ./...`, `go vet ./...`, `gofmt -l` all clean.
