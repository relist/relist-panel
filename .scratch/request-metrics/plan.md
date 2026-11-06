# Request Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Traefik's Access log into request counts by status class and p50/p90/p99 latency, shown on Monitoring › Requests, Monitoring › Panel and each Compose's Monitor tab.

**Architecture:** A new Go package `internal/requestmetrics` tails `<data dir>/.traefik/access/access.log`, aggregates lines into per-minute buckets keyed by series (total, panel, per Compose host) with a log-scale latency histogram, and writes them to SQLite once a minute together with a resume cursor. Complete hours are compacted into an hourly table kept for a configurable 1–90 days. HTTP endpoints merge histograms server-side and return percentiles; the web reuses `MetricChart`.

**Tech Stack:** Go (stdlib `encoding/json`, `database/sql` + SQLite, goose migrations), React + antd 6.5.1 + SWR, vitest.

**Spec:** `.scratch/request-metrics/spec.md` — read it before starting any task.

## Global Constraints

- New migration only: `internal/migrations/00035_request_metrics.sql`. Never edit a committed migration (live installs run feature branches).
- New ADR: `docs/adr/0033-request-metrics-from-the-access-log.md`. Re-check `ls internal/migrations docs/adr` against `main` before Task 4 and Task 6; take the next free numbers if `main` moved.
- Series are keyed by `RouterName`, never by `RequestHost`.
- Status is `DownstreamStatus`; latency is `Duration` (nanoseconds).
- Histogram: bucket ratio 1.2, from 1 ms to 60 s, plus underflow and overflow; percentile = geometric midpoint of its bucket; within ±10% of exact.
- Watermark close: minute M closes when watermark ≥ M + 1m + 30s; idle close after 2 minutes without a new line.
- Minute rows kept 25 h; hourly rows kept `retention_days` (1–90, default 1).
- Windows: `15m`, `1h`, `6h`, `24h` from minutes; `7d` (retention ≥ 7), `30d` (≥ 30), `90d` (= 90) from hours. Anything else → 400.
- Host-wide routes are admin-only (left out of `authz.Routes`, listed in `adminOnly`); the Compose route is `LevelViewer`.
- Spec deviation, deliberate: the Compose route is `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/requests/metrics`, matching the existing Compose routes, not the spec's shorthand `/api/composes/{id}/…`.
- Spec deviation, deliberate: the Domain → host map is reloaded every 30 s and on a miss (at most once per 10 s), not by hooking deploys and Domain edits.
- Before any `web/` change: read https://ant.design/llms.txt, the pages it links for `Tabs`, `Segmented`, `Select`, `Drawer`, `InputNumber`, `Statistic`, `Alert`, and `docs/GUIDELINES.md`.
- Commits: Conventional Commits, no scope. End every commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Go tests: `go test ./internal/requestmetrics/...` per task; `go test ./...` before the last commit of a Go task. Web: `cd web && npm run typecheck && npm test`.

## Review Focus

1. **Rotation while minutes are open** — the old file is read to EOF; the cursor never points into the old file after the switch. Pinned in Task 5 (`TestIngesterRotationKeepsOpenMinutes`).
2. **Restart mid-burst** — totals after a restart equal totals without one: no line counted twice, none lost. Pinned in Task 5 (`TestIngesterResumeIsExact`).
3. **Quiet site** — with no traffic, the last minute still gets written (idle close) and the charts show gaps, not zeros. Pinned in Task 3 (`TestIdleClose`) and Task 7 (`nullPercentilesAreGaps`).
4. **Window past retention** — `30d` with retention 7 is a 400, and the UI never offers it. Pinned in Task 6 (`TestWindowBeyondRetention`) and Task 7 (`windowsFor`).
5. **Member of another Project** — gets the existing authz refusal on the Compose route, and a Compose ID from another Project under this Project's path is 404. Pinned in Task 6 (`TestComposeRouteOwnership`).

---

### Task 1: Latency histogram

**Files:**
- Create: `internal/requestmetrics/histogram.go`
- Test: `internal/requestmetrics/histogram_test.go`

