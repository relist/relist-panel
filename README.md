# Relist Panel

Relist Panel is a small self-hosted UI for managing Docker Compose deployments.
Ticket 01 provides the walking skeleton: a Go HTTP server, embedded Vite/React
shell, SQLite database, and goose migrations.

## Development

Install the frontend dependencies, then build the embedded frontend and Go
binary:

```sh
pnpm --dir web install
make build
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
the panel, Traefik, and the shared `relist-public` network together.

```sh
docker compose up -d
```

This builds the panel image (Go binary + embedded frontend, per the
`Dockerfile`), starts it alongside Traefik, and attaches both to the
`relist-public` network — the same network the panel later joins every
deployed Compose to, so Traefik can route to it without extra setup
(ADR-0002). The panel also ensures this network exists on every startup, so
it stays correct even if it's ever removed outside of Compose.

Override the defaults via environment variables (or a `.env` file next to
`docker-compose.yml`):

- `RELIST_PORT` (default `8080`) — the panel's port, on both the host and the
  container
- `RELIST_DATA_DIR` (default `/var/lib/relist-panel`) — bind-mounted at the
  **same path** on the host and inside the panel container, per ADR-0002
- `TRAEFIK_HTTP_PORT` (default `80`) — the host port Traefik's HTTP
  entrypoint is published on

Traefik ships with a minimal default (`--providers.docker`, HTTP entrypoint
only, `exposedbydefault=false`); TLS/ACME and additional entrypoints are left
to the operator to configure — routing is controlled entirely by Traefik
labels written directly in each deployed Compose's YAML, per the v1 scope.
