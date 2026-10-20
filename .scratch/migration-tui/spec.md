# Spec — Drive a server migration from the terminal

Date: 2026-10-20.
Status: needs-triage

Builds on `.scratch/server-migration/spec.md` and ADR-0018. Everything those describe about what a
**Migration** copies, the **Migration lock**, the item protocol between the two hosts, preflight
and finalize stays as it is. This spec changes who drives it and from where.

## Problem

The new host's web page loses its session in the middle of the Migration. Finalize swaps in the
old host's `panel.db`, and the session the operator signed in with does not exist in it. The steps
worth watching (Traefik starting with the copied certificates, the Composes deploying, `finish` on
the old host) all run while the operator is on the sign-in page. By the time they sign in again
with the old admin account, it is over.

A second problem is how a Migration is authorized. On the old host, `POST /api/migration/tokens`
needs only a signed-in admin cookie, with no password or Two-factor check again. Whoever holds a
stolen session can create a token and pull every Volume, the database, the chosen host paths and
`acme.json` to a host of their own. It is the largest way out for data the panel has.

## Decision

Both hosts drive the Migration from a terminal user interface (TUI) built with Bubble Tea, run
inside the panel container:

```
# old host
docker compose exec panel relist-panel migration serve
# new host
docker compose exec panel relist-panel migration pull
```

- **The TUI is a client, not the worker.** The panel process keeps doing the work exactly as now
  (the `Source`, the `Target`, the Finalizer). The TUI talks to it over a **control socket**: a
  Unix socket inside the container. Quitting the TUI, or losing the SSH connection, does not stop
  or pause a Migration. Running the command again shows where it is, with the full activity log.
- **Finalize re-executes the panel instead of exiting.** Today `Begin` ends with `os.Exit(0)` and
  relies on `restart: unless-stopped`. The panel is PID 1 of its container, so the container
  restarts, and every process started with `docker exec` (the TUI) dies with it. Instead the
  panel replaces itself with `syscall.Exec`: same PID, same container, fresh process. The TUI sees
  the socket drop for a moment, reconnects, and follows finalize to the end. The swap of
  `panel.db` before the database is opened (`PrepareFinalize`) is unchanged.
- **Running a command in the container is the authorization.** Whoever can `docker exec` into the
  panel container is root on that host already, and could copy the data without the panel. The
  socket needs no token, cookie or password of its own.
- **In two phases.** Phase 1 adds the TUI next to the web pages, which keep working. Phase 2, once
  the TUI has carried real Migrations, removes the web pages and the web routes that create
  tokens and drive a pull. Only phase 2 closes the stolen-session problem.

Rejected:

- **Keeping the web page and carrying the session across the swap** (a watch key kept in
  `state.json`). It fixes the first problem in a day, but not the second, and keeps the most
  complex page of the frontend.
- **A TUI that does the work itself.** It dies with the SSH connection, and at finalize.
- **Running the TUI in a separate container** (`docker compose run --rm panel …`), so it survives
  the panel container's restart. The command is long, must be run from the install directory,
  and still depends on the restart policy.
- **The TUI only on the new host.** The operator asked for both hosts to work the same way, and the
  token is the part that needs the terminal most.

## Language

Added to `CONTEXT.md`:

- **Control socket**: the Unix socket inside the panel container through which the `relist-panel`
  command talks to the running panel. Reaching it requires running a command in the container.
  _Avoid_: admin socket, API socket.

## Commands

All are subcommands of the `relist-panel` binary, which is the image's entrypoint. They need the
panel to be running in the same container, and say so if the socket is missing.

| Command | Host | What it does |
|---|---|---|
| `migration serve` | old | TUI: create a token, then follow the claim, the lock, the items being sent and `finish`. Cancel from it. |
| `migration pull` | new | TUI: connect, preview, choose host paths, start, follow every item and finalize, then the summary. Retry or abort from it. |
| `migration status` | either | Plain text, no TUI: where the Migration is on this host. `--watch` reprints every 2 s until done or paused. Works without a terminal. |
| `migration resume` | old | Cancel the Migration before `finish`, or Resume this server after it. Asks to type `RESUME`. |

