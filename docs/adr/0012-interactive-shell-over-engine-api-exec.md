# Interactive shell over Engine API exec

The operator wants to open a shell inside a Service's Container from the Compose page, instead of
leaving the panel to `docker exec -it <container> bash` by hand. The panel's stdlib `net/http` has no
WebSocket implementation, and nothing in the backend streams today.

**Decision:** the browser speaks WebSocket to the panel, and the panel drives a TTY exec through the
Docker Engine API over `/var/run/docker.sock`, reusing the unix-socket dialer `ContainerSource`
already has (ADR-0006). The new domain package `internal/terminal` owns the HTTP surface and the
session lifecycle; its seam is a consumer-side `Exec` interface implemented in `dockerexec` and faked
in `internal/terminal/faketest`, as `volumes.Runner` is.

The exec is four Engine API calls:

- `POST /containers/{id}/exec` with `Tty: true`, `AttachStdin/AttachStdout/AttachStderr: true`, the
  chosen `User`, `Env: ["TERM=xterm-256color"]`, and the wrapper command below.
- `POST /exec/{id}/start` with `Connection: Upgrade` and `Upgrade: tcp`; the hijacked connection is
  the raw bidirectional stream. With a TTY, stdout and stderr arrive merged and carry no multiplex
  header.
- `POST /exec/{id}/resize?h=<rows>&w=<cols>` on every resize.
- `GET /exec/{id}/json` for the exit code when the stream ends.

The transport adds one dependency, `github.com/coder/websocket`, which is context-based and checks
`Origin` by default.

## Considered options

**`docker exec -it` under a PTY (`creack/pty`).** Rejected: it costs one `docker` CLI process
(~20–30 MB) per session on a 256 MB panel, resizes through two PTY layers, and needs two new
dependencies instead of one.

**Hand-rolled WebSocket framing on stdlib `net/http`.** Rejected: masking, ping and close handshakes
are not worth owning to save one small dependency.

## Consequences

- **Origin is checked, with no allowlist.** `coder/websocket`'s default refuses an `Origin` whose host
  differs from the request's `Host`; Traefik passes `Host` through. There is no `OriginPatterns`
  allowlist. This blocks cross-site WebSocket hijacking, which the session cookie would otherwise
  permit — a cross-site page cannot open a terminal with the operator's cookie.
- **Shell and user are allowlisted.** `shell` accepts only `auto | bash | sh` (`auto` probes
  `/bin/bash` and falls back to `/bin/sh`); `user` accepts only `default | root` (`default` sends no
  `User`, exactly like a bare `docker exec`; `root` sends `User: "0"`). There is no free-text command
  or user. The operator already controls the Docker socket, so `root` adds no privilege; the allowlist
  keeps the request surface to exactly what the UI can produce.
- **Docker 29.8.0 leaks a TTY exec's process when its attach connection closes.** The Task 1 spike
  (this host's daemon is 29.8.0, not the plan's "Docker 27") recorded **LEAKS**: after killing the
  client, the exec'd `sleep` and the interactive `sh` both survived, so the panel cannot rely on
  closing the hijacked connection to end the shell. The spike's verdict is recorded in
  `.scratch/compose-terminal/spec.md` §Spike result.
- **Every shell runs through the `relist-terminal` wrapper so the panel can hang it up.** The command
  is `["/bin/sh", "-c", "echo $$; exec \"$1\"", "relist-terminal", "<shell>"]`: the server reads the
  first line of the stream as the shell's PID (never forwarding it to the browser), and `relist-terminal`
  is the marker the startup sweep looks for. On session end, for any reason, the server runs
  `Kill(pid, "HUP")`, waits the 2 s grace, probes the pid with `kill -0`, and sends `KILL` only if the
  probe still finds it; the shell is the session leader of the exec's TTY, so when it exits the kernel hangs up that TTY's foreground job (`vim`, `top`, a
  migration) as well. A `nohup … &` job may survive, as after an SSH logout; that is intended. The
  kill execs run on their own `context.WithTimeout(5s)`, never on the already-cancelled request
  context. The wrapper needs `/bin/sh`, so an image with `/bin/bash` but no `/bin/sh` is reported as
  having no usable shell.
- **A startup sweep covers a panel crash.** On panel start, for each running Container of a known
  Compose, the panel reads `ExecIDs` from `GET /containers/{id}/json`, inspects each, and hangs up any
  that is `Running` and carries the `relist-terminal` marker. The hang-up is the session's own: HUP,
  the 2 s grace, the `kill -0` probe, then KILL only if the shell is still alive, one goroutine per
  orphan. The sweep keeps going past a Container or exec that fails to inspect, hanging up what it
  found and logging the error, so one bad exec cannot leave the other shells running. This is the
  in-process cleanup that never ran because the process died.
- **A server-wide cap of 10 concurrent sessions.** Separate browser tabs are independent sessions and
  the server does not tie a session to a page; the cap is the guard against a reconnect loop or
  forgotten tabs. The 11th is refused with `Too many open terminal sessions (10) — close one first`.
- **The terminal route is outside the project coordinator.** It is a `GET` outside `/volumes`, so
  `internal/httpapi/coordination.go` does not gate it. This is deliberate: a session can last hours and
  must not block deploy, stop or delete of its Project. A deploy that replaces the Container ends the
  session through EOF on the stream.
- **ADR-0003's seam count rises by one.** The rule "one adapter package, one port per consumer" gains
  `terminal.Exec`, alongside `ContainerSource`, `volumes.CLI` and `volumes.Runner`.
- **No reconnect, no scrollback, no recording.** Reconnect always opens a new shell, and closing the
  WebSocket leaves the Container as it found it.
