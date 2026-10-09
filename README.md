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
- Through Traefik, via `Host` routing: `https://panel.localhost/` (resolves to
  loopback on most systems without any DNS setup; plain HTTP redirects to it) —
  override the hostname with `RELIST_DOMAIN` for a real domain

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
- `TRAEFIK_HTTPS_PORT` (default `443`) — the host port Traefik's HTTPS
  entrypoint is published on
- `RELIST_ACME_EMAIL` (unset by default; optional but recommended) — the
  contact address for your Let's Encrypt account. Traefik's reference marks it
  as required, but an empty value registers an account anyway (ADR-0013). Set
  it so Let's Encrypt can reach you about expiring certificates and so you can
  recover the account, and in case a later Traefik enforces it
- `RELIST_IMAGE` (unset by default) — the panel image that Volume file
  operations run from; set it only when the panel cannot name its own image
  (`docker inspect $(hostname)` fails, e.g. a custom `hostname:` on the panel
  service) by adding it to that service's `environment:`. The image must be on
  the host; `docker compose build` puts it there

Traefik ships with two entrypoints, `web` (port 80) and `websecure` (port 443),
and a Let's Encrypt certificate resolver named `letsencrypt` that answers the
HTTP-01 challenge on `web`. HTTPS is always on: the panel's own router uses
`websecure` with `letsencrypt`, and plain HTTP for the panel redirects to it.
Certificates are stored in `RELIST_DATA_DIR/traefik/acme.json`, so the same
backup covers them. Other settings stay minimal (`--providers.docker`,
`exposedbydefault=false`). To route a Compose's Services, add a **Domain** on
its Domains tab; the panel injects the Traefik labels at the next deploy, and
labels you write by hand in the YAML keep working. Let's Encrypt can only issue
when the host's DNS points at this machine and port 80 is reachable from the
internet. When it cannot, Traefik serves its default self-signed certificate and
logs the ACME error; the panel cannot see this.

Two limits on Domains: a hand-written router in another Compose, or one for
`RELIST_DOMAIN`, that uses the same host as a Domain is not detected, and
Traefik picks one of the two routers by priority. A custom resolver name that
Traefik does not know shows only in Traefik's log.

Image uploads through the domain must finish within one hour: Traefik's read
timeout on the `web` and `websecure` entrypoints is set to `3600s` (this applies
to every app on those entrypoints, not only the panel), and 10 GiB needs roughly
25 Mbit/s sustained. A slower link can upload through the panel's direct port
(`RELIST_PORT`, default host port `8888`), which bypasses Traefik.

### Local development

The bare binary on `:8080` and the direct port `:8888` bypass Traefik and do not
change. `http://panel.localhost` now redirects to `https://panel.localhost`,
which shows a self-signed certificate warning, because Let's Encrypt does not
issue for `.localhost`. Traefik logs an ACME error for it on each retry; that is
log noise only. Port 443 must be free on the host, or set `TRAEFIK_HTTPS_PORT`
to another port. Domains that stay on HTTP, such as `myapp.localhost`, behave as
before.

### Upgrading an existing install

A new panel with an old install `docker-compose.yml` has no `websecure`
entrypoint and no `letsencrypt` resolver, so every HTTPS domain fails and only
Traefik's log says why. To upgrade:

1. Update `docker-compose.yml` from this version.
2. Set `RELIST_ACME_EMAIL` in your `.env`.
3. Open port 443 on the host's firewall (or the port you set as
   `TRAEFIK_HTTPS_PORT`).
4. Run `docker compose up -d`.

Composes with hand-written Traefik labels keep routing as before.

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
plain HTTP is a data-exfiltration channel. The install stack's Traefik serves the
panel over HTTPS with a Let's Encrypt certificate (above), which needs a public
`RELIST_DOMAIN` pointing at the host; on `panel.localhost` the certificate is
self-signed, so do not expose the panel beyond localhost that way.

New Composes use the immutable Docker project name `relist-c<ID>` and keep their
deployment files under `RELIST_DATA_DIR/.composes/<ID>/`. Upgrading preserves
existing names and directories; conflicting legacy identities refuse operations
until the operator reconciles the shared stack (ADR-0011). Parent deletion tears
down its child stacks before removing their records.

Before a Volume operation, select its mounting Service or a numeric uid:gid
override. UID 0 and unverifiable bind paths require separate acknowledgements,
including downloads started directly from the Volumes tab.
