# One Updater command for the web and the shell, and an Install script

ADR-0024 split a Panel update between two processes. The panel ran the Preflight, took the update lock, waited for the queues and backed up `panel.db`; only then did it start the Updater for the writes, `up` and rollback. That left three gaps:

- a Panel update could only start from the web, so a panel that does not answer could not be updated or repaired;
- the Apply request waited on the running Deployment with no feedback, because the quiesce ran inside the request;
- a new host had no install path short of cloning the repo and building the image.

**Decision:** one command, `relist-panel update`, does the whole Panel update, and the web and a shell script both start it the same way. A `curl | sh` Install script sets up a new host from a release.

- **`relist-panel update --preflight` checks; `relist-panel update --report <id>` applies.** Both run in a short-lived Container of the current panel image, outside the Install, with the Docker socket, the Install directory and the data dir at their host paths. The command fetches and verifies the Update manifest itself, so it does not need the panel to be running. `--preflight` writes a report and changes nothing else. `--report` re-runs the Preflight, refuses if the result differs from the report, and then quiesces, backs up, writes, runs `up -d`, waits for health and rolls back on failure.
- **The web and the script make the same three calls.** Preview is a `--preflight --json` Container whose report the panel shows. Update starts `--report` detached, as `relist-panel-updater-<id>`, and returns at once. The Install script prints the report, asks for the manual steps on the terminal, starts the same detached Container and follows its log. Because the Updater is detached, a dropped SSH session or a closed tab never stops a run halfway.
- **The Updater asks the panel to quiesce through files in the run directory.** The Updater writes `status.json` (state `quiescing`); the panel writes `panel.json`. The panel's watcher turns the update lock on, writes `waiting` with what it waits on and a heartbeat, and writes `ready` once the Deployment queue and the scan queue are idle, or `refused` while a Migration lock is held. Each file has one writer. The Updater waits for `ready` with no time limit. Until the backup starts, SIGTERM to the Updater (`docker kill --signal TERM`, never `docker stop`, whose SIGKILL could land mid-update) aborts the run cleanly and the panel releases the lock.
- **A panel that does not answer is stopped, not waited for.** With no heartbeat within 20 s, the Updater stops the panel Container so nothing writes `panel.db`, says so in the run's state, and goes on. This is the recovery path: a running Deployment may be cut.
- **The Updater takes the backup.** `VACUUM INTO` from the Updater's own SQLite connection, while the panel is locked and idle. The panel is not stopped first: if the Updater dies before `up`, the old panel is still running and the watcher marks the run `interrupted`.
- **The Install file names its Containers and runs the published image.** `container_name: relist-panel` and `relist-traefik`, so `docker exec relist-panel …` works from anywhere on the host; there is one Install per host anyway. The panel Service has `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}` and no `build:`. Building from source moves to the Install override, from a block in `docker-compose.override.example.yml`, with the local image named `relist-panel:local`.
- **The Install script ships with each release.** The release workflow writes the version and the image digest into `install.sh` and attaches it to the GitHub Release, so `releases/latest/download/install.sh` always installs the image released with it. The script checks everything before it writes anything, refuses a host that already has an Install, and never installs Docker.

## Considered options

**Keep the quiesce in the panel and add a separate offline updater for the shell.** Rejected. Two paths to the same writes, each needing its own tests, and the web path would keep the blocking Apply request.

**Talk to the panel over its control socket (ADR-0019) through `docker exec`.** Not chosen. It gives a request and a reply, but the lock must survive both processes restarting, so its state has to live in the run directory anyway. Files in the run directory carry both the lock and the reply.

**Stop the panel before the backup in every run.** Rejected. If the Updater dies between the stop and `up`, the panel stays down with nothing to restart it.

**A time limit on the wait for the running Deployment.** Rejected by the operator. A limit that aborts surprises nobody but leaves the update undone; a limit that cuts the Deployment destroys work. The page shows what the run waits on, and Cancel is one click.

**Install Docker from the script (`get.docker.com`).** Rejected. The script would touch the host's packages and need to follow every distribution. Hosts that run the panel are expected to have Docker; the script stops and links to Docker's install guide.

**A script from `main` (`raw.githubusercontent.com`).** Rejected. It can be newer than the latest image and install a combination that was never released.

## Consequences

- Supersedes these parts of ADR-0024: the panel no longer quiesces or backs up, and no longer starts a helper Container to read the Install; `panel-update apply --run` and `plan.json` are gone. The signed manifest, the Install file rules, the merge, the invariants, the update lock and the health check through Traefik stay as ADR-0024 decided them.
- **A Panel update can run while the panel is down**, from `install.sh --update`. It then cuts any running Deployment.
- **The panel is unavailable a few seconds longer** in a Panel update started from the shell when the panel does not answer. Deployed Services are not affected: they are other Compose projects.
- **A checkout that builds from source must copy the build block into `docker-compose.override.yml`** before its next rebuild, or `up` pulls `latest` from GHCR. Its Panel update shows *Needs a manual update*, because the `service-image` invariant refuses `build:`.
- **Renaming the Containers recreates them once**, at the first `up` with the new Install file. Commands that name `relist-panel-panel-1` change to `relist-panel`.
- **One Install per Docker host**, now enforced by the Container names as well as by the ports, `relist-network` and the data dir.
- Spec: `.scratch/self-update/spec.md`.
