# Spec — Request metrics from the Access log

Date: 2026-11-06.
Status: open — branch claude/access-logs-metrics-monitoring-49f18e

## Problem

The panel shows CPU, memory, disk and network for the host and for each Service, but
nothing about the HTTP traffic itself. An operator cannot see how many requests a site
gets, how many fail, or how slow they are, without downloading the Access log and
analysing it by hand.

Traefik already writes every request to the **Access log** as JSON lines
(`<data dir>/.traefik/access/access.log`, ADR-0027), turned on in the Traefik page's
Settings (ADR-0030). Each line carries `StartUTC`, `RouterName`, `RequestHost`,
`DownstreamStatus` and `Duration` (nanoseconds).

## Goal and success criteria

- The panel reads the Access log as Traefik writes it and turns it into **request
  metrics**: request counts by status class (1xx–5xx) and latency p50 / p90 / p99.
- Three views:
  1. **Monitoring › Requests** (admins): every request that went through Traefik, as one
     series. No split by host.
  2. **Monitoring › Panel** (admins): requests to the panel itself (Traefik router `panel`).
  3. **Compose › Monitor › Requests** (anyone who can see the Project): requests to the
     Compose's Domains, split by **host** (all paths of one host together), with an
     "All hosts" choice that is the default.
- History of 24 hours at 1-minute resolution, always. Older history at 1-hour resolution,
  kept for a number of days set by an admin: 1–90, default 1.
- A burst of traffic never loses lines: reading late only delays the charts. A panel
  restart resumes where it stopped.
- Requests with a client-chosen `Host` cannot grow storage: the number of stored series
  is fixed by the number of Domains, never by the traffic.
- Ingesting costs well under 1% of one core at 1,000 requests per second.

## Out of scope

- Alerts or notifications on any metric.
- Per-host series on the host-wide page, top-host lists, or a series for unmatched
  requests. They are counted in the total only; to investigate, download the Access log.
- Separating backend time (`OriginDuration`) from total time. Possible later as a second
  histogram.
- Routers written by hand as Traefik labels in a Compose: counted in the total only.
- Longer history for CPU / memory / disk / network: they stay at 24 hours.
- More percentiles. The stored histogram makes adding one later a read-side change only.

## Design

### Data flow

```
access.log ──tail──▶ Ingester ──▶ Aggregator (open minutes in memory)
                                      │ minute closes (watermark)
                                      ▼
                         request_metric_rollups   (1 minute, kept 25 h)
                                      │ hour complete
                                      ▼
                         request_metric_hourly    (1 hour, kept N days)
                                      │
                                      ▼
                         API ?window=…  → merge histograms → percentiles
```

### Ingester

A goroutine in the panel, started with the other housekeeping jobs.

- Opens `access.log` and reads appended bytes. A line without a trailing `\n` is held
  until it is complete.
- **Rotation**: when `access.log` on disk has a different inode from the open file, the
  Ingester reads the open file to EOF, then opens the new one from offset 0. The open
  descriptor keeps a rotated file readable even after the rotator gzips and removes it.
- **No file** (Access log off, Traefik not updated): retry every 10 seconds. The panel
  keeps running.
- **Bad line** (invalid JSON, missing `StartUTC`): skipped and counted; logged at most once
  a minute.
- Polls for new bytes every second while idle. No inotify.

### Mapping a line to series

Every line counts in `total`. Additionally:

| `RouterName` | Also counts in |
|---|---|
| `panel@docker` | `panel` |
| `relist-c<C>-d<D>@docker` | `host` series (compose C, host of Domain D) |
| `panel-http@docker`, `relist-c<C>-d<D>-http@docker` (HTTP → HTTPS redirects) | nothing else |
| anything else, or empty (no router matched) | nothing else |

The Domain ID → host map is read from the database and reloaded when Domains change
(after a deploy or a Domains edit), and at most once per 10 seconds on a miss. A Domain
ID the map does not know (deleted Domain) counts in `total` only.

The host is lower-cased as stored on the Domain; it never comes from `RequestHost`.

Status is `DownstreamStatus`, the status the client got: 1xx–5xx go to their class;
anything else goes to `cother`. Latency is `Duration`.

### Minute buckets and the watermark

- A line goes into the minute of its `StartUTC`, never the minute it was read.
- The Aggregator keeps every minute still open in memory: per series, the six counters
  and a latency histogram.
- **Watermark**: the latest `StartUTC` read so far. Minute M closes when the watermark
  passes M + 1 minute + 30 seconds (30 seconds for lines written slightly out of order).
  With no new line for 2 minutes, minutes close by the wall clock instead, so a quiet
  site still gets its rows.
- Reading late therefore only holds minutes open longer. Memory grows by a few KB per
  open minute.
- A line whose minute has already closed (more than 30 seconds out of order) is dropped
  and counted.

### Latency histogram

- Log-scale buckets: bucket k covers [1 ms × 1.2^k, 1 ms × 1.2^(k+1)), from 1 ms to 60 s,
  plus an underflow (< 1 ms) and an overflow (≥ 60 s) bucket. About 62 buckets.
