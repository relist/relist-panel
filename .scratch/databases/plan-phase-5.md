# Databases phase 5 (Direct access) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin opens a timed Direct access that publishes one Database on a host port, so DBeaver or TablePlus can reach it over an SSH tunnel. The proxy closes on time even when the panel is down. The model is generic (target, channel, expiry), so later phases can add Compose Services, Tailscale and Cloudflare without a new table.

**Architecture:**
- **`relist-proxy`** (`cmd/relist-proxy` plus the pure package `internal/tcpproxy`) forwards TCP to one fixed target and exits at a deadline. It rereads the deadline from a file, so Extend never drops connections.
- **`internal/directaccess`** is a new package that owns:
  - the `direct_accesses` table;
  - the rules (durations, one per Database for now, reconcile);
  - the routes `/api/direct-accesses`.

  It knows targets only through a `Targets` interface. `internal/databases` implements the `database` kind.
- **`internal/dockerexec`** runs the proxy as `relist-access-<access id>` from the panel's own image.
- **`internal/databases`** closes a Database's Direct accesses before deleting it, through a small interface in its `Deps`.
- **The UI** is a card on the Database's Connection tab.
- **Delivery:** one PR, branch `claude/databases-phase-5` on `claude/databases-phase-4b` (#86).

**Tech Stack:** Go (stdlib only, no new dependency), SQLite/goose, the Docker Engine API, React 19, antd 6.5.1, vitest, Playwright.

**Spec:** `.scratch/databases/spec-2.md`, section "Phase 5: Direct access". Read `spec.md` "Runtime" for the Database container rules this sits beside.

## Global Constraints

- **Domain term:** **Direct access**. The parts are **target** (`target_kind`, `target_id`, `target_port`), **channel** (`channel`, `channel_config`) and **expiry**.
- **Phase 5 values:**
  - target kind `database`;
  - channel `host_port` with `channel_config` `{"bind":"127.0.0.1"|"0.0.0.0","hostPort":<n>}`.

  Any other kind or channel is 400 "not supported yet".
- **Names.**
  - Container: `relist-access-<access id>`.
  - Labels: `relist.access.id=<id>`, `relist.access.target=<kind>:<id>`, and `relist.access.expires=<unix>`. The expires label is informational only; the table is the truth.
  - Program: `/usr/local/bin/relist-proxy`.
- **Proxy limits.**
  - 64 concurrent connections; the 65th is closed at accept.
  - The deadline file `/tmp/deadline` is reread every 2 s.
  - Inside the container it listens on the target port (5432, 3306 or 6379).
- **Container.**
  - `AutoRemove: true`, restart policy `no`, and the network the target names (`relist-network` for a Database).
  - Memory 32 MiB, `PidsLimit` 64, `CapDrop: ["ALL"]`, `SecurityOpt: ["no-new-privileges"]`.
  - `ReadonlyRootfs: true`, plus a 1 MiB tmpfs on `/tmp`.
- **Durations.**
  - Exactly 15, 60, 240 or 480 minutes; anything else is 400.
  - A `host_port` channel always has an expiry.
  - Extend sets the expiry to now + duration, which is at most 8 h from now.
- **Bind and port.**
  - The API takes `allInterfaces: bool`: false means `127.0.0.1`, true means `0.0.0.0`. No other address is accepted.
  - The port is `0` (Docker picks) or 1024–65535.
- **One per Database** is a service rule. The table has no unique constraint on the target.
- **Who.** Admin only. `/api/direct-accesses` is left out of authz `Routes`, as `/api/databases` is (ADR-0039).
  - Audit actions: `direct_access.open` (details `target`, `channel`, `bind`, `port`, `minutes`), `direct_access.extend` (`minutes`) and `direct_access.close`.
  - The audit target is the target's display name, for example the Database name.
  - Expiry and reconcile are not audited.
- **Migration.** One new file, `internal/migrations/00048_direct_accesses.sql`. Never edit 00041–00047.
- **Panel image.** Resolve it with the existing `panelImage` (`internal/dockerexec/volume_runner.go`). Add the binary to the Dockerfile beside `relist-volhelper`, built with `CGO_ENABLED=0`.
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **Go tests** run as `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`.

## Review Focus

1. **The panel restarts after an Extend.** Reconcile must read `expires_at` from the table, never the label, and leave the extended proxy running. Test in Task 4: `TestReconcileKeepsExtendedAccess`.
2. **The port is already in use.** Docker's start error becomes a 409 that names the port, with no row and no container left behind. Tests: `TestStartProxyPortInUse` (Task 3) and `TestOpenFailureLeavesNothing` (Task 4).
3. **A Database is deleted while a Direct access is open.** The proxy is removed before the Database container. Test in Task 4: `TestDeleteDatabaseClosesAccess`.
4. **Open connections at expiry.** A client mid-session is cut at the deadline, not only refused new connections. Test in Task 1: `TestDeadlineClosesOpenConnections`.
5. **Extend after the proxy already exited.** The expiry passed between page load and click, so the exec fails on a gone container. The result is 404 "this Direct access has ended", the row is deleted, and there is no panic. Test in Task 4: `TestExtendAfterExpiry`.

---

### Task 1: `internal/tcpproxy` and `cmd/relist-proxy`

**Files:**
- Create: `internal/tcpproxy/proxy.go` and `internal/tcpproxy/proxy_test.go`
- Create: `cmd/relist-proxy/main.go` and `cmd/relist-proxy/main_test.go`
- Modify: `Dockerfile` (build and copy `/usr/local/bin/relist-proxy`)

**Interfaces:**
- Produces:
  - `tcpproxy.Serve(ctx context.Context, ln net.Listener, dial func(ctx context.Context) (net.Conn, error), deadline func() time.Time, maxConns int) error` returns nil when the deadline passes or ctx ends. `deadline` is polled.
  - `tcpproxy.FileDeadline(path string, initial time.Time) func() time.Time` reads a unix-seconds integer from `path`. A missing or garbage file keeps the last good value.
  - `tcpproxy.WriteDeadline(path string, t time.Time) error` writes the file atomically (temp file, then rename).
  - The argv contract, which Task 3 relies on:
    - `relist-proxy serve --listen :<port> --target <host:port> --deadline <unix> --state /tmp/deadline`
    - `relist-proxy extend --state /tmp/deadline <unix>`

- [ ] **Step 1: Write the failing tests** in `proxy_test.go`, with loopback listeners and an echo target:
  - `TestForwardsBothWays`: "ping" in, "ping" back.
  - `TestRefusesOverMaxConns`: with `maxConns=2`, a third conn reads EOF within 1 s while the first two still echo.
  - `TestDeadlineClosesOpenConnections`: with the deadline at now+300ms, an open conn gets EOF or an error by 1 s, `Serve` returns nil, and a new dial is refused.
  - `TestDeadlineMovesLater`: the `deadline` func returns now+200ms and is then switched to now+2s. At now+600ms the conn still echoes.
  - `TestFileDeadline`: a missing file keeps `initial`; `WriteDeadline` is then read; a garbage file keeps the last good value.
- [ ] **Step 2: Run** `go test ./internal/tcpproxy/` and expect FAIL (undefined).
- [ ] **Step 3: Implement.**
  - A semaphore of `maxConns`.
  - Each conn is copied both ways with `io.Copy`, half-closing with `CloseWrite` when available.
  - A watcher checks `deadline()` every 2 s; tests may shorten this through an unexported var.
  - At expiry it closes the listener and every tracked conn.
- [ ] **Step 4: Run** the tests and `go test -race ./internal/tcpproxy/`, and expect PASS.
- [ ] **Step 5: Write `main_test.go`.**
  - `TestParseServe`: a missing `--target` is an error; a past `--deadline` exits 0 without listening.
  - `TestExtendWritesState`: `run([]string{"extend","--state",p,"1900000000"})` writes 1900000000.
  - `main` is `os.Exit(run(os.Args[1:], ...))`, as `relist-volhelper` does.
- [ ] **Step 6: Implement.**
  - `serve` dials `--target` with a 10 s timeout.
  - SIGTERM and SIGINT cancel ctx.
  - It writes the initial deadline to `--state` before listening.
- [ ] **Step 7: Update the Dockerfile** with `RUN CGO_ENABLED=0 go build -o /out/relist-proxy ./cmd/relist-proxy` and the matching `COPY`. Build only against the sandbox `DOCKER_HOST` (`. .data/e2e/env`), or let `task e2e:reload` build it.
- [ ] **Step 8: Commit** `feat: add relist-proxy, a timed tcp forwarder for direct access`.

### Task 2: Table and store

**Files:**
- Create: `internal/migrations/00048_direct_accesses.sql`
- Create: `internal/directaccess/store.go` and `internal/directaccess/store_test.go`

**Interfaces:**
- Produces:
  - `type Target struct { Kind string; ID int64; Port int }`
  - `type HostPort struct { Bind string `json:"bind"`; HostPort int `json:"hostPort"` }`
  - `type Access struct { ID int64; Target Target; Channel string; HostPort HostPort /* channel_config when Channel=="host_port" */; ExpiresAt *time.Time; OpenedBy int64; OpenedAt time.Time }`
  - `const KindDatabase = "database"` and `const ChannelHostPort = "host_port"`
  - `NewStore(db *sql.DB) *Store`
  - `(*Store) Insert(ctx, a Access) (int64, error)`
  - `(*Store) SetHostPort(ctx, id int64, port int) error`
  - `(*Store) SetExpiry(ctx, id int64, t time.Time) error`
  - `(*Store) Get(ctx, id int64) (Access, error)`, giving `ErrNotFound` when missing
  - `(*Store) ForTarget(ctx, kind string, id int64) ([]Access, error)`
  - `(*Store) List(ctx) ([]Access, error)`
  - `(*Store) Delete(ctx, id int64) error`, where missing is not an error

- [ ] **Step 1: Write the migration.**
  - Columns: `direct_accesses(id INTEGER PRIMARY KEY AUTOINCREMENT, target_kind TEXT NOT NULL, target_id INTEGER NOT NULL, target_port INTEGER NOT NULL, channel TEXT NOT NULL, channel_config TEXT NOT NULL, expires_at INTEGER, opened_by INTEGER, opened_at INTEGER NOT NULL)`.
  - An index on `(target_kind, target_id)`.
  - No foreign key, because the target kinds live in different tables.
  - Times are unix seconds, as `00047`.
  - A header comment cites spec-2 phase 5 and ADR-0041.
- [ ] **Step 2: Write the failing tests.**
  - An Access round-trips through `Insert` and `Get`, including the `channel_config` JSON and a nil `ExpiresAt`.
  - `SetHostPort` and `SetExpiry` each change only their column.
  - `ForTarget` returns only that target's rows.
  - `Delete` of a missing row is nil.
  - The existing migration test passes with `00048`.
- [ ] **Step 3: Implement**, run `go test ./internal/directaccess/ ./internal/migrations/`, and expect PASS.
- [ ] **Step 4: Commit** `feat: store direct accesses`.

### Task 3: Docker side of the proxy

**Files:**
- Create: `internal/directaccess/ports.go` (the interfaces below)
- Create: `internal/dockerexec/direct_access.go` and `internal/dockerexec/direct_access_test.go`

**Interfaces:**
- Produces, in `internal/directaccess`:
  - `func ContainerName(id int64) string` returns `"relist-access-<id>"`.
  - Labels: `const IDLabel = "relist.access.id"`, `TargetLabel = "relist.access.target"` and `ExpiresLabel = "relist.access.expires"`.
  - `type ProxySpec struct { AccessID int64; TargetLabel string /* "database:7" */; Network, Upstream string /* "relist-db-7:5432" */; ListenPort int; BindIP string; HostPort int /* 0 = Docker picks */; Deadline time.Time }`
  - `type ProxyContainer struct { AccessID int64; Running bool }`
  - `var ErrPortInUse = errors.New("that port is already in use on the host")`, wrapped so the message names the port.
  - `type Host interface`:
    - `StartProxy(ctx, ProxySpec) (hostPort int, err error)` creates, starts, and reads the published port back from Inspect.
    - `ListProxies(ctx) ([]ProxyContainer, error)` filters by `IDLabel`.
    - `RemoveProxy(ctx, accessID int64) error` force-removes; a missing container is nil.
    - `Extend(ctx, accessID int64, deadline time.Time) error` execs `relist-proxy extend --state /tmp/deadline <unix>`. A missing or stopped container gives `ErrGone`.
  - `var ErrGone = errors.New("the proxy is no longer running")`

- [ ] **Step 1: Write the failing tests** with the fake-Engine pattern of `internal/dockerexec/databases_test.go`.
  - `TestStartProxyRequest` asserts the create JSON:
    - the image from an injected resolver;
    - `Entrypoint ["/usr/local/bin/relist-proxy"]`;
    - `Cmd ["serve","--listen",":5432","--target","relist-db-7:5432","--deadline","<unix>","--state","/tmp/deadline"]`;
    - `ExposedPorts {"5432/tcp":{}}` and `PortBindings {"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":""}]}`;
    - `AutoRemove true`, `RestartPolicy no`, `NetworkMode relist-network`;
    - `Memory 33554432`, `PidsLimit 64`, `CapDrop ["ALL"]`, `SecurityOpt ["no-new-privileges"]`;
    - `ReadonlyRootfs true`, `Tmpfs {"/tmp":"size=1m"}`;
    - the three labels.

    Also: `HostPort 15432` gives `"15432"`, and the published port is read from the inspect after start.
  - `TestStartProxyPortInUse`: a start answering 500 "port is already allocated" gives `errors.Is(err, directaccess.ErrPortInUse)`, and the created container is removed.
  - `TestListProxies` parses the ID label.
  - `TestRemoveProxyMissing`: 404 is nil.
  - `TestExtendGone`: an exec-create on a missing or stopped container gives `ErrGone`.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/` and expect FAIL.
- [ ] **Step 3: Implement** `(*DatabaseHost)` methods, or a new `ProxyHost` sharing its `Engine` and `TerminalExec`; choose whichever keeps `databases.go` from growing. Assert `var _ directaccess.Host = ...`.
- [ ] **Step 4: Run** the tests and expect PASS.
- [ ] **Step 5: Commit** `feat: run the direct access proxy container`.

### Task 4: Service, Database target and lifecycle

**Files:**
- Create: `internal/directaccess/service.go`, `internal/directaccess/service_test.go` and `internal/directaccess/fakes_test.go`
- Create: `internal/databases/direct_target.go` and `internal/databases/direct_target_test.go`
- Modify:
  - `internal/databases/service.go`: `Deps.DirectAccess DirectAccessCloser`; `Delete` calls it first.
  - `internal/httpapi/databases.go` and `internal/httpapi/httpapi.go`: wiring. Start `directaccess.Service.Run(ctx)` beside the database scheduler; it reconciles at start and every minute.

**Interfaces:**
- Produces, in `internal/directaccess`:
  - `type Endpoint struct { Name string /* display, for audit */; Network, Host string; Port int; Running bool }`
  - `type Targets interface { Resolve(ctx context.Context, kind string, id int64) (Endpoint, error) }`; an unknown or deleted target gives `ErrTargetNotFound`.
  - `type Deps struct { Store *Store; Host Host; Targets Targets; Locked func(ctx context.Context) bool; Now func() time.Time }`
  - `type OpenInput struct { TargetKind string; TargetID int64; Channel string; AllInterfaces bool; HostPort int; Minutes int; OpenedBy int64 }`
  - `(*Service) Open(ctx, in OpenInput) (Access, error)`
  - `(*Service) Extend(ctx, id int64, minutes int) (Access, error)`
  - `(*Service) Close(ctx, id int64) error`
  - `(*Service) ForTarget(ctx, kind string, id int64) ([]Access, error)`, which hides expired rows
  - `(*Service) CloseTarget(ctx, kind string, id int64) error`
  - `(*Service) Reconcile(ctx) error`
  - `(*Service) Run(ctx)`
  - Errors:

    | Error | Status | Notes |
    |---|---|---|
    | `ErrInvalid` | 400 | message names the field: minutes, port, kind or channel |
    | `ErrUnsupported` | 400 | "not supported yet" |
    | `ErrOpen` | 409 | "this Database already has a Direct access" |
    | `ErrNotFound` | 404 | "this Direct access has ended" |
    | `ErrTargetNotFound` | 404 | |
    | `ErrTargetStopped` | 409 | "start the Database first" |
    | `ErrLocked` | 409 | |
    | `ErrPortInUse` | 409 | |

- Produces, in `internal/databases`:
  - `type DirectAccessCloser interface { CloseTarget(ctx context.Context, kind string, id int64) error }`
  - `(*Service) DirectTarget() directaccess.Targets` resolves `database` to `Endpoint{Name: d.Name, Network: Network, Host: ContainerName(id), Port: engine.Port(), Running: <status running>}`; a tombstone gives `ErrTargetNotFound`.
  - Keep imports one-way: `databases` may import `directaccess`, never the reverse.

- [ ] **Step 1: Write the failing service tests** with a fixed `Now`, a fake Host and fake Targets.
  - `TestOpen`:
    - the bind defaults to `127.0.0.1`; `AllInterfaces` gives `0.0.0.0`;
    - the ProxySpec has `Upstream "relist-db-7:5432"`, `ListenPort 5432`, `TargetLabel "database:7"` and `Deadline` now+minutes;
    - the row is stored with the returned host port.
  - `TestOpenRefuses`:
    - minutes 30 or port 80 give `ErrInvalid`;
    - kind `service` or channel `tailscale` gives `ErrUnsupported`;
    - a second open on the same Database gives `ErrOpen`;
    - a stopped target gives `ErrTargetStopped`;
    - the Migration lock gives `ErrLocked`;
    - a missing target gives `ErrTargetNotFound`.
  - `TestOpenFailureLeavesNothing`: start fails with `ErrPortInUse`, leaving no row, and `RemoveProxy` was called for the inserted ID. The row is inserted first to get the ID, so it is deleted on failure.
  - `TestExtend`: calls `Host.Extend` with now+minutes, then updates the row; disallowed minutes are refused.
  - `TestExtendAfterExpiry`:
    - a row past its expiry gives `ErrNotFound` with the row deleted and no `Host.Extend` call;
    - `Host.Extend` returning `ErrGone` gives `ErrNotFound` with the row deleted.
  - `TestClose` removes the container and the row; a missing ID gives `ErrNotFound`.
  - `TestReconcileKeepsExtendedAccess`: a running container plus a future `expires_at` is kept.
  - `TestReconcileRemoves`:
    - a container with no row: removed;
    - a row past its expiry: container removed and row deleted;
    - a row with no running container: row deleted.
  - `TestCloseTarget` closes every access of that target.
- [ ] **Step 2: Write the failing databases tests.**
  - `TestDirectTargetResolve`: running, stopped and tombstone.
  - `TestDeleteDatabaseClosesAccess`: `Delete` calls `CloseTarget("database", id)` before removing the Database container, and a `CloseTarget` error stops the delete.
- [ ] **Step 3: Run** `go test ./internal/directaccess/ ./internal/databases/` and expect FAIL.
- [ ] **Step 4: Implement.**
  - One `sync.Mutex` serialises Open, Extend, Close and Reconcile. Direct access never takes a Database's job slot.
  - Open order: validate, resolve, insert the row (for the ID), start the proxy, then `SetHostPort`. On any failure after the insert, remove the proxy and delete the row.
- [ ] **Step 5: Run** the tests and `-race` on both packages, and expect PASS.
- [ ] **Step 6: Commit** `feat: open, extend and close direct access to a database`.

### Task 5: HTTP, audit, ADR and context

**Files:**
- Create: `internal/directaccess/http.go` and `internal/directaccess/http_test.go`
- Modify: `internal/authz/actions.go` (and `record.go` if the resource map needs `direct-accesses`), `internal/httpapi/httpapi.go` (register the routes)
- Create: `docs/adr/0041-direct-access.md`
- Modify: `CONTEXT.md` (term **Direct access**)

**Interfaces:**
- Produces the routes, all admin-only:
  - `GET /api/direct-accesses?targetKind=database&targetId=7` → `200 {"accesses": AccessJSON[]}`
  - `POST /api/direct-accesses` with body `{"targetKind":"database","targetId":7,"channel":"host_port","allInterfaces":false,"hostPort":0,"minutes":60}` → `201 AccessJSON`
  - `POST /api/direct-accesses/{id}/extend` with body `{"minutes":60}` → `200 AccessJSON`
  - `DELETE /api/direct-accesses/{id}` → `204`
  - `AccessJSON = {"id":n,"targetKind":"database","targetId":n,"targetPort":n,"channel":"host_port","bind":"127.0.0.1","hostPort":n,"expiresAt":RFC3339|null,"openedAt":RFC3339}`
  - authz actions: `direct_access.open`, `direct_access.extend` and `direct_access.close`.

- [ ] **Step 1: Write the failing handler tests** with the audit harness pattern from `internal/databases/http_test.go`.
  - The statuses and every error mapping in the Task 4 table.
  - The audit details and target named in Global Constraints. `OpenedBy` comes from the session user.
  - A body over 64 KiB, or bad JSON, gives 400.
  - The authz table test covers the three actions, and a non-Admin gets 403 on all four routes.
- [ ] **Step 2: Run** and expect FAIL. **Implement**, run again, and expect PASS.
- [ ] **Step 3: Write ADR-0041 "Direct access".**
  - Context: there is no host port by design (ADR-0039), and the operator needs desktop tools.
  - Decision:
    - a generic model of target, channel and expiry;
    - a proxy from the panel image;
    - the panel enforces nothing on the channel; the channel's own control is the guard;
    - `host_port` is `127.0.0.1` by default and always expires;
    - the deadline is enforced by the proxy itself, and the table is the truth.
  - Consequences: Docker-published ports bypass UFW; nothing is carried by a Migration; future channels (`tailscale`, `cloudflare`) and kinds (`service`) need no new table.
  - Add the **Direct access** term to `CONTEXT.md` Language, with `_Avoid_: tunnel, port forward, gateway` (a tunnel is one possible channel, not the thing).
- [ ] **Step 4: Commit** `feat: serve direct access`.

### Task 6: Connection tab card

**Files:**
- Modify: `web/src/data/api.ts` (types and the four calls) and `web/src/pages/DatabasePage.tsx` (`ConnectionTab`)
- Create: `web/src/pages/DirectAccessCard.tsx` and `web/src/pages/DirectAccessCard.test.tsx`

**Interfaces:**
- Consumes: the routes and `AccessJSON` from Task 5.
- Produces: `<DirectAccessCard db={Database} />` at the end of `ConnectionTab` for every engine. It is written against a target `{kind, id, port, running}` and `db` adapts to it, so a later Service page can reuse it.

- [ ] **Step 1: Read** `https://ant.design/llms.txt`, then the Card, Form, Select, InputNumber, Checkbox, Alert, Descriptions, Typography (copyable), Statistic.Timer and Popconfirm pages for antd 6.5.1. Use Alert `title`, not `message`.
- [ ] **Step 2: Write the failing vitest tests.**
  - **Closed state:**
    - The title is "Direct access".
    - The Duration select offers 15 minutes, 1 hour (the default), 4 hours and 8 hours.
    - Port is optional, with the placeholder "Any free port".
    - **All interfaces** shows a red Alert with the title "Anyone on the internet can reach this Database" and the text "Docker publishes this port past host firewalls such as UFW. Only its password protects it."
    - **Open** posts the body from Task 5.
  - **Open state:**
    - It shows the bind, the host port and a countdown to `expiresAt`.
    - The copyable command is ``ssh -N -L <hostPort>:127.0.0.1:<hostPort> <user>@${location.hostname}``, with `<user>` literal. With `0.0.0.0` it shows `${location.hostname}:<hostPort>` instead.
    - The copyable URL has no password, for example `postgres://postgres@127.0.0.1:<hostPort>/postgres`, built with the same engine rules as the Connection URL. A note says "Use the password above".
    - **Extend** (with a duration select) and **Close now** (Popconfirm) are present.
  - A stopped Database disables Open with "Start the Database first".
  - The countdown reaching zero refetches.
- [ ] **Step 3: Run** `pnpm --dir web test -- DirectAccessCard --maxWorkers=6` and expect FAIL. **Implement**, run again, and expect PASS. Then run `pnpm --dir web typecheck` and `pnpm --dir web build`.
- [ ] **Step 4: Commit** `feat: add the direct access card to the connection tab`.

### Task 7: End-to-end

**Files:**
- Create: `e2e/tests/direct-access.spec.ts`

- [ ] **Step 1: Write the spec.**
  - Follow the `docker` pattern of `databases-backup.spec.ts`: the sandbox `DOCKER_HOST`/`DOCKER_CONFIG`, and clients run as `docker run --rm --network host <engine image> …`, so `127.0.0.1` is the dind host.
  - Make a Postgres and a Redis through the API, then check:
    - Open a Direct access on the Postgres through the UI for 15 minutes, and read the port from the card. `psql postgresql://postgres:<pw>@127.0.0.1:<port>/postgres -c 'select 1'` succeeds.
    - Redis through the API: `redis-cli -h 127.0.0.1 -p <port> -a <pw> ping` gives PONG.
    - Extend keeps a held session: `psql -c 'select pg_sleep(8)'` runs in the background, Extend is clicked, and it exits 0.
    - Close now: `psql` then fails to connect, and `docker ps -a --filter name=relist-access-` is empty.
    - A second open on the same Database gives 409.
    - Deleting the Database with an open access removes the proxy container.
    - The audit list holds `direct_access.open`, `direct_access.extend` and `direct_access.close`.
  - **Do not wait out real expiries in e2e.** Task 1 owns the deadline behaviour.
- [ ] **Step 2: Run** `task e2e:reload`, then `task e2e -- direct-access`, and expect PASS. Then run the full `task e2e`.
- [ ] **Step 3: Commit** `test: cover direct access end to end`.

### Final checks (implementer)

- [ ] `env -u DOCKER_CONFIG -u DOCKER_HOST go vet ./... && env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`, and `-race` on `./internal/tcpproxy/ ./internal/directaccess/ ./internal/databases/`.
- [ ] Web typecheck, vitest (`--maxWorkers=6`) and build.
- [ ] The full `task e2e`.
- [ ] A manual check following `docs/agents/live-checks.md`: connect through `ssh -L` to the sandbox if possible, otherwise through `docker run --network host`. Take screenshots of the card open and closed.