`serve`, `pull` and `resume` need a terminal. Without one (`docker exec` without `-it`) they exit
with status 2 and print: `needs a terminal: run it with docker compose exec, or docker exec -it`.

Ctrl-C and `q` quit the TUI only. On the way out they print one line saying the Migration goes on
and which command shows it again. Aborting is always an explicit key with its own confirmation.

## The TUI: `migration pull` (new host)

The top line shows the journey, as the web page's Steps do: Connect · Preview · Lock old server ·
Copy · Finalize · Done, with the current one highlighted. A failed or paused stage is red.

1. **Connect.** Three fields: the old panel's URL, the Migration token (masked), and this panel's
   own URL. The old host shows the last as "Migrated to …". It defaults to `https://` plus
   `RELIST_DOMAIN` when that is set. Enter connects, with a spinner. An error stays under the
   fields. An `http://` URL is refused unless the panel runs with `--insecure-migration-source`,
   as today.
2. **Preview.**
   - Sizes by kind: data directory, database, Volumes, host paths, Images.
   - Blocking checks in red, warnings in yellow, each on its own line, as the web page lists them.
   - Volumes: name, driver, options, size. A Volume that cannot be restored here is red.
   - Images: which are pulled by digest and which are copied.
   - Host paths: a checklist. Space toggles, and system paths start unticked, as in the web page.
     Changing the selection runs `recheck`.
   - Keys: `enter` start (shown disabled while a check blocks), `r` recheck, `a` abort.
3. **Confirm start.** "Start the migration? The old server stops every Compose and locks until
   you resume it. [y/N]". Only `y` starts.
4. **Lock and copy.**
   - The old server's lock step ("Stopping Composes", "Waiting for Deployment #12 of api").
   - One row per item: kind, name, a progress bar with bytes copied of the estimate, state
     (waiting, copying, retrying n/3, done, failed).
   - The activity log's last lines, newest at the bottom. `l` opens the whole log, scrollable.
   - When the Migration pauses after three failed attempts: the error, then `r` retry and
     `a` abort.
5. **Finalize.** The finalize steps as a list (stop Traefik, move data, restart, start Traefik,
   deploy, finish), each checked as it passes. While the panel re-executes, the line reads
   "Restarting the panel…" and the TUI reconnects every second. After 2 minutes without the
   socket it says the panel did not come back and to run `docker compose up -d` in the install
   directory.
6. **Done.** The DNS records to change, the Deployments started with their numbers (and the error
   of any that failed), the warnings, and: "Sign in with the admin account of the old server."
   `q` quits.

**Abort** (`a`, from Preview to before finalize) asks: "Abort? The old server resumes and starts its
Composes again. Everything copied here is deleted. [y/N]".

## The TUI: `migration serve` (old host)

1. **Token.**
   - If a Migration is already under way or this host is Migrated, go straight to step 2.
   - Otherwise pick a lifetime: 1 hour, 24 hours (default) or 7 days. If a live token exists,
     say that creating one revokes it.
   - The token is shown once in a box, with its expiry and the command to run on the new host.
2. **Follow.**
   - "Waiting for the new server to connect…", then "Claimed by <address>".
   - The lock steps as a checked list: cancel queued Deployments, wait for a running one, stop
     Composes, maintenance page, manifest.
   - The items as the new host requests them: the one being sent with its bytes so far, and how
     many are done.
   - Then "Migrated to <URL> on <time>".
   - Keys: `c` cancel the Migration, before `finish` only (type `RESUME`).
3. **Quitting before the token is claimed** asks "Revoke the token? [Y/n]". Yes is the default, so
   a token is not left alive by accident.

## Control socket

- Path: `/run/relist-panel/control.sock`, mode `0600`, directory `0700`. It lives in the
  container's own filesystem, outside the data directory, so it is never in the `data-dir` item and
  never copied. The panel removes a stale socket file before listening, which also covers the
  re-execute at finalize.
