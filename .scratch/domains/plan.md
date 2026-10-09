# Domains — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Domains tab to each Compose. Its domains turn into Traefik labels injected at deploy, and the install's Traefik gains an always-on `websecure` entrypoint with a Let's Encrypt resolver.

**Architecture:** A new domain package `internal/domains` owns the `domains` table, the validation, the four CRUD routes, and a pure label injector. `internal/composes` loads a Compose's domains in `prepareDeployFiles` and passes them into `resolveCompose`. The injector runs there, next to the existing `relist-network`/`env_file` injection (ADR-0002). `domains` queries `composes`/`environments`/`projects` with SQL directly, the same way `composes` checks its parents, so `domains` never imports `composes`.

**Tech Stack:** Go 1.25 stdlib `net/http`, `gopkg.in/yaml.v3`, goose migrations, SQLite; React 19, antd 6.5.1; vitest.

**Spec:** [.scratch/domains/spec.md](spec.md)

## Global Constraints

- Commits: Conventional Commits, no scope. **No** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md).
- Traefik object names: `relist-c<composeID>-d<domainID>`, plus the suffixes `-http`, `-redirect`, `-strip` and `-prefix`. The name never depends on identity version or names.
- The network name always comes from `dockernet.NetworkName`. Never write the literal anywhere else in Go.
- Resolver name for Let's Encrypt: `letsencrypt`. Default entrypoints: `web`, `websecure`.
- Copy, verbatim from the spec:
  - Deploy refusal: `domain <host><path> points at service "<svc>", which is not in the compose file`.
  - Conflict: `<host><path> is already used by <project> / <environment> / <compose>`.
  - Banner: `Domain changes apply on the next deploy.`
  - Empty state: `No domains. Add one to route a Service through Traefik.`
  - Custom resolver help: `Must match a certificate resolver in Traefik's static config. The panel can't check it — a wrong name shows only in Traefik's log.`
  - None help: `Traefik serves a certificate matching this host if it has one, otherwise its default self-signed certificate.`
- Domain words come from CONTEXT.md: Compose, Service, Domain. Never "stack" or "app".
- Before any edit under `web/`, read `https://ant.design/llms.txt` and the pages for `Modal`, `Form`, `Switch`, `Select`, `InputNumber`, `Table`, `Alert` and `Popconfirm`, read `docs/GUIDELINES.md`, and check props against antd 6.5.1 (AGENTS.md).
- Behaviour that needs real Traefik or Let's Encrypt is verified manually (Task 7), never through fakes.

## Review Focus

- **Labels written as YAML scalars of other types**, e.g. map form `traefik.enable: false` (a bool) or `...port: 3000` (an int). Detecting `enable=false` must treat a bool `false` like the string `"false"`. Overwritten values are written as strings. Test in Task 1.
- **A list label with no `=`** (`- traefik.enable`) means an empty value in Compose. It must neither crash nor be duplicated: the injector replaces it by key. Test in Task 1.
- **Host typed with surrounding spaces, uppercase or a trailing dot** (` API.Example.com.`). Spaces are trimmed and case is lowered. The trailing dot is rejected on `host` (400) rather than stored. Test in Task 2.
- **Re-saving a domain with its own unchanged host+path** must succeed (`200`), not `409` against itself. Test in Task 2.
- **Saving a YAML edit that removes a domain's Service**, then deploying: the deploy answers `400` with the refusal copy, the runner's `Up` is never called, and the previously written resolved file is left unchanged. Test in Task 3.

## File Structure

