# Spec — Build resource limits with Builders

Date: 2026-11-03.
Status: ready-for-human

## Problem

A build runs on the host's dockerd with no limits. BuildKit lives inside dockerd,
not inside the panel container, so the panel container's own limits do not reach it.
One heavy build (`npm ci`, a Rust or Go compile) can take every CPU and all memory,
and the apps already running on the host slow down or get OOM-killed.

The operator cannot cap a build today. `build:` in a Compose file has no CPU or
memory keys, and `docker build --memory`/`--cpu-*` work only on the legacy builder.
BuildKit ignores them.

## Goal and success criteria

- A host-wide **Builders** page lists Builders. Each Builder has a name, a CPU limit,
  a memory limit and an optional max parallel steps setting.
  - The list always contains **Docker default**: dockerd's own builder, unlimited.
    It cannot be edited or deleted.
  - The operator can add, edit and delete Builders.
  - One entry is marked as the **panel default**. A fresh install and an upgraded
    install both start with Docker default as the panel default, so nothing changes
    until the operator opts in.
  - Each Builder row shows its status (running, stopped, not created yet, error),
    its cache size, and a **Prune cache** action.
- Each Compose has a **Builder** setting: "Use panel default" (the default), or any
  entry from the list, Docker default included.
- Every build of that Compose runs on the chosen Builder. This covers the build steps
  of a Deployment with a Source, and `up --build` / `scale --build` for a Compose
  without a Source.
- The Deployment log says which Builder ran the build and its limits, for example
  `Builder: large (2 CPU, 4 GB, 4 parallel steps)`.
- If a build fails because it ran out of memory, the log says so in plain words and
  names the Builder and its limit. It does not stop at the bare
  `cannot allocate memory` from BuildKit.
- The design must keep working if Deployments later run concurrently. Nothing changes
  a Builder's limits per build.

## Out of scope

- Concurrent Deployments. The queue stays one at a time (ADR-0016). This spec only
  avoids choices that would block concurrency later.
- Per-Service Builders. The setting is per Compose.
- Limits on disk, PIDs or network for builds.
- Remote Builders on other hosts, or the `kubernetes` and `remote` buildx drivers.
- Cache export or import (`cache_from`/`cache_to`) between Builders.
- Moving Builder caches during a Migration (see Migration below).
- A per-Builder BuildKit image. The image is one panel-wide setting.
- A Docker Hub tag picker for the BuildKit image, like the Scanner's. The operator
  pastes a reference.
- Removing old BuildKit images after a switch. Other Builders may still use them until
  their next build.

## Decisions and evidence

Probed on the dev host on 2026-11-03 with Docker 29.8.0, Compose v5.5.1 and
buildx 0.37.0, from the host CLI and from a fresh `docker:29-cli` container.
Throwaway builders named `relist-exp-*` were used and removed afterwards.

The dev panel container ships newer tools: Compose v5.6.1 and buildx 0.38.1. It runs
as root with a writable `HOME=/root`, and it has no `~/.docker` yet. So
`buildx create` can write its state there, but the probes should be repeated with
the panel's own versions during implementation.

- **A Builder is one buildx builder with the `docker-container` driver**: one BuildKit
  container plus one state volume (`buildx_buildkit_<name>0` and
  `buildx_buildkit_<name>0_state`). Limits are set on the container with
  `--driver-opt memory=…,memory-swap=…,cpu-quota=…,cpu-period=…`. Max parallel steps
  goes in a `buildkitd.toml` (`[worker.oci] max-parallelism`) passed with
  `--buildkitd-config`.
- **Compose picks the Builder from `BUILDX_BUILDER=<name>`** in the environment of the
  `docker compose` call. Probed: the image lands in the host's image store, and
  `up --no-build` finds it and starts it. No Compose file or flag change is needed.