- Protocol: HTTP/1.1 over the socket, JSON, with the same handlers the web routes use, so the
  socket and the web cannot drift apart while both exist:
  - The `Target` routes, today under `/api/migration/pull/`, without the web's admin guard.
  - On the old host: create token, revoke token, status, resume. These are today's admin routes
    without the cookie.
  - New: `GET /api/migration/serve/activity` on the old host. It returns the item being sent, its
    bytes so far, and the number of items done. The `Source` keeps these counters in memory. Today
    it does not track them.
- The TUI polls every 500 ms. At this rate a long poll or a stream would not be worth its
  complexity.
- `curl --unix-socket /run/relist-panel/control.sock http://panel/...` works. The probe uses that.

## Re-executing at finalize

- `FinalizeDeps.Exit` becomes `Restart`. Production calls `syscall.Exec` with `os.Executable()`,
  the same `os.Args` and the same environment.
- Before it does:
  - Stop accepting HTTP connections on both listeners, the TCP port and the socket.
  - Close the database.
  - Flush the log.
- Go opens files and sockets with `O_CLOEXEC`, so nothing leaks into the new process. The plan
  confirms this for the SQLite driver and the Docker client, and the probe checks it below.
- If `syscall.Exec` fails, log the error and `os.Exit(1)`, which falls back to the restart policy
  as today.
- Preflight's warning about a panel container without a restart policy is removed. It no longer
  matters to finalize.

## Phase 1: add the TUI

- `relist-panel migration serve|pull|status|resume`, the control socket, the activity counters and
  the re-execute at finalize.
- The web pages stay and keep working, and both can drive the same Migration:
  - Connect, Recheck and Start already refuse a second caller with the existing busy error
    (`op.TryLock`).
  - Abort waits for the action under way (`op.Lock`).
  - Retry takes only the state lock.

  The plan adds a test in which the TUI and the web act at once, for example Abort during Retry,
  and the state stays consistent. The web page also gains from the re-execute: no restart-policy
  dependency.
- The README's migration section and the Docker page's Server migration card name the commands.
- `go.mod` moves from `go 1.25.0` to `go 1.26.0`, which Bubble Tea v2 needs. The build image
  (`golang:1.27-alpine`) and local toolchains already satisfy it.
- Dependencies: `charm.land/bubbletea/v2` (v2.1.0), `charm.land/bubbles/v2` (v2.2.1),
  `charm.land/lipgloss/v2` (v2.0.6).

## Phase 2: remove the web side

Only after the operator has run real Migrations with the TUI and says so.

- Web: `MigrationPage` and its route, the "Migrate from another panel" links (setup screen,
  Docker page), and the token form on the Server migration card. The card instead says which
  command to run.
- `MigrationLockPage` stays and is read-only. It shows the step and the target URL, and names
  `migration resume` instead of its Cancel and Resume buttons.
- HTTP routes removed from the TCP listener:
  - all of `/api/migration/pull/*`;
  - `POST /api/migration/tokens` and `DELETE /api/migration/tokens/current`;
  - `POST /api/migration/resume`.

  `GET /api/migration/status` stays for the lock page. The routes between the two hosts (`claim`,
  `preflight`, `start`, `items`, `digest`, `abort`, `finish`) stay: they are how the data moves.
- ADR-0018's consequence "Who can reach an empty panel can start a Migration into it" no longer
  holds. Remove it there and point to ADR-0019.
- `scripts/dev-migration.sh` and the Vite proxy lose their purpose. The sandbox stays, and its
  printout names the commands.

## ADR

ADR-0019, "A Migration is driven from the terminal", records the Decision section above, with
the rejected options, as part of phase 1. It amends ADR-0018: the web flow there is replaced, and
the exit-and-restart at finalize becomes a re-execute.

## Out of scope

- Changing what is copied, the item protocol between the hosts, preflight's checks, or finalize's
  steps other than how the panel restarts.
