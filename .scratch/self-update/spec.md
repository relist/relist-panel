# Spec — Panel update and the Install script

Date: 2026-11-01 (rewritten the same day for ADR-0025).
Status: ready-for-human

ADRs: [0024](../../docs/adr/0024-panel-self-update.md) (signed manifest, Install file,
merge, invariants, lock, health check) and
[0025](../../docs/adr/0025-one-updater-and-install-script.md) (one `update` command for
the web and the shell, the Install script).

## Problem

Upgrading the panel today takes all of this:

- SSH to the host;
- `git pull`;
- edit `docker-compose.yml` by hand, following the README's "Upgrading an existing
  install";
- `docker compose up -d --build`.

An operator who misses one note gets a panel that half works, and only Traefik's log
says why. An operator who has edited the Install's `docker-compose.yml` must merge each
new version by hand.

Installing the panel on a new host means cloning the repo and building the image there.
A panel that does not start cannot be updated or repaired except by hand.

## Goal and success criteria

- The panel tells the operator when a newer version exists and what it changes.
- One button, after a preview, moves the Install to that version, with no SSH. The same
  update runs from the shell with `install.sh --update`, and works when the panel does
  not answer.
- An Install whose operator customised it only in `docker-compose.override.yml` and
  `.env` updates with no questions asked.
- An Install whose `docker-compose.yml` was edited is merged. A merge conflict stops the
  update before anything is written, and the diff is shown.
- An Install that would not satisfy the new version's Install invariants is refused
  before anything is written. The refusal names each invariant, and the file and key
  that break it.
- A running Deployment and a running scan finish before anything is written. The page and
  the script say what the update is waiting on, and the update can be cancelled while it
  waits.
- If the new panel is not healthy through Traefik within 180 s, the Updater puts back the
  Install files, `.env` and `panel.db`, and the old version runs again. The web and the
  script say so.
- The web page survives the panel restarting. It polls, shows *Restarting*, and reloads
  once the new version answers.
- On a host with Docker, one `curl … | sh` installs the panel, Traefik and the first
  admin. With `--auto` it asks nothing.

## Words

These are defined in `CONTEXT.md`:

- **Install**
- **Install file**
- **Install override**
- **Install script** (new)
- **Panel update**
- **Updater** (widened: it now runs the whole Panel update)
- **Update manifest**
- **Install invariant**

"Migration" always means a server Migration (ADR-0018). SQL changes are **schema
migrations**.

## Releases

A release is pushed from a `v*` tag by `.github/workflows/release.yml`. The workflow
stays on `workflow_dispatch` only until ticket 02's owner steps are done; then the tag
trigger written in its header comment goes back. The workflow does the following:

1. Build the panel image for `linux/amd64` and `linux/arm64` and push it to GHCR as
   `ghcr.io/relist/relist-panel:<version>`. Stamp the version and the release public
   key into the binary with `-ldflags`.
2. Write the Update manifest from the index digest and sign it with the release key,
   stored as the repo secret `RELIST_RELEASE_KEY`. Verify the signature.
3. Write `install.sh` from `release/install.sh` with the version and the digest ref
   filled in, and check it with `shellcheck`.
4. Attach `update-manifest.json`, `update-manifest.json.sig` and `install.sh` to the
   GitHub Release.
5. Tag the image `latest`, only after the Release exists.

### Update manifest

```json
{
  "schema": 1,
  "version": "1.4.0",
  "released_at": "2026-11-20T09:00:00Z",
  "image": "ghcr.io/relist/relist-panel@sha256:…",
  "notes_url": "https://github.com/relist/relist-panel/releases/tag/v1.4.0",
  "install_file": "<the version's docker-compose.yml, verbatim>",
  "invariants": [ { "kind": "…", "...": "..." } ],
  "manual_steps": [ { "id": "open-443", "text": "Open port 443 on the host firewall." } ]
}
```

- **Signature:** `.sig` is a raw ed25519 signature over the exact bytes of the manifest.
  The public key is a constant in the binary. A manifest that fails verification is
  dropped and logged; it is never shown.
- **Manual steps:** `manual_steps` lists what the Updater cannot do, such as opening a
  firewall port or setting a new `.env` key that has no default. The operator must tick
  each one in the preview before **Update** is enabled.

### Install invariant kinds (closed set)

Each kind is evaluated against the merged config, `docker compose config --format json`.
The panel code checks them; the manifest only picks kinds and values.

