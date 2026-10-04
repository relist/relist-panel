# Compose terminal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Terminal tab on the Compose page opens an interactive TTY shell (bash/sh, auto-detected; image user or root) in a running Container of a chosen Service, over a WebSocket.

**Architecture:** A new domain package `internal/terminal` owns the HTTP/WebSocket surface and session lifecycle behind one consumer-declared seam, `terminal.Exec`. Its real adapter, `internal/dockerexec/terminal.go`, drives Engine API exec over the Docker socket (create → start with a hijacked `Upgrade: tcp` stream → resize → inspect). The browser runs xterm.js; a React-free `terminal-session.ts` owns the frame protocol and state machine. Shell cleanup on disconnect and the startup sweep are **conditional** on the Task 1 spike.

**Tech Stack:** Go 1.25 stdlib `net/http` + `github.com/coder/websocket`; React 19 + antd 6.5.1 + `@xterm/xterm` + `@xterm/addon-fit`; vitest.

**Spec:** [.scratch/compose-terminal/spec.md](spec.md)

## Global Constraints

- Commits: Conventional Commits, no scope; **no** `Co-Authored-By` or `Claude-Session` trailers (AGENTS.md).
- Copy uses CONTEXT.md vocabulary: Compose, Service, Container. Never "stack", "app", "console", "SSH".
- `shell` ∈ `auto | bash | sh` → `/bin/bash`, `/bin/sh`. `user` ∈ `default | root` → `User: ""` / `User: "0"`. No free-text shell, command or user, ever.
- `rows`, `cols` ∈ 1–500 on the URL (400 otherwise); `resize` frames are clamped to 1–500.
- Server-wide cap: **10** concurrent sessions. Read limit: **64 KiB** per frame. Ping every **30 s**, pong timeout **10 s**. Kill grace **2 s**; kill execs run on their own `context.WithTimeout(5s)`.
- Error copy, verbatim from the spec:
  - `Service "<name>" has no running container — deploy or start it first`
  - `bash is not installed in this image — use sh`
  - `No shell found in this image (/bin/bash, /bin/sh)`
  - `Too many open terminal sessions (10) — close one first`
  - Docker API failures pass Docker's own message through unchanged.
- WebSocket Origin check is `coder/websocket`'s default: no `OriginPatterns`, no `InsecureSkipVerify`.
- The terminal routes are `GET` outside `/volumes`, so the project coordinator never gates them. Do not change `coordination.go`.
- Before any edit under `web/`: read `https://ant.design/llms.txt` and the pages for the components used, read `docs/GUIDELINES.md`, and check props against antd 6.5.1 (AGENTS.md).
- Behaviour that needs a real Docker daemon is verified manually (Tasks 1 and 11), never through fakes.

## Review Focus

- **A `container` id that belongs to a different Service, a different Compose, or the panel itself** must be refused with the no-running-container `error`, and no exec may be created. The id comes from the URL, so this is the one place a crafted request could reach outside the Compose (Task 5).
- **Editor ↔ Terminal tab switching** must not remount `ComposePage`. Today the Editor and the `/:tab` routes are separate route objects, so the session would die on the first switch to Editor (Task 2).
- **A shell that exits immediately or a stream that hits EOF before any output** (container removed by a redeploy mid-session) must reach the browser as `exit` or close, never a hung socket. It must also release its cap slot (Task 5).
- **A burst of `resize` frames while a slow Docker resize call is in flight** must not block keystrokes. Resize runs off the input pump (Task 5).
- **A cross-Origin upgrade carrying a valid session cookie** must be refused before any exec (Task 5).

## File Structure

- Create `internal/terminal/terminal.go`: `Exec` seam, types, `Options`, `Service`, `RegisterRoutes`, the `services` handler.
- Create `internal/terminal/session.go`: the WebSocket handler: validation, upgrade, shell resolution, cap, pumps, ping, exit.
- Create `internal/terminal/faketest/faketest.go` (+ `faketest_test.go`): the scripted `Exec` fake.
- Create `internal/dockerexec/terminal.go` (+ `terminal_test.go`): the Engine API adapter.
- Modify `internal/dockerexec/containersource.go`: export nothing new; `terminal.go` reuses the same unix-socket dialer, extracted to `newSocketClient(socketPath) *http.Client`.
- Modify `internal/httpapi/httpapi.go`: `NewHandler` gains `terminalOptions *terminal.Options` (nil means the routes are not registered).
- Create `internal/httpapi/terminal_test.go`: the HTTP/WebSocket seam tests. They live here, next to `volumeops_test.go`, because they need the auth and Compose fixtures in `httpapi_test.go`.
- Modify `internal/httpapi/httpapi_test.go`, `legacy_identity_test.go`, `lifecycle_regression_test.go`: pass the new argument.
- Modify `main.go`: wire the real adapter (and, if Task 7 runs, the sweep).
- Modify `web/src/app/paths.ts`, `web/src/app/routes.tsx`, `web/src/app/paths.test.ts`: one optional-segment Compose route.
- Modify `web/src/pages/compose-view.ts` (+ test): `ComposeTab` gains `'terminal'`.
- Create `web/src/data/terminal-session.ts` (+ `terminal-session.test.ts`): frames, URL builder, state machine.
- Modify `web/src/data/api.ts`, `web/src/data/types.ts`: `terminalServices`.
- Create `web/src/components/ComposeTerminalTab.tsx`; modify `web/src/pages/ComposePage.tsx`, `web/src/app/theme.ts`.
- Create `docs/adr/0012-interactive-shell-over-engine-api-exec.md`; modify `CONTEXT.md`, `docs/GUIDELINES.md`, `docs/adr/0003-go-architecture.md` (seam count note).

