# Spec — Server migration (move a whole panel to a new host)

Date: 2026-10-19.
Status: needs-triage

## Problem

Moving a Relist Panel install to a new server today means copying the data directory, every named
Volume, every host path a Compose mounts and every Image without a registry by hand, then redeploying
each Compose. It is the reason the panel was started, and nothing in it helps yet.

Some details make doing this by hand easy to get wrong:

- Containers mount the data directory by absolute path. ADR-0002 bind-mounts it at an identical path,
  and since ADR-0017 each Deployment mounts its own materialized source tree from it. A new host with
  a different `RELIST_DATA_DIR` starts Containers that cannot find their files.
- Named Volumes carry Compose's labels. Recreating one by hand without them makes Compose refuse or
  warn about a volume it did not create.
- Images built from a Source (`relist-c42-api:latest`) have no registry to pull them from.
- Copying a database's Volume while it runs produces a corrupt copy.

## Goal and success criteria

- An operator installs an empty panel on a new host and, from its setup screen, picks **Migrate from
  another panel**. They enter the old panel's URL and a **Migration token**, and the new panel pulls
  everything over HTTPS. No SSH and no intermediate file are needed.
- After the **Migration**, the new host has the old `panel.db` (same admin, password, Two-factor,
  Projects, Environments, Composes, Variables, Domains, Deploy keys and Deployment history), the
  whole data directory, every named Volume, the chosen host paths and every Image the Composes need.
  The Composes that were running on the old host are running on the new one.
- Traefik on the new host serves the old certificates (`acme.json`), so HTTPS works as soon as DNS
  points at it, without asking Let's Encrypt again.
- During and after the Migration, the old host is frozen by a **Migration lock**, so its data cannot
  drift from what was copied. Its panel-managed Domains show a maintenance page.
- A failed item is retried on its own. A 50 GB Migration does not start over because one Volume
  failed.
- The operator can cancel at any point before the new host finalizes, and can return to the old
  host at any time with **Resume this server**.

## Out of scope

- Choosing which Projects, Environments or Composes to move. A Migration moves everything. The only
  choice is which host paths outside the data directory to include.
- Merging into a panel that already has data. The new panel must be empty.
- Two-phase sync (copy while running, then a short final delta). V1 copies once, after stopping
  everything. The item-based protocol is designed so a later delta pass can be added per item.
- Forwarding traffic from the old host to the new one while DNS propagates. V1 shows a maintenance
  page instead. Forwarding is a follow-up.
- Scheduled backups to remote storage, and restoring from one. The item format is meant to be
  reusable by a later backup feature.
- Volumes whose driver is not `local`, or that set driver options (NFS, CIFS, plugins). They are
  reported and not moved.
- Changing DNS. The panel tells the operator what to change.

## Language

Added to `CONTEXT.md`:

- **Migration**: moving everything one panel manages to an empty panel on another host. The new
  panel pulls from the old one. _Avoid_: Backup, Restore, Transfer (as the feature name).
- **Migration token**: a short-lived, single-use secret created on the old panel that lets one new
  panel claim it as its Migration source.
- **Migration lock**: the persisted state in which the old panel refuses every change, so what is
  copied stays what is there. It survives a restart. Its final form is **Migrated**.

ADR-0018 records the decisions below. It widens ADR-0001 (the panel now pulls Images during a
Migration) and leans on ADR-0002's identical-path rule.

## Flow

### On the old host

1. On the Docker page (`/docker`), the **Server migration** card has **Create migration token**. The
   operator picks a lifetime of 1 hour, 24 hours (default) or 7 days. The token is shown once. A
   token can be revoked until a Migration starts. There is at most one live token at a time: creating
   a new one revokes the previous one.
2. Nothing is locked yet. The panel works normally.

### On the new host

1. The operator installs the panel with the same `RELIST_DATA_DIR` as the old host and opens it.
   The setup screen (`AuthPages.tsx`) gains **Migrate from another panel** next to creating the admin
   account.
2. They enter the old panel's URL (`https://` only) and the token. The new panel claims the token
   and runs **preflight**.
