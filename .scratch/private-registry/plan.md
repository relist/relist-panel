# Private registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the panel pull private images with stored External registry credentials, run its own `registry:3` that CI pushes into, and clean that registry on a schedule.

**Architecture:**
- **`internal/registryauth`** owns:
  - External registries: table, API, and the Test action;
  - `Source`, the list of `auths` every pull or build needs.
- **`dockerexec`** gives each pull or build call a temporary `DOCKER_CONFIG` built from `Source`. It points `BUILDX_CONFIG` at the original buildx state so Builders stay visible.
- **`internal/registry`** owns the Panel registry:
  - settings and Push credentials;
  - the files in `<data dir>/.registry/`;
  - reconcile, the registry HTTP client and Repositories;
  - in PR 3, cleanup.

  It drives Docker through one port, `registry.Host`, implemented in `internal/dockerexec/registry.go` over the Engine API.
- **`composes`** records the image refs of each successful deploy in `compose_image_refs`. That table is the "in use" source for stopped Composes.

**Tech Stack:** Go (stdlib `net/http`, `database/sql`, `modernc.org/sqlite`, goose, `golang.org/x/crypto/bcrypt`), CNCF Distribution `registry:3` (3.1.2). React + antd (the version in `web/package.json`), vitest + Testing Library.

**Spec:** `.scratch/private-registry/spec.md`

## Global Constraints

- **Migrations:** never edit a committed migration.
  - New files are `00035_external_registries.sql` (PR 1), `00036_registry.sql` (PR 2), `00037_compose_image_refs.sql` (PR 2) and `00038_registry_cleanup.sql` (PR 3).
  - If `main` has moved on, renumber them before committing.
- **Registry image:** `registry:3@sha256:38907510915c3f1604c7e83d6ab602ab3020d092f73bdaed8696773693ff768c` (index digest; Distribution 3.1.2, probed 2026-11-06).
  - It lives in `registry.DefaultImage`.
  - Re-check it before Task 9 is committed: `docker buildx imagetools inspect registry:3`.
- **Host names:**
  - container `relist-registry`, port 5000, on `relist-network`, no host port;
  - volume `relist-registry`, mounted at `/var/lib/registry`;
  - GC container `relist-registry-gc`;
  - files in `<data dir>/.registry/`, mounted **as a directory** at `/etc/distribution`, read-only.
  - The data dir has the same path inside the panel container and on the host (`docker-compose.yml`), so the bind source is the data dir path itself.
- **Files are written by temp file + rename in the same directory.** Probed: with a directory mount, registry 3.1.2 picks up a renamed `htpasswd` with no restart. A new user got 200, and a removed user got 401, both within 2 s.
- **Docker config:**
  - every pull or build call gets `DOCKER_CONFIG=<fresh 0700 temp dir>` holding a `0600` `config.json`;
  - when the inherited env has no `BUILDX_CONFIG`, it gets `BUILDX_CONFIG=<original DOCKER_CONFIG or $HOME/.docker>/buildx`.
  - Probed: with only `DOCKER_CONFIG` swapped, `docker buildx ls` lost the panel's builders. With `BUILDX_CONFIG` set, they are back.
  - The temp dir is removed when the call returns.
- **Validation:**
  - **External registry host:** lowercase. Strip `https://`, `http://` and a trailing `/`. Must match `^[a-z0-9.-]+(:[0-9]{1,5})?$`. Unique.
    - `docker.io`, `index.docker.io` and `registry-1.docker.io` are stored as `docker.io`, and written to `auths` under `https://index.docker.io/v1/`.
  - **Registry hostname:** a DNS name of 1–253 characters with at least one dot, no port, lowercase. It must not equal the panel's `RELIST_DOMAIN` or any Domain's host.
  - **Push credential name:** matches `^[a-z0-9][a-z0-9_-]{0,39}$`, is unique, and is not `relist-panel`.
  - **Traefik read timeout:** a Go duration (`90s`, `5m`, `1h`) or bare seconds (`3600`). At least 30 s; 0 is rejected.
  - **Keep N:** 1–1000.
  - **Schedule:** `days` is a non-empty subset of `mon tue wed thu fri sat sun`, or the single value `daily`. `minute` is 0–1439.
- **Tokens:** a Push credential token and the system secret are 32 random bytes, base64url without padding.
  - Store the bcrypt hash at the default cost.
  - Return a Push credential token only in the create response.
- **Copy, verbatim:**
  - read timeout help: `Longest time to receive one request, body included. Image uploads on the Images page and pushes to the Registry need minutes per large layer.`
  - read timeout warning (below 10 minutes): `Large image uploads and registry pushes may be cut off.`
  - read timeout problem: `read timeout must be a duration of at least 30s, like 90s, 5m or 1h`
  - token hint (External registry form): `Use a token that can only pull, such as a GitHub PAT with read:packages.`
  - pull hint (Deployment log, after a pull that fails with `unauthorized`, `denied` or `authentication required`): `If the image is private, add its registry's credentials on Registry → External registries.`
  - hairpin hint (pull of an image on the Registry hostname fails with a dial or TLS error): `This host could not reach its own Registry at <hostname>. Check that the hostname resolves to this host from the host itself.`
  - disable warning: `These Composes use images from the Registry; their next deploy will fail to pull: <a>, <b>`
  - delete tag confirm: `Deleting <repo>:<tag> also deletes <list>, which point at the same image.`
  - hostname conflict: `409 <host> is the panel's own domain` / `409 <host> is a Domain of <project>`
  - a Domain on the Registry hostname: `409 <host> is the Registry's hostname`
  - cleanup busy: `409 A cleanup is already running`
- **Access:** every `/api/registry/...` route is admin-only. Leave them out of `authz.Routes`, which defaults to admin, and add an action name for each write to `authz/actions.go`, `registry.*`.
- **Commits:** Conventional Commits with no scope (`docs/agents/commit-conventions.md`).
- **Frontend:** before touching `web/`, read https://ant.design/llms.txt and the pages for Tabs, Table, Form, Input, InputNumber, Select, Checkbox, TimePicker, Switch, Modal, Popconfirm, Tag, Alert, Descriptions, Empty and Typography (copyable). Check them against the installed antd. Follow `docs/GUIDELINES.md`.
- **Tests:**
  - `go test ./...`, `pnpm --dir web test` and `pnpm --dir web typecheck`.
  - `internal/webassets/dist` is git-ignored since c25ab7a: run `task build-web` to test the binary locally, and never commit its output.

