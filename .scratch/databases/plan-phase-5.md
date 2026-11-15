# Databases phase 5 (Temporary access) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin opens a timed TCP proxy that publishes one Database on a host port, so DBeaver or TablePlus can reach it over an SSH tunnel. The proxy closes on time even when the panel is down.

**Architecture:**
- **A new program `relist-dbproxy`** (`cmd/relist-dbproxy` plus the pure package `internal/dbproxy`) forwards TCP to one fixed target and exits at a deadline. It rereads the deadline from a file, so Extend never drops connections.
- **The panel runs it** as the container `relist-dbaccess-<id>` from its own image, with a published port, over the Engine API in `internal/dockerexec`.
- **`internal/databases`** owns the rules: one access per Database, durations, reconcile, and closing on Database delete. The new table `database_accesses` is the truth for the expiry, because container labels cannot change on Extend.
- **The UI** is a card on the Connection tab.
- **Delivery:** one PR, branch `claude/databases-phase-5` on `claude/databases-phase-4b` (#86).

**Tech Stack:** Go (stdlib only, no new dependency), SQLite/goose, the Docker Engine API, React 19, antd 6.5.1, vitest, Playwright.

**Spec:** `.scratch/databases/spec-2.md`, section "Phase 5: Temporary access". Read `spec.md` "Runtime" for the Database container rules this sits beside.

## Global Constraints

- **Names.** The container is `relist-dbaccess-<database id>`. Labels: `relist.dbaccess.database=<id>` and `relist.dbaccess.expires=<unix seconds>`. The second is informational only; the table is the truth.
- **Proxy limits.** 64 concurrent connections; the 65th is closed at accept. The deadline file is reread every 2 s. Inside the container the proxy listens on the engine's port (5432, 3306 or 6379).
- **Container.**
  - `AutoRemove: true`, restart policy `no`, network `relist-network`.
  - Memory 32 MiB, `PidsLimit` 64, `CapDrop: ["ALL"]`, `SecurityOpt: ["no-new-privileges"]`.
  - `ReadonlyRootfs: true` with a 1 MiB tmpfs on `/tmp`; the deadline file is `/tmp/deadline`.
- **Durations.** Exactly 15, 60, 240 or 480 minutes; any other value is 400. Extend sets the expiry to now + duration, which is at most 8 h from now.
- **Bind.** The API takes `allInterfaces: bool`: false means `127.0.0.1`, true means `0.0.0.0`. No other address is accepted. The port is `0` (Docker picks) or 1024–65535.
- **Who.** Admin only, as every `/api/databases` route.
  - Audit actions: `database.access_open` (details `bind`, `port`, `minutes`), `database.access_extend` (`minutes`), and `database.access_close`.
  - Expiry and reconcile are not audited.
- **Migration.** One new file, `internal/migrations/00048_database_accesses.sql`. Never edit 00041–00047.
- **Panel image.** Resolve it with the existing `panelImage` (`internal/dockerexec/volume_runner.go`); add the binary to the Dockerfile beside `relist-volhelper`, built with `CGO_ENABLED=0`.
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **Go tests** run as `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`.

## Review Focus

1. **The panel restarts after an Extend.** Reconcile must read `expires_at` from the table, never the label, and leave the extended proxy running. Test in Task 4: `TestReconcileKeepsExtendedAccess`.
2. **The port is already in use.** Docker's start error becomes a 409 that names the port. No row is left behind and no container is left over. Tests in Task 3 (`TestStartAccessPortInUse`) and Task 4 (`TestOpenAccessFailureLeavesNothing`).
3. **A Database is deleted while access is open.** The proxy is removed before the Database container. Test in Task 4: `TestDeleteClosesAccess`.
4. **Open connections at expiry.** A client in the middle of a session is cut off at the deadline, not just refused new connections. Test in Task 1: `TestDeadlineClosesOpenConnections`.
5. **Extend after the proxy already exited.** The expiry passed between page load and click, and the exec fails because the container is gone. Answer 404 "no temporary access is open", delete the row, and never panic. Test in Task 4: `TestExtendAfterExpiry`.

---

### Task 1: `internal/dbproxy` and `cmd/relist-dbproxy`

**Files:**
- Create: `internal/dbproxy/proxy.go`, `internal/dbproxy/proxy_test.go`
- Create: `cmd/relist-dbproxy/main.go`, `cmd/relist-dbproxy/main_test.go`
- Modify: `Dockerfile` (build and copy `/usr/local/bin/relist-dbproxy`)

**Interfaces:**
- Produces:
  - `dbproxy.Serve(ctx context.Context, ln net.Listener, dial func(ctx context.Context) (net.Conn, error), deadline func() time.Time, maxConns int) error`. It returns nil when the deadline passes or ctx ends. Its `deadline` is polled.
  - `dbproxy.FileDeadline(path string, initial time.Time) func() time.Time`. It reads a unix-seconds integer from `path`; when the file is missing or unreadable it keeps the last good value, starting from `initial`.
  - `dbproxy.WriteDeadline(path string, t time.Time) error`. It writes atomically (a temp file, then rename).
  - The argv contract, which Task 3 relies on:
    - `relist-dbproxy serve --listen :<port> --target <host:port> --deadline <unix> --state /tmp/deadline`
    - `relist-dbproxy extend --state /tmp/deadline <unix>`

- [ ] **Step 1: Write the failing tests** in `proxy_test.go`. Use loopback listeners and an echo target.
  - `TestForwardsBothWays`: write "ping" and read "ping" back through the proxy.
  - `TestRefusesOverMaxConns`: with `maxConns=2`, a third conn reads EOF within 1 s while the first two still echo.
  - `TestDeadlineClosesOpenConnections`: the deadline is now+300ms. An open conn gets EOF or an error by 1 s, `Serve` returns nil, and a new dial is refused.
  - `TestDeadlineMovesLater`: the `deadline` func returns now+200ms and is then switched to now+2s. At now+600ms the conn still echoes.
  - `TestFileDeadline`: a missing file keeps `initial`; after `WriteDeadline` the new value is read; a garbage file keeps the last good value.
- [ ] **Step 2: Run** `go test ./internal/dbproxy/` and expect FAIL (undefined).
- [ ] **Step 3: Implement** `proxy.go`.
  - A semaphore of `maxConns`.
  - Each accepted conn is copied both ways with `io.Copy`, half-closing via `CloseWrite` when available.
  - A watcher checks `deadline()` at most every 2 s (tests may inject a shorter poll through an unexported var). At expiry it closes the listener and every tracked conn.
  - The deadline poll in `FileDeadline` uses the 2 s reread from Global Constraints.
- [ ] **Step 4: Run** and expect PASS. Also run `go test -race ./internal/dbproxy/`.
- [ ] **Step 5: Write `main_test.go`.**
  - `TestParseServe`: parses the argv above; a missing `--target` is an error; a `--deadline` already in the past exits 0 without listening.
  - `TestExtendWritesState`: `run([]string{"extend","--state",p,"1900000000"})` writes 1900000000.
  - `main` stays `os.Exit(run(os.Args[1:], ...))`, with dispatch in `run`, as `relist-volhelper` does.
- [ ] **Step 6: Implement `main.go`.**
  - `serve` dials `--target` with a 10 s timeout.
  - SIGTERM and SIGINT cancel ctx.
  - It writes the initial deadline to `--state` before listening.
- [ ] **Step 7: Update the Dockerfile.** Add `RUN CGO_ENABLED=0 go build -o /out/relist-dbproxy ./cmd/relist-dbproxy` and the matching `COPY` to `/usr/local/bin/relist-dbproxy`. Check with `docker build` against the sandbox `DOCKER_HOST` only (`. .data/e2e/env` first), or let `task e2e:reload` build it.
- [ ] **Step 8: Commit** `feat: add relist-dbproxy, a timed tcp forwarder for temporary access`.

### Task 2: Table and store

**Files:**
- Create: `internal/migrations/00048_database_accesses.sql`
- Create: `internal/databases/access_store.go`, `internal/databases/access_store_test.go`

**Interfaces:**
- Produces:
  - `type Access struct { DatabaseID int64; Bind string; HostPort int; ExpiresAt, OpenedAt time.Time }`
  - `(*Store) GetAccess(ctx, databaseID int64) (Access, bool, error)`
  - `(*Store) PutAccess(ctx, a Access) error`, which inserts and fails on an existing row
  - `(*Store) SetAccessExpiry(ctx, databaseID int64, t time.Time) error`
  - `(*Store) DeleteAccess(ctx, databaseID int64) error`
  - `(*Store) ListAccesses(ctx) ([]Access, error)`

- [ ] **Step 1: Write the migration.**
  - `database_accesses(database_id INTEGER PRIMARY KEY REFERENCES databases(id) ON DELETE CASCADE, bind TEXT NOT NULL, host_port INTEGER NOT NULL, expires_at INTEGER NOT NULL, opened_at INTEGER NOT NULL)`.
  - Times are unix seconds, as `00047`.
  - The header comment cites spec-2 phase 5 and ADR-0041.
- [ ] **Step 2: Write the failing tests.**
  - Round-trip `PutAccess`/`GetAccess`.
  - A second `PutAccess` for the same Database errors.
  - `SetAccessExpiry` changes only the expiry.
  - `DeleteAccess` on a missing row is not an error.
  - `ListAccesses` returns all rows.
  - The existing migration test still passes, which also means `00048` applies on top of `00047`.
- [ ] **Step 3: Implement** and run `go test ./internal/databases/ ./internal/migrations/`, expecting PASS.
- [ ] **Step 4: Commit** `feat: store temporary database access`.

### Task 3: Docker side of the proxy

**Files:**
- Modify: `internal/databases/ports.go` (add the interface and names)
- Create: `internal/dockerexec/databases_access.go`, `internal/dockerexec/databases_access_test.go`

**Interfaces:**
- Produces, in `internal/databases`:
  - `func AccessContainerName(id int64) string`, which returns `"relist-dbaccess-<id>"`.
  - `const AccessDatabaseLabel = "relist.dbaccess.database"` and `const AccessExpiresLabel = "relist.dbaccess.expires"`.
  - `type AccessSpec struct { DatabaseID int64; Target string /* relist-db-<id>:<port> */; ListenPort int; BindIP string; HostPort int /* 0 = Docker picks */; Deadline time.Time }`
  - `type AccessContainer struct { DatabaseID int64; Running bool; HostPort int }`
  - `var ErrPortInUse = errors.New("that port is already in use on the host")`. Wrap it with the port number so the message names it.
  - `type AccessHost interface`:
    - `StartAccess(ctx, AccessSpec) (hostPort int, err error)`: creates, starts, and reads back the published port;
    - `ListAccessContainers(ctx) ([]AccessContainer, error)`: by the `AccessDatabaseLabel` filter;
    - `RemoveAccess(ctx, databaseID int64) error`: force remove, where missing is not an error.
  - `Deps.Access AccessHost`, and `Deps.ProxyBinary string` defaulting to `/usr/local/bin/relist-dbproxy`.
- Consumes: Task 1's argv contract and the existing `Host.Exec`, used for `extend` in Task 4.

- [ ] **Step 1: Write the failing tests** with the existing fake-Engine pattern in `databases_test.go`.
  - `TestStartAccessRequest`: assert the create JSON has:
    - the image from an injected resolver;
    - `Entrypoint ["/usr/local/bin/relist-dbproxy"]`;
    - `Cmd ["serve","--listen",":5432","--target","relist-db-7:5432","--deadline","<unix>","--state","/tmp/deadline"]`;
    - `ExposedPorts {"5432/tcp":{}}`;
    - `PortBindings {"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":""}]}`;
    - `AutoRemove true`, `RestartPolicy no`, `NetworkMode relist-network`, `Memory 33554432`, `PidsLimit 64`, `CapDrop ["ALL"]`, `SecurityOpt ["no-new-privileges"]`, `ReadonlyRootfs true`, `Tmpfs {"/tmp":"size=1m"}`;
    - both labels.

    With `HostPort 15432`, the binding says `"15432"`. The published port comes back from the inspect after start.
  - `TestStartAccessPortInUse`: the start answers 500 with "port is already allocated", and the result is `errors.Is(err, databases.ErrPortInUse)`. The created container is removed.
  - `TestListAccessContainers` parses the label and the port from a list response.
  - `TestRemoveAccessMissing`: 404 is nil.
- [ ] **Step 2: Run** `go test ./internal/dockerexec/` and expect FAIL.
- [ ] **Step 3: Implement** `(*DatabaseHost) StartAccess`, `ListAccessContainers` and `RemoveAccess`. Use the injected image resolver, defaulting to `panelImage`. Assert `var _ databases.AccessHost = (*DatabaseHost)(nil)`.
- [ ] **Step 4: Run** and expect PASS. Then wire `Access: d.DatabaseHost` in `internal/httpapi/databases.go`.
- [ ] **Step 5: Commit** `feat: run the temporary access proxy container`.

### Task 4: Service rules

**Files:**
- Create: `internal/databases/access.go`, `internal/databases/access_test.go`
- Modify:
  - `internal/databases/fakes_test.go`: a fake `AccessHost` that records calls and can fail start;
  - `internal/databases/service.go`: Delete closes access first;
  - `internal/databases/jobs.go`: `Startup` runs reconcile, logging errors and never failing startup;
  - `internal/databases/scheduler.go`: `tick` runs reconcile.

**Interfaces:**
- Produces:
  - `type OpenAccessInput struct { AllInterfaces bool; HostPort int; Minutes int }`
  - `(*Service) OpenAccess(ctx, id int64, in OpenAccessInput) (Access, error)`
  - `(*Service) ExtendAccess(ctx, id int64, minutes int) (Access, error)`
  - `(*Service) CloseAccess(ctx, id int64) error`
  - `(*Service) GetAccess(ctx, id int64) (*Access, error)`: nil when there is none or it has expired
  - `(*Service) ReconcileAccess(ctx) error`
  - Errors:
    - `ErrAccessOpen` "temporary access is already open" (409);
    - `ErrNoAccess` "no temporary access is open" (404);
    - `ErrInvalidAccess` (400; message names the field: minutes or port);
    - `ErrPortInUse` (409).
- Consumes: Tasks 2 and 3, and `Host.Exec` for extend.

- [ ] **Step 1: Write the failing tests** with a fixed `Now`.
  - `TestOpenAccess`:
    - defaults bind to `127.0.0.1`, and `AllInterfaces` gives `0.0.0.0`;
    - the spec's `Target` is `relist-db-<id>:<engine port>`, `ListenPort` is the engine port, and `Deadline` is now+minutes;
    - the row is stored with the returned host port.
  - `TestOpenAccessRefuses`:
    - minutes 30 or a port of 80 give `ErrInvalidAccess`;
    - a second open gives `ErrAccessOpen`;
    - a stopped Database gives `ErrNotRunning`;
    - under the Migration lock it gives `ErrLocked`;
    - a tombstone gives `ErrNotFound`.
  - `TestOpenAccessFailureLeavesNothing`: when start fails with `ErrPortInUse`, there is no row and `RemoveAccess` was called.
  - `TestExtendAccess`: it execs `[ProxyBinary,"extend","--state","/tmp/deadline","<unix>"]` in `relist-dbaccess-<id>`, then updates the row. It refuses any minutes outside the allowed set.
  - `TestExtendAfterExpiry`:
    - a row past its expiry gives `ErrNoAccess`, with the row deleted and no exec;
    - when the exec fails because the container is gone, the result is also `ErrNoAccess` with the row deleted.
  - `TestCloseAccess` removes the container and the row. On none it gives `ErrNoAccess`.
  - `TestReconcileKeepsExtendedAccess`: a running container plus a row whose `expires_at` is in the future is kept, even with an older label value.
  - `TestReconcileRemoves`:
    - a container with no row: removed;
    - a row past its expiry: container removed and row deleted;
    - a row with no running container: row deleted.
  - `TestDeleteClosesAccess`: `Delete` calls `RemoveAccess` before removing the Database container, and the row is gone.
  - `TestGetAccessHidesExpired`.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Access` and expect FAIL.
- [ ] **Step 3: Implement.**
  - One `accessMu sync.Mutex` on the Service serialises open, extend, close and reconcile. Access never takes the job slot, so it does not block Backups.
  - Open order: validate, start the container, then `PutAccess`. If `PutAccess` fails, remove the container.
  - Exec errors from a missing container map to `ErrNoAccess`.
- [ ] **Step 4: Run** the package tests, then `-race`, and expect PASS.
- [ ] **Step 5: Commit** `feat: open, extend and close temporary database access`.

### Task 5: HTTP, audit, ADR and context

**Files:**
- Create: `internal/databases/access_http.go`, `internal/databases/access_http_test.go`
- Modify: `internal/databases/http.go` (routes and the `writeError` cases), `internal/authz/actions.go`
- Create: `docs/adr/0041-temporary-database-access.md`
- Modify: `CONTEXT.md` (term **Temporary access**)

**Interfaces:**
- Produces the routes, all admin-only:
  - `GET /api/databases/{id}/access` → `200 {"access": null | AccessJSON}`
  - `POST /api/databases/{id}/access` with body `{"allInterfaces": bool, "hostPort": number, "minutes": number}` → `201 AccessJSON`
  - `POST /api/databases/{id}/access/extend` with body `{"minutes": number}` → `200 AccessJSON`
  - `DELETE /api/databases/{id}/access` → `204`
  - `AccessJSON = {"bind": string, "hostPort": number, "expiresAt": RFC3339, "openedAt": RFC3339}`
  - authz: `"POST /api/databases/{id}/access": "database.access_open"`, `"POST /api/databases/{id}/access/extend": "database.access_extend"` and `"DELETE /api/databases/{id}/access": "database.access_close"`.

- [ ] **Step 1: Write the failing handler tests** with the `http_test.go` audit harness.
  - The status codes above, and the 400, 404 and 409 mappings.
  - The audit details on open: `bind`, `port`, `minutes`. On extend: `minutes`. The target is the Database name.
  - A body over 64 KiB, or bad JSON, gives 400.
  - The authz table test covers the three new actions.
- [ ] **Step 2: Run** and expect FAIL. **Implement**, run again and expect PASS.
- [ ] **Step 3: Write ADR-0041 "Temporary database access".**
  - Context: no host port by design (ADR-0039).
  - Decision: a timed proxy from the panel image, `127.0.0.1` by default, the deadline enforced by the proxy itself, and the table as the truth.
  - Consequences: Docker-published ports bypass UFW; nothing is carried by a Migration.
  - Add the **Temporary access** term to `CONTEXT.md` Language, with `_Avoid_: port forward, tunnel` (the tunnel is the Admin's SSH).
- [ ] **Step 4: Commit** `feat: serve temporary database access`.

### Task 6: Connection tab card

**Files:**
- Modify: `web/src/data/api.ts` (types and the four calls), `web/src/pages/DatabasePage.tsx` (the `ConnectionTab`)
- Create: `web/src/pages/DatabaseAccessCard.tsx`, `web/src/pages/DatabaseAccessCard.test.tsx`

**Interfaces:**
- Consumes: Task 5's routes and `AccessJSON`.
- Produces: `<DatabaseAccessCard db={Database} />`, rendered at the end of `ConnectionTab` for every engine.

- [ ] **Step 1: Read** `https://ant.design/llms.txt`, then the Card, Form, Select, InputNumber, Checkbox, Alert, Descriptions, Typography (copyable) and Statistic.Timer (countdown) pages for antd 6.5.1. Use Alert `title`, not `message`.
- [ ] **Step 2: Write the failing vitest tests.**
  - **When closed:**
    - the Duration select offers 15 minutes, 1 hour (default), 4 hours and 8 hours;
    - Port is optional, with the placeholder "Any free port";
    - ticking **All interfaces** shows a red Alert whose title is "Anyone on the internet can reach this Database" and whose text is "Docker publishes this port past host firewalls such as UFW. Only its password protects it.";
    - **Open access** posts `{allInterfaces, hostPort, minutes}`.
  - **When open:**
    - it shows the bind, the host port and a countdown to `expiresAt`;
    - the copyable SSH command is ``ssh -N -L <hostPort>:127.0.0.1:<hostPort> <user>@${location.hostname}`` with `<user>` literal. With `0.0.0.0`, it shows `${location.hostname}:<hostPort>` instead of the SSH command;
    - the copyable URL has no password: `postgres://postgres@127.0.0.1:<hostPort>/postgres`, built with the same engine rules as the existing Connection URL, with a note "Use the password above";
    - **Extend** (with a duration select) and **Close now** (Popconfirm) are present.
  - A stopped Database disables Open with the reason "Start the Database first".
  - The countdown reaching zero refetches.
- [ ] **Step 3: Run** `pnpm --dir web test -- DatabaseAccessCard --maxWorkers=6` and expect FAIL. **Implement**, run again and expect PASS. Then run `pnpm --dir web typecheck` and `pnpm --dir web build`.
- [ ] **Step 4: Commit** `feat: add the temporary access card to the connection tab`.

### Task 7: End-to-end

**Files:**
- Create: `e2e/tests/databases-access.spec.ts`

- [ ] **Step 1: Write the spec.** Use the `databases-backup.spec.ts` pattern: `docker` with the sandbox `DOCKER_HOST`/`DOCKER_CONFIG`. Run clients as `docker run --rm --network host <engine image> …` so `127.0.0.1` is the dind host. The test makes a Postgres and a Redis through the API.
  - Through the UI, open access on the Postgres for 15 minutes. Read the port from the card.
  - `psql postgresql://postgres:<pw>@127.0.0.1:<port>/postgres -c 'select 1'` succeeds.
  - Redis: `redis-cli -h 127.0.0.1 -p <port> -a <pw> ping` gives PONG.
  - Extend keeps a held session alive: start `psql -c 'select pg_sleep(8)'` in the background, extend, and the sleep finishes with exit 0.
  - Close now, then `psql` fails to connect, and `docker ps -a --filter name=relist-dbaccess-` is empty.
  - A second open on the same port number while the first is open gives 409 through the API.
  - Deleting the Database with access open removes the proxy container.
  - A short deadline: set the row's expiry in the past (`docker exec` into the panel container to run `sqlite3` if it is there, otherwise skip with a note), wait for the next tick, and the container is gone. **Do not wait out real 15-minute expiries in e2e;** Task 1's unit tests own the deadline behaviour.
  - The audit list holds `database.access_open`, `database.access_extend` and `database.access_close`.
- [ ] **Step 2: Run** `task e2e:reload`, then `task e2e -- databases-access` and expect PASS. Then run the full `task e2e`.
- [ ] **Step 3: Commit** `test: cover temporary database access end to end`.

### Final checks (implementer)

- [ ] `env -u DOCKER_CONFIG -u DOCKER_HOST go vet ./... && env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`, plus `-race` on `./internal/dbproxy/ ./internal/databases/`.
- [ ] Web typecheck, vitest (`--maxWorkers=6`) and build.
- [ ] Full `task e2e`.
- [ ] A manual check following `docs/agents/live-checks.md`: connect DBeaver-style through `ssh -L` to the sandbox if possible, otherwise through `docker run --network host`. Screenshot the card open and closed.
