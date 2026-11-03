# Traefik Logs and Access Log Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Traefik logs at INFO, keeps a JSON Access log on disk, and the panel rotates it daily and shows logs, files and the retention setting on a new Traefik page.

**Architecture:** The install `docker-compose.yml` gains Traefik flags and `logging:` limits. A new Go package, `internal/accesslog`, holds four things: a single-row settings store, a `Rotator` that runs in the panel process, the file listing, and the HTTP service for `/api/traefik/*`. The package reaches Traefik only through a small `Traefik` interface. `internal/dockerexec` implements that interface with the docker CLI, reusing the install-Traefik lookup that Migration already does. The frontend adds a Traefik page modelled on `CertificatesPage.tsx`.

**Tech Stack:** Go (`compress/gzip`, `encoding/json`, goose SQL migrations), docker CLI, React + antd + SWR + vitest.

**Spec:** `.scratch/traefik-logging/spec.md`

## Global Constraints

- Never edit a committed migration. The new one is `internal/migrations/00029_access_log_settings.sql`.
- Access log directory: `<data dir>/.traefik/access/`, which Traefik sees as `/letsencrypt/access/`. The live file is `access.log`.
- Rotated names: `access-YYYY-MM-DD.log`, then gzipped to `access-YYYY-MM-DD.log.gz`. A collision gets `-2`, `-3` and so on before `.log`. The date is the UTC day before the run.
- Download/list name pattern, exact: `^access(-\d{4}-\d{2}-\d{2}(-\d+)?)?\.log(\.gz)?$`. Files that do not match are never touched or listed.
- Rotation: at 00:00 UTC daily and once at startup. A plain `access-*.log` is gzipped only once its mtime is more than 5 minutes old.
- Retention: default 90 days. `retention_days >= 1`. `keep_forever` skips deletion. Delete a file when its date is before today minus `retention_days`.
- Traefik log tail: 500 lines, `--timestamps`.
- Every rotation error is logged and stored in `last_error`. None of them is fatal to the panel.
- Commit messages: Conventional Commits, no scope, no `Co-Authored-By` / session trailers (`docs/agents/commit-conventions.md`).
- Before any UI work, read `https://ant.design/llms.txt` and the pages for Alert, Card, Table, Segmented, InputNumber, Switch, Button and Typography. Check them against the `antd` version in `web/package.json`. Follow `docs/GUIDELINES.md`.
- UI copy fixed by the spec, verbatim:
  - Banner: "Access log is not on. This install's Traefik was started without the access-log flags; run a Panel update."
  - Retention warning: "Thai law requires service providers to keep traffic data for at least 90 days."
  - Missing container: "Traefik container `relist-traefik` not found"

## Review Focus

1. **Fresh install, no `access.log` yet** (Traefik has received no request). `RunOnce` succeeds and changes nothing, and the page lists no files. Test in Task 3.
2. **A crash mid-gzip leaves `access-D.log.gz.tmp`.** The next run must not list it or count it, must replace it, and must keep the plain file until the `.gz` is complete. Test in Task 3.
3. **Traefik writes some log lines to stderr.** The Traefik log card must still show them, so `Logs` captures stdout and stderr together. Test in Task 4.
4. **An encoded traversal name such as `..%2Fpanel.db` or `access.log%2F..%2F..%2Fpanel.db`** must get 404 and never open a file. Test in Task 5.
5. **The panel runs without the install stack** (`go run`, dev, or no Compose project label). The page shows "not found" and `accessLogEnabled: false` instead of 500, and the Rotator keeps working on the directory. Test in Task 5.

---

### Task 1: Probe `traefik:v3.7`, then change the install compose

**Files:**
- Modify: `docker-compose.yml` (the `traefik` `command:`, plus `logging:` on `traefik` and `panel`)
- Modify: `install_compose_test.go`
- Modify: `README.md` (env list near line 181, "Upgrading an existing install" near line 331)
- Modify: `.scratch/traefik-logging/spec.md` (append `## Comments` with the probe results)

**Interfaces:**
- Produces: the Traefik flags, verbatim from the spec's "Install compose" section. Later tasks rely on `--accesslog.filepath=/letsencrypt/access/access.log` and `--entrypoints.websecure.forwardedheaders.trustedips=` as the prefixes they search for.

