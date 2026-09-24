# Spec — Host monitoring (1-hour metric history)

## Problem

The panel answers "what is deployed" but not "what is the host doing". CPU, memory, disk space,
Docker's own disk footprint, block I/O and network I/O are only visible by SSHing in and running
`htop` / `df` / `docker system df`. The operator wants the last hour of each as a chart, inside the
panel, without making the panel heavier and without spending RAM on a metrics stack.

The design draft lives at `web-design/src/pages/MonitoringPage.tsx` (mock data, no API) and is the
visual contract: six charts, a 15m/1h/6h/24h window control defaulting to 1h, `partial` and
`unavailable` states, and a downsample-to-pixels rule.

## Decision

Sample the host **in-process, inside the existing panel binary**. No agent, no sidecar, no
Prometheus/cAdvisor/Netdata — those are separate containers with hundreds of MB of RSS and their own
storage, which is exactly the cost the operator asked to avoid.

Budget (decided 2026-09-24):

- 10-second interval; a ring buffer of the last hour (360 samples) in memory; 1-minute rollups for
  24 hours (1440 rows) written to the panel's existing SQLite. About 190 KB of heap plus ~150 KB/day
  of rows. A restart costs the current hour's 10-second detail, not the day's shape — the screen's
  `partial` state ("collecting — N of 60 minutes") covers the seam.
- One goroutine with a `time.Ticker`, started with the panel and running for its lifetime — not a
  cron, not a separate service. Per-container sampling rides the same loop.
- Stdlib only (`os.ReadFile`, `syscall.Statfs`) plus the SQLite handle and the Docker socket the
  panel already holds — no new dependency, no new process, no new container.

### Why not cAdvisor

Measured on this host (2026-09-24), default invocation (five host mounts — `/`, `/var/run`, `/sys`,
`/var/lib/docker`, `/dev/disk` — plus `--privileged` and `--device=/dev/kmsg`):

| | cAdvisor v0.60.6 (current) | cAdvisor v0.49.1 | In-process sampler |
| --- | --- | --- | --- |
| Heap (`anon` from cgroup `memory.stat`) | 28.8 MB | 20.4 MB | ~190 KB of buffers |
| What `docker stats` reports | 324 MB — page cache 76 MB + **195 MB reclaimable slab** from crawling `/sys` | 23–33 MB | — |
| `/metrics` per scrape | **5.4 MB** / 4,013 lines | 292 KB / 2,635 lines | 7 KB of `/proc` |
| Scraped every 10 s, per day | ~47 GB | ~2.5 GB | ~60 MB of local reads |
| Privileges | `--privileged` + 5 host mounts | same | none; two read-only mounts |
| Containers it detected on this host | 0 | 0 | — |

Neither version saw the Docker containers on this WSL2 host (it collected the 40 systemd cgroups
only), so these are its floor, not its ceiling. Tuning the old version with
`--housekeeping_interval=10s --storage_duration=30s --disable_metrics=… --store_container_labels=false`
moved `docker stats` from ~23–30 MB to 19.6 MB on the same workload, and the payload only from 292 KB
to 269 KB — the expensive series are the fs/blkio/memory ones we actually want. `--docker_only=true`
dropped it to ~15 MB by collecting the root cgroup alone, which is not a usable configuration here.

cAdvisor earns its keep when you want per-container metrics scraped by Prometheus. Here it would be a
second service to run, secure and update, and the panel would still have to store the history itself.

### Sources (measured on the real panel image, 2026-09-24)

| Metric | Source | Cost per sample | Reachable from the panel container today |
| --- | --- | --- | --- |
| CPU usage | `/proc/stat` (CPU jiffies are not namespaced) | < 10 ms, ~2.3 KB | Yes, already host-accurate |
| Memory usage | `/proc/meminfo` | < 10 ms, ~1.5 KB | Yes — but only because this Docker does not mount lxcfs. Treat as runtime behaviour, not a contract. |
| Disk space | `statfs` on the mounted data dir (a host filesystem) | instant | Yes |
| Block I/O | `/proc/diskstats` (28 devices inside and outside) | < 10 ms, ~1.7 KB | Yes, already host-accurate |
| Network I/O | `/proc/<pid>/net/dev` | < 10 ms, ~1.5 KB | **No — see below** |
| Docker disk usage | `docker system df` (or `-v` for the per-image/container/volume breakdown) over the mounted socket | 0.66–0.67 s, ~32 MB transient | Yes, but must be cached |
| Per-container CPU / memory / I/O | container cgroup files, or the Docker stats API | see below | Only with a second mount, or via the socket |

