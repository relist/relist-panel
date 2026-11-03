# Spec — Traefik logs and Access logs kept on disk, rotated by the panel

Date: 2026-11-03.
Status: open — branch claude/traefik-logging-certificates-9e3daa

## Problem

Traefik in the install `docker-compose.yml` logs almost nothing and nothing is visible
from the panel:

- No `--log.level`, so Traefik runs at its default `ERROR`.
- No access log at all: there is no record of which client requested which host.
- Let's Encrypt (ACME) failures appear only in `docker logs relist-traefik`, which the
  operator must reach over SSH. ADR-0013 already records that "Let's Encrypt failures are
  invisible to the panel".
- Neither install container sets a `logging:` limit. Docker's default `json-file` driver
  never rotates, so turning on more logging to stdout would fill the disk.

Thai law (Computer Crime Act section 26) requires a service provider to keep computer
traffic data for at least 90 days. Docker's stdout logs cannot do this. Their rotation is
by size, not by day, and they are deleted whenever the container is recreated, which
happens on every Panel update.

## Goal and success criteria

- Traefik logs at `INFO`, and both install containers keep at most 5 × 10 MB of Docker logs.
- Traefik writes an **Access log** as JSON lines to a file in the data directory, so it
  survives container recreation and moves with a Migration.
- The panel rotates the Access log once a day, gzips the rotated file, and deletes files
  older than the retention setting. The default is 90 days. The operator may choose any
  number of days ≥ 1 or keep files forever. Under 90 days shows a warning.
- The Access log records the TCP peer (`ClientAddr`) and the client address
  (`ClientHost`). When the peer is listed in `TRAEFIK_TRUSTED_IPS`, the client address is
  taken from `X-Forwarded-For`. By default the list is empty and no peer is trusted.
- A new host-wide **Traefik** page shows:
  - the last 500 lines of Traefik's own log, filterable by level, where ACME errors show up;
  - the Access log files with their sizes, each one downloadable;
  - the retention setting;
  - a warning when the running Traefik was not started with the access-log flags, for an
    install that has not had a Panel update.

## Out of scope / follow-up

- **Panel-managed Traefik static config.** This is the next spec, with its own ADR. The
  panel renders Traefik's static config file so `TRAEFIK_TRUSTED_IPS`, the log level and
  similar settings move from `.env` into the UI, and the install compose and `.env` get
  smaller. Traefik takes static config from one source only (file, CLI flags or env), so
  that spec moves all of it. It must design restart-on-save, bootstrap and update ordering,
  and lockout recovery. Not done here.
- Cloudflare's IP ranges as a built-in default or toggle. The operator writes them into
  `TRAEFIK_TRUSTED_IPS` by hand.
- ACME status per Domain: reading `acme.json`, and tying ACME errors to the Domain that
  caused them. Errors are visible in the Traefik log card only.
- Searching or viewing Access log contents in the UI. Files are downloaded.
- Live streaming of Traefik's log. The card has a Refresh button.
- Integrity proofs (hashes, signing) of rotated files.
- Time synchronisation (NTP) of the host. This is the operator's responsibility.
- Shipping logs to an external system (syslog, Loki, S3).

## Domain changes

- `CONTEXT.md` gains:

  **Access log**:
  The record of every HTTP request Traefik receives, kept as JSON lines in
  `<data dir>/.traefik/access/`, one gzipped file per day, and deleted by the panel after
  the retention period. It is host-wide, not per Compose (ADR-0026).
  _Avoid_: Request log, Traffic log (as the entity name). "Traefik log" means Traefik's own
  log on stdout, not this.

- ADR-0026 records the decision: the Access log goes to a file in the data directory
  rather than Docker logs, and the panel rotates it rather than host `logrotate` or a
  sidecar.

## Install compose (`docker-compose.yml`)

Panel update replaces this file, so every updated install gets these changes.

`traefik` `command:` gains:

```
- --log.level=INFO
- --log.nocolor=true
- --accesslog=true
- --accesslog.filepath=/letsencrypt/access/access.log
- --accesslog.format=json
- --accesslog.fields.headers.defaultmode=drop
- --accesslog.fields.headers.names.User-Agent=keep
- --entrypoints.web.forwardedheaders.trustedips=${TRAEFIK_TRUSTED_IPS:-}
- --entrypoints.websecure.forwardedheaders.trustedips=${TRAEFIK_TRUSTED_IPS:-}
```

