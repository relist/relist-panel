# Spec — Domains (route a Compose's Services through Traefik without hand-written labels)

Date: 2026-10-09. Sub-project 1+2 of the domain-management effort. Sub-project 3, a **Certificates**
section for uploading a cert and key that Traefik serves through its file provider, gets its own spec
later.

## Problem

To give a Service a domain today, the operator writes Traefik labels by hand inside the Compose YAML
(`traefik.enable`, `traefik.http.routers.<x>.rule=Host(...)`,
`traefik.http.services.<x>.loadbalancer.server.port`, ...). ADR-0001 made this deliberate: "No
domain-management UI — Traefik labels are written by hand inside the Compose file." In practice it
is the most error-prone part of every deploy: router names must be unique across the whole host, the
Host rule needs backticks, and the label that pins Traefik to `relist-network` is easy to forget.

There is also no HTTPS. The install's Traefik (`docker-compose.yml`) has only the `web` entrypoint on
port 80, with no certificate resolver, so the panel's own login, 2FA codes and Volume files travel in
plain HTTP unless the operator reconfigures Traefik by hand (README, "Volume files").

## Goal and success criteria

- A Compose page has a **Domains** tab. The operator adds a domain with a form, modelled on
  Dokploy's domain dialog: Host, Path, Internal Path, Strip Path, **Service**, Container Port,
  Custom Entrypoint, HTTPS, Certificate Provider (None / Let's Encrypt / Custom), Middlewares.
  2026-10-28: the default is now None — see `.scratch/certificates/spec.md`.
- On the next deploy the panel injects the matching Traefik labels into the resolved Compose file.
  The YAML the operator wrote stays untouched.
- A domain with HTTPS and Let's Encrypt serves a valid certificate on a public host, and plain HTTP
  for it redirects to HTTPS.
- The panel itself is served over HTTPS with a Let's Encrypt certificate, and HTTP redirects to it.
- Existing Composes with hand-written Traefik labels keep working unchanged.

## Out of scope

- Uploading certificates (the Certificates section, sub-project 3). Until it exists, the **None**
  provider serves Traefik's default self-signed certificate.
- A host-wide list of every domain.
- Dokploy's "generate a host" button.
- Wildcard hosts. Let's Encrypt HTTP-01 cannot issue them.
- DNS-challenge setup. An operator who wants it defines their own resolver in Traefik's static
  config and selects it with the **Custom** provider.
- Applying domain changes without a redeploy.
- Fixing `network_mode` handling in network injection (see Known issue).

## Decisions

### Labels injected at deploy, not a file provider or an override file

The labels are added inside `resolveCompose` (`internal/composes/composes.go`), the function that
already injects `relist-network` and the generated `env_file` into every Service (ADR-0002). This is
the second thing the panel adds to user-authored YAML, and it is added the same way: to the resolved
file on disk, never to the stored YAML.

Rejected: a generated `docker-compose.domains.yml` passed as a second `-f`. Every compose command the
panel runs (`up`, `down`, `ps`, `logs`, `restart`, volume resolution) would then need both files, and
missing it once makes `docker compose` see a different project. Also rejected: Traefik's file
provider. It applies changes without a redeploy, but its backend URL would be
`http://<service>:<port>` on `relist-network`, and two Composes that both name a Service `web` would
share that DNS name, so Traefik would round-robin between them. Fixing that needs a unique network
alias injected at deploy anyway.

Consequence: Traefik reads labels when a container is created, so a domain change takes effect on
the next deploy. The UI says so (see Redeploy banner).

### Network: `relist-network` and `traefik.docker.network`

Unchanged: every Service is already on `relist-network`, injected by `resolveCompose`, with the
network declared `external: true`. A Service with a domain is therefore always reachable by Traefik.

Added: every Service with at least one domain gets `traefik.docker.network=relist-network`. Each
Service sits on two networks, its Compose's default network and `relist-network`. Without the label,
Traefik may pick the container's IP on the default network, which Traefik is not on, giving
intermittent 502/504. If the YAML already sets `traefik.docker.network` to another value on that
Service, the panel overwrites it and returns a warning.

### Names derived from database IDs

Each domain's Traefik objects are named `N = relist-c<composeID>-d<domainID>`. Router, service and
middleware names are global across the Traefik instance. Database IDs are unique and never reused
(`AUTOINCREMENT`), and they do not depend on mutable names (ADR-0005) or on the Compose's identity
version (ADR-0011), so legacy and ID-based Composes are named the same way. A name like this cannot
collide with a hand-written one unless the operator writes a `relist-c…` name on purpose.

### Merging with hand-written labels

The panel adds its own labels and leaves the operator's alone, with three exceptions on a Service that
has a domain. In each case the panel overwrites the value and the deploy response carries a warning:

- `traefik.enable=false` becomes `true`.
- `traefik.docker.network` with another value becomes `relist-network`.
- A hand-written router on that Service with no explicit `traefik.http.routers.<r>.service`. Traefik
  links such a router automatically: to the only service defined on the container, or to a default
  service it creates when none is defined. The panel adds one service per domain, which changes that
  outcome. With no hand-written service label, the router now silently routes to the domain's
  port. With a hand-written service as well, there are two services and the router errors. The
  panel changes nothing here. It only warns, naming the router, so the operator can add `.service`.

Two routers for the same host, one generated and one hand-written, are detected only within the
same Compose. If a hand-written `traefik.http.routers.*.rule` in this Compose's YAML contains
`` Host(`<host>`) `` for a host one of its domains manages, the deploy warns, naming the router.
`UNIQUE (host, path)` does not see hand-written rules in other Composes or the panel's own
`RELIST_DOMAIN`, so those collisions go undetected (see Error handling).

`labels:` may be a list (`- key=value`) or a map (`key: value`). The panel reads either and writes
back the same shape.

### TLS foundation in the install compose (always on)

All of this is Traefik static config in the install `docker-compose.yml`. The panel cannot read or
change it at runtime.

Traefik `command` adds:

```
--entrypoints.websecure.address=:443
--entrypoints.websecure.transport.respondingTimeouts.readTimeout=3600s
--certificatesresolvers.letsencrypt.acme.email=${RELIST_ACME_EMAIL:-}
--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json
--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web
```

- Port `"${TRAEFIK_HTTPS_PORT:-443}:443"`.
- Volume `${RELIST_DATA_DIR:-/var/lib/relist-panel}/.traefik:/letsencrypt`. The certificates live in
  the data dir, so the same backup covers them. Traefik creates `acme.json` with mode 600 itself. The directory is dotted, like `.composes`, so a legacy Project slugged `traefik` cannot share it.
- `websecure` gets the same one-hour read timeout as `web`, so an image upload through the panel's
  domain still works over HTTPS.
- Traefik answers `/.well-known/acme-challenge/` on `web` at top priority, ahead of the panel's
  per-domain redirect routers.

The panel's own labels change. The `panel` router moves to `websecure` with
`tls.certresolver=letsencrypt`. A new `panel-http` router on `web`, with the same Host rule, carries a
`redirectscheme` middleware (`scheme=https`, `permanent=true`).

There is no global redirect on the `web` entrypoint, because it would break domains deliberately left
on HTTP. Redirects are per router.

HTTPS is always on rather than switched by an env var, because Compose cannot add or remove
flags and labels conditionally. The alternative, a separate `docker-compose.https.yml`, means two
install modes, and the panel cannot see which one Traefik runs, so an HTTPS domain would fail silently
in HTTP mode.

`RELIST_ACME_EMAIL` is optional. Traefik's reference marks `acme.email` as required, but a probe on
2026-10-08 showed otherwise. It used `traefik:v3.7` (lego v5.4.1) with `acme.email=` against the Let's
Encrypt staging CA: the account registered (`Register...`) and the run reached `new-order`, which
failed only on the probe's forbidden `example.com` host. The README recommends setting the email
anyway, both for Let's Encrypt account recovery and in case a later Traefik enforces it.

Effect on local development:

- The bare binary on `:8080` and the direct port `:8888` bypass Traefik and do not change.
- `http://panel.localhost` redirects to `https://panel.localhost`, which shows a self-signed
  certificate warning, because Let's Encrypt does not issue for `.localhost`. Traefik logs an ACME
  error for it on each retry. This is log noise only.
- Port 443 must be free on the host, or `TRAEFIK_HTTPS_PORT` set to another port.
- HTTP-only domains such as `myapp.localhost` behave as before.

## Data model

Migration `00013_domains.sql`:

```sql
CREATE TABLE domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    host TEXT NOT NULL,                 -- lowercase
    path TEXT NOT NULL DEFAULT '/',
    internal_path TEXT NOT NULL DEFAULT '/',
    strip_path INTEGER NOT NULL DEFAULT 0 CHECK (strip_path IN (0, 1)),
    service TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    entrypoint TEXT NOT NULL DEFAULT '', -- '' = default (web / websecure)
    https INTEGER NOT NULL DEFAULT 0 CHECK (https IN (0, 1)),
    cert_provider TEXT NOT NULL DEFAULT 'none' CHECK (cert_provider IN ('none', 'letsencrypt', 'custom')),
    cert_resolver TEXT NOT NULL DEFAULT '', -- used only when cert_provider = 'custom'
    middlewares TEXT NOT NULL DEFAULT '[]', -- JSON array of strings
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (host, path)
);
CREATE INDEX domains_compose_id_idx ON domains(compose_id);

ALTER TABLE composes ADD COLUMN domains_changed_at INTEGER;
```

- `UNIQUE (host, path)` is host-wide: no two Composes can claim the same host and path. Host is stored
  lowercase, so the constraint is case-insensitive in practice. Path is case-sensitive, as in Traefik.
- `composes.domains_changed_at` is set on every create, update and delete of one of that Compose's
  domains. It lives on `composes` rather than being derived from `domains.updated_at` because a
  deleted domain must still mark the Compose as needing a redeploy.
- `PRAGMA foreign_keys = ON` is already set (`internal/database/database.go`), so deleting a Compose,
  or its Environment or Project, removes its domains.

### Validation

Values end up inside Traefik label values and rule expressions, so validation is strict. That keeps
a value from breaking out of its label or rule.

| Field | Rule |
|---|---|
| `host` | Hostname: 1–253 chars, dot-separated labels of `[a-z0-9-]` (1–63, not starting or ending with `-`). Lowercased before checking. No `*`, no port, no scheme. |
| `path`, `internal_path` | Start with `/`; no backtick, space, `?` or `#`; at most 255 chars. |
| `strip_path` | Allowed with any path. It has no effect when `path` is `/`. |
| `service` | A key of `services:` in the Compose's **saved** YAML. That Service must not set `network_mode`. |
| `port` | Integer 1–65535. |
| `entrypoint` | `''` or `[A-Za-z0-9_-]{1,64}`. |
| `cert_provider` | `none`, `letsencrypt` or `custom`. Ignored and stored as `none` when `https` is false. |
| `cert_resolver` | Required, `[A-Za-z0-9_-]{1,64}`, when the provider is `custom`. Otherwise stored as `''`. |
| `middlewares` | At most 20 entries, each `[A-Za-z0-9_.@-]{1,128}` (no comma, since Traefik joins them with commas). Order kept. |

## Generated labels

For a domain `N = relist-c<composeID>-d<domainID>` on Service `S`:

```
traefik.enable=true
traefik.docker.network=relist-network
traefik.http.routers.N.rule=Host(`<host>`)                      # + " && PathPrefix(`<path>`)" when path ≠ "/"
traefik.http.routers.N.entrypoints=<entrypoint>                  # custom, else websecure if https, else web
traefik.http.routers.N.service=N
traefik.http.services.N.loadbalancer.server.port=<port>
```

When `https`:

```
traefik.http.routers.N.tls=true
traefik.http.routers.N.tls.certresolver=letsencrypt              # provider letsencrypt
traefik.http.routers.N.tls.certresolver=<cert_resolver>          # provider custom; omitted for none
```

When `https` and no custom entrypoint, a second router redirects HTTP:

```
traefik.http.routers.N-http.rule=<same rule as N>
traefik.http.routers.N-http.entrypoints=web
traefik.http.routers.N-http.middlewares=N-redirect
traefik.http.routers.N-http.service=N
traefik.http.middlewares.N-redirect.redirectscheme.scheme=https
traefik.http.middlewares.N-redirect.redirectscheme.permanent=true
```

Middleware chain on router `N`, in this order and only when it is non-empty:

1. `N-strip`, when `strip_path` and `path ≠ "/"`: `traefik.http.middlewares.N-strip.stripprefix.prefixes=<path>`
2. `N-prefix`, when `internal_path ≠ "/"`: `traefik.http.middlewares.N-prefix.addprefix.prefix=<internal_path>`
3. The operator's `middlewares`, in their order.

These are written as `traefik.http.routers.N.middlewares=<comma-joined list>`.

Example: path `/api`, internal path `/v1`, strip on. A request for `/api/users` reaches the container
as `/v1/users`. With strip off, it reaches it as `/v1/api/users`.

A Service with several domains gets each domain's labels. Each domain names its own Traefik service
`N`, so two domains on different ports of the same Service work.

## Deploy

`prepareDeployFiles` loads the Compose's domains and passes them to `resolveCompose`.

- A domain whose `service` is no longer a key of the YAML's `services:`, or whose Service now sets
  `network_mode`, refuses the deploy with `400`, before any file is written:
  `domain api.example.com/ points at service "web", which is not in the compose file`. The operator
  edits or deletes the domain, then deploys. Deploy does not skip the domain silently.
- Both `deploy` and `deploy-fresh` responses gain `warnings: string[]` next to the Compose fields. It
  is `[]` when there is nothing to report. Other endpoints that return a Compose do not include it.
- A Compose with no domains resolves exactly as today, apart from the order of map keys.

## HTTP protocol

All routes sit under `/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}`
and use the same auth and project gate as the other Compose routes.

### `GET …/domains`

`200`:

```json
{
  "domains": [
    {
      "id": 3, "host": "api.example.com", "path": "/", "internalPath": "/", "stripPath": false,
      "service": "web", "port": 3000, "entrypoint": "", "https": true,
      "certProvider": "letsencrypt", "certResolver": "", "middlewares": [],
      "createdAt": "…", "updatedAt": "…"
    }
  ],
  "services": ["db", "web"]
}
```

`services` is the sorted list of keys of `services:` in the **saved** YAML, so the Service dropdown
works before the first deploy. Services that set `network_mode` are left out. If the saved YAML does
not parse, `services` is `[]` and the response stays `200`. The Editor tab already reports YAML errors.

### `POST …/domains` and `PUT …/domains/{domainID}`

The body is the domain object without `id`, `createdAt` and `updatedAt`. Every field is sent; omitted
optional fields take their defaults. The response is `201` or `200` with the stored domain.

- `400` `{ "error": "…", "field": "host" }`, for the first invalid field.
- `409` when host and path are taken: `api.example.com/ is already used by my-saas / production / web`,
  naming the owning Project / Environment / Compose (which may be this Compose).
- `404` when the Compose or the domain does not exist under this route.

### `DELETE …/domains/{domainID}`

`204`, or `404`.

### Compose JSON

`Compose` gains `domainsChangedAt`, a timestamp or `null` when no domain has ever changed.

## User interface

Before building, read Ant Design's `llms.txt` and the pages for `Modal`, `Form`, `Switch`, `Select`
(tags mode), `Table`, `Alert` and `Popconfirm`, checked against `antd` 6.5.1 (AGENTS.md). Follow
`docs/GUIDELINES.md`.

### Domains tab

On the Compose page, after Editor: Editor · **Domains** · Logs · Terminal · Monitor · Volumes. The tab
has its own URL, as the other tabs do.

- **Redeploy banner.** An `Alert` (type `info`): "Domain changes apply on the next deploy." It has a
  **Deploy** button that runs the page's normal deploy. Shown when the Compose has deployed before
  (`lastDeployedAt` not null) and `domainsChangedAt >= lastDeployedAt`. Both timestamps have
  one-second resolution, so a tie counts as pending: a needless banner is safer than a missing one.
  A Compose that has never deployed shows no banner, because its first deploy includes the domains.
- **Table:**
  - Host + path, as a link to `http(s)://host/path` in a new tab.
  - `service:port`.
  - HTTPS tag: `HTTP` / `Let's Encrypt` / `custom: <resolver>` / `TLS (default cert)`.
  - Edit / Delete actions.
- **Empty state:** "No domains. Add one to route a Service through Traefik." with an **Add domain**
  button. The **Add domain** button also sits in the tab header.
- **Delete:** a `Popconfirm` naming the host and path. It changes nothing on the host until the next
  deploy, so it is a cheap delete under GUIDELINES' destructive-action rule.

### Add / Edit Domain modal

A `Modal` with a `Form`, fields in this order:

1. **Host** (required).
2. **Path** (default `/`).
3. **Internal Path** (default `/`). Help: "The path your application expects to receive requests on."
4. **Strip Path** (`Switch`). Help: "Remove the external path before forwarding."
5. **Service** (`Select`, required), filled from `services`. If `services` is empty, a hint says the
   saved YAML declares no Services that can take a domain.
6. **Container Port** (`InputNumber`, required).
7. **Custom Entrypoint** (`Switch`). When on, an **Entrypoint** input (required) appears.
8. **HTTPS** (`Switch`). When on, **Certificate Provider** appears (`Select`: None / Let's Encrypt /
   Custom, default Let's Encrypt).
   - **Custom** shows **Resolver name** (required), with help: "Must match a certificate resolver in
     Traefik's static config. The panel can't check it — a wrong name shows only in Traefik's log."
   - **None** shows help: "Traefik serves a certificate matching this host if it has one, otherwise
     its default self-signed certificate."
9. **Middlewares** (`Select mode="tags"`), with placeholder `e.g. rate-limit@file, auth@file`.

A hidden field is cleared when it is hidden, so switching Custom → Let's Encrypt sends
`certResolver: ""`. Server errors with a `field` show on that field. A `409` shows above the form.

### Deploy warnings

When a deploy or deploy-fresh response has `warnings`, the Compose page shows them in a `warning`
`Alert` until the operator closes it or deploys again.

## Error handling

| Case | Result |
|---|---|
| Invalid field | `400` with `field`, shown on the form field |
| Host + path taken | `409` naming the owner, shown above the form |
| Domain's Service missing from YAML, or uses `network_mode` | Deploy refused with `400`, message names domain and Service |
| Hand-written `traefik.enable=false` / other `traefik.docker.network` on a Service with a domain | Overwritten; deploy warning |
| Hand-written router without `.service` on a Service with a domain | Left as is; deploy warning naming the router |
| Custom resolver name that Traefik does not know | Not detectable by the panel; Traefik logs it and serves its default cert. Covered by the form help text. |
| Let's Encrypt cannot issue (DNS not pointing here, port 80 closed, `.localhost`) | Not detectable by the panel; Traefik serves its default cert and logs the ACME error. README explains this. |
| Hand-written router in the same Compose with `Host()` of a managed host | Deploy warning naming the router |
| Same host in a hand-written label of another Compose, or equal to `RELIST_DOMAIN` | Not detected; Traefik picks one router by priority. README notes it. |
| Panel upgraded but install `docker-compose.yml` not (no `websecure`, no `letsencrypt` resolver) | Not detectable by the panel; every HTTPS domain fails, and only Traefik's log says why. The README's **Upgrading an existing install** section is the guard. |

## Known issue (not fixed here)

`resolveCompose` adds `networks:` to every Service, including one that sets `network_mode`, and
`docker compose` refuses that combination. Such a Compose already fails to deploy today. This spec
only keeps domains away from those Services. A fix for the injection itself is a separate change.

## Testing

### Go: label generation (pure function)

- HTTP only; HTTPS with `letsencrypt`, `custom` and `none`; custom entrypoint (no `N-http` router).
- Path `/` (no `PathPrefix`) vs `/api`; strip on/off; internal path `/` vs `/v1`; middleware chain
  order with operator middlewares.
- `labels:` as a list and as a map, each keeping its shape; hand-written labels kept unchanged.
- `traefik.enable=false` and a foreign `traefik.docker.network` overwritten with warnings; a
  hand-written router without `.service` produces a warning naming it; a hand-written router whose
  rule has `` Host(`<managed host>`) `` produces a warning naming it.
- Two domains on one Service, with different ports.
- Names are `relist-c<composeID>-d<domainID>` for both identity versions.

### Go: HTTP API (`internal/httpapi`, fake runner)

- CRUD and each validation rule, including backtick and comma rejection.
- `409` across two Composes, with the owner's names in the message.
- `domainsChangedAt` set on create, update and delete; deleting a Compose removes its domains.
- `services` comes from the saved YAML for a Compose that has never deployed, leaves out
  `network_mode` Services, and is `[]` for unparsable YAML.
- Deploy writes the labels into the resolved file; deploy is refused when a domain's Service is
  missing; `warnings` is present on deploy and deploy-fresh.

### Vitest: `domains-view.ts`

- Form ↔ payload mapping, including clearing hidden fields.
- The banner rule: never deployed, tie, changed after, changed before.
- HTTPS tag text.

### Manual

1. **Local:** bring up the new install file. `:8888` works; `http://panel.localhost` redirects to
   HTTPS with the self-signed cert; an HTTP-only domain `whoami.localhost` on a `traefik/whoami`
   Compose routes; strip path and internal path checked with `curl -H 'Host: …'`.
2. **Public host:** with a real domain pointing at the host, first against the Let's Encrypt staging
   CA (`--certificatesresolvers.letsencrypt.acme.caserver=…staging…`), then production: the
   certificate is issued, HTTP redirects to HTTPS, the panel itself serves a valid certificate. If no
   public host is available, record this step as not run.
3. **Upgrade:** follow the README upgrade steps on an install with a Compose that uses hand-written
   labels; it keeps routing.

## Documentation

- **ADR-0013** (new): the panel manages domains by injecting Traefik labels at deploy. It supersedes
  ADR-0001's "No domain-management UI" non-goal, extends ADR-0002's injection, and records the
  rejected override-file and file-provider options plus the always-on HTTPS decision.
- **ADR-0001:** a "Widened since" note on the domain non-goal pointing to ADR-0013.
- **CONTEXT.md:** add **Domain**: a host and path that Traefik routes to one Service's port in a
  Compose, managed on the Compose's Domains tab and applied on deploy. _Avoid_: Route, Hostname (as
  the entity name). Update **Compose**: drop "the panel has no separate domain-management UI".
- **README.md:**
  - Replace the "TLS/ACME … left to the operator" paragraph.
  - Document `RELIST_ACME_EMAIL` and `TRAEFIK_HTTPS_PORT`.
  - Note that the one-hour read timeout now covers `websecure` too.
  - Explain the local-development effect.
  - Add an **Upgrading an existing install** section: update `docker-compose.yml`, set
    `RELIST_ACME_EMAIL`, open port 443, `docker compose up -d`.
  - Update the "Volume files" TLS note.
- **docs/GUIDELINES.md:** a §4 entry for the Domains tab.

## Manual verification

Run on 2026-10-09 against the host's real Docker daemon (Docker 29.8.0, Compose v5.5.1), branch
`feat-domains`. There is no browser, and the host's own Relist install (panel on :8888, Traefik on
:80, plus another Compose) was running and had to stay untouched. So the panel was built (`task
build`) and run as a bare process (`RELIST_DATA_DIR` in a temp dir, `RELIST_PORT=18888`), driven over
HTTP with `curl` and a session cookie. A standalone `traefik:v3.7` container
(`relist-domtest-traefik`) on `relist-network` published 18080→80 and 18443→443, with the same flags
as the install file's `traefik` service plus the Let's Encrypt **staging** CA, and a provider
constraint (`LabelRegex(com.docker.compose.project, ^relist-c[0-9]+$)`) so it ignored the host's
other containers. The Compose ran `traefik/whoami` as service `web`.

