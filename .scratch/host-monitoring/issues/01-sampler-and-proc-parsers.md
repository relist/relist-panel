# 01 — Sampler and `/proc` parsers

**What to build:** The new `internal/hostmetrics` package: pure parsers for the four `/proc` files,
the ring buffer, and the sampling loop. One goroutine with a single `time.Ticker` at the host's 10s
cadence; the 30s container sweep, the minute-close rollup and the 5-minute `docker system df` refresh
run when their turn comes round. Disk space comes from `statfs` on the data dir; network counters
from `/host/proc/1/net/dev` (never `/host/proc/net/dev` — that is `/proc/self/net/dev`, see
ADR-0006). Counters are cumulative, so what is stored is a delta rate; the first tick has no rate and
reports nothing rather than zero.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Parsers are pure functions over fixture text, tested with golden files for `/proc/stat`,
      `/proc/meminfo`, `/proc/diskstats` and `/proc/net/dev`, including counter-wrap and
      freshly-booted/zero-delta cases
- [ ] The ring keeps the last hour (360 samples) and slices any window from it without allocation
      churn; the rollup average is computed from the samples a minute actually saw
- [ ] A source that cannot be read lands in the `sources` map as unavailable with a reason; the sample
      itself stays intact, so one missing source never blanks the screen
- [ ] Nothing reads `/proc` at package init or in a constructor: the loop takes a root path
      (`/host/proc` in production, a `t.TempDir()` fixture in tests) so no test touches the host
- [ ] `go test ./...`, `go vet ./...`, `gofmt` clean

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
