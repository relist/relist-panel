# Spec — Compose terminal (an interactive shell into a Service's Container)

## Problem

Debugging a deployed Compose today means leaving the panel: SSH to the host and run
`docker exec -it <container> bash` by hand. The panel already shows logs and metrics for each
Service, but the operator cannot open a shell. That shell is how they run `vim` or `top`, read a
config file, run a migration, or poke at a database client inside the Container.

Current behaviour, verified in code: nothing in the backend streams. Logs are one
`docker compose logs --tail=200` call returned in a single response (`internal/dockerexec/dockerexec.go`),
there is no WebSocket or SSE anywhere, and stdlib `net/http` has no WebSocket implementation. The
panel already talks to the Docker Engine API directly over the mounted socket for metrics
(`internal/dockerexec/containersource.go`, ADR-0006).

## Goal and success criteria

- A **Terminal** tab on the Compose page opens a shell in a running Container of a chosen Service.
- The terminal is a full interactive TTY: colours, cursor movement, `vim`, `top`/`htop`, Ctrl+C,
  and resizing that follows the browser box.
- The shell is `bash` or `sh`, auto-detected by default, with a manual override.
- The shell runs as the image's default user by default, with a per-connect option to run as root.
- Switching tabs within the Compose page keeps the session. Leaving the page or reloading closes it,
  and closing it leaves no shell process running in the Container.
- A cross-site page cannot open a terminal using the operator's session cookie.

## Out of scope

- Several sessions inside one page (sub-tabs, split panes). One session per page; separate browser
  tabs are independent sessions (see Decisions).
- Sessions that survive a reload or reattach later; server-side scrollback.
- Automatic reconnect.
- Recording, audit log or replay of sessions.
- Shells other than `/bin/bash` and `/bin/sh`; arbitrary commands; arbitrary `--user` values.
- File upload/download through the terminal (the Volume Explorer covers files).
- An idle timeout.

## Decisions

### Transport: WebSocket + Docker Engine API exec

The browser speaks WebSocket to the panel; the panel drives the exec through the Engine API over
`/var/run/docker.sock`, reusing the unix-socket dialer `ContainerSource` already has.

- `POST /containers/{id}/exec` with `Tty: true`, `AttachStdin/AttachStdout/AttachStderr: true`,
  `User`, `Env: ["TERM=xterm-256color"]` and the wrapper `Cmd` below.
- `POST /exec/{id}/start` with `Connection: Upgrade` and `Upgrade: tcp`; the hijacked connection is
  the raw bidirectional stream. With a TTY, stdout and stderr arrive merged with no multiplex header.
- `POST /exec/{id}/resize?h=<rows>&w=<cols>` on every resize.
- `GET /exec/{id}/json` for the exit code when the stream ends.

Rejected: `docker exec -it` under a PTY (`creack/pty`). It costs one `docker` CLI process
(~20–30 MB) per session on a 256 MB panel, resizes through two PTY layers, and needs two new
dependencies instead of one. Also rejected: writing WebSocket framing on stdlib by hand. Masking, ping
and close handshakes are not worth owning to save one small dependency.

New Go dependency: `github.com/coder/websocket`, which is context-based and checks Origin by default.

### Package and seam

New domain package `internal/terminal` (ADR-0003 package-per-domain) owns the HTTP surface and the
session lifecycle. It declares its seam as a consumer-side interface, as `volumes.Runner` does:

```go
type Exec interface {
    // ServiceContainers lists the running Containers of one Service in one Compose project,
    // ordered by their com.docker.compose.container-number label.
    ServiceContainers(ctx context.Context, project, service string) ([]Container, error)
    // HasShell reports whether path runs as a shell in the Container.
    HasShell(ctx context.Context, containerID, path string) (bool, error)
    // Start opens a TTY exec running the wrapper around shell as user.
    Start(ctx context.Context, containerID string, opts ExecOptions) (Session, error)
    // Kill sends sig to pid inside the Container, as root.
    Kill(ctx context.Context, containerID string, pid int, sig string) error
}

type ExecOptions struct {
    Shell      string // "/bin/bash" or "/bin/sh"
    User       string // "" (image default) or "0"
    Rows, Cols uint16
}

type Session interface {
    io.ReadWriteCloser
    Resize(ctx context.Context, rows, cols uint16) error
    ExitCode(ctx context.Context) (int, error)
}
```