## Decisions made while planning (sharpen the spec)

- **Probes from 2026-11-06 settled three of the spec's open questions:**
  - registry 3.1.2 reloads `htpasswd` by itself, so no restart is needed;
  - a per-call `DOCKER_CONFIG` needs `BUILDX_CONFIG` to keep the Builders;
  - the config path is `/etc/distribution/config.yml`.
- **Read-only mode is probed.**
  - The switch rewrites `config.yml` with `storage.maintenance.readonly.enabled: true` and runs `docker restart relist-registry`.
  - While it is on, pulls work, and a push fails with `405 Method Not Allowed` on the manifest PUT.
  - `registry garbage-collect --delete-untagged /etc/distribution/config.yml` ran on the same volume, in a second container from the same image. Deleting one tag and collecting took the volume from 2468 KB to 2348 KB.
  - GC ignores the read-only flag, so one `config.yml` serves both containers.
- **The panel talks to the registry API over `relist-network`** at `http://relist-registry:5000`, with the system credential (user `relist-panel`).
  - It never goes through the public hostname, so Repositories works without hairpin NAT.
  - A panel run outside the install stack (a bare `go run`) cannot reach it. Repositories then shows the error.
- **Docker pulls go through the public hostname**, because Compose files name `<hostname>/<repo>:<tag>`. The system credential is written into every temp `config.json` under that hostname.
- **"In use"** is the union of two sources:
  - `dockerexec.InUse.Images`, which only sees running containers;
  - `compose_image_refs`, the refs the latest successful deploy of each Compose pulled or built.

  `prepareImages` returns its refs, and `deploy` stores them right after `markDeployed`. A failed deploy keeps the previous set.
- **Tags and digests:**
  - A tag's digest is the `Docker-Content-Digest` of `HEAD /v2/<repo>/manifests/<tag>`. Its Accept header lists the OCI index, OCI manifest, Docker manifest list and Docker v2 manifest types.
  - **For an index,** size and created come from the first child manifest whose platform is not `unknown/unknown` (buildx attestations use that platform). Size is the sum of that manifest's config and layer sizes.
  - Delete is `DELETE /v2/<repo>/manifests/<digest>`, which returns 202.
- **The registry route has an HTTP→HTTPS redirect,** like a Domain's: a `relist-registry-http` router on `web` with a `redirectscheme` middleware. It uses Traefik labels on the container.
  - **TLS:** with `letsencrypt`, `tls.certresolver=letsencrypt`; with `certificate`, `tls=true` and no resolver, so an uploaded Certificate is chosen by SNI.
  - The label `relist.registry.hash` holds the SHA-256 of the image ref, hostname, TLS mode and `config.yml`. Reconcile compares it to detect drift.
- **Disable removes the container,** and with it the route, because the route is labels. It keeps the volume and the files.
- **Cleanup is not a Deployment queue job.**
  - It starts only when `queue.Busy()` is false and no Panel update or Migration lock is on.
  - A Deployment that starts during GC still pulls, because read-only allows pulls.
  - `registry.Cleaner` exposes `WaitIdle(ctx) error`. Panel update's quiesce waits on it like it waits on scans: it is a second `panelupdate.ScanWaiter`, `Deps.RegistryCleanup`.
- **The Migration option travels as `includeRegistry` (bool)** in the start body, beside `hostPaths`.
  - An old peer omits it, which reads as false. No protocol bump is needed.
  - When it is true, the manifest adds the `relist-registry` volume as an ordinary `KindVolume` item.
  - On the new host, the restored database has the registry enabled, and start-up reconcile creates the container.
- **Ownership on the Docker page:** the registry image and the `relist-registry` volume are owned by `Registry`, so the confirmed cleanup never removes them.

## Review Focus

1. **A buildx push from CI is an OCI index with an attestation manifest.**
   - Repositories must show one tag with a real size and created time, not the attestation's.
   - Delete must remove the whole index.
   - Task 12: `TestTagInfoReadsIndexSkippingAttestation`.
2. **The panel restarts or is killed during GC.** The registry must come back read-write, and the run must be recorded `interrupted`. Task 19: `TestStartupFinishesInterruptedGC` and `TestStartupWaitsForRunningGCContainer`.
3. **A panel rebuild wipes `~/.docker` while a per-call `DOCKER_CONFIG` is in use.** Builders must still resolve, and no temp dir may leak. Task 4: `TestAuthEnvKeepsBuildxConfig` and `TestAuthDirRemovedAfterCall`.
4. **CI pushes a new tag onto a digest that cleanup is about to delete.** Task 19: `TestCleanupSkipsDigestRetaggedSinceSelection`.
5. **The Registry hostname collides with a Domain or the panel domain,** in either order. Task 10: `TestHostnameConflicts`; Task 15: `TestDomainRefusesRegistryHost`.
6. **The Docker page's cleanup** must not remove the registry image or its volume. Task 15: `TestOwnershipIncludesRegistry`.

---

## PR 0 — Traefik read timeout

### Task 1: Read timeout in the Traefik form (backend)

**Files:**
- Modify: `internal/traefikconfig/form.go`
- Test: `internal/traefikconfig/form_test.go`

**Interfaces:**
- Produces: `Form.ReadTimeout string` with JSON `readTimeout`, and `const DefaultReadTimeout = "3600s"` (the value in `base.yml`).

- [ ] **Step 1: Write the failing tests**

