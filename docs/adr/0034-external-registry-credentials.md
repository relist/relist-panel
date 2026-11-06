# Pulls and builds log in to External registries with a Docker config made for each call

A Compose could name a private image (`ghcr.io/me/app:1`), but the panel had no credentials for it, so `docker compose pull` in `prepareImages` failed with `unauthorized`. The operator wants CI to push images to a registry and the panel to deploy them (`.scratch/private-registry/spec.md`).

**Decision:** An admin stores **External registries** on the Registry page: host, username and a token or password, in `external_registries` (migration `00035`). Each `docker compose` call that may pull or build runs with `DOCKER_CONFIG` pointing at a fresh `0700` temp directory. Its `config.json` (`0600`) has one `auths` entry per External registry, with Docker Hub under `https://index.docker.io/v1/`. The directory is removed when the call returns.
- The calls are `pull`, `build`, and `up` and `scale` whenever they may pull or build. `ps`, `logs`, `config` and `down` get no logins.
- Moving `DOCKER_CONFIG` also moves where buildx keeps its builders, so every Builder (ADR-0028) would vanish for that call. The call therefore also gets `BUILDX_CONFIG` set to the original directory's `buildx`, unless the environment already sets it.
- **Test** runs `docker login --password-stdin` against a throwaway `DOCKER_CONFIG` and stores the result. A login never stays behind.
- A pull that fails with `unauthorized`, `denied` or `authentication required` adds a hint pointing at Registry → External registries to the Deployment's failure.

Credentials are host-wide and admin-only. Only admins write Compose YAML (ADR-0032), so a credential gives a non-admin nothing new.

## Considered options

**`docker login` once, kept in the panel container's `~/.docker`.** Rejected:
- The login would outlive a deleted credential until someone logged out.
- A panel rebuild wipes `~/.docker` (ADR-0028), so the logins would silently disappear.
- The panel would have to replay every login on start.

**Credentials per Project.** Rejected for now. Only admins can name an image, so per-Project scoping would protect nothing yet.

**Encrypting secrets at rest.** Rejected:
- The key would have to sit on the same host as the database, and the panel holds the Docker socket, so anyone who can read one can read the other.
- Environment Variables, SSH keys and Certificate keys are already plaintext, so encrypting only these secrets adds no protection.
- What limits the damage instead: the form asks for a pull-only token (a GitHub PAT with `read:packages`), and the API never returns a secret.
- Encryption with a key outside the data directory would be a separate, panel-wide decision.

## Consequences

- A secret stays in the database until the External registry is deleted, and leaves the host with a Migration's database.
- Each pull or build writes and removes one small temp directory. A crash mid-call can leave a `relist-auth-*` directory in the panel container's temp dir; it holds the logins and is gone when the container is recreated.
- Probes (2026-11-06 and 2026-11-07, Docker 29.8):
  - With only `DOCKER_CONFIG` swapped, `docker buildx ls` lost a `docker-container` builder; with `BUILDX_CONFIG` set to the original `buildx` directory, it was listed again.
  - A wrong GHCR token on Test answered `Error response from daemon: Get "https://ghcr.io/v2/": denied: denied`, and left no temp directory.