- **Limits are enforced.** Probed:
  - Memory: a step that allocated 900 MB under a 512 MB limit failed with
    `cannot allocate memory`. buildkitd survived, and the Builder kept working.
    Docker's `OOMKilled` flag on the Builder container stays `true` afterwards.
  - CPU: a 2-thread loop that took 3 s on Docker default took 12 s under 0.5 CPU,
    which is the expected 6 CPU-seconds at half a core.
  - Max parallel steps = 1: two independent 3 s stages took 7 s, against 4–5 s with
    no limit. This was one run only.
- **Set `memory-swap` equal to `memory`.** When only `memory` is given, Docker sets
  swap to twice the memory. On a host with swap, a build over its limit would then
  thrash instead of failing.
- **Limits apply to the Builder as a whole, not to each step.** Inside a step,
  `/sys/fs/cgroup/cpu.max` reads `max`, and `nproc` reports every host CPU (16 on the
  dev host). Tools that size thread pools from `nproc` (`make -j$(nproc)`, some
  bundlers) still work, but slower. The UI says this next to the CPU field.
- **No per-build `docker update`.** Changing a shared Builder's limits before each
  build would race as soon as two builds run at once. Each Builder is a fixed
  resource pool instead. Concurrent builds on one Builder share its limits. Builds on
  different Builders do not affect each other: probed, a limited and an unlimited
  Builder ran side by side at 11 s and 3 s.
- **The database is the source of truth. Ensure on use.** The panel container's
  `~/.docker` is lost on every panel rebuild. Before each build, the panel runs
  `buildx create` with the stored settings (idempotent by name) and
  `buildx inspect --bootstrap`. Probed from a fresh `docker:29-cli` container: this
  attaches to the existing container, and the cache is still hit (`CACHED`).
- **Re-creating a Builder does not apply new limits.** Probed: `buildx create` with a
  new `memory` against an existing container keeps the old limit and reports nothing.
  So on edit and on ensure:
  - CPU and memory: compare the container's `HostConfig` with the stored settings and
    run `docker update --cpu-quota --cpu-period --memory --memory-swap` when they
    differ. Probed: 512m → 256m took effect at once. This resizes the pool. It is not
    a per-build change, so it does not conflict with concurrency later.
  - Max parallel steps: `buildx rm --keep-state` then `buildx create`. Probed: the
    cache survives. This needs the Builder to be idle (see Editing).
- **Pin the BuildKit image by digest**, the same way the Scanner image is pinned
  (`--driver-opt image=moby/buildkit@sha256:…`). Probed with
  `sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe`
  (BuildKit v0.34.2, the current `buildx-stable-1`). Record the digest again at
  implementation time. This is a third exception to "the panel never pulls an image".
- **The BuildKit image is one panel-wide setting** (decided 2026-11-04), modelled on
  the Scanner image (`internal/vulnscan/settings.go`):
  - Empty means the default pinned in the release, so a panel update moves every
    Builder to the new BuildKit.
  - A custom value must be `repo[:tag]@sha256:<64 hex>`.
  - Saving pulls it first. A failed pull rejects the save and keeps the old value.
- **A Builder switches image when it is next used.** An image cannot be changed with
  `docker update`. So when Ensure finds that the container's image differs from the
  setting (after a setting change or a panel update), it re-creates the Builder with
  `--keep-state`, which keeps the cache (probed).
  - This happens only when no other build is using that Builder. If one is, the
    build runs on the old image and the log says so.
  - Moving to a newer BuildKit keeps the cache. Moving to an older one may not be able
    to read the state. This was not probed. **Prune cache** is the remedy.
- **The Builder container is `privileged` with `restart=unless-stopped`.** These are
  buildx defaults. Privileged is what BuildKit needs to run steps. The panel already
  holds the read-write Docker socket, so this grants nothing new. The restart policy
  means Builders come back after a host reboot by themselves. This goes in the ADR.
