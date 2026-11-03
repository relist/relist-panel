# Builds run on panel-managed Builders

A build ran on the host dockerd's own BuildKit, with no limits. BuildKit lives inside dockerd, not inside the panel container, so the panel container's limits never reached it, and one heavy build could take every CPU and all memory. When memory ran out, the kernel's OOM killer picked the largest process, which could be a running database rather than the build. `build:` in a Compose file has no CPU or memory keys, and `docker build --memory`/`--cpu-*` only work on the legacy builder; BuildKit ignores them.

**Decision:** The operator defines **Builders**. Each one is a buildx builder with the `docker-container` driver, named `relist-builder-<id>`: one BuildKit container with `--cpu-quota`/`--cpu-period`, `--memory` and an equal `--memory-swap`, an optional `max-parallelism` in a `buildkitd.toml`, and one state volume that holds its cache. Each Compose picks the panel default, Docker default (dockerd's own builder, unlimited) or one Builder. A fresh or upgraded install keeps Docker default as the panel default. A Deployment snapshots the resolved Builder when it is queued, and the panel sets `BUILDX_BUILDER` only on `docker compose` calls that can build (`build`, `up --build`, `scale --build`).

The database is the source of truth, and the panel ensures a Builder on use. Before the first build of a Deployment, it creates the builder (attaching to an existing container when its own `~/.docker` was lost in a rebuild), bootstraps it, and runs `docker update` when the container's limits differ from the stored ones, because `buildx create` never changes an existing container's limits.

All Builders run one BuildKit image: `moby/buildkit:v0.34.2` pinned by digest, which an operator can override panel-wide with another reference by digest. Saving a custom image pulls it first. A Builder switches to a new image (after a setting change or a Panel update that bumps the default) on its next use, by `buildx rm --keep-state` and create, but only when no other build holds it. The cache survives that re-create.

## Considered options

**A per-build `docker update` on one shared builder.** Rejected: two builds running at once would overwrite each other's limits. A Builder is a fixed pool instead; builds on one Builder share it, and builds on different Builders do not affect each other.

**`--cgroup-parent` with a systemd slice.** Rejected: Compose's `build:` has no `cgroup_parent` key, and setting a slice's limits needs root on the host, which the panel in its container does not have.

**Limiting dockerd through systemd.** Rejected: it limits every running container, not just builds.

**A BuildKit image per Builder.** Rejected for now: one setting covers the need to move off a bad release, and per-Builder images add a field to every form for a rare case.

## Consequences

- **A Builder cannot see the host's image store.** `FROM` an image that exists only on the host (loaded on the Images page, or built by another Service of the same Compose) fails on a Builder with `pull access denied`. Such a Compose stays on Docker default, or uses `additional_contexts: { base: "service:<name>" }` for a sibling Service. The Deployment log says so when it sees this failure.
- **Limits apply to the Builder as a whole, not to each step.** Inside a step, `nproc` still reports every host CPU, so tools that size thread pools by it run slower rather than smaller.
- **A step over its memory limit is killed** (`cannot allocate memory`, or exit code 137); buildkitd survives and the Builder keeps working. The log names the Builder and its limit.
- **Caches are duplicated.** Each Builder keeps its own cache beside the host's image store, and its first build pulls base images itself. Each Builder has **Prune cache**, and the Docker page's build cache prune keeps covering Docker default only.
- **The Builder container is privileged** and restarts `unless-stopped`; both are buildx defaults. The panel already holds the read-write Docker socket, so this grants nothing new.
- **The BuildKit image is a third exception** to "the panel never pulls an image", after the Scanner's image (ADR-0021) and the panel's own image (ADR-0024).
- **A Migration carries Builder definitions and each Compose's choice**, not their containers or caches. The first build on each Builder on the new host is cold.
- Probes (2026-11-03 and 2026-11-04, Docker 29.8, Compose v5.5.1 and v5.6.1, buildx 0.37 and 0.38.1): a 900 MB allocation under a 512 MB limit failed and buildkitd survived; a 2-thread loop took 12 s under 0.5 CPU against 3 s unlimited; DNS, `extra_hosts`, `secrets`, `ssh` and `additional_contexts: service:` behaved as on Docker default; a 2.47 GB image built in 15 s warm against 16 s on Docker default, and 54 s cold; a re-create from v0.34.2 to v0.34.1 kept the cache.
- Spec: `.scratch/build-resource-limits/spec.md`.