3. **Preview**: total size by kind, the Images it will pull and the ones it will copy, the named
   Volumes, a checklist of host paths outside the data directory, and every warning. **Start
   migration** stays disabled while a blocking check fails.
4. **Start migration** asks the old host to lock (below), then pulls items one at a time, with
   progress per item.
5. When every item has passed, the new panel finalizes (below), then shows **Done**: each Compose's
   deploy result with a link to its Deployment log, and the DNS records to point at this host.

### Locking the old host (`start`)

In this order, recorded in the database before each step so a restart resumes it:

1. Persist the Migration lock. From here on:
   - The UI shows a full-page "This server is being migrated" screen.
   - The API answers `503` with that message for every route except `/api/migration/*`, `/healthz`,
     `/api/auth/status`, `/api/auth/login` and `/api/auth/logout`.
   - The deployment queue accepts nothing new. Queued Deployments are canceled with the reason
     "server migration".
   - The host guard (`internal/dockerhost/guard.go`) refuses prune, Image loading and Volume
     operations. Open Terminal Sessions are closed.
2. Wait for a running Deployment to end. The new panel shows "Waiting for Deployment #n of
   <compose>".
3. Record which Composes have running Containers, then `docker compose stop` each one. `stop` keeps
   the Containers, so Resume can start them again.
4. Start the **maintenance container**: one container named `relist-maintenance`, run from the
   panel's own image (`relist-panel maintenance-page`), on `relist-network`. It carries one Traefik
   router per panel-managed Domain, with the same rule, entrypoints and certificate resolver as
   the Domain's own router, and answers `503` with a "down for maintenance" page. Routers that the
   operator hand-wrote in YAML are not covered, and preflight lists them.
5. Build and return the **manifest**.

### Finalizing on the new host

1. Move the staged data directory into place and put the new `panel.db` beside it. Write the marker
   `.migration/finalize`, then exit. The install's `restart: unless-stopped` brings the panel back.
   If the panel runs without a restart policy, the screen says to restart it.
2. On boot with the marker present:
   - Run goose migrations.
   - Clear the Migration lock and tokens copied from the old database.
   - Mark queued and running Deployments `canceled` with the reason "server migration".
3. Restart the install stack's Traefik container. It is found by the panel container's
   `com.docker.compose.project` label and service `traefik`. Traefik reads `acme.json` only at start.
4. Enqueue a `skip-build` Deployment (ADR-0016) for every Compose that the manifest lists as running.
   They use the copied Images and materialized trees, so nothing is rebuilt and Git is not contacted.
5. Call `finish` on the old host, then delete `.migration/` except a summary file for the Done screen.

### After the Migration, on the old host

The old panel enters **Migrated**. Every page shows "Migrated to <new URL> on <time>". Nothing can
be changed, Composes stay stopped and the maintenance container keeps running. The only action is
**Resume this server**, which requires typing `RESUME` and warns that data written on the new host
is not here. Resume removes the maintenance container, starts the Composes that were running, then
clears the lock. The same action is **Cancel migration** before `finish`, and it is the rollback if
the new host fails.

## Protocol

All endpoints are on the old panel under `/api/migration/`. They are JSON except item streams.

| Endpoint | Auth | Purpose |
|---|---|---|
| `POST claim` | Migration token | Exchange the token for a session key. The token is spent. |
| `GET preflight` | session | Protocol version, schema version, `RELIST_DATA_DIR`, install settings, sizes, host paths, warnings |
| `POST start` | session | Lock (above) and return the manifest. Repeating it returns the same manifest. |
| `GET items/{id}` | session | Stream one item. |
| `GET items/{id}/digest` | session | The SHA-256 of the last complete stream of that item. |
| `POST abort` | session | Same as Cancel migration on the old host. |
| `POST finish` | session | Enter Migrated, with the new panel's URL. |
| `POST tokens` | admin | Create a token with a lifetime. Revokes any previous token. |
| `DELETE tokens/current` | admin | Revoke the token before `start`. |
| `GET status` | admin | Lock step, claimed-by address, target URL, for the old panel's own screen. |
| `POST resume` | admin | Cancel migration / Resume this server. Body `{"confirm":"RESUME"}`. |

