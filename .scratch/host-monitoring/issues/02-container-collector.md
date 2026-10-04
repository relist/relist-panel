# 02 — Container collector

**What to build:** The `ContainerSource` port and its real implementation. Declared in
`hostmetrics` (the consumer, per ADR-0003 as refined by ADR-0006) and implemented in `dockerexec`
over the Docker socket: `GET /containers/json` once per sweep (0.01s, 16 KB) for every container's
labels, state, ports and status text, then `GET /containers/{id}/stats?stream=false` per container
(~1.97s wall, no CPU, 1.9 KB) concurrently. Raw API numbers only — never the CLI's formatted
`--format json` strings. Service identity comes from the Compose labels (`com.docker.compose.project`
is ADR-0005's frozen `<p>-<e>-<c>`), so a container is attributed to a Service, not to a container ID.
Health is parsed from the container's status text, so no `docker compose ps` process is ever spawned.

**Blocked by: 01**

**Status:** closed

- [x] A fake `ContainerSource` drives the tests; no test talks to a real daemon
- [x] CPU, network and block I/O are stored as deltas; a container's first sweep reports no rate
- [x] Memory used is `usage − cache`; the limit is the container's own
- [x] **Redeploy continuity test:** the same Service seen with a new container ID keeps one series
      (this is the test that would have caught keying on container ID)
- [x] A container whose labels name a Compose the panel does not know is ignored, not invented
- [x] The 30s sweep is measured: `/containers/json` once, stats per container in parallel, and the
      sweep never blocks the 10s host tick

## Comments

Implemented in this ticket's single `feat:` commit on `impl/host-monitoring` (see the branch log).
`hostmetrics/container.go` declares the port and the collector; `dockerexec/containersource.go` is
the real implementation, tested against a fake daemon on a real unix socket (the same transport, no
real daemon). Verified: `go test ./...` green, `go test -race` on both touched packages clean,
`go vet ./...` and `gofmt` clean; no new dependencies in `go.mod`.

Self-review findings, fixed before committing:

- First draft launched stats goroutines with `go func(){...}(i)` against a parameterless closure —
  caught by the compiler, fixed to an explicit `(i, tgt)` parameter list.
- The shutdown-drain test originally drove `Run` with a 2ms interval and waited for the sweep to
  fire — but the due counters are wall-duration-aligned by design, so the sweep's turn arrives
  30s in, whichever the interval. The test now launches the blocked sweep via direct `tick()` calls
  and runs `Run` on an already-cancelled context: it must wait out the in-flight hook, and does.

Decisions taken where the ticket left room:

- The port returns **cumulative counters already summed** (networks over all interfaces; blkio
  read/write summed case-insensitively over `io_service_bytes_recursive`, skipping `sync`/`async`/
  `total`); the collector only computes deltas. cgroup v2's `inactive_file` is preferred over v1's
  `cache` when present, like the docker CLI.
- Service rates are stored **per second** (B/s), with dt taken from the actual time between sweeps,
  so Service charts share units with the host's; container CPU follows Docker's own arithmetic
  (Δcontainer / Δsystem × online CPUs × 100 — a share of one core, so >100 is normal, and tested).
- A **redeploy resets the baseline** explicitly: same Service, new container ID → one series, and
  that sweep reports NaN rates even if the new counters happened to exceed the old ones.
- The cache **drops a Service whose container left the listing** (the sweep is the truth about what
  is running; history is ticket 03's rollups), and a failed stats call keeps the previous sample
  rather than inventing one; `Status()` reports the last sweep's outcome for ticket 04's sources
  block.
- **Scale > 1 is a known collapse:** two containers of one Service map to one key and the last one
  listed wins. Summing per-container samples is deferred until a real user runs replicas.
- The per-Service sample **history** (the Monitor tab's windows) is not a checkbox here — the cache
  holds the latest sample per Service; its ring/rollups land with tickets 03–04, as does the
  `main.go` wiring (which will pass `dockerexec.DefaultSocketPath`).

