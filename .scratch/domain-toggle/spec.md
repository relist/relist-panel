# Spec — Turn a Domain off without deleting it

Date: 2026-11-23.
Status: open — design approved in chat, spec awaiting review

## Problem

The only way to take a Domain offline is to delete it. Bringing it back means
entering the host, path, internal path, Service, port, entrypoint, HTTPS and
certificate provider, middlewares and Basic auth group again from memory. Taking
a host down for a while is common (a staging site between test rounds, a
preview that should not be public this week). Today it costs a delete plus a
full re-entry.

## Goal and success criteria

- Each row on a Compose's Domains tab has a switch that turns the Domain off
  and on. Anyone who may edit Domains (Editor and up) may use it.
- A Domain that is off keeps every field. Turning it on again restores exactly
  what it routed before.
- Turning a Domain off or on applies on the **next deploy**, like every other
  Domain change (ADR-0013). Toggling stamps `domains_changed_at`, so the existing
  "Domain changes apply on the next deploy." banner shows.
- After the deploy, a request to a Domain that is off gets Traefik's own `404`.
  The panel writes no router for it.
- A Domain that is off still holds its host for its Project. No other Project
  may take the host meanwhile.
- A Service whose only Domains are off stays on `relist-network` and keeps its
  `.internal` aliases. Turning public routing off does not cut other Composes'
  access to it.

## Out of scope

- Applying the switch without a deploy (Traefik's file provider, or a deploy
  started for the user). ADR-0013's "a Domain change applies on the next deploy"
  stands.
- A "temporarily unavailable" page or any response other than Traefik's `404`.
- Turning Domains off in bulk, or per Compose / Environment.
- Scheduling (off until a date).
- A switch in the Add / Edit form. New Domains start on, and an edit never
  changes the switch.
- Letting Operators toggle. It stays an Editor action, like the rest of the tab.

## Domain changes

The **Domain** entry in `CONTEXT.md` gains:

> A Domain may be turned off: it keeps its fields and its host, and from the
> next deploy Traefik no longer routes it (ADR-0052).

New ADR `docs/adr/0052-domains-can-be-turned-off.md` records three decisions and
the options rejected:

1. A Domain that is off still counts as "has a Domain" for joining
   `relist-network` and getting `.internal` aliases (ADR-0014). The rejected
   option was dropping it everywhere. That would silently break other Composes
   that reach the Service by alias.
2. A Domain that is off is answered by Traefik's `404`. The rejected option was
   a maintenance page, which needs a standing container or service and is a
   separate feature.
3. The switch has its own endpoint and is not a field of `Input`. The rejected
   option was an `enabled` field on the existing PUT, where a body without the
   field would decode as `false` and turn the Domain off by accident.

## Data model

New migration `internal/migrations/00057_domain_enabled.sql` (renumber if another
branch takes 00057 first; never edit a committed migration):

```sql
-- Domains can be turned off (.scratch/domain-toggle/spec.md, ADR-0052).
-- Every existing Domain stays on.

-- +goose Up
ALTER TABLE domains ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE domains DROP COLUMN enabled;
```

`domains.Domain` gains `Enabled bool \`json:"enabled"\``, read in `domainColumns`
and `scanDomain`. `domains.Input` does **not** change. `create` inserts with the
column default (on). `update` does not touch the column.

## API

```
PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/domains/{domainID}/enabled
Body: {"enabled": true | false}
200:  the Domain, as GET lists it
```

- **Authz:** `internal/authz/routes.go` gives it `LevelEditor`.
  `internal/authz/actions.go` names it `domain.toggle`. The handler adds
  `audit.Detail(ctx, "host", host)` and `audit.Detail(ctx, "enabled", <bool>)`.
  The action map is keyed by route, not by body, so one action carries both
  directions. This is the same as the Zero-downtime switch (`zero_downtime.update`).
- **Write:** in `Store.inTx`, so `domains_changed_at` is stamped in the same
  transaction. Setting a Domain to the value it already has still stamps it.
  That is harmless and keeps the handler simple.
- **Errors:**
  - `400` when the body is not `{"enabled": <bool>}`.
  - `404` when the Domain is not in this Compose (`ErrNotFound`).
  - `422` when turning **on** a Domain whose Service is not in
    `DeclaredServices(yamlContent)`, with message
    `service "<name>" is not a service of this compose that can be routed to; edit the domain first`.
    That is the same Service check `Validate` runs. Without it the next deploy
    would fail with a `TargetError`. Turning **off** never runs the check, so a
    Domain can be parked while its Service is out of the YAML.
- The existing PUT keeps validating the Service, so editing a parked Domain
  whose Service is gone means choosing an existing Service. This is accepted.

## Who reads which Domains

`Store.ListForCompose` and `Store.ListAll` keep returning every Domain, on and
off. Each consumer filters on its own, so none of them silently loses a
Domain. Add `domains.Enabled(ds []Domain) []Domain` (keeps order) for the
consumers that want only the Domains that are on.

