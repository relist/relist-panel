# relist-network only where needed, with unique aliases

ADR-0002 put every Service of every Compose on the shared `relist-network`. A Service with no `networks:` key got **only** that network, so a Compose's own Services also reached each other over it. Docker Compose registers the bare Service name as an alias on every network a Service joins. Two Composes that each have a `db` therefore both answered to `db`, and one app could reach the other Compose's database. There was also no unique name for calling a Service in another Compose.

**Decision:** in `resolveCompose` (`internal/composes/networks.go`), a Service without `network_mode` joins `relist-network` only when one of these holds:
- a Domain targets it;
- it carries a hand-written `traefik.*` label;
- its YAML already lists `relist-network`. This is the opt-in for calls from other Composes.

Every other Service keeps the networks its YAML gives it. That is the Compose's private `default` network when it has none. A joining Service with no `networks:` key gets `default` beside `relist-network`, so it still reaches its own siblings. A list-form `networks:` becomes a map so aliases can be attached. A Service that joins through hand-written labels and has no `traefik.docker.network` gets `traefik.docker.network=relist-network`, because it is now on two networks and Traefik must pick the routable one. This supersedes ADR-0002's "every service" rule. The `env_file` injection still covers every Service; ADR-0015 adds per-Service variable sets beside it.

Each joining Service gets two aliases on `relist-network`, added beside any the operator wrote:
- **stable**, `<service>.<deploy project name>.internal`, e.g. `web.relist-c12.internal`. It never changes, by ADR-0005 and ADR-0011.
- **readable**, `<service>.<compose>.<environment>.<project>.internal` from the current names' slugs, e.g. `web.api.production.shop.internal`.

An alias is skipped with a deploy warning when a part has no DNS-safe characters (e.g. a Thai name, which would slugify to the fallback `x`), when a DNS label exceeds 63 characters or the name 253, or when two joining Services in one Compose slugify alike.

## Probe

On 2026-10-09 a probe ran two Composes on a throwaway external network. Each had a `web` with aliases and a `db` left on `default`. Results:
- the dotted `.internal` aliases resolved from the other Compose (musl `nslookup` and glibc `getent`);
- bare `db` from A's `web` resolved only to A's `db`;
- once B's `db` joined the shared network, bare `db` from A's `web` resolved to **B's** `db` on every lookup, not round-robin;
- `docker compose config -q` accepted the resolved shape (`null` entries, explicit `default`, `aliases`).

## Consequences

- **Every existing Compose changes networks on its next deploy.** A Service with no Domain, no Traefik labels and no `relist-network` in its YAML leaves the shared network. Anything that reached it from another Compose must add `relist-network` to that Service's `networks:` and should call it by alias.
- **Opting in brings the bare-name collision back** for that Service: the probe shows that another Compose's lookup of the same bare name can land on it. Call Services in other Composes by alias, never by bare name.
- **Custom networks partition as plain Compose does.** A Service whose YAML lists only `custom` no longer reaches `default`-network siblings over `relist-network`.
- **Traefik's file provider** can no longer reach a Service by name unless that Service opts in.
- **The readable alias follows renames.** After a Project, Environment or Compose rename it changes on the next deploy, and it can briefly duplicate another until both redeploy. Config should use the stable alias.
- **`network_mode` Services are no longer given networks.** This fixes the known issue recorded in ADR-0013.
