# 01 — Sampler and `/proc` parsers

**What to build:** The new `internal/hostmetrics` package: pure parsers for the four `/proc` files,
the ring buffer, and the sampling loop. One goroutine with a single `time.Ticker` at the host's 10s
cadence; the 30s container sweep, the minute-close rollup and the 5-minute `docker system df` refresh
run when their turn comes round. Disk space comes from `statfs` on the data dir; network counters
from `/host/proc/1/net/dev` (never `/host/proc/net/dev` — that is `/proc/self/net/dev`, see
ADR-0006). Counters are cumulative, so what is stored is a delta rate; the first tick has no rate and
reports nothing rather than zero.

**Blocked by: —**

**Status:** open

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

## Comments
