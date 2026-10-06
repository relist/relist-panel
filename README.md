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
- `RELIST_IMAGE` (unset by default) — the panel image that Volume file
  operations run from; set it only when the panel cannot name its own image
  (`docker inspect $(hostname)` fails, e.g. a custom `hostname:` on the panel
  service) by adding it to that service's `environment:`. The image must be on
  the host; `docker compose build` puts it there

Traefik ships with a minimal default (`--providers.docker`, HTTP entrypoint
only, `exposedbydefault=false`); TLS/ACME and additional entrypoints are left
to the operator to configure — routing for anything deployed through the
panel is controlled entirely by Traefik labels written directly in that
Compose's YAML, per the v1 scope.

Image uploads through the domain must finish within one hour: Traefik's read
timeout on the `web` entrypoint is set to `3600s`, and 10 GiB needs roughly
25 Mbit/s sustained. A slower link can upload through the panel's direct port
(`RELIST_PORT`, default host port `8888`), which bypasses Traefik.

## Volume files

Each Compose page has a **Volumes** tab listing the Volumes that Compose
mounts, with a **Volume Explorer** behind each one for browsing, editing,
uploading, deleting, and downloading or restoring a tar.gz.

Deploy runs `docker compose up -d --build --remove-orphans`, so the containers of
Services you removed from the YAML are removed with the next deploy. Volumes are
left alone. The Compose page also offers **Deploy with fresh volumes**, which
removes the Compose's named volumes (`down -v`) before redeploying, and the
Volumes tab lists **orphan volumes** — named volumes Compose created for this
Compose's project that the deployed file no longer declares — for you to delete
one by one. Docker refuses to delete a volume a container still uses, and the
panel never forces it. Bind mounts and `external: true` volumes are never
deleted by either action.

Two limits to know before using either. Fresh volumes runs `down -v`, which can
also remove volumes used only by Services behind a Compose profile, even though
the dialog's list (resolved without profiles) may not show them; and a volume
declared with an explicit `name:` is removed even if another Compose uses it.
The panel does not detect another Compose that references one of this
Compose's volumes by its full name: deleting that volume as an orphan, or
removing it with fresh volumes, takes that Compose's data too.

Volume files require a **containerized panel**: the helper runs from the
panel's own image, which must be on the host, over the Docker socket this
install mounts. Running the bare binary hides the Volumes tab.

The feature assumes **TLS** on the panel's domain — a volume file manager over
plain HTTP is a data-exfiltration channel. The install stack's Traefik is
HTTP-only by default (above), so terminate TLS before exposing the panel
beyond localhost.

New Composes use the immutable Docker project name `relist-c<ID>` and keep their
deployment files under `RELIST_DATA_DIR/.composes/<ID>/`. Upgrading preserves
existing names and directories; conflicting legacy identities refuse operations
until the operator reconciles the shared stack (ADR-0011). Parent deletion tears
down its child stacks before removing their records.

Before a Volume operation, select its mounting Service or a numeric uid:gid
override. UID 0 and unverifiable bind paths require separate acknowledgements,
including downloads started directly from the Volumes tab.