| kind | fields | holds when |
|---|---|---|
| `service-image` | `service`, `image` | the Service's `image` equals the manifest's digest ref, and the Service has no `build:` |
| `service-mount` | `service`, `source`, `target`, `read_only?` | a mount with that source and target exists (`${…}` expanded) |
| `service-command-has` | `service`, `arg` | the Service's `command` contains `arg` exactly |
| `service-label` | `service`, `key`, `value` | the label is present with that value |
| `service-network` | `service`, `network` | the Service is attached to the network |
| `network-external` | `network`, `name` | the top-level network is `external: true` with that name |
| `data-dir-same-path` | `service` | the data dir is mounted at the same path on both sides |

**Unknown kinds:** the binary checks a manifest's kinds against the set it knows. One it
does not know makes the version *Needs a manual update*, and the preview shows the
README link.

The first manifest's invariants turn each note in the README's "Upgrading an existing
install" into a check:
- the docker socket and the data dir are mounted on the panel Service;
- `/proc:/host/proc:ro`;
- `relist-network` is external;
- Traefik's `websecure` and `letsencrypt` flags, and `--providers.file.directory=/letsencrypt/dynamic`;
- the panel router's priority label;
- `service-image` for the panel.

## Install file and image pin

- **Image:** the panel Service has
  `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}` and no `build:` or
  `pull_policy`. The Install script and every Panel update write the digest ref to
  `RELIST_PANEL_IMAGE` in `.env`. That is the one `.env` key the Updater writes.
- **Container names:** `container_name: relist-panel` on the panel Service and
  `container_name: relist-traefik` on Traefik. The panel finds itself from `HOSTNAME`
  and labels, never by name, so nothing in the code depends on these.
- **Comment header:** the file starts with a comment block. It says the file is replaced
  by Panel update, that edits belong in `docker-compose.override.yml` and `.env`, and
  where the README covers this.
- **Embedded copy:** the Install file is embedded in the binary (`go:embed`). The
  embedded copy is the base for the change check and for the merge, and the file
  `setup` writes.
- **Building from source:** `docker-compose.override.example.yml` starts with a block
  that adds `build: .`, `image: relist-panel:local` and `pull_policy: build` to the panel
  Service. A checkout that builds from source copies it to
  `docker-compose.override.yml`. The `service-image` invariant refuses `build:`, so such
  an Install never updates itself.
- **Ignored files:** `.gitignore` lists `docker-compose.override.yml` and `.env`, so a
  checkout used as an Install never commits them.

## The Install

The `update` command needs the Install's project name, directory and config files.

- **From the web:** the panel inspects its own Container (`HOSTNAME` → the Engine API) and
  reads `com.docker.compose.project`, `com.docker.compose.project.working_dir` and
  `com.docker.compose.project.config_files`. It passes them to the command as
  `--project`, `--dir` and `--file` (repeated, in label order).
- **From the shell:** the script passes `--dir`. The command then reads the same three
  labels from any Container whose `com.docker.compose.project.working_dir` is that
  directory. With none (the Install is down), it uses the project name from `.env`'s
  `COMPOSE_PROJECT_NAME`, or the directory's base name, and `docker-compose.yml` plus
  `docker-compose.override.yml` if it exists.
- **Unavailable:** labels missing on the panel (a plain `docker run`) shows
  *Unavailable: this panel was not started with Docker Compose*. An Install started with
  `--env-file` or `COMPOSE_*` variables cannot be seen from the labels; its preview shows
  the difference (the README says so).
- **Reading files:** the command reads the Install files directly from its own mount of
  the Install directory. There is no helper Container.

## Update check

- **When:** once a day, at startup plus 5 minutes, and from **Check now**.
- **What:** fetch the latest release's `update-manifest.json` and `.sig` from GitHub,
  verify them, and keep the newest version greater than the running one.
- **Storage:** schema migration `00025` adds `panel_update_settings` (check on/off,
  last check, last error) and `panel_updates` (one row per run: id, from/to version,
  state, error, times).
- **Off:** with the check off, nothing goes out; **Check now** still works.
- **Failure:** a failed check keeps the last good result and shows the error. It never
  hides the version.

## The `update` command

```
relist-panel update --preflight [--dir PATH] [--project NAME] [--file PATH]... [--json]
relist-panel update --report ID [--steps-done ID,...] [--dir PATH] [--project NAME] [--file PATH]...
```

