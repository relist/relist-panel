# Request metrics read from the Access log, keyed by router

The panel showed CPU, memory, disk and network for the host and each Service, but nothing about the HTTP traffic: how many requests a site gets, how many fail, how slow they are. Traefik already writes every request to the Access log (ADR-0027) as JSON lines with `StartUTC`, `RouterName`, `RequestHost`, `DownstreamStatus` and `Duration`.

**Decision:**

- The panel tails `<data dir>/.traefik/access/access.log` and turns it into **request metrics**: counts by status class (1xx to 5xx, and other) and latency p50, p90 and p99, per minute. A goroutine, the Ingester, started with the other housekeeping jobs, polls the file once a second. It follows a rotation by reading the old file to its end, then opening the new one.
- **Series are keyed by `RouterName`, never by `RequestHost`.** The client chooses `Host`; the panel chooses router names. Every line counts in `total`. `panel@docker` also counts in `panel`. `relist-c<C>-d<D>@docker` also counts in the host series of Compose C, the host of Domain D being read from the database. Redirect routers (`-http`), hand-written Traefik labels and unmatched requests count in `total` only. The number of stored series is therefore fixed by the number of Domains, not by the traffic.
- A line goes into the minute of its own `StartUTC`. A minute closes when the watermark (the latest `StartUTC` read) passes it by 90 seconds, or by the wall clock after 2 minutes without a new line. Reading late only delays the charts; it loses nothing.
- Latency is a stored log-scale histogram (ratio 1.2, 1 ms to 60 s, plus underflow and overflow), kept as a sparse BLOB. Histograms add, so a window's percentile is computed from merged histograms and is within about 10% of the exact value. Percentiles are never averaged.
- Minute rows are kept 25 hours. Each complete hour is compacted into an hourly row kept for 1 to 90 days, set by an admin (default 1). Both live in `panel.db` and move with a Migration.
- A cursor (inode, resume offset, flushed-through time) is written in the same transaction as the minute rows it accounts for, so a restart resumes without counting a line twice or losing one. Only a panel outage across the midnight rotation, or a first start, leaves a gap.
- Three views: Monitoring › Requests and Monitoring › Panel for admins, and a Requests section on a Compose's Monitor tab for anyone who can see the Project, split by host. No new role or grant: the host-wide routes are left out of `authz.Routes` (admin only), the Compose route is a Viewer's, like the Service metrics (ADR-0031).
- When the override filters the Access log or drops a field the metrics read, every view warns that the numbers cover only the logged requests.

## Considered options

**Traefik's Prometheus metrics.** Rejected: coarse default latency buckets, labelled by router and service rather than by the host the operator knows, and one more endpoint to scrape and keep.

**An external Prometheus, Grafana or Loki stack.** Rejected: the footprint is out of proportion on the single host this panel targets, and it adds services to install, update and migrate.

**Keying by `RequestHost`.** Rejected: the client controls it, so any visitor could create series without limit and grow storage. `RouterName` is written by the panel.

**Counting a line in the minute it was read.** Rejected: a burst or a slow read would pile its lines into the wrong minutes. The line's own `StartUTC` is the truth.

**Averaging the percentiles of each minute for a longer window.** Rejected: an average of percentiles is not a percentile. Merging histograms is.

## Consequences

- **Domain changes reach the host map by polling.** The map reloads every 30 seconds and on an unknown Domain, at most once per 10 seconds. A new Domain's first requests may count in `total` only for a few seconds.
- **Rows of a deleted Domain or Compose expire; they are not removed early.** The Compose view reads the Compose's current hosts only.
- **Requests the panel cannot place are in `total` only.** Hand-written Traefik routers, unmatched requests and a deleted Domain's route show up there and nowhere else. To investigate, download the Access log.
- **Filters make the numbers wrong.** The panel reads the Traefik override and says so; it does not stop the filter.
- **The 7d, 30d and 90d windows read hourly rows**, so the current hour is missing from them until it is complete, and they exist only up to the retention an admin set.
- **A restart within about 90 seconds after the Access log is rotated can lose the lines of the minutes that were still open**, because the cursor can only point into the new file. A Panel update or crash at midnight UTC is the case.
- Spec: `.scratch/request-metrics/spec.md`. Builds on ADR-0027 and ADR-0030.