```go
func TestReadFormReadTimeoutDefaultsToBase(t *testing.T) // empty override → "3600s"
func TestReadFormReadTimeoutFromWebsecure(t *testing.T)  // websecure 5m → "5m"
func TestReadFormWarnsWhenReadTimeoutsDiffer(t *testing.T) // web 1h, websecure 5m → Problem{Key: "entryPoints.web.transport.respondingTimeouts.readTimeout"}
func TestWriteFormReadTimeoutBothEntryPoints(t *testing.T) // "10m" → both paths = "10m"
func TestWriteFormReadTimeoutDefaultRemovesKey(t *testing.T) // "3600s" or "1h" → key absent from the override; empty maps on the way removed
func TestWriteFormRejectsShortReadTimeout(t *testing.T) // "29s", "0", "abc" → Problem{Key: "entryPoints.websecure.transport.respondingTimeouts.readTimeout", Message: "read timeout must be a duration of at least 30s, like 90s, 5m or 1h"}
func TestWriteFormAcceptsBareSeconds(t *testing.T) // "120" → written "120s"
```

- [ ] **Step 2: Run** `go test ./internal/traefikconfig/ -run 'ReadTimeout'`. Expected: FAIL, with undefined field `ReadTimeout`.
- [ ] **Step 3: Implement.**
  - Path: `entryPoints.<ep>.transport.respondingTimeouts.readTimeout`.
  - Parse with `parseReadTimeout(s string) (time.Duration, string, bool)`: bare digits mean seconds; otherwise `time.ParseDuration`. It returns the normalised text (`"120"` → `"120s"`; others as typed, trimmed).
  - A value equal to 1 h removes the key from both entry points.
- [ ] **Step 4: Run** `go test ./internal/traefikconfig/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: set Traefik's request read timeout from the Settings form`

### Task 2: Read timeout field (frontend)

**Files:**
- Modify: `web/src/data/types.ts` (`TraefikForm.readTimeout: string`)
- Modify: `web/src/components/TraefikConfigTab.tsx`
- Modify: `web/src/pages/traefik-config-view.ts`, which gets `readTimeoutWarning(value: string): boolean` (true when the value parses below 600 s)
- Test: `web/src/pages/traefik-config-view.test.ts`, `web/src/pages/TraefikPage.test.tsx`

- [ ] **Step 1: Write failing tests:**
  - `readTimeoutWarning('5m') === true`
  - `readTimeoutWarning('10m') === false`
  - `readTimeoutWarning('3600') === false`
  - `readTimeoutWarning('junk') === false`
  - the page test: the field labelled `Request read timeout` shows the help copy, shows the warning Alert for `5m`, and sends `readTimeout` in the preview body.
- [ ] **Step 2: Run** `pnpm --dir web test traefik`. Expected: FAIL.
- [ ] **Step 3: Implement.** Put an `Input` after Access log, with `extra` = help copy and an inline `Alert type="warning"` = warning copy when `readTimeoutWarning` is true.
- [ ] **Step 4: Run** `pnpm --dir web test && pnpm --dir web typecheck`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the read timeout field to the Traefik page`.

---

## PR 1 — External registries

### Task 3: External registries table and store

**Files:**
- Create: `internal/migrations/00035_external_registries.sql`
- Create: `internal/registryauth/store.go`
- Test: `internal/registryauth/store_test.go`

**Interfaces:**
- Produces:
  - `type External struct { ID int64; Host, Username, Secret string; LastTestAt *time.Time; LastTestOK bool; LastTestError string; CreatedAt, UpdatedAt time.Time }`
  - `NormalizeHost(s string) (string, error)`
  - `NewStore(db *sql.DB) *Store`
  - `List(ctx) ([]External, error)`, ordered by host
  - `Get(ctx, id) (External, error)`, which returns `sql.ErrNoRows` when missing
  - `Create(ctx, e External, now time.Time) (External, error)`
  - `Update(ctx, e External, now time.Time) error`: an empty `Secret` keeps the stored one
  - `Delete(ctx, id) error`
  - `SetTestResult(ctx, id, ok bool, msg string, at time.Time) error`
  - `ErrDuplicateHost`

- [ ] **Step 1: Write failing tests:**
  - `TestNormalizeHost`, table-driven:
    - `"https://GHCR.io/"` → `"ghcr.io"`
    - `"index.docker.io"` → `"docker.io"`
    - `"reg.example.com:5443"` passes
    - `"a b"` errors
  - `TestCreateDuplicateHost` (`ErrDuplicateHost`)
  - `TestUpdateEmptySecretKeepsStored`
  - `TestListOrderedByHost`
- [ ] **Step 2: Run** `go test ./internal/registryauth/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Table `external_registries(id INTEGER PRIMARY KEY, host TEXT NOT NULL UNIQUE, username TEXT NOT NULL, secret TEXT NOT NULL, last_test_at TEXT, last_test_ok INTEGER NOT NULL DEFAULT 0, last_test_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`. Follow `internal/builders/store.go` for time encoding.
- [ ] **Step 4: Run.** Expected: PASS, including `go test ./internal/migrations/...` if it exists.
- [ ] **Step 5: Commit** `feat: store External registry credentials`

### Task 4: Per-call Docker config

**Files:**
- Create: `internal/registryauth/config.go`
- Modify: `internal/dockerexec/dockerexec.go` and `internal/dockerexec/cli_env.go`
- Modify: `main.go:180` (`dockerexec.New()` → `dockerexec.New(auths)`), `internal/httpapi/httpapi.go` (`Deps.RegistryAuth *registryauth.Switch`)
- Test: `internal/registryauth/config_test.go`, `internal/dockerexec/dockerexec_test.go`

**Interfaces:**
- Produces:
  - `type Auth struct { Host, Username, Secret string }`
  - `type Source interface { Auths(ctx context.Context) ([]Auth, error) }`
  - `(*Store).Auths(ctx) ([]Auth, error)`, which makes `*Store` a Source
  - `MultiSource(sources ...Source) Source`, which concatenates
  - `type Switch struct` with `Set(Source)` and `Auths(ctx)`, which returns nil when unset. `main.go` creates one, passes it to `dockerexec.New` and to `httpapi.Deps.RegistryAuth`, and `httpapi.NewHandler` calls `Set`. The Runner is built in `main.go` before the stores exist in `httpapi`, so the source must be late-bound.
  - `WriteConfig(dir string, auths []Auth) error`, which writes `dir/config.json` with mode 0600 as `{"auths":{"<key>":{"auth":"<base64 user:secret>"}}}`; the key is `https://index.docker.io/v1/` for `docker.io`, else the host
  - `dockerexec.New(auths registryauth.Source) Runner`, where a nil source means no injection
  - `authEnv(ctx, src, environ []string) (env []string, cleanup func(), err error)`