"session" means the Migration session key as `Authorization: Bearer`. "admin" means the normal
signed-in admin cookie. Admin endpoints stay reachable under the lock because they are under
`/api/migration/`.

### Tokens and sessions

- A token is 32 random bytes, shown as base64url. The database stores its SHA-256, its expiry and
  whether it is spent. Lifetime: 1 hour, 24 hours or 7 days. Nothing longer is accepted.
- `claim` spends the token and returns a 32-byte session key, also stored hashed. There is one
  session per Migration. A second `claim` returns `409`. `claim` is rate limited per client IP like
  sign-in. Wrong tokens never reveal whether a token exists.
- The session key ends with `finish`, `abort`, Resume or token revocation before `start`.
- The new panel refuses `http://` sources. The CLI flag `--insecure-migration-source`, for tests
  and probes, allows them. The UI has no such option.

### Preflight checks

Blocking:

- The new panel's highest known goose migration is lower than the old database's applied version.
- `RELIST_DATA_DIR` differs between the hosts. The message gives the value to set.
- The new panel is not empty: an admin account exists, or any Project exists.
- A named Volume in the manifest already exists on the new host.
- A selected host path exists on the new host and is not an empty directory.
- Free space is too small. The data directory is checked with `statfs`. The Docker root
  (`DockerRootDir` from `docker info`) is checked through a helper run that mounts it read-only.
  Both need the item sizes plus 10%.
- The protocol versions differ.

Warnings:

- `RELIST_DOMAIN`, `RELIST_ACME_EMAIL`, `TRAEFIK_HTTP_PORT` or `TRAEFIK_HTTPS_PORT` differ. These
  live in the install's `.env`, not the data directory, so the operator copies them.
- A Volume has a driver other than `local` or driver options. It is not moved.
- Routers hand-written in YAML get no maintenance page.
- A Deployment is running or queued on the old host. Queued ones will be canceled.

### Manifest and items

The manifest is built after the Composes stop, so it does not change while items are copied. Each
item has an `id`, a `kind`, a size estimate and kind-specific fields.

| Kind | What | Old host streams | New host writes |
|---|---|---|---|
| `data-dir` | The data directory without `panel.db*` and `.migration/` | tar | Into `.migration/staging/`, moved into place at finalize |
| `database` | `panel.db` | A copy made with `VACUUM INTO` after the lock | `.migration/staging/panel.db` |
| `volume` | Each named Volume that any Compose declares, resolved with every profile enabled, including `external: true` ones and Orphan Volumes | Helper run with the Volume mounted read-only, tar | Create the Volume with the same name, driver and labels, then a helper run extracts into it |
| `host-path` | Each host path the operator ticked | Helper run with the path mounted read-only, tar | Helper run with the parent mounted, extract to the same path |
| `image` | Each Image of a stopped Container, by Image ID, plus its tags | `docker save` of the ID, only when asked | See Images |

- Tar streams keep numeric uid/gid, modes, symlinks and mtimes. A database Volume owned by uid 999
  must come back owned by uid 999. `volfs.Extract` strips setuid and has a 4 GiB cap, so the helper
  gains two subcommands, `relist-volhelper pack` and `relist-volhelper unpack`. They have no size
  cap, keep ownership, and keep the path rules: no absolute names, no `..`, no links that leave the
  target.
- Streams are plain tar without compression. The panel container has `cpus: 0.25`, and gzip at
  that budget would be the bottleneck. HTTPS is terminated by Traefik.
- The old host computes the SHA-256 while it streams. The new host computes its own and compares
  it with `items/{id}/digest`. A Traefik hop may drop HTTP trailers, so the digest is not sent
  as one.
- System paths are unticked by default in the host path checklist: anything under `/etc`, `/proc`,
  `/sys`, `/dev`, `/run`, `/var/run`, `/var/lib/docker`, the data directory itself, and any socket.
  Others are ticked by default. Paths that are a single file are moved as a file.

### Images

For each Image in the manifest:

1. If it has `RepoDigests`, the new host runs `docker pull <repo>@<digest>` and re-applies the
   original tags.
2. If it has none, or the pull fails (a private registry the panel's Docker CLI cannot log in to,
   a deleted tag, rate limits), the new host asks for the item stream and runs `docker load`.
