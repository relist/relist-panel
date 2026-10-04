# Spec — Deploy timestamps (the API learns when a Compose last deployed)

## Problem

The API records no deploy time, and the port worked around it client-side: the store stamps a
session-local `deployedAt` map on every deploy, and everything that wants "when did this last
deploy" reads that map — the Overview's "Last deployed" column (an em-dash until the session
deploys something), the shell's attention badge (stopped-after-success needs a date), and the
Monitor tab's deploy marker (a marker that vanishes on refresh is exactly the spike-vs-deploy
comparison the tab exists for). A restart forgets it all.

## Decision

Settled by grill, 2026-09-25:

- One column on the Compose row: `deployed_at`, `INTEGER` Unix seconds, nullable — null means
  never deployed. Set **only on a successful deploy**; stop and restart never touch it, so "has
  deployed" and "is running" stay two different facts (which is what the attention rule reads).
- The wire gains `lastDeployedAt` alongside `createdAt`/`updatedAt` (camelCase `time.Time`, the
  same serialization the Compose payload already uses).
- The frontend retires the `deployedAt` session map and every consumer of it: the API is the only
  source. The `withDeployStamps` helper (overview-view) collapses to nothing — tree values pass
  through unchanged.

Migration idiom per the repo: `00009_deploy_timestamps.sql`, 5-digit sequence, goose `Up`/`Down`,
and `internal/database/database_test.go`'s latest-version assertion bumps to 9.

## Tickets

Two, backend then frontend:

- `01-deployed-at.md` — the column, the deploy-success write, the wire field. (No blockers.)
- `02-surface-last-deployed.md` — the API value everywhere, the session map retired. (Blocked by 01.)

## Verification

Per ticket: `go test ./...` / `go vet ./...` / gofmt for 01; `pnpm --dir web typecheck`, `test`,
`build` for 02; the DOM-probe/file:// render technique the design-port tickets used for the
screens that change.
