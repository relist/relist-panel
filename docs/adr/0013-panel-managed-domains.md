# Panel-managed domains and always-on HTTPS

Giving a Service a domain meant writing Traefik labels by hand in the Compose YAML: unique router names across the whole host, a backticked Host rule, and the label that pins Traefik to `relist-network`. It was the most error-prone part of every deploy. ADR-0001 made it a non-goal to manage domains, and the install's Traefik had only an HTTP entrypoint, so the panel's own login and Volume files travelled in plain HTTP.

**Decision:** the panel stores a Compose's **Domains** and, at deploy, injects the matching Traefik labels into the resolved Compose file, in `resolveCompose` (`internal/composes/composes.go`). This supersedes ADR-0001's "No domain-management UI" non-goal and extends ADR-0002's injection: it is the second thing the panel adds to user-authored YAML, added the same way, to the resolved file on disk and never to the stored YAML. Each domain's Traefik objects are named `relist-c<composeID>-d<domainID>`, from database IDs that are unique and never reused. Hand-written labels are left alone, except where they would defeat a domain (`traefik.enable=false`, another `traefik.docker.network`); the panel overwrites those and the deploy response carries a warning.

The install `docker-compose.yml` gains an always-on `websecure` entrypoint on port 443 (`TRAEFIK_HTTPS_PORT`) and a Let's Encrypt resolver named `letsencrypt` using the HTTP-01 challenge on `web`, with certificates stored under `<data dir>/.traefik`, dotted so a legacy Project slugged `traefik` cannot share it. The panel's own router moves to `websecure`, and a `panel-http` router redirects HTTP to it. There is no global redirect on `web`, because that would break domains deliberately left on HTTP.

## Considered options

**A generated `docker-compose.domains.yml` passed as a second `-f`.** Rejected: every compose command the panel runs (`up`, `down`, `ps`, `logs`, `restart`, volume resolution) would need both files, and missing it once makes `docker compose` see a different project.

**Traefik's file provider.** Rejected: it applies changes without a redeploy, but its backend URL is `http://<service>:<port>` on `relist-network`, and two Composes that both name a Service `web` share that DNS name, so Traefik would round-robin between them. Fixing that needs a unique network alias injected at deploy anyway.

**HTTPS switched by an env var, or a separate `docker-compose.https.yml`.** Rejected: Compose cannot add or remove flags and labels conditionally, and a second file means two install modes. The panel cannot see which one Traefik runs, so an HTTPS domain would fail silently in HTTP mode. HTTPS is always on instead.

## Consequences

- **A domain change applies on the next deploy.** Traefik reads labels when a container is created. The UI says so.
- **`RELIST_ACME_EMAIL` is optional.** Traefik's reference marks `acme.email` as required, but a probe on 2026-10-08 showed otherwise. It used `traefik:v3.7` (lego v5.4.1) with `acme.email=` against the Let's Encrypt staging CA: the account registered and the run reached `new-order`, which failed only on the probe's forbidden `example.com` host. The README recommends setting it anyway, for account recovery and in case a later Traefik enforces it.
- **The panel cannot read Traefik's static config.** A panel upgraded without the new install file (no `websecure`, no `letsencrypt`) fails every HTTPS domain, and only Traefik's log says why. The README's "Upgrading an existing install" section is the guard.
- **Let's Encrypt failures are invisible to the panel.** If DNS does not point at the host, port 80 is closed, or the host is `.localhost`, Traefik serves its default certificate and logs the ACME error.
- **Local development changes.** `http://panel.localhost` redirects to a self-signed `https://panel.localhost`, and port 443 must be free or `TRAEFIK_HTTPS_PORT` set.
- **Known issue, not fixed here.** `resolveCompose` adds `networks:` to every Service, including one that sets `network_mode`, and `docker compose` refuses that combination. Such a Compose already fails to deploy. Domains are only kept away from those Services; fixing the injection is a separate change.