---

### Task 1: Spike — does a TTY exec outlive its attach connection on Docker 27?

Throwaway. The output is one recorded answer, not code.

**Files:**
- Modify: `.scratch/compose-terminal/spec.md` (append `## Spike result`)

- [ ] **Step 1: Start a target container**

```bash
docker run -d --name relist-spike alpine:3 sleep 3600
```

- [ ] **Step 2: Open a TTY exec, then kill the client hard (no close handshake)**

```bash
script -qc "docker exec -it relist-spike sh -c 'exec sleep 1001'" /dev/null & CLIENT=$!
sleep 2; kill -9 $CLIENT; sleep 3
docker top relist-spike
```

`script` supplies the TTY that `-t` needs. Killing the CLI drops the attach connection exactly as the panel would.

- [ ] **Step 3: Repeat with an interactive shell running a foreground job**

```bash
script -qc "docker exec -it relist-spike sh" /dev/null <<'EOF' & CLIENT=$!
top
EOF
sleep 2; kill -9 $CLIENT; sleep 3
docker top relist-spike
```

- [ ] **Step 4: Record the answer and clean up**

Append to `spec.md` under `## Spike result`: the Docker version (`docker version --format '{{.Server.Version}}'`), the `docker top` output of both runs, and one line: **LEAKS** (a `sleep 1001`, `sh` or `top` is still listed) or **CLEAN**.

```bash
docker rm -f relist-spike
```

**Gate:** **LEAKS** → Tasks 6 and 7 run. **CLEAN** → skip Tasks 6 and 7 and say so in the ADR (Task 11).

- [ ] **Step 5: Commit**

```bash
git add .scratch/compose-terminal/spec.md
git commit -m "docs: record whether a tty exec outlives its client"
```

---

### Task 2: One Compose route, so tab switches never remount the page

**Files:**
- Modify: `web/src/app/paths.ts` (the two `compose/:composeId` entries in `routePaths`)
- Modify: `web/src/app/routes.tsx` (`elements` keys)
- Modify: `web/src/pages/compose-view.ts` (`ComposeTab`, `activeComposeTab`)
- Test: `web/src/app/paths.test.ts`, `web/src/pages/compose-view.test.ts`

**Interfaces:**
- Produces: `ComposeTab = 'editor' | 'logs' | 'terminal' | 'monitor' | 'volumes'`; route pattern `projects/:projectId/env/:environmentId/compose/:composeId/:tab?`.

- [ ] **Step 1: Write the failing tests**

```ts
// paths.test.ts
it('serves the Editor and every Compose tab from one route, so the page is not remounted', () => {
  const editor = resolvePath('/projects/1/env/2/compose/3');
  const terminal = resolvePath('/projects/1/env/2/compose/3/terminal');
  expect(editor?.pattern).toBe('projects/:projectId/env/:environmentId/compose/:composeId/:tab?');
  expect(terminal?.pattern).toBe(editor?.pattern);
  expect(terminal?.params.tab).toBe('terminal');
  expect(resolvePath('/projects/1/env/2/compose/3/volumes/explorer')?.pattern).toBe(
    'projects/:projectId/env/:environmentId/compose/:composeId/volumes/explorer',
  );
});

// compose-view.test.ts
it('recognises the terminal tab', () => {
  expect(activeComposeTab('terminal')).toBe('terminal');
});
```

- [ ] **Step 2: Run, expect FAIL**

Run: `pnpm --dir web test -- paths compose-view`
Expected: FAIL. The pattern is `…/compose/:composeId` for the Editor, and `activeComposeTab('terminal')` returns `'editor'`.

- [ ] **Step 3: Implement**

