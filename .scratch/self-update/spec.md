# Spec — Panel update from the web

Date: 2026-11-01.
Status: ready-for-human

ADR: [docs/adr/0024-panel-self-update.md](../../docs/adr/0024-panel-self-update.md).

## Problem

Upgrading the panel today takes all of this:

- SSH to the host;
- `git pull`;
- edit `docker-compose.yml` by hand, following the README's "Upgrading an existing
  install";
- `docker compose up -d --build`.

An operator who misses one note gets a panel that half works, and only Traefik's log
says why (HTTPS Domains failing, Certificates ignored, zero-downtime Domains 404). An
operator who has edited the Install's `docker-compose.yml` must merge each new version
by hand.

## Goal and success criteria

- The panel tells the operator when a newer version exists and what it changes.
- One button, after a preview, moves the Install to that version, with no SSH:
  - it replaces or merges the Install file and pins the new image;
  - it runs `up -d`.
- An Install whose operator customised it only in `docker-compose.override.yml` and
  `.env` updates with no questions asked.
- An Install whose `docker-compose.yml` was edited is merged. A merge conflict stops the
  update before anything is written, and the diff is shown.
- An Install that would not satisfy the new version's Install invariants is refused
  before anything is written. The refusal names each invariant, and the file and key
  that break it.
- If the new panel is not healthy through Traefik within 180 s, the Updater puts back the
  Install files, `.env` and `panel.db`, and the old version runs again. The web says so.
- The web page survives the panel restarting. It polls, shows *Restarting*, and reloads
  once the new version answers.

## Words

These are defined in `CONTEXT.md`:

- **Install**
- **Install file**
- **Install override**
- **Panel update**
- **Updater**
- **Update manifest**
- **Install invariant**

"Migration" always means a server Migration (ADR-0018). SQL changes are **schema
migrations**.

## Releases

A release is pushed from a `v*` tag by a GitHub Actions workflow. The workflow does the
following:

1. Build the panel image for `linux/amd64` and `linux/arm64` and push it to GHCR
   as `ghcr.io/relist/relist-panel:<version>` and `ghcr.io/relist/relist-panel:latest`. Stamp the version into the binary with
   `-ldflags -X main.version=<version>`.
2. Write the Update manifest and sign it with the release key, stored as a repo secret.
3. Attach `update-manifest.json` and `update-manifest.json.sig` to the GitHub Release.

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

There are two phases:

- **Until releases exist** (ticket 01): the panel Service keeps `build: .` and gains
  `image: ghcr.io/relist/relist-panel:local` and `pull_policy: build`. The image gets a stable name, and
  `docker compose pull` never swaps in what GHCR holds. Panel update shows
  *Unavailable*. The local tag is never `latest`, the tag the published image uses.
- **Once releases exist** (ticket 08): the panel Service has
  `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}` and no `build:`:
  - a fresh install needs no key and starts from `latest`;
  - the first Panel update writes the digest ref to `RELIST_PANEL_IMAGE` in `.env`;
  - that is the one `.env` key the Updater writes;
  - from then on, nothing depends on a tag.
- **Comment header:** the file starts with a comment block. It says the file is replaced
  by Panel update, that edits belong in `docker-compose.override.yml` and `.env`, and
  where the README covers this.
- **Embedded copy:** the Install file is embedded in the binary (`go:embed`). The
  embedded copy is the base for the change check and for the merge.
- **Overrides in the repo:** the repo ships `docker-compose.override.example.yml`, not an
  override, so a fresh clone has none.
- **Development** (from ticket 08): a development checkout builds with `build: .` from
  `docker-compose.dev.yml`. The `service-image` invariant refuses it, so a development
  panel never updates itself.

## The Install, found from labels

At startup and before each preflight, the panel inspects its own Container (`HOSTNAME`
→ the Engine API) and reads its labels:

- `com.docker.compose.project`
- `com.docker.compose.project.working_dir`
- `com.docker.compose.project.config_files` (comma-separated, host paths)

**Unavailable states:**
- Labels missing (a plain `docker run`): Panel update shows *Unavailable: this panel was
  not started with Docker Compose*.
- Started with `--env-file` or `COMPOSE_*` variables: the labels cannot show it. The
  preflight's `config` run would then differ from the running config-hash of the panel
  Service, and the page shows *Unavailable: the Install's configuration cannot be
  reproduced*. To verify: whether a config-hash mismatch reliably detects this, or
  whether a different check is needed.