- **A Builder cannot see the host's image store.** Probed on 2026-11-04: a Dockerfile
  with `FROM relist-exp-base:local`, an image that exists only on the host, builds on
  Docker default. On a container Builder it fails with
  `pull access denied … failed to resolve source metadata for docker.io/library/…`,
  because BuildKit tries a registry. This breaks:
  - `FROM` an image loaded through the Images page;
  - `FROM` an image that another Service of the same Compose builds. Compose's
    `additional_contexts: { base: "service:<name>" }` is the documented way around
    this. Not probed.

  Such a Compose has to use Docker default. When a build on a Builder fails with
  `pull access denied` or `failed to resolve source metadata` for an image that
  exists in the host's image store, the log adds: "<image> exists on this host, but
  Builders cannot use local images. Pick Docker default for this Compose, or push the
  image to a registry."
- **`build.network` with a custom network fails on both kinds of Builder.** Probed:
  `--network relist-exp-net` fails on Docker default and on a container Builder with
  `network mode "…" not supported by buildkit`. Moving to a Builder takes nothing away
  here.
- **Builder names on the host are `relist-builder-<id>`**, keyed by database ID, so
  renaming a Builder in the UI never orphans its container or cache.
- **The step's network is unchanged in kind.** The Builder container sits on the
  default bridge network (probed). Whether `RUN` steps resolve and reach the same
  hosts as on Docker default is not probed yet. See Open questions.

## Behaviour

### Builders page

- A **BuildKit image** field above the list shows the effective image and a
  **Use default** button. It shows "Default" when the field is empty.
- Fields:
  - Name: unique, 1–40 characters.
  - CPU: cores, 0.1 up to the host's CPU count, one decimal place.
  - Memory: 256 MB up to the host's total memory.
  - Max parallel steps: empty means the BuildKit default, otherwise 1–64.
- The Docker default row says plainly what it does not do. It has no CPU or memory
  limit, and when a build uses up the host's memory, the kernel may kill a running
  app instead of the build. It also says what it can do that Builders cannot: `FROM`
  images that exist only on this host.
- The Builder container is not created when a Builder is saved. It is created on its
  first build, or when the operator presses **Start** on the row. A row shows
  "Not created yet" until then.
- Cache size comes from `docker buildx du --builder <name>` and is shown as a total.
  **Prune cache** runs `docker buildx prune -af --builder <name>`. Both are refused
  while that Builder is running a build.
- **Delete** removes the container and its state volume (`buildx rm` without
  `--keep-state`). It is refused, with the reason shown, when:
  - the Builder is the panel default;
  - any Compose uses it explicitly (the error lists them);
  - a queued or running Deployment snapshot references it.

### Editing a Builder

- Name: applied at once. The host-side name does not change.
- CPU and memory: applied at once with `docker update`, even mid-build. If Docker
  refuses, for example because the new memory is below what the running build is
  using, the edit is rejected with Docker's message and the stored settings are left
  unchanged.
- Max parallel steps: needs a re-create. If the Builder is running a build, the edit
  is refused ("A build is running on this Builder; try again when it finishes").
  Otherwise the panel re-creates it with `--keep-state`.

### Compose setting

- A **Builder** select on the Compose page offers "Use panel default (<name>)",
  Docker default, and every Builder. It is shown only when at least one Service has
  `build:`.