**Interfaces:**
- Produces:
  - `const NumBuckets = 63` (index 0 = underflow `< 1 ms`, 1..61 = log buckets, 62 = overflow `≥ 60 s`)
  - `type Histogram struct { counts [NumBuckets]uint64 }`
  - `func (h *Histogram) Observe(d time.Duration)`
  - `func (h *Histogram) Merge(o *Histogram)`
  - `func (h *Histogram) Total() uint64`
  - `func (h *Histogram) Quantile(q float64) (ms float64, ok bool)` — `ok` false when empty
  - `func (h *Histogram) MarshalBinary() []byte` / `func UnmarshalHistogram(b []byte) (Histogram, error)`

- [x] **Step 1: Write the failing tests**

```go
func TestQuantileWithinTenPercent(t *testing.T)       // 100k log-uniform samples 1ms..60s, seed 1: |est-exact|/exact ≤ 0.10 for q in {0.5, 0.9, 0.99}
func TestMergeEqualsObservingAll(t *testing.T)        // split samples in two histograms, Merge → identical counts to one histogram
func TestQuantileEdges(t *testing.T)                  // empty → ok=false; only 0.3ms → 1; only 90s → 60000
func TestBinaryRoundTrip(t *testing.T)                // Marshal → Unmarshal equal; empty encodes to len 0; len ≤ 400 bytes for a full histogram
func TestUnmarshalRejectsBadIndex(t *testing.T)       // index ≥ NumBuckets → error
```

- [x] **Step 2: Run** `go test ./internal/requestmetrics/ -run 'Quantile|Merge|Binary|Unmarshal'` — Expected: FAIL (package does not compile).

- [x] **Step 3: Implement** in `histogram.go`. Bucket of `d` (ms = d/1e6 as float): `ms < 1` → 0; `ms ≥ 60000` → 62; else `1 + floor(log(ms)/log(1.2))`, clamped to 61. Quantile: `rank = ceil(q * total)`, walk cumulative counts; bucket `i` in 1..61 reports `1.2^(i-1) * sqrt(1.2)` ms. Encoding: for each non-zero bucket, `uvarint(index)` then `uvarint(count)`.

- [x] **Step 4: Run** the same command — Expected: PASS.

- [x] **Step 5: Commit** `feat: add a log-scale latency histogram for request metrics`

---

### Task 2: Access log line parsing and router classification

**Files:**
- Create: `internal/requestmetrics/line.go`
- Test: `internal/requestmetrics/line_test.go`

**Interfaces:**
- Produces:
  - `type Line struct { Start time.Time; Router string; Status int; Duration time.Duration }`
  - `func ParseLine(b []byte) (Line, error)` — error on invalid JSON or zero `StartUTC`
  - `type RouteKind int` with `RouteOther`, `RoutePanel`, `RouteDomain`
  - `type Route struct { Kind RouteKind; ComposeID, DomainID int64 }`
  - `func ClassifyRouter(name string) Route`

- [x] **Step 1: Write the failing tests**

```go
func TestParseLine(t *testing.T)
// `{"StartUTC":"2026-11-06T10:00:01.5Z","RouterName":"relist-c12-d3@docker","DownstreamStatus":502,"Duration":1500000}`
//   → Start 2026-11-06T10:00:01.5Z, Router "relist-c12-d3@docker", Status 502, Duration 1.5ms
// `{"RouterName":"x"}` → error; `not json` → error; `{"StartUTC":"…"}` with no router → Router "" and no error

func TestClassifyRouter(t *testing.T) // table:
// "panel@docker"               → RoutePanel
// "panel-http@docker"          → RouteOther
// "relist-c12-d3@docker"       → RouteDomain{12, 3}
// "relist-c12-d3-http@docker"  → RouteOther
// "relist-c12-d3"              → RouteDomain{12, 3}   (no provider suffix)
// "relist-c-d3@docker", "relist-c1-dx@docker", "myapp@docker", "" → RouteOther
```

- [x] **Step 2: Run** `go test ./internal/requestmetrics/ -run 'ParseLine|ClassifyRouter'` — Expected: FAIL.