| Consumer | Domains it sees | Why |
|---|---|---|
| `domains.Inject` in `resolveCompose` (deploy and preview) | on only | No router, service or middleware labels for an off Domain → Traefik `404`. |
| `domains.TargetError` (missing / `network_mode` Service) | on only | It is raised inside `Inject`. A parked Domain must not block deploys. |
| `sharedNetworkJoins` in `resolveCompose` | all | The Service keeps `relist-network` and its aliases (ADR-0052 decision 1). |
| `policyTraefik` (`domains.Generated`) | on only | It must match what `Inject` writes, or the policy would allow labels the deploy does not write. |
| `zeroDowntimeServices`, `servicesWithDomain` | on only | A Rollout needs a router to move traffic. A Service whose Domains are all off leaves the Zero-downtime card and is recreated on deploy. Its stored switch is kept and applies again once a Domain is on. |
| `hostFree` (host belongs to one Project) | all | An off Domain still holds the host (ADR-0032 rule unchanged). |
| `migration` `ensureMaintenance` / `MaintenanceLabels` | on only | A host that served `404` before the move must not show the maintenance page during it. If none are on, no maintenance container starts. |
| `migration` finalize `summary.DNS` | all | The host still belongs to the install and will come back. The operator should point its DNS at the new host too. |
| `requestmetrics` host check | all | History of an off host stays viewable. |

`resolveCompose` currently passes one `ds` to both `sharedNetworkJoins` and
`Inject`. It keeps taking all Domains and passes `domains.Enabled(ds)` to
`Inject` itself, so the split lives in one place and `resolveForDeploy` stays
unchanged.

## UI

`web/` uses antd 6.5.1. Before changing anything, read
`https://ant.design/llms.txt` and the Switch and Table pages (AGENTS.md).
Antd's guidance is to use Switch for a change that saves at once and Checkbox
for a value saved by a later submit. The switch saves at once and the banner
says when it reaches traffic, so Switch is the right control.

**Domains tab** (`web/src/components/ComposeDomainsTab.tsx`):

- A new first column titled **On** holds `<Switch size="small" checked={domain.enabled}>`
  with `aria-label` "Turn off `<address>`" when on and "Turn on `<address>`" when off.
- While the request runs, that row's switch shows `loading` and ignores
  further clicks. Other rows stay usable.
- On success the tab refetches the Domains (as Edit and Delete do) and the
  Compose (so `domainsChangedAt` updates and `RedeployBanner` appears).
- On failure the switch shows the stored value again and a closable
  `error` `Alert` above the card shows the server's message. It is the same
  pattern as `deleteError`, and may share that state as one "row action" error.
- **A row that is off:**
  - the address is `Text type="secondary"`, not a link, since it would
    open a `404`;
  - a `Tag` "Off" follows the address, before the Basic auth tag;
  - Edit and Delete work as before.
- The Add / Edit modal does not change.

**Data layer:**
- `Domain` in `web/src/data/types.ts` gains `enabled: boolean`.
- `DomainInput` does not change.
- A `setDomainEnabled(composeId, domainId, enabled)` call sits beside
  `updateDomain` / `deleteDomain`.

**View rules:** the switch label, whether the address is a link and the
"Off" tag go in `web/src/pages/domains-view.ts` as pure functions, tested in
`domains-view.test.ts`.

`docs/GUIDELINES.md`, **Domains tab** paragraph: add the On column, the off-row
look, and that the switch, like Delete, needs no confirm because nothing on the
host changes until the next deploy.

The tab is already wrapped in `editorOnly` on `ComposePage`, so non-Editors never
see the switch. No extra role check is needed in the component.

## Testing

**Go, `internal/domains`:**
- `Enabled` keeps only the Domains that are on, in order.
- A store toggle flips `enabled`, stamps `domains_changed_at`, and returns
  `ErrNotFound` for a Domain of another Compose.
- `create` starts on. `update` keeps whatever the switch was.
- `hostFree` still refuses another Project a host whose only Domain is off.
- HTTP: `PUT …/enabled` returns 200 with `enabled` flipped. A bad body returns
  400. Turning on with the Service gone returns 422. Turning off with the
  Service gone returns 200.

**Go, `internal/composes`:**
- `resolveCompose` with one off Domain writes no `traefik.*` labels for it.
  The Service still joins `relist-network` with both `.internal` aliases.
- An off Domain on a missing Service does not fail the resolve.
- `zeroDowntimeServices` and `servicesWithDomain` skip a Service whose Domains
  are all off.
- The policy's allowed Traefik labels match the injected ones with an off
  Domain present.

**Go, `internal/migration`:**
- `ensureMaintenance` covers only Domains that are on, and starts nothing when
  all are off.
- Finalize `DNS` lists the host of an off Domain.

**Go, `internal/authz`:** the new route is Editor-level and maps to
`domain.toggle`. The existing route/action completeness tests must pass.

**Frontend (Vitest):**
- `domains-view` rules.
- `ComposeDomainsTab`: an off row renders secondary text and the Off tag.
  Toggling calls `setDomainEnabled` and refetches. A failed toggle shows the
  Alert and the old value.

**Live check, then e2e:** run the check by hand first through `task e2e:serve`
("Running a manual check" in `docs/agents/live-checks.md`). Once it passes, add
`e2e/tests/domain-toggle.spec.ts`, modelled on `basic-auth.spec.ts`:
1. Deploy a Compose with `traefik/whoami` behind a Domain. Its host answers
   `200`.
2. Turn the Domain off. The tab shows the redeploy banner. Deploy. The host
   answers `404`.
3. From a second Compose's container on `relist-network`, the whoami
   Service's `.internal` alias still answers.
4. Turn it on, deploy, and the host answers `200` again.
5. While it is off, creating a Domain with the same host in another Project
   is refused (`409`).

## Compatibility

- The migration only adds a column with a default, so a live install keeps
  routing every Domain until someone turns one off.
- An older frontend bundle never calls the new endpoint and ignores the new
  field. No release note is needed beyond the feature itself.