Replace the two Compose entries in `routePaths` and `elements` with the single `…/compose/:composeId/:tab?`, keep the explorer entry, and add `'terminal'` to `ComposeTab` and `activeComposeTab`. Optional segments are supported by the installed react-router. Search `web/src` for the old pattern string and update any caller that compares `pattern`.

- [ ] **Step 4: Run, expect PASS, then typecheck**

Run: `pnpm --dir web test && pnpm --dir web typecheck`
Expected: all pass.

- [ ] **Step 5: Manual check**

Run the dev server, open a Compose, type in the Logs filter, then switch Editor → Logs → Editor. The Monaco undo history survives, which proves the page was not remounted.

- [ ] **Step 6: Commit**

```bash
git add web/src/app web/src/pages/compose-view.ts web/src/pages/compose-view.test.ts
git commit -m "fix: keep the compose page mounted across its tabs"
```

---

### Task 3: `terminal` package, fake, services listing and wiring

**Files:**
- Create: `internal/terminal/terminal.go`, `internal/terminal/faketest/faketest.go`, `internal/terminal/faketest/faketest_test.go`
- Modify: `internal/httpapi/httpapi.go:30-49`, `main.go:84` (passes `nil` until Task 4), and the three test files that call `NewHandler`
- Test: `internal/httpapi/terminal_test.go`

**Interfaces:**
- Produces, in package `terminal`:

```go
type Container struct {
    ID     string `json:"id"`
    Name   string `json:"name"`
    Number int    `json:"number"`
}
type ExecOptions struct {
    Shell      string // "/bin/bash" or "/bin/sh"
    User       string // "" or "0"
    Rows, Cols uint16
}
type Session interface {
    io.ReadWriteCloser
    Resize(ctx context.Context, rows, cols uint16) error
    ExitCode(ctx context.Context) (int, error)
}
type Exec interface {
    Available(ctx context.Context) error
    ServiceContainers(ctx context.Context, project, service string) ([]Container, error)
    HasShell(ctx context.Context, containerID, path string) (bool, error)
    Start(ctx context.Context, containerID string, opts ExecOptions) (Session, error)
}
type Options struct {
    Exec         Exec
    MaxSessions  int           // 0 → 10
    PingInterval time.Duration // 0 → 30s
    PingTimeout  time.Duration // 0 → 10s
    KillGrace    time.Duration // 0 → 2s (used only if Task 6 runs)
}
func New(composeService *composes.Service, opts Options) *Service
func (s *Service) RegisterRoutes(mux *http.ServeMux)
```

- `faketest.Exec`: `Containers map[string][]terminal.Container` (keyed `project/service`), `Shells map[string]bool` (keyed `containerID:path`), `AvailableErr`, `StartErr error`, `ExitCode int`, and records `Starts []StartCall{ContainerID string; Opts terminal.ExecOptions}`. Each `Start` returns a `*faketest.Session` built on `net.Pipe`: `Remote net.Conn` is the container's side, which the test reads and writes, and `Resizes [][2]uint16` is guarded by a mutex. `Session.Closed() <-chan struct{}`.
- Produces in `httpapi`: `NewHandler(db, runner, dataDir, history, services, volumeRunner, volumeResolver, terminalOptions *terminal.Options)`.
- `GET …/composes/{composeID}/terminal/services` responds:

```json
{"available": true, "reason": "",
 "services": [{"name": "app", "containers": [{"id": "c1", "name": "relist-c3-app-1", "number": 1,
                                               "shells": {"bash": true, "sh": true}}]}]}
```

Service names come from the Compose's status (`ComposeRunner.Status`, already used by the Compose page). Services with no running Container appear with `"containers": []`. When `Exec.Available` fails, the response is `available: false`, `reason` set to the error text, and `services: []`, with status 200, as `volumeapi.list` does.

- [ ] **Step 1: Write the failing tests** in `internal/httpapi/terminal_test.go`

Add `terminal *terminalfake.Exec` to `testServer` and construct it in `newTestServer`. Then:
- `TestTerminalServicesListsRunningContainersAndShells`: two Services, `app` (one Container with bash and sh) and `worker` (none). Assert the JSON above, and that `worker` has `containers: []`.
- `TestTerminalServicesReportsTheFeatureUnavailable`: `AvailableErr = errors.New("docker socket not reachable")` → `available: false` with that `reason`.
- `TestTerminalServicesRequiresAKnownComposeAndParents`: an unknown compose/env/project → 404.
- `TestTerminalServicesRequiresAuthentication`: no cookie → 401.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/httpapi/ -run TerminalServices -v`
Expected: FAIL (does not compile: `terminal` package missing).

- [ ] **Step 3: Implement**

Write the `terminal` package and the fake. In `NewHandler`, when `terminalOptions != nil`, register `terminal.New(composesService, *terminalOptions).RegisterRoutes(mux)`. Pass `nil` from `legacy_identity_test.go` and `lifecycle_regression_test.go`. `main.go` passes `nil` for now; Task 4 wires the real adapter. Resolve the Compose through `composes.ComposeRunRequest`. `sql.ErrNoRows` → 404, as in `volumeapi`.

- [ ] **Step 4: Run, expect PASS**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/terminal internal/httpapi main.go
git commit -m "feat: list a compose's terminal targets"
```