- The choice is part of the configuration a Deployment snapshots when it is queued
  (CONTEXT: "A newly queued Deployment keeps the configuration selected when it was
  queued"). The snapshot records which Builder to use, not its limits. Limits are
  read when the build starts.

### During a Deployment

- Before the first build of the Deployment, the panel ensures the Builder exists,
  runs, and matches the stored limits and the BuildKit image setting. On an image
  mismatch it re-creates the Builder first (see Decisions). It logs the Builder line. A failure here fails
  the step, for example "Could not start Builder large: <docker error>".
- `BUILDX_BUILDER` is set on every `docker compose` call that can build: `build`,
  `up --build`, and `scale --build`. It is never set on calls that cannot build.
  When the choice is Docker default, the variable is not set.
- Out-of-memory detection: when a build on a limited Builder fails, and its output
  contains `cannot allocate memory` or a step exited with code 137, append "The build
  ran out of memory on Builder large (4 GB). Raise its memory or pick a larger
  Builder for this Compose."
  - Do not use the container's `OOMKilled` flag. It stays true until the container
    restarts, so it cannot tell one build from the next.

### Docker page cleanup

- No exclusion code is needed. The panel never stops a Builder (there is no Stop
  action), and `restart=unless-stopped` brings it back after a reboot, so a Builder
  container is never a candidate for the containers prune.
- If a Builder container is removed anyway, the next ensure-on-use re-creates it,
  and its cache survives. Probed: after `docker rm -f` on the container, the
  re-created Builder still hit the cache. The cache is lost only if a volumes prune
  ran while the container was gone. That costs one cold build.
- A running Builder's `_state` volume is in use, so the volumes prune never selects
  it.
- The Docker page's **Prune build cache** keeps covering Docker default only. Its copy
  says so and links to the Builders page.

### Migration

- Builder definitions and each Compose's choice travel with the database.
- Builder containers and caches do not. They are created on first use on the new
  host. The first build on each Builder there is a cold build.
- Warning about Builders whose limits exceed the new host's capacity is out of
  scope. What Docker does with such a limit was not probed: it may reject it, or
  accept it and not constrain anything.

## Domain changes

`CONTEXT.md`:

- **Image**: add the BuildKit image to the exceptions in "never pulls one itself".
- New **Builder**:
  - A named, resource-limited place where the panel builds Images: one BuildKit
    container and its cache, with fixed CPU, memory and max parallel steps.
  - **Docker default** is the built-in, unlimited Builder (dockerd's own).
  - Each Compose uses the panel default Builder unless it picks one.
  - _Avoid_: Runner, Worker, Build agent.
- **Deployment**: mention that each build step runs on the Compose's Builder.

New ADR (next number after 0027): "Builds run on panel-managed buildx Builders".
It records:

- the `docker-container` driver;
- the database as the source of truth, with ensure-on-use;
- why there is no per-build `docker update`;
- the pinned BuildKit image as a pull exception;
- that the container is privileged, and why that adds no risk beyond the socket.

## Probes inside the panel image (2026-11-04)

These ran inside the dev panel image `relist-panel-panel` (Compose v5.6.1,
buildx 0.38.1, root, `HOME=/root`) with the Docker socket mounted. Each was run on
Docker default and on a container Builder pinned to the BuildKit digest, with
`memory=2g` and `max-parallelism = 2`. Builder state lands in
`/root/.docker/buildx/instances/<name>`.

| Probe | Docker default | Container Builder |
|---|---|---|
| DNS and HTTPS from `RUN` | OK | OK |
| `build.extra_hosts` | OK | OK |
| `build.secrets` (file secret) | OK | OK |
| `build.ssh: [default=<key file>]` | OK | OK |
| `additional_contexts: { base: "service:base" }` | OK | OK, so the sibling-Service case has a workaround |
| `build.network: host` | fails: needs `--allow=network.host` | the same failure |
| `FROM` an image that exists only on the host | OK | fails (see Decisions) |
| node:24 + new 400 MB layer, image 2.47 GB | 20 s, then 16 s | 54 s cold (it pulls node:24 itself), then 27 s and 15 s |

- A warm build of a large image runs at about the same speed. The first build on a
  Builder is slower, because base images come from the registry, not the host store.
- The Builder's cache held 4.17 GB after these builds. The image itself was 2.47 GB in
  the host store. This is the duplicated disk use. **Prune cache** is the remedy.
- `buildx create` without `image=` pulled the unpinned `moby/buildkit:buildx-stable-1`.
  Ensure-on-use must always pass the pinned `image=`.

## Settled questions

- Builders are identified by the `relist-builder-<id>` name. Containers get no
  labels: buildx sets none, and `--driver-opt` has no label option.
- Cache size: `buildx du` runs per Builder. The Builders page fetches sizes after the
  list renders, so a slow Builder never blocks the page.
- No `gckeepstorage` in v1. BuildKit's own default GC plus **Prune cache** is enough
  until real usage says otherwise.
