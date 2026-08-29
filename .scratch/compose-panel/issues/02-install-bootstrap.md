# 02 — Install bootstrap: self-host via docker compose

**What to build:** A shipped `docker-compose.yml` that installs the whole system — panel plus Traefik plus the shared network they both need — with one `docker compose up`, so the panel is reachable through Traefik on a real Docker host.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] A `docker-compose.yml` at the repo root brings up the panel container and a Traefik container together
- [ ] The panel container mounts `/var/run/docker.sock` and the data directory at an identical path on host and container (default `/var/lib/relist-panel`, per ADR-0002)
- [ ] Panel startup ensures the shared external network (e.g. `relist-public`) exists, creating it if missing, idempotently
- [ ] Traefik and the panel container are both attached to that shared network
- [ ] Running `docker compose up` from a clean host results in the panel's HTTP server being reachable