For scale: the panel binary idles at 3.86 MB RSS today, so the ring buffer is roughly a 5 % addition.

### Per-container metrics

`/sys/fs/cgroup` inside the panel container is its **own** subtree — `/proc/self/cgroup` is `0::/` and
zero `docker-*.scope` entries are visible — so container cgroups cannot be read without a mount. With
`-v /sys/fs/cgroup:/host/cgroup:ro`, the host's tree appears (29 entries, 7 container scopes). Two
routes, and the choice is a cadence question:

| | Docker stats API over the socket | Read-only `/sys/fs/cgroup` mount |
| --- | --- | --- |
| Extra mount | none | one |
| Cost | `/containers/json` 0.01 s / 16 KB for the whole list; `/containers/{id}/stats?stream=false` ~1.97 s wall but **0.00–0.02 s CPU** and 1.9 KB — the wall time is Docker sampling a delta window, not work. Calls can run concurrently, so N containers ≈ 2 s. | three small files per container per tick |
| Cadence it suits | 30–60 s | 10 s, same as the host |
| Caveats | one request per container per tick | must map cgroup → container name/image via `/containers/json`, and handle cgroup v1 vs v2 layouts |

`docker system df -v` costs the same as the aggregate (0.67 s, 25 KB) and returns per-image,
per-container and per-volume sizes, so the container breakdown of Docker disk usage is free once the
aggregate call is being made — cache both for a few minutes.

### Network I/O is the one real gap, and it fails silently

The panel container has its own network namespace, so its `/proc/net/dev` reports interfaces with the
**same names** as the host (`lo`, `eth0`) carrying its own counters — measured: container `eth0` rx
200 bytes vs host `eth0` rx 5,273,301,899 bytes. Rendering that as "host network I/O" would be a
wrong number wearing a right label.

Mounting host `/proc` read-only at `/host/proc` is necessary but **not sufficient**:
`/host/proc/net/dev` still returns the container's counters, because `/proc/net/dev` is
`/proc/self/net/dev` and `self` resolves to the process doing the reading. The working form is
`/host/proc/1/net/dev` — PID 1's namespace is the host's — which was verified to match the host's
numbers exactly.

So this feature needs one line in `docker-compose.yml` (a read-only host `/proc` mount) and a note in
ADR-0002. The panel already holds the Docker socket, which is root-equivalent, so a read-only `/proc`
mount does not change the trust model — but it does change what the panel can read, which is why it
is recorded rather than assumed.

### Screen rules the implementation must keep

- Never plot container-scoped numbers as host numbers: a source that is not readable renders
  `unavailable` with an explanation, never a zeroed line.
- Downsample to the plot's pixel width, keeping each bucket's min **and** max so spikes survive.
- Percent metrics use a fixed 0–100 axis; byte-rate metrics auto-scale.
- Docker disk usage refreshes every few minutes and is carried forward between refreshes, so its
  series is a step, not a 10-second sample.

## Decisions

Settled 2026-09-24:

- Sampling **in-process** (not cAdvisor), one background loop for the panel's lifetime.
- **10-second** interval for the host; 1-hour ring buffer in memory with 1-minute rollups persisted
  in SQLite so the day survives a restart.
- **Read-only host `/proc` mount** (`/host/proc`), network counters read from `/host/proc/1/net/dev`.
  Without it five of six host metrics work and network I/O stays `unavailable`. The panel already
  holds the Docker socket, which is root-equivalent, so the mount does not change the trust model —
  but it does change what the panel can read, which is why it is recorded. Needs a line in
  `docker-compose.yml` and a note in ADR-0002.
