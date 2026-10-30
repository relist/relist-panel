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

- On the host itself, via its published port, which listens on loopback only:
  `http://127.0.0.1:8888/`. From another machine, open an SSH tunnel with
  `ssh -L 8888:127.0.0.1:8888 <host>` and browse `http://127.0.0.1:8888/`.
  This is the way in when Traefik or a Domain breaks routing.
- Through Traefik, via `Host` routing: `https://panel.localhost/` (resolves to
  loopback on most systems without any DNS setup; plain HTTP redirects to it) —
  override the hostname with `RELIST_DOMAIN` for a real domain

Then create the admin account from a terminal on the host. The panel has no
setup screen: until an admin exists, its sign-in page refuses everyone, and its
log says to run this.

```sh
docker compose exec panel relist-panel create-admin
```

It asks for the email and the password; leave the password empty to have one
generated and printed once. For scripts, pass the password on stdin or have it
generated:

```sh
printf '%s\n' "$PASSWORD" | docker compose exec -T panel relist-panel create-admin --email admin@example.com --password-stdin
docker compose exec -T panel relist-panel create-admin --email admin@example.com --generate
```

The command refuses when an admin already exists.

If you lose the password, reset it from the same terminal. It takes the same
options as `create-admin` (`--email` is only needed with more than one admin),
keeps two-factor on, clears any sign-in lock and signs every session out:

```sh
docker compose exec panel relist-panel reset-password
docker compose exec -T panel relist-panel reset-password --generate
```

Override the defaults via environment variables (or a `.env` file next to
`docker-compose.yml`):

- `RELIST_PORT` (default `8080`) — the panel's port inside the container; when
  set it also becomes the published host port, which otherwise defaults to
  `8888`. The host port is bound to `127.0.0.1` only
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
Certificates are stored in `RELIST_DATA_DIR/.traefik/acme.json`, so the same
backup covers them. Other settings stay minimal (`--providers.docker`,
`exposedbydefault=false`). To route a Compose's Services, add a **Domain** on
its Domains tab; the panel injects the Traefik labels at the next deploy, and
labels you write by hand in the YAML keep working. Let's Encrypt can only issue
when the host's DNS points at this machine and port 80 is reachable from the
internet. When it cannot, Traefik serves its default self-signed certificate and
logs the ACME error; the panel cannot see this.

The panel refuses a Domain whose host is `RELIST_DOMAIN`, and its own routers
carry the top Traefik priority, so no Domain can take the panel's traffic. A
Domain that already uses that host is left as it is: change its host in the
Domains tab. One limit on Domains: a hand-written router in another Compose that
uses the same host as a Domain is not detected, and Traefik picks one of the
two routers by priority. A custom resolver name that
Traefik does not know shows only in Traefik's log.

### Certificates

To serve a certificate you already have (bought, or a wildcard issued
elsewhere), upload it on the **Certificates** page: a name, the certificate
with its chain (server certificate first), and an unencrypted private key, both
as PEM. The panel writes each one into Traefik's file-provider directory,
`RELIST_DATA_DIR/.traefik/dynamic/`, and Traefik picks it up at once, with no
redeploy. Traefik serves it to any HTTPS Domain set to Certificate Provider
**None** whose host it covers, chosen by SNI, wildcards included; the panel does
not link a certificate to a Domain and does not check that one covers a host. A
Domain with no covering certificate gets Traefik's self-signed default. New
HTTPS Domains start on **None**. To renew, use **Replace** on the same
certificate rather than adding a second one: when two certificates cover a host,
Traefik picks either. Check what a host serves with
`openssl s_client -servername <host> -connect <host>:443`.

Image uploads through the domain must finish within one hour: Traefik's read
timeout on the `web` and `websecure` entrypoints is set to `3600s` (this applies
to every app on those entrypoints, not only the panel), and 10 GiB needs roughly
25 Mbit/s sustained. A slower link can upload through the panel's loopback port
(`RELIST_PORT`, default host port `8888`) over an SSH tunnel, which bypasses
Traefik.

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

Every upgrade that changes the panel's Traefik labels, such as the router
priority added to keep a Domain from taking the panel's host, needs the panel
container recreated: run `docker compose up -d` so it picks the labels up.

The panel's published port now listens on `127.0.0.1` only; before, it listened
on every interface and served the panel over plain HTTP, bypassing Traefik's
TLS. Recreate the panel container so the new binding applies (`docker compose
up -d` does it once `docker-compose.yml` is updated). Anything that reached
`http://<host>:8888/` from another machine must use the panel's domain or an SSH
tunnel instead.

