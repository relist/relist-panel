# 08 — Deploy

**What to build:** An operator can deploy a Compose. The panel injects the shared Traefik network and this Environment's variables, then actually brings the containers up via `docker compose`. This ticket establishes the `ComposeRunner` port that tickets 09–12 all build on.

**Blocked by:** 06, 07

**Status:** closed

- [x] The `ComposeRunner` interface (`Up`/`Down`/`Restart`/`Status`/`Logs`) is declared in the `compose` package per ADR-0003; a fake implementation used by tests records what it was asked to do
- [x] The real `dockerexec` implementation shells out to the `docker compose` CLI over the mounted Docker socket
- [x] Before deploying, the panel parses the Compose's YAML and injects the shared network into every service (merging with any existing `networks:` block), per ADR-0002
- [x] The panel writes the resolved YAML and a generated `.env` file (from this Environment's Environment Variables) to `<data dir>/<project>/<environment>/<compose>/`
- [x] Deploy runs under a deterministic Compose project name (`<project-slug>-<environment-slug>-<compose-slug>`) via `docker compose -p`
- [x] Clicking "Deploy" (per `ComposeGeneral.dc.html`) runs `docker compose up -d --build` for real and the UI reflects success
- [x] The status badge queries real container state ("N/M running") on page load and via a manual refresh button — no background polling
- [x] Covered by HTTP-seam tests using the fake `ComposeRunner`, per the spec's Testing Decisions

## Comments

Implemented via a new `internal/dockerexec` leaf package (the real
`ComposeRunner`, shelling out to `docker compose ... up/down/restart/ps/logs`)
plus deploy orchestration added to `internal/composes` (`ComposeRunner`
interface, `RunRequest`/`ComposeStatus` types, `resolveCompose` YAML
transform, deterministic naming/paths, and a new `POST .../composes/{id}/deploy`
route). The fake `ComposeRunner` used by tests lives in
`internal/httpapi/httpapi_test.go` (package `httpapi_test`), matching this
repo's actual practice of putting every domain's HTTP-seam tests there rather
than in a per-package `compose_test` file (no such file exists for any
domain in this repo).

Every Compose response (`create`/`get`/`list`/`update`/`deploy`) now embeds a
live `status: {running, total}`, computed via the runner only when a Compose
has actually been deployed (its resolved compose file exists on disk) so an
undeployed Compose never shells out; runner errors are swallowed to a
zero-value status rather than failing the request, since Docker being
briefly unreachable shouldn't make the whole panel unusable. The frontend
shows this as a "Stopped" / "N/M running" `Tag` on both the Composes list and
the Compose detail page, with a "Refresh" button next to it — no polling.
"Save & Deploy" replaces the prior primary "Save changes" action on the
detail page's General tab (matching the spec's own Out of Scope framing:
"deploys are manual via a 'Save & Deploy' action") and persists any pending
YAML edits before deploying, so the deployed artifact never lags the editor.

Two deliberate deviations from ADR-0002's literal wording, both needed for
correctness and called out here since a future reader may otherwise "fix"
them back to the letter of the ADR:

- ADR-0002 says Environment Variables are "not injected into the YAML text
  itself," implying `docker compose --env-file` alone suffices. It does not:
  that flag only affects `${VAR}` interpolation while parsing the compose
  file, not what lands in a container's environment. To satisfy story 24
  ("services pick up shared config without me hardcoding it"), each
  service's `env_file:` is injected to point at the generated `.env`
  (0600 permissions; the compose file itself is 0644), alongside the network
  injection, in the same `resolveCompose` pass.
- The resolved file is produced via a full YAML parse/re-marshal rather than
  text-level patching, so it does not preserve the user's original
  formatting/comments byte-for-byte. This only affects the on-disk resolved
  copy used to run `docker compose`, never the YAML stored in the database or
  shown in the editor, so it doesn't affect story 19 ("edit ... and save").

Also worth noting for future tickets: `composes`'s repository resolves
Project/Environment names (for slugging) and lists an Environment's
variables via direct SQL against the `projects`/`environments`/
`environment_variables` tables, rather than exported calls into those
packages. This follows the same "duplicated parent-existence checks" pattern
already established by `composes`/`environmentvariables` (accepted in ticket
07's review) — it's a read extending that precedent, not new territory,
whereas `projects` calling `environments.ProvisionDefault` is a
transactional *write* that genuinely needs an exported API.

HTTP-seam tests (`TestAuthenticatedAdminCanDeployCompose`,
`TestComposeDeployValidatesInputAndPropagatesRunnerErrors`) cover a full
deploy (asserting the deterministic project name, the resolved YAML's
network merge, the generated `.env` contents, and that a subsequent GET
reflects live status without redeploying), empty/invalid YAML rejection,
missing-compose 404, and a failing runner surfacing as 502. Verified with
`pnpm --dir web typecheck`, `pnpm --dir web build`, `go build ./...`, `go vet
./...`, `go test -race ./...`. Also ran a full browser smoke test against the
built binary (no Docker available in this environment): create a Project +
Environment Variable + Compose with a pasted YAML that already declared its
own network, clicked "Save & Deploy", and confirmed the resolved
`docker-compose.yml`/`.env` written to disk were correct (shared network
merged with the user's own, `env_file` injected, variable present) and that
the resulting "exec: docker: not found" failure surfaced as an inline error
without losing the rest of the page — a real bug (a stale `error || !compose`
guard from ticket 07 that blanked the whole detail view on any action
error, not just a failed initial load) found and fixed during this smoke
test.

`/code-review` against `b9d100d` (two parallel sub-agents) found one
concrete, applied fix: `addToNetworks`/`addToEnvFile` shared an
append-if-absent shape, extracted into `appendUnique`. It also raised the
two ADR-0002 deviations and the cross-domain SQL reads above, both addressed
by explanation rather than a code change per the reasoning documented above;
and noted (expected, not a violation) that `dockerexec`'s
`Down`/`Restart`/`Logs` methods exist to satisfy the `ComposeRunner`
interface but aren't yet wired to an HTTP route or seam-tested — that's
tickets 09/10/12's job.
