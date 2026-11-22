# Domains can be turned off

The only way to take a Domain (ADR-0013) offline was to delete it. Bringing it back meant entering the host, paths, Service, port, HTTPS settings, middlewares and Basic auth group again from memory.

**Decision:** a Domain has an on/off switch, stored as `domains.enabled` and on by default. A Domain that is off keeps every field and still holds its host for its Project (ADR-0032). Like every Domain change, the switch applies on the next deploy: the deploy writes no Traefik labels for an off Domain, so Traefik answers its host with its own `404`. The Service of an off Domain still joins `relist-network` with its `.internal` aliases (ADR-0014). The switch has its own endpoint, `PUT …/domains/{domainID}/enabled`, and is not a field of the Domain form's body. `ListForCompose` and `ListAll` keep returning every Domain. Each consumer that needs only the Domains that are on filters with `domains.Enabled`:
- label injection;
- the Compose policy;
- zero-downtime;
- the migration's maintenance page.

## Considered options

**Drop an off Domain everywhere, including network joins.** Rejected: a Service whose only Domain was turned off would leave `relist-network` and lose its aliases. Other Composes that reach it by alias would break, and nothing would say why. Turning public routing off should not cut internal access.

**Serve a "temporarily unavailable" page for an off Domain.** Rejected for now: it needs a standing container or service to answer those hosts, and is a feature of its own. Traefik's `404` is what a deleted Domain gives today.

**An `enabled` field on the existing Domain PUT.** Rejected: a body that omits the field would decode as `false` and turn the Domain off by accident.

**Apply the switch at once**, through Traefik's file provider or a deploy the panel starts. Rejected: ADR-0013 rules out the file provider for Domain routing, and starting a deploy on a switch would recreate containers the user did not ask to touch.

## Consequences

- A Service whose Domains are all off leaves the Zero-downtime card, and is recreated rather than rolled out on deploy. Its stored switch is kept and applies again once a Domain is on.
- Turning a Domain on checks that its Service can still be routed to and returns `422` if not. Turning it off never checks, so a Domain can be parked while its Service is out of the YAML.
- During a server migration the maintenance page covers only hosts of Domains that are on. The finalize summary still lists every host for DNS, since the host will come back.
