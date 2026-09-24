# 04 — Metrics endpoints and the sampler's lifecycle

**What to build:** Three endpoints, registered on the same mux as everything else so the existing auth
middleware covers them: `GET /api/host/metrics?window=1h`,
`GET /api/projects/{p}/environments/{e}/composes/{c}/services/{service}/metrics?window=1h`, and
`GET /api/projects/{p}/environments/{e}/composes/{c}/services` (the rail's and the Monitor tab's
facts). Windows are the design's four; each is served at its natural resolution — the 10s ring up to
an hour, the 1-minute rollups beyond — and downsampling stays in the chart. Every metrics response
carries a `sources` block naming what is readable and why not. The sampler starts in `main.go` and is
cancelled on shutdown.

**Blocked by: 01, 02, 03**

**Status:** closed

- [x] An invalid window is a 400 with a message, not a silent default
- [x] The host response is served from the ring for ≤1h and from rollups for 6h/24h, and says which
      (`partial` when the data is shorter than the window, e.g. after a restart)
- [x] `sources` reports an unmountable `/proc` as unavailable with a reason, so the screen renders
      `unavailable` rather than zeros
- [x] The Services endpoint is served from the collector's cache and returns name, image, state,
      health, ports and container ID per Service, for a Compose that exists and 404 otherwise
- [x] Sampler lifecycle: started once in `main.go`, cancelled on shutdown, and a second start is not
      possible (no duplicate goroutines)
- [x] HTTP-seam tests with an injected sampler fake; `go test ./...`, `go vet ./...` clean

## Comments

Implemented in this ticket's single `feat:` commit on `impl/host-monitoring` (see the branch log).
Verified: `go test ./...` green, `go test -race` on hostmetrics/metrics/httpapi/dockerexec clean,
`go vet ./...` clean, `gofmt -l` clean; no new dependencies in `go.mod`. Also verified against the
real binary: a bare run (no `/host/proc` mounted, no daemon reachable for df) serves the ring window
with `partial: true` and a fully-honest `sources` block, returns 400 with a message for `window=2h`,
serves `[]` for a known-but-empty Compose, and exits on SIGTERM after draining the sampler.

**The handed-over decision — NaN on the wire is `null`.** JSON has no NaN and Go's encoder refuses to
write it, so every metric field marshals through a `nullFloat` that turns NaN into JSON `null`. A
missing reading arrives as the absence of a point — the only encoding that cannot masquerade as a
zero — and the screens already know how to draw gaps. Documented on the type in
`internal/metrics/wire.go`.

Response shapes (envelope + samples):

- Every metrics response: `{window, from: "ring"|"rollups", intervalSeconds, partial, sources,
  samples}` — `partial` is computed server-side as "fewer samples than the window implies", which is
  exactly the screens' "collecting — N of M minutes".
- Host samples follow the design contract's `MetricSample` (epoch-ms `at`, `cpu`, `memUsed`,
  `memTotal`, `diskUsed`, `diskTotal`, `dockerUsed`, `dockerTotal`, `blkRead`, `blkWrite`, `netRx`,
  `netTx`); Service samples follow its `ServiceSample` (`cpu`, `memUsed`, `memLimit`, `netRx`,
  `netTx`, `blkRead`, `blkWrite`, `pids`). Source keys go camelCase at the wire (`diskSpace`,
  `blockIo`, `dockerDisk`); the Service side's single source is `dockerStats` (the collector's
  sweep outcome).
- Service metrics endpoints never 404 a Service: a Service the collector never swept in a known
  Compose gets an empty `partial` window (its rollups are its past). Only an unknown Compose is 404.
- `dockerTotal` is emitted as the statfs total of the data dir's device — the design mock's own
  denominator ("Docker usage as a share of the disk it sits on"). Honest on the reference install
  where `/var/lib/docker` shares `/`'s device; a host with Docker on another device would show a
  percent relative to the wrong disk, which the ADR-0002 note already flags as install-specific.

Decisions taken where the ticket left room:

- **`SweepInterval` (30s) is now a hostmetrics constant**, used by the loop's due counter, the
  collector's history sizing, and the endpoints' Service window math — one number for the sweep
  cadence that all three previously assumed.
- **Per-Service history ring lands here** (the read side ticket 02 deferred): each Service keeps its
  last hour of sweeps (120 samples) for the ≤1h windows. It is one series across redeploys, and it
  **outlives the container leaving the listing** — a stopped Service's last hour is still its
  history, and the rollups continue the story beyond it. Per-key memory is bounded by capacity; the
  set of keys is bounded by what the panel has ever seen deployed (accepted for v1; a
  compose-deletion hook could reap it later).
- **Docker disk needed its source**, or the ticket 03 column would stay NULL forever: the sampler
  gained `SetDockerDisk(used, err)` plus carry-forward (the series is a step; a failed refresh marks
  the source unavailable with its reason while the last reading stays on the chart), and
  `dockerexec` gained `SystemDf` — `GET /system/df`, summed like `docker system df`: deduplicated
  image layers + containers' writable layers + volumes + build-cache records — wired to the
  5-minute `RefreshDockerDisk` hook in `main.go`.
- **Lifecycle**: `Sampler.Start` is a compare-and-swap (a second call returns an error, so no wiring
  mistake can duplicate the loop) and `Sampler.Wait` gives shutdown a drain point. `main.go` uses
  `signal.NotifyContext`, `Shutdown`s the server on SIGINT/SIGTERM, then cancels and waits out the
  sampler — including any sweep still in flight.
- The collector's `Status()` now reports "not swept yet" before the first sweep instead of
  `available: true` by default — found against the live binary, where the empty default leaked into
  the Service metrics `sources` block.
- `composes.Compose.DeployProjectName()` is exported (and `runRequestFor` now uses it) so the
  `<p>-<e>-<c>` naming rule lives in exactly one place; the endpoints resolve a request path to the
  frozen identity through it (ADR-0005).
- The `docker-compose.yml` read-only `/host/proc` mount (ADR-0006) remains untouched for the
  orchestrator, as handed over by ticket 01.

Self-review findings from the diff walk, fixed before committing:

- `main.go`'s first shutdown draft raced an early signal: a SIGTERM landing before
  `ListenAndServe` began serving would return from `Shutdown` and then serve forever with the
  sampler already dead. Rewritten as a `select` over serve-error and `ctx.Done()`, with the drain
  (`stop(); sampler.Wait()`) on both paths.
- The `DockerDiskSource` port I drafted for the df fetch had no consumer — the sampler's
  `RefreshDockerDisk` hook is the seam, and ADR-0003's rule is ports consumers actually swap.
  Deleted; the concrete source is called directly in the wiring, like `dockernet`.
