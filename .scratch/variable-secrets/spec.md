# Spec — Variable secrets (a display tier, owned by the user)

## Problem

The Variables tab marks rows `secret`/`plain` by guessing from the key's shape
(`web/src/components/variables-view.ts`, `SECRET_KEY_PATTERN`). The guess is usually right and
sometimes wrong (`PASSWORD_HINT` gets masked; a genuinely sensitive `CONNECTION_STRING` does
not), and there is nowhere to record a user's correction — the API has no flag, so every render
re-guesses.

## Decision

Settled by grill, 2026-09-25:

- **A display tier, nothing more.** A secret-marked variable is masked on screen (reveal and
  copy work as today); its value is still stored and served by the API exactly as now. The panel
  has a single admin who already holds the Docker socket — hiding the value from that person is
  not a security boundary, and this flag does not pretend to be one. (The panel logs no variable
  values today; a pinning note belongs in the tests, not new machinery.)
- **The user owns the flag; the heuristic only suggests.** Create/edit forms carry a
  secret toggle that defaults to the existing key-shape heuristic's answer; the user can
  override it per variable.
- **One-time backfill.** The migration that adds the column marks existing rows by the same
  heuristic (SQL `LIKE` translation of `SECRET_KEY_PATTERN`), after which the guess is a
  confirmed value. The heuristic survives only as the create-form default — the tag itself
  always reads the flag.
- The `secret`/`plain` tag, masking, reveal and copy behave exactly as the screen does today
  (GUIDELINES §4.5); only the source of the marking changes.

Domain note: this creates the "distinct secret-handling tier" whose absence `CONTEXT.md` used to
justify avoiding the word — the glossary entry for Environment Variable is updated with this
spec, recording the tier as display-only.

## Tickets

Two, backend then frontend:

- `01-flag-and-api.md` — the column, the backfill, the wire. (No blockers.)
- `02-secret-toggle-ui.md` — the toggle and the flag-driven tag. (Blocked by 01.)

## Verification

Per ticket: `go test ./...` / `go vet ./...` / gofmt for 01; `pnpm --dir web typecheck`, `test`,
`build` for 02, plus the file:// render technique for the Variables tab states (masked, revealed,
toggle on/off in both modals, backfilled rows tagged).
