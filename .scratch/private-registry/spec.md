# Spec — Private registry and external registry credentials

Date: 2026-11-06.
Status: ready-for-human

## Problem

The operator wants CI (GitHub Actions first) to build images and push them
somewhere the panel can deploy them from, instead of building on the production
host. Later, CI will also trigger the deploy; that is a separate project.

Today the panel cannot do either half:

- A Compose can reference a private image (`ghcr.io/me/app:1`), but the panel
  has no credentials for it, so `docker compose pull` in `prepareImages`
  (`internal/composes/deploy.go`) fails with `unauthorized`.
- There is no registry on the host for CI to push into.

## Goal and success criteria

- **External registries.** An admin stores credentials for registries outside the
  panel (GHCR, Docker Hub, Harbor, …). Every pull, build and scan the panel runs
  can then reach private images on those hosts.
- **Panel registry.** An admin can turn on a `registry:3` the panel runs and
  manages, reachable over HTTPS at a hostname the admin chooses.
  - CI logs in with a named **Push credential** and pushes. A Compose deploys
    `registry.example.com/app:tag` with no further setup.
  - The admin can browse repositories and tags, see what uses a tag, and delete
    tags.
  - **Registry cleanup** keeps the newest N tags per repository plus every tag
    in use, then runs garbage collection, on a daily schedule and on demand.
    It reclaims disk space.
- **Traefik read timeout.** The admin can set the entry points' request read
  timeout on the Traefik page without writing YAML.
- The registry keeps serving while the panel is down or being updated. A panel
  crash mid-cleanup never leaves the registry read-only once the panel is back.

## Out of scope

- Triggering a Deployment from CI (deploy tokens, webhooks). Next project.
- Push credentials scoped to repositories or to pull-only. Every Push credential
  has full access (htpasswd). Scoped tokens would need the panel to be a token
  auth server; revisit if needed.
- Per-project External registry credentials. They are host-wide.
- Last-used time of a Push credential. htpasswd does not record it.
- Quotas per repository, re-tagging or copying tags in the UI.
- Pruning unused images on the host's own image store. Belongs to the Images
  and Docker pages; separate work.
- A dedicated Traefik entry point for the registry. Not needed (see Decisions).
- Mirroring or pull-through cache of Docker Hub.

## Decisions and evidence

- **The registry is `registry:3`** (CNCF Distribution v3), the operator's choice.
  Pinned by digest like the BuildKit image (ADR-0028). Record the digest at
  implementation time. It is a fourth exception to "the panel never pulls an
  image itself" (Scanner ADR-0021, panel image ADR-0024, BuildKit ADR-0028).
- **The panel runs the registry container itself**, as it does Builders. A Compose
  that the operator could edit or delete was rejected: the panel must control
  read-only mode and garbage collection reliably.
- **Push credentials use htpasswd** (bcrypt), which `registry:3` supports natively.
  Every credential has full push and pull access.
- **Traefik's read timeout is already one hour.** `internal/traefikconfig/base.yml`
  sets `entryPoints.{web,websecure}.transport.respondingTimeouts.readTimeout:
  3600s` for the 10 GiB Images upload. Traefik applies it per HTTP request, body
  included, and only per entry point, not per router (Traefik v3 reference,
  install-configuration/entrypoints). A `docker push` sends each layer in one
  `PATCH` request, so the limit is per layer, not per image. One hour per layer
  is enough, so the registry shares `websecure` on 443 and needs no entry point
  of its own.
- **Garbage collection needs read-only mode.** `registry garbage-collect` is not
  safe while pushes run. The registry is restarted with
  `storage.maintenance.readonly.enabled: true` for the duration. Pulls still
  work; pushes fail. A full stop was rejected because pulls would fail too.
- **Deleting is by digest.** The registry API deletes a manifest by digest, which
  removes every tag pointing at it. Retention and the UI account for this.
- **Registry data is a Docker volume, `relist-registry`**, not under the data
  directory. It can be rebuilt from CI, so a Migration does not move it unless
  the operator asks.
- **External registry credentials reach Docker through a per-call
  `DOCKER_CONFIG`.** Nothing stays logged in. See Behaviour.