Uploaded certificates need Traefik's file provider
(`--providers.file.directory=/letsencrypt/dynamic`). With an older install
`docker-compose.yml`, Traefik ignores every uploaded certificate and nothing
says so: update the file and run `docker compose up -d` so Traefik is recreated.

Zero-downtime deploy needs the same file provider: each switched-on Service's
Domains use a Traefik transport the panel writes there. With an install
`docker-compose.yml` older than uploaded certificates, those Domains return 404
after the next deploy. Update the file and recreate Traefik before turning the
switch on.

`RELIST_ADMIN_EMAIL` and `RELIST_ADMIN_PASSWORD` are no longer read; the
admin they created stays. Remove them from `.env`, and if **Account** still
says the password is the one from the env, change it there.

### Networks and internal hostnames

Only Services that need Traefik join `relist-network`: a Service with a Domain,
with hand-written `traefik.*` labels, or with `relist-network` listed in its own
`networks:`. Every other Service stays on its Compose's private `default`
network, so two Composes can both have a `db` without reaching each other's.

Each Service on `relist-network` also answers to two names from other Composes:

- `<service>.<docker compose project>.internal`, e.g. `web.relist-c12.internal`,
  which never changes — use this one in config;
- `<service>.<compose>.<environment>.<project>.internal`, e.g.
  `web.api.production.shop.internal`, built from the current names, so a rename
  changes it on the next deploy.

The Compose page's Preview shows each Service's exact aliases. Call Services in
other Composes by alias, never by bare name: once two Composes both put a `db`
on `relist-network`, a lookup of `db` can land on the other one.

**Upgrading from an earlier version:** on its next deploy, a Service with no
Domain, no Traefik labels and no `relist-network` in its YAML leaves the shared
network. If another Compose calls it, add `relist-network` to that Service's
`networks:` and switch the caller to its alias.

## Moving to a new server

A **Migration** moves everything this panel manages to an empty panel on
another host (ADR-0018): the database (admin account, Two-factor, Projects,
Environments, Composes, Variables, Domains, SSH keys and Deployment
history), the whole data directory with Traefik's certificates, every named
Volume, the host paths you pick and every Image the Composes need. The new
panel pulls it over HTTPS, item by item; no SSH and no archive file are
involved.

Drive it from a terminal on each host (ADR-0019), in the install directory:

```bash
# on the old server
docker compose exec panel relist-panel migration serve
# on the new server
docker compose exec panel relist-panel migration pull
```

This is the only way to migrate: the web has no route that creates a token
or starts a pull, so a stolen web session cannot copy the server. The
terminal follows every step to the end, including the panel restarting to
swap in the migrated database. Quitting either one does not stop the
migration: run the command again to see where it is.
`relist-panel migration status` prints the same without a terminal UI
(`--watch` to follow it), and `relist-panel migration resume` is the old
server's Resume.

1. On the new host, install the panel with the **same `RELIST_DATA_DIR`** as
   the old one. Containers mount the data directory by absolute path, so the
   migration refuses a different one. Copy the old install's `.env` too:
   `RELIST_DOMAIN`, `RELIST_ACME_EMAIL` and the Traefik ports live there, not
   in the data directory.
2. On the old host, run `migration serve` and create a migration token
   (1 hour, 24 hours or 7 days). It is shown once.
3. On the new host, run `migration pull`. Enter the old panel's `https://`
   URL, the token and the new panel's own URL, which the old panel shows as
   where it moved. An admin made with `create-admin` on the new panel is
   fine; the old host's admin replaces it.
4. Check the preview: sizes, which Images are pulled and which are copied,
   the host paths to include (system paths such as `/etc/localtime` and the
   Docker socket are unticked) and every warning. Start stays disabled while a
   check blocks, such as a full disk or a Volume that already exists.
5. **Start migration.** The old panel locks: queued Deployments are canceled,
   a running one finishes, every Compose stops, and its panel-managed Domains
   show a maintenance page. Apps are down from here until DNS moves.
6. The new panel copies each item, retrying a failed one on its own, then
   re-executes itself in place (the container keeps running), starts Traefik with the old certificates and deploys the
   Composes that were running. Sign in with the old admin account.
7. Point DNS at the new host, then turn the old one off.

The old panel stays locked as **Migrated** and refuses every change, including
after a restart; its web page only shows where it went. `relist-panel
migration resume` on it (type `RESUME`) starts its Composes again, which is
also how to cancel a migration or fall back to the old host.
Data written on the new host after the migration is not on the old one.

Not moved: the install's `.env`, Volumes with a driver other than `local` or
with driver options, and host paths you leave unticked. Domains routed by
hand-written Traefik labels get no maintenance page; they answer with
Traefik's error until DNS moves.