**Reading the files:** the panel reads the Install files through a short-lived helper
Container of its own image, with the Install directory mounted read-only at the same
path. This is the one-run-per-operation pattern of ADR-0010. The panel itself mounts
nothing new.

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

## Preflight (no writes)

**Preview** runs these steps in order and stops at the first that fails. It returns the
same report each time it is run on the same inputs.

1. **Gate:** no Migration lock is held, and the Install is available.
2. **Pull:** pull the manifest's image by digest. A pull failure shows Docker's words.
3. **Change check:** read the Install file and compare it with the embedded copy:
   - equal → *Not edited*; the new file replaces it;
   - not equal → `git merge-file -p <ours> <embedded> <theirs>`. Exit 0 is *Edited,
     merged*. A conflict is *Edited, conflict*: show both versions and the conflicted
     hunks and stop. The message says to move the edits to
     `docker-compose.override.yml`.
4. **Config:** write the candidate Install file and `.env` (with the new
   `RELIST_PANEL_IMAGE`) to a scratch directory, next to copies of the other config
   files. Run `config --format json` inside the read-only helper:
   ```
   docker compose -p <project> --project-directory <real install dir> \
     -f <scratch>/docker-compose.yml -f <scratch>/docker-compose.override.yml … \
     --env-file <scratch>/.env config --format json
   ```
   - **Project directory:** it stays the real directory, so relative paths in the
     operator's files resolve to the real paths (`./conf:/conf`, `env_file: ./x.env`,
     `secrets`/`configs` files).
   - **Files:** every `-f` from the labels is kept, in label order.
   - **Errors:** a `config` error shows Compose's words.

   Spiked on 2026-11-01:
   - with `--project-directory <scratch>`, an override's `env_file: ./x.env` failed;
   - with the real directory and the scratch files, `config --hash` equalled the
     running Container's label;
   - `--env-file` replaces the project `.env`; it is not merged with it.
5. **Invariants:** evaluate each Install invariant against that JSON and list them all,
   passing and failing. For a failing one, say which file sets the value. Tell the
   override apart from the Install file by checking whether `config` without the
   override passes.
6. **Recreated Services:** compare `config --hash '*'` with each running Container's
   `com.docker.compose.config-hash` label and list the Services `up` will recreate. If
   `traefik` is among them, warn that every Domain drops for a few seconds.
7. **Report:**
   - versions;
   - release notes link;
   - Not edited / Edited, merged (with the diff to be written);
   - the invariants;
   - the Services that will be recreated;
   - `manual_steps` as checkboxes;
   - the Gate's live state.

The scratch directory is under `<data dir>/.panel-update/preflight/`. It is the same
path on host and panel because the data dir is (ADR-0021).

## Apply

**Update** sends the preflight's report id. The panel re-runs the preflight and refuses
with 409 if the result differs (the files changed in between). Then:

1. **Quiesce.** Write `status.json` with state `quiescing` first. That turns on the
   **update lock**, which has three parts:
   - **Deployment queue:** held (`QueueControl.Hold`); then wait until it is idle.
   - **Scan queue:** gated, the same way it yields to the Migration lock; then wait for
     its current task to finish.
   - **API writes:** refused with 503 `The panel is updating`. These stay allowed:
     `/healthz`, `/api/auth/*` and `GET /api/panel-update/*`. This is the same
     middleware shape as the Migration lock (`allowedWhileLocked`), with its own
     allowlist.

   The daily update check skips its write while the lock is on.

   **Start locked.** Any panel process (the new one, or the old one after a rollback)
   checks the newest `status.json` before it serves requests or starts any worker. If
   the state is non-terminal, it starts with the update lock on. Without this, the new
   panel could take writes or run Deployments before the 180 s health deadline, and a
   rollback would restore a `panel.db` that does not know about them.
2. **Backup** to `<data dir>/.panel-update/<id>/`:
   - `panel.db` (`VACUUM INTO`);
   - every Install config file;
   - `.env`;
   - `status.json`.

   Remove the previous run's backup only after this one is complete.
3. **Start the Updater:**
   ```
   docker run -d --name relist-panel-updater-<id> \
     --network relist-network \
     -v /var/run/docker.sock:/var/run/docker.sock \
     -v <install dir>:<install dir> -v <data dir>:<data dir> \
     <current panel image digest> panel-update apply --run <id>
   ```
   - **Project label:** none, so `up` never treats it as an orphan of the project.
   - **Cleanup:** a stale `relist-panel-updater-*` from an earlier run is removed first,
     unless it is still running; then the update is refused.