- **Container:** always run from the current panel image, outside the Install, with no
  Compose labels:
  ```
  docker run [--rm | -d --name relist-panel-updater-<id>] \
    --network relist-network \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v <install dir>:<install dir> -v <data dir>:<data dir> \
    <current panel image> update …
  ```
  The current image is the panel Container's image ID. From the shell, when the panel
  Container is gone, it is the image `RELIST_PANEL_IMAGE` in `.env` names.
- **`--preflight`:** runs the Preflight and writes the report to
  `<data dir>/.panel-update/preflight/<id>/report.json`. It prints the report, as text or
  with `--json` as one JSON object. Exit 0: ready. Exit 1: not ready (the report says
  why). Exit 2: usage error. Exit 3: up to date, nothing to apply.
- **`--report ID`:** applies. Run detached as `relist-panel-updater-<run id>`.
  `--steps-done` lists the manual step ids the operator confirmed; a step not listed
  refuses the run.
- **Old command:** `relist-panel panel-update apply --run` and `plan.json` are removed.
- **Help:** `relist-panel update --help` prints the usage. Without `--preflight` or
  `--report` it prints the usage and exits 2.

## Preflight (no writes)

The Preflight runs these steps in order and stops at the first that fails. It returns the
same report each time it is run on the same inputs.

1. **Manifest:** fetch the latest release's manifest and `.sig`, verify the signature,
   and compare the version with the running one. No newer version ends the Preflight
   with *Up to date* (exit 3).
2. **Kinds:** every invariant kind in the manifest is one this binary knows. Otherwise
   *Needs a manual update*, before the pull.
3. **Pull:** pull the manifest's image by digest. A pull failure shows Docker's words.
4. **Change check:** compare the Install file with the embedded copy:
   - equal → *Not edited*; the new file replaces it;
   - not equal → `git merge-file -p <ours> <embedded> <theirs>`. Exit 0 is *Edited,
     merged*. Exit 1–127 is *Edited, conflict*: show both versions and the conflicted
     hunks and stop. The message says to move the edits to
     `docker-compose.override.yml`. Exit 255 is an error.
5. **Config:** write the candidate Install file and `.env` (with the new
   `RELIST_PANEL_IMAGE`) to the report's scratch directory, next to copies of the other
   config files, and run:
   ```
   docker compose -p <project> --project-directory <real install dir> \
     -f <scratch>/docker-compose.yml -f <scratch>/docker-compose.override.yml … \
     --env-file <scratch>/.env config --format json
   ```
   The project directory stays the real one, so relative paths in the operator's files
   resolve. Every `-f` is kept, in label order. A `config` error shows Compose's words.
6. **Invariants:** evaluate each Install invariant against that JSON and list them all,
   passing and failing. For a failing one, say which file sets the value, by checking
   whether `config` without the override passes.
7. **Recreated Services:** compare `config --hash '*'` with each running Container's
   `com.docker.compose.config-hash` label and list the Services `up` will recreate. If
   `traefik` is among them, warn that every Domain drops for a few seconds.
8. **Report:** versions, release notes link, the change check (with the diff to be
   written), the invariants, the Services to be recreated, the manual steps, and a
   fingerprint of every input (manifest bytes, image ID, each config file, `.env`).

Only the latest report directory is kept. The scratch directory is the same path on host
and in the Container because the data dir is (ADR-0021).

**Migration lock:** the Preflight does not check it. In the web the page shows the live
gate next to the report; in Apply the panel refuses at the quiesce step.

## Apply

`update --report ID` does this. It writes `status.json` in
`<data dir>/.panel-update/<run id>/` atomically (temp file and rename) before and after
each step. `status.json` has one writer: the Updater.

1. **Stale runs:** if the newest run is not terminal and its Updater Container is gone or
   exited, mark it `interrupted`. If its Updater is still running, refuse.
2. **Re-check:** run the Preflight again. Refuse if it is not ready or its fingerprint
   differs from the report's. Refuse if a manual step is not in `--steps-done`.
3. **Quiesce:** write state `quiescing`. Then:
   - **The panel answers** (a `panel.json` heartbeat within 20 s): wait for
     `panel.json` state `ready`, with no time limit. Copy `waiting_on` into
     `status.json` while waiting. State `refused` (a Migration lock is held) ends the
     run as `aborted` with the panel's reason.
   - **The panel does not answer:** stop the panel Container (found by the Install's
     project and `service=panel` labels) and note `panel did not answer; a running
     Deployment may have been cut` in `status.json`.
4. **Cancel:** until step 5 starts, SIGTERM ends the run as `aborted`. From step 5 on,
   SIGTERM is ignored.
