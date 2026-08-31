# 11 — Delete

**What to build:** An operator can fully tear down a Compose — stopping and removing its containers, networks, and volumes, and deleting its files from disk.

**Blocked by:** 08

**Status:** closed

- [x] `dockerexec.Down` with volumes (`-v`) is implemented for real and added to the fake `ComposeRunner`
- [x] Clicking "Delete" (per `ComposeGeneral.dc.html`) prompts for confirmation, then removes containers/networks/volumes and deletes the Compose's row and its on-disk files
- [x] Covered by HTTP-seam tests using the fake `ComposeRunner`

## Comments

Implemented Compose deletion across the backend and frontend. The DELETE
endpoint uses the existing deterministic Compose path, invokes a new
`DownWithVolumes` runner operation for deployed Composes, removes the
Compose directory, and then deletes the database row. Undeployed Composes
skip Docker teardown but still remove any stale on-disk directory and row.
Runner failures preserve the row and files so the operator can retry.

The runner interface keeps `Down` volume-preserving for Stop and adds the
explicit `DownWithVolumes` operation for Delete; the ADR was updated to keep
that interface decision documented. The General tab now has the requested
Ant Design `Popconfirm` Delete action and returns to the Composes list only
after a successful response.

HTTP-seam tests cover deployed deletion, undeployed cleanup, runner-error
preservation, volume-removing runner calls, deterministic identity, file
removal, and row removal. Verified with `gofmt -l .`, `git diff --check`,
`go build ./...`, `go vet ./...`, `go test -race ./internal/...`,
`pnpm --dir web typecheck`, and `pnpm --dir web build`. Local two-axis review
against `65f7229` found no standards or spec findings requiring changes.
