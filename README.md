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