- [ ] **Step 1: Write failing tests:**
  - `TestWriteConfigDockerHubKey`
  - `TestWriteConfigMode0600`
  - `TestAuthEnvKeepsBuildxConfig`:
    - no `BUILDX_CONFIG` and `HOME=/h` gives `BUILDX_CONFIG=/h/.docker/buildx`;
    - `DOCKER_CONFIG=/c` gives `/c/buildx`;
    - an existing `BUILDX_CONFIG` is kept.
  - `TestAuthDirRemovedAfterCall`: after `Pull` with the stand-in `command`, the temp dir is gone.
  - `TestStatusGetsNoAuthEnv`: `ps` runs with the inherited env only.
- [ ] **Step 2: Run** `go test ./internal/registryauth/ ./internal/dockerexec/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - `Runner` holds `auths registryauth.Source`.
  - `runOn` gains a `withAuth bool` argument. It is true for `Pull`, `Build`, `Scale`, and `Up` when `!opts.NoPull || opts.Build`.
  - `cliEnv` already keeps `BUILDX_CONFIG` and `DOCKER_*`. `authEnv` replaces `DOCKER_CONFIG`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Probe.** On the dev panel, run a deploy of a Compose that builds on a Builder, with one External registry stored. The build must use the Builder (log `Builder: <name>`), and `ls /tmp` must show no `relist-auth-*` left.
- [ ] **Step 6: Commit** `feat: give pulls and builds the stored registry credentials`

### Task 5: External registries API, Test, and the pull hint

**Files:**
- Create: `internal/registryauth/http.go`
- Create: `internal/dockerexec/login.go`, implementing `registryauth.Tester`
- Modify: `internal/authz/actions.go`, `internal/httpapi/httpapi.go`, `internal/composes/deploy.go`
- Test: `internal/registryauth/http_test.go`, `internal/composes/deploy_test.go`

**Interfaces:**
- Consumes: `Store` from Task 3.
- Produces:
  - `type Tester interface { Login(ctx context.Context, a Auth) error }`; the implementation runs `docker login <host> -u <user> --password-stdin` under a throwaway `DOCKER_CONFIG`, with a 30 s timeout
  - `New(store *Store, tester Tester, now func() time.Time) *Service`, with `RegisterRoutes(mux)`
  - Routes:
    - `GET /api/registry/external` returns `[{id, host, username, lastTestAt, lastTestOk, lastTestError}]`, never the secret
    - `POST /api/registry/external` and `PUT /api/registry/external/{id}` take `{host, username, secret}`
    - `DELETE /api/registry/external/{id}`
    - `POST /api/registry/external/{id}/test` returns `{ok, error}`
  - Actions `registry.external_create`, `registry.external_update`, `registry.external_delete` and `registry.external_test`; `audit.Target` is the host.

- [ ] **Step 1: Write failing tests:**
  - `TestListNeverReturnsSecret`
  - `TestCreateRejectsBadHost` (422)
  - `TestCreateDuplicate` (409)
  - `TestTestRecordsResult`, with a fake tester failing `unauthorized` → `lastTestOk=false`, and the error stored
  - `TestEveryRouteIsClassified`, the existing test, still passes
  - In composes, `TestPullUnauthorizedAddsHint`: a fake runner `Pull` fails with `unauthorized: authentication required`, and the `DeployFailure.Message` ends with the pull hint copy.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Wire `registryauth.New` in `httpapi.NewHandler`, and call `d.RegistryAuth.Set(store)` there when `d.RegistryAuth` is not nil.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: manage and test External registries`

### Task 6: Registry page with the External registries tab, ADR-0034

**Files:**
- Create: `web/src/pages/RegistryPage.tsx`, `web/src/pages/registry-view.ts`, `web/src/components/ExternalRegistriesTab.tsx`
- Modify: `web/src/app/AppShell.tsx` (menu item `registry` after `builders`, icon `DatabaseOutlined`, label `Registry`), `web/src/app/paths.ts` (`registry: '/registry'`), `web/src/app/routes.tsx`, `web/src/data/api.ts`, `web/src/data/types.ts`
- Create: `docs/adr/0034-external-registry-credentials.md`
- Test: `web/src/pages/registry-view.test.ts`, `web/src/pages/RegistryPage.test.tsx`

**Interfaces:**
- Produces:
  - in `api.ts`: `listExternalRegistries`, `createExternalRegistry`, `updateExternalRegistry`, `deleteExternalRegistry` and `testExternalRegistry`
  - `RegistryPage` with Tabs keyed `repositories | credentials | external | settings`. Only `external` is filled now; the others render `Empty` until PR 2.
  - The tab is kept in `?tab=`.

- [ ] **Step 1: Write failing tests:**
  - The table shows host, username and a pass/fail Tag with relative time.
  - Edit leaves the secret empty, with placeholder `Unchanged`.
  - The form shows the token hint copy.
  - **Test** shows the error text on failure.
  - The `registry` menu item exists for admins only.
