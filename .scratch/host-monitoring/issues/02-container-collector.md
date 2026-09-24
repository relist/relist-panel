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

**Status:** open

- [ ] A fake `ContainerSource` drives the tests; no test talks to a real daemon
- [ ] CPU, network and block I/O are stored as deltas; a container's first sweep reports no rate
- [ ] Memory used is `usage − cache`; the limit is the container's own
- [ ] **Redeploy continuity test:** the same Service seen with a new container ID keeps one series
      (this is the test that would have caught keying on container ID)
- [ ] A container whose labels name a Compose the panel does not know is ignored, not invented
- [ ] The 30s sweep is measured: `/containers/json` once, stats per container in parallel, and the
      sweep never blocks the 10s host tick

## Comments