- **Per-container metrics collected too**, for every container. Docker disk usage comes from
  `docker system df -v` over the socket (0.67 s, cached a few minutes) so the per-image/per-container
  breakdown is free once the aggregate is being fetched. Host disk space comes from `statfs` on the
  already-mounted data dir; on this host `/var/lib/docker` sits on the same device as `/`, but that is
  not guaranteed elsewhere, which is why Docker's own usage is asked of the daemon instead.

Per-container metrics come from the Docker stats API over the socket the panel already holds — **no
second mount, no cgroup files**. `docker stats` is a wrapper over `/containers/{id}/stats?stream=false`
and returns the same data, so the panel calls the API directly from its own process instead of
spawning a CLI: the CLI costs a 32.9 MB transient process and 2.04 s wall per call, the API call costs
~1.97 s wall, ~0.00 s CPU and 1,877 bytes, and the wall time is Docker sampling a delta window rather
than work. Calls run concurrently, so N containers is still ~2 s; a 30-second cadence is plenty for a
panel.

Read the raw API numbers, not the CLI's `--format json`: the CLI hands back formatted strings
(`"MemUsage": "10.34MiB / 15.62GiB"`, `"NetIO": "1.14MB / 698kB"`, `"CPUPerc": "0.24%"`) that would
have to be parsed, while the API returns `usage` / `cache` / `limit` in bytes, `cpu_usage.total_usage`
and `system_cpu_usage` in nanoseconds, `networks.eth0.rx_bytes`, and
`blkio_stats.io_service_bytes_recursive`. Container identity (name, image, Compose labels) comes from
`/containers/json`, 0.01 s and 16 KB for the whole list. CPU, network and block I/O arrive as
cumulative counters, so the sampler stores deltas and the first tick of a container's life has no
rate to report.

A read-only `/sys/fs/cgroup` mount would allow a 10-second cadence for three small file reads per
container per tick, but it is not needed for a 30-second one, so it stays out.

### Per-service history is keyed by service, not by container

Every deploy mints a new container with a new ID, so a container ID is a bad history key: the graph
would restart at zero on each deploy and the operator would lose the very comparison they opened the
tab for. Samples are therefore keyed by `(compose, service)`. The mapping from service to the running
container comes from the same place the status rail already gets it — `docker compose ps --format json`
— so the tab needs no new Docker surface beyond the stats API. Container CPU is a percentage of one
core and can exceed 100%, and memory used is `usage − cache`; both are recorded here because the
screen's axis and summary depend on them.

### Where it shows up on the screen

Host metrics get their own screen (`#/monitoring`, §4.2 of the guidelines). Per-service metrics live
on the Compose screen as a **Monitor tab** (`#/projects/:p/env/:e/compose/:c/monitor`), matching the
Project screen's habit of putting tabs in the path: pick a service, see that service's CPU, memory,
network and block I/O over the same 15m/1h/6h/24h windows, with a marker for the last deploy so a
spike can be read against what caused it. A Compose that has never been deployed shows the empty
state pointing at the editor tab, because deploying is what starts collection.

Also open: **where the per-container numbers go on the screen.** Decided — the Compose screen's
Monitor tab, see above. What remains open is only whether the selected service should be part of the
URL (`…/monitor?service=web`), which the prototype's hand-rolled router has no query support for; the
tab is linkable, the selection is view state.

## Budget for the per-service half

30-second cadence, roughly 72 bytes per sample (eight float64s plus a timestamp), so about 170 KB of
ring buffer for an hour at 20 containers, and 1-minute rollups of about 2 MB per day of rows in
SQLite. Together with the host half the sampler stays under 400 KB of heap — still around a tenth of
what the panel already uses.

## Implementation shape (settled 2026-09-24)

- **One goroutine, one tick.** A single `time.Ticker` at the host's 10s cadence; the 30s container
  sweep, the minute-close rollup and the 5-minute `docker system df` refresh run when their turn
  comes round, rather than three tickers whose drift has to be reasoned about separately.
