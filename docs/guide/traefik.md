# Traefik

Back to the [docs index](../README.md).

## 1. What it does

Traefik is the reverse proxy in front of everything the panel runs. It routes every
Domain and the panel itself (`RELIST_DOMAIN`), gets Let's Encrypt certificates, and serves
the Certificates uploaded on the Certificates page. It runs as the `relist-traefik`
Container from the Install file (`docker-compose.yml`).

The panel manages four things about it:

- **Static config.** This is what Traefik reads once, at start: entrypoints, providers,
  the ACME resolver, the log level, trusted proxies and the Access log. The operator
  changes it on the **Traefik** page, under **Configuration**. It is applied with a restart
  that rolls itself back when Traefik does not come up healthy. This is a **Traefik apply**.
- **Dynamic config.** This is what Traefik picks up while running: the labels a deploy puts
  on each Service with a Domain (`internal/domains`), and one file per uploaded
  Certificate in the file provider's directory.
- **The Access log**: every request as JSON lines, rotated daily and deleted after the
  retention period by the panel. It is off by default.
- **Traefik's own log**, shown on the page. Let's Encrypt errors show up there.

## 2. How it works

### Starting Traefik

Every `docker compose up` of the Install, whether from the install script, a Panel
update or by hand, does this:

1. Compose starts the one-shot `traefik-config` service (`relist-traefik-config`). It runs
   the panel image with the command `traefik-config` (`traefikconfig.go`), with no network.
2. If `<data dir>/.traefik/static/traefik.override.yml` does not exist, `traefik-config`
   creates it. This is the **Traefik override**, the operator's part of the config. Its
   first content comes from `.env`: `RELIST_ACME_EMAIL` becomes the ACME email,
   `TRAEFIK_TRUSTED_IPS` becomes the trusted proxies, and `accessLog: {}` is added if
   `.traefik/access/` already holds a file (an install that had the Access log on).
   After that, `.env` is not read for these again.
3. `traefik-config` merges the override over the **base**. The base is
   `internal/traefikconfig/base.yml`, embedded in the binary. Some of its keys are
   **locked**, because the panel depends on them: the entrypoint addresses, the Docker
   and file providers, the `letsencrypt` resolver's storage and challenge, and the
   Access log's path and format. The override may repeat a locked key's value but not
   change it. Maps merge key by key and lists are replaced. The result is written to
   `static/traefik.yml` with a "Generated … Do not edit" header.
4. If the override cannot be used (bad YAML, or a locked key changed), `traefik-config`
   keeps the `traefik.yml` that is already there. It writes the reason into `apply.json`,
   where the Traefik page shows it, and exits 0, so `up` goes on. It exits 1 only when
   there is no usable `traefik.yml` at all.
5. Compose then starts Traefik. Its whole `command:` is
   `--configfile=/etc/traefik/traefik.yml`, and `static/` is mounted read-only at
   `/etc/traefik`.

### Changing the config: a Traefik apply

On the Traefik page, **Configuration** edits a draft of the override:

- **Settings** has five fields: ACME email, Trusted proxies, Trust Cloudflare, Log level
  and Access log. Each edit is sent to `POST /api/traefik/config/preview`. That route
  rewrites the field in the YAML on the server and keeps comments and key order.
- **Advanced** is the override as YAML, next to the base and the generated file (both
  read-only). An edit is the draft. A preview 400 ms later refreshes the errors and the
  Settings fields.
- **Test** runs the dry-run below without changing anything.
- **History** lists the last 10 backups, each with **Restore**.

**Review & Apply** shows the diff of the generated file, then starts a run
(`internal/traefikconfig/apply.go`). **Restore** and **Restart** run the same steps:

