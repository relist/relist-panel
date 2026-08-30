# 09 — Stop

**What to build:** An operator can stop a deployed Compose's containers without deleting its saved YAML.

**Blocked by:** 08

**Status:** closed

- [x] `dockerexec.Down` (without volumes) is implemented for real and added to the fake `ComposeRunner`
- [x] Clicking "Stop" (per `ComposeGeneral.dc.html`) brings the Compose's containers down for real
- [x] The status badge reflects "Stopped" afterward
- [x] Covered by HTTP-seam tests using the fake `ComposeRunner`

## Comments

`dockerexec.Down` and the fake `ComposeRunner`'s `Down` method were already
implemented in ticket 08 (the full `ComposeRunner` interface was given real
bodies up front, even though only `Up`/`Status` were wired to an HTTP route
at the time). This ticket's actual work was: a new `POST
.../composes/{id}/stop` route in `internal/composes`, plumbed through the
same `runRequestFor`/deterministic-path machinery as `deploy`; and the
"Stop" button on the Compose detail page's General tab.

`stop` rejects a never-deployed Compose with 400 ("compose has not been
deployed") by checking the resolved compose file exists on disk before
calling the runner — the same file-existence gate `currentStatus` already
uses, extended to a mutating action so an undeployed Compose never shells
out to `docker compose down` for nothing. The frontend mirrors this by
disabling the "Stop" button whenever `status.total === 0` (nothing running
to stop). Both are minor, defensive additions beyond the ticket's literal
checklist, flagged as such by the Spec-axis review and judged not worth
holding up on.

The fake `ComposeRunner` used by tests (`internal/httpapi/httpapi_test.go`)
gained a `downErr` field (mirroring the existing `upErr`) and its `Down`
method now zeroes out the recorded status for that project on success, so
`TestAuthenticatedAdminCanStopCompose` can assert the status badge actually
reads back as stopped afterward rather than just asserting the call
happened.

`deploy` and `stop` shared an identical "run the action, then best-effort
refresh live status" tail; extracted into `withRefreshedStatus`, alongside
a `runRequestForCompose` helper (added ahead of review, since both handlers
already needed to resolve a Compose's deploy identity from its parent
Project/Environment names).

Verified with `pnpm --dir web typecheck`, `pnpm --dir web build`, `go build
./...`, `go vet ./...`, `go test -race ./...`. Also ran a browser smoke
test against the built binary (no Docker available in this environment):
created a Project + Compose, deployed it (fails with "exec: docker: not
found" as expected, but writes the resolved compose/env files to disk
first, same as ticket 08), confirmed the "Stop" button is correctly
disabled while `status.total == 0`, then exercised the real `/stop`
endpoint directly (bypassing the disabled button, since this sandbox can
never reach a truly "running" state) and confirmed it passed the
file-existence gate and propagated the runner's real "docker: executable
file not found" error as a 502 — the same error-handling path already
covered by the HTTP-seam tests.

`/code-review` against `fe827d7` (two parallel sub-agents) found no hard
standards violations and no missing/wrong spec requirements. It raised one
judgment call (the `withRefreshedStatus` duplication above, applied) and a
second, left as-is: the frontend's `status.total === 0` gating is "mild
primitive obsession" but pre-existing shape from ticket 08, not introduced
here.