Traefik's certificates move with `acme.json`. If `RELIST_ACME_EMAIL` is empty, the
probe saw Traefik on the new host register a new Let's Encrypt account even though the
copied file arrived intact; the certificates still came across. Set the email so the
account stays the same.

`task probe:migration` runs a whole migration between two `docker:dind` hosts
with real Docker. It needs privileged containers and about 12 GB of disk.

## Volume files

Each Compose page has a **Volumes** tab listing the Volumes that Compose
mounts, with a **Volume Explorer** behind each one for browsing, editing,
uploading, deleting, and downloading or restoring a tar.gz.

Deploy runs `docker compose up -d --remove-orphans`, with `--build` for a Compose
without a Source and `--no-build` for one with a Source, whose images are built
first (see "Building from a Source"). Either way, the containers of Services you
removed from the YAML are removed with the next deploy. Volumes are
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

## Building from a Source

A Compose can build its own images from code in a Git repository or from an
uploaded zip. The code lives in the Compose's `src/` directory, and the YAML
refers to it with Compose's standard `build:` key. Paths are relative to the
Compose directory, so `./src/api` is the `api` folder of the Source:

```yaml
services:
  api:
    build:
      context: ./src/api
      dockerfile: Dockerfile
      target: production   # optional build stage
```

The panel never injects `build:` or `image:` into the YAML, so the file still
runs outside the panel. A wrong path fails the build with Docker's own error in
the Deployment log.

A hand-written `env_file: .env` on a Compose that has never been deployed fails
its first build, because `.env` does not exist yet (the panel writes `.env`
itself on deploy). Leave `env_file` out and use Variables instead.

### Git over SSH

A Git Source chooses one SSH key from the host-wide **SSH keys** library. Open
**SSH keys** in the sidebar, choose **Generate key** and give it a name; the
panel creates an ed25519 pair and shows the public half. One key can serve
several Composes. The Source card, on the Editor tab below the
Environment card, is titled `owner/repo · branch`.

1. Enter the repository's SSH URL: `git@host:owner/repo.git` or
   `ssh://git@host[:port]/owner/repo.git`.
2. Choose the SSH key in the Source modal and add its public key to the
   repository as a read-only deploy key (Repository, Settings, Deploy keys).
   Git hosts refuse one deploy key on two repositories, so to reuse a key
   across repositories use a machine-user key on the Git host. The panel does
   not check this.
3. Test the connection, pick a branch, then Detect the Dockerfiles and Insert
   the matching `build:` entries into the editor. Save, then Build & Deploy.

The first connection trusts the host key it sees and stores it for that
Compose. If the host key later changes, the fetch fails; **Reset host key**
clears the stored key so the next fetch trusts the new one.

A Source with no SSH key cannot build; the card shows a warning until you
choose one. Deleting a key detaches it from every Source that uses it, but
Deployments already queued keep the key they were queued with. To rotate a
key, generate a new one and switch the Sources to it.

### Zip uploads

Upload a zip instead of configuring Git when you do not want an SSH key. A
GitHub "Download ZIP" works as is: when every entry sits under one top-level
directory, that directory is stripped. An archive may hold up to 500 MiB
compressed, 10,000 entries and 4 GiB uncompressed. Symlinks are kept only when
they are relative and stay inside `src/`.

An upload replaces the code and never merges with it. A failed upload leaves
the previous code in place. An upload does not deploy, so the card shows
"Not built yet" until you Build & Deploy.

### Deployments

Build & Deploy queues a Deployment and returns at once. The page follows its
log until the Deployment ends. A Deployment fetches the Source (Git only),
builds each Service that has `build:` one at a time in YAML order, then takes
the Project's lock and runs `docker compose up`. A failed build stops the
Deployment with status `failed` at `build:<service>`, and the containers that
were running stay up. Each Deployment's log is kept at
`<compose dir>/.deployments/<n>.log`.

Deployments run one at a time on the host, shared by every Compose, so a long
build delays the deploy of another Compose. A Compose has at most one queued
Deployment; pressing Build & Deploy again returns the queued one.

Each build leaves its previous image as a dangling image. Run
`docker image prune` to remove them.

### Build args and secrets

`build.args` can interpolate `${VAR}` from the Environment and Compose
Variables. An arg's value stays visible in the image's history, so keep tokens
out of args and pass them as build secrets. A secret is a Compose Variable
flagged secret:

```yaml
services:
  api:
    build:
      context: ./src/api
      secrets: [npm_token]     # Dockerfile: RUN --mount=type=secret,id=npm_token ...
secrets:
  npm_token:
    environment: NPM_TOKEN     # a Compose Variable, flagged secret
```
