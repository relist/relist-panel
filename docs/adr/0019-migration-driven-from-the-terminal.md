# A Migration is driven from the terminal

ADR-0018 drove a server **Migration** from web pages. On the new host, the session the operator
signed in with disappears when finalize swaps in the old host's `panel.db`, so the steps worth
watching (Traefik starting with the copied certificates, the Composes deploying, `finish` on the
old host) run while the operator is on the sign-in page. On the old host, a Migration token
needed only the admin cookie: a stolen session could create one and pull every Volume, the
database, the chosen host paths and `acme.json` to a host of the thief's choosing.

**Decision:** both hosts drive the Migration from a terminal UI built with Bubble Tea, run in
the panel container: `relist-panel migration serve` on the old host, `relist-panel migration
pull` on the new one, plus `status` and `resume`.

- **The TUI is a client, not the worker.** The panel keeps doing the work (`OldHost`, `Target`,
  the Finalizer). The TUI talks to it over the **Control socket**, a Unix socket at
  `/run/relist-panel/control.sock` in the container, outside the data directory. It serves the
  same handlers as the web routes, without the cookie. Quitting the TUI or losing the SSH
  connection never stops or pauses a Migration.
- **Finalize re-executes the panel instead of exiting.** `Begin` used to end with `os.Exit(0)`
  and rely on `restart: unless-stopped`. The panel is PID 1 of its container, so everything
  started with `docker exec` died with it. Now main shuts down and calls `syscall.Exec` on its
  own binary: same PID, same container, a fresh process that swaps the database before opening
  it, as before. If exec fails, the panel exits 1 and the restart policy takes over.
- **Running a command in the container is the authorization.** Whoever can `docker exec` into
  the panel container is root on that host already.
- **In two phases.** Phase 1 added the TUI next to the web pages. Phase 2 (2026-10-21) removed
  the web pages and the web routes that create tokens, resume the old host and drive a pull,
  which closes the stolen-session path. The old host's lock page stays, read-only.

Rejected:

- Carrying the session across the swap with a watch key in `state.json`. It fixes watching, but
  not the token, and keeps the most complex page of the frontend.
- A TUI that does the work itself. It dies with the SSH connection, and at finalize.
- The TUI in a separate container (`docker compose run --rm panel …`). The command is long,
  must run from the install directory, and still depends on the restart policy.
- A TUI on the new host only. The token is the part that needs the terminal most.

## Consequences

- Preflight no longer warns about a panel container without a restart policy.
- The panel image has no curl; the hidden `relist-panel migration api METHOD PATH [JSON]` sends
  one raw request over the socket, for the probe and debugging.
- A panel that cannot create the socket (a bare binary run by a user who cannot write `/run`)
  logs a warning and serves the web, but cannot start a Migration, pull, or resume a
  Migration lock until `RELIST_CONTROL_SOCKET` points somewhere writable and it restarts.
- Tokens, Resume and the pull exist only on the control socket. The web keeps the routes that
  move data between the hosts, which authenticate with the Migration session key, and the
  lock's status for the lock page.
- Spec: `.scratch/migration-tui/spec.md`.