The real implementation is `internal/dockerexec/terminal.go`, wired in `httpapi.NewHandler` the way
`volumeapi` is. A fake lives in `internal/terminal/faketest`, matching `internal/volumes/faketest`.

- `ServiceContainers` → `GET /containers/json` filtered on the labels
  `com.docker.compose.project=<DeployProjectName>` and `com.docker.compose.service=<service>`.
- `HasShell` → a non-TTY exec of `[<path>, "-c", "exit 0"]`. Exit 0 means present. An exec that
  fails to start (`executable file not found`) means absent. No `test` binary is needed, because
  minimal images may lack one.
- `Kill` → a non-TTY exec as `User: "0"` of `["/bin/sh", "-c", "kill -<sig> <pid>"]`, using the
  shell's builtin `kill`, so no `kill` binary is needed.

The Compose is resolved with `composes.ComposeRunRequest`, as `volumeapi` does, so a missing Project,
Environment or Compose takes the existing 404 path.

### Shell wrapper and process cleanup

Docker is known to leave an exec's process running after its client disconnects (moby issue on
`docker exec` processes surviving a closed attach). The panel cannot rely on closing the hijacked
connection to end the shell, so every shell is started through a wrapper that reports its PID:

```
Cmd: ["/bin/sh", "-c", "echo $$; exec \"$1\"", "relist-terminal", "<shell>"]
```

`relist-terminal` is `$0` of the wrapper script. It is the marker the startup sweep looks for in an
exec's `ProcessConfig.arguments`, and `$1` is the chosen shell. The server reads the first line of
the stream as the shell's PID and does not forward it to the browser. `exec` keeps the PID.

The wrapper needs `/bin/sh`. An image with `/bin/bash` but no `/bin/sh` is therefore reported as
having no usable shell. Such images are not known to exist in practice, and the case gets the same
`No shell found` message.

On session end, for any reason, the server runs `Kill(pid, "HUP")`, waits up to 2 s, then
`Kill(pid, "KILL")`. The shell is the session leader of the exec's TTY, so when it exits the kernel
hangs up that TTY's foreground job (`vim`, `top`, a migration) as well. A job the operator
backgrounded with `nohup … &` may survive, as it would after an SSH logout; that is intended. The
kill execs run on their own `context.WithTimeout(5s)`, never on the request context, which is already
cancelled by then.

**Spike first.** The plan opens by measuring, on Docker 27, whether a TTY exec's process survives its
attach connection closing. If it does not survive, `Kill` and the PID line are dropped and the design
is simpler. If it does survive, the cleanup above stays and the startup sweep is added:

- **Startup sweep (conditional).** On panel start, for each running Container of a known Compose, read
  `ExecIDs` from `GET /containers/{id}/json`, inspect each, and kill any that is `Running` and carries
  the `relist-terminal` marker. This covers a panel crash, where the in-process cleanup never ran.

### Shell and user are allowlisted

- `shell` accepts only `auto | bash | sh`. `auto` probes `/bin/bash` and falls back to `/bin/sh`.
- `user` accepts only `default | root`. `default` sends no `User` (the image's own user, exactly
  like a bare `docker exec`); `root` sends `User: "0"`.
- There is no free-text user or command. The operator already controls the Docker socket, so root
  adds no privilege; the allowlist keeps the request surface to what the UI can produce.

### Session count

- One session per page, enforced by the UI.
- Separate browser tabs are separate, independent sessions; the server does not tie a session to a
  page.
- **Server-wide cap of 10 concurrent sessions**, as a guard against a reconnect loop or forgotten
  tabs. The 11th is refused with `Too many open terminal sessions (10) — close one first`.