| # | Step | If it fails |
|---|---|---|
| 1 | Take the lock: one run at a time, refused during a Panel update or a Migration | refused (409 / 503), nothing changed |
| 2 | Validate: the YAML parses, no locked key changed, every top-level key is a Traefik section, trusted IPs are IPs or CIDRs, and the revision (sha256 of the override on disk) still matches | refused (400 / 409), nothing changed |
| 3 | Dry-run: start the running Traefik's image as `relist-traefik-dryrun-<id>` with no network, no Docker socket and the candidate file. It passes if it is still running after 5 s with no `Command error` in its log. The container is always removed | refused, the dry-run's log is shown |
| 4 | Back up the current override and `traefik.yml` to `history/<UTC time>-<id>/`, keeping 10 | refused, nothing changed |
| 5 | Write the new override and `traefik.yml` (temp file, then rename) | the backup is put back, refused |
| 6 | Note Traefik's `RestartCount`, then `docker restart relist-traefik` | roll back |
| 7 | Watch every 2 s for up to 60 s: running, `RestartCount` unchanged, no `Command error`, and the panel's `/healthz` answers through Traefik for `RELIST_DOMAIN` | roll back |
| 8 | Healthy: record the file's sha256 and Traefik's start time, and free the lock | — |

A **rollback** writes the backup back, restarts, and watches with the same rules. The run
ends as `rolled-back` if that works and `rollback-failed` if it does not. It never tries a
third time. Other `ERR` lines, such as ACME rate limits or DNS that does not point here,
are not failures, because Traefik is healthy while it logs them.

The run's state is kept in `static/apply.json`. The browser loses the panel while Traefik
restarts: the progress modal polls `GET /api/traefik/config/apply` every 2 s and shows
"reconnecting" until it answers again. A result nobody has closed opens again on the next
visit. If the panel itself restarts mid-run, it picks the run up again at start. Before
the write, the run is marked refused. After the write, it watches again and rolls back if
needed.

**Pending restart.** A Panel update's `up` regenerates `traefik.yml` with the new base,
but it does not always recreate Traefik. When the file differs from the one Traefik last
started healthy with, the page shows "Config changed since Traefik started — Restart to
apply".

### The Access log and Certificates

When the Access log is on, Traefik writes `/letsencrypt/access/access.log`. Once at start
and every day at 00:00 UTC, the panel's rotator (`internal/accesslog/rotate.go`) renames
the file to `access-<yesterday>.log`, sends Traefik `USR1` to reopen it, gzips older files
and deletes files past the retention period. Retention defaults to 90 days and is stored
in `panel.db` (ADR-0027).

An uploaded Certificate is written as one file under `.traefik/dynamic/`. Traefik's file
provider watches that directory, so a new Certificate needs no restart (ADR-0022).

## 3. Where the code is

| Path | What |
|---|---|
| `internal/traefikconfig/base.yml`, `accesslog.yml`, `base.go` | The embedded base, and the Access log section added when the override has `accessLog` |
| `internal/traefikconfig/merge.go`, `sections.go` | Merge, the locked keys, and validation against Traefik's static sections |
| `internal/traefikconfig/form.go`, `cloudflare.go` | The five Settings fields read from and written into the override's YAML, and Cloudflare's ranges |
| `internal/traefikconfig/convert.go`, `bootstrap.go` | The first override (from `.env`, or from a Traefik still on flags) and what `traefik-config` does at `up` |
| `internal/traefikconfig/apply.go`, `history.go` | The Traefik apply, its rollback and resume, and the backups |
| `internal/traefikconfig/http.go` | `/api/traefik/config…` and `/api/traefik/restart` |
| `internal/traefikconfig/traefik.go`, `internal/dockerexec/traefik.go` | What a run needs from Docker (state, restart, logs, dry-run, `/healthz`) and its docker CLI implementation |
| `traefikconfig.go` | The `relist-panel traefik-config` command |
| `internal/accesslog/*` | Rotation, retention settings, file listing and download, `/api/traefik/status` and `/api/traefik/logs` |
| `internal/certificates/*` | Uploaded Certificates and the files written for Traefik |
| `docker-compose.yml`, `release/invariants.json` | The `traefik-config` and `traefik` services, and what a Panel update checks they still do |
| `web/src/pages/TraefikPage.tsx`, `web/src/components/TraefikConfigTab.tsx`, `TraefikApplyModal.tsx` | The Traefik page, the Configuration tab and the apply modal |

## 4. Where the data is

