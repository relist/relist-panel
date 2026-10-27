# Spec — Background vulnerability scanning of images in use

Date: 2026-10-28.
Status: needs-triage

## Problem

The operator cannot tell whether the images the host is running carry known CVEs.
Finding out means running a scanner by hand (`docker scout` needs a Docker Hub
account and is proprietary), and nothing tells the operator which Compose to rebuild
or which base image to bump. Later, the operator also wants to be alerted when a new
Critical CVE appears. Alerting is out of scope here, but the data model must make it
possible.

## Goal and success criteria

- A host-wide **Vulnerabilities** menu lists every image in use: the panel's
  Composes' Services, the panel itself, Traefik and the Scanner's own image.
  - For each image it shows its CVEs: severity, package, installed version, fixed
    version, and whether the CVE is new.
  - A second view lists unique CVEs across the host, with filters.
- Images are scanned in the background, one at a time. Scanning never runs while a
  Deployment is running or while the panel is under the Migration lock.
- Scans are triggered three ways:
  - after every successful Deployment, if that is enabled;
  - once a day at a chosen time, if that is enabled;
  - on demand with **Scan now**, for all images or one.
- A master switch turns scanning on and off. All three switches are **off** by default.
- Overview, the Project page and the Compose page show Critical and High counts and how
  many CVEs are fixable. The Compose page breaks the counts down per Service. Each badge
  links to the menu with that filter applied.
- The scanner never receives the Docker socket and runs with no network, no
  capabilities, a read-only root filesystem and a non-root user.

## Out of scope

- Notifications of any kind (email, webhook, in-panel toast). Host-wide first-seen dates
  are kept so they can be built later.
- EPSS and KEV enrichment. Trivy does not ship them. They can be added later from the
  public FIRST and CISA feeds.
- Accepting or ignoring individual CVEs. The only filter is a fixed, built-in list of
  ignored packages.
- Per-Compose opt-out. All settings are host-wide.
- Scanning images that no container uses, dangling images, or images before deploy.
- Trivy's secret, misconfiguration and license scanners. Only `--scanners vuln` is used.
- Scan history and trend charts. Only the latest result per image is kept.
- Grype or any second scanner backend.

## Decisions and evidence

Benchmarks were run on the dev host on 2026-10-27, against 5 running images from 19 MB
to 1.66 GB. The reports are in the session scratchpad and are not committed. Their
numbers are copied into ADR-0021.

- **Scanner: Trivy**, not Grype.
  - Trivy uses less of everything: DB 1.5 GB vs 3.1 GB on disk, and peak memory 267 MiB
    vs 2.2 GB on node:24.
  - Warm rescans take 0.3–1.1 s because of Trivy's per-layer cache. Grype has no
    cache, and its rescans take as long as cold scans (up to 20 s).
  - On Debian images, Trivy reports about 3,000 `linux-libc-dev` (kernel header)
    matches. Grype ignores these. The panel drops them at ingest.
- **Version: Trivy 0.76.1**, pinned by its manifest-list digest
  `sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c` (record it
  again at implementation time).
  - It is the security release for the eMudhra intermediate-CA compromise. The
    Scanner fetches its DB over TLS daily, so an older trust store is the bigger risk.
  - v0.69.4 was a compromised release and must never be offered as a default.
- **No socket. The image reaches the Scanner as a tar.**
  - The panel exports the image with `GET /images/{id}/get`.
  - Trivy scans it with `image --input`.
  - The probe found the same findings as a socket scan on all 5 images, and the
    layer cache still hits: it is keyed by content.
  - Export took 0.15–4.5 s. The tars were 5–428 MB, because layers stay compressed.
- **Tar delivery through the data dir.**
  - `${RELIST_DATA_DIR}` is bind-mounted at the same path on the host and in the
    panel container.
  - The panel writes `${RELIST_DATA_DIR}/.scan-work/<image-id>.tar` (mode 0644).
  - The Scanner bind-mounts only that one file, read-only.
  - The install's `docker-compose.yml` needs no new mount.