- **No encryption at rest** (decided 2026-11-06). External registry secrets are
  stored like SSH keys: in the database, plaintext, never returned by the API.
  - A key that decrypts them would live on the same host, and the panel holds
    the Docker socket. Anyone who can read the database can also read the key.
  - Environment Variables, SSH keys and Certificate keys are plaintext too.
    Encrypting only these secrets would add no protection.
  - What limits the damage instead:
    - **Push credentials are stored only as bcrypt hashes.** The token is shown
      once and never stored, so a leaked database yields no push token.
    - **The External registry form asks for a pull-only token.** For GHCR, a PAT
      with `read:packages` only. A leaked token can then pull but not push.
  - The system credential is the one plaintext Panel registry secret.
  - Encrypting secrets at rest, with a key kept outside the data directory,
    would be a separate project covering the whole panel.

## Behaviour

### Traefik read timeout (PR 0)

- The Traefik page's Settings form gets **Request read timeout**, next to Log
  level and Access log.
- One value applies to both `web` and `websecure`. The default is `3600s`, the
  current value, so nothing changes on upgrade.
- Accepts Traefik durations (`90s`, `5m`, `1h`). Minimum `30s`. `0` (no
  timeout) is rejected.
- Help text: "Longest time to receive one request, body included. Image uploads on
  the Images page and pushes to the Registry need minutes per large layer."
- Below `10m` the form shows a warning that large image uploads and registry
  pushes may be cut off.
- Saving goes through the existing Traefik apply (ADR-0030).

### External registries (PR 1)

- The Registry page's **External registries** tab lists host, username and the
  last **Test** result (pass/fail, time).
- Add and edit take host, username and token/password. The API never returns
  the secret. Leaving it empty on edit keeps the stored one.
- The form recommends a pull-only token, for example a GHCR PAT with
  `read:packages` only.
- **Test** runs `docker login` against a throwaway `DOCKER_CONFIG` and reports
  the result.
- Before every `docker compose pull`, `build`, `up --build` and `scale --build`,
  the panel writes a `config.json`
  with an `auths` entry per External registry and the Panel registry's system
  credential. The file goes in a fresh temp dir with mode `0600`. The panel sets
  `DOCKER_CONFIG` on that call and removes the dir afterwards.
  - The `buildx` builder state the panel keeps in `~/.docker` (ADR-0028) must
    stay reachable. The temp dir is seeded with what buildx needs, or
    `BUILDX_CONFIG` points at the original location. Decide by probe.
- A pull that fails with `unauthorized` or `denied` on a host with no stored
  credential adds a Deployment log hint pointing at External registries.
- The tab works whether the Panel registry is on or off.

### Panel registry (PR 2)

**Container and routing**

- The container is `relist-registry` from the pinned `registry:3`, with
  `restart: unless-stopped`. It is on `relist-network` with no host port, and the
  `relist-registry` volume is mounted at `/var/lib/registry`.
- The panel writes `config.yml` and `htpasswd` under
  `<data dir>/.registry/` and mounts them read-only. `config.yml` sets
  `storage.delete.enabled: true` and htpasswd auth.
- Traefik routes `https://<hostname>` to the container on `websecure`. TLS uses
  the `letsencrypt` resolver, or none, in which case an uploaded Certificate
  covering the host is served by SNI (ADR-0022).

**Settings**

