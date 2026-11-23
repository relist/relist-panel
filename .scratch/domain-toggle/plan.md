# Domain Toggle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Editor can turn a Domain off and on from the Domains tab without deleting it. From the next deploy, Traefik stops routing a Domain that is off.

**Architecture:** A new `domains.enabled` column defaults to on. A separate
`PUT …/domains/{domainID}/enabled` endpoint writes it through `Store.inTx`, so
the redeploy banner fires. `ListForCompose` / `ListAll` keep returning every
Domain. Each consumer either filters with the new `domains.Enabled` (Inject,
policy, zero-downtime, maintenance page) or keeps all of them (network joins,
host ownership, DNS list, metrics, Registry host, Basic auth in-use). The
frontend adds an **On** switch column.

**Tech Stack:** Go, SQLite with goose SQL migrations, React with antd 6.5.1 and vitest, Playwright e2e.

**Spec:** `.scratch/domain-toggle/spec.md`. Read it with this plan. Its "Who reads which Domains" table is the contract for Tasks 3 and 4.

## Global Constraints

- Never edit a committed migration. The new one is `internal/migrations/00057_domain_enabled.sql`. If `00057` is taken on `main` when you start, use the next free number. Copy the header comment and `-- +goose Up` / `-- +goose Down` layout from `00056_session_hardening.sql`.
- `domains.Input` does **not** gain `enabled`. Create inserts with the column default. Update never writes the column.
- The toggle route is `PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/domains/{domainID}/enabled` with body `{"enabled": <bool>}` and response `200` and the Domain. It is `LevelEditor` and audit action `domain.toggle`, with details `host` and `enabled`.
- The toggle's error responses:
  - `400` `request must contain {"enabled": true|false}` when the body is not exactly a bool field.
  - `404` when the Domain is not in this Compose.
  - `422` `service "<name>" is not a service of this compose that can be routed to; edit the domain first` when turning on a Domain whose Service is not in `DeclaredServices`.
- A Domain that is off gets no `traefik.*` labels. Its Service still joins `relist-network` with both `.internal` aliases.
- UI copy, verbatim:
  - column title `On`;
  - switch `aria-label` `Turn off <address>` / `Turn on <address>`;
  - tag `Off`.
- Before any UI work, read `https://ant.design/llms.txt` and the Switch, Table, Tag and Typography pages, and follow `docs/GUIDELINES.md`.
- Commit messages follow Conventional Commits with no scope (`docs/agents/commit-conventions.md`) and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Go tests: `go test ./internal/...`. Frontend: `cd web && pnpm test` and `pnpm typecheck`.

## Review Focus

1. **One Service with one Domain on and one off.** The on Domain still gets its router, and zero-downtime still applies to the Service. Test in Task 3.
2. **Editing a Domain that is off** through the existing PUT leaves it off. Test in Task 2.
3. **A Basic auth group used only by a Domain that is off** still cannot be deleted (`409`). Test in Task 2.
4. **Double-clicking the switch** sends one request, since the row's switch is `loading` and ignores clicks. Test in Task 5.
5. **Turning a Domain on after its Service moved to `network_mode`** returns `422`, because `DeclaredServices` excludes such Services. Test in Task 2.

---

### Task 1: Column, field and `Enabled` helper

**Files:**
- Create: `internal/migrations/00057_domain_enabled.sql`
- Modify: `internal/domains/domains.go` (the `Domain` struct; add `Enabled`)
- Modify: `internal/domains/store.go` (`domainColumns`, `scanDomain`)
- Test: `internal/domains/domains_test.go`, `internal/httpapi/domains_test.go`

**Interfaces:**
- Produces:
  - `Domain.Enabled bool` with JSON tag `enabled`;
  - `func Enabled(ds []Domain) []Domain` in `internal/domains/domains.go`, which returns the Domains that are on, in input order, and a non-nil empty slice for none.

- [ ] **Step 1: Write the failing tests**
  - `TestEnabledKeepsOrderAndDropsOff` in `domains_test.go`:
    - input IDs 1 (on), 2 (off), 3 (on) → IDs `[1 3]`;
    - all off → `len == 0` and `!= nil`.
  - `TestDomainsStartOn` in `httpapi/domains_test.go`: `createDomain` → the POST response and the GET `/domains` list both carry `"enabled":true`.
- [ ] **Step 2: Run** `go test ./internal/domains/ ./internal/httpapi/ -run 'TestEnabledKeepsOrder|TestDomainsStartOn'` → FAIL (undefined `Enabled`, no `enabled` in JSON).
- [ ] **Step 3: Implement.**
  - Write the migration: `ALTER TABLE domains ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;`, and in Down `ALTER TABLE domains DROP COLUMN enabled;`.
  - Append `d.enabled` to `domainColumns` and scan it as an int, like `https`.
  - Add `Enabled`.
