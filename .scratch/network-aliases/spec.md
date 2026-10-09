# Spec — Network aliases and a narrower relist-network

Date: 2026-10-09. Builds on Domains and Compose preview (branch `feat-compose-preview`).
Status: ready-for-agent

## Problem

The panel puts every Service of every Compose on the shared `relist-network` (ADR-0002). A Service
that has no `networks:` key gets **only** that network, so the Compose's own Services also talk to
each other over it. Docker Compose always registers the bare Service name as an alias on every
network a Service joins. Two Composes that each have a `db` therefore both answer to `db` on
`relist-network`, and an app can reach the other Compose's database.

There is also no unique name for calling a Service in another Compose.

## Behaviour

### Who joins relist-network

A Service without `network_mode` joins `relist-network` when at least one of these is true:

1. A Domain targets it.
2. It has a hand-written label whose key starts with `traefik.` (list or map form).
3. Its YAML already lists `relist-network` under `networks:`. This is how to opt in for calls
   from other Composes.

Every other Service is left on the networks its YAML gives it. With no `networks:` key, that means
the Compose's `default` network, so `db` stays private to its Compose. Services with
`network_mode` are never given networks. Every Service still gets the `env_file` injection.

When a joining Service had no `networks:` key, it gets `default` **and** `relist-network`, so it
can still reach its own Compose's Services. A list-form `networks:` is rewritten to map form so the
aliases can be attached; existing entries keep their values.

A Service that joins through rule 2 without a `traefik.docker.network` label gets
`traefik.docker.network=relist-network`. It now sits on two networks, and Traefik must pick the
routable one. An existing `traefik.docker.network` label is left alone; Domains already
overrides it on Services with a Domain.

The top-level `networks.relist-network: {external: true}` is always declared, as today.

### Aliases

Every Service that joins `relist-network` gets these aliases on that network. They are added to
any aliases the operator wrote, without duplicates.

- **Stable:** `<service>.<deploy project name>.internal`, e.g. `whoami.relist-c12.internal`. A
  legacy Compose uses its frozen name, e.g. `whoami.hello-production-test.internal`. This alias
  never changes (ADR-0005/0011).
- **Readable:** `<service>.<compose>.<environment>.<project>.internal` from the **current** names'
  slugs, e.g. `whoami.test.production.hello.internal`. A rename changes it on the next deploy.
  Sibling names are already unique by slug, so it is unique across the host.

Service names are slugified too. The readable alias is skipped, with a deploy warning, when any
part slugifies to the fallback `x` from a name that is not literally `x` (e.g. a Thai name), when a
DNS label exceeds 63 characters, or when the whole name exceeds 253. The stable alias is skipped
under the same length limits. Both show up in Compose preview.

### UI

The Deploy identity card gains **Internal hostname**. It shows the stable pattern
`<service>.<docker compose project>.internal`, which is not copyable because of the
placeholder, and one line on which Services get it. It points to Preview for each
Service's exact aliases, including the readable one. The readable pattern is not shown on the
card, because it needs the Go slug rules and a TypeScript copy would drift.

Two joining Services in one Compose whose names slugify alike (`my_db`, `my-db`) get no
aliases, with a warning. Opting a Service in brings the bare-name collision back for it: the
probe in ADR-0014 shows that another Compose's lookup of the bare name can land on it.

## Upgrade note (README)

After a redeploy, a Service with no Domain, no Traefik labels and no `relist-network` in its YAML
leaves `relist-network`. Anything that reached it from another Compose by bare name must add
`relist-network` to that Service's `networks:` and should call it by an alias instead.
ADR-0002's "every service" rule is superseded by ADR-0014.
