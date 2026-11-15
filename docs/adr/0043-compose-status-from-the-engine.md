# Compose status comes from the Engine API, not `docker compose ps`

Loading the panel killed the panel container. The web store loads the whole Project tree and asks `GET …/environments/{id}/composes` for every Environment at once. Each request ran `docker compose ps --all --format json` once per Compose, so with about 36 Composes about 20 `docker` + `docker-compose` process pairs ran at the same time, about 6 MB of anonymous memory each. The kernel killed the panel at its 256 MiB `mem_limit` (`Memory cgroup out of memory: Killed process … (relist-panel)`), the panel restarted in the middle of a request, and Traefik answered 502. More tabs or users made it worse.

**Decision:**

- Compose status (running and total Containers, Service names) is read through the Engine API. No code path starts `docker compose ps` any more: `Runner.Status` and `parseStatus` are gone.
- A list of Composes reads one snapshot of every Compose Container (`GET /containers/json?all=1`, filtered to `com.docker.compose.project` with `com.docker.compose.oneoff=False`). Containers are grouped by the exact project label, never by prefix, so `relist-c1` and `relist-c19` stay apart. One-off Containers (`compose run`) are left out, as `ps --all` left them out.
- The snapshot is shared: list requests that arrive while it is being read wait for the same call, and its result is reused for 2 s. It is a mutex and a done channel, with no new dependency. The shared call runs on a context that outlives the request that started it (`context.WithoutCancel`, 10 s timeout), and each waiting caller still returns when its own context ends. An error is never kept, so the next request tries again. There is no background loop: nothing runs while nobody is looking.
- A deploy, stop, restart, scale or removal drops the snapshot, so the list shows the outcome at once, not up to 2 s later.
- A single Compose (`GET` one Compose, the answer to a stop or restart, the terminal's Service names, the migration's running check) always reads fresh with `ProjectContainers`, and counts only the Services in `docker compose config`. See the orphan result below.
- An Engine error, or no Engine, is a zero status: the request still answers 200. A Compose with no deployed file on disk is a zero status without any Docker call.

## Considered options

**A semaphore around the CLI calls.** Rejected: it still forks two processes per Compose per request, and it serialises page loads behind the slowest `compose ps`.

**A background loop running `docker compose ps` into a central cache.** Rejected: it forks two processes per Compose every round, rounds get longer as Composes are added, it keeps working while nobody looks, and it is stale after a deploy. The shared 2 s Engine snapshot gives the same single source without any of that.

## Measured (e2e sandbox, 2026-11-16)

- **Orphan parity.** A Container labelled with the project but a Service the file does not have (an orphan, started with `docker run --label …`), plus a one-off Container of a real Service. `docker compose ps --all --format json` listed 1 Container, 1 running: it leaves out both. The Engine list counted the orphan, so a single Compose read 2 running of 3 total, which differs. Hence the `docker compose config` filter on the single-Compose paths; after it, the single Compose read 1 of 1, the same as the CLI. The one-off Container is left out in both. The Compose list still counts an orphan, because filtering it would cost one CLI call per Compose, which is the cost this change removes. The panel's own deploy removes orphans, so one only appears when something outside the panel creates it.
- **Fan-out.** 30 deployed Composes over 10 Environments (3 of them with `network_mode: none`, because the Docker daemon in the sandbox ran out of address pools at 27 networks). With `ps -o args` inside `relist-panel` sampled every 0.3 s for 20 s (56 samples) while five rounds of the store's list requests ran (every Environment at once, signed in), no sample contained `docker` or `compose`: only `relist-panel` and the sampler itself. `memory.peak` of the panel container after a restart and those rounds was 25.9 MB (about 24.7 MiB), against the 256 MiB limit and the 100 MiB goal. It had been 38.2 MB over the 30 deploys and an earlier reload. The panel's `RestartCount` stayed 0.
- **Full e2e suite.** `task e2e`: 10 passed. `RestartCount` was 0 before and after.

## Consequences

- Listing Composes costs one Engine call per 2 s however many Composes, Environments or tabs there are.
- A list can be up to 2 s behind a change made outside the panel, such as `docker stop` by hand. Actions through the panel drop the snapshot.
- The list and a single Compose can disagree on a Compose with an orphan Container (see above).
- A Compose's Service names are sorted, where `ps` gave first-seen order. Nothing depends on the order.