- [ ] **Step 4: Run** `go test ./internal/...` → PASS. Existing tests that build `Domain` literals pass `Enabled: false` implicitly. That is fine until Task 3 starts filtering. Task 3 updates those fixtures.
- [ ] **Step 5: Commit** `feat: store whether a domain is on`

### Task 2: Toggle endpoint

**Files:**
- Modify: `internal/domains/store.go`
  - `Store.setEnabled`;
  - `Service.toggle` handler;
  - `RegisterRoutes`.
- Modify: `internal/authz/routes.go`, `internal/authz/actions.go`
- Test: `internal/httpapi/domains_test.go`; authz tests already enforce route coverage (`TestEveryRouteIsClassified`, `TestEveryChangeHasAnAction`)

**Interfaces:**
- Consumes: `Domain.Enabled` (Task 1).
- Produces:
  - `func (s *Store) setEnabled(ctx context.Context, composeID, id int64, enabled bool) (Domain, error)`. It runs in `inTx`, so `domains_changed_at` is stamped even when the value is unchanged, and returns `ErrNotFound` via `requireAffected`.
  - A route `PUT base+"/{domainID}/enabled"`.
  - Test helper `func (f domainsFixture) setEnabled(t *testing.T, id int64, enabled bool) (*http.Response, []byte)` in `httpapi/domains_test.go`, reused by Task 3.

- [ ] **Step 1: Write the failing tests** in `httpapi/domains_test.go`:
  - `TestDomainToggle`:
    - off → `200`, `enabled:false`, and GET agrees;
    - on → `200`, `enabled:true`;
    - every toggle moves `domainsChangedAt` (backdate as `TestDomainsChangedAtMovesOnEveryChange` does), including off→off.
  - `TestDomainToggleBadBody`: body `{}`, `{"enabled":"yes"}` and `not json` → `400`.
  - `TestDomainToggleOtherCompose`: a Domain ID of a sibling Compose → `404`.
  - `TestDomainToggleOnNeedsService`:
    - off, then save YAML without `web` → turning off again is `200`, turning on is `422` with the exact message from Global Constraints;
    - save YAML where `web` has `network_mode: host` → turning on is still `422` (Review Focus 5).
  - `TestDomainEditKeepsItOff`: off, then PUT the full body with a new port → `enabled:false` (Review Focus 2).
  - `TestDomainOffStillHoldsItsHost`: off in project `shop`, then create the same host in project `blog` → `409`.
  - `TestAuthGroupInUseByOffDomain`: a group on a Domain that is off → `DELETE /api/auth-groups/{id}` is `409`. Follow `TestDomainsBasicAuthGroup` for setup (Review Focus 3).
  - Audit: after a toggle, the audit list holds action `domain.toggle` with details `host` and `enabled`. Follow how existing httpapi tests read audit rows.
- [ ] **Step 2: Run** `go test ./internal/httpapi/ -run 'TestDomainToggle|TestDomainEditKeepsItOff|TestDomainOffStillHolds|TestAuthGroupInUseByOff'` → FAIL (`404`/`405` from the mux).
- [ ] **Step 3: Implement.**
  - The handler:
    - parses the route with `parseRoute(w, r, true)`;
    - decodes into `struct{ Enabled *bool \`json:"enabled"\` }` with `DisallowUnknownFields`, and treats `nil` as `400`;
    - when turning on, loads the Domain with `get`, loads the YAML with `composeYAML`, and checks the Service with `contains(DeclaredServices(yaml), d.Service)`;
    - calls `setEnabled`;
    - sets the audit details;
    - errors through `writeStoreError`.
  - Add to `routes.go`: `"PUT " + compose + "/domains/{domainID}/enabled": {Level: LevelEditor}`.
  - Add to `actions.go`: `"domain.toggle"`.
- [ ] **Step 4: Run** `go test ./internal/...` → PASS, including the authz coverage tests.
- [ ] **Step 5: Commit** `feat: turn a domain off and on`

### Task 3: Deploy path ignores Domains that are off

**Files:**
- Modify:
  - `internal/composes/composes.go`: `resolveCompose`, which passes `domains.Enabled(ds)` to `Inject` and all `ds` to `sharedNetworkJoins`;
  - `internal/composes/deploy.go`: `resolveForDeploy`, which passes `domains.Enabled(composeDomains)` to `zeroDowntimeServices`;
  - `internal/composes/policy.go`: `policyTraefik`, which calls `Generated(…, domains.Enabled(ds), …)`;
  - `internal/composes/zerodowntime_http.go`: `servicesWithDomain`, which ranges over `domains.Enabled(ds)`.
