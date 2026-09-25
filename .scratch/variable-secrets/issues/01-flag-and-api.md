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

**Status:** ready-for-agent

- [ ] Migration follows the repo idiom and is idempotent; the backfill only ever runs in it (no
      Go-side guessing after migration)
- [ ] An existing `JWT_SECRET` row comes back `secret: true` after migration; a `MONKEY_PATCH`
      row comes back `secret: false`
- [ ] Create and update persist the flag; the validation (key pattern, size) is unchanged
- [ ] The API never logs a variable's value — if any handler path does today, fix it and pin it
      with a test that captures the log output
- [ ] `go test ./...`, `go vet ./...`, gofmt clean; repository-seam tests cover the round-trip
      and the backfill against a real migrated SQLite

## Comments

Cut 2026-09-25 from the grill: display tier only — the value stays stored and served as today;
nothing downstream (deploy interpolation, `docker compose` env) changes.
