# 12 — Logs

**What to build:** An operator can view a deployed Compose's recent logs on demand.

**Blocked by:** 08

**Status:** closed

- [x] `ComposeRunner.Logs` is implemented for real (`docker compose logs --tail=200`) and added to the fake
- [x] The Logs tab (per `ComposeLogs.dc.html`) fetches and displays the last ~200 lines on demand, with a manual refresh button — no live streaming
- [x] Covered by HTTP-seam tests using the fake `ComposeRunner`

## Comments

Implemented the authenticated `GET .../composes/{composeID}/logs` endpoint.
It reuses the deployed-file gate shared by Stop, Restart, and Logs, calls the
existing `ComposeRunner.Logs` operation, and returns the raw output as
`{"logs":"..."}`. Runner failures return `502` with the existing error
response shape. The real `dockerexec` adapter already runs
`docker compose logs --no-color --tail=200`; the HTTP fake now supports
configurable log output and errors.

The Compose detail page now has exactly the General and Logs tabs. Logs are
fetched when the Logs tab is first activated or when the operator presses
Load logs/Refresh, rendered in a bounded monospace block, and guarded against
stale responses during Compose navigation. No polling or streaming was added.

HTTP-seam tests cover successful raw-log retrieval, deterministic runner
identity, undeployed validation without a runner call, and runner-error
propagation. Verified with `gofmt -l .`, `git diff --check`, `go build ./...`,
`go vet ./...`, `go test -race ./internal/...`, `pnpm --dir web typecheck`,
and `pnpm --dir web build` (the existing large-chunk warning remains).
Local two-axis review against `2053f1a` found no standards or spec findings
requiring changes. Real Docker/browser smoke testing is unavailable in this
sandbox because no Docker daemon is present.