- A percentile is the geometric midpoint of the bucket that holds it: within about ±10%
  of the exact value. Underflow reports 1 ms and overflow 60 s.
- Merging two histograms adds their buckets, so a merged percentile is exact to the
  bucket. Percentiles are never averaged.
- Stored as a compact sparse BLOB (bucket index + count as varints); a few hundred bytes
  at most.

### Storage

New migration `00035_request_metrics.sql` (never edit a committed migration):

```sql
CREATE TABLE request_metric_rollups (
    scope      TEXT    NOT NULL CHECK (scope IN ('total', 'panel', 'host')),
    compose_id INTEGER NOT NULL,  -- 0 unless scope = 'host'
    host       TEXT    NOT NULL,  -- '' unless scope = 'host'
    bucket_at  INTEGER NOT NULL,  -- unix seconds, start of the minute
    c1xx INTEGER NOT NULL, c2xx INTEGER NOT NULL, c3xx INTEGER NOT NULL,
    c4xx INTEGER NOT NULL, c5xx INTEGER NOT NULL, cother INTEGER NOT NULL,
    latency    BLOB    NOT NULL,
    PRIMARY KEY (scope, compose_id, host, bucket_at)
) WITHOUT ROWID;

CREATE TABLE request_metric_hourly ( -- same columns; bucket_at is the start of the hour
    ...
) WITHOUT ROWID;

CREATE TABLE request_metric_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL DEFAULT 1 CHECK (retention_days BETWEEN 1 AND 90)
);
INSERT INTO request_metric_settings (id, retention_days) VALUES (1, 1);

CREATE TABLE request_metric_cursor (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    inode           INTEGER NOT NULL,
    resume_offset   INTEGER NOT NULL,
    flushed_through INTEGER NOT NULL  -- unix seconds: every minute before this is written
);
```

Indexes for the retention deletes by `bucket_at` alone, as in `00008_metrics.sql`.

- **Minute close**: one transaction writes the closed minutes' rows and the cursor. The
  panel runs with one SQLite connection; the write is one small transaction per minute.
- **Hour compaction**: once every minute of hour H is closed, the rows of H are merged
  in Go (counters added, histograms merged) and written to `request_metric_hourly`.
- **Retention**: after compaction, delete minute rows older than 25 hours and hourly rows
  older than `retention_days`. Lowering the setting deletes the excess at the next hourly
  run.
- The tables live in `panel.db`, so they move with a Migration.
- Rows of a deleted Domain or Compose are not removed early; they expire.

### Resuming after a restart

The cursor is saved in the same transaction as the minute rows it accounts for.

- `resume_offset` is the smallest file offset among lines held in still-open minutes (or
  the read position when none are open). Every line before it is in a written row.
- `flushed_through` is the start of the oldest still-open minute.
- On start: if `access.log`'s inode equals the cursor's, read from `resume_offset` and
  skip lines whose minute is before `flushed_through` (already written). Otherwise
  (rotated while the panel was down, or no cursor yet) start at the end of `access.log`.
- The only loss is a panel outage across the midnight UTC rotation, or a first start.
  The gap shows as missing buckets.

### Access log filters

An admin can add `accessLog.filters` (status codes, retry attempts, min duration) or drop
fields in the Traefik override YAML. Either makes the metrics wrong. The panel reads the
override: when `accessLog` has `filters`, or `fields` drops `RouterName`, `StartUTC`,
`DownstreamStatus` or `Duration`, every request-metrics view shows a warning saying the
numbers cover only the logged requests.

### API

All endpoints take `window` and answer 400 for an unknown window or one longer than the
retention allows (no silent default).

| Endpoint | Who | Series |
|---|---|---|
| `GET /api/requests/metrics?window=` | admin | `total` |
| `GET /api/requests/panel/metrics?window=` | admin | `panel` |
| `GET /api/composes/{id}/requests/metrics?window=&host=` | `LevelViewer` on the Project, as Service metrics (`internal/authz/routes.go`) | one host, or with `host` empty all **current** hosts of the Compose merged |
| `GET /api/requests/settings` | admin | — |
| `PUT /api/requests/settings` | admin, audited | `{ "retentionDays": 1..90 }`, else 400 |

The Compose endpoint answers 400 for a `host` that is not one of the Compose's current
Domain hosts. The authorization rules are the existing ones (ADR-0031): no new role or
grant. The four host-wide routes are left out of the authz table, which makes them admin-only.

Windows:

| Window | Source | Bucket |
|---|---|---|
| `15m`, `1h`, `6h`, `24h` | `request_metric_rollups` | 1 minute |
| `7d` | hourly, if retention ≥ 7 | 1 hour |
| `30d` | hourly, if retention ≥ 30 | 1 hour |
| `90d` | hourly, if retention = 90 | 1 hour |

Response:

```json
{
  "window": "1h",
  "partial": false,
  "accessLogEnabled": true,
  "accessLogFiltered": false,
  "retentionDays": 1,
  "hosts": ["api.example.com", "example.com"],
  "summary": { "requests": 12345, "c1xx": 0, "c2xx": 12000, "c3xx": 200, "c4xx": 140,
               "c5xx": 5, "cother": 0, "p50Ms": 12, "p90Ms": 80, "p99Ms": 410 },
  "buckets": [ { "at": "2026-11-06T10:00:00Z", "c1xx": 0, "c2xx": 190, "c3xx": 3,
                 "c4xx": 2, "c5xx": 0, "cother": 0, "p50Ms": 11, "p90Ms": 75, "p99Ms": 390 } ]
}
```

- `hosts` only on the Compose endpoint: its current Domain hosts, for the selector.
- The server computes every percentile; the browser never sees a histogram.
- `summary` percentiles come from merging every bucket of the window.
- A bucket with no requests has `null` percentiles: a gap, never zero.
- A bucket with no row at all (panel down) is absent; `partial` is true when the window
  has fewer buckets than its length, as the existing metric endpoints do.

### UI

Before building, read Ant Design's `llms.txt` and the relevant component pages for the
installed `antd` 6.5.1, and `docs/GUIDELINES.md`.

Every view has, top to bottom:

1. A `Segmented` window picker (only windows the retention allows), as on Monitoring.
2. A summary row: requests, 5xx rate %, p50, p90, p99 for the window.
3. **Status** chart: four lines, 2xx / 3xx / 4xx / 5xx, in requests per minute. Hourly
   buckets are divided by 60, so 24h and 7d read on the same scale. (1xx and other are
   in the summary only.)
4. **Latency** chart: three lines, p50 / p90 / p99, in ms.

Both charts use the existing `MetricChart`, with two new units: `requestsPerMinute` and
`milliseconds`.

- **Monitoring** gets tabs: the existing host charts, **Requests**, **Panel**. Monitoring is
  already admin-only. Requests has a **Settings** button
  that opens a drawer with the retention days (1–90) and warns, before saving a lower
  value, that older history will be deleted within the hour.
- **Compose › Monitor** gets a **Requests** section under the Service charts, with a
  `Select` of hosts ("All hosts" first). Hidden when the Compose has no Domain.
- Access log off → an `Alert` in place of the charts, linking to the Traefik page.
- Access log filtered → a warning `Alert` above the charts.

## Error handling

| Situation | Behaviour |
|---|---|
| Access log off, or Traefik not updated | `accessLogEnabled: false`; views show the Alert |
| `access.log` missing | Ingester retries every 10 s |
| Bad line, unknown router, unknown Domain ID | counts in what it can (or nothing), skipped, counted, logged ≤ once a minute |
| Line for an already-closed minute | dropped, counted |
| SQLite write fails | logged; the minutes are dropped and show as a gap; the cursor is not advanced, so a restart re-reads them |
| Unknown or too-long `window`, unknown `host` | 400 |
| Not allowed | existing authz answers (403 / 404) |

## Resource estimate

| | Estimate |
|---|---|
| CPU | ~2–5 µs per line with `encoding/json`; < 1% of a core at 1,000 req/s |
| Memory | a few KB per series per open minute |
| Disk | ~(2 + host count) rows per minute; 100 hosts ≈ 150k minute rows (25 h) + 2.4k hourly rows per retained day, a few hundred bytes each |
| SQLite | one transaction per minute, one per hour |

To be measured during implementation with a generated log, not assumed.

## Testing

- **Go unit**: line parsing and series mapping (every row of the mapping table); histogram
  add, merge and percentile error against exact percentiles of random data (≤ 10%);
  watermark closing, wall-clock closing, late-line dropping; hour compaction; retention
  deletes; cursor resume (same inode, other inode, no cursor, skip by `flushed_through`).
- **Ingester with real files** in a temp dir: append, partial line, rename + new file
  (old file read to EOF first), old file removed while open, missing file.
- **HTTP**: every window, retention gating, admin vs Project member vs outsider, `host`
  set / empty / unknown, settings PUT in and out of range, audit record.
- **Web (vitest)**: view model from response to chart series (per-minute normalisation of
  hourly buckets, `null` gaps), window list from retention, Access-log-off and filtered
  Alerts, host selector.
- **Live check**: real `traefik:v3.7`, generated requests across two Domains and the panel,
  numbers on all three views match the counts sent. A burst large enough to make the
  Ingester lag, then a panel restart mid-burst: totals still match.
- Browser check: report honestly if it cannot run in this WSL.

## Decisions

- Read the Access log in the panel; not Traefik's Prometheus metrics (coarse default
  buckets, labelled by router not host) and not an external Prometheus / Grafana / Loki
  stack (footprint).
- Key series by `RouterName`, not `RequestHost`: the client controls `Host`, the panel
  controls router names.
- Host-wide view is one total series; Compose view is per host; panel is its own series.
- Percentiles p50 / p90 / p99 from a stored histogram.
- 1-minute resolution for 24 hours; 1-hour resolution for a configurable 1–90 days,
  default 1.
- Resume from a saved cursor after a restart.
- Needs an ADR (next free number) recording the Access log as the metrics source and the
  router-name keying.
