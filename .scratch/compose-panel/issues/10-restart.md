# 10 — Restart

**What to build:** An operator can restart a deployed Compose's containers without a full redeploy.

**Blocked by:** 08

**Status:** closed

- [x] `dockerexec.Restart` is implemented for real and added to the fake `ComposeRunner`
- [x] Clicking "Restart" (per `ComposeGeneral.dc.html`) cycles the Compose's containers for real
- [x] Covered by HTTP-seam tests using the fake `ComposeRunner`

## Comments

`dockerexec.Restart` and the fake `ComposeRunner`'s `Restart` method were
already implemented in ticket 08 (the same up-front full-interface pattern
noted in ticket 09's comments). This ticket's actual work was: a new `POST
.../composes/{id}/restart` route in `internal/composes`, and the "Restart"
button on the Compose detail page's General tab, placed between "Save &
Deploy" and "Stop" (matching the spec's stated action order
"Deploy/Stop/Restart/Delete").

`restart` reuses the exact same "reject an undeployed Compose with 400"
file-existence gate `stop` already uses, and the frontend mirrors it with the
same `status.total === 0` disabled condition — a direct, sanctioned
extension of the pattern ticket 09's review already accepted for `stop`.

The fake `ComposeRunner` gained a `restartErr` field (mirroring `upErr`/
`downErr`) for error-injection in `TestComposeRestartValidatesInputAndPropagatesRunnerErrors`.
Unlike `stop`'s `downErr` path, `Restart` doesn't mutate `f.statuses`, since
a restart cycles containers rather than stopping them — the fake's default
non-zero status is the correct simulated outcome.

Verified with `pnpm --dir web typecheck`, `pnpm --dir web build`, `go build
./...`, `go vet ./...`, `go test -race ./...`. Also ran a browser smoke test
against the built binary (no Docker available in this environment): created
a Project + Compose, confirmed the "Restart" button renders between "Save &
Deploy" and "Stop" and is correctly disabled while `status.total == 0`,
attempted a deploy (fails with "exec: docker: not found" as expected, but
still writes the resolved compose/env files to disk), then exercised the
real `/restart` endpoint directly via `fetch` (bypassing the disabled button,
since this sandbox can never reach a truly "running" state) and confirmed it
passed the file-existence gate and propagated the runner's real "docker:
executable file not found" error as a 502 — the same error-handling path
already covered by the HTTP-seam tests. Also confirmed the 400 gate directly
against a freshly created, never-deployed Compose.

`/code-review` against `44d464f` (two parallel sub-agents) found no hard
standards violations and no missing/wrong spec requirements. Standards
raised one judgement call: with this ticket, the "load compose, resolve its
run identity, gate on it being deployed" preamble now appeared identically
in both `stop` and `restart` (a "rule of three" once combined with the
already-similar `deploy` preamble) — applied by extracting
`resolveDeployedCompose`. Spec raised only a stale doc comment
(`withRefreshedStatus`'s comment still said "deploy/stop", not mentioning
restart) — fixed in the same pass — and reconfirmed, per ticket 09's
precedent, that the shared file-existence gate and frontend disabled
condition are sanctioned extensions rather than new scope creep.
