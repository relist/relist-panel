# Panel update: a signed image, a panel-owned Install file and an Updater container

Upgrading the panel means SSH to the host, `git pull`, edit `docker-compose.yml` by the README's upgrade notes, and `docker compose up -d --build`. The panel cannot do this to itself: it runs in a Container of the Install, and the `up` that recreates it would kill the process running it. Operators also edit the Install's files, so an update cannot simply overwrite them.

**Decision:** a **Panel update** moves the Install to a newer panel version, started from the web.

- **Versions come from a signed Update manifest.** Every release publishes the panel image to GHCR as `ghcr.io/relist/relist-panel`, a public package and an Update manifest on GitHub Releases. The manifest holds the version, the image digest, the version's Install file, its Install invariants and any manual steps. It is signed with an ed25519 key whose public half is compiled into the binary. The panel pulls the image by digest only. This is a second exception to "the panel never pulls an image", next to the Scanner image. _Contradicts ADR-0021 ("The panel pulls one image"), because a Panel update cannot work without pulling the panel's own image._
- **The Install file belongs to the panel; the operator customises elsewhere.** `docker-compose.yml` is shipped as is and embedded in the binary. The operator customises the Install in `docker-compose.override.yml` and `.env`. The Updater writes the Install file and one `.env` key, `RELIST_PANEL_IMAGE` (`ghcr.io/relist/relist-panel@sha256:…`), and nothing else. The image pin is kept in `.env` because a binary cannot embed a file that names its own image digest. The Install file reads `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}`, so a fresh install needs no key; the first Panel update pins the digest. `latest` is only that fallback: a Panel update never relies on a tag, because a moved tag leaves no name for the rollback target.
- **An edited Install file is merged, not overwritten.** If the file on disk equals the embedded copy, it is replaced. Otherwise `git merge-file` merges it, with the embedded copy as base and the new version's file as theirs. A conflict stops the update and shows the diff.
- **Correctness is judged on the merged config, not on the text.** The Install invariants are checked against `docker compose config --format json` of every Install file the panel Container was started from, with `.env`. The current binary evaluates them, and each one is a kind from a closed set. A manifest that uses a kind the binary does not know stops the update, which then has to be done by hand.
- **The Updater is a Container of the current panel image.** The panel finds the Install from its own Container's labels: `com.docker.compose.project`, `…project.working_dir` and `…project.config_files`. It starts the Updater with `docker run`, outside the Compose project. The Updater has the Docker socket, and the Install directory and data dir mounted at their host paths. It runs `docker compose -p <project> --project-directory <dir> -f … up -d`.
- **An update lock covers the whole run.** The lock is held from before the backup until the run reaches a terminal state:
  - the Deployment queue is held;
  - the scan queue is gated;
  - API writes are refused with 503, the same way the Migration lock refuses them.

  The lock is driven by the run's `status.json` in the data dir, not by process memory, so each panel process starts with it on if the run has not finished. That includes the new panel during its health window and the old one after a rollback. The backup is taken only once the queues are idle: `panel.db` with `VACUUM INTO`, plus the Install files and `.env`. Restoring it therefore loses nothing. If the Updater Container is gone before the run finishes, the run is marked `interrupted` and the lock is released, so a crashed Updater cannot lock the panel for good.
- **Success is checked through Traefik.** A Panel update succeeds when the panel Container runs the new digest and `/healthz` answers through Traefik for `RELIST_DOMAIN`, within 180 s. The Updater reaches Traefik by the IP of the Install's `traefik` Container. It does not use the name, because a user Service on `relist-network` can have the same name. Otherwise the Updater restores the Install files, `.env` and `panel.db`, and runs `up -d` again.

## Evidence

Checked on 2026-11-01 on the dev host (Docker Compose v5.5.1 on the host, v5.6.1 in the panel image):

