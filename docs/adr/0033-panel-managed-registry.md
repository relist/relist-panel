# The panel runs its own registry:3 container, on the shared websecure entry point

The operator wants CI (GitHub Actions) to build images and push them to this host, so Composes can deploy them without a third-party registry (`.scratch/private-registry/spec.md`). The registry must be switchable on and off, and its disk must be reclaimable.

**Decision:** The panel runs one **Registry** per host: a `relist-registry` container from `registry:3` (CNCF Distribution 3.1.2), pinned by digest in `registry.DefaultImage`.
- **Container.** It is on `relist-network` with no host port, `restart: unless-stopped`, and its data in the Docker volume `relist-registry` at `/var/lib/registry`.
- **Files.** The panel writes `config.yml` (`storage.delete.enabled: true`, htpasswd auth) and `htpasswd` under `<data dir>/.registry/` (`0700` dir, `0600` htpasswd) and mounts the directory read-only at `/etc/distribution`.
- **Route.** Traefik labels route `https://<hostname>` on `websecure`, with an HTTP→HTTPS redirect on `web`. TLS is the `letsencrypt` resolver, or an uploaded Certificate served by SNI (ADR-0022).
- **Settings.** The settings (enabled, hostname, TLS) live in `registry_settings` (migration `00036`) and are the source of truth. On panel start and before every Registry operation, the panel compares the container (by a `relist.registry.hash` label over image, hostname, TLS and config) with them and recreates it on drift. Drift the panel did not cause shows as **Drift**; Drift, Stopped and Error all offer **Repair**. Calls to the registry API (repositories, tags, delete) reconcile first.
- **Hostname.** A saved Registry hostname is reserved: it cannot be the panel's domain or any Domain, and no Domain can take it.
- **Push credentials** are named htpasswd logins, stored only as bcrypt hashes; the token is shown once. One hidden system login, `relist-panel`, is stored in plaintext so the panel can call the registry API (at `http://relist-registry:5000`) and pull. It reaches every pull and build through the same per-call Docker config as External registries (ADR-0034).
- **Pull exception.** Pulling `registry:3` is the fourth exception to "the panel never pulls an image itself", after the Scanner (ADR-0021), the panel image (ADR-0024) and BuildKit (ADR-0028).
- **Deploys record their images** (`compose_image_refs`, migration `00037`), so Disable can warn which Composes will fail to pull and tag delete can ask for confirmation when a tag is in use.

**Shared `websecure`.** Traefik's `readTimeout` applies per entry point, not per router, and per HTTP request. A `docker push` sends each layer in one request, so the limit is per layer. `base.yml` already sets one hour for the 10 GiB Images upload, and the Traefik page can change it (PR 0). The Settings tab shows the current value. A dedicated entry point and port were rejected: one hour per layer is enough, and another port needs another firewall rule.

**Cleanup is read-only GC** (PR 3). Distribution's `garbage-collect` is not safe during pushes, so a cleanup run:
1. deletes the tags retention picks;
2. restarts the registry with `storage.maintenance.readonly.enabled: true`, so pulls keep working and pushes get 405;
3. runs `garbage-collect --delete-untagged` in a short-lived `relist-registry-gc` container on the same volume, with no network;
4. always restarts the registry read-write, success or failure.

A `gc_running` flag lets start-up reconcile finish an interrupted run. A full stop was rejected because pulls would fail too.

**Deleting is by digest.** The API deletes a manifest, so every tag on it goes. The UI lists the other tags before deleting. Before each digest delete, retention re-lists the repository and skips the digest if a tag outside its delete set now points at it. A push landing between that re-list and the delete can still lose its tag — a window of one HTTP round trip. CI can push again.

## Considered options

**A Compose the operator manages.** Rejected: the operator could edit or delete it, and the panel must reliably control read-only mode and GC.

**Token auth (a JWT issuer) instead of htpasswd.** Rejected: per-repository scopes are not needed yet, since every Push credential is full access by decision, and htpasswd is native to `registry:3`.

**Registry data under the data directory.** Rejected: it would make every Migration and backup carry images that CI can rebuild. The volume moves only when a Migration ticks **Include registry data** (PR 3).

## Consequences

- **The Registry outlives the panel.** It is an ordinary container with `restart: unless-stopped`, so it keeps serving if the panel stops. It can be run by hand with the Docker CLI using the files in `<data dir>/.registry/`.
- **Hairpin access.** A Compose pulls `<hostname>/<repo>:<tag>` through the public hostname, so the host must reach its own hostname. When it cannot, the Deployment log names this (Task 15).
- **Delete all registry data** removes the volume. It needs the Registry disabled and the hostname typed.
- **Probes** (2026-11-06 and 2026-11-07, Docker 29.8, registry 3.1.2):
  - The config path is `/etc/distribution/config.yml`, and the image runs as root, so it reads a `0600` htpasswd.
  - It reloads htpasswd when the file is replaced by rename in a directory mount, so revoking a credential needs no restart.
  - Read-only maintenance mode serves pulls and answers pushes with 405.
  - `garbage-collect --delete-untagged` works. On an empty registry it exits 1 with `Path not found: /docker/registry/v2/repositories`; cleanup treats that as nothing to collect.
  - Without an HTTP secret the registry logs `No HTTP secret provided`. That is harmless for one instance.
  - A buildx push with provenance makes an OCI index with an `unknown/unknown` attestation entry; tag info skips it and reads size and created from the platform manifest.
  - `RegistryHost` was exercised against real Docker: create, inspect, restart, GC, volume usage, remove.