| # | Step | Observed | Result |
|---|------|----------|--------|
| a | `GET .../domains` on the new Compose | `{"domains":[],"services":["web"]}` | PASS |
| b | HTTP domain `whoami.localhost`, path `/api`, internal path `/v1`, strip on; deploy; `curl -H 'Host: whoami.localhost' http://localhost:18080/api/x` | Deploy `200`, `warnings: []`. whoami printed `GET /v1/x` and `X-Forwarded-Prefix: /api`. | PASS |
| c | HTTPS domain `whoami-tls.localhost` (`letsencrypt`, default entrypoint); redeploy | `curl -I` on HTTP: `308 Permanent Redirect`, `Location: https://whoami-tls.localhost/`. `curl -sk --resolve` on 18443 returned whoami output with Traefik's default cert (`CN=TRAEFIK DEFAULT CERT`). Traefik logged `Unable to obtain ACME certificate ... rejectedIdentifier ... Domain name does not end with a valid public suffix (TLD)`, as expected for `.localhost`. The earlier `/api/x` route still worked. | PASS |
| d | YAML with `labels: [traefik.enable=false]` on `web`; deploy | `200`, `warnings: ["service \"web\": traefik.enable=false was overridden to true because a domain points at it"]` | PASS |
| e | YAML with `web` renamed to `web2`; deploy | `400 {"error":"domain whoami.localhost/api points at service \"web\", which is not in the compose file"}`. The `relist-c1-web-1` container ID was unchanged, so nothing was redeployed. Restoring the YAML and redeploying gave `200`. | PASS |
| f | `docker compose -f docker-compose.yml config` | Router `panel` (entrypoints `websecure`, `tls.certresolver=letsencrypt`, service `panel`); router `panel-http` (entrypoints `web`, middlewares `panel-redirect`, service `panel`); middleware `panel-redirect` (`redirectscheme.scheme=https`, `permanent=true`). Live panel redirect: not run, the host's install holds the default ports. | PASS (labels); live redirect NOT RUN |
| g1 | Public host, Let's Encrypt issuance (staging, then production) | No public host available. | NOT RUN |
| g2 | Upgrade path on an existing install with hand-written labels | Would modify the host's running install. | NOT RUN |
| h | Host's own stack during the run | `http://localhost:8888/healthz` and `/` both `200`; `relist-panel-panel-1`, `relist-panel-traefik-1` and `hello-production-test-whoami-1` stayed Up (about 35 minutes, never restarted). | PASS |

Not verified: a real Let's Encrypt certificate (neither staging nor production issued one, since
`.localhost` cannot be certified and there was no public host); the live `panel.localhost` HTTP→HTTPS
redirect with the self-signed cert; the README upgrade steps on an existing install; the Domains tab
in a browser (banner, HTTPS tag, form behaviour).

Notes: a first attempt at row c set `entrypoint` to `websecure` in the request, which the API treats
as a custom entrypoint (spec: no `N-http` redirect router), so plain HTTP got 404; leaving
`entrypoint` empty gave the default and the redirect. That matches the spec, not a bug. The Compose was
deleted through the API (204), which removed its containers; the test Traefik container, the panel
process and the temp directory were removed. No images were pulled or removed, and `relist-network`
was left alone.