- **The Trivy cache is a named volume, `relist-trivy-cache`**, owned by uid 65532. It
  stays out of the data dir, so a Migration does not copy 1.5 GB that can be downloaded
  again in about 20 s.
- **The panel pulls one image.** The glossary says the panel never pulls an image. The
  Scanner image is the single exception: a tool image named with a digest, pulled when
  scanning is first enabled or when the Scanner image setting changes. Recorded in
  ADR-0021.
- **The scan queue is separate from the Deployment queue and yields to it.**
  - ADR-0016 wants one heavy thing at a time. Before starting each task, the Scanner
    worker waits while `deployments.Queue` is running a Deployment or is held for the
    Migration lock.
  - A task that has already started runs to completion. Tasks take seconds.
- **First-seen is host-wide, keyed by CVE ID.** Image IDs change on every rebuild, so
  keying by image would mark every CVE new after each build. "New" means the CVE has
  never been seen on this host.

## Domain changes

`CONTEXT.md`:

- **Image**: change "the panel builds images only from a Source and never pulls one
  itself" to add "— except the Scanner's own image (ADR-0021)".
- New **Scanner**:
  - The vulnerability scanner the panel runs (Trivy), as a short-lived, locked-down
    container from the Scanner image, which is named by digest.
  - It never holds the Docker socket.
  - _Avoid_: Scout, Agent.
- New **Vulnerability scan**:
  - One run of the Scanner over one Image, keyed by image ID.
  - Only the latest result per Image is kept.
  - Scans run one at a time in the host-wide scan queue, which never runs while a
    Deployment is running.
  - _Avoid_: Job, Audit.
- New **Finding**:
  - One CVE matched against one package in one Image: severity, installed version,
    fixed version.
  - A Finding is *fixable* when it has a fixed version.
  - _Avoid_: Issue, Alert.

New `docs/adr/0021-vulnerability-scanning.md` records the decisions above with the
benchmark numbers, and the two rejected alternatives:

- **A resident `trivy server`.** It uses RAM permanently. The ADR-0006 reasoning
  applies.
- **Trivy embedded in the panel image.** It adds 150–200 MB for everyone, including
  operators who never enable scanning, and puts the scanner inside the image that
  holds the socket.

## Data model

New migration `internal/migrations/00022_vulnerability_scans.sql`. Committed migrations
are not edited.

```sql
CREATE TABLE scan_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    after_deploy INTEGER NOT NULL DEFAULT 0,
    daily INTEGER NOT NULL DEFAULT 0,
    daily_minute INTEGER NOT NULL DEFAULT 180,   -- minutes after local midnight
    scanner_image TEXT NOT NULL DEFAULT '',       -- repo:tag@sha256:..., '' = the built-in default in code
    last_daily_at INTEGER,
    db_version TEXT,                              -- Trivy DB UpdatedAt, RFC 3339
    db_updated_at INTEGER,                        -- when the panel last refreshed it
    db_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO scan_settings (id) VALUES (1);

CREATE TABLE scan_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('db_update', 'image')),
    image_id TEXT,                                -- sha256:..., NULL for db_update
    reason TEXT NOT NULL CHECK (reason IN ('deploy', 'daily', 'manual', 'setup')),
    queued_at INTEGER NOT NULL,
    started_at INTEGER
);
CREATE UNIQUE INDEX scan_queue_image ON scan_queue(image_id) WHERE kind = 'image';
CREATE UNIQUE INDEX scan_queue_db ON scan_queue(kind) WHERE kind = 'db_update';

CREATE TABLE image_scans (
    image_id TEXT PRIMARY KEY,
    image_ref TEXT NOT NULL,                      -- repo tags at scan time, for display
    os TEXT NOT NULL DEFAULT '',                  -- e.g. "alpine 3.22"
    scanned_at INTEGER,                           -- last successful scan
    db_version TEXT,                              -- DB used by the last successful scan
    error TEXT NOT NULL DEFAULT '',               -- last attempt's failure, '' if it succeeded
    attempted_at INTEGER NOT NULL
);

CREATE TABLE image_findings (
    image_id TEXT NOT NULL REFERENCES image_scans(image_id) ON DELETE CASCADE,
    vuln_id TEXT NOT NULL,                        -- CVE-..., or GHSA-... when no CVE exists
    pkg_name TEXT NOT NULL,
    pkg_path TEXT NOT NULL DEFAULT '',            -- binary path for language packages
    installed_version TEXT NOT NULL,
    fixed_version TEXT NOT NULL DEFAULT '',       -- '' = no fix
    severity TEXT NOT NULL CHECK (severity IN ('CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN')),
    title TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (image_id, vuln_id, pkg_name, pkg_path, installed_version)
);
CREATE INDEX image_findings_vuln ON image_findings(vuln_id);

CREATE TABLE vulnerability_first_seen (
    vuln_id TEXT PRIMARY KEY,
    first_seen_at INTEGER NOT NULL
);
```

