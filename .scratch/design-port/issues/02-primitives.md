# 02 — Primitives and the frontend test setup

**What to build:** Port the prototype's shared components into `web/src/components/`:
`primitives.tsx` (`PageHeader`, `StatCard`, `EmptyState`, `CodeValue`, `RelativeTime`,
`ComposeStatusTag`, `ComposeStatusInline`, `ServiceStateTag`, `HealthBadge`) and `DangerModal.tsx`
(type-the-name confirmation listing what is destroyed). Add vitest with a `test` script and cover the
pure helpers this port introduces.

**Blocked by: —**

**Status:** closed

- [x] Every component matches the prototype's props and markup, including `PageHeader`'s
      title/tags/description/right-actions/hairline contract (GUIDELINES §6)
- [x] `DangerModal` requires the object's name typed exactly and lists what disappears
- [x] vitest is a devDependency with a `test` script; `pnpm --dir web test` runs
- [x] Pure helpers have tests (unit formatting, relative time, status derivation) — no component tests
- [x] The old tree still compiles: components are additive until the screens switch in 06–09

## Comments

Implemented in this commit (`feat: port the design primitives and add the frontend test setup`).

- `primitives.tsx` and `DangerModal.tsx` are byte-identical ports of the prototype's files (verified
  with `diff`). `DangerModal` compares `typed.trim() === confirmWord`, resets on open, and renders the
  caller's `consequences` node above the typed-name input.
- The primitives import `composeState` from `../data/types`, so this ticket also lands
  `web/src/data/types.ts` (types + pure helpers only — the store ticket 03 adds is the remaining half
  of that ticket's scope). The prototype's monitoring/activity types (`MetricSample`, `ServiceSample`,
  `METRIC_WINDOWS`, `ActivityEntry`, `HostInfo`) are deliberately not ported: no endpoint returns
  them, and they belong to `.scratch/host-monitoring` / the omitted Activity cards. `Compose.lastAction`
  is dropped (nothing in the prototype's screens reads it); `lastDeployedAt` stays `@design-only` —
  screens already treat it as unknown. `dayjs` moves into `web/` dependencies for `RelativeTime`.
- vitest 5 as a devDependency, `"test": "vitest run"`; default node environment, no jsdom — pure
  functions only. 17 tests over `composeState` (the status vocabulary's truth table),
  `summarize`/`projectComposes`/`findCompose(Location)`, `toMonacoColor` (hex passthrough, short-hex
  expansion, rgba→8-digit hex — the formatting the Monaco bridge does), and the `LAYOUT` key contract.
  Relative time is dayjs's `fromNow` inside `RelativeTime` — library behaviour, not a port-introduced
  helper — so it has no unit test; no component tests, per the ticket.
- Verification: `pnpm --dir web typecheck`, `pnpm --dir web test` (17 passed) and
  `pnpm --dir web build` all green; the monolith is untouched and the components stay additive.
