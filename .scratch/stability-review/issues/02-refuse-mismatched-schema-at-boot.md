# 02 — Refuse to boot against a schema this binary does not match

**What to build:** goose v3.24.3 returns nil when the database is at a version higher than any
embedded migration ("no migrations to run"), and it records only `version_id` — no file name or
checksum — so a second branch's `00020_*` is skipped when another branch's `00020_*` was applied.
The panel then boots and fails later with `no such column` or constraint errors. Running feature
branches on the live install makes both cases reachable. Make `OpenAndMigrate` refuse them.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] After `goose.Up`, compare `SchemaVersion` with `KnownVersion` (both already in
      `internal/database/version.go`); a higher schema returns an error naming both versions and
      what to do (run the binary that applied it, or restore a backup). `main` already `log.Fatal`s
      on the error; `create-admin` and `reset-2fa` share `OpenAndMigrate` and so refuse too
- [ ] Record each applied migration's file name and SHA-256 in a new table through a **new**
      migration (never edit a committed one); at boot, an applied version whose recorded name/hash
      differs from the embedded file is refused with both names. Versions applied before the table
      existed are backfilled from the embedded files on first boot
- [ ] Tests: a database one version ahead is refused; an applied version recorded with a different
      file name is refused; a normal upgrade from 17 and from 19 still boots

## Source

Stability review, item A1 (`.scratch/stability-review/spec.md`). Reproduction:
`.scratch/stability-review/verification/database_review_verify_test.go`. The live database was at
version 17 on 2026-10-24.