### Coordination

The terminal route is a `GET` outside `/volumes`, so the project coordinator
(`internal/httpapi/coordination.go`) does not gate it. This is deliberate: a session can last hours
and must not block deploy, stop or delete of its Project. A deploy that replaces the Container ends
the session through EOF on the stream.

## HTTP and WebSocket protocol

### Endpoints

Both are under `/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}`.

1. `GET …/terminal/services` returns JSON. Each Service of the Compose is listed with its running
   Containers (`name`, `number`, `id`) and, per Container, which of `bash`/`sh` exist. The UI fills
   its selectors from this. When the Docker socket is unavailable the response carries
   `available: false` with a reason, as the Volumes listing does.
2. `GET …/terminal?service=&container=&shell=&user=&rows=&cols=` upgrades to a WebSocket and runs the
   session.

### Before and after the upgrade

- The auth middleware runs first. An unauthenticated request gets the existing 401 before the
  upgrade.
- A missing Compose gets 404, and invalid parameters (`shell` or `user` outside the allowlist, `rows`
  or `cols` outside 1–500) get 400, both before the upgrade. Only a frontend bug sends these.
- Every other failure is reported **after** the upgrade, as an `error` frame followed by a close. A
  browser `WebSocket` cannot read the body of a failed upgrade, so this is the only way the operator
  sees a real message.
- **Origin check:** `coder/websocket`'s default, which refuses an `Origin` whose host differs from the
  request's `Host`. Traefik passes `Host` through. No `OriginPatterns` allowlist. This blocks
  cross-site WebSocket hijacking, which the session cookie would otherwise allow.

### Frames

| Direction | Frame | Content |
| --- | --- | --- |
| browser → server | binary | keystrokes and paste, raw bytes |
| browser → server | text (JSON) | `{"type":"resize","rows":40,"cols":120}` |
| server → browser | binary | terminal output, raw bytes (xterm decodes UTF-8) |
| server → browser | text (JSON) | `{"type":"ready","shell":"bash","user":"default","container":"relist-c3-app-1"}` |
| server → browser | text (JSON) | `{"type":"exit","code":0}` |
| server → browser | text (JSON) | `{"type":"error","message":"…"}` |

Splitting by frame type means keystrokes are never parsed as control, and nothing is escaped.

### Sequence

1. Browser opens the WebSocket with the query above.
2. Server accepts, resolves the Container, probes the shell, checks the session cap, and starts the
   exec.
3. Server reads the PID line, then sends `ready`; bytes flow both ways.
4. The shell exits (`exit`, Ctrl+D). The server reads the exit code, sends `exit`, and closes with 1000.

### Limits and liveness

- Read limit 64 KiB per frame (a large paste fits).
- The server pings every 30 s; a missing pong within 10 s ends the session. A dropped network is
  therefore detected and cleaned up within ~40 s.
- `rows`/`cols` in `resize` are clamped to 1–500. The browser debounces resize before sending.

## Error handling

| Case | Where | Message |
| --- | --- | --- |
| Project/Environment/Compose missing | 404 before upgrade | existing path |
| `shell`/`user`/size outside allowlist | 400 before upgrade | — |
| Service has no running Container | `error` frame | `Service "app" has no running container — deploy or start it first` |
| `bash` chosen, not in image | `error` frame | `bash is not installed in this image — use sh` |
| `auto`, neither shell present | `error` frame | `No shell found in this image (/bin/bash, /bin/sh)` |
| Session cap reached | `error` frame | `Too many open terminal sessions (10) — close one first` |
| Docker API failure | `error` frame | Docker's own message, unchanged (GUIDELINES §8) |
| Docker socket unavailable | `services` listing | `available: false` + reason; the tab shows its unavailable state |

Cleanup runs through `defer` on every path. The order is: close the WebSocket, close the hijacked
connection, kill the shell (if the spike keeps it), stop the ping ticker, and release the cap slot.

