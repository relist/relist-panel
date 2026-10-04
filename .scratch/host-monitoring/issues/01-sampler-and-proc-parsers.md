# 01 — Sampler and `/proc` parsers

**What to build:** The new `internal/hostmetrics` package: pure parsers for the four `/proc` files,
the ring buffer, and the sampling loop. One goroutine with a single `time.Ticker` at the host's 10s
cadence; the 30s container sweep, the minute-close rollup and the 5-minute `docker system df` refresh
run when their turn comes round. Disk space comes from `statfs` on the data dir; network counters
from `/host/proc/1/net/dev` (never `/host/proc/net/dev` — that is `/proc/self/net/dev`, see
ADR-0006). Counters are cumulative, so what is stored is a delta rate; the first tick has no rate and
reports nothing rather than zero.

**Blocked by: —**

**Status:** closed

- [x] Parsers are pure functions over fixture text, tested with golden files for `/proc/stat`,
      `/proc/meminfo`, `/proc/diskstats` and `/proc/net/dev`, including counter-wrap and
      freshly-booted/zero-delta cases
- [x] The ring keeps the last hour (360 samples) and slices any window from it without allocation
      churn; the rollup average is computed from the samples a minute actually saw
- [x] A source that cannot be read lands in the `sources` map as unavailable with a reason; the sample
      itself stays intact, so one missing source never blanks the screen
- [x] Nothing reads `/proc` at package init or in a constructor: the loop takes a root path
      (`/host/proc` in production, a `t.TempDir()` fixture in tests) so no test touches the host
- [x] `go test ./...`, `go vet ./...`, `gofmt` clean

## Parser decisions

Settled while starting this ticket (2026-09-24), so they are not re-derived from scratch:

- **CPU** is busy/total jiffies from the aggregate `cpu` line of `/proc/stat`, with idle + iowait as
  idle and `guest`/`guest_nice` left out — the kernel already counts them inside `user` and `nice`.
- **Memory** is `MemTotal − MemAvailable`, not `MemTotal − MemFree`: availability is what makes the
  number agree with `free` and `htop`, because reclaimable page cache is not memory in use.
- **Block I/O** totals whole physical devices only (`sdX`, `vdX`, `hdX`, `nvmeXnY`, `mmcblkX`, and
  never their partitions), skipping `loop`, `ram`, `zram` and device-mapper. A partition and the disk
  under it, plus the dm layers above them, each report the same bytes; summing everything would
  multiply the rate. Sector counts are 512 bytes by definition, whatever the device's logical sector
  size is.
- **Network** totals every interface except `lo`, so local traffic is not counted twice.
- Every parser **errors rather than returning zeros** when its file is unreadable or nothing matched.
  That is what keeps "the panel cannot read this source" distinguishable from "the host is idle", and
  it is what the `sources` map is built from.

## Comments

Implemented in this ticket's single `feat:` commit on `impl/host-monitoring` (a ticket file cannot
cite its own commit's hash; see the branch log). Package: `internal/hostmetrics` (`proc.go` parsers,
`ring.go`, `hostmetrics.go` sampler) with golden fixtures under `testdata/`. Verified:
`go test ./...` (hostmetrics 25 tests green, rest of repo unaffected), `go vet ./...` clean,
`gofmt -l` clean; no new dependencies in `go.mod`.

Self-review findings from the diff walk, both fixed before committing:

- `Ring.AppendLast(n)` returned the *oldest* n samples instead of the newest — the window slice now
  starts at `filled − n`, so `Window(1)` is the latest tick, not the first.
- A failed source left its gauge fields at Go's zero value — a zeroed line wearing a real
  measurement's clothes. Each tick now starts from an all-NaN sample (`nanSample`) and overwrites
  only what it actually read, so an unreadable source is NaN, never 0.

Decisions taken where the ticket left room (settled from the spec, noted here per the working
agreement):

- "No rate" is represented as **NaN**, uniformly for rates (first tick, counter wrap/reset) and for
  gauges of unreadable sources; `MinuteAverage` drops NaN fields so a first tick without a CPU rate
  cannot drag the rollup toward zero. Wire encoding of NaN (e.g. `null`) is left to ticket 04.
- **Counter wrap** (cur < prev) reports no rate for that tick rather than guessing a counter width;
  an equal counter is a real zero (the host was idle) — both are covered by dedicated tests.
- The `sources` map keys are `cpu`, `memory`, `disk_space`, `block_io`, `network`; the map is seeded
  "not sampled yet" before the first tick and is a snapshot copy per read.
- Ring capacity derives from the interval (`1h / 10s = 360`), and the sweep/minute/df due-counters
  are rounded from the same interval, so both stay correct if the cadence is ever tuned.
- In this ticket the three due jobs fire as synchronous hooks; making the 30s container sweep not
  block the 10s tick is ticket 02's checkbox and lands there.
- The `docker-compose.yml` read-only `/host/proc` mount (ADR-0006) is a deployment change no
  checkbox here covers — left for the wiring ticket (04) or a human.