---

### Task 4: Engine API exec adapter

**Files:**
- Create: `internal/dockerexec/terminal.go`
- Modify: `internal/dockerexec/containersource.go` (extract `newSocketClient`), `main.go:84`
- Test: `internal/dockerexec/terminal_test.go`

**Interfaces:**
- Consumes: `terminal.Exec`, `terminal.Container`, `terminal.ExecOptions`, `terminal.Session` (Task 3).
- Produces: `dockerexec.NewTerminalExec(socketPath string) *TerminalExec`, implementing `terminal.Exec`.

- [ ] **Step 1: Write the failing tests**

They run against an `httptest.Server` on a unix listener in `t.TempDir()`. Its handler records each request and scripts answers. For `/exec/{id}/start`, it answers `101 Switching Protocols` with `Upgrade: tcp`, hijacks, echoes one line back, and closes.

- `TestTerminalExecServiceContainersFiltersByComposeLabels`: the request is `GET /containers/json` with a `filters` query decoding to `{"label":["com.docker.compose.project=relist-c3","com.docker.compose.service=app"]}`. Two scripted containers numbered `"2"` and `"1"` come back ordered `1, 2`, with `Name` stripped of its leading `/`.
- `TestTerminalExecHasShellReadsTheExitCode`: exec create body `Cmd == ["/bin/bash","-c","exit 0"]`, `Tty == false`. Inspect `ExitCode: 0` → `true`. A create answering `404 {"message":"…executable file not found…"}`, or a start whose inspect returns non-zero, → `false, nil`. A `500` → error.
- `TestTerminalExecStartHijacksATtyStream`: create body has `Tty: true`, all three `Attach*` true, `User` as passed (`""` omitted, `"0"` kept), `Env: ["TERM=xterm-256color"]`, `ConsoleSize: [rows, cols]`. The start request carries `Connection: Upgrade`, `Upgrade: tcp` and body `{"Detach":false,"Tty":true}`. Bytes written to the returned `Session` reach the server and the echoed line is readable.
- `TestTerminalExecSessionResizeAndExitCode`: `Resize(ctx, 40, 120)` is `POST /exec/{id}/resize?h=40&w=120`; `ExitCode` reads `ExitCode` from `GET /exec/{id}/json`.
- `TestTerminalExecAvailablePingsTheDaemon`: `GET /_ping` 200 → nil; dial failure → error whose text names the socket path.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/dockerexec/ -run TerminalExec -v`
Expected: FAIL (undefined: `NewTerminalExec`).

- [ ] **Step 3: Implement `internal/dockerexec/terminal.go`**

- `ServiceContainers`, `HasShell`, `Available` use plain requests through the `newSocketClient` client.
- `Start` cannot use `http.Client`, because it needs the raw connection. Dial the unix socket directly, write the request with `req.Write(conn)`, read the response with `http.ReadResponse(bufio.NewReader(conn), req)`, and require `101`. The `Session` reads through that same `bufio.Reader` (bytes already buffered after the headers belong to the stream) and writes to `conn`.
- Non-2xx answers become `fmt.Errorf("docker api %s: %s: %s", path, status, message)`, matching `ContainerSource.get`, so Docker's own words reach the operator.

- [ ] **Step 4: Wire it and run, expect PASS**

In `main.go`, replace Task 3's `nil` with `&terminal.Options{Exec: dockerexec.NewTerminalExec(dockerexec.DefaultSocketPath)}`.

Run: `go build ./... && go test ./internal/dockerexec/ -v`
Expected: PASS, including the existing tests.

- [ ] **Step 5: Commit**

```bash
git add internal/dockerexec main.go
git commit -m "feat: drive interactive exec through the docker engine api"
```

---

### Task 5: The WebSocket session

**Files:**
- Create: `internal/terminal/session.go`
- Modify: `go.mod`, `go.sum` (`go get github.com/coder/websocket@latest`)
- Test: `internal/httpapi/terminal_test.go`

**Interfaces:**
- Consumes: Task 3's `Exec`, `Options` and fake.
- Produces: `GET …/composes/{composeID}/terminal?service=&container=&shell=&user=&rows=&cols=`, speaking the frames in spec §Frames. Control frames are JSON text frames with `type` ∈ `resize` (in), and `ready | exit | error` (out). `ready` carries `shell` (`"bash"`/`"sh"`), `user` (`"default"`/`"root"`) and `container` (the Container `Name`).

Test helper: `dialTerminal(t, server, query string, header http.Header) (*websocket.Conn, *http.Response, error)`. It starts `httptest.NewServer(server.handler)`, copies the session cookie into `header`, and dials with `websocket.DialOptions{HTTPHeader: header}`. Tests run with `Options{MaxSessions: 10, PingInterval: 50 * time.Millisecond, PingTimeout: 50 * time.Millisecond, KillGrace: 50 * time.Millisecond}` set in `newTestServer`.

- [ ] **Step 1: Write the failing tests**

- `TestTerminalSessionPumpsBytesBothWays`: after `ready`, a client binary frame `"ls\r"` is readable on `Remote`, and `Remote` writing `"out"` arrives as a binary frame `"out"`.
- `TestTerminalSessionReadyNamesTheResolvedTarget`: with `shell=auto`, bash present → `ready` `{"shell":"bash","user":"default","container":"relist-c3-app-1"}` and `Starts[0].Opts == {Shell:"/bin/bash", User:"", Rows:24, Cols:80}`. With bash absent, `shell` is `"sh"` and `Shell` is `"/bin/sh"`.
- `TestTerminalSessionRunsAsRootOnRequest`: `user=root` → `Starts[0].Opts.User == "0"`, and `ready.user == "root"`.
- `TestTerminalSessionForwardsClampedResizes`: frames `{"type":"resize","rows":40,"cols":120}` then `{"type":"resize","rows":9000,"cols":0}` → `Resizes` eventually equals `[[40 120] [500 1]]`.
- `TestTerminalSessionReportsAMissingShell`: `shell=bash` without bash → `error` with `bash is not installed in this image — use sh`, then close. With neither shell and `shell=auto` → `No shell found in this image (/bin/bash, /bin/sh)`. No `Start` recorded.
- `TestTerminalSessionRefusesAContainerOutsideTheService`: `container=` the id of another Service's Container, an id from another Compose, and `deadbeef` each get `error` `Service "app" has no running container — deploy or start it first`, with no `Start` and no `HasShell` call on the foreign id. **(Review Focus)**
- `TestTerminalSessionDefaultsToTheLowestNumberedContainer`: with `container` omitted and Containers numbered 2 and 1, `Starts[0].ContainerID` is the number-1 id.
- `TestTerminalSessionReportsTheExitCode`: `ExitCode = 3`, `Remote.Close()` → `{"type":"exit","code":3}` then close status 1000.
- `TestTerminalSessionEndsWhenTheStreamEndsBeforeOutput`: `Remote.Close()` immediately after `ready` → `exit` arrives, and an 11th session can then open (slot released). **(Review Focus)**
- `TestTerminalSessionEnforcesTheSessionCap`: 10 open sessions, the 11th gets `Too many open terminal sessions (10) — close one first`. Close one, and a new one gets `ready`.
- `TestTerminalSessionEndsWhenPongsStop`: the client stops reading after `ready` (so it answers no pings), and the fake `Session.Closed()` fires within 1 s.
- `TestTerminalSessionKeystrokesFlowDuringASlowResize`: the fake's `Resize` blocks on a channel; after sending a resize, a keystroke frame still reaches `Remote`. **(Review Focus)**
- `TestTerminalSessionRefusesACrossOriginUpgrade`: header `Origin: http://evil.example` with a valid cookie → the dial fails, the response status is 403, and no `Start` is recorded. **(Review Focus)**
- `TestTerminalSessionValidatesParametersBeforeUpgrade`: `shell=zsh`, `user=nobody`, `rows=0`, `cols=501` and a missing `service` → 400 each. An unknown Compose → 404. No cookie → 401.
- `TestTerminalSessionPassesDockerErrorsThrough`: `StartErr = errors.New("docker api /containers/x/exec: 409 Conflict: container is paused")` → `error.message` equals that text.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/httpapi/ -run TerminalSession -v`
Expected: FAIL (404 on the route).

- [ ] **Step 3: Implement `internal/terminal/session.go`**

The order inside the handler is fixed by the spec:
1. Validate query → 400.
2. Resolve Compose → 404.
3. `websocket.Accept(w, r, nil)`, with Origin checked by default.
4. `SetReadLimit(64 << 10)`.
5. Take a cap slot, a buffered `chan struct{}` of `MaxSessions` with a non-blocking send. On failure, send the cap `error`.
6. `ServiceContainers` and pick the container. A `container` id not in that list counts as none.
7. Resolve the shell via `HasShell`.
8. `Start` and send `ready`.

Run three goroutines (output pump, input pump, ping loop) plus a resize worker that receives on a 1-slot channel where newer sizes replace stale ones. The first to end cancels the rest. `defer` releases the slot and closes the `Session`. Exit code: after the output pump hits EOF, `ExitCode` on `context.WithTimeout(context.Background(), 5*time.Second)`.

- [ ] **Step 4: Run, expect PASS, and run with the race detector**

Run: `go test -race ./internal/httpapi/ ./internal/terminal/...`
Expected: PASS, no races.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/terminal internal/httpapi/terminal_test.go
git commit -m "feat: open an interactive shell in a service over a websocket"
```