## User interface

GUIDELINES §4.6, §6, §8 and §9 apply. antd 6.5.1.

### Route and tab

- New route `…/compose/:composeId/terminal`, tab key `terminal`. Tab order:
  **Editor | Logs | Terminal | Monitor | Volumes**.
- The pane mounts lazily on first visit and stays mounted while the Compose page is open; antd v6
  `Tabs` keeps inactive panes mounted unless `destroyOnHidden` is set, and the Compose page does not
  set it. The plan verifies that a tab change (a URL param change) does not remount `ComposePage`.
- Leaving the Compose page unmounts the pane, which closes the WebSocket.

### Files

- `web/src/components/ComposeTerminalTab.tsx` holds the UI.
- `web/src/data/terminal-session.ts` holds the protocol encode/decode and the session state machine
  (`idle → connecting → open → exited | failed | lost`), with no React in it.
- New dependencies: `@xterm/xterm`, `@xterm/addon-fit`.

### Toolbar

One `Flex` row, the same family as the Logs tab:

- `Select` **Service**. Services without a running Container are listed but disabled, marked
  `not running` (visible-but-disabled, §9).
- `Select` **Container**, shown only when the chosen Service has more than one running Container.
  Defaults to the lowest container number.
- `Segmented` **Shell**: `Auto | bash | sh`. A shell the probe did not find is disabled, with a
  tooltip such as `bash is not installed in this image`.
- `Segmented` **User**: `Image default | root`.
- `Button` **Connect** (primary), which becomes **Disconnect** while open. All selectors are disabled
  while connected.
- Right-aligned status text: `Connected · bash · image default · relist-c3-app-1`.

### Terminal box

- A bounded box, like the logs viewer (§6). Its height is a new layout constant,
  `LAYOUT.terminalHeight = clamp(420px, 65vh, 800px)`.
- The xterm theme comes from antd tokens and `ThemeModeContext`, the way `YamlEditor` themes Monaco.
- `addon-fit` with a `ResizeObserver` sends a debounced `resize`. The box refits when the tab becomes
  active again, because a hidden pane measures zero width.
- Ctrl+C is SIGINT, as in a real terminal. Copy is Ctrl+Shift+C / Cmd+C on a selection, and paste is
  Ctrl+Shift+V / Cmd+V.

### States

| State | Display |
| --- | --- |
| Compose not deployed, or no Service running | `EmptyState` with a link to the Editor tab: `Deploy to start a terminal` |
| Feature unavailable (no socket) | Unavailable state with the server's reason, as the Volumes tab does |
| Connecting | Spinner on the Connect button |
| `error` frame | `Alert type="error"` with the server's message verbatim, and a `Retry` action |
| `exit` frame | A dim line in the terminal, `[exited with code N]`, and a `Reconnect` button |
| Connection lost (network, redeploy, panel restart) | `Alert type="warning"`: `Connection lost — the shell in <container> was closed`, and a `Reconnect` button. The old output stays above a divider |
| Close before `ready` | Check `GET /api/auth/status`. If the session expired, say so and link to sign-in; otherwise show the lost-connection state |

Reconnect never happens automatically. It always opens a new shell, and the copy says so.

### Leaving while connected

While a session is open, and only then:

- `beforeunload` asks the browser's own confirmation on reload or close.
- React Router's `useBlocker` (the app uses `createBrowserRouter`, a data router) asks before in-app
  navigation away from the Compose page: `Leaving closes the terminal session in <container>.`
  Switching tabs within the page is not blocked.

## Testing

### Go: `internal/terminal` (external package `terminal_test`, black-box HTTP seam per ADR-0003)

A real `coder/websocket.Dial` client runs against `httptest.Server`, with the fake `Exec` from
`terminal/faketest`. The fake's session is a `net.Pipe` that records every resize and kill.

1. Bytes flow in both directions.
2. A `resize` frame reaches the fake, clamped to 1–500.
3. `shell=auto` picks bash when present, sh when not; `shell=bash` without bash returns the `error`
   frame.