- Settings: Enabled, hostname, TLS (Let's Encrypt or uploaded Certificate).
- **Enable** pulls the image, writes the files, creates the container and the
  route.
- **Disable** stops and removes the container and route. The volume and files
  stay.
- If Composes reference images on the registry hostname, Disable first lists
  them and warns that their next deploy will fail to pull. Running containers
  are not affected.
- **Delete all registry data** is in a danger zone. It requires the registry to
  be disabled and the hostname typed to confirm. It removes the volume.
- **Check endpoint** requests `https://<hostname>/v2/` from the panel and reports
  whether DNS, TLS or auth failed.

**Push credentials**

- Each has a name (e.g. `github-ci`), created-at and created-by.
- The database stores only the bcrypt hash, which is what goes into `htpasswd`.
- Creating one generates a random token and shows it **once**, with copyable
  `docker login <hostname> -u <name>` and a GitHub Actions `docker/login-action`
  snippet.
- **Revoke** removes it and rewrites `htpasswd` immediately. Distribution reloads
  htpasswd when the file changes; confirm by probe, else restart the container.
- The panel keeps one hidden **system credential**, `relist-panel`, for its own
  catalog and tag API calls and for pulls. The UI does not list it.

**Repositories**

- Lists each repository with tag count and newest tag time.
- Expanding a repository lists its tags: tag, short digest, size, created, and
  **In use by** (links to Composes).
- Size is the sum of the manifest's compressed layer sizes. Shared layers count
  in every tag, so the tab also shows **Disk used**, the volume's real usage,
  separately.
- A **Kept** tag is in use or among the newest N. It carries a badge so the
  operator can see what cleanup will keep.
- **Delete tag** confirms by listing every tag that shares the digest. A tag in
  use requires typing its name.
- Each tag has a copy button for `<hostname>/<repo>:<tag>`.
- "Created" is the image config's `created`. Ties (reproducible builds with a
  fixed epoch) break by tag name, descending.

**Reconcile**

- The database is the source of truth. On panel start, and before every registry
  operation, the panel compares the container's existence, image digest, config,
  htpasswd and route with the stored settings, and repairs drift.
- When drift is found that the panel did not cause (the container was removed or
  changed by hand), the Settings tab shows **Drift** with a **Repair** button
  as well.

### Registry cleanup (PR 3)

**Settings**

- Automatic cleanup on/off, default on once the registry is enabled.
- Keep last N tags per repository, default 10, minimum 1.
- **Schedule**: frequency and time, in host time. Frequency is **Daily** or
  **Weekly** with one or more weekdays. Default Daily at `03:00`. Not a cron
  expression.
- Each run deletes tags and then runs GC, one after the other. There is no
  separate schedule for GC.
- **Run cleanup now** shows a preview first: the tags it will delete per
  repository.

**A tag is kept when any of these holds**

1. It is among the N newest tags of its repository, by created.
2. A container of a Compose runs or holds it. This reuses the Scanner's in-use
   source, `dockerexec.InUse`.
3. The latest successful Deployment of any Compose names it.
4. It points at the same digest as a kept tag.

Everything else is deleted.

**One run**

1. Compute the delete set: a pure function of tags, digests, created times, the
   in-use set and N.
2. For each digest to delete, list the repository's tags again. If a tag
   outside the delete set now points at it (a push raced), skip that digest.
   Otherwise delete it. A small race window remains; the ADR records it.
3. If step 2 had any error, stop here: no GC this run.
4. Record `gc_running` in the database. Restart the registry read-only.
5. Run `registry garbage-collect --delete-untagged /etc/distribution/config.yml`
   in a short-lived container from the same image, sharing the volume, run
   detached so it outlives a panel crash.
6. When it exits, restart the registry read-write **always**, success or not.
   Clear `gc_running`, and record the result.

**Scheduling and safety**

- Cleanup runs in the host-wide queue with the Vulnerability scans. It never runs
  while a Deployment runs, and waits if one is running.
- A Panel update's quiesce waits for a running cleanup and starts no new one.
- **On panel start**, if `gc_running` is set: wait for the GC container if it
  is still running; then restart read-write, clear the flag, and record the run
  as interrupted.
- **History** keeps the last 10 runs: time, trigger (schedule or a user), tags
  deleted, bytes freed (volume usage before and after), result, and log.
- Every run, credential change, enable/disable, tag delete and data delete is
  written to the audit log.

### Access

- Only admins see the Registry page and its API. Admins alone write Compose YAML
  (ADR-0032), so host-wide pull credentials grant non-admins nothing new.

### Server migration

- A Migration always carries the registry settings, Push credentials and External
  registries.
- The registry volume moves only when the operator ticks **Include registry
  data**, default off. Without it, the new host starts with an empty registry
  and CI must push again before Composes that use it can deploy.

## UI

One sidebar entry, **Registry**, after Builders, admin only. Tabs, in this
order:

1. **Repositories**: empty state "Registry is off" with a link to Settings
   when disabled.
2. **Push credentials**: same empty state when disabled.
3. **External registries**
4. **Settings**: status (Running / Read-only (cleanup) / Stopped / Drift /
   Error), endpoint, cleanup and history, danger zone.

The Traefik page's Settings form gains **Request read timeout** (PR 0).

Follow `docs/GUIDELINES.md` and the antd docs for the installed version.

## Error handling

| Case | Behaviour |
|---|---|
| Pulling `registry:3` fails on Enable | Status Error with the message; Enable again retries. Nothing left half-made. |
| DNS not pointing here, ACME fails | Check endpoint names the failing step. |
| GC fails | Registry back to read-write regardless; run recorded failed with log. |
| Panel dies during GC | Start-up reconcile (above). Worst case: pushes fail while the panel is down. No data loss. |
| Retention fails partway | Deleted tags stay deleted; GC skipped; next run continues. |
| External credential wrong or expired | Pull fails; Deployment log hint to External registries. |
| Host cannot reach its own hostname (no hairpin NAT) | Pulls from the Panel registry fail; Deployment log hint names this. |
| Container or files changed by hand | Reconcile repairs; Settings shows Drift with Repair. |

## Domain changes

- **CONTEXT.md**
  - Add **Registry**: the panel's own `registry:3`, one per host.
  - Add **Push credential**: a named htpasswd login to the Registry.
  - Add **External registry**: credentials for a registry the panel does not run.
  - Add **Registry cleanup**: retention plus GC.
  - Extend **Image**'s pull exceptions with the Registry image.
- **ADR-0033 Panel-managed registry**
  - registry:3 pinned by digest; the fourth pull exception.
  - Shared `websecure` because the read timeout is per entry point and already
    one hour.
  - Read-only GC; start-up reconcile.
  - Data in a volume, not migrated by default.
  - The registry outlives the panel and can be run by hand with the Docker CLI.
  - The retention race window.
  - Probe results.
- **ADR-0034 External registry credentials**
  - Per-call `DOCKER_CONFIG` instead of a lingering `docker login`.
  - Host-wide.
- **Database**: new migrations only. Never edit a committed one.

## Testing

- **Go unit**
  - The retention function: N, in-use, shared digests, ties, a repository
    entirely in use.
  - Rendering of `config.yml`, `htpasswd` and Docker `config.json`.
  - Duration validation for the Traefik read timeout.
  - The reconcile and cleanup state machine with a fake runner: crash at each step.
- **Probes on real Docker**, results recorded in ADR-0033
  - Push and pull through Traefik.
  - A layer over 1 GB.
  - Tag delete.
  - GC under read-only: pull works, push refused.
  - Killing the panel mid-GC, then reconcile.
  - htpasswd reload without restart.
  - Hairpin pull.
  - buildx with a per-call `DOCKER_CONFIG`.
- **Frontend**
  - `registry-view.test.ts` and `RegistryPage.test.tsx`.
  - Traefik settings form tests.
  - A browser check before each merge.

## Delivery

Four PRs, in order. Each is shippable alone.

0. **Traefik read timeout** setting.
1. **External registries**: storage, `DOCKER_CONFIG` injection, the tab, ADR-0034.
2. **Panel registry**: container, route, Push credentials, Repositories with
   single-tag delete, reconcile, Settings, ADR-0033, CONTEXT.md.
3. **Registry cleanup**: retention, read-only GC, schedule, history, crash
   recovery, Migration option.

## Settled questions

- Self-hosted and external both, external first (2026-11-06).
- Full-access named credentials, not scoped (2026-11-06).
- Manual delete, GC button, plus scheduled retention; host image prune excluded
  (2026-11-06).
- Registry data in a volume; Migration moves it only on request (2026-11-06).
- Shared 443, no dedicated entry point; read timeout stays as is and becomes a
  Traefik page setting (2026-11-06).
- Cleanup deletes tags and runs GC together. The schedule is daily or chosen
  weekdays at a set time (2026-11-06).
- No encryption at rest. Push credentials are hash-only; the External registry
  form recommends pull-only tokens (2026-11-06).