- **Two rollup tables, one per scope.** Host and Service samples carry different fields (disk and
  Docker disk on one side, memory limit and PIDs on the other), so each gets its own table with only
  the columns it uses, in the repo's migration idiom: 5-digit sequence, goose `Up`/`Down`, timestamps
  as `INTEGER` Unix seconds. A rollup row is written when a minute closes, averaged over the samples
  that minute actually saw — a minute that a restart cut short is still a row, because the screens'
  `partial` state is the honest display for a gap. Retention deletes rows older than the longest
  window (a day, plus slack) and is the panel's first housekeeping job of any kind.
- **Endpoints**, all behind the existing auth middleware by virtue of being registered on the same
  mux: `GET /api/host/metrics?window=1h`, and
  `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/services/{service}/metrics?window=1h`.
  Both serve the window's natural resolution — the 10s ring up to an hour, the 1-minute rollups
  beyond it — and leave downsampling to the chart, which already implements the min/max rule to the
  plot's pixel width. Both return a `sources` block naming what is readable and why not, so the
  screens can render `unavailable` with a reason instead of a zeroed line.
- **A Services endpoint for the rail and the facts strip**:
  `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/services`, served
  from the container collector's cache — name, image, state, health (parsed from the container status
  text, not by spawning the compose CLI), ports, and the current container ID. The Compose resource
  itself stays about the Compose.
- **Docker access**: a second port, `ContainerSource`, declared in the new consumer package and
  implemented in `dockerexec` over the same socket — recorded in ADR-0006 as a refinement of
  ADR-0003's single-port rule, together with the read-only host `/proc` mount that refines ADR-0002.
- **Frontend tests**: vitest for pure functions only (`web/package.json` gains the devDependency and
  a `test` script) — the downsample rule, unit formatting, and the window→resolution table. No
  component tests; the UI keeps being verified by build, DOM probes, real-browser geometry and
  screenshots.

## Tickets

Six, in order. 01–04 are Go; 05–06 are the screens, which land after the design port's shell and
Compose tickets.

1. `01-sampler-and-proc-parsers.md` — the new package: `/proc` parsers as pure functions, the ring
   buffer, the 10s loop with its due-counters, statfs disk space, the `sources` availability map.
2. `02-container-collector.md` — the `ContainerSource` port and its socket implementation, service →
   container mapping off the compose labels, cumulative-counter deltas, the 30s cache.
3. `03-rollups-and-retention.md` — the migration (two tables, indices, `database_test.go`'s version
   assertion), the minute-close writer, the retention delete.
4. `04-metrics-endpoints.md` — the three endpoints, window validation, the sampler's lifecycle
   (started in `main.go`, cancelled on shutdown) and its fake for tests.
5. `05-monitoring-screen.md` — the host screen (six charts, window control, `partial`/`unavailable`).
6. `06-compose-monitor-tab.md` — the Compose screen's Monitor tab (service picker, four charts, the
   deploy marker), which also needs the Services endpoint's data in the status rail.

## Testing decisions

- The `/proc` parsers are pure functions over fixture text (golden files for `/proc/stat`,
  `/proc/meminfo`, `/proc/diskstats`, `/proc/net/dev`), including counter-wrap and
  freshly-booted/zero-delta cases.
- The container collector is tested against a fake `ContainerSource` (per ADR-0003 the port is
  declared by its consumer, so no test talks to a real daemon).
- Redeploy continuity is its own test: the same service seen with a new container ID keeps one
  series, because the key is `(compose, service)`. This is the test that would have caught keying on
  container ID.
- The sampler's ring buffer is tested for window slicing and rollup, not by sleeping.
- The rollup writer is tested for the retention delete (old rows go, the window stays intact) against
  a real migrated SQLite, per ADR-0003's habit of testing against the real database.
- The endpoints are tested at the HTTP seam with `httptest`, with the sampler injected as a fake so
  tests never read the real `/proc`.
- The frontend's downsample rule is a vitest pure-function test: 360 samples in, min/max pairs out,
  spike preserved.