- Create `internal/migrations/00013_domains.sql`: the table and the `composes.domains_changed_at` column, exactly as the spec's Data model.
- Create `internal/domains/domains.go`: `Domain`, `Input`, `FieldError`, `Validate`, `DeclaredServices`, `RouterName`, `Address`.
- Create `internal/domains/labels.go`: `Inject`, `TargetError`.
- Create `internal/domains/store.go`: `Store` (SQL) and the HTTP `Service`, plus `RegisterRoutes`.
- Create `internal/domains/labels_test.go` and `internal/domains/domains_test.go`: pure tests (package `domains_test`).
- Create `internal/httpapi/domains_test.go`: HTTP-seam tests reusing `newTestServer`, `setupAndLogin`, `createProject`, `productionEnvironment` and `createCompose` (`metrics_test.go:146`).
- Modify `internal/composes/composes.go`: `resolveCompose`, `prepareDeployFiles`, the deploy responses, `Compose.DomainsChangedAt`, `scanComposeValues` and the two `SELECT`s at lines 951 and 992.
- Modify `internal/httpapi/httpapi.go`: register `domains.New(db)`.
- Modify `docker-compose.yml`, `README.md`, `CONTEXT.md`, `docs/adr/0001-v1-scope-and-stack.md`; create `docs/adr/0013-panel-managed-domains.md`.
- Modify `web/src/data/types.ts`, `api.ts`, `api.test.ts`, `store.tsx`.
- Create `web/src/pages/domains-view.ts` and `domains-view.test.ts`.
- Create `web/src/components/ComposeDomainsTab.tsx` and `web/src/components/DomainFormModal.tsx`.
- Modify `web/src/pages/compose-view.ts` (and its test) and `web/src/pages/ComposePage.tsx`.
- Modify `docs/GUIDELINES.md` §4.6.

---

### Task 1: Pure label injection

**Files:**
- Create: `internal/domains/domains.go` (types and `RouterName` only in this task), `internal/domains/labels.go`, `internal/domains/labels_test.go`

**Interfaces:**
- Produces (package `domains`):
  ```go
  type Domain struct {
      ID           int64     `json:"id"`
      ComposeID    int64     `json:"-"`
      Host         string    `json:"host"`
      Path         string    `json:"path"`
      InternalPath string    `json:"internalPath"`
      StripPath    bool      `json:"stripPath"`
      Service      string    `json:"service"`
      Port         int       `json:"port"`
      Entrypoint   string    `json:"entrypoint"`
      HTTPS        bool      `json:"https"`
      CertProvider string    `json:"certProvider"` // "none" | "letsencrypt" | "custom"
      CertResolver string    `json:"certResolver"`
      Middlewares  []string  `json:"middlewares"` // never nil on the wire
      CreatedAt    time.Time `json:"createdAt"`
      UpdatedAt    time.Time `json:"updatedAt"`
  }
  func RouterName(composeID, domainID int64) string          // "relist-c12-d3"
  func (d Domain) Address() string                           // host+path, e.g. "api.example.com/" or "api.example.com/api"
  // TargetError refuses a deploy: the domain's Service is missing or sets network_mode.
  type TargetError struct{ Domain Domain; Reason string }     // Error() is the spec copy
  // Inject adds every domain's labels to its Service in services (the YAML
  // "services" map, mutated in place) and returns warnings in a stable order.
  // Domains are processed in ID order.
  func Inject(services map[string]any, composeID int64, domains []Domain, network string) ([]string, error)
  ```
- `TargetError.Error()`:
  - Missing Service: `domain <Address> points at service "<svc>", which is not in the compose file`.
  - `network_mode`: `domain <Address> points at service "<svc>", which sets network_mode and cannot take a domain`.
- Warning copy:
  - `service "<svc>": traefik.enable=false was overridden to true because a domain points at it`
  - `service "<svc>": traefik.docker.network was "<old>", overridden to <network>`
  - `service "<svc>": router "<r>" has no traefik.http.routers.<r>.service label, so it now routes to a domain's service; add one`
  - `router "<r>" also matches Host(` + "`<host>`" + `), which a domain manages`