Rules:

- **A successful scan** replaces all of an image's `image_findings` rows in one
  transaction, sets `scanned_at`, `db_version` and `error = ''`, and inserts any unseen
  `vuln_id` into `vulnerability_first_seen` (`INSERT OR IGNORE`).
- **A failed scan** only sets `error` and `attempted_at`. The last successful findings
  stay visible, flagged "last scan failed".
- **Ingest filter:** a built-in list of package names is dropped before insert. It
  starts as `linux-libc-dev`. Duplicate Trivy entries that share the primary key are
  collapsed. The probe saw 577 raw entries become 500 unique on the panel image.
- **Image-to-Service mapping is not stored.** It is read live from
  `GET /containers/json` (each container's `ImageID` and Compose labels), the way
  ADR-0006 resolves Services.
- **Pruning:** the daily run deletes `image_scans` rows (and their findings, by cascade)
  for images that no running container uses and that are not the Scanner image. It never
  deletes `vulnerability_first_seen`.
- **Counting:** Project and host counts are unique `vuln_id` per severity across the
  scope's images. Compose counts are given in total and per Service. "Fixable" counts
  unique `vuln_id` with at least one finding in scope that has a `fixed_version`.

## Scan queue and scheduling

Package `internal/vulnscan` owns the stores, the worker, the scheduler and the HTTP
handlers. It declares the ports it consumes (ADR-0003, "one adapter package, one port per
consumer"):

```go
type Scanner interface {
    EnsureImage(ctx context.Context, ref string) error  // pull by digest if missing, then `trivy --version`
    UpdateDB(ctx context.Context, ref string) (DBInfo, error)
    Scan(ctx context.Context, ref, imageID string) (Report, error) // findings already filtered
    RemoveImage(ctx context.Context, ref string) error
    DeleteCache(ctx context.Context) error
    Cleanup(ctx context.Context) error                  // leftover containers and .scan-work files
}
type InUse interface {
    Images(ctx context.Context) ([]UsedImage, error)    // image ID, refs, and each user: Compose/Service, panel, Traefik
}
type DeployGate interface {
    Busy() bool                                         // a Deployment is running, or the queue is held
}
```

**Worker**: one goroutine that runs one task at a time.

- **Order:** `db_update` first, then images in `queued_at` order.
- **Gate:** before each task it waits while `DeployGate.Busy()`, checking on a
  short tick and on a wake channel.
- **Skip:** an `image` task is skipped when `image_scans.db_version` already equals the
  current DB version and `error = ''`.
- **Enqueue:** enqueueing an image already in the queue is a no-op, enforced by the
  unique index.
- **Restart:** on panel start, `Scanner.Cleanup` runs first. Then every task with a
  `started_at` gets it cleared, and the task is retried.

**Triggers**:

1. **After deploy**: `deployments.Deps` gains `OnSucceeded func(composeID int64)`,
   wired in `main.go`. If `enabled && after_deploy`, vulnscan reads the Compose's
   running containers and enqueues their image IDs with reason `deploy`.
   `deployments` does not import `vulnscan`.
2. **Daily**: a scheduler checks every minute, using an injected clock. When
   `enabled && daily`, the current local time is past `daily_minute` today, and
   `last_daily_at` is before that moment, it does three things:
   - enqueues `db_update`;
   - enqueues every image from `InUse` plus the Scanner image, with reason `daily`;
   - prunes stale `image_scans`, and sets `last_daily_at`.

   A run missed while the panel was down therefore fires once on start.
3. **Scan now**: `POST /scans` enqueues every in-use image, or one image, with
   reason `manual`. A manual scan does not skip on an equal DB version.

**Enabling** (master switch false → true):

1. Runs `Scanner.EnsureImage` synchronously, then enqueues `db_update` and a full scan
   with reason `setup`.
2. If `EnsureImage` fails, the switch stays off and the error is returned.

**Disabling**:

- Clears `scan_queue`, and lets the running task finish.
- Results stay visible, marked "scanning is off — last results from …".
- The cache volume is kept, and the operator can delete it with `DELETE /cache`.

**The built-in default** is a Go constant in `vulnscan`. While `scanner_image` is `''`,
the effective image is that constant, so a panel release that bumps it upgrades every
install that has not overridden it. On the first start of such a release, if scanning is
enabled and the effective image changed, the same sequence runs as for a manual change.
"Use default" stores `''`.

**Changing the Scanner image** (validated `repo[:tag]@sha256:<64 hex>`):

1. `EnsureImage` runs on the new reference.
2. On success, the setting is saved, the old image is removed, and the new Scanner image
   is enqueued for scanning.
3. On failure, the old setting stays.

## Scanner adapter

`dockerexec.TrivyScanner` implements `vulnscan.Scanner`.

- **Scan**:
  1. Check free space in `${RELIST_DATA_DIR}`. It must be at least the image's
     `Size` from `GET /images/{id}/json` plus 512 MB. If not, fail with
     "not enough disk space for export".
  2. Stream `GET /images/{id}/get` to `${RELIST_DATA_DIR}/.scan-work/<hex>.tar`,
     chmod 0644.
  3. Run the scan:
     ```
     docker run --rm --name relist-vulnscan-<hex12> --label relist.vulnscan=1 \
       --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
       --user 65532:65532 --cpus 1 --memory 1g \
       --tmpfs /tmp:rw,noexec,nosuid,size=512m,uid=65532 \
       -v relist-trivy-cache:/cache \
       -v ${RELIST_DATA_DIR}/.scan-work/<hex>.tar:/work/image.tar:ro \
       <scanner_image> --cache-dir /cache image --input /work/image.tar \
       --scanners vuln --skip-db-update --skip-java-db-update --offline-scan -f json -q
     ```
  4. Parse stdout and delete the tar. Delete it in a `defer`, so it is removed on
     failure too.
  5. The timeout is 10 minutes. Exit 137 is reported as "scanner ran out of memory
     (1 GB limit)".
- **UpdateDB** runs the same container without `--network none` and without the tar
  mount: `image --download-db-only --no-progress`. It then reads the DB metadata
  (`/cache/db/metadata.json`) for `DBInfo`. The timeout is 5 minutes.
- **Cache volume**: on first use the volume is created and chowned to 65532 with a
  one-off `docker run --rm` of the Scanner image as root.
- **Parsing**: `Results[].Vulnerabilities[]` maps to findings. Fields used:
  - `VulnerabilityID`, `PkgName`, `PkgPath`, `InstalledVersion`, `FixedVersion`,
    `Severity`, `Title`, `PrimaryURL`
  - `Metadata.OS` gives `os`
- **Ownership**: the Docker page's prune (`dockerhost.Ownership`) treats the Scanner
  image and `relist-trivy-cache` as panel-owned, so a prune does not delete them. If
  either is missing anyway, the next task runs `EnsureImage`, or `db_update` first.
- **Migration**: `skipInDataDir` adds `.scan-work`. The cache volume is not a data-dir
  item, so it is not copied. Scan tables move with the database. On the destination,
  an `enabled` setting runs the enable sequence on first start.

## API

All endpoints are under `/api/vulnerabilities`, use the existing auth, CSRF protection
and JSON error shape, and are registered on the stdlib `ServeMux`.

| Method and path | Body / query | Response |
|---|---|---|
| `GET /settings` | | `{enabled, afterDeploy, daily, dailyTime: "03:00", scannerImage, defaultScannerImage}` |
| `PUT /settings` | the same fields | `200` with the new settings. `422` with `{error}` if the image fails to pull or verify. Runs synchronously with a 2-minute timeout. |
| `GET /scanner-tags` | | `{tags: [{tag, digest, pushedAt, ageDays, young}]}`. See below. `502` if Docker Hub fails. |
| `GET /status` | | `{enabled, running: {kind, imageRef}?, queued, db: {version, updatedAt, stale, error}, lastDailyAt, nextDailyAt, timeZone, cacheBytes}`. `timeZone` is the panel process's zone, which is UTC in the container unless `TZ` is set. |
| `POST /scans` | `{imageId?}` | `202`. `409` if scanning is off. |
| `DELETE /cache` | | `204`. `409` if scanning is on. |
| `GET /images` | | One row per in-use image: `{imageId, refs, users: [{kind: "service"\|"panel"\|"traefik"\|"scanner", projectId?, composeId?, service?}], counts, fixable, scannedAt, error}` |
| `GET /images/{id}/findings` | | Findings with `firstSeenAt` and `new` (first seen within 7 days) |
| `GET /cves` | `severity, fixable, new, projectId, composeId, q, page, pageSize` | Unique CVEs with severity, fix state, first seen and affected images. Paginated on the server. |
| `GET /summary` | `projectId` or `composeId`, or neither for the host | `{enabled, counts: {critical, high}, fixable, services?: [...]}`. If `enabled` is false, the UI hides badges. |

**`/scanner-tags`**:

- **Source:** the panel calls
  `https://hub.docker.com/v2/repositories/<ns>/<repo>/tags?page_size=100&ordering=last_updated`
  for the configured repository. Only Docker Hub repositories are supported; for others
  the endpoint returns `400`.
- **Filter:** it keeps semver tags only, with no `latest`, `canary` or pre-release.
- **Window:** it returns tags pushed in the last 14 days, newest first, plus the newest
  tag older than 14 days. Tags younger than 14 days carry `young: true`.
- **Untrusted data:** the response is never executed or pulled until the operator saves
  it.

## UI (antd 6.5.1)

Before building, read `https://ant.design/llms.txt` and the component pages used, and
follow `docs/GUIDELINES.md`. View logic lives in `web/src/pages/vulnerabilities-view.ts`,
following the `*-view.ts` pattern.

- **Nav**: a new **Vulnerabilities** item after Images, with the `SafetyOutlined` icon.
  A red `Badge` shows the host Critical count, and is hidden when scanning is off or the
  count is 0.
- **`/vulnerabilities` page**:
  - **Status bar**:
    - **Shows:** scanning on/off, the running task, the number queued, the DB date
      (yellow if older than 7 days or if the last update failed, with the error), and
      the last and next daily run.
    - **Buttons:** **Scan now** (disabled when off) and **Settings**.
  - **Images tab**:
    - **Table:** image refs, used by (tags linking to Compose/Service, or "Panel",
      "Traefik", "Scanner (Trivy)"), Critical/High counts, fixable, scanned at, and an
      error tag.
    - **Expanded row:** a findings table with ID (linked to `url`), severity, package,
      installed → fixed, and a "new" tag.
    - **Row action:** "Scan".
  - **CVEs tab**:
    - **Filters:** severity, fixable, new in 7 days, Project, Compose, and a text search.
    - **Table:** CVE ID, severity, title, fix state, first seen, and affected images.
  - Query params `projectId`/`composeId` preselect the filter, and are used by the
    badges.
- **Settings drawer**:
  - **Switches:** master, after deploy, and daily, with a `TimePicker` (hours and
    minutes). The two sub-switches are disabled while master is off.
  - **Scanner image:** an `Input` that validates the digest form, "Use default",
    "Pick from recent tags" (a modal table of `/scanner-tags`, with a warning tag and
    confirm on `young`), and Save. Save shows a spinner while the image is pulled and
    verified.
  - **Delete scanner cache:** enabled only while off, with a confirm that states the
    volume size.
- **Overview**: a Vulnerabilities card with `N Critical · N High · N fixable`, which
  links to the page. It is hidden when off.
- **Project page**: a count tag group in the header, which links to the page with
  `?projectId=`.
- **Compose page**: a tag group in the header, plus a Vulnerabilities column in the
  Services table. It links with `?composeId=`.
- **Empty and partial states**: "Scanning is off" with a link to settings; "Not scanned
  yet"; and "Last scan failed: …" alongside the last good counts. When scanning is off,
  badges are hidden rather than showing 0.

## Error handling

| Case | Behaviour |
|---|---|
| Pull fails on enable or on image change | Setting unchanged, `422` with the Docker error |
| DB update fails | Scans continue on the old DB. `db_error` is set, and the status bar turns yellow. |
| No DB at all | Image tasks fail with "vulnerability DB not downloaded yet" |
| Export fails, or the image was removed while queued | Task fails. The image row keeps the last good findings with `error` set. Images no longer in use are pruned on the next daily run. |
| Not enough disk | Task fails with a clear reason. No tar is written. |
| Timeout or OOM | Container killed. Task fails with the reason. Tar removed. |
| Panel restarts mid-task | On start: remove `relist-vulnscan-*` containers, empty `.scan-work/`, and requeue started tasks |
| Migration lock | Worker idles until released. `.scan-work` is skipped by the Migration. |
| Docker Hub unreachable | `/scanner-tags` returns `502`. Manual entry still works. |

## Testing

- **`vulnscan` (external test package, HTTP seam)**: fake `Scanner`, `InUse` and
  `DeployGate`. Cases:
  - settings round-trip, and enable fails when `EnsureImage` fails;
  - queue dedupe;
  - the worker waits while `Busy`;
  - restart requeues started tasks;
  - an equal DB version skips an automatic scan but not a manual one;
  - a failed scan keeps previous findings;
  - first-seen survives image pruning;
  - summary counting: unique per Project, per-Service breakdown, and fixable;
  - `/cves` filters and pagination.
- **Scheduler**: injected clock. Cases: fires once a day at `daily_minute`; fires once
  on start after a missed run; does nothing when `daily` or `enabled` is off.
- **Parser**: small fixtures trimmed from real Trivy 0.76.1 JSON. Cases:
  - an OS package, a Go binary package with `PkgPath`, and a Node package;
  - a `linux-libc-dev` entry that must be dropped;
  - duplicate entries that must collapse;
  - an image with no vulnerabilities (`Results` without `Vulnerabilities`).
- **`dockerexec`**:
  - argv builder tests assert every lockdown flag and that no socket mount is ever
    present, the way the `volumeArgv` tests do;
  - the image reference validator;
  - a Docker integration test, skipped when Docker is unavailable, that scans
    `traefik/whoami` end to end.
- **`/scanner-tags`**: an `httptest` Docker Hub fake. Cases: semver filtering, the
  14-day window plus the newest older tag, and the `young` flag.
- **Frontend**: `vulnerabilities-view.test.ts` covers counts, filters, empty and
  partial states, and hidden-when-off. Page tests cover the settings drawer flows and
  the tag picker confirm.

## Resolved questions

- **Purpose:** both deciding what to rebuild (fix versions) and seeing the overall
  risk. Alerts come later.
- **History:** only the latest result per image, plus host-wide first-seen per CVE.
- **Scope:** images of the panel's Composes, plus the panel, Traefik and the Scanner
  image. Scan now covers all images or one.
- **Settings:** host-wide master, after-deploy and daily-with-time switches, all off by
  default.
- **Badges:** Critical and High plus a fixable count, per Service on the Compose page.
- **Menu:** an Images tab and a CVEs tab, with a status bar.
- **Scanner:** a short-lived Trivy 0.76.1 container per task. No socket; the image
  arrives as a tar through the data dir.
- **Scanner image:** the operator may override it with any `repo:tag@sha256:` value.
  A tag picker shows Docker Hub tags from the last 14 days plus the newest older one,
  with a warning on young tags.
