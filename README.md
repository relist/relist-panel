# Relist Panel

Relist Panel is a small self-hosted UI for managing Docker Compose deployments.
Ticket 01 provides the walking skeleton: a Go HTTP server, embedded Vite/React
shell, SQLite database, and goose migrations.

## Development

Install [go-task](https://taskfile.dev) and the frontend dependencies, then
build the embedded frontend and Go binary:

```sh
pnpm --dir web install
task build
```

Run the binary with a writable data directory during local development:

```sh
RELIST_DATA_DIR="$PWD/.data" ./relist-panel
```

The server listens on `:8080` by default. Set `RELIST_PORT` to use another
port. `RELIST_DATA_DIR` defaults to `/var/lib/relist-panel`; SQLite is stored
at `<data dir>/panel.db`.

## Self-hosted install

Ticket 02 adds the install bootstrap: a `docker-compose.yml` that brings up
the panel, Traefik, and the shared `relist-network` network together.

`relist-network` is declared `external: true` — it must outlive this install
stack's own lifecycle, since every Compose deployed through the panel later
attaches to it too, so `docker compose down` on this stack must never be able
to delete it out from under a running deployment (ADR-0002). Create it once,
then bring up the stack:

```sh
docker network create relist-network   # one-time, safe to re-run
docker compose up -d
```

This builds the panel image (Go binary + embedded frontend, per the
`Dockerfile`) and starts it alongside Traefik, both attached to
`relist-network`. The panel is reachable two ways out of the box:

- Directly, via its published port: `http://<host>:8888/`
- Through Traefik, via `Host` routing: `http://panel.localhost/` (resolves to
  loopback on most systems without any DNS setup) — override the hostname
  with `RELIST_DOMAIN` for a real domain

Override the defaults via environment variables (or a `.env` file next to
`docker-compose.yml`):

- `RELIST_PORT` (default `8080`) — the panel's port inside the container; when
  set it also becomes the published host port, which otherwise defaults to
  `8888`
- `RELIST_DATA_DIR` (default `/var/lib/relist-panel`) — bind-mounted at the
  **same path** on the host and inside the panel container, per ADR-0002
- `RELIST_DOMAIN` (default `panel.localhost`) — the Host rule Traefik routes
  to the panel on
- `TRAEFIK_HTTP_PORT` (default `80`) — the host port Traefik's HTTP
  entrypoint is published on

Traefik ships with a minimal default (`--providers.docker`, HTTP entrypoint
only, `exposedbydefault=false`); TLS/ACME and additional entrypoints are left
to the operator to configure — routing for anything deployed through the
panel is controlled entirely by Traefik labels written directly in that
Compose's YAML, per the v1 scope.