- [x] **Step 3: Implement.** `ParseLine` unmarshals into a private struct (`StartUTC time.Time`, `RouterName string`, `DownstreamStatus int`, `Duration int64`). `ClassifyRouter` cuts the `@provider` suffix, then matches `panel` or `^relist-c(\d+)-d(\d+)$`.

- [x] **Step 4: Run** — Expected: PASS.

- [x] **Step 5: Commit** `feat: parse access log lines and classify their routers`

---

### Task 3: Minute aggregator with watermark and resume cursor

**Files:**
- Create: `internal/requestmetrics/aggregate.go`
- Test: `internal/requestmetrics/aggregate_test.go`

**Interfaces:**
- Consumes: `Histogram`, `Line`, `ClassifyRouter` (Tasks 1–2).
- Produces:
  - `const (ScopeTotal = "total"; ScopePanel = "panel"; ScopeHost = "host")`
  - `type Key struct { Scope string; ComposeID int64; Host string }`
  - `type Counts struct { C1xx, C2xx, C3xx, C4xx, C5xx, Cother uint64; Latency Histogram }` with `func (c *Counts) Add(status int, d time.Duration)`, `func (c *Counts) Merge(o *Counts)`, `func (c *Counts) Requests() uint64`
  - `type Row struct { Key Key; BucketAt time.Time; Counts Counts }`
  - `type HostLookup func(composeID, domainID int64) (host string, ok bool)`
  - `func NewAggregator(flushedThrough time.Time, hosts HostLookup) *Aggregator`
  - `func (a *Aggregator) Add(l Line, offset int64, now time.Time) bool` — false when dropped (minute already closed)
  - `func (a *Aggregator) CloseReady(now time.Time) []Row` — rows sorted by `BucketAt`, then key
  - `func (a *Aggregator) NewFile()` — offsets recorded before it no longer count for the cursor
  - `func (a *Aggregator) Cursor(readOffset int64) (resumeOffset int64, flushedThrough time.Time)`
  - `func (a *Aggregator) Dropped() uint64`

Semantics (from the spec, pinned by the tests):
- Every line → `Key{Scope: total}`. `RoutePanel` → also `Key{Scope: panel}`. `RouteDomain` whose lookup succeeds → also `Key{host, composeID, host}`. Other → total only.
- Watermark = latest `Start` seen. A minute M closes when `watermark ≥ M+90s`, or when `now − lastAddAt ≥ 2m` and `now ≥ M+1m`. Closing is always a prefix: never close M while an older minute stays open.
- `flushedThrough` = start of the oldest open minute; with none open, the end of the newest closed minute (unchanged if nothing ever closed).
- `resumeOffset` = smallest offset of a line added since the last `NewFile` that sits in a still-open minute; with none, `readOffset`.

- [x] **Step 1: Write the failing tests**

```go
func TestSeriesMapping(t *testing.T)      // one line each: panel, domain (lookup ok), domain (lookup miss), -http, unknown → total=5, panel=1, host{12,"a.test"}=1
func TestStatusClasses(t *testing.T)      // 101,200,302,404,503,0,600 → C1xx..C5xx = 1 each, Cother = 2
func TestWatermarkClose(t *testing.T)     // lines at 10:00:10, 10:01:20 → nothing closed; line at 10:01:30 → 10:00 closed
func TestOutOfOrderWithinGrace(t *testing.T) // 10:01:25 then 10:00:59 → both counted, 10:00 has 1 line
func TestLateLineDropped(t *testing.T)    // after 10:00 closed, a 10:00:50 line → Add false, Dropped()==1
func TestIdleClose(t *testing.T)          // one line 10:00:10, lastAdd at 10:00:11, CloseReady(10:02:10) → nothing; CloseReady(10:02:11) → 10:00 closed
func TestCursor(t *testing.T)             // offsets 0,100,200 in 10:00, 300 in 10:01; close 10:00 → Cursor(400) = (300, 10:01)
func TestCursorAfterNewFile(t *testing.T) // line offset 500 in open 10:01, NewFile(), line offset 0 in 10:01 → Cursor(50) = (0, 10:01); with no new-file line → (50, 10:01)
```