---

### Task 6 (only if Task 1 recorded LEAKS): Kill the shell on session end

**Files:**
- Modify: `internal/terminal/terminal.go` (`Exec` gains `Kill`), `internal/terminal/session.go`, `internal/terminal/faketest/faketest.go`, `internal/dockerexec/terminal.go`
- Test: `internal/httpapi/terminal_test.go`, `internal/dockerexec/terminal_test.go`

**Interfaces:**
- Produces: `Exec.Kill(ctx context.Context, containerID string, pid int, sig string) error` and `terminal.ErrNoProcess`, returned when the kill exec exits non-zero (the pid is gone). `sig == "0"` is the liveness probe. The fake records `Kills []KillCall{ContainerID string; PID int; Signal string}`; `KillExits map[int]bool` makes a `HUP` end that pid, after which `Kill(pid, "0")` returns `ErrNoProcess`.

- [ ] **Step 1: Write the failing tests**

- `TestTerminalSessionHidesTheWrapperPID`: `Remote` writes `"4242\r\n$ "` → the client receives exactly `"$ "`.
- `TestTerminalSessionHangsUpTheShellOnDisconnect`: after `ready` (PID 4242), the client closes. The `Kills` entries other than signal `"0"` are `[{c1 4242 HUP} {c1 4242 KILL}]`. With `KillExits[4242] = true` they are `[{c1 4242 HUP}]` only.
- `TestTerminalSessionStartsTheMarkedWrapper`: `Starts[0]` command is `["/bin/sh","-c","echo $$; exec \"$1\"","relist-terminal","/bin/bash"]`. Expose the command as `ExecOptions.Cmd []string` built by the session, so the adapter stays dumb.
- In `dockerexec`: `TestTerminalExecKillUsesTheShellBuiltinAsRoot` → create body `User:"0"`, `Cmd:["/bin/sh","-c","kill -HUP 4242"]`, `Tty:false`.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/httpapi/ ./internal/dockerexec/ -run 'Wrapper|HangsUp|Kill' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Read the PID line before `ready`: up to the first `\n`, `\r` trimmed, at most 32 bytes, and it must parse as a positive int, otherwise send an `error` and close. On session end, `Kill(pid, "HUP")`, wait `KillGrace`, `Kill(pid, "0")`, and `Kill(pid, "KILL")` only if that did not return `ErrNoProcess`. All of this runs on `context.WithTimeout(context.Background(), 5*time.Second)`.

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/...`
Expected: PASS.

- [ ] **Step 5: Manual check**: repeat Task 1 Step 3 through the panel instead of `docker exec`. `docker top` shows no `sh` or `top` within 3 s of closing the browser tab.

- [ ] **Step 6: Commit**

```bash
git add internal
git commit -m "fix: hang up a terminal's shell when its session ends"
```

---

### Task 7 (only if Task 1 recorded LEAKS): Sweep marked shells at panel start

**Files:**
- Modify: `internal/terminal/terminal.go` (`Exec` gains `OrphanShells`), `internal/terminal/faketest/faketest.go`, `internal/dockerexec/terminal.go`, `main.go`
- Create: `internal/terminal/sweep.go`
- Test: `internal/terminal/sweep_test.go`, `internal/dockerexec/terminal_test.go`

**Interfaces:**
- Produces: `Exec.OrphanShells(ctx context.Context, projects []string) ([]OrphanShell, error)` with `OrphanShell{ContainerID string; PID int}`, and `terminal.Sweep(ctx context.Context, exec Exec, projects []string, logf func(string, ...any))`.

- [ ] **Step 1: Write the failing tests**

- `dockerexec`, `TestTerminalExecOrphanShellsFindsRunningMarkedExecs`: for each Container of the given project labels, `GET /containers/{id}/json` → `ExecIDs`. `GET /exec/{id}/json` keeps only `Running: true` with `ProcessConfig.arguments[2] == "relist-terminal"`. The container-namespace pid is the last field of the `NSpid:` line in `<procRoot>/<Pid>/status`. `procRoot` is a constructor field, `hostmetrics.DefaultProcRoot` in production and a `t.TempDir()` with a golden `status` file in the test. An exec whose status file is missing is skipped, not an error.
- `terminal`, `TestSweepHangsUpEveryOrphan`: two orphans → `Kills` has `HUP` for each, and an `OrphanShells` error is logged through `logf`, not returned.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/terminal/ ./internal/dockerexec/ -run 'Sweep|OrphanShells' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**, and call `terminal.Sweep` once from `main.go`, in a goroutine after startup, with `knownComposeProjects(db)` (already in `main.go`) and a 30 s timeout.

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal main.go
git commit -m "fix: hang up terminal shells left behind by a panel restart"
```

