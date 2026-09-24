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

**Status:** open

- [ ] An invalid window is a 400 with a message, not a silent default
- [ ] The host response is served from the ring for ≤1h and from rollups for 6h/24h, and says which
      (`partial` when the data is shorter than the window, e.g. after a restart)
- [ ] `sources` reports an unmountable `/proc` as unavailable with a reason, so the screen renders
      `unavailable` rather than zeros
- [ ] The Services endpoint is served from the collector's cache and returns name, image, state,
      health, ports and container ID per Service, for a Compose that exists and 404 otherwise
- [ ] Sampler lifecycle: started once in `main.go`, cancelled on shutdown, and a second start is not
      possible (no duplicate goroutines)
- [ ] HTTP-seam tests with an injected sampler fake; `go test ./...`, `go vet ./...` clean

## Comments