- The panel Container's labels hold the project name, the working directory and every config file, including an override, all as host paths.
- `compose -p <project> --project-directory <dir> -f … up -d`, run from a `docker run` of the panel image with the directory mounted at the same path, did the following:
  - it adopted the existing Container, which kept its ID when nothing had changed;
  - it read `.env` from the project directory;
  - after a `.env` image change it recreated exactly one Container.
- Candidate files have to be checked from the real project directory. With `--project-directory <scratch>`, an override's `env_file: ./x.env` failed to load. With the real directory, the candidate files passed as `-f` and `--env-file <candidate .env>`, `config --hash` equalled the running label. `--env-file` replaces the project `.env`; it is not merged with it.
- A `command:` list in an override replaces the base list; it is not appended. An override can therefore drop a flag a new version needs without any text conflict, which is why invariants are checked on the merged config.
- `docker compose config --format json` and `config --hash '*'` both work. The hash equals the `com.docker.compose.config-hash` label, so the panel can tell before the update which Services `up` will recreate.
- `git` is in the panel image (`/usr/bin/git`, used by Sources), so `git merge-file` needs nothing new.
- A binary refuses a `panel.db` whose schema is newer than its own (`ErrSchemaAhead`, `internal/database/files.go`). Rolling back the image without the database would therefore leave the old panel unable to start.

## Considered options

**`git pull` and build on the host.** Rejected for these reasons:
- the panel has 0.25 CPU;
- the build needs Node and Go;
- a build that fails halfway leaves nothing to roll back to;
- the result cannot be checked against a signature.

**Docker Hub instead of GHCR.** Not chosen. The registry is only the image ref in the manifest, so either works. GHCR wins on setup: the workflow pushes with its own `GITHUB_TOKEN`, so there is no extra secret, and a public package costs nothing.

**Cosign keyless signatures.** Deferred. Verifying them needs the Sigstore trust root and its verification library in the panel. A single compiled-in ed25519 key uses only Go's standard library.

**The new image as the Updater.** Rejected. Rollback would then depend on code that has never run on this host. The current image is known to work here, and the invariants it checks are data in the manifest.

**Watchtower or any tag watcher.** Rejected. It changes only the image: no Install file, no backup, no rollback.

**Rollout (ADR-0023) for the panel.** Rejected. Two panels would open the same `panel.db` and both run the Deployment queue.

**A bind mount of the Install directory in the Install file.** Rejected. Every existing Install would need a manual edit before its first Panel update. The labels already say where the Install is.

## Consequences

- **Until releases exist, the Install file builds the panel** (`build: .` with `image: ghcr.io/relist/relist-panel:local` and `pull_policy: build`), so Panel update is unavailable. The local tag must never be `latest`: a `docker compose pull` would replace the build with whatever GHCR holds.
- **The first version with Panel update is installed by hand.** So is every Install whose panel Service uses `build:`, including a development checkout. An invariant fails on `build:` and tells the operator how to switch.
- **A Panel update that recreates Traefik interrupts every Domain** for a few seconds. The preview says so, using the config hash.
- **Only `.env` in the project directory is read.** The labels do not record `--env-file` or `COMPOSE_*` variables, so an Install started with those is refused with a message.
- **Rolling back after the new panel has served requests is not offered.** Backups are only restored by the Updater, during its own run.
- **Prune plans list the previous panel image and the backups** under "Panel update", so a prune cannot remove the rollback target. Only the latest backup is kept.
- **The panel calls GitHub once a day** to check for a newer version, unless the check is switched off. The check can be switched off for hosts with no route out.
- **Each release must state what it needs as Install invariants.** Each item the README used to list under "Upgrading an existing install" becomes an invariant in that release's manifest.
- Extends ADR-0018 and ADR-0019: Panel update holds the queue the same way the Migration lock does.
- Spec: `.scratch/self-update/spec.md`.
- Partly superseded by ADR-0025: the `update` command now does the Preflight, the quiesce and the backup, and the web and the Install script both start it.