---

### Task 8: Frontend protocol and session state machine

**Files:**
- Create: `web/src/data/terminal-session.ts`, `web/src/data/terminal-session.test.ts`
- Modify: `web/src/data/types.ts`, `web/src/data/api.ts`, `web/package.json`, `web/pnpm-lock.yaml`

**Interfaces:**
- Consumes: Task 3's services JSON and Task 5's frames.
- Produces:

```ts
export type TerminalShell = 'auto' | 'bash' | 'sh';
export type TerminalUser = 'default' | 'root';
export type TerminalTarget = { service: string; container?: string; shell: TerminalShell; user: TerminalUser };
export type TerminalServices = { available: boolean; reason: string;
  services: { name: string; containers: { id: string; name: string; number: number;
                                          shells: { bash: boolean; sh: boolean } }[] }[] };
export type ServerFrame =
  | { type: 'ready'; shell: 'bash' | 'sh'; user: TerminalUser; container: string }
  | { type: 'exit'; code: number }
  | { type: 'error'; message: string };
export type SessionState =
  | { kind: 'idle' } | { kind: 'connecting' }
  | { kind: 'open'; ready: Extract<ServerFrame, { type: 'ready' }> }
  | { kind: 'exited'; code: number; container: string }
  | { kind: 'failed'; message: string }
  | { kind: 'lost'; container?: string };
export function terminalUrl(location: ComposePath, target: TerminalTarget, rows: number, cols: number, origin: string): string; // ws: or wss: from origin
export function parseServerFrame(text: string): ServerFrame | undefined;
export function resizeFrame(rows: number, cols: number): string;
export function nextState(state: SessionState, event: { type: 'frame'; frame: ServerFrame } | { type: 'close' }): SessionState;
// api.ts
terminalServices: (location: ComposePath) => Promise<TerminalServices>
```