- [ ] **Step 1: Run the probe.** Start a throwaway `traefik:v3.7` with the spec's flags, a bind-mounted temp dir as `/letsencrypt`, and a `whoami` backend. Use a scratch compose under the scratchpad, not in the repo. Check:
  1. Set `trustedips` to the client's peer IP and send `X-Forwarded-For: 203.0.113.9`. The access log's `ClientHost` must be `203.0.113.9`. Then remove the peer from `trustedips` and send the same header: `ClientHost` must be the peer IP.
  2. With `trustedips=` (empty), Traefik must start without error.
  3. With `access/` missing, check whether Traefik creates it and writes `access.log`.
  4. Run `mv access.log access-x.log`, then `docker kill --signal=USR1 <id>`, then send a request. The new line must land in a fresh `access.log`.
  5. Record one ERROR, WARN and INFO line exactly as `docker logs --timestamps` shows them. Note whether Traefik writes to stdout or stderr. Task 6's level filter uses the level token found here.
- [ ] **Step 2: Record the results** under `## Comments` in the spec.
  - If check 2 failed: the default becomes `127.0.0.1/32` in the flags and the test below.
  - If check 3 failed: add `.traefik/access` to the directories the panel creates at startup in Task 5. Also mount it on its own like `dynamic/`.
  - If check 1 or 4 failed: stop and report. The design does not hold.
- [ ] **Step 3: Write the failing test** `TestInstallComposeTraefikLogging` in `install_compose_test.go`, in the style of `TestInstallComposeMountsTraefikDynamicDir`. Extend the parsed struct with `Logging struct{ Driver string; Options map[string]string }`. Assert:
  - `traefik.Command` contains each of the 8 flags listed in the spec, verbatim, including `${TRAEFIK_TRUSTED_IPS:-}` for both entrypoints.
  - Both `traefik` and `panel` have `Logging.Driver == "json-file"` and `Options == {"max-size": "10m", "max-file": "5"}`.
- [ ] **Step 4: Run** `go test -run 'TestInstallCompose|TestEmbeddedInstallFile' .`. Expected: `TestInstallComposeTraefikLogging` FAILS.
- [ ] **Step 5: Edit `docker-compose.yml`.** Add each new flag with a one-line comment in the file's existing comment style. Point the trusted-IPs comment at `.env`.
- [ ] **Step 6: Rebuild the embedded copy.** `installFile` embeds `docker-compose.yml`. Run `go test -run 'TestInstallCompose|TestEmbeddedInstallFile' .`. Expected: PASS.
- [ ] **Step 7: Update the README.**
  - Add `TRAEFIK_TRUSTED_IPS` to the env list after `TRAEFIK_HTTPS_PORT`: comma-separated IPs/CIDRs of proxies in front of Traefik, such as Cloudflare's ranges; X-Forwarded-For is trusted only from them; empty (the default) trusts no one.
  - Under "Upgrading an existing install", note that the Access log and the new log limits start only after a Panel update, and that the Traefik page warns until then.
- [ ] **Step 8: Run** `go test ./internal/panelupdate/...`. Expected: PASS. The invariants must still accept the changed install file.
- [ ] **Step 9: Commit:** `feat: turn on Traefik access log and cap container logs`

### Task 2: Settings migration and store