3. Either way, the new host then checks that the Image ID matches the manifest.

This is the first time the panel pulls an Image. ADR-0018 records it as a Migration-only exception
to ADR-0001.

## Error handling

- The new panel keeps its progress in `.migration/state.json`, not the database, because the
  database is replaced at finalize. After a restart it continues from the first item not marked
  done.
- A failed item (network error, digest mismatch, extract error) is cleaned up: its staging
  directory, Volume or host path written so far is removed. It is retried three times with backoff,
  then the Migration pauses with **Retry** and **Abort**.
- **Abort** on the new host calls `abort` on the old host, which then acts like Resume. The new host
  deletes `.migration/`, the Volumes and host paths it created, and the Images it loaded, then
  returns to the setup screen.
- If the old host is unreachable, it stays locked. The operator retries from the new host or
  resumes the old one.
- A Compose whose deploy fails after finalize does not roll the Migration back. Done lists it with
  its log.
- The old host stays locked across restarts. On boot it continues whatever lock step it had
  reached, so Composes that stopped stay stopped and the maintenance container is recreated if it
  is missing.

## UI

Both screens follow `docs/GUIDELINES.md` and Ant Design (check `llms.txt` and the installed version
before building them).

- **Old host, Docker page:** a Server migration card with the token form, the token shown once with
  a copy button, its expiry and **Revoke**.
- **Old host, locked:** a full-page state with the lock step, the new panel's address once claimed,
  and **Cancel migration** (type `RESUME`). The same page in Migrated shows the target URL and
  **Resume this server**.
- **New host, setup:** a link to **Migrate from another panel**, then a page at `/migration` with
  three stages: Connect (URL, token), Preview (sizes, Images, host path checklist, warnings and
  blocking errors), Progress (one row per item with bytes and state), then Done.

## Testing

### Unit tests (`go test ./...`, no Docker)

- Tokens: expiry per lifetime, a lifetime above 7 days refused, single claim, `409` on a second
  claim, revocation only before `start`, only hashes stored, `claim` rate limit.
- Migration lock: persisted and reloaded at boot. `503` on every route outside the allowlist.
  The queue refuses new Deployments and cancels queued ones. `start` waits for a running one. The
  host guard refuses prune, Image load and Volume operations.
- Manifest: Volumes with all profiles enabled, `external: true` and Orphan Volumes. Non-local
  drivers become warnings. Images split by `RepoDigests`, including Images built from a Source.
  System host paths are unticked.
- Preflight: each blocking check, and an `http://` source refused.
- Helper `pack` / `unpack`: ownership kept, no cap, unsafe names refused.
- Finalize: the lock and tokens cleared, Deployments canceled, `skip-build` Deployments only for
  Composes that were running, resume from `state.json`.

### Integration tests (both panels in one process)

Two `httptest.Server` panels with separate temporary data directories and a fake Docker runner:

- Happy path: claim, preflight, start, every item, finalize, finish. The old panel is Migrated, and
  the new database matches the snapshot.
- A connection cut mid-item: retried, nothing half-written left.
- A digest mismatch: the item is retried.
- A pull failure: falls back to `docker save`.
- Abort: the old panel resumes, its Composes start and the maintenance container is removed.
- Resume requires `RESUME`.

### End-to-end probe (manual, not CI)

`task probe:migration` starts two `docker:dind` hosts, installs the panel on both, and deploys three
Composes on the old one:

1. Postgres with data in a named Volume.
2. A Compose built from a zip Source (an Image without `RepoDigests`).
3. A Compose bind-mounting `/srv/probe`.

It migrates, then checks that the Postgres rows, the files under `/srv/probe` and `acme.json` match,
and that every Compose is running. The spec records each probe run by date, as ADR-0016 records the
`docker:27-cli` probe. The probe also confirms that a stream of over 4 GiB passes through Traefik
untouched.

## Left to the plan

- The maintenance container is a bare `docker run -d` container, not a Compose project, so it never
  appears among the operator's Composes. It runs with `--restart unless-stopped` and the label
  `relist.maintenance=1`, which is how the old panel finds it again after a restart.
- The plan picks the Ant Design components for the progress list and the full-page lock state.