- [ ] **Step 1: Install dependencies**

Run: `pnpm --dir web add @xterm/xterm @xterm/addon-fit`. Check that the two packages' peer ranges agree.

- [ ] **Step 2: Write the failing tests**

- `terminalUrl` with origin `https://panel.example` → `wss://panel.example/api/projects/1/environments/2/composes/3/terminal?service=app&shell=auto&user=default&rows=24&cols=80` (and `container=` appears only when set; `http:` → `ws:`; a Service name with a space is encoded).
- `parseServerFrame('{"type":"exit","code":3}')` → frame. Unknown `type`, invalid JSON, or `exit` without a numeric `code` → `undefined`.
- `resizeFrame(40, 120)` → `'{"type":"resize","rows":40,"cols":120}'`.
- Transitions: `connecting` + `ready` → `open`; `open` + `exit` → `exited` (keeps `container`); `connecting` + `error` → `failed`; `open` + `close` → `lost` (keeps `container`); `exited` + `close` → stays `exited`; `failed` + `close` → stays `failed`.

- [ ] **Step 3: Run, expect FAIL**

Run: `pnpm --dir web test -- terminal-session`
Expected: FAIL (module not found).

- [ ] **Step 4: Implement**, then run the tests and `pnpm --dir web typecheck`. Expect PASS.

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/src/data
git commit -m "feat: model a compose terminal session in the web client"
```

---

### Task 9: Terminal tab UI

Read `https://ant.design/llms.txt`, then the `Tabs`, `Select`, `Segmented`, `Button`, `Alert`, `Tooltip` and `Empty` pages, and `docs/GUIDELINES.md` §4.6, §6, §8–§10, before editing.

**Files:**
- Create: `web/src/components/ComposeTerminalTab.tsx`
- Modify: `web/src/pages/ComposePage.tsx` (Tabs `items`), `web/src/app/theme.ts` (`LAYOUT`)

