# Spec — Implement the redesign in `web/`

## Problem

The panel's UI was redesigned as a mock-only prototype (`web-design/`, API-free, with
`GUIDELINES.md` as its contract) and the port has not started. `web/src/main.tsx` is a single
1,608-line file: state-driven navigation with no router, no theme beyond antd's defaults (ADR-0004),
nested tabs where the design has an Environment `Segmented` plus `Composes | Variables | Settings`,
and no Compose screen of its own. Everything the prototype settled — the shell, the status
vocabulary, the Compose layout, the states — lives only in `web-design/` today.

## Decision

Implement the prototype's screens in `web/` in the design's own order (`GUIDELINES.md` §12), with
these choices settled 2026-09-24:

- **Routing**: react-router, with the design's paths as history URLs (`/projects/1/env/1/compose/1`).
  The prototype's hash router is a review artifact and §12 says not to port it. This needs two small
  Go changes: `webassets` serves the shell for a path that is not a real file, and `auth.publicPath`
  treats anything outside `/api/` as public (the shell is public today; the API stays authenticated).
- **New app tree, not a mutated monolith.** New files under `src/app/`, `src/pages/`,
  `src/components/`, `src/data/`; the panel keeps working on the old tree until the cutover, when
  `main.tsx` switches to the new root and the monolith is deleted.
- **Data layer**: the prototype's store shape (`web-design/src/data/store.tsx`) with its method
  bodies replaced by `fetch` calls lifted from the monolith's existing call sites. Components do not
  change; the store is the seam.
- **Tailwind is dropped** with the theme ticket. Its ~30 uses in the monolith are spacing utilities
  the design expresses with antd `Flex`/`Space` and `LAYOUT`, and no design screen uses it. That also
  removes the `@layer tailwind-base, antd` ordering the reset currently has to respect.
- **Deliberate omissions**, so nobody reads them as something the port forgot: the prototype strip,
  the latency switch, the hash router, the design-system screens (Guidelines, States gallery, which
  stay in `web-design/` as the reference), the Activity cards on Overview and Compose (the API
  records no events — §11 says v1 ships without them), the ⌘K palette, and dark mode (§12 step 9's
  optional list; ADR-0004 defers dark).
- **No feature is lost.** The port must ship everything the panel does today: setup/login/logout,
  Project CRUD, Environment CRUD, Variable CRUD with masking and reveal/copy, Compose CRUD, the
  editor, deploy/stop/restart/delete, and logs.

## Tickets

Nine, in order. 01–05 are the foundation (a screen only lands once the shell and the data layer it
depends on exist); 06–09 reshape the remaining screens. The monitoring screens are separate — see
`.scratch/host-monitoring/spec.md`, and they land after 04 and 07 here.

- `01-theme.md` — the theme and the reset, and tailwind's removal. (No blockers.)
- `02-primitives.md` — the shared components, and the frontend test setup. (No blockers.)
- `03-data-layer.md` — the API client and store. (No blockers.)
- `04-router-and-shell.md` — history routing (with the Go fallback) and the shell. (Blocked by 01, 02, 03.)
- `05-auth-screens.md` — setup and sign-in. (Blocked by 01, 02, 03, 04.)
- `06-project.md` — Project, reshaped. (Blocked by 01, 02, 03, 04.)
- `07-compose.md` — Compose as its own route, Editor tab. (Blocked by 01, 02, 03, 04, 06.)
- `08-variables.md` — variables, reshaped. (Blocked by 01, 02, 03, 04, 06.)
- `09-overview.md` — the Overview screen. (Blocked by 01, 02, 03, 04, 06.)

## Verification

Per ticket: `pnpm --dir web typecheck`, `pnpm --dir web build`, then the DOM-probe and screenshot
pass this project already uses (`web-design/scripts/screenshot.sh` for the prototype; the same headless
technique against the built app) — plus `go test ./...` whenever a ticket touches Go. The design's
states (loading, empty, error, dirty, degraded) are judged against `#/design/states` in
`web-design/`, which stays the reference implementation of them.
