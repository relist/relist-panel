# 01 — Enable foreign keys on every SQLite connection

**What to build:** `PRAGMA foreign_keys = ON` is run once, on the first connection, in
`database.Migrate` (`internal/database/database.go:42`). modernc.org/sqlite discards a connection
whose query was interrupted by a canceled context (`ResetSession` → `ErrBadConn` when
`sqlite3_is_interrupted`), and `database/sql` opens a replacement with `foreign_keys=0` that stays
for the life of the process. Every `ON DELETE CASCADE` (22 of them) then stops firing silently:
deleting a Project leaves Environments, Composes, Variables, Domains and Deployment snapshots
behind. Put the pragma in the DSN so every connection gets it.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] DSN becomes `file:<path>?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`
      (verified: after five interrupted queries every replacement connection reports `foreign_keys=1`)
- [ ] At boot, after migrations, run `PRAGMA foreign_key_check` and log any violation found (do not
      delete rows; the operator decides)
- [ ] Test: a query canceled mid-run (recursive CTE under a 20 ms context) leaves
      `PRAGMA foreign_keys` at 1, and deleting a Project afterwards leaves no Environment rows
- [ ] Test: a connection forced out of the pool (`SetMaxIdleConns(0)`) is replaced with
      `foreign_keys=1`

## Source

Stability review, item B5 (`.scratch/stability-review/spec.md`). Reproduction:
`.scratch/stability-review/verification/database_review_verify_test.go`. The live database had no
violations on 2026-10-24.
