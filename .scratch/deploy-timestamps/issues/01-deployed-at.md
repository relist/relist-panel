# 01 — `deployed_at` on the Compose row, written on deploy success

**What to build:** Migration `00009_deploy_timestamps.sql` adding `deployed_at INTEGER` (nullable,
no default) to `composes`. The deploy handler (`internal/composes/composes.go`, the `deploy`
service method) writes the column **only when a deploy succeeds** — after the runner returns
without error, not before — with the same repository-owns-SQL discipline the package already has.
The Compose wire payload gains `lastDeployedAt` (`time.Time`, zero-as-null or pointer — pick what
keeps "never deployed" indistinguishable from "deployed at the epoch" on the wire; the frontend
treats absent as never). Stop, restart, save and delete never touch the column.

**Blocked by: —**

**Status:** closed

- [x] Migration follows the repo idiom (5-digit sequence, goose `Up`/`Down`) and is idempotent
      under `goose.Up` on an already-migrated database; `database_test.go` asserts version 9
- [x] A failed deploy leaves `deployed_at` untouched (the write happens on success only)
- [x] `GET` of a Compose that never deployed omits/nulls `lastDeployedAt`; one that deployed
      returns the timestamp, and it survives a panel restart
- [x] Existing rows backfill to NULL — never-deployed stays never-deployed
- [x] `go test ./...`, `go vet ./...`, gofmt clean; the write is covered at the repository seam
      the package already tests at

## Comments

Cut 2026-09-25 from the grill over the implementation's leftovers: the session-stamp workaround
(`deployedAt` map in the store) retires in ticket 02 once this lands.

Implemented in 27de143. The write is `repository.markDeployed`, called from the
deploy handler only after `runner.Up` returns without error, then the row is re-read so the deploy
response itself carries the fresh timestamp; `list`/`findByID`/`scanComposeValues` grew the column
and a `sql.NullInt64` → `*time.Time` scan.

- Wire choice: `LastDeployedAt *time.Time` with no `omitempty` — the key is always present, `null`
  when never deployed, so "never deployed" (null) can never be confused with "deployed at the Unix
  epoch" (`"1970-01-01T00:00:00Z"`), and the payload shape stays stable. Noted per the ticket's
  instruction to record the pick.
- A failure to persist the stamp after a successful `Up` answers 500 ("could not record deploy
  time") rather than answering 200 with a missing timestamp — practically unreachable while the
  SQLite writes around it succeed, and honest when it is not.
- Verification: `go test ./...` (with `pnpm --dir web build` first — dist unchanged by this
  ticket), `go vet ./...`, gofmt clean. New coverage: `TestComposeDeployRecordsDeployedAtOnSuccessOnly`
  (HTTP seam: never-deployed null on wire and row, failed deploy untouched, success stamps
  response/GET/row, stop/restart/save leave it, and a fresh handler over the same DB — a panel
  restart — still serves it) and `TestDeployTimestampsMigrationBackfillsExistingRowsToNull`
  (`database_test.go`: goose `UpTo` 8, seed a row, migrate to 9, assert NULL); the suite's
  latest-version assertion now reads 9.
