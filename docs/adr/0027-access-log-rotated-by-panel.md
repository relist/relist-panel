# Access log on disk, rotated by the panel

Traefik ran at its default `ERROR` log level with no access log, so there was no record of which client requested which host, and Let's Encrypt failures were only in `docker logs relist-traefik`. Neither install container capped its Docker logs, and Docker's `json-file` driver never rotates. A Docker log also cannot hold a traffic record: it rotates by size, not by day, and is deleted whenever the container is recreated, which every Panel update does. Thai law (Computer Crime Act section 26) asks service providers to keep traffic data for at least 90 days.

**Decision:** Traefik writes an **Access log** as JSON lines to `<data dir>/.traefik/access/access.log`, through the existing `.traefik` → `/letsencrypt` mount. The panel rotates it in-process: once at startup and at every 00:00 UTC it renames `access.log` to `access-<yesterday>.log`, sends Traefik `USR1` to reopen the file, gzips rotated files once they have gone unmodified for 5 minutes, and deletes rotated files dated before today minus the retention days. Retention lives in `panel.db` (default 90 days, at least 1, or forever) and is set on the host-wide **Traefik** page, which also shows the last 500 lines of Traefik's own log (now at `INFO`) and lets the operator download each file. Both install containers cap their Docker logs at 5 × 10 MB.

The client address comes from `X-Forwarded-For` only when the peer is listed in `TRAEFIK_TRUSTED_IPS`, set in `.env`, which is empty by default. The log keeps both `ClientAddr` (the TCP peer) and `ClientHost` (the client address), so a header-derived address can always be traced to the proxy that sent it.

## Considered options

**Docker's json-file logs with a size cap.** Rejected for the Access log: size-based rotation cannot promise a number of days, and the log is lost on every container recreate. Kept for Traefik's own log, which only needs to be recent.

**Host `logrotate`.** Rejected: the install script would have to write into `/etc/logrotate.d/`, outside the Docker-only footprint, and a Migration would not carry it.

**A sidecar container that rotates.** Rejected: another service in the install file to keep running, for work the panel already has the Docker socket to do.

**Cloudflare's ranges as a default or a toggle.** Rejected by the operator for now: one variable that lists every trusted proxy is simpler, and a boolean cannot be expressed in Compose interpolation without a shell wrapper.

## Consequences

- **The Access log moves with a Migration**: it is part of the data directory (ADR-0018). Traefik keeps appending while the data directory is packed, so the pack copies each file as it was when opened (and skips one the rotator removed meanwhile); lines Traefik writes after that stay on the old host.
- **Files are dated in UTC.** A rotated file is named after the last day it covers; after the panel was down for days, one file covers them all and is deleted later than its first day alone would require, never sooner.
- **If `USR1` fails while Traefik runs, Traefik keeps writing to the renamed file.** The 5-minute rule leaves it ungzipped and retention never deletes it while it is still written. With no `access.log` beside it, the next run, ten minutes later, signals again, and the error stays on the Traefik page until a signal succeeds.
- **An install that has not had a Panel update writes no Access log.** The panel reads Traefik's arguments with `docker inspect` and the Traefik page says so.
- **`TRAEFIK_TRUSTED_IPS` and the log level stay in `.env` and the install file** until the panel manages Traefik's static config, which needs its own decision: Traefik takes static config from one source only, so that change moves all of it.
- Spec: `.scratch/traefik-logging/spec.md`.
