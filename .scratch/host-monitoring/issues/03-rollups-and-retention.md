# 03 — Rollups and retention

**What to build:** Migration `00008_metrics.sql` with one rollup table per scope — host and Service
carry different fields, so each gets the columns it uses (host: cpu, memory, disk, docker disk, block
I/O, network; service: cpu, memory used/limit, network, block I/O, pids) — keyed on
`bucket_at INTEGER` (Unix seconds), with indices for the window queries. The writer runs when a
minute closes, averaging over the samples that minute saw. The retention delete drops rows older than
the longest window plus slack and is the panel's first housekeeping job. Bump
`internal/database/database_test.go`'s latest-migration assertion.

**Blocked by: 01, 02**

**Status:** closed

- [x] Migration follows the repo's idiom (5-digit sequence, goose `Up`/`Down`, `INTEGER` Unix seconds)
      and is idempotent under `goose.Up` on an already-migrated database
- [x] A minute that a restart cut short still writes a row, because the screens' `partial` state is the
      honest display for the gap
- [x] Retention test on a real migrated SQLite: old rows go, the window stays intact
- [x] `database_test.go` asserts the new version
- [x] The write is one small statement per minute per scope, and it does not hold the single
      connection long enough to delay an API read (the panel runs `SetMaxOpenConns(1)`)

## Comments

Implemented in this ticket's single `feat:` commit on `impl/host-monitoring` (a ticket file cannot
cite its own commit's hash; see the branch log). New package `internal/metrics`: the rollup `Store`
(`WriteHostRollup`, `WriteServiceRollups`, `DeleteRollupsBefore`), the minute-close `Recorder`, and
their tests on real migrated SQLite. The sampling loop itself is untouched except for two field
additions it must carry. Verified: `go test ./...` green, `go vet ./...` clean, `gofmt -l` clean; no
new dependencies in `go.mod`.

What landed where:

- `00008_metrics.sql`: `host_metric_rollups` keyed on `bucket_at` (the primary key serves the window
  queries) and `service_metric_rollups` keyed on `(compose_project, service, bucket_at)` with a
  `bucket_at` index for the retention delete, which drops by age across every Service. `Down` drops
  both tables. Metric columns are nullable: a field no sample carried has no honest average.
- The Service side of the minute close lives in the collector, which owns the sweep stream: each
  completed sweep appends its samples to a pending buffer, and `CloseMinute()` averages them per
  Service (NaN fields drop out, the host's `averageField` generalized over both sample types) and
  reopens the minute. Failed stats calls keep the previous sample and contribute nothing to a minute.
- `Recorder.OnMinuteClose` (the sampler's hook, wired in ticket 04) writes the host row, drains the
  collector's pending sweeps into Service rows, then runs retention — logging failures, never
  panicking the loop. Retention horizon is **25h** (the 24h window plus an hour of slack), run on
  every minute close: an indexed range delete over a bounded table, cheap enough to never schedule.

Decisions taken where the ticket left room:

- **NaN stores as NULL**, column-level: "never measured this minute" and "measured zero" stay
  distinguishable in SQL as they are in memory. Writes are `INSERT OR REPLACE`, so a wall-clock
  minute written twice (a restart landing in the same bucket, or the ticker's close window drifting
  across a minute boundary) replaces the earlier row instead of failing the write.
- **Pids** needed a source, not just a column: `ContainerStatsSample` gains `PidsCurrent` (raw
  `pids_stats.current` from the same stats call ticket 02 already makes, parsed in dockerexec), and
  `ServiceSample` gains `Pids`. A live container always holds at least one PID, so a reported 0
  means "the runtime reported nothing" and reads NaN — same rule as the other unreadable gauges.
- **Docker disk** is a rollup column and a `HostSample` field (`DockerDiskBytes`, averaged by
  `MinuteAverage` like the rest), but the source that fills it — the 5-minute `docker system df`
  refresh carrying its value forward between refreshes — is ticket 04's wiring. Until its first
  success the column is NULL, which is the honest value.
- A rollup's identity fields (name/image/state/health) are not persisted; the Service tables store
  metric columns only, so a Service's history never pins a stale container's identity.
- The single-connection checkbox is met by construction (one small `INSERT OR REPLACE` per row on
  the primary key, the Service batch in one short transaction, no write ever issued while a row
  cursor is open — the same deadlock rule `BackfillLegacyIdentities` documents) and exercised
  implicitly: the recorder tests write and read through `OpenAndMigrate`'s `SetMaxOpenConns(1)` handle.

Self-review finding from the diff walk, fixed before committing: `serviceMinuteAverages` grouped
samples through a needless one-field struct; replaced with a plain `map[serviceKey][]ServiceSample`.