- [ ] **Step 1: Write the failing tests** in `labels_test.go`. Table-driven. Each case parses a YAML `services:` snippet, calls `Inject(services, 12, ds, "relist-network")`, and asserts on the resulting label set, read back in the same shape (a list or a map):
  - `TestInjectHTTPOnly`: domain `{ID:3, Host:"a.example.com", Path:"/", Service:"web", Port:3000}` yields `traefik.enable=true`, `traefik.docker.network=relist-network`, ``traefik.http.routers.relist-c12-d3.rule=Host(`a.example.com`)``, `…entrypoints=web`, `…service=relist-c12-d3`, `traefik.http.services.relist-c12-d3.loadbalancer.server.port=3000`, and no `tls` key, no `-http` router and no `middlewares` key.
  - `TestInjectPathPrefix`: Path `/api` makes the rule ``Host(`a.example.com`) && PathPrefix(`/api`)``.
  - `TestInjectHTTPSLetsEncrypt`: entrypoints `websecure`, `tls=true`, `tls.certresolver=letsencrypt`. Router `relist-c12-d3-http` has the same rule, entrypoints `web`, `middlewares=relist-c12-d3-redirect` and `service=relist-c12-d3`. Also `redirectscheme.scheme=https` and `permanent=true`.
  - `TestInjectHTTPSCustomAndNone`: custom adds `tls.certresolver=myresolver`; none adds `tls=true` with no certresolver key.
  - `TestInjectCustomEntrypoint`: Entrypoint `internal` with HTTPS gives entrypoints `internal` and no `-http` router.
  - `TestInjectMiddlewareChain`: Path `/api`, StripPath, InternalPath `/v1`, Middlewares `["auth@file"]`. Router middlewares are exactly `relist-c12-d3-strip,relist-c12-d3-prefix,auth@file`. Also `stripprefix.prefixes=/api` and `addprefix.prefix=/v1`. With StripPath and Path `/`, there is no strip middleware.
  - `TestInjectKeepsLabelShape`: list form stays a `[]any` of `key=value`; map form stays a map. Hand-written `traefik.http.routers.mine.rule` and `.service` are kept unchanged. A list entry `traefik.enable` with no `=` is replaced, not duplicated. A service with no `labels:` gets a list.
  - `TestInjectOverridesWithWarnings`: map `traefik.enable: false` (bool) and list `traefik.enable=false` both become `true`, each with the enable warning. `traefik.docker.network=other` becomes `relist-network`, with its warning.
  - `TestInjectWarnsOnImplicitRouter`: hand-written `traefik.http.routers.mine.rule` without `.service` on the domain's Service gives the implicit-router warning naming `mine`. The same router with `.service` gives no warning.
  - `TestInjectWarnsOnSameHostRouter`: a hand-written router on **another** Service of the same Compose whose rule contains ``Host(`a.example.com`)`` gives the host warning.
  - `TestInjectTwoDomainsOneService`: ports 3000 and 9000 produce two routers and two services, each pinned with `.service`.
  - `TestInjectRefusesMissingOrNetworkModeService`: `errors.As(err, &*domains.TargetError)` holds, and `err.Error()` is the exact copy for each case.
- [ ] **Step 2: Run** `go test ./internal/domains/...`. Expected: FAIL (undefined `Inject`).
- [ ] **Step 3: Implement** `domains.go` (types, `RouterName`, `Address`) and `labels.go`. Normalise either label shape into an ordered key→value list, set keys by replacement, and write back in the original shape (a list when absent). Stringify non-string scalars with `fmt.Sprint`. Detect hand-written routers as keys `traefik.http.routers.<r>.…` where `<r>` does not start with `relist-c<composeID>-`.
- [ ] **Step 4: Run** `go test ./internal/domains/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: generate traefik labels for a compose's domains`.

### Task 2: Domain storage, validation and CRUD API

**Files:**
- Create: `internal/migrations/00013_domains.sql`, `internal/domains/store.go`, `internal/domains/domains_test.go`, `internal/httpapi/domains_test.go`
- Modify: `internal/domains/domains.go`, `internal/httpapi/httpapi.go`, `internal/composes/composes.go` (`DomainsChangedAt` only)

