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
  runs and matches the stored limits. It logs the Builder line. A failure here fails
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

- Builder containers and their `_state` volumes must not be removed by the Docker
  page's cleanup actions. The Docker page's **Prune build cache** keeps covering
  Docker default only, and its copy says so and links to the Builders page.

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

## Open questions (verify at implementation)

1. Do `RUN` steps on a `docker-container` Builder resolve DNS and reach the network
   the same way as on Docker default (corporate DNS, `extra_hosts`,
   `network: host` in `build:`)? `network: host` needs the
   `network.host` entitlement on a container Builder.
2. Do `build.secrets`, `ssh`, and `additional_contexts` written by the user in a
   Compose file behave the same on a container Builder? The panel itself injects none
   of them: `.ssh` in the snapshot is used only by the Git fetch.
3. Does the Docker page's container or volume prune touch a stopped Builder container
   or its `_state` volume today? If so, exclude them by name prefix or label.
4. Can the Builder container get a label (`--driver-opt` has none), or must the panel
   identify Builders by the `buildx_buildkit_relist-builder-` name prefix?
5. Is a disk-use readout per Builder fast enough to show on page load? If not, load
   it after the page.
6. Are large builds noticeably slower? All probes used tiny contexts and images. On a
   container Builder, the context streams in over `docker exec`, and the image comes
   back as an OCI tar to load. Measure with a real Source and an image of 1 GB or more.
7. Each Builder runs its own BuildKit garbage collection, so N Builders hold N caches.
   Should the panel set `gckeepstorage` in `buildkitd.toml`, as a fixed value or a
   per-Builder field?