4. **The Updater** (`relist-panel panel-update apply`) writes `status.json` before and
   after each step:
   1. write the Install file (from the preflight) and set `RELIST_PANEL_IMAGE` in `.env`,
      replacing the line if present and appending it if not;
   2. `docker compose -p … --project-directory … -f … up -d`;
   3. wait up to 180 s until both of these hold:
      - the `panel` Container's image ID is the new digest;
      - `GET https://<traefik IP>/healthz` with Host and SNI `RELIST_DOMAIN`
        (certificate not verified) returns 200. The Updater finds the Traefik
        Container by its Compose labels (the Install's project and
        `service=traefik`) and uses its IP on `relist-network`. The name `traefik` is
        not used, because a user Service on that shared network can have the same
        name;
   4. success: state `succeeded`, then exit;
   5. failure in any step: copy the backup's Install files and `.env` back, then
      restore `panel.db`:
      - stop the panel Container;
      - replace the file;
      - `up -d`;
      - wait for health as in step 3, against the old digest;
      - state `rolled-back` with the reason, or `rollback-failed` with both errors.
5. **Whichever panel is running** watches `status.json` of the newest run:
   - **Terminal state** (`succeeded`, `rolled-back`, `rollback-failed`): copy the state
     into `panel_updates`, release the update lock and remove the exited Updater
     Container.
   - **Non-terminal, but the Updater Container is gone or exited** (it crashed, or the
     host rebooted): mark the run `interrupted` and release the lock. The page shows
     the running image and the backup path. Without this, a dead Updater would leave
     the panel locked for good.

The backup's `panel.db` is not restored after a successful update.

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
| Preview | the report from Preflight; **Update** enabled once invariants pass and every manual step is ticked |
| Refused | the failing step in the server's words; conflict diff or invariant list |
| Updating | steps with their state from `status.json` |
| Restarting | the panel does not answer (network error, 502, 404): `Restarting…` with elapsed time |
| Succeeded | the new version; the page reloads itself once |
| Rolled back | `Rolled back to <old>` and the reason, as a warning `Alert` |
| Rollback failed | an error `Alert` with both errors and the backup path, plus the commands to restore by hand |
| Interrupted | a warning `Alert`: the Updater stopped before it finished; the running version, the backup path and the commands to check by hand |
| No answer | after 5 min with no answer: the commands to check on the host (`docker ps`, `docker logs relist-panel-updater-<id>`) |

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
- that Deployments wait until the update ends.

It uses a plain confirm, not a `DangerModal`, because the update rolls itself back.

## API

| Method | Path | |
|---|---|---|
| GET | `/api/panel-update/status` | running version, available version, check settings, current run's state |
| POST | `/api/panel-update/check` | check now |
| PUT | `/api/panel-update/settings` | daily check on/off |
| POST | `/api/panel-update/preflight` | runs Preflight, returns the report with an id |
| POST | `/api/panel-update/apply` | `{report_id, manual_steps_done: [...]}` |

These are admin-only and go through the existing CSRF protection.

## One-time manual upgrade (README)

The version that ships Panel update is installed by hand, once:

1. Replace `docker-compose.yml` with the new one.
2. Move any edits you had made to it into `docker-compose.override.yml`.
3. Run `docker compose pull panel && docker compose up -d`. With no `RELIST_PANEL_IMAGE`
   in `.env`, this starts `ghcr.io/relist/relist-panel:latest`. The first Panel update pins the digest.

After that, Panel update does the rest. The "Upgrading an existing install" notes move
into the manifests' invariants and `manual_steps`.

## Out of scope

- Downgrades, and choosing a version other than the newest.
- Rolling back after the update succeeded.
- Updating Traefik's image on its own (it changes only when an Install file changes it).
- Automatic updates without a click.
- Cosign/Sigstore verification (ADR-0024, deferred).
- Installs not started with Docker Compose.

## To verify while building

- A `config-hash` mismatch on the panel Service catches an Install started with
  `--env-file` or `COMPOSE_FILE`.
- The Engine API reports the image of a Container created from a digest ref as that
  digest's ID on both Docker 29 and the version the README requires.
- `https://<traefik IP>/healthz` with Host/SNI `RELIST_DOMAIN` works when Let's Encrypt has
  not issued yet (Traefik's default certificate).
- The `docker compose` in the panel image may be newer than the host's, which is
  normal. It adopts Containers the older one created: seen once (v5.6.1 over v5.5.1),
  but not yet across a minor gap of more than one.