**Interfaces:**
- Consumes: `Domain`, `Address` (Task 1).
- Produces (package `domains`):
  ```go
  type Input struct { Host, Path, InternalPath string; StripPath bool; Service string; Port int; Entrypoint string; HTTPS bool; CertProvider, CertResolver string; Middlewares []string } // JSON tags as Domain
  type FieldError struct{ Field, Message string }  // Field is the JSON name: "host", "path", "internalPath", "service", "port", "entrypoint", "certProvider", "certResolver", "middlewares"
  // Validate trims and lowercases host, fills defaults ("/", "/", "none", []), clears
  // certProvider/certResolver per the spec, and checks every rule in the spec's
  // Validation table against services (DeclaredServices of the saved YAML).
  func Validate(in Input, services []string) (Input, *FieldError)
  // DeclaredServices returns the sorted keys of services: in yamlContent, leaving out
  // any Service that sets network_mode; nil-safe; [] on a parse error.
  func DeclaredServices(yamlContent string) []string
  func NewStore(db *sql.DB) *Store
  func (s *Store) ListForCompose(ctx context.Context, composeID int64) ([]Domain, error) // ID order
  func New(db *sql.DB) *Service
  func (s *Service) RegisterRoutes(mux *http.ServeMux)
  ```
- Routes: `GET|POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/domains` and `PUT|DELETE …/domains/{domainID}`. Wire formats are in the spec's HTTP protocol section. A `400` body is `{"error": Message, "field": Field}`.
- `composes.Compose` gains `DomainsChangedAt *time.Time `json:"domainsChangedAt"``, read from `composes.domains_changed_at` in both `SELECT`s and in `scanComposeValues`.

- [ ] **Step 1: Write the failing pure tests** in `domains_test.go`:
  - `TestValidateNormalisesAndDefaults`: ` API.Example.com ` becomes `api.example.com`; empty paths become `/`; `HTTPS:false` with `CertProvider:"custom", CertResolver:"x"` comes back as `none` and `""`; nil middlewares become `[]`.
  - `TestValidateRejects`: one case per rule, each asserting `Field`:
    - `api.example.com.` and `*.example.com` → `host`
    - `/a b` and ``/a`b`` → `path`
    - `api` → `path` (no leading `/`)
    - port 0 and 65536 → `port`
    - `bad name` → `entrypoint`
    - `custom` with an empty resolver → `certResolver`
    - `a,b` and 21 entries → `middlewares`
    - an unknown service → `service`
  - `TestDeclaredServices`: sorted; leaves out a `network_mode: host` Service; `[]` for `: :` (unparsable) and for empty YAML.
- [ ] **Step 2: Write the failing HTTP tests** in `internal/httpapi/domains_test.go`, with a Compose whose YAML declares `web` and `db`:
  - `TestDomainsCRUD`: `GET` gives `{"domains":[],"services":["db","web"]}`; `POST` gives `201` and the stored domain; `PUT` gives `200`; `PUT` with its own unchanged host+path also gives `200`; `DELETE` gives `204`, and a second `DELETE` gives `404`.
  - `TestDomainsRejectInvalidFieldWith400`: a body with host `bad host` gives `400` with `"field":"host"`.
  - `TestDomainsConflictAcrossComposes`: a second Compose in another Project posting the same host+path gets `409` whose `error` equals `api.example.com/ is already used by <project> / production / <compose>`. The same host with path `/api` succeeds.
  - `TestDomainsChangedAtMovesOnEveryChange`: Compose `GET` has `domainsChangedAt: null` initially, and non-null after create. After update and after delete it is `>=` the previous value.
  - `TestDomainsCascadeWithCompose`: after deleting a Compose that had a domain, `SELECT COUNT(*) FROM domains` on the test server's DB is 0.
  - `TestDomainsRequireKnownCompose`: an unknown Compose, or a Compose under the wrong Environment, gives `404`.
- [ ] **Step 3: Run** `go test ./internal/domains/... ./internal/httpapi/...`. Expected: FAIL.
- [ ] **Step 4: Implement** the migration (verbatim from the spec), `Validate`, `DeclaredServices`, `Store` and `Service`. Each write and its `UPDATE composes SET domains_changed_at = ?` share one transaction. Detect the conflict from the `UNIQUE` constraint error, then look up the owner names with a join through `composes` → `environments` → `projects`. Register `domains.New(db).RegisterRoutes(mux)` in `NewHandler` next to `composesService`. These routes sit under `/api/projects/{id}/…`, so the existing project gate already serialises writes against deploy. Leave `coordination.go` unchanged.
- [ ] **Step 5: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 6: Commit** `feat: store and validate a compose's domains behind a crud api`.

### Task 3: Inject domains at deploy