- [x] **Step 2: Run** `go test ./internal/requestmetrics/ -run 'Series|Status|Watermark|OutOfOrder|Late|Idle|Cursor'` — Expected: FAIL.

- [x] **Step 3: Implement** `aggregate.go`: open minutes in a `map[int64]*openMinute` (unix minute → `{series map[Key]*Counts; minOffset int64; gen int}`); `NewFile` increments the current gen.

- [x] **Step 4: Run** — Expected: PASS.

- [x] **Step 5: Commit** `feat: aggregate access log lines into minute buckets`

---

### Task 4: Migration and storage

**Files:**
- Create: `internal/migrations/00035_request_metrics.sql` (exact DDL from the spec's Storage section, plus `CREATE INDEX request_metric_rollups_bucket ON request_metric_rollups (bucket_at)` and the same for `request_metric_hourly`; goose `Up`/`Down` like `00029_access_log_settings.sql`)
- Create: `internal/requestmetrics/store.go`
- Test: `internal/requestmetrics/store_test.go` (use the migrated test database helper other packages' store tests use)

**Interfaces:**
- Consumes: `Row`, `Key`, `Counts`, `Histogram` encoding.
- Produces:
  - `type Resolution int` with `Minute`, `Hour`
  - `type Cursor struct { Inode uint64; ResumeOffset int64; FlushedThrough time.Time }`
  - `var ErrInvalidRetention = errors.New("retention must be between 1 and 90 days")`
  - `func NewStore(db *sql.DB) *Store`
  - `func (s *Store) WriteMinutes(ctx, rows []Row, c Cursor) error` — one transaction, `INSERT OR REPLACE` rows and the cursor
  - `func (s *Store) Cursor(ctx) (Cursor, bool, error)`
  - `func (s *Store) LatestHour(ctx) (time.Time, bool, error)` — `MAX(bucket_at)` of hourly
  - `func (s *Store) CompactHour(ctx, hour time.Time) error` — merges minute rows of `[hour, hour+1h)` per key in Go into hourly
  - `func (s *Store) DeleteBefore(ctx, minuteCutoff, hourCutoff time.Time) error`
  - `func (s *Store) Rows(ctx, res Resolution, keys []Key, from time.Time) ([]Row, error)` — ordered by `bucket_at`
  - `func (s *Store) RetentionDays(ctx) (int, error)` / `func (s *Store) SaveRetention(ctx, days int) error`

- [ ] **Step 1: Write the failing tests**

```go
func TestWriteMinutesAndCursorAreOneTransaction(t *testing.T) // rows + cursor written; Cursor() returns it; Rows(Minute,…) returns rows with Latency decoded equal
func TestCompactHour(t *testing.T)    // 3 minute rows of key K in 10:xx, one in 11:00 → hourly 10:00 has summed counts and merged histogram; 11:00 untouched
func TestDeleteBefore(t *testing.T)   // removes only rows strictly older than each cutoff
func TestRetention(t *testing.T)      // default 1; Save 90 ok; Save 0 and 91 → ErrInvalidRetention
func TestRowsFiltersKeys(t *testing.T) // two host keys + total stored; Rows with one host key returns only it
```

- [ ] **Step 2: Run** `go test ./internal/requestmetrics/ -run 'WriteMinutes|Compact|DeleteBefore|Retention|RowsFilters'` — Expected: FAIL.
- [ ] **Step 3: Implement** the migration and `store.go`.
- [ ] **Step 4: Run** the same, then `go test ./internal/migrations/... ./internal/database/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: store request metrics in minute and hourly tables`

---

### Task 5: Domain host lookup and the Ingester

**Files:**
- Create: `internal/requestmetrics/hosts.go`, `internal/requestmetrics/ingest.go`
- Test: `internal/requestmetrics/hosts_test.go`, `internal/requestmetrics/ingest_test.go`

**Interfaces:**
- Consumes: `domains.NewStore(db).ListAll(ctx) ([]domains.Domain, error)` (fields `ID`, `ComposeID`, `Host`); Tasks 1–4.
- Produces:
  - `func NewDomainHosts(list func(context.Context) ([]domains.Domain, error), now func() time.Time) *DomainHosts`
  - `func (h *DomainHosts) Lookup(composeID, domainID int64) (string, bool)` — matches `HostLookup`; reloads when the cache is older than 30 s, or on a miss at most once per 10 s; host lower-cased
  - `type Ingester struct { Dir string; Store *Store; Hosts HostLookup; Now func() time.Time; Poll time.Duration }` (`Poll` defaults to 1 s; missing-file retry is 10 s)
  - `func (in *Ingester) Run(ctx context.Context)`
  - `func (in *Ingester) Step(ctx context.Context) error` — one poll: read, add, close, write, hourly maintenance. `Run` loops over it; tests call it directly.

Behaviour (spec: Ingester, Resuming after a restart):
- First `Step`: load the cursor. Same inode as `access.log` → seek to `ResumeOffset`, aggregator starts with `FlushedThrough`. Otherwise seek to end; `FlushedThrough` = now truncated to the minute.
- Read appended bytes; keep an incomplete last line in a buffer; feed complete lines with their start offsets. Bad lines: skip, count, log at most once a minute.
- If `access.log`'s inode differs from the open file: read the open file to EOF, then open the new file at offset 0 and call `NewFile()`.
- After `CloseReady`, if rows were closed: `WriteMinutes(rows, Cursor{inode of the open file, resumeOffset, flushedThrough})`. On error: log, keep going.
- Hourly maintenance when `FlushedThrough` crosses an hour: compact each complete hour after `LatestHour` (or from `FlushedThrough − 25h` when none), then `DeleteBefore(now − 25h, now − retentionDays×24h)`.

- [ ] **Step 1: Write the failing tests** (temp dir, migrated DB, fake clock)

```go
func TestDomainHostsReload(t *testing.T)            // miss → reload; second miss within 10s → no reload; after 30s → reload; host "A.Test" → "a.test"
func TestIngesterReadsAppendedLines(t *testing.T)   // append 3 lines in minute 10:00, a line at 10:01:31, Step → 1 total row with 3 requests
func TestIngesterPartialLine(t *testing.T)          // half a line, Step, rest of it, Step → counted once
func TestIngesterRotationKeepsOpenMinutes(t *testing.T) // lines in 10:00 open, rename access.log → access-x.log, append to the renamed file, create new access.log with a 10:01:31 line, Step → 10:00 row includes the lines appended to the renamed file; cursor inode = new file
func TestIngesterRotatedFileRemovedWhileOpen(t *testing.T) // rename, remove the old file, new access.log → no error, keeps reading
func TestIngesterMissingFile(t *testing.T)          // no access.log → Step returns nil, nothing written; file appears → read on a later Step
func TestIngesterResumeIsExact(t *testing.T)        // write 1000 lines over 5 minutes; Ingester A steps through half then is discarded; Ingester B (same DB) resumes; sum of total requests in rows == 1000 (lines of the last minute closed by an extra line)
func TestIngesterHourlyMaintenance(t *testing.T)    // lines across 10:59–11:01, cross 11:01:30 → hourly 10:00 exists; old rows past cutoffs deleted
```

- [ ] **Step 2: Run** `go test ./internal/requestmetrics/ -run 'DomainHosts|Ingester'` — Expected: FAIL.
- [ ] **Step 3: Implement** `hosts.go` and `ingest.go`. Inode from `fi.Sys().(*syscall.Stat_t).Ino`.
- [ ] **Step 4: Run** `go test ./internal/requestmetrics/...` — Expected: PASS.
- [ ] **Step 5: Measure** with a throwaway benchmark (`BenchmarkIngest` in `ingest_test.go`, 100k generated lines): report ns/line in the commit body. Keep the benchmark.
- [ ] **Step 6: Commit** `feat: ingest the access log into request metrics`

---

### Task 6: HTTP endpoints, authz, wiring and ADR

**Files:**
- Modify: `internal/traefikconfig/merge.go` (add `AccessLogFiltered`), test in `internal/traefikconfig/merge_test.go`
- Create: `internal/requestmetrics/http.go`, `internal/requestmetrics/http_test.go`
- Modify: `internal/authz/routes.go` (Compose route at `LevelViewer`), `internal/authz/routescan_test.go` (`adminOnly`: the four host-wide patterns), `internal/authz/actions.go` (`"PUT /api/requests/settings": "request_metrics.settings_update"`)
- Modify: `internal/httpapi/httpapi.go` (construct, register, start `Ingester.Run` in the `worker` group next to the Rotator, only when `d.Traefik != nil`)
- Create: `docs/adr/0033-request-metrics-from-the-access-log.md` (decision, considered options — Traefik Prometheus metrics, external stack, keying by `RequestHost` — and consequences, from the spec's Decisions)

**Interfaces:**
- Consumes: `Store`, `Key`, `Row`, `Counts`, `Histogram.Quantile`; `traefikconfig.PathsFor(dataDir).Effective()`, `traefikconfig.AccessLogOn`; `domains.NewStore(db).ListForCompose`.
- Produces:
  - `func AccessLogFiltered(effective []byte) bool` (traefikconfig) — true when `accessLog.filters` exists, or `accessLog.fields.defaultMode` is `drop` (or `fields.names` drops any of `StartUTC`, `RouterName`, `DownstreamStatus`, `Duration`)
  - `func NewService(store *Store, paths traefikconfig.Paths, domains *domains.Store, db *sql.DB) *Service` and `func (s *Service) RegisterRoutes(mux *http.ServeMux)` registering:
    - `GET /api/requests/metrics`
    - `GET /api/requests/panel/metrics`
    - `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/requests/metrics`
    - `GET /api/requests/settings`, `PUT /api/requests/settings`
  - Response JSON exactly as the spec's API section; `hosts` only on the Compose route; `at` in epoch milliseconds (as the existing metrics wire, `internal/metrics/wire.go`); percentiles `null` when a bucket has no requests.

Window handling: map `15m,1h,6h,24h` → `Minute`, `7d,30d,90d` → `Hour` with required retention 7/30/90; else 400 with message `unknown window "x": must be one of …` or `window "30d" needs a retention of at least 30 days`. `partial` = bucket count < window length / bucket size. Compose ownership: 404 `compose not found` unless the Compose belongs to the Environment and Project in the path (same SQL shape as `metrics.Store.ComposeProjectName`). Unknown `host` → 400. Settings PUT body `{ "retentionDays": n }`, 400 on `ErrInvalidRetention`.

- [ ] **Step 1: Write the failing tests**

```go
func TestAccessLogFiltered(t *testing.T)       // base accesslog.yml → false; + filters.statusCodes → true; + fields.names.RouterName: drop → true; headers drop only → false
func TestTotalMetrics(t *testing.T)            // seeded minute rows → summary counts sum, p50/p90/p99 from merged histograms; empty bucket → null percentiles
func TestHourlyWindowPerMinuteSource(t *testing.T) // retention 7, window 7d reads hourly rows
func TestWindowBeyondRetention(t *testing.T)   // retention 7: 30d → 400; unknown "2h" → 400; missing → 400
func TestComposeHosts(t *testing.T)            // two Domains (a.test/, a.test/api, b.test) → hosts ["a.test","b.test"]; host="" merges both; host="a.test" one; host="c.test" → 400
func TestComposeRouteOwnership(t *testing.T)   // compose of project 2 under project 1's path → 404
func TestAccessLogFlags(t *testing.T)          // no effective file → accessLogEnabled false; filtered effective → accessLogFiltered true
func TestSettingsPut(t *testing.T)             // 30 → 200 and GET returns 30; 0 / 91 / bad JSON → 400
```

Plus extend the authz matrix: a Viewer of the Project gets 200 on the Compose route, a member of another Project the existing refusal, a non-admin 403 on `/api/requests/metrics`.

- [ ] **Step 2: Run** `go test ./internal/requestmetrics/ ./internal/traefikconfig/ ./internal/authz/` — Expected: FAIL.
- [ ] **Step 3: Implement** `AccessLogFiltered`, `http.go`, the authz entries, the wiring, and write the ADR.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS (including `TestEveryRouteIsClassified`).
- [ ] **Step 5: Commit** `feat: serve request metrics to admins and Project viewers` (ADR in the same commit)

---

### Task 7: Web data layer, view model and chart units

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`
- Modify: `web/src/components/MetricChart.tsx` (units)
- Create: `web/src/pages/request-metrics-view.ts`, `web/src/pages/request-metrics-view.test.ts`

**Interfaces:**
- Produces:
  - `type RequestWindow = '15m' | '1h' | '6h' | '24h' | '7d' | '30d' | '90d'`
  - `interface RequestBucket { at: number; c1xx…cother: number; p50Ms: number | null; p90Ms: number | null; p99Ms: number | null }`
  - `interface RequestMetrics { window: RequestWindow; partial: boolean; accessLogEnabled: boolean; accessLogFiltered: boolean; retentionDays: number; hosts?: string[]; summary: RequestSummary; buckets: RequestBucket[] }`
  - `api.requestMetrics(window)`, `api.panelRequestMetrics(window)`, `api.composeRequestMetrics(location: ComposePath, window, host?: string)`, `api.requestMetricSettings()`, `api.saveRequestMetricSettings({ retentionDays })`
  - `MetricUnit` gains `'requestsPerMinute'` (formats `12.3 req/min`) and `'milliseconds'` (`410 ms`, `1.2 s` at ≥ 1000)
  - `windowsFor(retentionDays: number): RequestWindow[]`
  - `statusSeries(m: RequestMetrics): MetricSeries[]` (2xx/3xx/4xx/5xx, hourly windows divided by 60)
  - `latencySeries(m: RequestMetrics): MetricSeries[]` (p50/p90/p99; a `null` is a gap — omitted point, the same rule `monitor-view.ts` uses)
  - `summaryItems(m: RequestMetrics): { label: string; value: string }[]` — Requests, 5xx rate (`c5xx / requests`, `—` when 0 requests), p50, p90, p99

- [ ] **Step 1: Write the failing tests** in `request-metrics-view.test.ts`:

```ts
test('windowsFor', () => {
  expect(windowsFor(1)).toEqual(['15m', '1h', '6h', '24h']);
  expect(windowsFor(7)).toEqual(['15m', '1h', '6h', '24h', '7d']);
  expect(windowsFor(90)).toEqual(['15m', '1h', '6h', '24h', '7d', '30d', '90d']);
});
test('hourly buckets read per minute', ...)    // window 7d, c2xx 600 → point value 10
test('nullPercentilesAreGaps', ...)            // bucket with p99Ms null → no p99 point at that `at`
test('summary 5xx rate', ...)                  // 5 of 1000 → '0.5%'; 0 requests → '—'
```

and in the existing MetricChart test file (or a new one), `formatMetricValue(12.34, 'requestsPerMinute') === '12.3 req/min'`, `formatMetricValue(1250, 'milliseconds') === '1.25 s'`, `formatMetricValue(410, 'milliseconds') === '410 ms'`.

- [ ] **Step 2: Run** `cd web && npm test -- request-metrics-view MetricChart` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `cd web && npm run typecheck && npm test` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the request metrics view model and chart units`

---

### Task 8: Monitoring › Requests and Panel tabs, settings drawer

**Files:**
- Create: `web/src/components/RequestMetricsPanel.tsx` (+ `RequestMetricsPanel.test.tsx`)
- Create: `web/src/components/RequestMetricsSettingsDrawer.tsx`
- Modify: `web/src/pages/MonitoringPage.tsx`

**Interfaces:**
- Consumes: Task 7.
- Produces: `RequestMetricsPanel({ swrKey, load, extra }: { swrKey: string; load: (w: RequestWindow) => Promise<RequestMetrics>; extra?: ReactNode })` — window `Segmented` (from `windowsFor(retentionDays)` of the last response, default `1h`), summary row (`Statistic`), Status and Latency `MetricChart`s, `Alert` when `!accessLogEnabled` (link to `/traefik`, text: "The Access log is off. Turn it on in Traefik › Settings to collect request metrics."), warning `Alert` when `accessLogFiltered` ("The Access log is filtered, so these numbers cover only the requests it logs."), error `Alert` with Retry as on MonitoringPage.

- [ ] **Step 1: Write the failing tests** (`RequestMetricsPanel.test.tsx`, mocked `load`): renders summary values; access-log-off Alert replaces the charts; filtered warning shows above them; retention 7 offers `7d` but not `30d`.
- [ ] **Step 2: Run** `cd web && npm test -- RequestMetricsPanel` — Expected: FAIL.
- [ ] **Step 3: Implement** the panel; the drawer (`InputNumber` 1–90, Save; when the new value is lower than the current one, a confirm warns "History older than N days will be deleted within the hour."); MonitoringPage gets `Tabs` — `Host` (the current content, unchanged), `Requests` (panel with the Settings button as `extra`), `Panel`. Keep `?window=` for the Host tab; add `?tab=`.
- [ ] **Step 4: Run** `cd web && npm run typecheck && npm test` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: show request metrics on the Monitoring page`

---

### Task 9: Compose › Monitor requests section and embedded build

**Files:**
- Modify: `web/src/components/ServiceMonitor.tsx` (append the section) and its test
- Regenerate: embedded frontend (`task build-web`)

**Interfaces:**
- Consumes: `RequestMetricsPanel`, `api.composeRequestMetrics`.

- [ ] **Step 1: Write the failing tests**: with `hosts: []` the Requests section is absent; with two hosts a `Select` lists "All hosts", then each host; choosing one calls `composeRequestMetrics(location, window, 'a.test')`.
- [ ] **Step 2: Run** `cd web && npm test -- ServiceMonitor` — Expected: FAIL.
- [ ] **Step 3: Implement** a "Requests" section under the Service charts: `Select` of hosts as `extra` on `RequestMetricsPanel`; hidden while the response's `hosts` is empty.
- [ ] **Step 4: Run** `cd web && npm run typecheck && npm test`, then `task build-web` and `go test ./...` — Expected: PASS.
- [ ] **Step 5: Commit** the feature as `feat: show request metrics per host on the Compose Monitor tab`, then the regenerated assets as `chore: rebuild the embedded frontend`.

---

### Task 10: Live check

**Files:** none committed, unless the check finds a bug (then fix it with a test in the owning task's files).

- [ ] **Step 1:** Run the panel with a real `traefik:v3.7` (the setup `task probe:traefik-config` uses), Access log on, two Domains on one Compose (`a.test/`, `a.test/api`) plus the panel host.
- [ ] **Step 2:** Send a known mix with `curl`/`hey`: 500 × 200 to `a.test/`, 50 × 404 to `a.test/api`, 20 to the panel, 30 with a random `Host`. Wait 2 minutes.
- [ ] **Step 3:** Check via the API: total = 600; panel = 20; Compose `host=""` = 550 with c2xx 500, c4xx 50; `a.test` the same.
- [ ] **Step 4:** Burst ~50k requests, restart the panel container mid-burst, wait 2 minutes: total for the burst minutes equals the number sent.
- [ ] **Step 5:** Report the measured CPU (`docker stats` during the burst) and ns/line from Task 5. Browser check of the three views: do it if headless Chrome works in this environment; otherwise say it was not done.

---

## Self-review notes

- Spec coverage: Ingester (5), mapping (2–3), watermark (3), histogram (1), storage + retention (4–5), resume (3–5), filters (6), API + authz (6), UI three views + settings (7–9), error table (3, 5, 6, 8), resource estimate (5, 10), testing (all), ADR (6).
- Spec statement "reloaded when Domains change" is met by polling (Global Constraints); spec route shorthand replaced by the real Compose path.
