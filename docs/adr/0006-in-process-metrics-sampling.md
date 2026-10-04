# Metrics are sampled in-process, and history is keyed by Service

The operator wants CPU, memory, disk, block and network I/O for the host and for every Service, over
the last hour (and the day behind it), without a metrics stack. We sample the host and the containers
from inside the panel binary — one goroutine, four `/proc` files per tick, the Docker stats API over
the socket the panel already holds — keep the last hour at 10s in a ring buffer in memory, and
persist 1-minute rollups in the panel's existing SQLite for a day. `/proc` tells the truth about a
container's network only if the host's namespace is read, so the install gains one read-only mount:
host `/proc` at `/host/proc`, and network counters are read from `/host/proc/1/net/dev` — PID 1's
network namespace is the host's, while `/host/proc/net/dev` is `/proc/self/net/dev` and silently
reports the reader's own counters under the host's interface names.

Considered and rejected, all measured on the panel's own image (2026-09-24):

- **A resident metrics service** (cAdvisor, Prometheus, Netdata). cAdvisor v0.60.6 idles at 28.8 MB of
  real heap plus ~195 MB of reclaimable slab and 76 MB of page cache from crawling `/sys`, needs
  `--privileged` and five host mounts, and keeps only ~2 minutes of history — so the panel would
  scrape and store it anyway; its `/metrics` payload is 5.4 MB per scrape (47 GB/day at 10s). The
  older v0.49.1 sits at 20.4 MB of heap. Reading four `/proc` files costs ~7 KB and no new process.
- **A read-only `/sys/fs/cgroup` mount** instead of the stats API. It buys a 10s cadence for three
  small file reads per container, but it is not needed at the 30s cadence Services use, and it would
  add cgroup v1/v2 layout handling.
- **Shelling out to the `docker` CLI** for container metrics (`docker stats`): 2.04 s wall and a
  32.9 MB transient process per call, versus 1,877 bytes and no process from the stats API. The CLI's
  `--format json` also returns formatted strings (`"10.34MiB / 15.62GiB"`) where the API returns
  integers.

Two departures from earlier ADRs, recorded here rather than by editing them:

- **ADR-0002** fixed the panel's mounts (Docker socket, data dir) and its Docker access as
  shelling out to the compose CLI. Metrics add a third mount (read-only host `/proc`) and a second
  way of talking to Docker (HTTP over the same socket, for `containers/json` and
  `containers/{id}/stats`), because the CLI path is measurably too expensive to poll.
- **ADR-0003** records exactly one port/adapter pair (`ComposeRunner`). Metrics need a second:
  `ContainerSource`, declared in the new consumer package and implemented in the same `dockerexec`
  leaf package, so the rule becomes "one adapter package, one port per consumer".

Consequences worth knowing before reading the code:

- History is keyed by **(Compose, Service)**, never by Container: a deploy mints a new container, and
  a series keyed by container ID would restart at zero on exactly the deploy the operator opened the
  chart to understand. Answering "which container is this Service running as right now" rides on
  ADR-0005's frozen deploy identity — the Docker project label is `<p>-<e>-<c>`.
- A restart costs the current hour's 10s detail, not the day's shape: the ring is memory, the rollups
  are rows. The screens' `partial` state ("collecting — N of M minutes") is what that seam looks
  like, and it is also what a source that cannot be read looks like (`unavailable`) — never a zeroed
  line, and never a container's counters wearing a host label.
- The rollup retention delete is the panel's first housekeeping job; nothing in the codebase
  previously deleted rows by age.