4. `user=root` sends `User: "0"`; `user=default` sends none.
5. A cross-Origin upgrade is refused.
6. No running Container, and no shell at all, each return the matching `error` frame and close.
7. A shell exit sends `exit` with the session's code.
8. A client disconnect makes the fake receive `HUP`, then `KILL`, for the PID from the wrapper line.
9. The PID line never reaches the client.
10. The 11th concurrent session gets the cap `error`, and closing one frees a slot.
11. Invalid `shell`/`user`/size returns 400 before upgrade, and a missing Compose returns 404.

### Go: `internal/dockerexec`

Against a fake unix-socket HTTP server, with no real Docker needed: the exec-create body, the
`Upgrade: tcp` start request and its hijacked stream, the resize query, the label filter on
`/containers/json`, and the kill exec's command and user.

### Vitest: `web/src/data/terminal-session.test.ts`

Control-frame encode/decode, and the state transitions `connecting → open → exited`,
`connecting → failed` (`error` before `ready`), and `open → lost` (close without `exit`).

### Manual

Against a real Compose: connect, run `vim` and `top`, resize the window, press Ctrl+C, `exit`, switch
tabs and back, close the browser tab, then confirm with `docker top <container>` that no shell
remains. Repeat with the network cut (DevTools offline) and confirm cleanup within ~40 s.

## Documentation

- **ADR-0012 "Interactive shell over Engine API exec"** records the panel opening shells into user
  Containers; WebSocket transport and the `coder/websocket` dependency; the Origin check; the
  shell/user allowlist; the shell cleanup and the spike's outcome; the session cap; and the
  terminal sitting outside the project coordinator. It also amends ADR-0003's seam count by one
  (`terminal.Exec`).
- **`CONTEXT.md`** gains **Terminal Session**: one interactive shell in one Container of a Service,
  living only as long as the page that opened it. _Avoid_: Console, SSH.
- **`docs/GUIDELINES.md`** §4.6 adds the Terminal tab to the tab list, and §6 adds the terminal box to
  the bounded boxes.

## Spike result

Spike (Task 1): does a TTY exec's process outlive its attach connection?

- **Docker server version** (`docker version --format '{{.Server.Version}}'`): `29.8.0` — the plan says
  "Docker 27"; this host's daemon is 29.8.0.
- Ran against `alpine:3` (pulled locally first; image was not present). Container `relist-spike`
  (`docker run -d --name relist-spike alpine:3 sleep 3600`).

### Run 1 — `exec -it sh -c 'exec sleep 1001'`, client `kill -9`

After `script -qc "docker exec -it relist-spike sh -c 'exec sleep 1001'" /dev/null & CLIENT=$!;
sleep 2; kill -9 $CLIENT; sleep 3`, `docker top relist-spike`:

```
UID                 PID                 PPID                C                   STIME               TTY                 TIME                CMD
root                498733              498705              0                   21:46               ?                   00:00:00            sleep 3600
root                498850              498705              0                   21:47               pts/0               00:00:00            sleep 1001
```

`sleep 1001` (the exec'd process) is still listed after its client was killed → it outlived the
connection.

### Run 2 — interactive `sh` running foreground `top`, client `kill -9`

After `script -qc "docker exec -it relist-spike sh" /dev/null <<'EOF' & CLIENT=$!; top; EOF;
sleep 2; kill -9 $CLIENT; sleep 3`, `docker top relist-spike`:

```
UID                 PID                 PPID                C                   STIME               TTY                 TIME                CMD
root                498733              498705              0                   21:46               ?                   00:00:00            sleep 3600
root                498850              498705              0                   21:47               pts/0               00:00:00            sleep 1001
root                498985              498705              0                   21:47               pts/1               00:00:00            sh
```

The interactive `sh` (the exec'd shell, PID 498985) is still listed after its client was killed. Its
foreground `top` died when the pty closed, but the shell itself survived the disconnect.

**Verdict:** **LEAKS**