**Files:**
- Modify: `internal/composes/composes.go` (`resolveCompose`, `prepareDeployFiles`, `deployFiles`, `deploy`, `deployFresh`, `completeDeploy`)
- Test: `internal/httpapi/domains_test.go`

**Interfaces:**
- Consumes: `domains.NewStore`, `Store.ListForCompose`, `domains.Inject`, `domains.TargetError` (Tasks 1–2).
- Produces:
  - `func resolveCompose(yamlContent, networkName, envFilePath string, composeID int64, ds []domains.Domain) (string, []string, error)`.
  - The `deploy` and `deploy-fresh` responses are the Compose JSON plus `"warnings": [...]` (never null). No other endpoint adds the field. Embed `Compose` in an unexported `deployResponse{Compose; Warnings []string}` written only by `completeDeploy`.

- [ ] **Step 1: Write the failing HTTP tests**:
  - `TestDeployInjectsDomainLabels`: create an HTTPS Let's Encrypt domain on `web`, deploy, and read `upCall.request.ComposeFilePath` as at `httpapi_test.go:1162`. Assert it holds ``traefik.http.routers.relist-c<ID>-d<ID>.rule=Host(`api.example.com`)`` and `relist-network`, and that the response has `"warnings":[]`.
  - `TestDeployReturnsWarnings`: YAML with `traefik.enable=false` on `web`, plus a domain, gives a `deploy` response with the enable warning. Run the same check against `deploy-fresh`.
  - `TestDeployRefusedWhenDomainServiceRemoved`: deploy once, save YAML without `web`, deploy again. The response is `400` with the exact refusal copy, the runner records no second `Up`, and the resolved file on disk is byte-identical to the first deploy's.
  - `TestDeployWithoutDomainsUnchanged`: the existing `TestAuthenticatedAdminCanDeployCompose` still passes untouched.
- [ ] **Step 2: Run** `go test ./internal/httpapi/ -run 'Deploy'`. Expected: the new tests FAIL.
- [ ] **Step 3: Implement.** In `prepareDeployFiles`, load the domains with `ListForCompose(ctx, compose.ID)` (`500 could not load domains` on error) and pass them to `resolveCompose`. Inside `resolveCompose`, call `domains.Inject(services, composeID, ds, networkName)` after the network/env_file loop. Map a `*domains.TargetError` to `400` with its message as-is; other errors keep the `compose yaml is invalid: ` prefix. `composes.Service` builds its store from `domains.NewStore(db)` in `New`, so `New`'s signature is unchanged.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: inject domain labels into the resolved compose file at deploy`.

### Task 4: Traefik TLS foundation and docs

**Files:**
- Modify: `docker-compose.yml`, `README.md`, `CONTEXT.md`, `docs/adr/0001-v1-scope-and-stack.md`
- Create: `docs/adr/0013-panel-managed-domains.md`

- [ ] **Step 1: Edit `docker-compose.yml`** exactly per the spec's "TLS foundation" section:
  - the five Traefik flags
  - port `${TRAEFIK_HTTPS_PORT:-443}:443`
  - volume `${RELIST_DATA_DIR:-/var/lib/relist-panel}/traefik:/letsencrypt`
  - panel router `panel` on `websecure` with `tls.certresolver=letsencrypt`
  - router `panel-http` on `web`, same rule, `middlewares=panel-redirect`
  - `traefik.http.middlewares.panel-redirect.redirectscheme.scheme=https` and `.permanent=true`

  Also set `traefik.http.routers.panel.service=panel` and `traefik.http.routers.panel-http.service=panel`, so neither depends on Traefik's implicit linking (the existing service label is already named `panel`). Comment the read timeout the way the `web` one is.
- [ ] **Step 2: Verify:** `docker compose config -q` prints nothing and exits 0. Then `docker compose config | grep -E 'websecure|letsencrypt|panel-http'` shows each.
- [ ] **Step 3: Docs.** Write each edit as the spec's Documentation section lists it:
  - README: the TLS paragraph, `RELIST_ACME_EMAIL` (optional but recommended, with the reason from the spec), `TRAEFIK_HTTPS_PORT`, the timeout note, the local-development effect, an **Upgrading an existing install** section, and the Volume files TLS note.
  - ADR-0013: context, decision, the rejected override-file and file-provider options, always-on HTTPS, and the empty-email probe.
  - ADR-0001: a "Widened since" note.
  - CONTEXT.md: the **Domain** term, and the **Compose** entry updated.