**Files:**
- Create: `internal/migrations/00029_access_log_settings.sql` (goose Up/Down, with the table from the spec's "Data model", verbatim, plus `DROP TABLE` in Down)
- Create: `internal/accesslog/store.go`
- Test: `internal/accesslog/store_test.go`

**Interfaces:**
- Produces:
  ```go
  package accesslog
  type Settings struct {
      RetentionDays int
      KeepForever   bool
      LastRotatedAt *time.Time
      LastError     string
  }
  func NewStore(db *sql.DB) *Store
  func (s *Store) Get(ctx context.Context) (Settings, error)
  func (s *Store) SaveRetention(ctx context.Context, days int, forever bool) error // days < 1 → ErrInvalidRetention
  func (s *Store) RecordRun(ctx context.Context, at time.Time, runErr string) error
  var ErrInvalidRetention = errors.New("retention must be at least 1 day")
  ```
  `last_rotated_at` is stored as RFC 3339 UTC text, like the other tables.

- [ ] **Step 1: Write the failing tests.** Open the DB with `database.OpenAndMigrate`, as the certificates tests do.
  - `TestStoreDefaults`: `Get` returns `{RetentionDays: 90, KeepForever: false, LastRotatedAt: nil, LastError: ""}`.
  - `TestStoreSaveRetention`: save `(30, true)`, then `Get` returns 30 and true. `(0, false)` returns `ErrInvalidRetention`, and the row is unchanged.
  - `TestStoreRecordRun`: `RecordRun(t0, "boom")`, then `Get` returns `LastRotatedAt == t0` (UTC) and `LastError == "boom"`. `RecordRun(t1, "")` clears the error.
- [ ] **Step 2: Run** `go test ./internal/accesslog/`. Expected: FAIL (package missing).
- [ ] **Step 3: Implement** the migration and `store.go`. Add a package doc comment on `store.go` pointing at ADR-0027.
- [ ] **Step 4: Run** `go test ./internal/accesslog/ ./internal/database/...`. Expected: PASS.
- [ ] **Step 5: Commit:** `feat: store access log retention settings`

### Task 3: Rotator and file listing

**Files:**
- Create: `internal/accesslog/rotate.go`
- Create: `internal/accesslog/files.go`
- Test: `internal/accesslog/rotate_test.go`, `internal/accesslog/files_test.go`

**Interfaces:**
- Consumes: `Store` from Task 2.
- Produces:
  ```go
  // Traefik is the install's Traefik container, as the panel reaches it.
  type Traefik interface {
      Reopen(ctx context.Context) error                 // send USR1
      Logs(ctx context.Context, tail int) (string, error)
      Args(ctx context.Context) ([]string, error)
  }
  var ErrNoTraefik = errors.New("no Traefik container in the install")

  func Dir(dataDir string) string // filepath.Join(dataDir, ".traefik", "access")

  type Rotator struct {
      Dir      string
      Store    *Store
      Traefik  Traefik
      Now      func() time.Time
      QuietFor time.Duration // 5 * time.Minute in production
  }
  func (r *Rotator) RunOnce(ctx context.Context) error
  func (r *Rotator) Run(ctx context.Context)

  type File struct {
      Name string
      Date string // "YYYY-MM-DD", "" for access.log
      Size int64
  }
  func ValidName(name string) bool
  func List(dir string) (files []File, total int64, err error) // newest first; access.log first
  ```

- [ ] **Step 1: Write the failing tests** in `rotate_test.go`. Use a temp dir, a fixed `Now`, a fake `Traefik` that records `Reopen` calls and can fail, and `QuietFor: 5*time.Minute`. Set file mtimes with `os.Chtimes`. Each test asserts the exact directory listing afterwards. `Now` is 2026-11-03T00:00:30Z unless stated.
  - `TestRotateRenamesYesterday`: `access.log` has a first line `{"StartUTC":"2026-11-02T10:00:00Z"}`. After the run: `access-2026-11-02.log` exists, `access.log` is gone, and `Reopen` was called once.
  - `TestRotateSkipsTodaysFile`: the first line has today's date, so nothing changes and there is no `Reopen`.
  - `TestRotateNoFileIsNoop`: the directory is empty or missing. `RunOnce` returns nil, nothing is created, and there is no `Reopen`.
  - `TestRotateCatchUpUsesLatestDate`: `Now` is 2026-11-06T09:00Z and the first line is from 2026-11-02. The file becomes `access-2026-11-05.log`.
  - `TestRotateUnparsableFirstLineUsesMtime`: the first line is `garbage` and the mtime is 2026-11-01. The file is rotated.
  - `TestRotateCollisionSuffix`: `access-2026-11-02.log.gz` already exists, so the file becomes `access-2026-11-02-2.log`.
  - `TestGzipWaitsForQuiet`: a plain file with mtime `Now-1m` stays plain. One with mtime `Now-6m` becomes `.log.gz`, which decompresses to the original bytes, and the plain file is removed.
  - `TestGzipLeftoverTmpReplaced`: `access-2026-11-01.log` plus a truncated `access-2026-11-01.log.gz.tmp`. The result is a valid `.gz`, and no `.tmp` remains.
  - `TestReopenFailureKeepsFile`: `Reopen` returns an error. The renamed file exists, `RunOnce` returns the error, and `Store.Get().LastError` is non-empty.
  - `TestRetentionDeletesOld`: retention is 90 and `Now` is 2026-11-03. `access-2026-08-04.log.gz` is deleted, and `access-2026-08-05.log.gz` is kept. 2026-11-03 minus 90 days is 2026-08-05.
  - `TestRetentionForeverKeepsAll`: `KeepForever` is true, and a 2020 file is kept.
  - `TestUnmatchedFilesUntouched`: `notes.txt` and `access.log.bak` are untouched by every step.
  - `TestRecordRunOnSuccess`: after a clean run, `LastRotatedAt == Now` and `LastError == ""`.
- [ ] **Step 2: Write the failing tests** in `files_test.go`.
  - `TestValidName`: it accepts `access.log`, `access-2026-11-02.log`, `access-2026-11-02.log.gz` and `access-2026-11-02-3.log.gz`. It rejects `../panel.db`, `access.log/../x`, `access-2026-11-02.log.gz.tmp`, `Access.log` and `""`.
  - `TestListOrderAndTotal`: `access.log` comes first, then dated files newest first. `total` is the sum of the listed files' sizes. A `.tmp` file and `notes.txt` are excluded. A missing directory gives an empty list and no error.
- [ ] **Step 3: Run** `go test ./internal/accesslog/`. Expected: FAIL.
- [ ] **Step 4: Implement** `files.go` (`ValidName` uses the exact pattern from Global Constraints) and `rotate.go`. `RunOnce` follows the spec's four steps in order.
  - Rename only when needed. If the rename happens, call `Reopen`.
  - Gzip each quiet plain file by writing `<name>.gz.tmp`, fsyncing it, renaming it to `.gz`, then removing the plain file.
  - Apply retention.
  - Finish with `Store.RecordRun(Now, errString)`. Keep the first error, but go on to later steps after a `Reopen` failure.
  - `Run` calls `RunOnce`, then sleeps until the next 00:00 UTC. It sleeps only 10 minutes when a plain `access-*.log` is left after the run. It returns when `ctx` is done. Every error goes through `log.Printf("accesslog: ...")`.
- [ ] **Step 5: Run** `go test ./internal/accesslog/`. Expected: PASS.
- [ ] **Step 6: Commit:** `feat: rotate, gzip and expire the Traefik access log`

### Task 4: Docker adapter for the install's Traefik

**Files:**
- Create: `internal/dockerexec/traefik.go`
- Modify: `internal/dockerexec/migration.go:327-348`. Move the body of `(*MigrationDocker).traefik` into a package function both types call. `StopTraefik` and `StartTraefik` keep their behaviour.
- Test: `internal/dockerexec/traefik_test.go`

**Interfaces:**
- Consumes: `accesslog.Traefik` and `accesslog.ErrNoTraefik` from Task 3.
- Produces:
  ```go
  func NewTraefikDocker() *TraefikDocker
  var _ accesslog.Traefik = (*TraefikDocker)(nil)
  // shared lookup, used by MigrationDocker.traefik too:
  func installTraefik(ctx context.Context, docker func(ctx context.Context, args ...string) (string, error), hostname string) (string, error)
  ```
  `TraefikDocker` has the same `runDocker` and `hostname` fields as `MigrationDocker`, so tests can reuse the `recorder` from `migration_test.go`.

- [ ] **Step 1: Write the failing tests.** Use the `recorder`, with answers for the project-label inspect and `ps` argv already used by `MigrationDocker.traefik`.
  - `TestTraefikReopenArgv`: the last call is `docker kill --signal=USR1 <id>`.
  - `TestTraefikLogsArgvAndStderr`: the call is `docker logs --tail 500 --timestamps <id>`. Stdout and stderr both reach the result. Extend `recorder` with an optional `stderr` answer map if needed.
  - `TestTraefikArgs`: inspect returns `--log.level=INFO\n--accesslog=true\n` for `docker inspect --format {{range .Args}}{{println .}}{{end}} <id>`. The result is the two flags.
  - `TestTraefikNotFound`: `ps` returns empty, or the hostname has no project label. Every method's error satisfies `errors.Is(err, accesslog.ErrNoTraefik)`.
  - The existing migration tests still pass unchanged.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/ -run 'Traefik|Migration'`. Expected: the new tests FAIL.
- [ ] **Step 3: Implement.**
  - The not-found branches of `installTraefik` wrap `accesslog.ErrNoTraefik`, so the existing migration error text still appears in the message.
  - `Logs` calls `runDocker` with the same buffer for stdout and stderr.
- [ ] **Step 4: Run** `go test ./internal/dockerexec/`. Expected: PASS.
- [ ] **Step 5: Commit:** `feat: reach the install's Traefik for logs, args and USR1`

### Task 5: HTTP service and wiring

**Files:**
- Create: `internal/accesslog/http.go`
- Test: `internal/accesslog/http_test.go`
- Modify: `internal/httpapi/httpapi.go`
  - Add a `Traefik accesslog.Traefik` field to `Deps`.
  - In `NewHandler`: register the service, and when `d.Traefik != nil`, start `Rotator.Run` on the `worker` WaitGroup next to the other workers.
- Modify: `main.go`: `Traefik: dockerexec.NewTraefikDocker()` in the `httpapi.Deps` literal.

**Interfaces:**
- Consumes: `Store`, `Rotator`, `List`, `ValidName`, `Dir` and `Traefik` from Tasks 2–4.
- Produces, for Task 6. Responses are JSON with camelCase fields:
  - `func New(db *sql.DB, dataDir string, traefik Traefik) *Service`
  - `func (s *Service) RegisterRoutes(mux *http.ServeMux)`
  - `GET /api/traefik/logs` → `{"logs": string, "found": bool}`
  - `GET /api/traefik/status` → `{"found": bool, "accessLogEnabled": bool, "trustedIps": string}`
  - `GET /api/traefik/access-logs` → `{"files": [{"name", "date", "size"}], "totalSize": int}`. `date` is `null` for `access.log`.
  - `GET /api/traefik/access-logs/{name}` → the file, with `Content-Disposition: attachment; filename="<name>"`.
  - `GET /api/traefik/access-log-settings` → `{"retentionDays", "keepForever", "lastRotatedAt" (string|null), "lastError"}`
  - `PUT /api/traefik/access-log-settings`, body `{"retentionDays", "keepForever"}` → the same shape as GET. A bad value gives `400 {"error": ..., "field": "retentionDays"}`.
  - Errors use `writeError`/`writeJSON`, copied in the style of `internal/certificates/http.go`.

- [ ] **Step 1: Write the failing tests.** Use a `newService` helper like the certificates one, with a fake `Traefik`.
  - `TestLogsFound` and `TestLogsNotFound`: the fake returns `ErrNoTraefik`, and the response is `200 {"logs":"","found":false}`. Any other error gives 500.
  - `TestStatusParsesArgs`:
    - Args containing `--accesslog.filepath=/letsencrypt/access/access.log` and `--entrypoints.websecure.forwardedheaders.trustedips=10.0.0.0/8` give `accessLogEnabled: true, trustedIps: "10.0.0.0/8"`.
    - Args without them give `false` and `""`.
    - `ErrNoTraefik` gives `found: false`.
  - `TestListAccessLogs`: files in `Dir(dataDir)` come back in `List` order with `totalSize`.
  - `TestDownloadAccessLog`: the bytes are returned, and so is the attachment header.
  - `TestDownloadRejectsBadNames`: `/api/traefik/access-logs/..%2Fpanel.db`, `/api/traefik/access-logs/access.log%2F..%2F..%2Fpanel.db`, `notes.txt` and a valid name that does not exist all give 404.
  - `TestSettingsRoundTrip`:
    - GET returns the defaults.
    - PUT `{30, true}` returns 30 and true.
    - PUT `{0, true}` returns 400 with field `retentionDays`; the days must be ≥ 1 even when forever is on.
    - A malformed JSON body gives 400.
- [ ] **Step 2: Run** `go test ./internal/accesslog/`. Expected: FAIL.
- [ ] **Step 3: Implement `http.go`.** Serve downloads with `os.Open` and `http.ServeContent`, using content type `application/gzip` for `.gz` and `text/plain; charset=utf-8` otherwise. Then do the wiring in `httpapi.go` and `main.go`.
- [ ] **Step 4: Run** `go test ./internal/accesslog/ ./internal/httpapi/ .`. Expected: PASS. The existing httpapi tests construct `Deps` without `Traefik`, and the feature's routes still register with a nil `Traefik`. Make nil behave like `ErrNoTraefik` in the service.
- [ ] **Step 5: Commit:** `feat: serve Traefik logs and access log files to the panel`

### Task 6: Traefik page

**Files:**
- Modify: `web/src/data/types.ts`. Add `TraefikLogs`, `TraefikStatus`, `AccessLogFile`, `AccessLogList` and `AccessLogSettings`, matching Task 5's shapes.
- Modify: `web/src/data/api.ts`. Next to the certificate calls, add:
  - `traefikLogs()`, `traefikStatus()`, `listAccessLogs()`, `accessLogSettings()`
  - `saveAccessLogSettings(input: { retentionDays: number; keepForever: boolean })`
  - `accessLogDownloadUrl(name: string): string`, which returns `/api/traefik/access-logs/${encodeURIComponent(name)}`
- Create: `web/src/pages/traefik-view.ts`, `web/src/pages/traefik-view.test.ts`
- Create: `web/src/pages/TraefikPage.tsx`
- Modify: `web/src/app/paths.ts` (`traefik: '/traefik'` and `{ path: 'traefik' }`), `web/src/app/routes.tsx`, and `web/src/app/AppShell.tsx`. AppShell needs the menu item after Certificates, the selected key, `navigate`, and the breadcrumb `Overview / Traefik`. Mirror the `certificates` entries at the lines found by `grep -n certificates`.

**Interfaces:**
- Consumes: Task 5's JSON. The level token comes from Task 1's probe note in the spec's `## Comments`.
- Produces:
  ```ts
  export type LogLevel = 'all' | 'error' | 'warn' | 'info';
  export function filterLogLines(text: string, level: LogLevel): string;
  export function showRetentionWarning(days: number, keepForever: boolean): boolean; // days < 90 && !keepForever
  ```
  `filterLogLines` keeps the lines at or above the chosen level, matched on the probe's level token for each line: `error` ⊂ `warn` ⊂ `info`, and `all` keeps every line, including untagged ones.

- [ ] **Step 1: Read** `https://ant.design/llms.txt` and the component pages listed in Global Constraints.
- [ ] **Step 2: Write the failing tests** in `traefik-view.test.ts`. Build fixture lines from Task 1's recorded samples (ERROR, WARN, INFO, plus one untagged continuation line).
  - `filterLogLines(text, 'error')` keeps only the ERROR line.
  - `'warn'` keeps the ERROR and WARN lines.
  - `'info'` keeps three lines.
  - `'all'` keeps all four.
  - `showRetentionWarning`: `(89,false)` → true, `(90,false)` → false, `(1,true)` → false.
- [ ] **Step 3: Run** `pnpm --dir web test traefik-view`. Expected: FAIL.
- [ ] **Step 4: Implement** `traefik-view.ts`, then `TraefikPage.tsx` per the spec's "UI — Traefik page", using the verbatim copy from Global Constraints.
  - Fetch with SWR, as `CertificatesPage.tsx` does.
  - Show the log in a monospace pre-wrapped block with a `Segmented` level filter and a Refresh button.
  - Show a Table of files, with sizes through the existing `formatBytes` from `web/src/components/MetricChart`. Download is an `<a href={accessLogDownloadUrl(name)} download>`, and the total size.
  - In the settings form, the Keep forever switch disables the `InputNumber` (min 1). Save calls `saveAccessLogSettings`, then revalidates.
  - Show the last rotation time and error under the table when present.
- [ ] **Step 5: Run** `pnpm --dir web test` and `pnpm --dir web typecheck`. Expected: PASS.
- [ ] **Step 6: Check in the browser.** Run the app via the `run` skill: open `/traefik` and confirm that all three cards render and that the banner shows on a dev install with no Traefik. Report the result. If the browser cannot be driven, say so.
- [ ] **Step 7: Commit:** `feat: add Traefik page with logs, access log files and retention`

### Task 7: Docs

**Files:**
- Create: `docs/adr/0027-access-log-rotated-by-panel.md`, in the style of ADR-0022:
  - Problem: no request record, and Docker logs are lost on recreate and rotated by size.
  - Decision: a JSON file in `.traefik/access`, rotated by the panel with USR1, and retention in `panel.db`.
  - Considered options: Docker json logs, host `logrotate`, a sidecar.
  - Consequences: it moves with a Migration; `TRAEFIK_TRUSTED_IPS` sits in `.env` until panel-managed static config arrives; files are UTC-dated; deletion errs late.
- Modify: `CONTEXT.md`. Add the **Access log** entry, verbatim from the spec, after **Certificate**.

- [ ] **Step 1: Write both files.**
- [ ] **Step 2: Run** `go test ./... && pnpm --dir web test && pnpm --dir web typecheck`. Expected: PASS. This is the final full check.
- [ ] **Step 3: Commit:** `docs: record the access log decision and term`