5. **Backup** to the run directory: `panel.db` with `VACUUM INTO` from the Updater's own
   connection, every Install config file, `.env`. Remove the previous run's backup only
   after this one is complete.
6. **Write:** the Install file from the report and `RELIST_PANEL_IMAGE` in `.env`,
   replacing the line if present and appending it if not. A symlinked Install file or
   `.env` is refused.
7. **Up:** `docker compose -p … --project-directory … -f … up -d`, with a 15-minute
   timeout.
8. **Health:** wait up to 180 s, polling every 2 s, until both hold:
   - the panel Container's image ID is the new digest's;
   - `GET https://<traefik IP>/healthz` with Host and SNI `RELIST_DOMAIN` (certificate
     not verified) returns 200. The Traefik Container is found by the Install's project
     and `service=traefik` labels, and its IP on `relist-network` is used.
9. **Success:** state `succeeded`, then exit.
10. **Rollback** on a failure in steps 6–8: copy the backup's Install files and `.env`
    back; stop the panel; remove `panel.db-wal` and `panel.db-shm`; restore `panel.db`;
    `up -d`; wait for health as in step 8 against the old digest. State `rolled-back`
    with the reason, or `rollback-failed` with both errors. A failed rollback keeps its
    backup.

Run states: `quiescing`, `backing-up`, `writing-files`, `compose-up`, `waiting-health`,
`rolling-back`; terminal: `succeeded`, `rolled-back`, `rollback-failed`, `aborted`,
`interrupted`.

## The panel's side

- **Watcher:** every 2 s the panel reads the newest run's `status.json`.
  - **Non-terminal:** turn the update lock on (below). While the lock is on, write
    `panel.json` every 2 s: `{state, waiting_on, heartbeat}`. `state` is `waiting` until
    the Deployment queue and the scan queue are idle, then `ready`. With a Migration lock
    held it is `refused` and the lock is not taken.
  - **Terminal:** copy the run into `panel_updates`, release the lock and remove the exited
    Updater Container.
  - **Non-terminal, Updater gone or exited:** the panel writes `interrupted` to
    `status.json` (the one case where the panel writes it, since the Updater is dead) and
    releases the lock.
- **Update lock:** the same three parts as before:
  - **Deployment queue:** held; queued Deployments are cancelled (a held queue would
    otherwise run the rows already queued); the running one finishes.
  - **Scan queue:** gated; the running scan finishes.
  - **API writes:** refused with 503 `The panel is updating`. Allowed: `/healthz`,
    `/api/auth/status`, `/api/auth/login`, `/api/auth/logout`, `GET /api/panel-update/*`
    and `POST /api/panel-update/cancel`. Password and 2FA changes are refused: a rollback
    would silently revert them.

  The daily update check skips its write while the lock is on.
- **Start locked:** a panel process reads the newest `status.json` before it serves
  requests or starts any worker, and starts with the lock on if the run is not terminal.
- **Preview:** `POST /api/panel-update/preflight` runs the `update --preflight --json`
  Container with `--rm`, waits for it, and returns its report.
- **Apply:** `POST /api/panel-update/apply` checks the report id is the latest, starts the
  Updater detached with `--report` and `--steps-done`, and returns the run id at once.
- **Cancel:** `POST /api/panel-update/cancel` runs `docker stop` on the running Updater
  while the run is `quiescing`, and refuses otherwise.
- **Prune plans:** the previous panel image and `.panel-update/` are owned by "Panel
  update" and are never offered for removal.

## The Install script

```
curl -fsSL https://github.com/relist/relist-panel/releases/latest/download/install.sh | sh
curl -fsSL …/install.sh | sh -s -- --auto --domain panel.example.com --email me@example.com
curl -fsSL …/install.sh | sh -s -- --update
```

- **Shape:** POSIX `sh`, `set -eu`, the whole body inside `main()` called on the last
  line, so a truncated download runs nothing. The release fills in `VERSION` and
  `IMAGE` (the digest ref). `RELIST_INSTALL_IMAGE` overrides `IMAGE`, for the dind test
  only; the README does not mention it.
- **Flags:** `--domain`, `--email` (admin), `--acme-email` (defaults to `--email`),
  `--auto`, `--update`, `--dir` (default `/opt/relist-panel`), `--help`. An unknown flag
  prints the help and exits 2.
- **Prompts:** read from `/dev/tty`, because stdin is the pipe from `curl`. With no
  terminal and no `--auto`, stop and say to pass `--auto`.

