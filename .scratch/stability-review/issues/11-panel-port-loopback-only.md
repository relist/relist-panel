# 11 — Publish the panel's port on loopback only

**What to build:** `docker-compose.yml` publishes `${RELIST_PORT:-8888}:8080` on every interface,
which bypasses Traefik and TLS (plain-text sign-in, cookie without `Secure`). Decision (2026-10-24):
access goes through Traefik; keep a loopback port as the emergency way in (SSH tunnel) for when
Traefik or a Domain breaks routing.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] Port mapping becomes `127.0.0.1:${RELIST_PORT:-8888}:${RELIST_PORT:-8080}`
- [ ] README: how to reach the panel through an SSH tunnel; existing installs must recreate the
      panel container
- [ ] Nice-to-have: `ReadHeaderTimeout: 10s` and `IdleTimeout: 2m` on the panel's `http.Server`
      (no overall read/write timeout — image uploads and the terminal websocket are long-lived)

## Source

Stability review, item A3; discussion on 2026-10-24.