- [ ] **Step 2: Run** `pnpm --dir web test registry`. Expected: FAIL.
- [ ] **Step 3: Implement**, following `SshKeysPage.tsx` and `BuildersPage.tsx`.
- [ ] **Step 4: Write ADR-0034.**
  - **Decision:** a per-call `DOCKER_CONFIG` plus `BUILDX_CONFIG`. Credentials are host-wide and stored plaintext like SSH keys.
  - **Rejected:** a lingering `docker login`, and encryption at rest (the reasons are in the spec's Decisions).
  - **Probes:** the 2026-11-06 buildx `ls` result.
- [ ] **Step 5: Run** `pnpm --dir web test && pnpm --dir web typecheck && go test ./...`. Expected: PASS.
- [ ] **Step 6: Commit** `feat: add the Registry page with External registries`.

---

## PR 2 — Panel registry

### Task 7: Registry tables and store

**Files:**
- Create: `internal/migrations/00036_registry.sql`
- Create: `internal/registry/store.go`
- Test: `internal/registry/store_test.go`

**Interfaces:**
- Produces:
  - `type Settings struct { Enabled bool; Hostname string; TLS string /* "letsencrypt" | "certificate" */; SystemSecret string; LastError string }`
  - `type Credential struct { ID int64; Name, Hash, CreatedBy string; CreatedAt time.Time }`
  - `NewStore(db) *Store`
  - `Settings(ctx) (Settings, error)`. The one row (`id = 1`) is inserted by the migration with TLS `letsencrypt`, a NULL hostname (read as `""`) and an empty secret.
  - `SaveSettings(ctx, s Settings) error`
  - `SetLastError(ctx, msg string) error`
  - `Credentials(ctx) ([]Credential, error)`, ordered by name
  - `CreateCredential(ctx, c Credential) (Credential, error)`, which can return `ErrDuplicateName`
  - `DeleteCredential(ctx, id) (Credential, error)`

- [ ] **Step 1: Write failing tests:** `TestSettingsDefaultRow`, `TestCredentialDuplicateName` and `TestDeleteCredentialReturnsIt`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement** the tables `registry_settings` and `registry_credentials`.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat: store Registry settings and Push credentials`

### Task 8: Registry files

**Files:**
- Create: `internal/registry/files.go`
- Test: `internal/registry/files_test.go`

**Interfaces:**
- Produces:
  - `RenderConfig(readOnly bool) []byte`: the probed YAML.
    - `version: 0.1`, `log.level: info`
    - `storage.delete.enabled: true`, `storage.filesystem.rootdirectory: /var/lib/registry`, `storage.maintenance.readonly.enabled: <readOnly>`
    - `http.addr: :5000`, `http.relativeurls: false`
    - `auth.htpasswd: {realm: relist-registry, path: /etc/distribution/htpasswd}`
  - `RenderHtpasswd(systemHash string, creds []Credential) []byte`: `relist-panel:<hash>` first, then one `name:hash` line per credential, in name order
  - `type Files struct{ Dir string }`, where `Dir` is `<data dir>/.registry`
  - `(Files).Write(config, htpasswd []byte) error`, with an atomic rename per file, dir mode 0700, files 0644 (the registry runs as a non-root user)
  - `(Files).ReadOnly() (bool, error)`, which reads the flag back from `config.yml`

- [ ] **Step 1: Write failing tests:**
  - `TestRenderConfigReadOnly` (parse the YAML back and check the flag)
  - `TestRenderHtpasswdOrder`
  - `TestWriteIsAtomic`: write, then check there is no `*.tmp` and the contents match
- [ ] **Step 2–4: Run → implement → run.** Expected: FAIL, then PASS.
- [ ] **Step 5: Commit** `feat: render the Registry's config and htpasswd`

### Task 9: Docker port for the registry

**Files:**
- Create: `internal/registry/ports.go`
- Create: `internal/dockerexec/registry.go`
- Test: `internal/dockerexec/registry_test.go`, against an `httptest` Engine like `engine_test.go`

**Interfaces:**
- Produces:
  - `type ContainerSpec struct { Image, Hostname, TLS, Hash, FilesDir string }`
  - `type ContainerState struct { Exists, Running bool; Image string; Hash string /* label relist.registry.hash */ }`
  - `type Host interface {`
    - `EnsureImage(ctx, ref) error`
    - `Inspect(ctx) (ContainerState, error)`
    - `Create(ctx, ContainerSpec) error`, which removes any existing `relist-registry` first, then creates and starts it
    - `Restart(ctx) error`
    - `Remove(ctx) error`
    - `RemoveVolume(ctx) error`
    - `VolumeBytes(ctx) (int64, error)`, from `GET /system/df?type=volume`
    - `RunGC(ctx, image, filesDir string) error`, which creates and starts the detached `relist-registry-gc` without `--rm`
    - `GCState(ctx) (exists, running bool, exitCode int, logs string, err error)`
    - `RemoveGC(ctx) error`
  - `}`
  - `registry.DefaultImage`
  - Labels, as decided above:
    - router `relist-registry` on `websecure` with rule ``Host(`<hostname>`)``;
    - router `relist-registry-http` on `web` with redirect middleware `relist-registry-redirect`;
    - service port 5000, `traefik.docker.network=relist-network`;
    - `relist.registry.hash`.
  - `RestartPolicy` is `unless-stopped`.
  - GC `Cmd`: `["garbage-collect","--delete-untagged","/etc/distribution/config.yml"]`.

- [ ] **Step 1: Write failing tests:**
  - `TestCreateSpecLabelsLetsEncrypt` and `TestCreateSpecLabelsCertificate`, which assert the request body sent to `POST /containers/create`
  - `TestCreateMountsFilesDirReadOnly`
  - `TestGCCommand`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: drive the registry container through the Engine API`

### Task 10: Registry service (enable, disable, reconcile)

**Files:**
- Create: `internal/registry/service.go`
- Test: `internal/registry/service_test.go`, with a fake `Host` in `fakes_test.go`

**Interfaces:**
- Consumes: Tasks 7–9.
- Produces:
  - `type Deps struct { Store *Store; Host Host; Files Files; PanelDomain string; DomainHosts func(ctx) (map[string]string /* host → project name */, error); ComposesUsing func(ctx, host string) ([]string, error); Now func() time.Time }`
  - `New(Deps) *Service`
  - `Save(ctx, s Settings) error`, which validates and returns `ErrHostnameConflict{Message}`
  - `Enable(ctx) error`
  - `Disable(ctx) ([]string /* warnings */, error)`
  - `Reconcile(ctx) (drift bool, err error)`
  - `Status(ctx) (string, error)`, one of `off | running | read-only | stopped | drift | error`
  - `DeleteData(ctx, confirm string) error`
  - `Hostname(ctx) (string, bool)`
  - Enable generates `SystemSecret` on first use.

- [ ] **Step 1: Write failing tests:**
  - `TestHostnameConflicts`: the panel domain gives `<host> is the panel's own domain`; a Domain host gives `<host> is a Domain of <project>`
  - `TestEnablePullsWritesCreates`, in fake call order: EnsureImage, Files.Write, Create
  - `TestEnablePullFailureSetsLastError`
  - `TestReconcileRecreatesOnHashChange`
  - `TestReconcileStartsMissingContainer`
  - `TestReconcileLeavesDisabledAlone`
  - `TestDisableWarnsComposesUsing`
  - `TestDeleteDataRequiresDisabledAndHostname`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: enable, disable and reconcile the Panel registry`

### Task 11: Push credentials

**Files:**
- Create: `internal/registry/credentials.go`
- Test: `internal/registry/credentials_test.go`

**Interfaces:**
- Produces:
  - `(*Service).CreateCredential(ctx, name, createdBy string) (Credential, string /* token */, error)`
  - `(*Service).DeleteCredential(ctx, id) error`
  - Both rewrite `htpasswd` through `Files.Write`. There is no restart: the reload was probed.

- [ ] **Step 1: Write failing tests:**
  - `TestCreateCredentialStoresHashOnly`: the token is not in any store column, and `bcrypt.CompareHashAndPassword(hash, token)` succeeds
  - `TestCreateRejectsReservedName` (`relist-panel`)
  - `TestDeleteRewritesHtpasswd`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: create and revoke Push credentials`

### Task 12: Registry API client

**Files:**
- Create: `internal/registry/client.go`
- Test: `internal/registry/client_test.go`, with an `httptest` server that serves canned manifests

**Interfaces:**
- Produces:
  - `type Client struct { Base string /* http://relist-registry:5000 */; User, Secret string; HTTP *http.Client }`
  - `Repositories(ctx) ([]string, error)`, which follows `Link` pagination
  - `Tags(ctx, repo) ([]string, error)`
  - `TagInfo(ctx, repo, tag) (TagInfo, error)`
  - `Delete(ctx, repo, digest) error`
  - `type TagInfo struct { Tag, Digest string; Size int64; Created time.Time }`

- [ ] **Step 1: Write failing tests:**
  - `TestTagInfoReadsIndexSkippingAttestation`: an index has an `unknown/unknown` child first and a `linux/amd64` child second. Size and created come from the amd64 child, and the digest is the index's.
  - `TestTagInfoPlainManifest`
  - `TestRepositoriesFollowsLink`
  - `TestDeleteSendsDigest`, expecting 202
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: read and delete tags through the registry API`

### Task 13: Record each deploy's image refs

**Files:**
- Create: `internal/migrations/00037_compose_image_refs.sql`
- Modify: `internal/composes/deploy.go`: `prepareImages` returns `([]string, error)`, the image of every Service (the Compose-built name for `build:`)
- Modify: the repository in `internal/composes`: `setImageRefs` and `ImageRefs`
- Test: `internal/composes/deploy_test.go`

**Interfaces:**
- Produces:
  - `(*Service).ImageRefs(ctx) (map[string][]ComposeRef, error)`, keyed by ref
  - `type ComposeRef struct { ComposeID int64; Name, ProjectName, EnvironmentName string }`
  - The table is `compose_image_refs(compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE, ref TEXT NOT NULL, PRIMARY KEY (compose_id, ref))`. It is replaced on each successful deploy, after `markDeployed`, and in the `deploySubset` path too.

- [ ] **Step 1: Write failing tests:**
  - `TestDeployRecordsImageRefs`
  - `TestFailedDeployKeepsPreviousRefs`
  - `TestDeletedComposeDropsRefs`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: record the images each deploy used`

### Task 14: Repositories and settings API

**Files:**
- Create: `internal/registry/http.go` and `internal/registry/repositories.go`
- Modify: `internal/authz/actions.go`
- Test: `internal/registry/http_test.go`

**Interfaces:**
- Consumes: Tasks 10–13, and `vulnscan.InUse`.
- Produces routes:
  - `GET /api/registry` returns `{settings:{enabled, hostname, tls}, status, lastError, diskBytes, readTimeout}`. `readTimeout` comes from `traefikconfig.ReadForm` on the current override.
  - `PUT /api/registry` takes `{hostname, tls}`.
  - `POST /api/registry/enable`, `POST /api/registry/disable` (returns `{warnings}`), `POST /api/registry/repair`.
  - `POST /api/registry/check` returns `{ok, step: "dns"|"tls"|"auth"|"", error}`. It sends `GET https://<hostname>/v2/` with the system credential: a dial or resolve error is `dns`, an `x509`/TLS error is `tls`, and 401 is `auth`.
  - `DELETE /api/registry/data` takes `{confirm}`.
  - `GET /api/registry/credentials`, `POST /api/registry/credentials` (returns `{credential, token}`), `DELETE /api/registry/credentials/{id}`.
  - `GET /api/registry/repositories` returns `[{name, tagCount, newest}]`.
  - `GET /api/registry/repositories/{repo...}/tags` returns `[{tag, digest, size, created, inUseBy:[ComposeRef], kept:false}]`. `kept` is filled in PR 3.
  - `DELETE /api/registry/repositories/{repo...}/tags/{tag}` returns `{deleted:[tags]}`. It refuses a tag in use unless the body has `{confirm: "<tag>"}`.
  - Actions `registry.update`, `registry.enable`, `registry.disable`, `registry.repair`, `registry.delete_data`, `registry.credential_create`, `registry.credential_delete` and `registry.tag_delete`.
- "In use by" matches `<hostname>/<repo>:<tag>` against both sources. `InUse` refs come from image inspect `RepoTags`.

- [ ] **Step 1: Write failing tests:**
  - `TestTagsMarkInUseFromBothSources`
  - `TestDeleteTagListsSharedDigestTags`
  - `TestDeleteInUseTagNeedsConfirm` (409 without `confirm`)
  - `TestCreateCredentialReturnsTokenOnce`: the token is in the create response and absent from the list
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: serve the Registry's settings, credentials and repositories`

### Task 15: Wiring, pull credential, ownership and Domain guard

**Files:**
- Modify: `internal/httpapi/httpapi.go`, `main.go`
- Modify: `internal/domains/domains.go`
- Create: `internal/registry/source.go`
- Modify: `internal/composes/deploy.go` (hairpin hint)
- Test: `internal/registry/source_test.go`, `internal/domains/domains_test.go`, `internal/httpapi/httpapi_test.go`

**Interfaces:**
- Produces:
  - `(*Service).Auths(ctx) ([]registryauth.Auth, error)`, which returns `[{Host: hostname, Username: "relist-panel", Secret: SystemSecret}]` when enabled. `httpapi.NewHandler` calls `d.RegistryAuth.Set(registryauth.MultiSource(externalStore, registryService))`.
  - `domains.(*Service).WithReservedHost(func(ctx) (string, bool))`, which refuses with `409 <host> is the Registry's hostname`.
  - At start-up, `httpapi.NewHandler` runs `registryService.Reconcile` in the background and logs errors.
  - Ownership: in `owners`, `images[registry.DefaultImage] = "Registry"` and `volumes["relist-registry"] = "Registry"`.
  - Hairpin hint: a failed pull whose error names the registry hostname together with `dial tcp`, `no such host`, `connection refused` or `x509` appends the hairpin hint copy.

- [ ] **Step 1: Write failing tests:**
  - `TestAuthsEmptyWhenDisabled`
  - `TestDomainRefusesRegistryHost`
  - `TestOwnershipIncludesRegistry`
  - `TestPullHairpinHint`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Probe on the dev host.**
  - Enable the registry with hostname `registry.<dev domain>`.
  - From another machine (or the host), `docker login`, then push a 1.2 GB single-layer image.
  - Deploy a Compose using it.
  - Revoke the credential, and check that a push returns 401.
  - Record the timings for ADR-0033.
- [ ] **Step 6: Commit** `feat: wire the Panel registry into deploys and the Docker page`

### Task 16: Registry page tabs (frontend), ADR-0033, CONTEXT.md

**Files:**
- Create: `web/src/components/RegistryRepositoriesTab.tsx`, `RegistryCredentialsTab.tsx`, `RegistrySettingsTab.tsx`
- Modify: `RegistryPage.tsx`, `registry-view.ts`, `api.ts`, `types.ts`
- Create: `docs/adr/0033-panel-managed-registry.md`
- Modify: `CONTEXT.md` (the terms and the Image exception, per the spec's Domain changes)
- Test: `registry-view.test.ts`, `RegistryPage.test.tsx`

- [ ] **Step 1: Write failing tests:**
  - **Disabled:** Repositories and Push credentials show `Registry is off` with a link to Settings.
  - **Create credential:** the modal shows the token once, with copyable `docker login <hostname> -u <name>` and a `docker/login-action` snippet.
  - **Delete tag:** the confirm uses the delete tag copy with the shared tags; a tag in use needs its name typed.
  - **Settings:**
    - shows status, and `Repair` only on `drift`;
    - **Check endpoint** shows the failing step;
    - the danger zone is disabled while the registry is enabled;
    - shows `readTimeout` with a link to the Traefik page.
  - **Disable:** the warnings are shown.
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Write ADR-0033** from the spec's Domain changes, and include the probe results from this plan and from Task 15.
- [ ] **Step 6: Commit** `feat: add the Registry tabs`, and `docs: record the Panel registry decision`.

---

## PR 3 — Registry cleanup

### Task 17: Cleanup settings and history tables

**Files:**
- Create: `internal/migrations/00038_registry_cleanup.sql`
- Modify: `internal/registry/store.go`
- Test: `internal/registry/store_test.go`

**Interfaces:**
- Produces:
  - `type CleanupSettings struct { Enabled bool; Keep int; Days []string /* ["daily"] or weekdays */; Minute int; LastRunAt *time.Time }`. The defaults are true, 10, `["daily"]` and 180.
  - `CleanupSettings(ctx)` and `SaveCleanupSettings(ctx, c)`
  - `SetGCRunning(ctx, runID int64 /* 0 clears */) error` and `GCRunning(ctx) (int64, error)`
  - `type Run struct { ID int64; StartedAt time.Time; FinishedAt *time.Time; Trigger string /* "schedule" | "user:<name>" */; Deleted []string /* repo:tag */; FreedBytes int64; Result string /* running|ok|failed|interrupted */; Log string }`
  - `StartRun`, `FinishRun` and `Runs(ctx, limit int)`. Rows beyond the last 10 are deleted on `StartRun`.

- [ ] **Step 1–4:** Write the tests `TestCleanupDefaults`, `TestRunsKeepsLastTen` and `TestGCRunningRoundTrip` → run → implement → run.
- [ ] **Step 5: Commit** `feat: store Registry cleanup settings and runs`

### Task 18: Retention selection

**Files:**
- Create: `internal/registry/retention.go`
- Test: `internal/registry/retention_test.go`

**Interfaces:**
- Produces: `SelectDeletions(tags []TagInfo, inUse map[string]bool /* tag */, keep int) (deleteDigests []string, deletedTags []string)`, a pure function per repository.
  - Sort by `Created` descending, ties by tag name descending.
  - The newest `keep` tags and every in-use tag are kept.
  - A digest is deleted only if none of its tags is kept.
  - `deletedTags` lists every tag of the deleted digests.

- [ ] **Step 1: Write failing tests:**
  - `TestKeepsNewestN`
  - `TestKeepsInUse`
  - `TestSharedDigestKeptByAnyTag`
  - `TestTiesBreakByTagDesc`
  - `TestAllInUseDeletesNothing`
  - `TestKeepLargerThanTags`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: choose which Registry tags cleanup deletes`

### Task 19: Cleaner (run, GC, crash recovery, schedule)

**Files:**
- Create: `internal/registry/cleaner.go` and `internal/registry/schedule.go`
- Test: `internal/registry/cleaner_test.go` and `internal/registry/schedule_test.go`

**Interfaces:**
- Consumes: Tasks 9, 12, 17 and 18.
- Produces:
  - `type Gate interface { Busy() bool; Locked() bool }`. `httpapi` adapts `queue.Busy`, `panelUpdates.Locked` and the Migration lock.
  - `NewCleaner(svc *Service, gate Gate) *Cleaner`
  - `Preview(ctx) (map[string][]string /* repo → tags */, error)`
  - `RunNow(ctx, trigger string) (runID int64, error)`, which returns `ErrCleanupBusy` while one runs; the HTTP layer maps it to 409
  - `Startup(ctx) error`
  - `RunScheduler(ctx)`, which ticks each minute
  - `WaitIdle(ctx) error`
  - `Due(now time.Time, c CleanupSettings) bool`
  - `Next(now, c) *time.Time`

  `Due` and `Next` follow `vulnscan/scheduler.go`: a missed run is made up once, and only on a listed day.

**One run:**
1. Wait until `!gate.Busy() && !gate.Locked()`, polling every 10 s.
2. Read `VolumeBytes` (before).
3. For each repository, delete the selected digests. Before each `Delete`, call `TagInfo` again for every tag of the repository. Skip a digest that a tag outside `deletedTags` now points at.
4. Any error → `FinishRun(failed)` without GC.
5. Otherwise:
   - `SetGCRunning(runID)`;
   - write the config with `readOnly=true`, then `Restart`;
   - `RunGC`;
   - poll `GCState` every 2 s;
   - **always** write the config with `readOnly=false`, `Restart`, then `RemoveGC` and `SetGCRunning(0)`;
   - read `VolumeBytes` (after);
   - `FinishRun(ok|failed)` with the GC logs.
6. `Startup`: if `GCRunning` is non-zero, wait for `GCState` to stop running, then do the "always" part, and finish that run as `interrupted`.

- [ ] **Step 1: Write failing tests:**
  - `TestCleanupSkipsDigestRetaggedSinceSelection`
  - `TestRetentionErrorSkipsGC`
  - `TestGCFailureStillRestoresReadWrite`
  - `TestStartupFinishesInterruptedGC`
  - `TestStartupWaitsForRunningGCContainer`
  - `TestRunNowBusy`
  - `TestWaitsWhileDeploymentBusy`
  - `TestDueWeekdaysAndMakeUp`
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: run Registry cleanup with read-only garbage collection`

### Task 20: Cleanup API, wiring and UI

**Files:**
- Modify: `internal/registry/http.go`, `internal/httpapi/httpapi.go` (start `Cleaner.Startup` then `RunScheduler`; `puDeps.RegistryCleanup = cleaner`), `internal/panelupdate/service.go` and `watch_quiesce.go` (wait on `RegistryCleanup` after `Scans`), `internal/authz/actions.go`
- Modify: `RegistrySettingsTab.tsx`, `RegistryRepositoriesTab.tsx` (`Kept` Tag), `api.ts`, `types.ts`
- Test: `internal/registry/http_test.go`, `internal/panelupdate/watch_quiesce_test.go`, `RegistryPage.test.tsx`

**Interfaces:**
- Produces routes:
  - `GET /api/registry/cleanup` returns `{settings, next, runs}`
  - `PUT /api/registry/cleanup` takes the settings (action `registry.cleanup_update`)
  - `GET /api/registry/cleanup/preview`
  - `POST /api/registry/cleanup/run` (action `registry.cleanup_run`, trigger `user:<name>`)
- Tags gain `kept: true`, computed with `SelectDeletions`.

- [ ] **Step 1: Write failing tests:**
  - `TestQuiesceWaitsForRegistryCleanup`
  - `TestRunNowConflict` (409 copy)
  - The UI test: the schedule offers Daily and Weekly with weekday checkboxes and a time. **Run cleanup now** opens the preview, then runs. The history lists the last runs with freed size.
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Commit** `feat: schedule and run Registry cleanup from the Registry page`.

### Task 21: Migration option "Include registry data"

**Files:**
- Modify: `internal/migration/oldhost_http.go` (start body `includeRegistry`), `store.go` (`LockState.IncludeRegistry`), `lock.go`, `manifest.go` (`Build(ctx, running, hostPaths, includeRegistry bool)` appends `volume-relist-registry` when the volume exists), `target.go` (`Progress.IncludeRegistry`, sent in start)
- Modify: `internal/migrationtui/pull.go`: a checkbox `Include registry data (<size>)`, default off, shown only when the old host reports the volume
- Test: `internal/migration/manifest_test.go`, `internal/migration/target_test.go`, `internal/migrationtui/pull_test.go`

- [ ] **Step 1: Write failing tests:**
  - `TestManifestIncludesRegistryWhenAsked`
  - `TestManifestOmitsRegistryByDefault`
  - `TestStartBodyWithoutIncludeRegistryIsFalse`
  - the TUI test for the checkbox
- [ ] **Step 2–4: Run → implement → run.**
- [ ] **Step 5: Update** ADR-0033's Migration line.
- [ ] **Step 6: Commit** `feat: let a Migration carry the Registry's data`

### Task 22: Browser check

- [ ] Run the panel (`/run`), and as admin:
  - set the read timeout;
  - add, test and delete an External registry;
  - enable the registry and create a credential;
  - push two tags, then delete one;
  - run cleanup with N=1, and check that history shows bytes freed;
  - disable the registry with a Compose using it, and check the warning.
- [ ] Record pass or fail per item in the PR description.

## Self-review notes

- **Spec coverage:**

  | Spec item | Tasks |
  |---|---|
  | Traefik read timeout | 1–2 |
  | External registries | 3–6 |
  | Container and routing | 9–10, 15 |
  | Settings | 10, 14, 16 |
  | Push credentials | 11, 14, 16 |
  | Repositories | 12–14, 16 |
  | Reconcile | 10, 15 |
  | Cleanup | 17–20 |
  | Access | the Global constraint |
  | Server migration | 21 |
  | Error table | 5, 10, 14, 15, 19 |
  | Domain changes | 6, 16, 21 |
  | Testing | each task, and 22 |

- **Delivery:** four PRs as in the spec. Ask before each merge.