```
<data dir>/.traefik/
  acme.json, dynamic/, access/       mounted at /letsencrypt
  static/                            mounted at /etc/traefik (read-only)
    traefik.yml                      generated: base + override. Written by the panel and
                                     traefik-config, read by Traefik at start only.
    traefik.override.yml             the Traefik override (the operator's)
    history/<UTC time>-<id>/         the last 10 backups: traefik.override.yml + traefik.yml
    apply.json                       the last Traefik apply: state, result, log
    .dryrun-<id>/traefik.yml         candidate for a dry-run; removed afterwards
```

`<data dir>` is `RELIST_DATA_DIR`, `/var/lib/relist-panel` by default. Every write is a
temp file renamed over the target. A Migration carries all of `.traefik/` with the data
directory. The Access log retention setting is in `panel.db`.

## 5. What can break and how to recover

**`rollback-failed`: Traefik did not come back even with the previous files.** Every
Domain and the panel's public address are down. The panel still listens on the host's
loopback port, so reach it over SSH from your machine:

```sh
ssh -L 8888:127.0.0.1:8888 <host>      # then open http://127.0.0.1:8888/
```

Use your `RELIST_PORT` if you set one. From there, the Traefik page shows the result and
its log, and **History** can restore an older backup. If you would rather fix it on the
host, see "Restoring by hand" below and start from `docker logs --tail 50 relist-traefik`.

**A bad override at `up`.** Someone edited `traefik.override.yml` by hand and broke it, or
a Panel update's new base refuses it. `traefik-config` keeps the last `traefik.yml`,
Traefik starts on it, and the Traefik page shows the reason as a warning. Fix the YAML in
**Advanced** and apply, or restore a backup. `docker logs relist-traefik-config` shows the
same reason.

**Pending restart.** The page says "Config changed since Traefik started — Restart to
apply". The generated file changed, usually because a Panel update brought a new base,
and Traefik is still running the old one. **Restart** runs a Traefik apply with the same
files, so a bad result rolls back too. A restart with nothing to roll back to (the current
files are the ones failing) ends as `rollback-failed`.

**Restoring by hand from `history/`.** On the host:

```sh
D=/var/lib/relist-panel/.traefik/static     # $RELIST_DATA_DIR/.traefik/static
ls -r $D/history                            # newest first
cp $D/history/<backup>/traefik.override.yml $D/history/<backup>/traefik.yml $D/
docker restart relist-traefik
```

Copy both files: `docker restart` does not run `traefik-config`, so Traefik reads
`traefik.yml` as it is. `docker compose up -d` would regenerate it from the override.

**The Access log stays empty.** Check that **Access log** is on in Settings and that the
last apply ended `ok`. A rotation error shows under the Access logs tab.

## 6. How to test

```sh
go test ./internal/traefikconfig/ ./internal/accesslog/ ./internal/certificates/ ./internal/dockerexec/
go test . -run 'TraefikConfig|InstallCompose|Invariants'
pnpm --dir web exec vitest run src/components/TraefikConfigTab.test.tsx \
  src/components/TraefikApplyModal.test.tsx src/pages/TraefikPage.test.tsx \
  src/pages/traefik-config-view.test.ts src/pages/traefik-apply-view.test.ts
```

- `internal/traefikconfig` covers merge and locked keys, the form, the first override,
  `traefik-config` at `up`, and the apply with a fake Traefik: dry-run refusals, every
  rollback trigger, rollback failing, and resume after a panel restart.
- `install_compose_test.go` checks the `traefik-config` and `traefik` services in the
  Install file and that `release/invariants.json` holds against it.
- `task probe:traefik-config` runs real Docker inside `docker:dind` (privileged
  containers). It installs v0.0.2, updates to the current checkout with v0.0.2's Updater,
  and installs the checkout fresh, checking the generated file each time.

## 7. Decisions

- [ADR-0030](../adr/0030-panel-managed-traefik-static-config.md): static config from a
  base and the Traefik override, generated by a one-shot service, applied with self-heal.
- [ADR-0027](../adr/0027-access-log-rotated-by-panel.md): the Access log on disk, rotated
  by the panel (amended by ADR-0030: off by default).
- [ADR-0022](../adr/0022-uploaded-certificates.md): uploaded Certificates.
- [ADR-0013](../adr/0013-panel-managed-domains.md): Domains and the Traefik labels.
- Specs: `.scratch/traefik-static-config/spec.md`, `.scratch/traefik-logging/spec.md`.