### Install

Checks, before anything is written:
1. Root (`id -u` is 0); otherwise say to re-run with `sudo`.
2. `docker` exists, `docker info` answers, `docker compose version` is v2. Otherwise stop
   and link to Docker's install guide. The script never installs Docker.
3. `uname -m` is `x86_64` or `aarch64`.
4. `--dir` does not exist or is empty, and no Container is named `relist-panel` or
   `relist-traefik` or carries the label `com.docker.compose.project=relist-panel`.
   Otherwise refuse and point to `--update`.
5. Ports 80 and 443 are free (`ss -ltn`). Without `ss`, warn and go on.
6. **Domain:** required, no default. Asked until it is a valid host name; with `--auto`,
   missing `--domain` stops the script.
7. **Admin email:** asked, required. With `--auto` and no `--email`,
   `admin@relist.local`.
8. **ACME email:** `--acme-email`, else the admin email, else empty. An address under
   `.local` is never used for ACME.
9. **DNS:** if the domain does not resolve, or resolves to none of the host's addresses,
   warn that Let's Encrypt cannot issue until it does, and go on.
10. `docker pull <IMAGE>`.

Then:
1. `docker network create relist-network` unless it exists.
2. `mkdir -p <dir>`, then
   `docker run --rm -v <dir>:<dir> <IMAGE> setup --dir <dir> --domain … --acme-email … --image <IMAGE>`.
   `setup` writes the embedded Install file and `.env` (`0600`) with `RELIST_DOMAIN`,
   `RELIST_ACME_EMAIL`, `RELIST_PANEL_IMAGE` and `COMPOSE_PROJECT_NAME=relist-panel`. It
   refuses if either file exists.
3. `docker compose up -d` in `<dir>`, then wait up to 60 s for
   `http://127.0.0.1:8888/healthz`.
4. Admin:
   - prompted: `docker exec -it relist-panel relist-panel create-admin --email <email>`
     with `/dev/tty` as stdin, so `create-admin` asks for the password;
   - `--auto`: `docker exec relist-panel relist-panel create-admin --email <email> --generate`,
     and the password is printed once.
5. Print: `https://<domain>`, the SSH tunnel command (`ssh -L 8888:127.0.0.1:8888 <host>`),
   where the files are, and how to update.

A failure after the first write prints what was created and the commands to remove it.

### `--update`

1. `<dir>` holds an Install (`docker-compose.yml` exists); otherwise stop.
2. Run `update --preflight --dir <dir>` with `--rm` from the current image and print the
   report. Up to date (exit 3): say so and stop with 0. Not ready: stop with its exit
   code.
3. Ask each manual step on the terminal (y/n); a "no" stops. With `--auto` and any manual
   step, refuse and print the steps.
4. Start `update --report <id> --steps-done … --dir <dir>` detached and
   `docker logs -f` it until it exits. Print the final state from `status.json`.
5. Ctrl-C stops only the log. The script then prints `docker logs -f
   relist-panel-updater-<id>` and, while the run is `quiescing`, the `docker stop`
   command that cancels it.

## Web

**Placement:**
- **Page:** a **Panel update** page at `/panel-update`, below Docker in the side menu.
- **Header:** an `Update available` tag beside the version, linking to the page.

Read Ant Design's `llms.txt` and the component pages before building. Follow
`docs/GUIDELINES.md`.

### Page states

| State | What the page shows |
|---|---|
| Up to date | running version, last checked, **Check now**, the daily-check switch |
| Update available | new version, notes link, **Preview** |
| Needs a manual update | why (unknown invariant kind, Install unavailable, `build:`), README link |
| Preview | the report; **Update** enabled once invariants pass and every manual step is ticked |
| Refused | the failing step in the server's words; conflict diff or invariant list |
| Waiting | `quiescing`: what the run waits on (`Deployment api #123`, a scan), elapsed time, **Cancel** |
| Updating | steps with their state from `status.json` |
| Restarting | the panel does not answer (network error, 502, 404): `Restarting…` with elapsed time |
| Succeeded | the new version; the page reloads itself once |
| Rolled back | `Rolled back to <old>` and the reason, as a warning `Alert` |
| Rollback failed | an error `Alert` with both errors and the backup path, plus the commands to restore by hand |
| Aborted | an info `Alert`: cancelled, or refused by a Migration lock; nothing was written |
| Interrupted | a warning `Alert`: the Updater stopped before it finished; the running version, the backup path and the commands to check by hand |
| No answer | after 5 min with no answer: `docker ps` and `docker logs relist-panel-updater-<id>` |