**Interfaces:**
- Consumes: Task 8's exports; `ComposeTab` `'terminal'` from Task 2.
- Produces: `<ComposeTerminalTab compose={compose} location={…} active={activeTab === 'terminal'} onOpenEditor={…} />`, and `LAYOUT.terminalHeight = 'clamp(420px, 65vh, 800px)'`.

- [ ] **Step 1: Implement the tab**, following spec §User interface exactly. The decisions the spec leaves to this task:
  - Tab order in `ComposePage` `items`: Editor, Logs, **Terminal**, Monitor, Volumes. Use no `destroyOnHidden`.
  - Load the services listing when the tab first becomes `active`, and again on every `Reconnect`/`Retry`, so a redeploy's new Container is picked up.
  - On `active` turning true, call `fitAddon.fit()` inside `requestAnimationFrame`, because the pane has 0 width while hidden. Debounce `resize` sends by 100 ms.
  - xterm theme: `background: token.colorBgContainer`, `foreground: token.colorText`, `cursor: token.colorPrimary`, `selectionBackground: token.colorPrimaryBg`, font `token.fontFamilyCode`. Re-apply on `ThemeModeContext` change.
  - Write output with `term.write(new Uint8Array(event.data))` and `binaryType = 'arraybuffer'`. Send input with `term.onData` and `ws.send(new TextEncoder().encode(data))`.
  - `exited`: write `\r\n\x1b[2m[exited with code N]\x1b[0m\r\n` into the terminal. `lost`: write a dim divider line, keep the output, and show the warning `Alert`.
  - Close before `ready`: call `api.authStatus()`. If it says signed out, the `Alert` reads `Your session expired — sign in again` with a link to `/signin`.
  - Leave guard while `open` only: a `beforeunload` listener, plus `useBlocker` whose predicate is true only when the next pathname leaves this Compose (`resolvePath(next).params.composeId !== composeId` or the pattern changes). The confirm `Modal` reads `Leaving closes the terminal session in <container>.`
  - Unmount closes the socket with code 1000.
  - Icon-only buttons carry `aria-label` (§10).

- [ ] **Step 2: Typecheck, test, build**

Run: `pnpm --dir web typecheck && pnpm --dir web test && pnpm --dir web build`
Expected: all pass.

- [ ] **Step 3: Manual check against a real Compose** (run the panel through `docker compose up --build` from the repo):
  connect, run `vim` and `top`, resize the window (`stty size` follows), press Ctrl+C, then `exit` (exit line shown). Switch Editor ↔ Terminal while `top` runs and confirm the session survives. Try `root` (`id -u` prints `0`), and a distroless Service (error copy). Click a sider link while connected (confirm shown), and reload (browser confirm shown).

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat: add a terminal tab to the compose page"
```

---

### Task 10: Rebuild the embedded frontend

- [ ] **Step 1:** Run `task build`. Expected: success, with `internal/webassets/dist` changed.
- [ ] **Step 2:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 3: Commit**

```bash
git add internal/webassets/dist
git commit -m "chore: rebuild the embedded frontend with the terminal tab"
```

---

### Task 11: Documentation and final verification

**Files:**
- Create: `docs/adr/0012-interactive-shell-over-engine-api-exec.md`
- Modify: `CONTEXT.md`, `docs/GUIDELINES.md` (§4.6 tab list, §6 bounded boxes), `docs/adr/0003-go-architecture.md` (the "Widened since" note: one more seam, `terminal.Exec`)

- [ ] **Step 1: Write ADR-0012** in the house style (decision first, `## Considered options`, `## Consequences`), covering spec §Decisions. Cite the Task 1 result verbatim: either "Docker <version> leaves the shell running, so the panel hangs it up and sweeps at start", or "Docker <version> ends the shell with its attach, so the panel does no cleanup of its own".
- [ ] **Step 2: Add `Terminal Session` to CONTEXT.md**, after **Container**: "One interactive shell in one Container of a Service, opened from the Compose page's Terminal tab and living only as long as that page. _Avoid_: Console, SSH."
- [ ] **Step 3: Update GUIDELINES.md**: add Terminal (`…/compose/:composeId/terminal`) to §4.6's tab list, and add the terminal box (`LAYOUT.terminalHeight`) to §6's bounded boxes.
- [ ] **Step 4: Final verification**

Run: `go test -race ./... && pnpm --dir web typecheck && pnpm --dir web test`
Expected: all pass. Then repeat Task 9 Step 3's manual pass once on the rebuilt binary, plus: DevTools → Network → Offline while connected. The UI shows `Connection lost` within ~40 s, and `docker top` shows no shell (Task 6 ran) or the spike's CLEAN result holds.

- [ ] **Step 5: Commit**

```bash
git add docs CONTEXT.md
git commit -m "docs: record the compose terminal decision"
```
