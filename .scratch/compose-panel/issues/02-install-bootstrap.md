# 02 — Install bootstrap: self-host via docker compose

**What to build:** A shipped `docker-compose.yml` that installs the whole system — panel plus Traefik plus the shared network they both need — with one `docker compose up`, so the panel is reachable through Traefik on a real Docker host.

**Blocked by:** 01

**Status:** closed

- [x] A `docker-compose.yml` at the repo root brings up the panel container and a Traefik container together
- [x] The panel container mounts `/var/run/docker.sock` and the data directory at an identical path on host and container (default `/var/lib/relist-panel`, per ADR-0002)
- [x] Panel startup ensures the shared external network (e.g. `relist-public`) exists, creating it if missing, idempotently
- [x] Traefik and the panel container are both attached to that shared network
- [x] `docker network create relist-public` (one-time, idempotent) followed by `docker compose up` from a clean host results in the panel's HTTP server being reachable — see Comments for why the network needs that one preceding command

## Comments

Implemented via a multi-stage `Dockerfile` (frontend build → Go build → `docker:27-cli` runtime, which bundles the `docker compose` CLI plugin per ADR-0002) and a root `docker-compose.yml` (panel + `traefik:v3.7`, both on the `relist-public` network, plus Traefik labels on the panel service so it's genuinely reachable *through* Traefik via `Host(RELIST_DOMAIN)`, not just its own direct port publish). `internal/dockernet.EnsureNetwork` (TDD'd against a real Docker daemon, idempotent) lives in the `dockernet` package (moved there from `config` — it's a Docker-networking concern, not an env-derived setting) and is called from `main.go` on every startup as a best-effort, non-fatal self-heal.

**Amendment during `/code-review`:** ADR-0002 calls `relist-public` a shared *external* network — deliberately, since every Compose deployed later (ticket 08+) also attaches to it, so `docker compose down` on this install stack must never be able to delete it out from under a running deployment. I verified empirically that Docker Compose validates all `external: true` networks *before* starting any service in the file — there is no way to have a bootstrap container inside the same compose file create it first. So a genuinely external network and a zero-step `docker compose up` are mutually exclusive; I surfaced this to the user and they chose external + a one-time `docker network create relist-public` pre-step (standard practice — Traefik's own docs recommend the same pattern for shared proxy networks), over keeping the network compose-owned. Verified both properties hold: a bare `docker compose up` fails without the network pre-existing (as expected), and `docker compose down` afterward does *not* remove it.

Verified end to end against a real Docker daemon in an isolated test project (`docker compose -p relist-panel-ticket02-test`, non-default ports, scratch data dir): image built clean, `/healthz` reachable both directly on the host port and through Traefik via `Host: panel.localhost` (with a correct 404 for a non-matching host, since `exposedbydefault=false`), and the bind-mounted data dir resolves to the identical path/file on host and inside the container. Fully torn down afterward (containers, image, network, scratch dir) — no residue left on the host.

Note: `traefik:v3.1` (the tag first tried) failed to talk to this host's very new Docker Engine (continuous "Failed to retrieve information of the docker client" errors from its docker provider); `traefik:v3.7` (current stable) talks to the same daemon with no errors. Pinned to `v3.7`.
