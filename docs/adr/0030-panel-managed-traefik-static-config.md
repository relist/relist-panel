# Traefik static config managed by the panel, applied with self-heal

Traefik's static config was about 20 `command:` flags in the Install file plus two `.env` keys interpolated into them (`RELIST_ACME_EMAIL`, `TRAEFIK_TRUSTED_IPS`). Changing one setting, such as trusting Cloudflare, meant editing `.env` or the Install override on the host over SSH and running `docker compose up -d`. A mistake there stopped Traefik, and every Domain and the panel with it, with nothing to check the change first and nothing to put the old config back. A `command:` list in an override replaces the whole list (ADR-0024), so one changed flag meant copying all of them. Traefik takes its static config from exactly one source (a file, CLI flags or env), so a file has to replace all the flags at once.

**Decision:**

- Traefik reads one file, `/etc/traefik/traefik.yml`, and the Install file's Traefik `command:` is the single flag `--configfile=/etc/traefik/traefik.yml`. The file lives in `<data dir>/.traefik/static/`, mounted read-only at `/etc/traefik`.
- The generated `traefik.yml` is the **base** merged with the **Traefik override**. The base is embedded in the binary (`internal/traefikconfig/base.yml`) and holds the keys the panel needs, which are locked, next to defaults the operator may change. The Traefik override, `static/traefik.override.yml`, is the operator's, and the panel writes it only when the operator saves.
- A one-shot `traefik-config` service in the Install file runs the panel image's `traefik-config` command before Traefik on every `docker compose up` (`depends_on: service_completed_successfully`). It creates the first override when none exists and regenerates `traefik.yml`. A fresh install and an install updated from 0.0.2 take the same path, so the update needs no manual step.
- The first override is seeded from `RELIST_ACME_EMAIL` and `TRAEFIK_TRUSTED_IPS`, plus `accessLog: {}` when `<data dir>/.traefik/access/` already holds a file. After that the two `.env` keys are not read again.
- If the override cannot be used at `up`, `traefik-config` keeps the existing `traefik.yml`, records the reason for the Traefik page and exits 0. It exits 1 only when no usable `traefik.yml` exists and none can be written.
- The operator edits the override on the Traefik page, through five fields (**Settings**) or as YAML (**Advanced**).
- A **Traefik apply** validates the draft and dry-runs it in a throwaway Traefik. It then backs up the current files to `history/` (the last 10), writes the new ones and restarts Traefik. It watches for 60 s: Traefik must keep running, its `RestartCount` must not grow, its log must have no `Command error`, and the panel's `/healthz` must answer through Traefik. On failure it puts the backup back and restarts once more. It never retries in a loop. Restore (a history entry) and Restart (the same files) run the same flow. The state is kept in `static/apply.json`, so a result survives a panel restart and is shown once the browser reconnects.
- _Amends ADR-0027:_ the Access log is off by default on a fresh install, and the under-90-days warning is removed. Retention still defaults to 90 days.

## Evidence

Probes on 2026-11-04 against `traefik:v3.7` (3.7.13) and the host's Docker Compose:

- With `--configfile`, other CLI flags are ignored. With the file missing, the flags are used. Bad YAML exits 1. `${VAR}` in the file is not expanded.
- Unknown nested keys exit 1 (`field not found, node: <key>`). Unknown top-level keys are ignored silently, so the panel checks them against the list of Traefik's static sections. Keys are case-insensitive.
- A one-shot service with `depends_on: service_completed_successfully` reruns on every `up` without restarting an unchanged Traefik. When it exits 1, `up` exits 1 and the running Traefik stays.
- **Compose recreates Traefik before it runs the one-shot service.** On an update from 0.0.2, the Traefik left to inspect already has `--configfile`, so the old flags cannot be read. 0.0.2's flags took their values from the same two `.env` keys and always had the Access log on, which is why the seed is the `.env` keys plus "an Access log file exists".
- A directory bind mount, an atomic rename on the host, then `docker restart`: Traefik reads the new file. A single-file bind mount would keep the old inode.
- A dry-run with `--network none`, `/letsencrypt` on tmpfs and no Docker socket stays running with a base-like config.
- `task probe:traefik-config` (dind) installs v0.0.2, updates it to this build with the 0.0.2 Updater, and does a fresh install of this build. All three pass. The update keeps the ACME email, the trusted IPs and the Access log, and needs no manual step.

## Considered options

**Flags plus `.env`, written by the panel.** Rejected: every setting would need an `.env` key and Compose interpolation, a list (trusted IPs) or a section toggle (the Access log) cannot be expressed without a shell wrapper, and the panel would edit a file the Install override can shadow.

**One editable `traefik.yml`.** Rejected: a Panel update could not change the keys the panel depends on (entrypoints, the Docker provider, the `letsencrypt` resolver) without overwriting the operator's edits, and nothing would tell those keys apart from defaults.

**A transition release with fallback flags**, where Traefik keeps its flags and uses the file only when present. Rejected: Traefik ignores the flags as soon as `--configfile` is set, so the fallback would be dead config in the Install file for a release, and the one-shot service already covers the missing-file case.

**Two releases**, one to write the file and one to switch Traefik to it. Rejected: the probe shows the 0.0.2 Updater handles the one-shot service and the new Install file in one update.

**An `nginx -t`-style check.** Traefik has none. The dry-run in a throwaway container with no network is the nearest equivalent: it catches bad YAML and unknown nested keys, which exit at once.

**Rolling back on any `ERR` line.** Rejected: ACME rate limits and DNS that does not point at this host log `ERR` on a healthy Traefik, and rolling back would hide the setting the operator just applied without fixing anything. Only `Command error`, an exit, a restart loop, or no `/healthz` within 60 s count as a failure.

## Consequences

- **Every apply drops every Domain and the panel for a few seconds.** Traefik cannot reload static config, so an apply is a restart. The review modal, Restore and Restart say so.
- **`RELIST_ACME_EMAIL` and `TRAEFIK_TRUSTED_IPS` are seeds only.** Changing them in `.env` after the first `up` does nothing. The Traefik page is where they change.
- **ADR-0027's default flips:** a fresh install writes no Access log until it is turned on in Settings. An updated install keeps the one it has.
- **The base can change under a running Traefik.** A Panel update's `up` regenerates `traefik.yml` with the new base, and when it does not recreate Traefik, the page shows "Config changed since Traefik started — Restart to apply".
- **A Traefik override in `docker-compose.override.yml` that replaces Traefik's `command:` now fails the `--configfile` invariant**, and a Panel update refuses it. Flags carried in such an override on 0.0.2 are not converted into the Traefik override.
- **When even the rollback fails**, the panel is reachable only through its loopback port over an SSH tunnel. The modal gives the commands to put a backup from `history/` back by hand. See `docs/guide/traefik.md`.
- A Migration carries `static/` with the data directory (ADR-0018).
- Spec: `.scratch/traefik-static-config/spec.md`. Guide: `docs/guide/traefik.md`.