- The Access log uses the existing `.traefik` → `/letsencrypt` mount, so it is at
  `<data dir>/.traefik/access/` on the host.
- Request headers are dropped except `User-Agent`, so `Authorization` and `Cookie` never
  reach the file. The default fields stay. Among them are `StartUTC`, `ClientAddr`,
  `ClientHost`, `RequestHost`, `RequestMethod`, `RequestPath`, `DownstreamStatus`,
  `DownstreamContentSize`, `Duration`, `RouterName` and `ServiceURL`. `RouterName` is
  `relist-c<composeID>-d<domainID>@docker` for Domains (ADR-0013). A request that matched
  no router has an empty `RouterName` but still has `RequestHost`.
- The Traefik log keeps its default text format on stdout.

`traefik` and `panel` both gain:

```yaml
logging:
  driver: json-file
  options:
    max-size: "10m"
    max-file: "5"
```

`.env` gains one documented variable, which the README lists:

```env
# Comma-separated IPs/CIDRs of proxies in front of Traefik (e.g. Cloudflare's ranges).
# Traefik trusts X-Forwarded-For only from these. Empty = trust no one.
TRAEFIK_TRUSTED_IPS=
```

### To verify against `traefik:v3.7` before implementing

Run each check in a throwaway probe. Record the results under `## Comments` in this file.

1. With the peer in `trustedips`, `ClientHost` in the access log is the `X-Forwarded-For`
   client. With the peer not in `trustedips`, a spoofed `X-Forwarded-For` does not reach
   `ClientHost`.
2. `trustedips=` with an empty value starts Traefik without error. If it fails, the
   default becomes `127.0.0.1/32` and the spec is updated.
3. Traefik creates the missing `access/` directory itself. If it does not, the panel
   creates `<data dir>/.traefik/access` on startup, as it does for `dynamic/`. Then check
   whether Traefik retries the file once the directory exists, or whether a separate
   mount is needed as in ADR-0022.
4. `docker kill --signal=USR1 relist-traefik` makes Traefik close and reopen
   `access.log`: after a rename, new lines go to a fresh `access.log`.

## Rotation (panel)

A background job in the panel process, like the metrics sampler and the vulnerability
scheduler. It lives in its own package, `internal/accesslog`, behind small interfaces for
the clock, the directory and the signaller (Docker), so it can be tested without Docker.

**When:** once a day at 00:00 UTC, and once at panel startup.

**Each run:**

1. If `access.log` is not empty and its first line's `StartUTC` is before today (UTC),
   rename it to `access-<D>.log`. `<D>` is yesterday's
   UTC date, the last day the file covers. If that name exists, append `-2`, `-3` and so on.
2. Send `USR1` to the `relist-traefik` container so Traefik reopens `access.log`.
3. Gzip every `access-*.log` whose modification time is more than 5 minutes old to
   `access-*.log.gz`, `fsync` it, then remove the plain file.
4. Unless retention is "forever", delete every `access-*.log.gz` whose date `<D>` is older
   than today minus the retention days.

**Failure cases:**

- **The panel was down for days.** The startup run rotates the multi-day file under the
  latest date. Retention then deletes it later than its first day would require. That is
  later, never sooner.
- **Traefik is not running.** It holds no open file, so `USR1` failing is harmless. It
  creates a new `access.log` when it starts. The error is logged.
- **Traefik is running but `USR1` fails.** Traefik keeps writing to the renamed file.
  Step 3's 5-minute rule leaves that file alone. The error is logged, and the next run
  signals again.
- **Migration.** Old host: rotation keeps running under the Migration lock; it touches
  only `.traefik/access/`. Finalize stops Traefik and moves the whole data directory,
  Access log included (ADR-0018). New host: the startup run rotates as usual.
- Rotation errors never stop the panel. They are logged and shown on the Traefik page as
  "last rotation error" with its time.

## Data model

Migration `00029_access_log_settings.sql` adds a single-row settings table:

```sql
CREATE TABLE access_log_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  retention_days INTEGER NOT NULL DEFAULT 90 CHECK (retention_days >= 1),
  keep_forever INTEGER NOT NULL DEFAULT 0,
  last_rotated_at TEXT,
  last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO access_log_settings (id) VALUES (1);
```

## API

All routes are admin-only, like every other route.

- `GET /api/traefik/logs` → `{ "logs": "<text>", "found": true }`. Runs
  `docker logs --tail 500 --timestamps relist-traefik`. When there is no such container,
  it returns `found: false` and an empty log, not an error.