- [ ] **Step 4: Commit** `feat: serve the panel over https and add a lets encrypt resolver to traefik`.

### Task 5: Web data layer and view rules

**Files:**
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`, `web/src/data/api.test.ts`, `web/src/data/store.tsx`, `web/src/pages/compose-view.ts`, `web/src/pages/compose-view.test.ts`
- Create: `web/src/pages/domains-view.ts`, `web/src/pages/domains-view.test.ts`

**Interfaces:**
- Consumes: the HTTP API from Tasks 2–3.
- Produces:
  ```ts
  // types.ts
  export type CertProvider = 'none' | 'letsencrypt' | 'custom';
  export type DomainInput = { host: string; path: string; internalPath: string; stripPath: boolean; service: string; port: number;
    entrypoint: string; https: boolean; certProvider: CertProvider; certResolver: string; middlewares: string[] };
  export type Domain = DomainInput & { id: number; createdAt: string; updatedAt: string };
  export type ComposeDomains = { domains: Domain[]; services: string[] };
  // Compose gains: domainsChangedAt?: string | null; warnings?: string[]
  // api.ts: ApiError gains `readonly field?: string`, filled from the body's `field`
  listDomains(location: ComposePath): Promise<ComposeDomains>
  createDomain(location: ComposePath, input: DomainInput): Promise<Domain>
  updateDomain(location: ComposePath, domainId: number, input: DomainInput): Promise<Domain>
  deleteDomain(location: ComposePath, domainId: number): Promise<void>
  // store.tsx — each takes composeId, like loadVolumes; mutations call refreshStatus(composeId) afterwards
  loadDomains(composeId): Promise<ComposeDomains>; createDomain(composeId, input); updateDomain(composeId, domainId, input); deleteDomain(composeId, domainId)
  // domains-view.ts
  export type DomainFormValues = DomainInput & { customEntrypoint: boolean };
  export function domainFormValues(domain?: Domain): DomainFormValues        // defaults: path "/", internalPath "/", https false, certProvider "letsencrypt", middlewares []
  export function domainInput(values: DomainFormValues): DomainInput          // clears hidden fields
  export function domainsPendingRedeploy(compose: Pick<Compose, 'domainsChangedAt' | 'lastDeployedAt'>): boolean
  export function httpsTag(domain: Domain): string                            // 'HTTP' | "Let's Encrypt" | 'custom: <resolver>' | 'TLS (default cert)'
  export function domainUrl(domain: Domain): string                           // `${https ? 'https' : 'http'}://${host}${path}`
  // compose-view.ts: ComposeTab gains 'domains'; activeComposeTab('domains') === 'domains'
  ```
- In `runComposeAction`, the store keeps `updated.warnings` from deploy and deploy-fresh on the patched Compose. Any later `getCompose` patch drops it naturally.

- [ ] **Step 1: Write the failing tests:**
  - In `domains-view.test.ts`:
    - `domainInput` clears `entrypoint` when `customEntrypoint` is false, sends `certProvider: 'none'` and `certResolver: ''` when `https` is false, and clears `certResolver` when the provider is not `custom`.
    - `domainFormValues(undefined)` gives the defaults; `domainFormValues(domain)` sets `customEntrypoint` from a non-empty `entrypoint`.
    - `domainsPendingRedeploy` is false when never deployed and false when `domainsChangedAt` is null. It is true on a tie and when the change is after the deploy, and false when the change is before.
    - `httpsTag` gives all four strings; `domainUrl` gives both schemes.
  - In `compose-view.test.ts`: `activeComposeTab('domains')`.
  - In `api.test.ts`: a `400` body `{"error":"x","field":"host"}` yields `ApiError` with `field === 'host'`.
- [ ] **Step 2: Run** `pnpm --dir web test`. Expected: FAIL.
- [ ] **Step 3: Implement** the types, the API methods, `ApiError.field`, the store functions, the deploy-warning retention, `domains-view.ts` and the tab key.
- [ ] **Step 4: Run** `pnpm --dir web test && pnpm --dir web typecheck`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: add domain api calls and view rules to the web client`.