- Modify: `CONTEXT.md` (**Domain** entry, the sentence from the spec's "Domain changes")
- Create: `docs/adr/0052-domains-can-be-turned-off.md` (the three decisions and rejected options from the spec)
- Test:
  - `internal/composes/networks_test.go`
  - `internal/composes/zerodowntime_test.go`
  - `internal/composes/zerodowntime_http_test.go`
  - `internal/composes/policy_test.go`
  - `internal/httpapi/domains_test.go`

**Interfaces:**
- Consumes: `domains.Enabled` (Task 1), `domainsFixture.setEnabled` (Task 2).

- [ ] **Step 1: Update fixtures.**
  - Every `domains.Domain{…}` literal in `internal/composes` tests that is meant to route gets `Enabled: true`. Start with `webDomain()` in `networks_test.go`.
  - `seedDomain` in `zerodowntime_test.go` already gets the column default, so it needs no change.
  - Add `seedDomainOff(t, db, service)`, which inserts with `enabled = 0`.
- [ ] **Step 2: Write the failing tests:**
  - `networks_test.go` `TestOffDomainWritesNoTraefikLabelsButKeepsAliases`: `webDomain()` with `Enabled: false` →
    - `web` has no label key starting `traefik.`;
    - `sharedAliases(web)` equals `[web.relist-c12.internal web.test.production.hello.internal]`.
  - `networks_test.go` `TestOffDomainOnMissingServiceDoesNotFail`: an off Domain with `Service: "gone"` → `resolveCompose` returns no error.
  - `networks_test.go` `TestOnAndOffDomainsOnOneService` (Review Focus 1): Domains ID 3 on and ID 4 off on `web` → `relist-c12-d3` labels present, no `relist-c12-d4` label.
  - `zerodowntime_test.go` `TestCaptureSkipsServiceWhoseDomainsAreOff`:
    - `seedDomainOff(web)`, `switchOn(web)` → `Snapshot.ZeroDowntime` empty and no `retry.attempts` in the YAML;
    - adding `seedDomain(web)` (on) as well → `ZeroDowntime == [web]`.
  - `zerodowntime_http_test.go` `TestZeroDowntimeHTTPListSkipsOffDomain`: the only Domain off → the list does not contain `web`.
  - `policy_test.go`: with an off Domain, the policy's allowed Traefik labels for `web` are empty. Use whatever seam the existing policy tests use to read them.
  - `httpapi/domains_test.go` `TestDeploySkipsOffDomain`:
    - create, turn off, `deployOK`;
    - `resolvedFile` lacks `relist-c<id>-d<id>` and still contains `relist-network`;
    - then turn it on, deploy, and the router is back.
- [ ] **Step 3: Run** `go test ./internal/composes/ ./internal/httpapi/` → the new tests FAIL.
- [ ] **Step 4: Implement** the four filter points listed under Files. Then write the `CONTEXT.md` sentence and the ADR, modelled on `0036`'s layout.
- [ ] **Step 5: Run** `go test ./internal/...` → PASS.
- [ ] **Step 6: Commit** `feat: leave domains that are off out of deploys`

### Task 4: Migration: maintenance page and DNS list

**Files:**
- Modify: `internal/migration/lock.go`. In `ensureMaintenance`, use `ds = domains.Enabled(ds)` before the `len(ds) == 0` check.
- Leave `internal/migration/finalize.go` unchanged (DNS keeps every host). Add only a one-line comment naming why.
- Test: `internal/migration/lock_test.go`, `internal/migration/finalize_test.go`

**Interfaces:**
- Consumes: `domains.Enabled` (Task 1).

- [ ] **Step 1: Write the failing tests.**
  - In `lock_test.go`, `httpsDomain` and the other fixtures that should route get `Enabled: true`.
  - `TestMaintenanceSkipsOffDomains`:
    - Domains `[on a.example.com, off b.example.com]` → after `Start`, the labels passed to `RunMaintenance` mention `a.example.com` and not `b.example.com`. Read them from `FakeDocker`'s recorded call, or add a field there if none exists.
    - All off → `MaintenanceUp` stays false.
  - `finalize_test.go` `TestFinalizeDNSListsOffDomains`: `fakeDomains{{Host: "off.example.com"}}` (Enabled false) → `summary.DNS == [off.example.com]`.
- [ ] **Step 2: Run** `go test ./internal/migration/` → `TestMaintenanceSkipsOffDomains` FAILS. The DNS test passes already, which pins current behaviour.
- [ ] **Step 3: Implement** the filter in `ensureMaintenance`.
- [ ] **Step 4: Run** `go test ./internal/...` → PASS.
- [ ] **Step 5: Commit** `feat: keep the maintenance page off hosts whose domain is off`

### Task 5: Frontend switch

**Files:**
- Modify: `web/src/data/types.ts`. `Domain` gains `enabled: boolean`. `DomainInput` does not change.
- Modify: `web/src/data/api.ts`. Add `setDomainEnabled: (location: ComposePath, domainId: number, enabled: boolean) => request<Domain>(\`${domainsPath(location)}/${domainId}/enabled\`, jsonInit('PUT', { enabled }))`.
- Modify: `web/src/data/store.tsx`. Add `setDomainEnabled(composeId, domainId, enabled): Promise<Domain>`, tracked as `domain-toggle:${composeId}:${domainId}`, calling `refreshStatus(composeId)` like `updateDomain`, and listed in the store's type next to `deleteDomain`.
- Modify: `web/src/pages/domains-view.ts`:
  - `domainSwitchLabel(domain: Domain): string` returns `Turn off <address>` / `Turn on <address>`, using the address the tab already builds. Move or reuse the `address` helper so both use one.
  - `domainLinked(domain: Domain): boolean` returns `domain.enabled`.
- Modify: `web/src/components/ComposeDomainsTab.tsx`:
  - the `On` column first;
  - per-row toggling state;
  - the secondary text and `Off` tag;
  - the shared row-action error `Alert` (rename `deleteError` to `rowError`).
- Modify: `docs/GUIDELINES.md`, **Domains tab** paragraph. Add the On column, the off-row look, and that the switch needs no confirm because nothing changes until the next deploy.
- Test:
  - `web/src/pages/domains-view.test.ts`
  - `web/src/components/ComposeDomainsTab.test.tsx`. Add `setDomainEnabled: vi.fn()` to the hoisted `panel`, and `enabled: true` to the `domain` fixture.

**Interfaces:**
- Consumes: the endpoint from Task 2 and its response shape (`Domain` with `enabled`).

- [ ] **Step 1: Read** antd `llms.txt` and the Switch, Table, Tag and Typography pages (Global Constraints).
- [ ] **Step 2: Write the failing tests.**
  - `domains-view.test.ts`:
    - `domainSwitchLabel` gives `Turn off api.example.com/v1` for on and `Turn on api.example.com/v1` for off;
    - `domainLinked` is false for off.
  - `ComposeDomainsTab.test.tsx`:
    - **an off row:** no link named `api.example.com/v1`, and the text plus an `Off` tag are present;
    - **clicking the switch:** clicking `Turn off api.example.com/v1` calls `setDomainEnabled(composeId, 3, false)`, then `loadDomains` again;
    - **double-click (Review Focus 4):** with `setDomainEnabled` pending (an unresolved promise), a second click does not call it again;
    - **failure:** `setDomainEnabled` rejects with `ApiError(422, 'service "web" is not …')` → an error `Alert` shows the message, and the switch is still checked as before.
- [ ] **Step 3: Run** `cd web && pnpm test domains-view ComposeDomainsTab` → FAIL.
- [ ] **Step 4: Implement.**
  - Switch: `<Switch size="small" checked={domain.enabled} loading={toggling.has(domain.id)} onChange={…} aria-label={domainSwitchLabel(domain)} />`.
  - `onChange` returns early when the row is already toggling.
  - Then update `docs/GUIDELINES.md`.
- [ ] **Step 5: Run** `pnpm test` and `pnpm typecheck` → PASS.
- [ ] **Step 6: Commit** `feat: add an on switch to each domain`

### Task 6: Live check and e2e

**Files:**
- Create: `e2e/tests/domain-toggle.spec.ts`

**Interfaces:**
- Consumes: everything above. Use `e2e/lib/sandbox.ts` (`Panel`, `call`, `runId`, `sandbox`, `traefikUrl`) and a local `docker(...)` helper, as `e2e/tests/databases-security.spec.ts` defines one.

- [ ] **Step 1: Live check by hand.**
  - Run `task e2e:serve`, then read ports and credentials from `.data/e2e/env` and don't guess them.
  - Follow "Running a manual check" in `docs/agents/live-checks.md`.
  - Check each of the spec's e2e steps 1–5 in the browser and with curl, including the switch, the Off tag and the redeploy banner.
  - Ask before `task e2e:down`.
- [ ] **Step 2: Write the spec.** Use `test.describe.configure({ mode: 'serial' })`, with hosts `dt-${id}.localhost`. Each step checks:
  1. Deploy `traefik/whoami` behind a Domain → `200`.
  2. Turn the Domain off (`PUT …/enabled`), then deploy → `404`.
  3. A second Compose `busybox` (`command: ["sleep","infinity"]`, `networks: [relist-network]`), deployed → `docker exec <its container> wget -qO- http://whoami.<compose>.<environment>.<project>.internal` contains `Hostname:`. Build the readable alias with the slug rules (lowercase).
  4. Turn it on, deploy → `200`.
  5. While off, `POST` the same host to a Compose in another Project → `409`.
- [ ] **Step 3: Run** `task e2e -- domain-toggle` → PASS.
- [ ] **Step 4: Commit** `test: cover turning a domain off end to end`