### Polling

- **Interval:** every 2 s while Updating or Restarting.
- **Success test:** the version from `GET /api/panel-update/status` equals the target.
- **Restarting:** a 401 means the session is gone (`panel.db` was restored to before a
  newer login), so the page sends the operator to sign in. Any network error or 5xx
  counts as Restarting, never as a failure.

### Confirmation

**Update** opens a confirm that states:
- the versions;
- the Services that will be recreated (and the Traefik warning);
- that queued Deployments are cancelled, a running Deployment or scan is waited for, and
  new ones are refused until the update ends.

It uses a plain confirm, not a `DangerModal`, because the update rolls itself back.

## API

| Method | Path | |
|---|---|---|
| GET | `/api/panel-update/status` | running version, available version, check settings, current run's state and `waiting_on` |
| POST | `/api/panel-update/check` | check now |
| PUT | `/api/panel-update/settings` | daily check on/off |
| POST | `/api/panel-update/preflight` | runs the preflight Container, returns the report with its id |
| POST | `/api/panel-update/apply` | `{report_id, manual_steps_done: [...]}`; returns the run id once the Updater has started |
| POST | `/api/panel-update/cancel` | stops the Updater while `quiescing` |

These are admin-only and go through the existing CSRF protection.

## Existing Installs (README)

An Install from before this version is moved by hand, once:

1. Replace `docker-compose.yml` with the new one, and move any edits you had made to it
   into `docker-compose.override.yml`.
2. A checkout that builds from source: copy the build block of
   `docker-compose.override.example.yml` into `docker-compose.override.yml`.
3. `docker compose up -d` (with `--build` for a source checkout). The Containers are
   recreated once under their new names, `relist-panel` and `relist-traefik`.

The live install on the dev host is a source checkout: step 2 then
`docker compose up -d --build`. Its schema moves to v27.

## Out of scope

- Downgrades, and choosing a version other than the newest.
- Rolling back after the update succeeded.
- Updating Traefik's image on its own (it changes only when an Install file changes it).
- Automatic updates without a click or a command.
- Installing Docker.
- Scheduled backups of `panel.db` and a restore command (separate work).
- Cosign/Sigstore verification (ADR-0024, deferred).
- Installs not started with Docker Compose.

## Testing

- **Unit, with a fake Docker and filesystem:**
  - the quiesce: panel answers and reaches `ready`; `refused` under a Migration lock gives
    `aborted`; no heartbeat stops the panel and goes on; `waiting_on` is copied while a
    Deployment runs;
  - SIGTERM while `quiescing` gives `aborted` and the panel releases the lock; SIGTERM
    after the backup starts is ignored;
  - a stale run with a dead Updater is marked `interrupted` before a new run;
  - the existing tests for the merge, the invariants, the health wait, rollback and the
    lock, moved to the new entry point;
  - `setup` refuses existing files; `update` flag and exit-code handling.
- **`install.sh`:** `shellcheck`.
- **End to end, in docker:dind only** (extending `scripts/probe-panel-update.sh`), with a
  local registry through `RELIST_INSTALL_IMAGE`:
  1. `install.sh --auto` installs 1.0.0; the admin password is printed;
  2. a second `install.sh` is refused;
  3. a fake long Deployment is running; `--update` to 1.1.0 waits for it, then succeeds;
  4. a run cancelled while `quiescing` ends `aborted` and the lock is released;
  5. a run forced to fail health rolls back, and every file matches byte for byte;
  6. with the panel stopped, `--update` stops nothing more, takes the backup and succeeds.
- **The live install is never touched.** The probe's panel uses the same Container and
  router names as the live one, so it runs only inside dind.

## To verify while building

- `VACUUM INTO` from the Updater's Container while the panel holds `panel.db` open in
  another Container (same bind mount, same kernel).
- A detached Updater keeps running after the SSH session that started `install.sh`
  closes.
- Changing `container_name` recreates the panel and Traefik once with `up -d` and leaves
  no `-1` Container behind.
- `https://<traefik IP>/healthz` with Host/SNI `RELIST_DOMAIN` works when Let's Encrypt has
  not issued yet (Traefik's default certificate).
- The `docker compose` in the panel image may be newer than the host's. It adopts
  Containers the older one created: seen once (v5.6.1 over v5.5.1), not yet across a
  wider gap.