### Task 6: Domains tab, form modal and deploy warnings

**Files:**
- Create: `web/src/components/ComposeDomainsTab.tsx`, `web/src/components/DomainFormModal.tsx`
- Modify: `web/src/pages/ComposePage.tsx`, `docs/GUIDELINES.md` (§4.6)

**Interfaces:**
- Consumes: Task 5's store functions and `domains-view.ts`.
- `ComposeDomainsTab` props: `{ compose: Compose; deploying: boolean; onDeploy: () => void }`. It loads its own data with `loadDomains(compose.id)` on mount and after each mutation.
- `DomainFormModal` props: `{ open: boolean; domain?: Domain; services: string[]; saving: boolean; error?: ApiError; onCancel(): void; onSubmit(input: DomainInput): void }`.

- [ ] **Step 1: Build `DomainFormModal`.** Fields, order, defaults, conditional visibility and help copy exactly as in the spec's "Add / Edit Domain modal" section. Use `Form.useWatch` for `https`, `certProvider` and `customEntrypoint`. Submit through `domainInput`. Put `error.field` onto that field with `form.setFields`. Show a `409` (or a `400` with no field) as an `Alert` above the form.
- [ ] **Step 2: Build `ComposeDomainsTab`:**
  - The banner, when `domainsPendingRedeploy(compose)`, with a **Deploy** button that calls `onDeploy` and is `loading={deploying}`.
  - An **Add domain** button in the header.
  - The table columns per the spec. The host+path link opens `domainUrl` with `target="_blank" rel="noreferrer"`.
  - Edit opens the modal. Delete is a `Popconfirm` titled `Delete <Address>?`.
  - The empty state with the spec copy.
  - A load error shown as an `Alert` with **Retry**, matching how the Volumes tab shows its errors.
- [ ] **Step 3: Wire into `ComposePage`.**
  - Add tab `{ key: 'domains', label: 'Domains' }` right after Editor, rendering `<ComposeDomainsTab compose={compose} deploying={isPending(`deploy:${compose.id}`)} onDeploy={() => void deploy()} />`.
  - Above the tabs, when `compose.warnings?.length`, show a closable `warning` `Alert` listing each warning. Dismissal is local state keyed by `compose.lastDeployedAt`, so the next deploy shows new warnings.
- [ ] **Step 4: GUIDELINES §4.6.** Add **Domains** (`…/compose/:composeId/domains`) to the tab list, plus a short paragraph: banner rule, table, modal, `Popconfirm` delete, deploy-warnings alert.
- [ ] **Step 5: Run** `pnpm --dir web test && pnpm --dir web typecheck && task build`. Expected: all pass, and the embedded build succeeds.
- [ ] **Step 6: Commit** `feat: add a domains tab to the compose page`. Then commit `internal/webassets/dist` separately as `chore: rebuild the embedded frontend with the domains tab`.

### Task 7: Manual verification

**Files:**
- Modify: `.scratch/domains/spec.md` (append a `## Manual verification` table in the image-upload spec's format)

- [ ] **Step 1: Local.** Run the three local checks from the spec's Manual section against `docker compose up -d --build` with a `traefik/whoami` Compose:
  - `curl -I http://localhost:8888/` gives `200`.
  - `curl -sI -H 'Host: panel.localhost' http://localhost/` gives `308` to `https://`.
  - An HTTP domain `whoami.localhost`, with path `/api`, internal path `/v1` and strip on: `curl -H 'Host: whoami.localhost' http://localhost/api/x` shows `GET /v1/x`.
  - Deploy warnings appear for a Compose that has `traefik.enable=false`.
- [ ] **Step 2: Public host.** Run it if one is available (staging CA first); otherwise record it as not run.
- [ ] **Step 3: Upgrade.** An existing Compose with hand-written labels still routes after the new install file is applied.
- [ ] **Step 4: Commit** `docs: record the domains manual run`.
