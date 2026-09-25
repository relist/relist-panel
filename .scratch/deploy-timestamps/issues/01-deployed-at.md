# 01 — `deployed_at` on the Compose row, written on deploy success

**What to build:** Migration `00009_deploy_timestamps.sql` adding `deployed_at INTEGER` (nullable,
no default) to `composes`. The deploy handler (`internal/composes/composes.go`, the `deploy`
service method) writes the column **only when a deploy succeeds** — after the runner returns
without error, not before — with the same repository-owns-SQL discipline the package already has.
The Compose wire payload gains `lastDeployedAt` (`time.Time`, zero-as-null or pointer — pick what
keeps "never deployed" indistinguishable from "deployed at the epoch" on the wire; the frontend
treats absent as never). Stop, restart, save and delete never touch the column.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Migration follows the repo idiom (5-digit sequence, goose `Up`/`Down`) and is idempotent
      under `goose.Up` on an already-migrated database; `database_test.go` asserts version 9
- [ ] A failed deploy leaves `deployed_at` untouched (the write happens on success only)
- [ ] `GET` of a Compose that never deployed omits/nulls `lastDeployedAt`; one that deployed
      returns the timestamp, and it survives a panel restart
- [ ] Existing rows backfill to NULL — never-deployed stays never-deployed
- [ ] `go test ./...`, `go vet ./...`, gofmt clean; the write is covered at the repository seam
      the package already tests at

## Comments

Cut 2026-09-25 from the grill over the implementation's leftovers: the session-stamp workaround
(`deployedAt` map in the store) retires in ticket 02 once this lands.
