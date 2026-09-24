# 03 — Rollups and retention

**What to build:** Migration `00008_metrics.sql` with one rollup table per scope — host and Service
carry different fields, so each gets the columns it uses (host: cpu, memory, disk, docker disk, block
I/O, network; service: cpu, memory used/limit, network, block I/O, pids) — keyed on
`bucket_at INTEGER` (Unix seconds), with indices for the window queries. The writer runs when a
minute closes, averaging over the samples that minute saw. The retention delete drops rows older than
the longest window plus slack and is the panel's first housekeeping job. Bump
`internal/database/database_test.go`'s latest-migration assertion.

**Blocked by: 01, 02**

**Status:** ready-for-agent

- [ ] Migration follows the repo's idiom (5-digit sequence, goose `Up`/`Down`, `INTEGER` Unix seconds)
      and is idempotent under `goose.Up` on an already-migrated database
- [ ] A minute that a restart cut short still writes a row, because the screens' `partial` state is the
      honest display for the gap
- [ ] Retention test on a real migrated SQLite: old rows go, the window stays intact
- [ ] `database_test.go` asserts the new version
- [ ] The write is one small statement per minute per scope, and it does not hold the single
      connection long enough to delay an API read (the panel runs `SetMaxOpenConns(1)`)

## Comments