- `GET /api/traefik/status` → `{ "accessLogEnabled": bool, "trustedIps": "<value>" }`.
  Both come from `docker inspect relist-traefik` args. `accessLogEnabled` is true when
  `--accesslog.filepath=/letsencrypt/access/access.log` is among them.
- `GET /api/traefik/access-logs` → `{ "files": [{ "name", "date", "size" }], "totalSize" }`,
  newest first. `access.log` is included with `date` null.
- `GET /api/traefik/access-logs/{name}` streams one file as a download. `name` must match
  `^access(-\d{4}-\d{2}-\d{2}(-\d+)?)?\.log(\.gz)?$`. Anything else returns 404, so the
  route cannot escape the directory.
- `GET /api/traefik/access-log-settings` and `PUT` the same path:
  `{ "retentionDays": int ≥ 1, "keepForever": bool }`. The GET also returns
  `lastRotatedAt` and `lastError`. A PUT takes effect at the next run.

## UI — Traefik page

A new host-wide menu item **Traefik**, next to Certificates and SSH keys. Built with Ant
Design per `docs/GUIDELINES.md`. Before building it, read Ant Design's `llms.txt` and check
the components against the installed `antd` version.

- **Status banner.** A warning `Alert` when `accessLogEnabled` is false: "Access log is not
  on. This install's Traefik was started without the access-log flags; run a Panel update."
- **Traefik log card.** Monospace log text from `/api/traefik/logs`, a level filter
  (All / Error / Warn / Info) matched on the level token in each line, and a Refresh button.
  When `found` is false it shows "Traefik container `relist-traefik` not found".
- **Access logs card.** A table of files (date, name, size) with a Download action per row,
  and the total size on disk. The last rotation time and error are shown when present.
- **Settings card.** Retention days (`InputNumber`, min 1) and a "Keep forever" switch that
  disables the number. When days < 90 and forever is off, a warning: "Thai law requires
  service providers to keep traffic data for at least 90 days." Save button.

## Testing

- `internal/accesslog`, with a fake clock, a temp directory and a fake signaller:
  - normal daily rotation and naming;
  - name collision gets `-2`;
  - the startup run catches up after days of downtime;
  - a file modified within 5 minutes is not gzipped;
  - a signal failure leaves the renamed file for the next run;
  - retention deletes by date and keeps everything when forever is on;
  - files not matching the pattern are never touched.
- `internal/httpapi`: each new route. Download rejects names outside the pattern
  (`../panel.db`, `access.log/../x`). Settings validation rejects `retentionDays < 1`.
  `found: false` is returned when the container is missing.
- `install_compose_test.go`: the new Traefik flags, the `logging:` block on both services,
  and the `TRAEFIK_TRUSTED_IPS` default.
- web: `traefik-view.test.ts` for the level filter, the under-90 warning and the
  forever/days interplay, following the existing `*-view.test.ts` pattern.
- The four `traefik:v3.7` checks above, done once and recorded here.

## Docs

- ADR-0026 (see Domain changes).
- `CONTEXT.md`: **Access log** entry.
- README: `TRAEFIK_TRUSTED_IPS` in the `.env` reference. The "Upgrading an existing
  install" section notes that the Access log only starts after a Panel update, and that
  the Traefik page warns until then.

## Comments

**Probe, 2026-11-03, `traefik:v3.7`** (scratch container, `whoami` backend):

1. Peer `172.20.0.1` in `trustedips`, `X-Forwarded-For: 203.0.113.9` → `ClientHost` `203.0.113.9`, `ClientAddr` `172.20.0.1:34646`. With `trustedips=` (empty) the same spoofed header → `ClientHost` `172.20.0.1`. Pass.
2. `--entrypoints.web.forwardedheaders.trustedips=` (empty) starts cleanly. Pass; the default stays empty.
3. Traefik creates the missing `access/` directory itself (as root, `0755`; the file is `0644`). The panel runs as root, so it can rename and delete there. Pass; no separate mount needed.
4. `mv access.log access-x.log` + `docker kill --signal=USR1` → Traefik logs "Closing and re-opening log files for rotation" and the next request lands in a fresh `access.log`. Pass.
5. Traefik's own log goes to **stdout**, levels shown as `ERR`, `WRN`, `INF` (`DBG`) after the timestamp, e.g. `2026-11-03T07:32:27Z ERR Error matching constraints expression ...`. Without a TTY it still emits **ANSI colour codes**, so the install also gets `--log.nocolor=true`.