- A TUI for anything other than the Migration.
- Renaming the panel container (`container_name: relist-panel`) so `docker exec relist-panel …`
  works without compose. See Open questions.

## Testing

### Unit tests

- **TUI models.** Each screen is a Bubble Tea model, tested by sending messages to `Update` and
  asserting on `View`:
  - Connect validates the URL.
  - Preview disables start while a check blocks, and system paths start unticked.
  - Start and abort need `y`.
  - A paused Migration offers retry and abort.
  - A lost socket shows "Restarting the panel…" and recovers.
  - Done lists DNS and Deployments.
  - `serve` revokes the token on quit before a claim.

  The client is an interface faked in these tests.
- **Control socket.**
  - Served from a temporary directory in tests: a stale socket file is replaced, the mode is
    `0600`, the `Target` and `Source` routes answer without a cookie.
  - In phase 2, the removed routes answer `404` on the TCP listener.
- **Commands.** Without a terminal, `serve`, `pull` and `resume` exit 2 with the message. `status`
  prints plain text.
- **Activity counters.** Bytes and the current item follow a stream, and reset when it ends.
- **Finalize.** `Begin` calls `Restart` instead of `Exit`, after the marker is written. A failed
  `syscall.Exec` falls back to exit 1.

### Probe (`task probe:migration`)

- **Pull over the socket.** The probe drives the new host through the socket with
  `curl --unix-socket` instead of the web routes. It does this in phase 1 already, so phase 2
  removes nothing the probe needs.
- **Re-execute.** Before finalize, the probe starts `relist-panel migration status --watch` with
  `docker exec` in the background. It checks:
  - the process is still alive after the re-execute;
  - its last output says done;
  - the panel container's `StartedAt` and restart count did not change.
- **No leaked descriptors.** After the re-execute, `ls -l /proc/1/fd` in the container shows no
  descriptor left open on the old `panel.db`.
- **The TUI itself.** One run of `migration pull` under `script` (util-linux) with keystrokes on
  stdin is a manual check in the sandbox, not part of the probe: Bubble Tea output is too
  timing-dependent to assert on in a shell script.

### Sandbox

`task sandbox:migration` prints the commands for the two hosts:

```
docker exec -it sandbox-old docker exec -it install-panel-1 relist-panel migration serve
docker exec -it sandbox-new docker exec -it install-panel-1 relist-panel migration pull
```

## Open questions

1. **Container name.** The operator's sketch was `docker exec relist-panel migration serve`. The
   compose file names the container `<project>-panel-1`, for example `relist-panel-panel-1`.
   `docker compose exec panel …` works from the install directory without a rename. Setting
   `container_name: relist-panel` would make the short form work anywhere, but recreates the
   container on upgrade and allows one install per Docker host. Recommendation: document
   `docker compose exec`, no rename.
2. **Phase 2 trigger.** Who says the TUI has been tried enough, and on what? Recommendation: the
   operator, after one real Migration between two of their own servers.
3. **`resume` from the lock page.** Phase 2 makes Resume terminal-only. Resume does not move data
   out, so it is not part of the stolen-session problem. Keeping the web button is possible, at
   the cost of one more admin route. Recommendation: terminal-only, for one way to do it.

## Probe record

- **2026-10-20**, Docker 29.8.0 on the host, two `docker:27-dind` hosts, `task probe:migration`:
  `probe: migration ok`. The new host was driven over the control socket with
  `relist-panel migration api`. The panel container's `StartedAt` and restart count were the same
  before `start` and after `done`, so finalize re-executed the panel in place. A
  `relist-panel migration status --watch` started with `docker exec` before `start` followed it
  to its final `done` line. Its 2 s interval did not land in the re-execute itself, which was
  shorter than that. No descriptor of PID 1 pointed at a deleted file. The rows, files and
  `acme.json` matched as before.
- The first run that day failed to build the image: the Dockerfile copied only `main.go` and
  `cli.go`, not the new `restart.go` and `control.go`. It now copies every root `.go` file.
